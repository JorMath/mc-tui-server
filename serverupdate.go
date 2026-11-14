// serverupdate.go: actualización del software del servidor (F1) — la acción
// del menú es contextual: un modpack sigue yendo a packupdate.go y una
// instancia jar-based (vanilla/paper/purpur/fabric) abre el panel de
// versiones, baja el jar nuevo a staging, valida la firma y rota el anterior
// a un .bak.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/JorMath/mc-tui-server/internal/backup"
	"github.com/JorMath/mc-tui-server/internal/config"
	"github.com/JorMath/mc-tui-server/internal/download"
	"github.com/JorMath/mc-tui-server/internal/server"
	"github.com/JorMath/mc-tui-server/internal/serverjar"
	tui "github.com/grindlemire/go-tui"
)

// jarUpdatable indica si el tipo de servidor se actualiza rotando un jar
// único. Los loaders con installer (forge/neoforge/quilt) necesitan
// reinstalar el runtime y quedan fuera de F1.
func jarUpdatable(t config.ServerType) bool {
	switch t {
	case config.Vanilla, config.Paper, config.Purpur, config.Fabric:
		return true
	}
	return false
}

// updateAsk enruta según el tipo de instancia: modpack al flujo existente,
// jar-based al panel nuevo, installer-based a un aviso.
func (a *app) updateAsk() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	inst, ok := a.store.Get(mgr.Instance().Name)
	if !ok {
		return
	}
	if inst.PackID != "" {
		a.pkUpdateAsk()
		return
	}
	if !jarUpdatable(inst.Type) {
		a.appendLog(inst.Name, "[mc-tui] Updating loader installers (forge/neoforge/quilt) is not supported yet; recreate the instance or update its runtime manually")
		return
	}
	if st := mgr.Status(); st == server.Running || st == server.Stopping {
		a.appendLog(inst.Name, "[mc-tui] Stop the server before updating the server software")
		return
	}
	a.suType = inst.Type
	a.suCurrent = inst.Version
	a.suTarget = ""
	a.suVersions.Set([]string{})
	a.suIdx.Set(0)
	a.suConfirm.Set("")
	a.suRestart.Set(false)
	a.suOpen.Set(true)
	a.suBusy.Set(true)
	a.suMsg.Set("Checking for updates...")
	gen := a.suGen.Get() + 1
	a.suGen.Set(gen)
	name := inst.Name
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	a.suCancel = cancel
	go func() {
		defer cancel()
		p, err := a.newProvider(inst.Type)
		if err != nil {
			a.suFinish(gen, "Error: "+err.Error())
			return
		}
		versions, err := p.Versions(ctx)
		if err != nil {
			a.suFinish(gen, "Error: "+err.Error())
			return
		}
		if a.suGen.Get() != gen {
			return
		}
		a.suVersions.Set(versions)
		a.suIdx.Set(0)
		a.suBusy.Set(false)
		switch latest, ok := serverjar.Latest(versions, inst.Version); {
		case len(versions) == 0:
			a.suMsg.Set("The provider returned no versions")
		case !ok:
			a.suMsg.Set(fmt.Sprintf("Already on the newest version (%s)", inst.Version))
		case inst.Version == "":
			a.suMsg.Set(fmt.Sprintf("Newest version: %s (installed version unknown)", latest))
		default:
			a.suMsg.Set(fmt.Sprintf("Update available: %s → %s", inst.Version, latest))
		}
		a.appendLog(name, "[mc-tui] "+a.suMsg.Get())
	}()
}

// suFinish publica el desenlace de una goroutine solo si el panel sigue
// siendo el mismo (suGen no cambió).
func (a *app) suFinish(gen int, msg string) {
	if a.suGen.Get() != gen {
		return
	}
	a.suBusy.Set(false)
	a.suMsg.Set(msg)
}

// suMove desplaza la selección de versión dentro de la lista.
func (a *app) suMove(delta int) {
	n := len(a.suVersions.Get())
	if n == 0 {
		return
	}
	a.suIdx.Update(func(i int) int {
		i += delta
		if i < 0 {
			i = 0
		}
		if i >= n {
			i = n - 1
		}
		return i
	})
}

