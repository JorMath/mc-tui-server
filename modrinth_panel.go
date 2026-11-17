// modrinth_panel.go: buscador e instalador de plugins/mods de Modrinth (R6),
// filtrado por loader y versión de la instancia seleccionada.
package main

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JorMath/mc-tui-server/internal/assets"
	"github.com/JorMath/mc-tui-server/internal/config"
	"github.com/JorMath/mc-tui-server/internal/download"
	"github.com/JorMath/mc-tui-server/internal/hangar"
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
	a.mrSource.Set(sourceAll)
	a.mrSnapshot.Set(false)
	a.mrQuery.Set("")
	a.mrResults.Set([]contentItem{})
	a.mrIdx.Set(0)
	a.mrBusy.Set(false)
	a.mrMsg.Set("")
	a.mrPending = nil
	a.mrUpd.Set(false)
	a.mrVerMode.Set(false)
	a.mrVers.Set([]contentVersion{})
	a.mrVerIdx.Set(0)
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
	if !a.mrHangarOK() {
		a.mrSource.Set(sourceAll)
	}
	a.mrResults.Set([]contentItem{})
	a.mrIdx.Set(0)
	a.mrVerMode.Set(false)
	a.mrMsg.Set("")
	if !a.mrTyping.Get() && a.mrQuery.Get() != "" {
		a.mrSearch()
	}
}

func (a *app) mrClose() {
	a.mrGen.Update(func(g int) int { return g + 1 })
	a.mrOpen.Set(false)
}

// mrHangarOK reports whether Hangar applies to the current instance and
// content kind. The source filter is only offered when it does.
func (a *app) mrHangarOK() bool {
	mgr := a.current()
	return mgr != nil && hangarAvailable(mgr.Instance().Type, a.mrKind.Get())
}

// mrSourceLabel is the source filter actually in effect. When Hangar does
// not apply the panel is Modrinth-only even if the state says "all".
func (a *app) mrSourceLabel() string {
	if !a.mrHangarOK() {
		return sourceModrinth
	}
	return a.mrSource.Get()
}

// mrToggleSource cycles the source filter (key f) and repeats the current
// search. It is ignored when Hangar does not apply.
func (a *app) mrToggleSource() {
	if a.mrBusy.Get() || !a.mrHangarOK() {
		return
	}
	next := nextSource(a.mrSource.Get(), true)
	a.mrSource.Set(next)
	a.mrResults.Set([]contentItem{})
	a.mrIdx.Set(0)
	a.mrVerMode.Set(false)
	if !a.mrTyping.Get() && a.mrQuery.Get() != "" {
		a.mrSearch()
		return
	}
	a.mrMsg.Set("Source: " + next)
}

// searchLabel is the source name used in progress messages.
func searchLabel(source string) string {
	switch source {
	case sourceModrinth:
		return "Modrinth"
	case sourceHangar:
		return "Hangar"
	default:
		return "Modrinth and Hangar"
	}
}

// searchResultMessage summarizes a finished search. mrCount and hgCount are
// -1 for a source that was not queried.
func searchResultMessage(total, mrCount, hgCount int, warning string) string {
	msg := fmt.Sprintf("%d results", total)
	switch {
	case warning != "":
		msg += " · " + warning
	case mrCount >= 0 && hgCount >= 0:
		msg += fmt.Sprintf(" (%d Modrinth, %d Hangar)", mrCount, hgCount)
	}
	return msg + " · Enter installs into the selected instance"
}

