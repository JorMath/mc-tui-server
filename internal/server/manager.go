// Package server gestiona el ciclo de vida del proceso de un servidor
// Minecraft local (R1): iniciar, detener, reiniciar, enviar comandos y
// exponer el log en tiempo real a través de un canal.
package server

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/JorMath/mc-tui-server/internal/config"
)

// Status es el estado del proceso del servidor.
type Status string

const (
	Stopped  Status = "stopped"
	Running  Status = "running"
	Stopping Status = "stopping"
	// Crashed: el proceso terminó con error sin que nadie pidiera pararlo.
	Crashed Status = "crashed"
)

// logBuffer es la capacidad del canal de logs; si el consumidor no lee,
// las líneas nuevas se descartan para no bloquear al lector del proceso.
const logBuffer = 1024

// CommandFunc construye el exec.Cmd que lanza el servidor. Inyectable
// para poder testear sin un java real.
type CommandFunc func(inst config.Instance) *exec.Cmd

// argsFileFor devuelve el archivo de argumentos que generan los installers
// de Forge/NeoForge para el sistema dado.
func argsFileFor(goos string) string {
	if goos == "windows" {
		return "win_args.txt"
	}
	return "unix_args.txt"
}

// JavaCommand es la CommandFunc por defecto, ejecutada dentro del
// directorio de la instancia: java -Xms/-Xmx -jar server.jar nogui, o para
// Forge/NeoForge modernos (ArgsDir) java -Xms/-Xmx @<args>.txt nogui.
func JavaCommand(inst config.Instance) *exec.Cmd {
	java := inst.JavaPath
	if java == "" {
		java = "java"
	}
	var args []string
	if inst.MemoryMB > 0 {
		args = append(args,
			fmt.Sprintf("-Xms%dM", inst.MemoryMB),
			fmt.Sprintf("-Xmx%dM", inst.MemoryMB),
		)
	}
	args = append(args, inst.JavaArgs...)
	if inst.ArgsDir != "" {
		args = append(args, "@"+filepath.Join(inst.ArgsDir, argsFileFor(runtime.GOOS)), "nogui")
	} else {
		args = append(args, "-jar", inst.JarPath, "nogui")
	}
	cmd := exec.Command(java, args...)
	cmd.Dir = inst.Dir
	return cmd
}

// Option configura un Manager.
type Option func(*Manager)

// WithCommand reemplaza la CommandFunc por defecto.
func WithCommand(fn CommandFunc) Option {
	return func(m *Manager) { m.newCmd = fn }
}

// Manager controla una instancia de servidor: un proceso a la vez.
// El canal de Logs vive tanto como el Manager, así sobrevive a reinicios.
type Manager struct {
	inst   config.Instance
	newCmd CommandFunc
	logs   chan string

	mu     sync.Mutex
	status Status
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	exited chan struct{}
	closed bool
	// stopRequested distingue un stop pedido (Stop) de un crash: si el
	// proceso muere con error sin este flag, el estado queda en Crashed.
	stopRequested bool
}

// New crea un Manager detenido para la instancia dada.
func New(inst config.Instance, opts ...Option) *Manager {
	m := &Manager{
		inst:   inst,
		newCmd: JavaCommand,
		logs:   make(chan string, logBuffer),
		status: Stopped,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Instance devuelve la configuración de la instancia gestionada.
func (m *Manager) Instance() config.Instance {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inst
}

// notRunning indica si no hay proceso vivo (detenido o crasheado).
// El caller debe tener m.mu.
func (m *Manager) notRunning() bool {
	return m.status == Stopped || m.status == Crashed
}

// SetInstance reemplaza la configuración de la instancia (p.ej. tras un
// rename). Solo se permite con el servidor detenido para no desincronizar
// el proceso en marcha.
func (m *Manager) SetInstance(inst config.Instance) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.notRunning() {
		return fmt.Errorf("server %q is %s; stop it first", m.inst.Name, m.status)
	}
	m.inst = inst
	return nil
}

// Close cierra el canal de logs para que los consumidores terminen. Solo
// se permite con el servidor detenido (sin lectores escribiendo). Es
// idempotente; un Manager cerrado no debe reutilizarse.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.notRunning() {
		return fmt.Errorf("server %q is %s; stop it first", m.inst.Name, m.status)
	}
	if !m.closed {
		m.closed = true
		close(m.logs)
	}
	return nil
}

// Logs devuelve el canal por el que llegan las líneas de stdout/stderr.
func (m *Manager) Logs() <-chan string { return m.logs }

// Status devuelve el estado actual del proceso.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// PID devuelve el PID del proceso del servidor, o 0 si no está corriendo.
func (m *Manager) PID() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.notRunning() {
		return 0
	}
	return m.cmd.Process.Pid
}

// Start lanza el proceso del servidor y comienza a leer sus logs.
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.notRunning() {
		return fmt.Errorf("server %q is already %s", m.inst.Name, m.status)
	}
	m.stopRequested = false

	cmd := m.newCmd(m.inst)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("opening stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("opening stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("opening stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		return fmt.Errorf("starting server %q: %w", m.inst.Name, err)
	}

	m.cmd = cmd
	m.stdin = stdin
	m.status = Running
	exited := make(chan struct{})
	m.exited = exited

	var readers sync.WaitGroup
	readers.Add(2)
	go m.readLines(stdout, &readers)
	go m.readLines(stderr, &readers)
	go func() {
		// Los lectores deben terminar antes de Wait (contrato de os/exec).
		readers.Wait()
		err := cmd.Wait()
		m.mu.Lock()
		// Salida con error sin stop pedido = crash (un /stop desde el juego
		// o un Stop() nuestro terminan con código 0 o con stopRequested).
		if err != nil && !m.stopRequested {
			m.status = Crashed
		} else {
			m.status = Stopped
		}
		m.mu.Unlock()
		close(exited)
	}()
	return nil
}

// readLines copia líneas del pipe al canal de logs sin bloquear:
// si el buffer está lleno, la línea se descarta.
func (m *Manager) readLines(r io.Reader, wg *sync.WaitGroup) {
	defer wg.Done()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		select {
		case m.logs <- sc.Text():
		default:
		}
	}
}

// Stop detiene el servidor con gracia enviando "stop" por stdin; si no
// termina dentro del timeout, mata el proceso. Siempre espera a que el
// proceso haya salido antes de retornar.
func (m *Manager) Stop(timeout time.Duration) error {
	m.mu.Lock()
	if m.status != Running {
		m.mu.Unlock()
		return fmt.Errorf("server %q is not running", m.inst.Name)
	}
	m.status = Stopping
	m.stopRequested = true
	stdin, cmd, exited := m.stdin, m.cmd, m.exited
	m.mu.Unlock()

	// Si el proceso ya murió el write falla; el select de abajo resuelve igual.
	_, _ = io.WriteString(stdin, "stop\n")

	select {
	case <-exited:
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-exited
	}
	return nil
}

// Restart reinicia el servidor; si estaba detenido, simplemente lo inicia.
func (m *Manager) Restart(timeout time.Duration) error {
	if m.Status() == Running {
		// Stop solo falla si el proceso ya no corre; en ese caso Start igual procede.
		_ = m.Stop(timeout)
	}
	return m.Start()
}

// Send escribe un comando en el stdin del servidor (base de R2).
func (m *Manager) Send(command string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status != Running {
		return fmt.Errorf("server %q is not running", m.inst.Name)
	}
	if _, err := io.WriteString(m.stdin, command+"\n"); err != nil {
		return fmt.Errorf("sending command: %w", err)
	}
	return nil
}
