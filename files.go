// files.go: panel de archivos (R3) — editor de server.properties y
// gestión de mundos y plugins/mods con confirmación de borrado.
package main

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JorMath/mc-tui-server/internal/assets"
	"github.com/JorMath/mc-tui-server/internal/backup"
	"github.com/JorMath/mc-tui-server/internal/config"
	"github.com/JorMath/mc-tui-server/internal/properties"
	"github.com/JorMath/mc-tui-server/internal/server"
	tui "github.com/grindlemire/go-tui"
)

const fmTabs = 5

// fmTabNames son las pestañas del panel de archivos, en orden.
var fmTabNames = []string{"Properties", "Worlds", "Plugins/Mods", "Backups", "Logs"}

// fmLogsList lee los archivos de logs/ de la instancia: latest.log primero
// y el resto de más nuevo a más viejo.
func fmLogsList(instDir string) []string {
	entries, err := os.ReadDir(filepath.Join(instDir, "logs"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (!strings.HasSuffix(name, ".log") && !strings.HasSuffix(name, ".log.gz")) {
			continue
		}
		out = append(out, name)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	for i, n := range out {
		if n == "latest.log" {
			out = append(out[:i], out[i+1:]...)
			out = append([]string{"latest.log"}, out...)
			break
		}
	}
	return out
}

// readLogFile lee un log (descomprimiendo .gz) y devuelve sus últimas
// maxLogLines líneas.
func readLogFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("decompressing %s: %w", path, err)
		}
		defer gz.Close()
		r = gz
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > maxLogLines {
			lines = lines[1:]
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

func (a *app) fmOpenPanel() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	inst := mgr.Instance()
	a.fmTab.Set(0)
	a.fmPropsIdx.Set(0)
	a.fmWorldIdx.Set(0)
	a.fmPluginIdx.Set(0)
	a.fmBackupIdx.Set(0)
	a.fmLogIdx.Set(0)
	a.fmLogView.Set(false)
	a.fmEditing.Set(false)
	a.fmDirty.Set(false)
	a.fmConfirm.Set("")
	a.fmRestore.Set("")
	a.fmMsg.Set("")

	props, err := properties.Load(filepath.Join(inst.Dir, "server.properties"))
	if err != nil {
		a.fmMsg.Set("Error: " + err.Error())
		props = &properties.File{}
	}
	a.fmProps = props
	a.fmReloadLists(inst)
	a.fmRev.Update(func(r int) int { return r + 1 })
	a.fmOpen.Set(true)
}

func (a *app) fmReloadLists(inst config.Instance) {
	worlds, err := assets.Worlds(inst.Dir)
	if err != nil {
		a.fmMsg.Set("Error: " + err.Error())
	}
	a.fmWorlds.Set(worlds)
	plugins, err := assets.Plugins(inst.Dir, inst.Type)
	if err != nil {
		a.fmMsg.Set("Error: " + err.Error())
	}
	a.fmPlugins.Set(plugins)
	backups, err := backup.List(inst.Dir)
	if err != nil {
		a.fmMsg.Set("Error: " + err.Error())
	}
	a.fmBackups.Set(backups)
	a.fmLogs.Set(fmLogsList(inst.Dir))
}

func (a *app) fmClose() {
	a.fmOpen.Set(false)
}

func (a *app) fmPropLines() []string {
	keys := a.fmProps.Keys()
	out := make([]string, len(keys))
	for i, k := range keys {
		v, _ := a.fmProps.Get(k)
		out[i] = k + "=" + v
	}
	return out
}

func (a *app) fmItems() []listItem {
	switch a.fmTab.Get() {
	case 0:
		return fullItems(a.fmPropLines(), a.fmPropsIdx.Get())
	case 1:
		return fullItems(a.fmWorlds.Get(), a.fmWorldIdx.Get())
	case 2:
		return fullItems(a.fmPlugins.Get(), a.fmPluginIdx.Get())
	case 3:
		return fullItems(a.fmBackups.Get(), a.fmBackupIdx.Get())
	default:
		if a.fmLogView.Get() {
			// Contenido del log abierto, sin selección.
			return fullItems(a.fmLogLines.Get(), -1)
		}
		return fullItems(a.fmLogs.Get(), a.fmLogIdx.Get())
	}
}

// fmScrollY sigue la selección de la pestaña activa.
func (a *app) fmScrollY() int {
	switch a.fmTab.Get() {
	case 0:
		return scrollTo(a.fmPropsIdx.Get())
	case 1:
		return scrollTo(a.fmWorldIdx.Get())
	case 2:
		return scrollTo(a.fmPluginIdx.Get())
	case 3:
		return scrollTo(a.fmBackupIdx.Get())
	default:
		if a.fmLogView.Get() {
			return a.fmLogScroll.Get()
		}
		return scrollTo(a.fmLogIdx.Get())
	}
}

func (a *app) fmMove(delta int) {
	move := func(st *tui.State[int], n int) {
		if n == 0 {
			return
		}
		st.Update(func(i int) int {
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
	switch a.fmTab.Get() {
	case 0:
		move(a.fmPropsIdx, len(a.fmProps.Keys()))
	case 1:
		move(a.fmWorldIdx, len(a.fmWorlds.Get()))
	case 2:
		move(a.fmPluginIdx, len(a.fmPlugins.Get()))
	case 3:
		move(a.fmBackupIdx, len(a.fmBackups.Get()))
	default:
		if a.fmLogView.Get() {
			move(a.fmLogScroll, len(a.fmLogLines.Get()))
			return
		}
		move(a.fmLogIdx, len(a.fmLogs.Get()))
	}
}

func (a *app) fmSelectedKey() string {
	keys := a.fmProps.Keys()
	i := a.fmPropsIdx.Get()
	if i < 0 || i >= len(keys) {
		return ""
	}
	return keys[i]
}

func (a *app) fmBeginEdit() {
	key := a.fmSelectedKey()
	if key == "" {
		return
	}
	v, _ := a.fmProps.Get(key)
	a.fmEditText.Set(v)
	a.fmEditing.Set(true)
	a.fmMsg.Set("")
}

func (a *app) fmCommitEdit() {
	key := a.fmSelectedKey()
	a.fmProps.Set(key, a.fmEditText.Get())
	a.fmDirty.Set(true)
	a.fmEditing.Set(false)
	a.fmRev.Update(func(r int) int { return r + 1 })
	a.fmMsg.Set("Changed in memory; press w to save")
}

func (a *app) fmSave() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	path := filepath.Join(mgr.Instance().Dir, "server.properties")
	if err := a.fmProps.Save(path); err != nil {
		a.fmMsg.Set("Error: " + err.Error())
		return
	}
	a.fmDirty.Set(false)
	a.fmMsg.Set("Saved. Restart the server to apply the changes")
}

// fmAskDelete pide confirmación para borrar el mundo o plugin seleccionado.
func (a *app) fmAskDelete() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	if mgr.Status() != server.Stopped {
		a.fmMsg.Set("Stop the server before deleting files")
		return
	}
	var name string
	switch a.fmTab.Get() {
	case 1:
		if ws := a.fmWorlds.Get(); len(ws) > 0 {
			name = ws[a.fmWorldIdx.Get()]
		}
	case 2:
		if ps := a.fmPlugins.Get(); len(ps) > 0 {
			name = ps[a.fmPluginIdx.Get()]
		}
	case 3:
		if bs := a.fmBackups.Get(); len(bs) > 0 {
			name = bs[a.fmBackupIdx.Get()]
		}
	}
	if name == "" {
		return
	}
	a.fmConfirm.Set(name)
}

func (a *app) fmDoDelete() {
	mgr := a.current()
	name := a.fmConfirm.Get()
	a.fmConfirm.Set("")
	if mgr == nil || name == "" {
		return
	}
	inst := mgr.Instance()
	var err error
	switch a.fmTab.Get() {
	case 1:
		err = assets.DeleteWorld(inst.Dir, name)
	case 2:
		err = assets.DeletePlugin(inst.Dir, inst.Type, name)
	default:
		err = os.Remove(filepath.Join(inst.Dir, backup.Dir, name))
	}
	if err != nil {
		a.fmMsg.Set("Error: " + err.Error())
		return
	}
	a.fmMsg.Set(fmt.Sprintf("%q deleted", name))
	a.fmWorldIdx.Set(0)
	a.fmPluginIdx.Set(0)
	a.fmBackupIdx.Set(0)
	a.fmReloadLists(inst)
}

// fmAskRestore pide confirmación para restaurar el backup seleccionado.
func (a *app) fmAskRestore() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	if st := mgr.Status(); st == server.Running || st == server.Stopping {
		a.fmMsg.Set("Stop the server before restoring a backup")
		return
	}
	bs := a.fmBackups.Get()
	if len(bs) == 0 {
		return
	}
	name := bs[a.fmBackupIdx.Get()]
	if backup.WorldOf(name) == "" {
		a.fmMsg.Set("Cannot tell which world this backup belongs to")
		return
	}
	a.fmRestore.Set(name)
}

// fmDoRestore reemplaza el mundo con el contenido del backup confirmado.
func (a *app) fmDoRestore() {
	mgr := a.current()
	name := a.fmRestore.Get()
	a.fmRestore.Set("")
	if mgr == nil || name == "" {
		return
	}
	if st := mgr.Status(); st == server.Running || st == server.Stopping {
		a.fmMsg.Set("Stop the server before restoring a backup")
		return
	}
	inst := mgr.Instance()
	world := backup.WorldOf(name)
	err := backup.Restore(filepath.Join(inst.Dir, backup.Dir, name), filepath.Join(inst.Dir, world))
	if err != nil {
		a.fmMsg.Set("Error: " + err.Error())
		return
	}
	a.fmMsg.Set(fmt.Sprintf("World %q restored from %s", world, name))
	a.appendLog(inst.Name, fmt.Sprintf("[mc-tui] World %q restored from backup %s", world, name))
	a.fmReloadLists(inst)
}

// backupWorld (tecla b en la vista principal) comprime el mundo activo en
// backups/ de la instancia. Exige el servidor detenido: Minecraft escribe
// el mundo constantemente mientras corre.
func (a *app) backupWorld() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	inst := mgr.Instance()
	if st := mgr.Status(); st == server.Running || st == server.Stopping {
		a.appendLog(inst.Name, "[mc-tui] Stop the server before creating a backup")
		return
	}
	world := worldName(inst)
	worldDir := filepath.Join(inst.Dir, world)
	if _, err := os.Stat(worldDir); err != nil {
		a.appendLog(inst.Name, fmt.Sprintf("[mc-tui] No world %q to back up yet", world))
		return
	}
	name := backup.Name(world, time.Now())
	a.appendLog(inst.Name, fmt.Sprintf("[mc-tui] Creating backup %s...", name))
	go func() {
		dest := filepath.Join(inst.Dir, backup.Dir, name)
		if _, err := backup.Create(worldDir, dest); err != nil {
			a.appendLog(inst.Name, "[mc-tui] Backup failed: "+err.Error())
			return
		}
		size := int64(0)
		if info, err := os.Stat(dest); err == nil {
			size = info.Size()
		}
		a.appendLog(inst.Name, fmt.Sprintf("[mc-tui] Backup created: %s/%s (%dMB)",
			backup.Dir, name, size/(1024*1024)))
		a.pruneBackups(inst)
	}()
}

