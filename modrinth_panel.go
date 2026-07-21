// modrinth_panel.go: buscador e instalador de plugins/mods de Modrinth (R6),
// filtrado por loader y versión de la instancia seleccionada.
package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/JorMath/mc-tui-server/internal/assets"
	"github.com/JorMath/mc-tui-server/internal/config"
	"github.com/JorMath/mc-tui-server/internal/download"
	"github.com/JorMath/mc-tui-server/internal/modrinth"
	tui "github.com/grindlemire/go-tui"
)

// mrKindsFor devuelve los tipos de contenido de Modrinth instalables en la
// instancia: mods en los tipos con loader de mods (incluidas las creadas
// desde un modpack), plugins en Paper/Purpur y datapacks en todas — van al
// mundo, no al loader.
func mrKindsFor(t config.ServerType) []string {
	switch t {
	case config.Fabric, config.Forge, config.NeoForge, config.Quilt:
		return []string{"mods", "datapacks"}
	case config.Paper, config.Purpur:
		return []string{"plugins", "datapacks"}
	default:
		return []string{"datapacks"}
	}
}

func (a *app) mrOpenPanel() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	a.mrGen.Update(func(g int) int { return g + 1 })
	a.mrKind.Set(mrKindsFor(mgr.Instance().Type)[0])
	a.mrQuery.Set("")
	a.mrResults.Set([]modrinth.Project{})
	a.mrIdx.Set(0)
	a.mrBusy.Set(false)
	a.mrMsg.Set("")
	a.mrPending = nil
	a.mrUpd.Set(false)
	a.mrTyping.Set(true)
	a.mrOpen.Set(true)
}

// mrToggleKind rota el tipo de contenido (Tab). Si ya hay una búsqueda
// hecha, la repite en el tipo nuevo.
func (a *app) mrToggleKind() {
	mgr := a.current()
	if mgr == nil || a.mrBusy.Get() {
		return
	}
	kinds := mrKindsFor(mgr.Instance().Type)
	if len(kinds) < 2 {
		return
	}
	cur := a.mrKind.Get()
	next := kinds[0]
	for i, k := range kinds {
		if k == cur {
			next = kinds[(i+1)%len(kinds)]
			break
		}
	}
	a.mrKind.Set(next)
	a.mrResults.Set([]modrinth.Project{})
	a.mrIdx.Set(0)
	a.mrMsg.Set("")
	if !a.mrTyping.Get() && a.mrQuery.Get() != "" {
		a.mrSearch()
	}
}

func (a *app) mrClose() {
	a.mrGen.Update(func(g int) int { return g + 1 })
	a.mrOpen.Set(false)
}

func (a *app) mrSearch() {
	mgr := a.current()
	query := a.mrQuery.Get()
	if mgr == nil || a.mrBusy.Get() {
		return
	}
	// Enter con la búsqueda vacía sale del modo escritura: deja a mano
	// los atajos de lista como u (update all) sin exigir una búsqueda.
	if query == "" {
		a.mrTyping.Set(false)
		if a.mrKind.Get() != "datapacks" {
			a.mrMsg.Set("u checks for updates · / starts a search")
		}
		return
	}
	inst := mgr.Instance()
	kind := a.mrKind.Get()
	gen := a.mrGen.Get()
	a.mrBusy.Set(true)
	a.mrMsg.Set("Searching Modrinth...")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var results []modrinth.Project
		var err error
		if kind == "datapacks" {
			results, err = a.mr.SearchDatapacks(ctx, query, inst.Version)
		} else {
			results, err = a.mr.Search(ctx, query, inst.Type, inst.Version)
		}
		if a.mrGen.Get() != gen {
			return
		}
		a.mrBusy.Set(false)
		if err != nil {
			a.mrMsg.Set("Error: " + err.Error())
			return
		}
		a.mrResults.Set(results)
		a.mrIdx.Set(0)
		a.mrTyping.Set(false)
		if len(results) == 0 {
			a.mrMsg.Set(fmt.Sprintf("No %s found for %q compatible with %s %s", kind, query, inst.Type, inst.Version))
			return
		}
		a.mrMsg.Set(fmt.Sprintf("%d results · Enter installs into the selected instance", len(results)))
	}()
}