// suAskApply pide confirmación antes de tocar el jar. Un downgrade avisa
// explícitamente porque el mundo puede no abrir, pero no se bloquea.
func (a *app) suAskApply() {
	versions := a.suVersions.Get()
	i := a.suIdx.Get()
	if i < 0 || i >= len(versions) {
		return
	}
	version := versions[i]
	if version == a.suCurrent {
		a.suMsg.Set(fmt.Sprintf("Already on %s — pick another version", version))
		return
	}
	a.suTarget = version
	warn := ""
	if a.suCurrent != "" && download.CompareVersions(version, a.suCurrent) < 0 {
		warn = " This is a downgrade: the world may not open."
	}
	a.suConfirm.Set(fmt.Sprintf("Update %s from %s to %s?%s The world is backed up first.",
		a.currentName(), a.suCurrent, version, warn))
}

// suKeyMap captura todas las teclas del panel de actualización.
func (a *app) suKeyMap() tui.KeyMap {
	if a.suRestart.Get() {
		return tui.KeyMap{
			tui.OnStop(tui.Rune('y'), func(ke tui.KeyEvent) { a.suRestartNow() }),
			tui.OnStop(tui.Rune('n'), func(ke tui.KeyEvent) { a.suClose() }),
			tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.suClose() }),
		}
	}
	if a.suConfirm.Get() != "" {
		return tui.KeyMap{
			tui.OnStop(tui.Rune('y'), func(ke tui.KeyEvent) { a.suApply() }),
			tui.OnStop(tui.Rune('n'), func(ke tui.KeyEvent) { a.suConfirm.Set("") }),
			tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.suConfirm.Set("") }),
		}
	}
	if a.suBusy.Get() {
		return tui.KeyMap{
			tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.suClose() }),
			tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {}),
		}
	}
	return tui.KeyMap{
		tui.OnStop(tui.KeyUp, func(ke tui.KeyEvent) { a.suMove(-1) }),
		tui.OnStop(tui.KeyDown, func(ke tui.KeyEvent) { a.suMove(1) }),
		tui.OnStop(tui.KeyPageUp, func(ke tui.KeyEvent) { a.suMove(-10) }),
		tui.OnStop(tui.KeyPageDown, func(ke tui.KeyEvent) { a.suMove(10) }),
		tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.suAskApply() }),
		tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.suClose() }),
	}
}

// suApply ejecuta la actualización confirmada en segundo plano: backup del
// mundo, descarga con staging, rotación del jar y persistencia.
func (a *app) suApply() {
	a.suConfirm.Set("")
	version := a.suTarget
	mgr := a.current()
	if mgr == nil || version == "" {
		return
	}
	inst, ok := a.store.Get(mgr.Instance().Name)
	if !ok {
		return
	}
	if st := mgr.Status(); st == server.Running || st == server.Stopping {
		a.appendLog(inst.Name, "[mc-tui] Stop the server before updating the server software")
		return
	}
	oldVersion := inst.Version
	name := inst.Name
	gen := a.suGen.Get() + 1
	a.suGen.Set(gen)
	a.suBusy.Set(true)
	a.suMsg.Set(fmt.Sprintf("Updating to %s...", version))
	a.appendLog(name, fmt.Sprintf("[mc-tui] Updating server software %s → %s...", labelVersion(oldVersion), version))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	a.suCancel = cancel
	go func() {
		defer cancel()
		fail := func(err error) {
			a.suFinish(gen, "Update failed: "+err.Error())
			if a.suGen.Get() == gen {
				a.appendLog(name, "[mc-tui] Server update failed: "+err.Error())
			}
		}
		p, err := a.newProvider(inst.Type)
		if err != nil {
			fail(err)
			return
		}
		logf := func(msg string) {
			if a.suGen.Get() == gen {
				a.appendLog(name, "[mc-tui] "+msg)
			}
		}
		progress := func(done, total int64) {
			if total <= 0 || a.suGen.Get() != gen {
				return
			}
			a.suMsg.Set(fmt.Sprintf("Downloading %s... %d%%", version, done*100/total))
		}
		dl := func(url, dest string, p func(done, total int64)) error {
			return download.DownloadFile(ctx, nil, url, dest, p)
		}
		bak, err := applyServerUpdate(ctx, inst, version, p, dl, progress, logf)
		if err != nil {
			fail(err)
			return
		}
		inst.Version = version
		if err := a.store.Update(inst); err != nil {
			fail(err)
			return
		}
		if err := a.store.Save(); err != nil {
			fail(err)
			return
		}
		if err := mgr.SetInstance(inst); err != nil {
			fail(err)
			return
		}
		a.pruneBackups(inst)
		done := fmt.Sprintf("Server software updated to %s", version)
		if bak != "" {
			done += fmt.Sprintf(" (previous jar kept as %s)", filepath.Base(bak))
		}
		a.appendLog(name, "[mc-tui] "+done)
		if a.suGen.Get() != gen {
			return
		}
		a.suCurrent = version
		a.suBusy.Set(false)
		a.suMsg.Set(fmt.Sprintf("Updated to %s", version))
		a.suRestart.Set(true)
	}()
}