func (a *app) fmTitle() string {
	_ = a.fmRev.Get() // dependencia explícita: re-render tras mutar fmProps
	title := fmt.Sprintf("Files — %s · %s", a.currentName(), a.fmTabName())
	if a.fmDirty.Get() {
		title += " (unsaved)"
	}
	return title
}

func (a *app) fmTabName() string {
	tab := a.fmTab.Get()
	if tab == 4 && a.fmLogView.Get() {
		logs := a.fmLogs.Get()
		if i := a.fmLogIdx.Get(); i >= 0 && i < len(logs) {
			return "Logs · " + logs[i]
		}
	}
	return fmTabNames[tab]
}

// fmOpenLog carga el log seleccionado en el visor.
func (a *app) fmOpenLog() {
	mgr := a.current()
	logs := a.fmLogs.Get()
	idx := a.fmLogIdx.Get()
	if mgr == nil || idx < 0 || idx >= len(logs) {
		return
	}
	lines, err := readLogFile(filepath.Join(mgr.Instance().Dir, "logs", logs[idx]))
	if err != nil {
		a.fmMsg.Set("Error: " + err.Error())
		return
	}
	if len(lines) == 0 {
		lines = []string{"(empty)"}
	}
	a.fmLogLines.Set(lines)
	a.fmLogScroll.Set(0)
	a.fmLogView.Set(true)
	a.fmMsg.Set("")
}