// datapacksDir devuelve la carpeta de datapacks (relativa a la instancia)
// del mundo activo. Minecraft la carga aunque se cree antes del primer
// arranque.
func datapacksDir(inst config.Instance) string {
	return filepath.Join(worldName(inst), "datapacks")
}

func (a *app) mrInstall() {
	mgr := a.current()
	results := a.mrResults.Get()
	idx := a.mrIdx.Get()
	if mgr == nil || a.mrBusy.Get() || idx < 0 || idx >= len(results) {
		return
	}
	inst := mgr.Instance()
	kind := a.mrKind.Get()
	project := results[idx]
	var sub string
	if kind == "datapacks" {
		sub = datapacksDir(inst)
	} else {
		var ok bool
		sub, ok = assets.PluginsDir(inst.Type)
		if !ok {
			return
		}
	}
	gen := a.mrGen.Get()
	a.mrBusy.Set(true)
	a.mrMsg.Set(fmt.Sprintf("Resolving %s...", project.Title))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		var file modrinth.File
		var deps []modrinth.Dependency
		var err error
		if kind == "datapacks" {
			file, err = a.mr.LatestDatapackFile(ctx, project.ID, inst.Version)
		} else {
			file, deps, err = a.mr.LatestFileWithDeps(ctx, project.ID, inst.Type, inst.Version)
		}
		if err == nil {
			dest := filepath.Join(inst.Dir, sub, file.Filename)
			err = download.DownloadFile(ctx, nil, file.URL, dest, func(done, total int64) {
				if a.mrGen.Get() != gen {
					return
				}
				if total > 0 {
					a.mrMsg.Set(fmt.Sprintf("Downloading %s... %d%%", file.Filename, done*100/total))
				}
			})
		}
		if a.mrGen.Get() != gen {
			return
		}
		if err != nil {
			a.mrBusy.Set(false)
			a.mrMsg.Set("Error: " + err.Error())
			return
		}
		installedDeps := 0
		if kind != "datapacks" {
			installedDeps = a.mrInstallDeps(ctx, gen, inst, sub, project.ID, deps)
		}
		if a.mrGen.Get() != gen {
			return
		}
		a.mrBusy.Set(false)
		suffix := ""
		if installedDeps > 0 {
			suffix = fmt.Sprintf(" (+%d dependencies)", installedDeps)
		}
		if kind == "datapacks" {
			a.mrMsg.Set(fmt.Sprintf("Installed %s into %s · run /reload or restart to load it", file.Filename, filepath.ToSlash(sub)+"/"))
		} else {
			a.mrMsg.Set(fmt.Sprintf("Installed %s%s into %s/ · restart the server to load it", file.Filename, suffix, sub))
		}
		a.appendLog(inst.Name, fmt.Sprintf("[mc-tui] Installed %s (%s)%s", project.Title, file.Filename, suffix))
	}()
}

// mrInstallDeps resuelve e instala recursivamente las dependencias
// required de un mod recién instalado (Fabric API, librerías...), saltando
// los proyectos solo-cliente y los archivos que ya están en la carpeta.
// Devuelve cuántas instaló. Corre dentro de la goroutine de mrInstall.
func (a *app) mrInstallDeps(ctx context.Context, gen int, inst config.Instance, sub, rootID string, deps []modrinth.Dependency) int {
	queue := []string{}
	visited := map[string]bool{rootID: true}
	enqueue := func(list []modrinth.Dependency) {
		for _, d := range list {
			if d.Type == "required" && d.ProjectID != "" && !visited[d.ProjectID] {
				visited[d.ProjectID] = true
				queue = append(queue, d.ProjectID)
			}
		}
	}
	enqueue(deps)
	installed := 0
	// Tope defensivo: ningún árbol de dependencias razonable pasa de 25.
	for len(queue) > 0 && len(visited) <= 25 {
		id := queue[0]
		queue = queue[1:]
		if a.mrGen.Get() != gen {
			return installed
		}
		if unsupported, err := a.mr.ServerUnsupported(ctx, []string{id}); err == nil && unsupported[id] {
			continue
		}
		file, subDeps, err := a.mr.LatestFileWithDeps(ctx, id, inst.Type, inst.Version)
		if err != nil {
			a.appendLog(inst.Name, "[mc-tui] Skipped a dependency: "+err.Error())
			continue
		}
		enqueue(subDeps)
		dest := filepath.Join(inst.Dir, sub, file.Filename)
		if _, err := os.Stat(dest); err == nil {
			continue // ya instalada
		}
		a.mrMsg.Set(fmt.Sprintf("Installing dependency %s...", file.Filename))
		if err := download.DownloadFile(ctx, nil, file.URL, dest, nil); err != nil {
			a.appendLog(inst.Name, fmt.Sprintf("[mc-tui] Dependency %s failed: %s", file.Filename, err))
			continue
		}
		a.appendLog(inst.Name, fmt.Sprintf("[mc-tui] Installed dependency %s", file.Filename))
		installed++
	}
	return installed
}

