// manage.go: gestión de instancias (v0.1.1) — renombrar y eliminar con
// confirmación. Ambas operaciones exigen el servidor detenido.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

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

// memOpen abre el editor de memoria (v0.1.2) con el valor actual. Exige el
// servidor detenido: el heap de la JVM se fija al arrancar, y SetInstance
// rechaza managers corriendo.
func (a *app) memOpen() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	if mgr.Status() != server.Stopped {
		a.appendLog(mgr.Instance().Name, "[mc-tui] Stop the server before changing its memory")
		return
	}
	a.memText.Set(strconv.Itoa(mgr.Instance().MemoryMB))
	a.memMsg.Set("")
	a.memActive.Set(true)
}

func (a *app) memClose() {
	a.memActive.Set(false)
	a.memText.Set("")
	a.memMsg.Set("")
}

// memCommit aplica la nueva memoria a la instancia: registro JSON y manager.
func (a *app) memCommit() {
	mgr := a.current()
	if mgr == nil {
		a.memClose()
		return
	}
	mb, err := strconv.Atoi(a.memText.Get())
	if err != nil || mb <= 0 {
		a.memMsg.Set("Enter a positive number of MB")
		return
	}
	if mgr.Status() != server.Stopped {
		a.memMsg.Set("Stop the server before changing its memory")
		return
	}
	inst := mgr.Instance()
	if mb == inst.MemoryMB {
		a.memClose()
		return
	}
	inst.MemoryMB = mb
	if err := a.store.Update(inst); err != nil {
		a.memMsg.Set("Error: " + err.Error())
		return
	}
	if err := a.store.Save(); err != nil {
		a.memMsg.Set("Error: " + err.Error())
		return
	}
	// El manager está detenido (chequeado arriba): SetInstance no falla.
	_ = mgr.SetInstance(inst)
	a.appendLog(inst.Name, fmt.Sprintf("[mc-tui] Memory set to %d MB (applies on next start)", mb))
	a.memClose()
}

func (a *app) memKeyMap() tui.KeyMap {
	return tui.KeyMap{
		tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {
			if ke.Rune >= '0' && ke.Rune <= '9' {
				a.memText.Update(func(s string) string { return s + string(ke.Rune) })
			}
		}),
		tui.OnStop(tui.KeyBackspace, func(ke tui.KeyEvent) {
			a.memText.Update(func(s string) string {
				if len(s) == 0 {
					return s
				}
				return s[:len(s)-1]
			})
		}),
		tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.memCommit() }),
		tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.memClose() }),
	}
}

// schOpen abre el editor de schedule (v0.3.0) con los valores actuales:
// paso 1 horas entre backups (vacío = off), paso 2 hora de restart diario
// HH:MM (vacío = off).
func (a *app) schOpen() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	inst, ok := a.store.Get(mgr.Instance().Name)
	if !ok {
		return
	}
	if inst.BackupHours > 0 {
		a.schBackup.Set(strconv.Itoa(inst.BackupHours))
	} else {
		a.schBackup.Set("")
	}
	a.schRestart.Set(inst.RestartTime)
	a.schStep.Set(0)
	a.schMsg.Set("")
	a.schActive.Set(true)
}

func (a *app) schClose() {
	a.schActive.Set(false)
	a.schMsg.Set("")
}

// schCommit valida y persiste el schedule de la instancia.
func (a *app) schCommit() {
	mgr := a.current()
	if mgr == nil {
		a.schClose()
		return
	}
	hours := 0
	if s := a.schBackup.Get(); s != "" {
		h, err := strconv.Atoi(s)
		if err != nil || h <= 0 {
			a.schStep.Set(0)
			a.schMsg.Set("Backup hours must be a positive number (empty = off)")
			return
		}
		hours = h
	}
	restart := a.schRestart.Get()
	if restart != "" {
		if _, err := time.Parse("15:04", restart); err != nil {
			a.schMsg.Set("Restart time must be HH:MM, e.g. 04:30 (empty = off)")
			return
		}
	}
	inst, ok := a.store.Get(mgr.Instance().Name)
	if !ok {
		a.schClose()
		return
	}
	inst.BackupHours = hours
	inst.RestartTime = restart
	if err := a.store.Update(inst); err != nil {
		a.schMsg.Set("Error: " + err.Error())
		return
	}
	if err := a.store.Save(); err != nil {
		a.schMsg.Set("Error: " + err.Error())
		return
	}
	_ = mgr.SetInstance(inst)
	switch {
	case hours > 0 && restart != "":
		a.appendLog(inst.Name, fmt.Sprintf("[mc-tui] Schedule: backup every %dh, daily restart at %s", hours, restart))
	case hours > 0:
		a.appendLog(inst.Name, fmt.Sprintf("[mc-tui] Schedule: backup every %dh", hours))
	case restart != "":
		a.appendLog(inst.Name, "[mc-tui] Schedule: daily restart at "+restart)
	default:
		a.appendLog(inst.Name, "[mc-tui] Schedule cleared")
	}
	a.schClose()
}

func (a *app) schKeyMap() tui.KeyMap {
	editing := a.schBackup
	valid := func(r rune) bool { return r >= '0' && r <= '9' }
	if a.schStep.Get() == 1 {
		editing = a.schRestart
		valid = func(r rune) bool { return (r >= '0' && r <= '9') || r == ':' }
	}
	return tui.KeyMap{
		tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {
			if valid(ke.Rune) {
				editing.Update(func(s string) string { return s + string(ke.Rune) })
			}
		}),
		tui.OnStop(tui.KeyBackspace, func(ke tui.KeyEvent) {
			editing.Update(func(s string) string {
				if len(s) == 0 {
					return s
				}
				return s[:len(s)-1]
			})
		}),
		tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) {
			if a.schStep.Get() == 0 {
				a.schMsg.Set("")
				a.schStep.Set(1)
				return
			}
			a.schCommit()
		}),
		tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.schClose() }),
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