func (a *app) fmHints() []hint {
	if a.fmEditing.Get() {
		return []hint{{"Enter", "apply"}, {"Esc", "cancel"}}
	}
	if a.fmConfirm.Get() != "" {
		return []hint{{"y", "delete"}, {"n/Esc", "keep"}}
	}
	if a.fmRestore.Get() != "" {
		return []hint{{"y", "restore"}, {"n/Esc", "cancel"}}
	}
	switch a.fmTab.Get() {
	case 0:
		return []hint{{"↑/↓", "select"}, {"Enter", "edit"}, {"w", "save"}, {"1-5 Tab", "switch tab"}, {"Esc", "close"}}
	case 3:
		return []hint{{"↑/↓", "select"}, {"Enter", "restore"}, {"d", "delete"}, {"1-5 Tab", "switch tab"}, {"Esc", "close"}}
	case 4:
		if a.fmLogView.Get() {
			return []hint{{"↑/↓ PgUp/PgDn", "scroll"}, {"Esc", "back to list"}}
		}
		return []hint{{"↑/↓", "select"}, {"Enter", "view"}, {"1-5 Tab", "switch tab"}, {"Esc", "close"}}
	default:
		return []hint{{"↑/↓", "select"}, {"d", "delete"}, {"1-5 Tab", "switch tab"}, {"Esc", "close"}}
	}
}