// applyServerUpdate respalda el mundo, resuelve la URL, descarga a staging,
// valida y rota el jar. Devuelve la ruta del jar anterior ("" si no había).
// Es síncrona a propósito: la UI la lanza en una goroutine y los tests la
// ejercitan sin red ni timers.
func applyServerUpdate(ctx context.Context, inst config.Instance, version string, p download.Provider,
	dl serverjar.DownloadFunc, progress func(done, total int64), logf func(string)) (string, error) {
	jarPath := serverjar.Path(inst)
	if jarPath == "" {
		return "", fmt.Errorf("the instance has no server jar path")
	}
	world := worldName(inst)
	worldDir := filepath.Join(inst.Dir, world)
	if _, err := os.Stat(worldDir); err == nil {
		bname := backup.Name(world, time.Now())
		if _, err := backup.Create(worldDir, filepath.Join(inst.Dir, backup.Dir, bname)); err != nil {
			return "", fmt.Errorf("world backup: %w", err)
		}
		if logf != nil {
			logf(fmt.Sprintf("World backed up to %s/%s", backup.Dir, bname))
		}
	}
	url, err := p.ResolveJarURL(ctx, version)
	if err != nil {
		return "", fmt.Errorf("resolving the download URL: %w", err)
	}
	staged, err := serverjar.Stage(jarPath, url, dl, progress)
	if err != nil {
		return "", err
	}
	bak, err := serverjar.Commit(jarPath, staged, inst.Version)
	if err != nil {
		serverjar.Discard(staged)
		return "", err
	}
	return bak, nil
}

// suRestartNow cierra el panel y arranca la instancia recién actualizada.
func (a *app) suRestartNow() {
	a.suClose()
	a.startSelected()
}

// suClose cierra el panel e invalida cualquier goroutine en curso.
func (a *app) suClose() {
	if a.suCancel != nil {
		a.suCancel()
		a.suCancel = nil
	}
	a.suGen.Set(a.suGen.Get() + 1)
	a.suOpen.Set(false)
	a.suVersions.Set(nil)
	a.suIdx.Set(0)
	a.suBusy.Set(false)
	a.suMsg.Set("")
	a.suConfirm.Set("")
	a.suRestart.Set(false)
	a.suTarget = ""
}

// suTypeLabel muestra el tipo de servidor tal como lo usa el provider.
func (a *app) suTypeLabel() string {
	if a.suType == "" {
		return "unknown"
	}
	return string(a.suType)
}

// suCurrentLabel muestra la versión instalada o un aviso si no se conoce.
func (a *app) suCurrentLabel() string {
	if a.suCurrent == "" {
		return "unknown"
	}
	return a.suCurrent
}

// suTag marca la versión instalada en la lista.
func (a *app) suTag(version string) string {
	if version == a.suCurrent {
		return " · installed"
	}
	return ""
}

// suHints son los atajos del panel de actualización.
func (a *app) suHints() []hint {
	return []hint{
		{"↑/↓", "select"}, {"PgUp/PgDn", "page"},
		{"Enter", "update"}, {"Esc", "close"},
	}
}

// labelVersion evita un "→ 1.21.8" huérfano cuando la versión vieja no se
// conoce (import o instancias anteriores al tracking).
func labelVersion(version string) string {
	if version == "" {
		return "unknown"
	}
	return version
}