// sha1Of calcula el sha1 en hex de un archivo.
func sha1Of(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// pendingUpdate es una actualización encontrada, a la espera de confirmar.
type pendingUpdate struct {
	Old  string
	File modrinth.File
}

// mrUpdateAll (tecla u) compara los mods/plugins instalados contra
// Modrinth por hash y, si hay versiones nuevas, pide confirmación antes
// de descargar nada. Los archivos que Modrinth no reconoce se dejan igual.
func (a *app) mrUpdateAll() {
	mgr := a.current()
	if mgr == nil || a.mrBusy.Get() || a.mrKind.Get() == "datapacks" {
		return
	}
	inst := mgr.Instance()
	sub, ok := assets.PluginsDir(inst.Type)
	if !ok {
		return
	}
	gen := a.mrGen.Get()
	a.mrBusy.Set(true)
	a.mrMsg.Set("Checking installed files for updates...")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		dir := filepath.Join(inst.Dir, sub)
		names, err := assets.Plugins(inst.Dir, inst.Type)
		if err == nil && len(names) == 0 {
			err = fmt.Errorf("no files in %s/ to update", sub)
		}
		byHash := map[string]string{}
		var hashes []string
		if err == nil {
			for _, n := range names {
				h, hashErr := sha1Of(filepath.Join(dir, n))
				if hashErr != nil {
					err = hashErr
					break
				}
				byHash[h] = n
				hashes = append(hashes, h)
			}
		}
		var latest map[string]modrinth.File
		if err == nil {
			latest, err = a.mr.LatestByHash(ctx, inst.Type, inst.Version, hashes)
		}
		if a.mrGen.Get() != gen {
			return
		}
		a.mrBusy.Set(false)
		if err != nil {
			a.mrMsg.Set("Error: " + err.Error())
			return
		}
		var pending []pendingUpdate
		current, unknown := 0, 0
		for hash, oldName := range byHash {
			file, ok := latest[hash]
			switch {
			case !ok:
				unknown++
			case file.SHA1 == hash:
				current++
			default:
				pending = append(pending, pendingUpdate{Old: oldName, File: file})
			}
		}
		if len(pending) == 0 {
			a.mrMsg.Set(fmt.Sprintf("Everything up to date (%d current, %d not on Modrinth)", current, unknown))
			return
		}
		a.mrPending = pending
		a.mrUpd.Set(true)
		a.mrMsg.Set(fmt.Sprintf("%d updates available (%d current, %d not on Modrinth) — update now?",
			len(pending), current, unknown))
	}()
}

// mrApplyUpdates descarga las actualizaciones confirmadas y borra los
// archivos viejos.
func (a *app) mrApplyUpdates() {
	mgr := a.current()
	pending := a.mrPending
	a.mrPending = nil
	a.mrUpd.Set(false)
	if mgr == nil || len(pending) == 0 {
		return
	}
	inst := mgr.Instance()
	sub, ok := assets.PluginsDir(inst.Type)
	if !ok {
		return
	}
	gen := a.mrGen.Get()
	a.mrBusy.Set(true)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		dir := filepath.Join(inst.Dir, sub)
		updated := 0
		for _, u := range pending {
			a.mrMsg.Set(fmt.Sprintf("Updating %s → %s...", u.Old, u.File.Filename))
			if err := download.DownloadFile(ctx, nil, u.File.URL, filepath.Join(dir, u.File.Filename), nil); err != nil {
				a.appendLog(inst.Name, fmt.Sprintf("[mc-tui] Update of %s failed: %s", u.Old, err))
				continue
			}
			if u.File.Filename != u.Old {
				_ = os.Remove(filepath.Join(dir, u.Old))
			}
			a.appendLog(inst.Name, fmt.Sprintf("[mc-tui] Updated %s → %s", u.Old, u.File.Filename))
			updated++
		}
		if a.mrGen.Get() != gen {
			return
		}
		a.mrBusy.Set(false)
		a.mrMsg.Set(fmt.Sprintf("Updates done: %d of %d updated · restart the server to apply",
			updated, len(pending)))
	}()
}

