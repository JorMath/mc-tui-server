package server

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/JorMath/mc-tui-server/internal/config"
)

// TestHelperProcess no es un test real: es el proceso hijo que simula un
// servidor de Minecraft. Se invoca re-ejecutando el binario de test.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)

	fmt.Println("[Server] Hola desde el servidor falso")
	fmt.Fprintln(os.Stderr, "[Server] linea de stderr")

	switch os.Getenv("HELPER_MODE") {
	case "exit":
		// Termina inmediatamente por su cuenta.
		return
	case "crash":
		// Muere con código de error, como un servidor crasheado.
		os.Exit(3)
	case "stubborn":
		// Ignora "stop": hay que matarlo.
		time.Sleep(30 * time.Second)
		return
	default: // "obedient"
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "stop" {
				fmt.Println("[Server] Deteniendo...")
				return
			}
			fmt.Printf("cmd:%s\n", line)
		}
	}
}

// helperCommand devuelve una CommandFunc que lanza TestHelperProcess en el
// modo indicado, imitando al proceso java del servidor.
func helperCommand(mode string) CommandFunc {
	return func(inst config.Instance) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"HELPER_MODE="+mode,
		)
		return cmd
	}
}

func newTestManager(t *testing.T, mode string) *Manager {
	t.Helper()
	m := New(config.Instance{Name: "test"}, WithCommand(helperCommand(mode)))
	t.Cleanup(func() {
		if m.Status() == Running || m.Status() == Stopping {
			_ = m.Stop(2 * time.Second)
		}
	})
	return m
}