// searchContent queries the selected source(s). With source "all" and a
// plugin instance, Modrinth and Hangar are queried concurrently and merged.
// The search only fails when every queried source fails.
func (a *app) searchContent(ctx context.Context, query string, inst config.Instance, kind, source string) (items []contentItem, mrCount, hgCount int, warning string, err error) {
	platform, platformOK := hangar.Platform(inst.Type)
	runModrinth := source != sourceHangar
	runHangar := platformOK && kind == "plugins" && source != sourceModrinth
	mrCount, hgCount = -1, -1

	var mrItems, hgItems []contentItem
	var mrErr, hgErr error
	var wg sync.WaitGroup
	if runModrinth {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var results []modrinth.Project
			var searchErr error
			if kind == "datapacks" {
				results, searchErr = a.mr.SearchDatapacks(ctx, query, inst.Version)
			} else {
				results, searchErr = a.mr.Search(ctx, query, inst.Type, inst.Version)
			}
			if searchErr != nil {
				mrErr = searchErr
				return
			}
			mrItems = make([]contentItem, len(results))
			for i, p := range results {
				mrItems[i] = contentItemFromModrinth(p)
			}
		}()
	}
	if runHangar {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results, searchErr := a.hangar.Search(ctx, query, platform)
			if searchErr != nil {
				hgErr = searchErr
				return
			}
			hgItems = make([]contentItem, len(results))
			for i, p := range results {
				hgItems[i] = contentItemFromHangar(p)
			}
		}()
	}
	wg.Wait()

	switch {
	case !runHangar:
		if mrErr != nil {
			return nil, -1, -1, "", mrErr
		}
		return mrItems, len(mrItems), -1, "", nil
	case source == sourceHangar:
		if hgErr != nil {
			return nil, -1, -1, "", hgErr
		}
		return hgItems, -1, len(hgItems), "", nil
	case mrErr != nil && hgErr != nil:
		return nil, -1, -1, "", fmt.Errorf("Modrinth: %v; Hangar: %v", mrErr, hgErr)
	case mrErr != nil:
		return hgItems, -1, len(hgItems), "Modrinth search failed: " + mrErr.Error(), nil
	case hgErr != nil:
		return mrItems, len(mrItems), -1, "Hangar search failed: " + hgErr.Error(), nil
	}
	return mergeContentItems(mrItems, hgItems), len(mrItems), len(hgItems), "", nil
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
	source := a.mrSourceLabel()
	if source == sourceHangar && kind != "plugins" {
		a.mrTyping.Set(false)
		a.mrMsg.Set("Hangar only serves plugins — press f to switch source")
		return
	}
	gen := a.mrGen.Get()
	a.mrBusy.Set(true)
	a.mrMsg.Set("Searching " + searchLabel(source) + "...")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		items, mrCount, hgCount, warning, err := a.searchContent(ctx, query, inst, kind, source)
		if a.mrGen.Get() != gen {
			return
		}
		a.mrBusy.Set(false)
		if err != nil {
			a.mrMsg.Set("Error: " + err.Error())
			return
		}
		a.mrResults.Set(items)
		a.mrIdx.Set(0)
		a.mrTyping.Set(false)
		if len(items) == 0 {
			a.mrMsg.Set(fmt.Sprintf("No %s found for %q compatible with %s %s", kind, query, inst.Type, inst.Version))
			return
		}
		a.mrMsg.Set(searchResultMessage(len(items), mrCount, hgCount, warning))
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
	if project.Source == sourceHangar {
		a.mrInstallHangar(project)
		return
	}
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
		if err != nil {
			if a.mrGen.Get() == gen {
				a.mrBusy.Set(false)
				a.mrMsg.Set("Error: " + err.Error())
			}
			return
		}
		a.mrInstallFile(ctx, gen, inst, sub, kind, project.Title, project.ID, file, deps)
	}()
}

// externalMessage is the explicit hint shown for a Hangar version that is
// hosted outside Hangar. Nothing is downloaded or opened.
func externalMessage(v hangar.Version) string {
	target := v.ExternalURL
	if target == "" {
		target = v.DownloadURL
	}
	return "External download — open " + target
}

