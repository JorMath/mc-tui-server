// java.go: gestión del runtime de Java (F2). El modal por instancia se abre
// desde el menú de acciones (Enter) y permite elegir el Java del sistema, un
// Temurin gestionado que la app descarga una sola vez por major, o una ruta
// manual.
package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/JorMath/mc-tui-server/internal/javacheck"
	"github.com/JorMath/mc-tui-server/internal/javaenv"
	"github.com/JorMath/mc-tui-server/internal/server"
	tui "github.com/grindlemire/go-tui"
)

// javaAsk abre el modal de Java para la instancia seleccionada.
// Cambiar el runtime exige el servidor detenido, igual que SetInstance.
func (a *app) javaAsk() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	inst, ok := a.store.Get(mgr.Instance().Name)
	if !ok {
		return
	}
	if st := mgr.Status(); st == server.Running || st == server.Stopping {
		a.appendLog(inst.Name, "[mc-tui] Stop the server before changing its Java runtime")
		return
	}
	a.javaTarget = inst.Name
	a.javaVersion = inst.Version
	a.javaIdx.Set(0)
	a.javaPath.Set("")
	switch inst.JavaMode {
	case "managed":
		a.javaIdx.Set(1)
	case "manual":
		a.javaIdx.Set(2)
		a.javaPath.Set(inst.JavaPath)
	}
	a.javaTyping.Set(false)
	a.javaMsg.Set("")
	a.javaBusy.Set(false)
	a.javaSysLabel.Set("checking...")
	a.javaManLabel.Set("checking...")
	a.javaOpen.Set(true)

	gen := a.javaGen.Get() + 1
	a.javaGen.Set(gen)
	need := javacheck.Required(inst.Version)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		sys := "not found"
		if major, err := javaenv.Detect(ctx, ""); err == nil {
			sys = fmt.Sprintf("Java %d", major)
		}
		man := "not needed"
		if need > 0 {
			if rt, ok := javaenv.Installed(a.dataDir, need); ok {
				man = fmt.Sprintf("installed (%s)", rt.Path)
			} else {
				man = "not installed yet"
			}
		}
		if a.javaGen.Get() != gen {
			return
		}
		a.javaSysLabel.Set(sys)
		a.javaManLabel.Set(man)
	}()
}

// javaNeed devuelve la major que exige la instancia (0 si no se conoce).
func (a *app) javaNeed() int {
	mgr := a.current()
	if mgr != nil && mgr.Instance().Name == a.javaTarget {
		return javacheck.Required(mgr.Instance().Version)
	}
	return javacheck.Required(a.javaVersion)
}

// javaItems arma las tres opciones del modal con su estado detectado.
func (a *app) javaItems() []listItem {
	idx := a.javaIdx.Get()
	need := a.javaNeed()
	managed := "unknown Minecraft version"
	if need > 0 {
		managed = fmt.Sprintf("Temurin JRE %d — %s", need, a.javaManLabel.Get())
	}
	manual := a.javaPath.Get()
	if manual == "" {
		manual = "not set"
	}
	return []listItem{
		{Text: fmt.Sprintf("System Java on PATH — %s", a.javaSysLabel.Get()), Sel: idx == 0},
		{Text: "Managed " + managed, Sel: idx == 1},
		{Text: fmt.Sprintf("Manual path — %s", manual), Sel: idx == 2},
	}
}

func (a *app) javaMove(delta int) {
	a.javaIdx.Update(func(i int) int {
		i += delta
		if i < 0 {
			i = 0
		}
		if i > 2 {
			i = 2
		}
		return i
	})
}

// javaKeyMap captura todas las teclas del modal.
func (a *app) javaKeyMap() tui.KeyMap {
	if a.javaBusy.Get() {
		return tui.KeyMap{
			tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.javaCancelRun() }),
			tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {}),
		}
	}
	if a.javaTyping.Get() {
		return tui.KeyMap{
			tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {
				if ke.Rune >= ' ' {
					a.javaPath.Update(func(s string) string { return s + string(ke.Rune) })
				}
			}),
			tui.OnStop(tui.KeyBackspace, func(ke tui.KeyEvent) {
				a.javaPath.Update(func(s string) string {
					r := []rune(s)
					if len(r) == 0 {
						return s
					}
					return string(r[:len(r)-1])
				})
			}),
			tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.javaApplyManual() }),
			tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) {
				a.javaTyping.Set(false)
				a.javaMsg.Set("")
			}),
		}
	}
	return tui.KeyMap{
		tui.OnStop(tui.KeyUp, func(ke tui.KeyEvent) { a.javaMove(-1) }),
		tui.OnStop(tui.KeyDown, func(ke tui.KeyEvent) { a.javaMove(1) }),
		tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.javaChoose() }),
		tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.javaClose() }),
	}
}

// javaChoose ejecuta la opción seleccionada.
func (a *app) javaChoose() {
	switch a.javaIdx.Get() {
	case 0:
		a.javaSet("system", "")
	case 1:
		a.javaManaged()
	case 2:
		a.javaTyping.Set(true)
		a.javaMsg.Set("")
	}
}