func (a *app) fmKeyMap() tui.KeyMap {
	if a.fmEditing.Get() {
		return tui.KeyMap{
			tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {
				a.fmEditText.Update(func(s string) string { return s + string(ke.Rune) })
			}),
			tui.OnStop(tui.KeyBackspace, func(ke tui.KeyEvent) {
				a.fmEditText.Update(func(s string) string {
					r := []rune(s)
					if len(r) == 0 {
						return s
					}
					return string(r[:len(r)-1])
				})
			}),
			tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.fmCommitEdit() }),
			tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.fmEditing.Set(false) }),
		}
	}
	if a.fmConfirm.Get() != "" {
		return tui.KeyMap{
			tui.OnStop(tui.Rune('y'), func(ke tui.KeyEvent) { a.fmDoDelete() }),
			tui.OnStop(tui.Rune('n'), func(ke tui.KeyEvent) { a.fmConfirm.Set("") }),
			tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.fmConfirm.Set("") }),
		}
	}
	if a.fmRestore.Get() != "" {
		return tui.KeyMap{
			tui.OnStop(tui.Rune('y'), func(ke tui.KeyEvent) { a.fmDoRestore() }),
			tui.OnStop(tui.Rune('n'), func(ke tui.KeyEvent) { a.fmRestore.Set("") }),
			tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.fmRestore.Set("") }),
		}
	}
	setTab := func(t int) {
		a.fmLogView.Set(false)
		a.fmTab.Set(t)
	}
	return tui.KeyMap{
		tui.OnStop(tui.Rune('1'), func(ke tui.KeyEvent) { setTab(0) }),
		tui.OnStop(tui.Rune('2'), func(ke tui.KeyEvent) { setTab(1) }),
		tui.OnStop(tui.Rune('3'), func(ke tui.KeyEvent) { setTab(2) }),
		tui.OnStop(tui.Rune('4'), func(ke tui.KeyEvent) { setTab(3) }),
		tui.OnStop(tui.Rune('5'), func(ke tui.KeyEvent) { setTab(4) }),
		tui.OnStop(tui.KeyTab, func(ke tui.KeyEvent) { setTab((a.fmTab.Get() + 1) % fmTabs) }),
		tui.OnStop(tui.KeyUp, func(ke tui.KeyEvent) { a.fmMove(-1) }),
		tui.OnStop(tui.KeyDown, func(ke tui.KeyEvent) { a.fmMove(1) }),
		tui.OnStop(tui.KeyPageUp, func(ke tui.KeyEvent) { a.fmMove(-10) }),
		tui.OnStop(tui.KeyPageDown, func(ke tui.KeyEvent) { a.fmMove(10) }),
		tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) {
			switch a.fmTab.Get() {
			case 0:
				a.fmBeginEdit()
			case 3:
				a.fmAskRestore()
			case 4:
				if !a.fmLogView.Get() {
					a.fmOpenLog()
				}
			}
		}),
		tui.OnStop(tui.Rune('w'), func(ke tui.KeyEvent) {
			if a.fmTab.Get() == 0 {
				a.fmSave()
			}
		}),
		tui.OnStop(tui.Rune('d'), func(ke tui.KeyEvent) {
			if t := a.fmTab.Get(); t != 0 && t != 4 {
				a.fmAskDelete()
			}
		}),
		tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) {
			if a.fmLogView.Get() {
				a.fmLogView.Set(false)
				return
			}
			a.fmClose()
		}),
	}
}