// mrInstallHangar resolves the latest Release version of a Hangar project
// and downloads it into the plugins directory, verifying its SHA-256.
func (a *app) mrInstallHangar(project contentItem) {
	mgr := a.current()
	if mgr == nil {
		return
	}
	inst := mgr.Instance()
	platform, ok := hangar.Platform(inst.Type)
	if !ok || a.mrKind.Get() != "plugins" {
		return
	}
	sub, ok := assets.PluginsDir(inst.Type)
	if !ok {
		return
	}
	gen := a.mrGen.Get()
	a.mrBusy.Set(true)
	a.mrMsg.Set(fmt.Sprintf("Resolving %s...", project.Title))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		vers, err := a.hangar.Versions(ctx, project.Owner, project.Slug, platform, inst.Version, "Release")
		if a.mrGen.Get() != gen {
			return
		}
		if err == nil && len(vers) == 0 {
			err = fmt.Errorf("no Release version of %s supports %s", project.Title, inst.Version)
		}
		if err != nil {
			a.mrBusy.Set(false)
			a.mrMsg.Set("Error: " + err.Error())
			return
		}
		v := vers[0]
		if v.External() {
			a.mrBusy.Set(false)
			a.mrMsg.Set(externalMessage(v))
			return
		}
		dest := filepath.Join(inst.Dir, sub, v.FileName)
		if err := a.hangar.Download(ctx, v, dest); err != nil {
			if a.mrGen.Get() != gen {
				return
			}
			a.mrBusy.Set(false)
			a.mrMsg.Set("Error: " + err.Error())
			return
		}
		if a.mrGen.Get() != gen {
			return
		}
		a.mrBusy.Set(false)
		a.mrMsg.Set(fmt.Sprintf("Installed %s into %s/ · restart the server to load it", v.FileName, sub))
		a.appendLog(inst.Name, fmt.Sprintf("[mc-tui] Installed %s (%s)", project.Title, v.FileName))
	}()
}

// mrInstallFile descarga un archivo concreto (y sus dependencias si es un
// mod/plugin) y reporta el resultado. Corre dentro de una goroutine con
// mrBusy activo y lo apaga al terminar.
func (a *app) mrInstallFile(ctx context.Context, gen int, inst config.Instance, sub, kind, title, projectID string, file modrinth.File, deps []modrinth.Dependency) {
	dest := filepath.Join(inst.Dir, sub, file.Filename)
	err := download.DownloadFile(ctx, nil, file.URL, dest, func(done, total int64) {
		if a.mrGen.Get() != gen {
			return
		}
		if total > 0 {
			a.mrMsg.Set(fmt.Sprintf("Downloading %s... %d%%", file.Filename, done*100/total))
		}
	})
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
		installedDeps = a.mrInstallDeps(ctx, gen, inst, sub, projectID, deps)
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
	a.appendLog(inst.Name, fmt.Sprintf("[mc-tui] Installed %s (%s)%s", title, file.Filename, suffix))
}

// mrOpenVersions (tecla v) lista las versiones compatibles del proyecto
// seleccionado para instalar una concreta en vez de la última.
func (a *app) mrOpenVersions() {
	mgr := a.current()
	results := a.mrResults.Get()
	idx := a.mrIdx.Get()
	if mgr == nil || a.mrBusy.Get() || a.mrKind.Get() == "datapacks" || idx < 0 || idx >= len(results) {
		return
	}
	inst := mgr.Instance()
	project := results[idx]
	if project.Source == sourceHangar {
		a.mrLoadHangarVersions(project, false)
		return
	}
	gen := a.mrGen.Get()
	a.mrBusy.Set(true)
	a.mrMsg.Set(fmt.Sprintf("Fetching versions of %s...", project.Title))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		vers, err := a.mr.ProjectVersions(ctx, project.ID, inst.Type, inst.Version)
		if a.mrGen.Get() != gen {
			return
		}
		a.mrBusy.Set(false)
		if err != nil {
			a.mrMsg.Set("Error: " + err.Error())
			return
		}
		out := make([]contentVersion, len(vers))
		for i, v := range vers {
			out[i] = contentVersionFromModrinth(v)
		}
		a.mrVers.Set(out)
		a.mrVerIdx.Set(0)
		a.mrVerMode.Set(true)
		a.mrMsg.Set(fmt.Sprintf("%d versions of %s · Enter installs the selected one", len(out), project.Title))
	}()
}

