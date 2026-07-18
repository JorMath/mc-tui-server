// manage.go: gestión de instancias (v0.1.1) — renombrar y eliminar con
// confirmación. Ambas operaciones exigen el servidor detenido.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/JorMath/mc-tui-server/internal/server"
	tui "github.com/grindlemire/go-tui"
)

func (a *app) renOpen() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	if mgr.Status() != server.Stopped {
		a.appendLog(mgr.Instance().Name, "[mc-tui] Stop the server before renaming")
		return
	}
	a.renText.Set(mgr.Instance().Name)
	a.renMsg.Set("")
	a.renActive.Set(true)
}

func (a *app) renClose() {
	a.renActive.Set(false)
	a.renText.Set("")
	a.renMsg.Set("")
}

// renCommit renombra la instancia: carpeta en disco (si sigue la convención
// servers/<nombre>), registro JSON, manager y mapas de UI con clave por nombre.
func (a *app) renCommit() {
	mgr := a.current()
	if mgr == nil {
		a.renClose()
		return
	}
	old := mgr.Instance().Name
	name := a.renText.Get()
	if name == old {
		a.renClose()
		return
	}
	if name == "" {
		a.renMsg.Set("The name cannot be empty")
		return
	}
	if _, exists := a.store.Get(name); exists {
		a.renMsg.Set(fmt.Sprintf("An instance named %q already exists", name))
		return
	}
	if mgr.Status() != server.Stopped {
		a.renMsg.Set("Stop the server before renaming")
		return
	}
	inst := mgr.Instance()
	if filepath.Base(inst.Dir) == old {
		newDir := filepath.Join(filepath.Dir(inst.Dir), name)
		if err := os.Rename(inst.Dir, newDir); err != nil {
			a.renMsg.Set("Error: " + err.Error())
			return
		}
		inst.Dir = newDir
	}
	inst.Name = name
	if err := a.store.Rename(old, name); err != nil {
		a.renMsg.Set("Error: " + err.Error())
		return
	}
	if err := a.store.Update(inst); err != nil {
		a.renMsg.Set("Error: " + err.Error())
		return
	}
	if err := a.store.Save(); err != nil {
		a.renMsg.Set("Error: " + err.Error())
		return
	}
	// El manager está detenido (chequeado arriba): SetInstance no falla.
	_ = mgr.SetInstance(inst)
	a.logs.Update(func(m map[string][]string) map[string][]string {
		m[name] = m[old]
		delete(m, old)
		return m
	})
	a.statuses.Update(func(m map[string]server.Status) map[string]server.Status {
		m[name] = server.Stopped
		delete(m, old)
		return m
	})
	a.appendLog(name, fmt.Sprintf("[mc-tui] Renamed %q to %q", old, name))
	a.renClose()
}

func (a *app) renKeyMap() tui.KeyMap {
	return tui.KeyMap{
		tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {
			if validNameChar(ke.Rune) {
				a.renText.Update(func(s string) string { return s + string(ke.Rune) })
			}
		}),
		tui.OnStop(tui.KeyBackspace, func(ke tui.KeyEvent) {
			a.renText.Update(func(s string) string {
				if len(s) == 0 {
					return s
				}
				return s[:len(s)-1]
			})
		}),
		tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.renCommit() }),
		tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.renClose() }),
	}
}

func (a *app) delAsk() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	if mgr.Status() != server.Stopped {
		a.appendLog(mgr.Instance().Name, "[mc-tui] Stop the server before deleting the instance")
		return
	}
	a.delTarget.Set(mgr.Instance().Name)
}

// delDo elimina la instancia confirmada: carpeta completa en disco,
// registro JSON, manager y mapas de UI.
func (a *app) delDo() {
	name := a.delTarget.Get()
	a.delTarget.Set("")
	mgr := a.current()
	if mgr == nil || name == "" || mgr.Instance().Name != name {
		return
	}
	if mgr.Status() != server.Stopped {
		a.appendLog(name, "[mc-tui] Stop the server before deleting the instance")
		return
	}
	inst := mgr.Instance()
	// La carpeta se borra solo si no apunta al propio directorio de datos
	// (un instances.json editado a mano podría hacerlo).
	if inst.Dir != "" && filepath.Clean(inst.Dir) != filepath.Clean(a.dataDir) {
		if err := os.RemoveAll(inst.Dir); err != nil {
			a.appendLog(name, "[mc-tui] Error: "+err.Error())
			return
		}
	}
	if err := a.store.Remove(name); err != nil {
		a.appendLog(name, "[mc-tui] Error: "+err.Error())
		return
	}
	if err := a.store.Save(); err != nil {
		a.appendLog(name, "[mc-tui] Error: "+err.Error())
		return
	}
	// Detenido (chequeado arriba): Close no falla y termina el pump de logs.
	_ = mgr.Close()
	a.managers.Update(func(ms []*server.Manager) []*server.Manager {
		for i, m := range ms {
			if m == mgr {
				return append(ms[:i], ms[i+1:]...)
			}
		}
		return ms
	})
	a.selected.Update(func(i int) int {
		n := len(a.managers.Get())
		if n == 0 {
			return 0
		}
		if i >= n {
			return n - 1
		}
		return i
	})
	a.logs.Update(func(m map[string][]string) map[string][]string {
		delete(m, name)
		return m
	})
	a.statuses.Update(func(m map[string]server.Status) map[string]server.Status {
		delete(m, name)
		return m
	})
}

func (a *app) delKeyMap() tui.KeyMap {
	return tui.KeyMap{
		tui.OnStop(tui.Rune('y'), func(ke tui.KeyEvent) { a.delDo() }),
		tui.OnStop(tui.Rune('n'), func(ke tui.KeyEvent) { a.delTarget.Set("") }),
		tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.delTarget.Set("") }),
	}
}