// waitStatus espera hasta que el manager llegue al estado dado.
func waitStatus(t *testing.T, m *Manager, want Status) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for m.Status() != want {
		if time.Now().After(deadline) {
			t.Fatalf("status = %s, esperaba %s", m.Status(), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitForLog espera hasta encontrar una línea que contenga want.
func waitForLog(t *testing.T, m *Manager, want string) {
	t.Helper()
	waitForLogs(t, m, want)
}

// waitForLogs espera hasta ver todas las líneas pedidas, en cualquier
// orden: stdout y stderr llegan por goroutines distintas y su orden no
// está garantizado.
func waitForLogs(t *testing.T, m *Manager, wants ...string) {
	t.Helper()
	pending := make(map[string]bool, len(wants))
	for _, w := range wants {
		pending[w] = true
	}
	deadline := time.After(5 * time.Second)
	for len(pending) > 0 {
		select {
		case line := <-m.Logs():
			for w := range pending {
				if strings.Contains(line, w) {
					delete(pending, w)
				}
			}
		case <-deadline:
			t.Fatalf("no llegaron líneas de log con %v", pending)
		}
	}
}

func waitForStatus(t *testing.T, m *Manager, want Status) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m.Status() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("status = %q, quiero %q", m.Status(), want)
}

func TestStartEmitsLogsAndRuns(t *testing.T) {
	m := newTestManager(t, "obedient")
	if got := m.Instance().Name; got != "test" {
		t.Fatalf("Instance().Name = %q, quiero %q", got, "test")
	}
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if m.Status() != Running {
		t.Fatalf("status = %q, quiero %q", m.Status(), Running)
	}
	waitForLogs(t, m, "Hola desde el servidor falso", "linea de stderr")
}

func TestPID(t *testing.T) {
	m := newTestManager(t, "obedient")
	if got := m.PID(); got != 0 {
		t.Fatalf("PID detenido = %d, quiero 0", got)
	}
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := m.PID(); got <= 0 {
		t.Fatalf("PID corriendo = %d, quiero > 0", got)
	}
	if err := m.Stop(5 * time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := m.PID(); got != 0 {
		t.Fatalf("PID tras Stop = %d, quiero 0", got)
	}
}

func TestStartWhileRunningFails(t *testing.T) {
	m := newTestManager(t, "obedient")
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Start(); err == nil {
		t.Fatal("Start con el servidor corriendo debe fallar")
	}
}

func TestStartInvalidCommandFails(t *testing.T) {
	m := New(config.Instance{Name: "test"}, WithCommand(func(inst config.Instance) *exec.Cmd {
		return exec.Command("programa-que-no-existe-xyz")
	}))
	if err := m.Start(); err == nil {
		t.Fatal("Start con comando inválido debe fallar")
	}
	if m.Status() != Stopped {
		t.Fatalf("status tras fallo = %q, quiero %q", m.Status(), Stopped)
	}
}

func TestGracefulStop(t *testing.T) {
	m := newTestManager(t, "obedient")
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForLog(t, m, "Hola")
	if err := m.Stop(5 * time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if m.Status() != Stopped {
		t.Fatalf("status = %q, quiero %q", m.Status(), Stopped)
	}
}

func TestStopKillsStubbornProcess(t *testing.T) {
	m := newTestManager(t, "stubborn")
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForLog(t, m, "Hola")
	start := time.Now()
	if err := m.Stop(500 * time.Millisecond); err != nil {
		t.Fatalf("Stop con kill: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Stop tardó demasiado: %v", elapsed)
	}
	if m.Status() != Stopped {
		t.Fatalf("status = %q, quiero %q", m.Status(), Stopped)
	}
}

func TestStopWhenNotRunningFails(t *testing.T) {
	m := newTestManager(t, "obedient")
	if err := m.Stop(time.Second); err == nil {
		t.Fatal("Stop sin servidor corriendo debe fallar")
	}
}

func TestSendCommandReachesStdin(t *testing.T) {
	m := newTestManager(t, "obedient")
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForLog(t, m, "Hola")
	if err := m.Send("say hola mundo"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitForLog(t, m, "cmd:say hola mundo")
}

func TestSendWhenNotRunningFails(t *testing.T) {
	m := newTestManager(t, "obedient")
	if err := m.Send("say hola"); err == nil {
		t.Fatal("Send sin servidor corriendo debe fallar")
	}
}

func TestSendFailsOnBrokenStdin(t *testing.T) {
	// stubborn no lee stdin ni termina solo: cerrar el pipe a mano fuerza
	// el error de escritura con el proceso aún corriendo.
	m := newTestManager(t, "stubborn")
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForLog(t, m, "Hola")
	m.mu.Lock()
	m.stdin.Close()
	m.mu.Unlock()
	if err := m.Send("say hola"); err == nil {
		t.Fatal("Send con stdin cerrado debe fallar")
	}
}

func TestProcessExitingOnItsOwnBecomesStopped(t *testing.T) {
	m := newTestManager(t, "exit")
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForStatus(t, m, Stopped)
}

func TestRestart(t *testing.T) {
	m := newTestManager(t, "obedient")
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForLog(t, m, "Hola")
	if err := m.Restart(5 * time.Second); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if m.Status() != Running {
		t.Fatalf("status tras Restart = %q, quiero %q", m.Status(), Running)
	}
	// El segundo proceso vuelve a saludar por el mismo canal de logs.
	waitForLog(t, m, "Hola desde el servidor falso")
}

func TestRestartFromStoppedJustStarts(t *testing.T) {
	m := newTestManager(t, "obedient")
	if err := m.Restart(time.Second); err != nil {
		t.Fatalf("Restart desde detenido: %v", err)
	}
	if m.Status() != Running {
		t.Fatalf("status = %q, quiero %q", m.Status(), Running)
	}
}

func TestStartFailsWhenPipesUnavailable(t *testing.T) {
	// Un cmd con Stdout ya asignado hace fallar StdoutPipe.
	m := New(config.Instance{Name: "test"}, WithCommand(func(inst config.Instance) *exec.Cmd {
		cmd := helperCommand("obedient")(inst)
		cmd.Stdout = os.Stdout
		return cmd
	}))
	if err := m.Start(); err == nil {
		t.Fatal("Start con StdoutPipe roto debe fallar")
	}

	m = New(config.Instance{Name: "test"}, WithCommand(func(inst config.Instance) *exec.Cmd {
		cmd := helperCommand("obedient")(inst)
		cmd.Stderr = os.Stderr
		return cmd
	}))
	if err := m.Start(); err == nil {
		t.Fatal("Start con StderrPipe roto debe fallar")
	}

	m = New(config.Instance{Name: "test"}, WithCommand(func(inst config.Instance) *exec.Cmd {
		cmd := helperCommand("obedient")(inst)
		cmd.Stdin = os.Stdin
		return cmd
	}))
	if err := m.Start(); err == nil {
		t.Fatal("Start con StdinPipe roto debe fallar")
	}
}

func TestJavaCommandDefaults(t *testing.T) {
	cmd := JavaCommand(config.Instance{
		Name:    "s",
		Dir:     `C:\srv`,
		JarPath: "server.jar",
	})
	if got := cmd.Args[0]; !strings.Contains(got, "java") {
		t.Fatalf("binario = %q, debe ser java por defecto", got)
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "-jar server.jar nogui") {
		t.Fatalf("args = %q, falta '-jar server.jar nogui'", joined)
	}
	if strings.Contains(joined, "-Xmx") {
		t.Fatalf("args = %q, no debe haber -Xmx sin MemoryMB", joined)
	}
	if cmd.Dir != `C:\srv` {
		t.Fatalf("Dir = %q, quiero C:\\srv", cmd.Dir)
	}
}

func TestJavaCommandWithMemoryAndArgs(t *testing.T) {
	cmd := JavaCommand(config.Instance{
		Name:     "s",
		Dir:      `C:\srv`,
		JarPath:  "paper.jar",
		JavaPath: `C:\jdk\bin\java.exe`,
		JavaArgs: []string{"-XX:+UseG1GC"},
		MemoryMB: 4096,
	})
	joined := strings.Join(cmd.Args, " ")
	for _, want := range []string{"-Xms4096M", "-Xmx4096M", "-XX:+UseG1GC", "-jar paper.jar nogui"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args = %q, falta %q", joined, want)
		}
	}
	if cmd.Args[0] != `C:\jdk\bin\java.exe` {
		t.Fatalf("binario = %q, quiero la ruta de java configurada", cmd.Args[0])
	}
}

func TestSetInstanceWhileStopped(t *testing.T) {
	m := newTestManager(t, "obedient")
	inst := m.Instance()
	inst.Name = "renombrado"
	inst.Dir = `C:\servers\renombrado`
	if err := m.SetInstance(inst); err != nil {
		t.Fatalf("SetInstance detenido: %v", err)
	}
	if got := m.Instance().Name; got != "renombrado" {
		t.Fatalf("Instance().Name = %q, quiero %q", got, "renombrado")
	}
}

func TestSetInstanceWhileRunningFails(t *testing.T) {
	m := newTestManager(t, "obedient")
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	inst := m.Instance()
	inst.Name = "renombrado"
	if err := m.SetInstance(inst); err == nil {
		t.Fatal("SetInstance con el servidor corriendo debe fallar")
	}
	if got := m.Instance().Name; got != "test" {
		t.Fatalf("Instance().Name = %q, no debe cambiar tras el fallo", got)
	}
}

func TestCloseEndsLogChannel(t *testing.T) {
	m := newTestManager(t, "obedient")
	if err := m.Close(); err != nil {
		t.Fatalf("Close detenido: %v", err)
	}
	if _, open := <-m.Logs(); open {
		t.Fatal("el canal de logs debe estar cerrado tras Close")
	}
	// Idempotente: un segundo Close no debe hacer panic.
	if err := m.Close(); err != nil {
		t.Fatalf("Close repetido: %v", err)
	}
}

func TestCloseWhileRunningFails(t *testing.T) {
	m := newTestManager(t, "obedient")
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Close(); err == nil {
		t.Fatal("Close con el servidor corriendo debe fallar")
	}
}

func TestLogChannelDoesNotBlockWhenFull(t *testing.T) {
	m := newTestManager(t, "obedient")
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Sin lectores, llenamos el buffer con más líneas que su capacidad;
	// el manager no debe bloquearse ni el Stop colgarse.
	for i := 0; i < logBuffer+100; i++ {
		if err := m.Send(fmt.Sprintf("linea %d", i)); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}
	if err := m.Stop(5 * time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestJavaCommandArgsDirMode(t *testing.T) {
	cmd := JavaCommand(config.Instance{
		Name:     "s",
		Dir:      `C:\srv`,
		ArgsDir:  filepath.Join("libraries", "net", "minecraftforge", "forge", "1.20.1-47.4.18"),
		MemoryMB: 4096,
	})
	joined := strings.Join(cmd.Args, " ")
	want := "@" + filepath.Join("libraries", "net", "minecraftforge", "forge", "1.20.1-47.4.18", argsFileFor(runtime.GOOS))
	if !strings.Contains(joined, want+" nogui") {
		t.Fatalf("args = %q, falta %q", joined, want+" nogui")
	}
	if strings.Contains(joined, "-jar") {
		t.Fatalf("args = %q, no debe usar -jar en modo ArgsDir", joined)
	}
}

func TestArgsFileFor(t *testing.T) {
	if got := argsFileFor("windows"); got != "win_args.txt" {
		t.Fatalf("windows = %q", got)
	}
	if got := argsFileFor("linux"); got != "unix_args.txt" {
		t.Fatalf("linux = %q", got)
	}
}

func TestCrashDetection(t *testing.T) {
	m := newTestManager(t, "crash")
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitStatus(t, m, Crashed)
	// Un manager crasheado puede volver a arrancarse.
	if err := m.Start(); err != nil {
		t.Fatalf("Start tras crash: %v", err)
	}
	waitStatus(t, m, Crashed)
}

func TestSelfExitZeroIsStopped(t *testing.T) {
	m := newTestManager(t, "exit")
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Salida limpia (código 0) sin stop pedido no es un crash.
	waitStatus(t, m, Stopped)
}

func TestStopIsNotCrash(t *testing.T) {
	m := newTestManager(t, "stubborn")
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// El kill por timeout termina el proceso con error, pero fue un stop
	// pedido: el estado final debe ser Stopped, no Crashed.
	if err := m.Stop(200 * time.Millisecond); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := m.Status(); got != Stopped {
		t.Fatalf("status tras Stop = %s, quiero stopped", got)
	}
}