// mrLoadHangarVersions lists Hangar versions for the project on the
// instance's Minecraft version. keepIdx preserves the selection when the
// channel is toggled.
func (a *app) mrLoadHangarVersions(project contentItem, keepIdx bool) {
	mgr := a.current()
	if mgr == nil || a.mrBusy.Get() {
		return
	}
	inst := mgr.Instance()
	platform, ok := hangar.Platform(inst.Type)
	if !ok {
		return
	}
	channel := "Release"
	if a.mrSnapshot.Get() {
		channel = "Snapshot"
	}
	gen := a.mrGen.Get()
	prev := a.mrVerIdx.Get()
	a.mrBusy.Set(true)
	a.mrMsg.Set(fmt.Sprintf("Fetching %s versions of %s...", strings.ToLower(channel), project.Title))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		vers, err := a.hangar.Versions(ctx, project.Owner, project.Slug, platform, inst.Version, channel)
		if a.mrGen.Get() != gen {
			return
		}
		a.mrBusy.Set(false)
		if err != nil {
			a.mrMsg.Set("Error: " + err.Error())
			return
		}
		if len(vers) == 0 {
			a.mrVerMode.Set(false)
			a.mrMsg.Set(fmt.Sprintf("No %s versions of %s support %s", channel, project.Title, inst.Version))
			return
		}
		out := make([]contentVersion, len(vers))
		for i, v := range vers {
			out[i] = contentVersionFromHangar(v)
		}
		a.mrVers.Set(out)
		if keepIdx && prev < len(out) {
			a.mrVerIdx.Set(prev)
		} else {
			a.mrVerIdx.Set(0)
		}
		a.mrVerMode.Set(true)
		a.mrMsg.Set(fmt.Sprintf("%d %s versions of %s · Enter installs the selected one", len(out), channel, project.Title))
	}()
}

// mrToggleChannel switches Hangar versions between Release and Snapshot and
// refetches them. It is ignored for Modrinth rows.
func (a *app) mrToggleChannel() {
	results := a.mrResults.Get()
	idx := a.mrIdx.Get()
	if a.mrBusy.Get() || idx < 0 || idx >= len(results) {
		return
	}
	if results[idx].Source != sourceHangar {
		return
	}
	a.mrSnapshot.Update(func(b bool) bool { return !b })
	a.mrLoadHangarVersions(results[idx], true)
}

// mrInstallVersion instala la versión elegida en el selector.
func (a *app) mrInstallVersion() {
	mgr := a.current()
	results := a.mrResults.Get()
	vers := a.mrVers.Get()
	pIdx, vIdx := a.mrIdx.Get(), a.mrVerIdx.Get()
	if mgr == nil || a.mrBusy.Get() || pIdx < 0 || pIdx >= len(results) || vIdx < 0 || vIdx >= len(vers) {
		return
	}
	inst := mgr.Instance()
	project := results[pIdx]
	chosen := vers[vIdx]
	sub, ok := assets.PluginsDir(inst.Type)
	if !ok {
		return
	}
	if project.Source == sourceHangar {
		a.mrInstallHangarVersion(inst, sub, project, chosen)
		return
	}
	kind := a.mrKind.Get()
	gen := a.mrGen.Get()
	a.mrVerMode.Set(false)
	a.mrBusy.Set(true)
	a.mrMsg.Set(fmt.Sprintf("Installing %s %s...", project.Title, chosen.Label))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		a.mrInstallFile(ctx, gen, inst, sub, kind, project.Title, project.ID, chosen.File, chosen.Deps)
	}()
}

