// Package server gestiona el ciclo de vida del proceso de un servidor
// Minecraft local (R1): iniciar, detener, reiniciar, enviar comandos y
// exponer el log en tiempo real a través de un canal.
package server

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"mc-tui-server/internal/config"
)

// Status es el estado del proceso del servidor.
type Status string

const (
	Stopped  Status = "detenido"
	Running  Status = "corriendo"
	Stopping Status = "deteniendo"
)

// logBuffer es la capacidad del canal de logs; si el consumidor no lee,
// las líneas nuevas se descartan para no bloquear al lector del proceso.
const logBuffer = 1024

// CommandFunc construye el exec.Cmd que lanza el servidor. Inyectable
// para poder testear sin un java real.
type CommandFunc func(inst config.Instance) *exec.Cmd

// JavaCommand es la CommandFunc por defecto: java -Xms/-Xmx -jar server.jar nogui
// ejecutado dentro del directorio de la instancia.
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
	args = append(args, "-jar", inst.JarPath, "nogui")
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
func (m *Manager) Instance() config.Instance { return m.inst }

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
	if m.status == Stopped {
		return 0
	}
	return m.cmd.Process.Pid
}

// Start lanza el proceso del servidor y comienza a leer sus logs.
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status != Stopped {
		return fmt.Errorf("el servidor %q ya está %s", m.inst.Name, m.status)
	}

	cmd := m.newCmd(m.inst)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("abriendo stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("abriendo stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("abriendo stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		return fmt.Errorf("iniciando servidor %q: %w", m.inst.Name, err)
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
		_ = cmd.Wait()
		m.mu.Lock()
		m.status = Stopped
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
		return fmt.Errorf("el servidor %q no está corriendo", m.inst.Name)
	}
	m.status = Stopping
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
		return fmt.Errorf("el servidor %q no está corriendo", m.inst.Name)
	}
	if _, err := io.WriteString(m.stdin, command+"\n"); err != nil {
		return fmt.Errorf("enviando comando: %w", err)
	}
	return nil
}