// mrDownloadsText formatea el contador de descargas (9000 → 9.0k).
func mrDownloadsText(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return strconv.Itoa(n)
	}
}

func (a *app) mrItems() []listItem {
	results := a.mrResults.Get()
	limit := a.descLimit()
	lines := make([]string, len(results))
	for i, p := range results {
		desc := p.Description
		if r := []rune(desc); len(r) > limit {
			desc = string(r[:limit]) + "…"
		}
		lines[i] = fmt.Sprintf("%s (%s ⇩) — %s", p.Title, mrDownloadsText(p.Downloads), desc)
	}
	return fullItems(lines, a.mrIdx.Get())
}

func (a *app) mrMove(delta int) {
	n := len(a.mrResults.Get())
	if n == 0 {
		return
	}
	a.mrIdx.Update(func(i int) int {
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

// mrHasKinds indica si la instancia tiene más de un tipo de contenido y
// por tanto Tab alterna entre ellos.
func (a *app) mrHasKinds() bool {
	mgr := a.current()
	return mgr != nil && len(mrKindsFor(mgr.Instance().Type)) > 1
}

func (a *app) mrHints() []hint {
	if a.mrUpd.Get() {
		return []hint{{"y", "update all"}, {"n/Esc", "cancel"}}
	}
	var hints []hint
	if a.mrTyping.Get() {
		hints = []hint{{"Enter", "search"}}
	} else {
		hints = []hint{{"↑/↓", "select"}, {"Enter", "install"}, {"/", "new search"}}
		if a.mrKind.Get() != "datapacks" {
			hints = append(hints, hint{"u", "update all"})
		}
	}
	if a.mrHasKinds() {
		hints = append(hints, hint{"Tab", "switch type"})
	}
	return append(hints, hint{"Esc", "close"})
}

func (a *app) mrKeyMap() tui.KeyMap {
	if a.mrUpd.Get() {
		cancel := func(ke tui.KeyEvent) {
			a.mrPending = nil
			a.mrUpd.Set(false)
			a.mrMsg.Set("Update cancelled")
		}
		return tui.KeyMap{
			tui.OnStop(tui.Rune('y'), func(ke tui.KeyEvent) { a.mrApplyUpdates() }),
			tui.OnStop(tui.Rune('n'), cancel),
			tui.OnStop(tui.KeyEscape, cancel),
		}
	}
	esc := tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.mrClose() })
	tab := tui.OnStop(tui.KeyTab, func(ke tui.KeyEvent) { a.mrToggleKind() })
	if a.mrTyping.Get() {
		return tui.KeyMap{
			tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {
				a.mrQuery.Update(func(s string) string { return s + string(ke.Rune) })
			}),
			tui.OnStop(tui.KeyBackspace, func(ke tui.KeyEvent) {
				a.mrQuery.Update(func(s string) string {
					r := []rune(s)
					if len(r) == 0 {
						return s
					}
					return string(r[:len(r)-1])
				})
			}),
			tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.mrSearch() }),
			tab,
			esc,
		}
	}
	return tui.KeyMap{
		tui.OnStop(tui.KeyUp, func(ke tui.KeyEvent) { a.mrMove(-1) }),
		tui.OnStop(tui.KeyDown, func(ke tui.KeyEvent) { a.mrMove(1) }),
		tui.OnStop(tui.KeyPageUp, func(ke tui.KeyEvent) { a.mrMove(-10) }),
		tui.OnStop(tui.KeyPageDown, func(ke tui.KeyEvent) { a.mrMove(10) }),
		tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.mrInstall() }),
		tui.OnStop(tui.Rune('/'), func(ke tui.KeyEvent) { a.mrTyping.Set(true) }),
		tui.OnStop(tui.Rune('u'), func(ke tui.KeyEvent) { a.mrUpdateAll() }),
		tab,
		esc,
	}
}