// mrInstallHangarVersion downloads the selected Hangar version into the
// plugin directory. External versions install nothing and only report their
// page URL.
func (a *app) mrInstallHangarVersion(inst config.Instance, sub string, project contentItem, chosen contentVersion) {
	v := toHangarVersion(chosen)
	if v.External() {
		a.mrVerMode.Set(false)
		a.mrMsg.Set(externalMessage(v))
		return
	}
	gen := a.mrGen.Get()
	a.mrVerMode.Set(false)
	a.mrBusy.Set(true)
	a.mrMsg.Set(fmt.Sprintf("Installing %s %s...", project.Title, chosen.Label))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		dest := filepath.Join(inst.Dir, sub, v.FileName)
		if err := a.hangar.Download(ctx, v, dest); err != nil {
			if a.mrGen.Get() != gen {
				return
			}
			a.mrBusy.Set(false)
			a.mrMsg.Set("Error: " + err.Error())
			return
		}
		if a.mrGen.Get() != gen {
			return
		}
		a.mrBusy.Set(false)
		a.mrMsg.Set(fmt.Sprintf("Installed %s into %s/ · restart the server to load it", v.FileName, sub))
		a.appendLog(inst.Name, fmt.Sprintf("[mc-tui] Installed %s (%s)", project.Title, v.FileName))
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

// hashesOf computes the SHA-1 and SHA-256 of a file in one read. Both are
// needed for update checks: Modrinth matches jars by SHA-1 and Hangar by
// SHA-256.
func hashesOf(path string) (string, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	h1 := sha1.New()
	h256 := sha256.New()
	if _, err := io.Copy(io.MultiWriter(h1, h256), f); err != nil {
		return "", "", err
	}
	return hex.EncodeToString(h1.Sum(nil)), hex.EncodeToString(h256.Sum(nil)), nil
}

// sha1Of calculates the hex SHA-1 of a file.
func sha1Of(path string) (string, error) {
	sum, _, err := hashesOf(path)
	return sum, err
}

// pendingUpdate is an update found during an update-all pass, waiting for
// confirmation.
type pendingUpdate struct {
	Old    string
	Source string        // "modrinth" | "hangar"
	File   modrinth.File // Filename, URL, VersionNumber and Changelog
	SHA256 string        // Hangar only, verified when downloading
}

// lookupFailureText names the source(s) that could not answer an update
// check, so the summary never presents a network failure as "unknown file".
func lookupFailureText(mrErr, hgErr error) string {
	switch {
	case mrErr != nil && hgErr != nil:
		return fmt.Sprintf("Modrinth and Hangar failed (%v; %v)", mrErr, hgErr)
	case mrErr != nil:
		return "Modrinth failed (" + mrErr.Error() + ")"
	default:
		return "Hangar failed (" + hgErr.Error() + ")"
	}
}

// mrUpdateAll (key u) compares the installed mods/plugins against Modrinth
// and Hangar by file hash and asks for confirmation before downloading
// anything. Files neither source recognises are left untouched.
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
	platform, platformOK := hangar.Platform(inst.Type)
	hangarOK := platformOK && a.mrKind.Get() == "plugins"
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
		type localFile struct {
			name   string
			sha1   string
			sha256 string
		}
		var files []localFile
		var sha1s []string
		if err == nil {
			for _, n := range names {
				h1, h256, hashErr := hashesOf(filepath.Join(dir, n))
				if hashErr != nil {
					err = hashErr
					break
				}
				files = append(files, localFile{name: n, sha1: h1, sha256: h256})
				sha1s = append(sha1s, h1)
			}
		}
		// Each source is queried best-effort: one that is down only leaves
		// its own files unchecked, it never hides the other source's results.
		latest, mrErr := a.mr.LatestByHash(ctx, inst.Type, inst.Version, sha1s)
		var pending []pendingUpdate
		current, unknown, unchecked := 0, 0, 0
		var hgErr error
		for _, f := range files {
			if mrErr == nil {
				if file, known := latest[f.sha1]; known {
					if file.SHA1 == f.sha1 {
						current++
					} else {
						pending = append(pending, pendingUpdate{Old: f.name, Source: sourceModrinth, File: file})
					}
					continue
				}
			}
			// Modrinth does not know this jar (or could not answer). Ask
			// Hangar when it applies.
			if !hangarOK {
				if mrErr != nil {
					unchecked++
				} else {
					unknown++
				}
				continue
			}
			project, found, hashErr := a.hangar.ByHash(ctx, f.sha256)
			if hashErr != nil {
				hgErr = hashErr
				unchecked++
				continue
			}
			if !found {
				// A 404 is a real answer only while Modrinth still speaks.
				if mrErr != nil {
					unchecked++
				} else {
					unknown++
				}
				continue
			}
			vers, verErr := a.hangar.Versions(ctx, project.Owner, project.Slug, platform, inst.Version, "Release")
			if verErr != nil {
				hgErr = verErr
				unchecked++
				continue
			}
			if len(vers) == 0 || vers[0].External() {
				unknown++
				continue
			}
			v := vers[0]
			if strings.EqualFold(v.SHA256, f.sha256) {
				current++
				continue
			}
			pending = append(pending, pendingUpdate{
				Old:    f.name,
				Source: sourceHangar,
				File: modrinth.File{
					Filename:      v.FileName,
					URL:           v.DownloadURL,
					VersionNumber: v.Name,
				},
				SHA256: v.SHA256,
			})
		}
		if a.mrGen.Get() != gen {
			return
		}
		a.mrBusy.Set(false)
		// Nothing could be checked: report the failure instead of a false
		// "everything up to date".
		if mrErr != nil && (!hangarOK || (hgErr != nil && unchecked == len(files))) {
			if hgErr != nil {
				a.mrMsg.Set(fmt.Sprintf("Error: Modrinth: %v; Hangar: %v", mrErr, hgErr))
			} else {
				a.mrMsg.Set("Error: " + mrErr.Error())
			}
			return
		}
		gap := ""
		switch {
		case unchecked > 0:
			gap = fmt.Sprintf(" · %d unchecked: %s", unchecked, lookupFailureText(mrErr, hgErr))
		case mrErr != nil:
			// Hangar resolved every file, but the Modrinth pass is still
			// incomplete and must be reported.
			gap = " · " + lookupFailureText(mrErr, nil)
		}
		if len(pending) == 0 {
			sources := "Modrinth"
			if hangarOK {
				sources = "Modrinth or Hangar"
			}
			a.mrMsg.Set(fmt.Sprintf("Everything up to date (%d current, %d not on %s)%s", current, unknown, sources, gap))
			return
		}
		modrinthUpdates, hangarUpdates := 0, 0
		for _, u := range pending {
			if u.Source == sourceHangar {
				hangarUpdates++
			} else {
				modrinthUpdates++
			}
		}
		a.mrPending = pending
		a.mrUpd.Set(true)
		if hangarOK {
			a.mrMsg.Set(fmt.Sprintf("%d updates available (%d Modrinth, %d Hangar; %d current, %d unknown)%s — update now?",
				len(pending), modrinthUpdates, hangarUpdates, current, unknown, gap))
		} else {
			a.mrMsg.Set(fmt.Sprintf("%d updates available (%d current, %d not on Modrinth)%s — update now?",
				len(pending), current, unknown, gap))
		}
	}()
}