// javaManaged reutiliza el runtime gestionado o lo descarga en segundo plano.
func (a *app) javaManaged() {
	need := a.javaNeed()
	if need == 0 {
		a.javaMsg.Set("The Minecraft version is unknown — enter a Java path manually")
		return
	}
	if rt, ok := javaenv.Installed(a.dataDir, need); ok {
		a.javaSet("managed", rt.Path)
		return
	}
	gen := a.javaGen.Get()
	ctx, cancel := context.WithCancel(context.Background())
	a.javaCancel = cancel
	a.javaBusy.Set(true)
	a.javaMsg.Set(fmt.Sprintf("Downloading Temurin JRE %d...", need))
	go func() {
		defer cancel()
		rt, err := javaenv.Ensure(ctx, need, a.dataDir, func(done, total int64) {
			if a.javaGen.Get() != gen || total <= 0 {
				return
			}
			a.javaMsg.Set(fmt.Sprintf("Downloading Java %d... %d%% (%dMB of %dMB)",
				need, done*100/total, done/(1024*1024), total/(1024*1024)))
		})
		if a.javaGen.Get() != gen {
			return
		}
		if err != nil {
			a.javaBusy.Set(false)
			a.javaMsg.Set("Error: " + err.Error())
			return
		}
		a.javaBusy.Set(false)
		a.javaSet("managed", rt.Path)
	}()
}

// javaApplyManual valida la ruta escrita y la guarda como modo manual.
func (a *app) javaApplyManual() {
	p := strings.TrimSpace(a.javaPath.Get())
	if p == "" {
		a.javaMsg.Set("Type the full path to java or java.exe")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	major, err := javaenv.Detect(ctx, p)
	if err != nil {
		a.javaMsg.Set("Error: " + err.Error())
		return
	}
	if need := a.javaNeed(); need > 0 && major < need {
		a.javaMsg.Set(fmt.Sprintf("That Java is version %d, but the server needs %d+", major, need))
		return
	}
	a.javaTyping.Set(false)
	a.javaMsg.Set("")
	a.javaSet("manual", p)
}

// javaSet persiste el modo/ruta y actualiza el Manager de la instancia.
func (a *app) javaSet(mode, path string) {
	inst, ok := a.store.Get(a.javaTarget)
	if !ok {
		a.javaClose()
		return
	}
	inst.JavaMode = mode
	inst.JavaPath = path
	mgr := a.managerByName(inst.Name)
	if mgr == nil {
		a.javaMsg.Set("Error: the instance manager is gone")
		return
	}
	if st := mgr.Status(); st == server.Running || st == server.Stopping {
		a.javaMsg.Set("Stop the server before changing its Java runtime")
		return
	}
	if err := a.store.Update(inst); err != nil {
		a.javaMsg.Set("Error: " + err.Error())
		return
	}
	if err := a.store.Save(); err != nil {
		a.javaMsg.Set("Error: " + err.Error())
		return
	}
	if err := mgr.SetInstance(inst); err != nil {
		a.javaMsg.Set("Error: " + err.Error())
		return
	}
	a.appendLog(inst.Name, "[mc-tui] Java runtime: "+describeJava(mode, path))
	a.javaClose()
}

// describeJava resume el modo elegido para el log de la instancia.
func describeJava(mode, path string) string {
	switch mode {
	case "managed":
		return "managed Temurin JRE (" + path + ")"
	case "manual":
		return "manual path (" + path + ")"
	default:
		return "the system Java on PATH"
	}
}

// javaCancelRun aborta una descarga en curso y deja el modal abierto para
// elegir otra opción.
func (a *app) javaCancelRun() {
	if a.javaCancel != nil {
		a.javaCancel()
		a.javaCancel = nil
	}
	a.javaGen.Set(a.javaGen.Get() + 1)
	a.javaBusy.Set(false)
	a.javaMsg.Set("Download cancelled")
}

// javaClose cierra el modal e invalida sus goroutines.
func (a *app) javaClose() {
	a.javaGen.Set(a.javaGen.Get() + 1)
	if a.javaCancel != nil {
		a.javaCancel()
		a.javaCancel = nil
	}
	a.javaOpen.Set(false)
	a.javaTyping.Set(false)
	a.javaBusy.Set(false)
	a.javaMsg.Set("")
	a.javaPath.Set("")
	a.javaTarget = ""
	a.javaVersion = ""
}

// javaVersionLabel muestra la versión de Minecraft de la instancia abierta.
func (a *app) javaVersionLabel() string {
	if a.javaVersion == "" {
		return "unknown"
	}
	return a.javaVersion
}

// javaNeedLabel muestra la major de Java que exige esa versión.
func (a *app) javaNeedLabel() string {
	if need := a.javaNeed(); need > 0 {
		return fmt.Sprintf("Java %d+", need)
	}
	return "unknown"
}

// javaCurrentLabel resume el runtime que la instancia tiene configurado hoy.
func (a *app) javaCurrentLabel() string {
	inst, ok := a.store.Get(a.javaTarget)
	if !ok {
		return "unknown"
	}
	switch inst.JavaMode {
	case "managed":
		return "managed Temurin JRE"
	case "manual":
		if inst.JavaPath == "" {
			return "manual (no path)"
		}
		return "manual (" + inst.JavaPath + ")"
	default:
		return "system Java on PATH"
	}
}

func (a *app) javaHints() []hint {
	if a.javaTyping.Get() {
		return []hint{{"type or paste", "java path"}, {"Enter", "validate"}, {"Esc", "back"}}
	}
	return []hint{{"↑/↓", "choose"}, {"Enter", "apply"}, {"Esc", "close"}}
}

// managerByName busca el Manager de una instancia por nombre.
func (a *app) managerByName(name string) *server.Manager {
	for _, m := range a.managers.Get() {
		if m.Instance().Name == name {
			return m
		}
	}
	return nil
}