// mrApplyUpdates downloads the confirmed updates and deletes the old files.
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
			dest := filepath.Join(dir, u.File.Filename)
			var err error
			if u.Source == sourceHangar {
				err = a.hangar.Download(ctx, hangar.Version{
					Name:        u.File.VersionNumber,
					DownloadURL: u.File.URL,
					FileName:    u.File.Filename,
					SHA256:      u.SHA256,
				}, dest)
			} else {
				err = download.DownloadFile(ctx, nil, u.File.URL, dest, nil)
			}
			if err != nil {
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

// firstLine devuelve la primera línea no vacía de un texto, truncada.
func firstLine(s string, limit int) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "#-* "))
		if line == "" {
			continue
		}
		if r := []rune(line); len(r) > limit {
			line = string(r[:limit]) + "…"
		}
		return line
	}
	return ""
}

func (a *app) mrItems() []listItem {
	limit := a.descLimit()
	// Confirmación de update-all: se listan los cambios pendientes con su
	// versión y la primera línea del changelog.
	if a.mrUpd.Get() {
		lines := make([]string, len(a.mrPending))
		for i, u := range a.mrPending {
			line := fmt.Sprintf("%s → %s [%s]", u.Old, u.File.Filename, u.File.VersionNumber)
			if cl := firstLine(u.File.Changelog, limit); cl != "" {
				line += " — " + cl
			}
			lines[i] = line
		}
		return fullItems(lines, -1)
	}
	// Selector de versiones de un proyecto.
	if a.mrVerMode.Get() {
		vers := a.mrVers.Get()
		lines := make([]string, len(vers))
		for i, v := range vers {
			line := fmt.Sprintf("%s — MC %s [%s] (%s)",
				v.Label, gameVersionsLabel(v), v.Channel, v.File.Filename)
			if len(v.PluginDeps) > 0 {
				line += " · requires " + strings.Join(v.PluginDeps, ", ")
			}
			if v.external() {
				line += " · external"
			}
			lines[i] = line
		}
		return fullItems(lines, a.mrVerIdx.Get())
	}
	results := a.mrResults.Get()
	lines := make([]string, len(results))
	for i, p := range results {
		desc := p.Description
		if r := []rune(desc); len(r) > limit {
			desc = string(r[:limit]) + "…"
		}
		badge := "M"
		if p.Source == sourceHangar {
			badge = "H"
		}
		also := ""
		if p.AlsoOn != "" {
			also = " (also on Hangar)"
		}
		lines[i] = fmt.Sprintf("[%s] %s (%s ⇩) — %s%s", badge, p.Title, mrDownloadsText(p.Downloads), desc, also)
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

// mrVersionIsHangar reports whether the project being browsed in the
// version selector is a Hangar row.
func (a *app) mrVersionIsHangar() bool {
	results := a.mrResults.Get()
	idx := a.mrIdx.Get()
	return idx >= 0 && idx < len(results) && results[idx].Source == sourceHangar
}

func (a *app) mrHints() []hint {
	if a.mrUpd.Get() {
		return []hint{{"y", "update all"}, {"n/Esc", "cancel"}}
	}
	if a.mrVerMode.Get() {
		hints := []hint{{"↑/↓", "select"}, {"Enter", "install this version"}}
		if a.mrVersionIsHangar() {
			hints = append(hints, hint{"t", "release/snapshot"})
		}
		return append(hints, hint{"Esc", "back"})
	}
	var hints []hint
	if a.mrTyping.Get() {
		hints = []hint{{"Enter", "search"}}
	} else {
		hints = []hint{{"↑/↓", "select"}, {"Enter", "install"}, {"/", "new search"}}
		if a.mrKind.Get() != "datapacks" {
			hints = append(hints, hint{"v", "pick version"}, hint{"u", "update all"})
		}
		if a.mrHangarOK() {
			hints = append(hints, hint{"f", "source"})
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
	if a.mrVerMode.Get() {
		keys := tui.KeyMap{
			tui.OnStop(tui.KeyUp, func(ke tui.KeyEvent) { a.mrMoveVer(-1) }),
			tui.OnStop(tui.KeyDown, func(ke tui.KeyEvent) { a.mrMoveVer(1) }),
			tui.OnStop(tui.KeyPageUp, func(ke tui.KeyEvent) { a.mrMoveVer(-10) }),
			tui.OnStop(tui.KeyPageDown, func(ke tui.KeyEvent) { a.mrMoveVer(10) }),
			tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.mrInstallVersion() }),
			tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) {
				a.mrVerMode.Set(false)
				a.mrMsg.Set("")
			}),
		}
		if a.mrVersionIsHangar() {
			keys = append(keys, tui.OnStop(tui.Rune('t'), func(ke tui.KeyEvent) { a.mrToggleChannel() }))
		}
		return keys
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
		tui.OnStop(tui.Rune('v'), func(ke tui.KeyEvent) { a.mrOpenVersions() }),
		tui.OnStop(tui.Rune('f'), func(ke tui.KeyEvent) { a.mrToggleSource() }),
		tab,
		esc,
	}
}

func (a *app) mrMoveVer(delta int) {
	n := len(a.mrVers.Get())
	if n == 0 {
		return
	}
	a.mrVerIdx.Update(func(i int) int {
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
