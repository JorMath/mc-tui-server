// wizard.go: asistente de nueva instancia (R4) — tipo → versión → nombre
// → memoria → EULA → descarga del jar.
package main

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/JorMath/mc-tui-server/internal/config"
	"github.com/JorMath/mc-tui-server/internal/download"
	"github.com/JorMath/mc-tui-server/internal/installer"
	"github.com/JorMath/mc-tui-server/internal/modrinth"
	"github.com/JorMath/mc-tui-server/internal/mrpack"
	"github.com/JorMath/mc-tui-server/internal/server"
	tui "github.com/grindlemire/go-tui"
)

// Pasos del asistente de nueva instancia (R4). Los pasos wizPack* son el
// flujo de modpacks de Modrinth (v0.1.2): búsqueda → pack → versión.
const (
	wizOff = iota
	wizType
	wizLoading
	wizVersion
	wizPackSearch
	wizPackList
	wizPackVer
	wizImpPath
	wizImpVer
	wizName
	wizMem
	wizEula
	wizDownload
	wizError
)

// wizChoice es una opción del paso de tipo: una distribución de servidor,
// el instalador de modpacks de Modrinth o importar una carpeta existente.
type wizChoice struct {
	Label   string
	Type    config.ServerType
	Modpack bool
	Import  bool
}

var wizChoices = []wizChoice{
	{Label: "vanilla", Type: config.Vanilla},
	{Label: "paper", Type: config.Paper},
	{Label: "purpur", Type: config.Purpur},
	{Label: "fabric", Type: config.Fabric},
	{Label: "forge", Type: config.Forge},
	{Label: "neoforge", Type: config.NeoForge},
	{Label: "quilt", Type: config.Quilt},
	{Label: "modpack (Modrinth)", Modpack: true},
	{Label: "import existing server folder", Import: true},
}

// installerBased indica los tipos cuyo "jar" descargado es en realidad un
// installer que hay que ejecutar en la instancia.
func installerBased(t config.ServerType) bool {
	return t == config.Forge || t == config.NeoForge || t == config.Quilt
}

// wizIsModpack indica si la opción elegida en el paso de tipo es el flujo
// de modpacks.
func (a *app) wizIsModpack() bool {
	return wizChoices[a.wizTypeIdx.Get()].Modpack
}

// wizIsImport indica si la opción elegida es importar una carpeta.
func (a *app) wizIsImport() bool {
	return wizChoices[a.wizTypeIdx.Get()].Import
}

func validNameChar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		return true
	}
	return false
}

func (a *app) wizOpen() {
	a.wizGen.Update(func(g int) int { return g + 1 })
	a.wizTypeIdx.Set(0)
	a.wizVersions.Set([]string{})
	a.wizVerIdx.Set(0)
	a.wizName.Set("")
	a.wizMemory.Set("")
	a.wizMsg.Set("")
	a.wizPackQuery.Set("")
	a.wizPacks.Set([]modrinth.Project{})
	a.wizPackIdx.Set(0)
	a.wizPackVers.Set([]modrinth.PackVersion{})
	a.wizPackVerIdx.Set(0)
	a.wizImpPath.Set("")
	a.wizImpVer.Set("")
	a.wizStep.Set(wizType)
}

func (a *app) wizClose() {
	a.wizGen.Update(func(g int) int { return g + 1 })
	a.wizStep.Set(wizOff)
}

// wizFail muestra el error si el asistente sigue en la misma generación.
func (a *app) wizFail(gen int, err error) {
	if a.wizGen.Get() != gen {
		return
	}
	a.wizMsg.Set("Error: " + err.Error())
	a.wizStep.Set(wizError)
}

func (a *app) wizFetchVersions() {
	if a.wizIsModpack() {
		a.wizMsg.Set("")
		a.wizStep.Set(wizPackSearch)
		return
	}
	if a.wizIsImport() {
		a.wizMsg.Set("")
		a.wizStep.Set(wizImpPath)
		return
	}
	typ := wizChoices[a.wizTypeIdx.Get()].Type
	gen := a.wizGen.Get()
	a.wizMsg.Set(fmt.Sprintf("Fetching %s versions...", typ))
	a.wizStep.Set(wizLoading)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		prov, err := download.For(typ, nil)
		if err != nil {
			a.wizFail(gen, err)
			return
		}
		versions, err := prov.Versions(ctx)
		if err != nil {
			a.wizFail(gen, err)
			return
		}
		if len(versions) == 0 {
			a.wizFail(gen, fmt.Errorf("the %s API returned no versions", typ))
			return
		}
		if a.wizGen.Get() != gen {
			return
		}
		a.wizVersions.Set(versions)
		a.wizVerIdx.Set(0)
		a.wizStep.Set(wizVersion)
	}()
}

func (a *app) wizSubmitName() {
	name := a.wizName.Get()
	if name == "" {
		a.wizMsg.Set("The name cannot be empty")
		return
	}
	if _, exists := a.store.Get(name); exists {
		a.wizMsg.Set(fmt.Sprintf("An instance named %q already exists", name))
		return
	}
	a.wizMsg.Set("")
	a.wizStep.Set(wizMem)
}

func (a *app) wizMemoryMB() int {
	mb, err := strconv.Atoi(a.wizMemory.Get())
	if err != nil || mb <= 0 {
		return 2048
	}
	return mb
}

func (a *app) wizStartDownload() {
	typ := wizChoices[a.wizTypeIdx.Get()].Type
	version := a.wizVersions.Get()[a.wizVerIdx.Get()]
	name := a.wizName.Get()
	memMB := a.wizMemoryMB()
	gen := a.wizGen.Get()
	a.wizMsg.Set("Resolving download URL...")
	a.wizStep.Set(wizDownload)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		prov, err := download.For(typ, nil)
		if err != nil {
			a.wizFail(gen, err)
			return
		}
		url, err := prov.ResolveJarURL(ctx, version)
		if err != nil {
			a.wizFail(gen, err)
			return
		}
		dir := filepath.Join(a.dataDir, "servers", name)
		var jarPath, argsDir string
		if installerBased(typ) {
			jarPath, argsDir, err = a.wizRunInstaller(ctx, typ, url, dir, version, "")
			if err != nil {
				a.wizFail(gen, err)
				return
			}
		} else {
			jar := filepath.Join(dir, "server.jar")
			err = download.DownloadFile(ctx, nil, url, jar, func(done, total int64) {
				if total > 0 {
					a.wizMsg.Set(fmt.Sprintf("Downloading... %d%% (%dMB of %dMB)",
						done*100/total, done/(1024*1024), total/(1024*1024)))
				} else {
					a.wizMsg.Set(fmt.Sprintf("Downloading... %dMB", done/(1024*1024)))
				}
			})
			if err != nil {
				a.wizFail(gen, err)
				return
			}
			jarPath = "server.jar"
		}
		// El usuario aceptó el EULA en el paso anterior del asistente.
		if err := os.WriteFile(filepath.Join(dir, "eula.txt"), []byte("eula=true\n"), 0o644); err != nil {
			a.wizFail(gen, err)
			return
		}
		inst := config.Instance{
			Name:     name,
			Dir:      dir,
			JarPath:  jarPath,
			ArgsDir:  argsDir,
			MemoryMB: memMB,
			Type:     typ,
			Version:  version,
		}
		if err := a.store.Add(inst); err != nil {
			a.wizFail(gen, err)
			return
		}
		if err := a.store.Save(); err != nil {
			a.wizFail(gen, err)
			return
		}
		mgr := server.New(inst)
		a.pumpLogs(mgr)
		a.managers.Update(func(ms []*server.Manager) []*server.Manager {
			return append(ms, mgr)
		})
		a.appendLog(name, fmt.Sprintf("[mc-tui] Instance created: %s %s (%d MB)", typ, version, memMB))
		a.selected.Set(len(a.managers.Get()) - 1)
		a.wizClose()
	}()
}

func (a *app) wizTypeItems() []listItem {
	items := make([]listItem, len(wizChoices))
	for i, c := range wizChoices {
		items[i] = listItem{Text: c.Label, Sel: i == a.wizTypeIdx.Get()}
	}
	return items
}

// wizVersionItems devuelve la lista completa; el contenedor scrollable
// del render se encarga de recortar y seguir la selección.
func (a *app) wizVersionItems() []listItem {
	return fullItems(a.wizVersions.Get(), a.wizVerIdx.Get())
}

func (a *app) wizMoveType(delta int) {
	a.wizTypeIdx.Update(func(i int) int {
		i += delta
		if i < 0 {
			i = 0
		}
		if i >= len(wizChoices) {
			i = len(wizChoices) - 1
		}
		return i
	})
}

func (a *app) wizMoveVersion(delta int) {
	n := len(a.wizVersions.Get())
	a.wizVerIdx.Update(func(i int) int {
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

// wizPackSearchSubmit busca modpacks Fabric en Modrinth con la query actual.
func (a *app) wizPackSearchSubmit() {
	query := a.wizPackQuery.Get()
	if query == "" {
		a.wizMsg.Set("Type something to search")
		return
	}
	gen := a.wizGen.Get()
	a.wizMsg.Set("Searching modpacks on Modrinth...")
	a.wizStep.Set(wizLoading)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		packs, err := a.mr.SearchModpacks(ctx, query)
		if err != nil {
			a.wizFail(gen, err)
			return
		}
		if a.wizGen.Get() != gen {
			return
		}
		if len(packs) == 0 {
			a.wizMsg.Set(fmt.Sprintf("No Fabric modpacks found for %q", query))
			a.wizStep.Set(wizPackSearch)
			return
		}
		a.wizPacks.Set(packs)
		a.wizPackIdx.Set(0)
		a.wizMsg.Set("")
		a.wizStep.Set(wizPackList)
	}()
}

// wizFetchPackVersions lista las versiones del modpack elegido.
func (a *app) wizFetchPackVersions() {
	packs := a.wizPacks.Get()
	idx := a.wizPackIdx.Get()
	if idx < 0 || idx >= len(packs) {
		return
	}
	pack := packs[idx]
	gen := a.wizGen.Get()
	a.wizMsg.Set(fmt.Sprintf("Fetching versions of %s...", pack.Title))
	a.wizStep.Set(wizLoading)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		vers, err := a.mr.ModpackVersions(ctx, pack.ID)
		if err != nil {
			a.wizFail(gen, err)
			return
		}
		if a.wizGen.Get() != gen {
			return
		}
		a.wizPackVers.Set(vers)
		a.wizPackVerIdx.Set(0)
		a.wizMsg.Set("")
		a.wizStep.Set(wizPackVer)
	}()
}

// packLoaders extrae los loaders de mods de las categorías de un proyecto.
func packLoaders(p modrinth.Project) string {
	var found []string
	for _, l := range []string{"fabric", "forge", "neoforge", "quilt"} {
		for _, c := range p.Categories {
			if c == l {
				found = append(found, l)
				break
			}
		}
	}
	return strings.Join(found, "/")
}

func (a *app) wizPackItems() []listItem {
	packs := a.wizPacks.Get()
	limit := a.descLimit()
	lines := make([]string, len(packs))
	for i, p := range packs {
		desc := p.Description
		if r := []rune(desc); len(r) > limit {
			desc = string(r[:limit]) + "…"
		}
		loaders := packLoaders(p)
		if loaders != "" {
			loaders = " [" + loaders + "]"
		}
		lines[i] = fmt.Sprintf("%s%s (%s ⇩) — %s", p.Title, loaders, mrDownloadsText(p.Downloads), desc)
	}
	return fullItems(lines, a.wizPackIdx.Get())
}

func (a *app) wizPackVerItems() []listItem {
	vers := a.wizPackVers.Get()
	lines := make([]string, len(vers))
	for i, v := range vers {
		games := strings.Join(v.GameVersions, ", ")
		loaders := strings.Join(v.Loaders, "/")
		if loaders != "" {
			loaders = " · " + loaders
		}
		lines[i] = fmt.Sprintf("%s — MC %s%s [%s]", v.VersionNumber, games, loaders, v.VersionType)
	}
	return fullItems(lines, a.wizPackVerIdx.Get())
}

func (a *app) wizMovePack(delta int) {
	n := len(a.wizPacks.Get())
	if n == 0 {
		return
	}
	a.wizPackIdx.Update(func(i int) int {
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

func (a *app) wizMovePackVer(delta int) {
	n := len(a.wizPackVers.Get())
	if n == 0 {
		return
	}
	a.wizPackVerIdx.Update(func(i int) int {
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

// loaderTypes mapea el loader del mrpack al ServerType de la instancia.
var loaderTypes = map[string]config.ServerType{
	"fabric":   config.Fabric,
	"forge":    config.Forge,
	"neoforge": config.NeoForge,
	"quilt":    config.Quilt,
}

// wizRunInstaller descarga un installer de loader (Forge/NeoForge/Quilt)
// en la instancia, lo ejecuta mostrando su salida y detecta cómo arrancar
// el servidor resultante. loaderVer vacío deja que Quilt use el más nuevo.
func (a *app) wizRunInstaller(ctx context.Context, typ config.ServerType, url, dir, mcVer, loaderVer string) (jarPath, argsDir string, err error) {
	progress := func(line string) {
		if r := []rune(line); len(r) > 70 {
			line = string(r[:70]) + "…"
		}
		a.wizMsg.Set(line)
	}
	instJar := filepath.Join(dir, "loader-installer.jar")
	a.wizMsg.Set(fmt.Sprintf("Downloading the %s installer...", typ))
	if err := download.DownloadFile(ctx, nil, url, instJar, nil); err != nil {
		return "", "", err
	}
	a.wizMsg.Set(fmt.Sprintf("Running the %s installer — this can take a few minutes...", typ))
	if typ == config.Quilt {
		err = installer.RunQuilt(ctx, "", instJar, dir, mcVer, loaderVer, progress)
	} else {
		err = installer.RunForgeLike(ctx, "", instJar, dir, progress)
	}
	if err != nil {
		return "", "", err
	}
	_ = os.Remove(instJar)
	argsDir, jarPath, err = installer.DetectLaunch(dir)
	if err != nil {
		return "", "", err
	}
	return jarPath, argsDir, nil
}

// wizInstallLoader monta el runtime de servidor que exige el pack y
// devuelve cómo arrancarlo: un jar único o la carpeta de args-file de
// Forge/NeoForge modernos.
func (a *app) wizInstallLoader(ctx context.Context, dir string, ld mrpack.Loader) (jarPath, argsDir string, err error) {
	switch ld.Name {
	case "fabric":
		a.wizMsg.Set(fmt.Sprintf("Downloading Fabric server launcher (MC %s, loader %s)...", ld.MC, ld.Version))
		fab := &download.Fabric{}
		jarURL, err := fab.ServerJarURLFor(ctx, ld.MC, ld.Version)
		if err != nil {
			return "", "", err
		}
		if err := download.DownloadFile(ctx, nil, jarURL, filepath.Join(dir, "server.jar"), nil); err != nil {
			return "", "", err
		}
		return "server.jar", "", nil
	case "forge":
		return a.wizRunInstaller(ctx, config.Forge, download.ForgeInstallerURL("", ld.MC, ld.Version), dir, ld.MC, ld.Version)
	case "neoforge":
		return a.wizRunInstaller(ctx, config.NeoForge, download.NeoForgeInstallerURL("", ld.MC, ld.Version), dir, ld.MC, ld.Version)
	case "quilt":
		qURL, err := download.QuiltInstallerURL(ctx, nil, "")
		if err != nil {
			return "", "", err
		}
		return a.wizRunInstaller(ctx, config.Quilt, qURL, dir, ld.MC, ld.Version)
	default:
		return "", "", fmt.Errorf("unsupported loader %q", ld.Name)
	}
}

// wizDropClientOnly quita los archivos cuyo proyecto en Modrinth está
// marcado server_side "unsupported": muchos packs etiquetan mal sus mods
// de cliente (shaders, HUD...) y crashean el servidor dedicado al cargar
// clases de cliente. Si la consulta falla se instala todo (mejor un aviso
// que bloquear la instalación por un fallo de red).
func (a *app) wizDropClientOnly(ctx context.Context, files []mrpack.IndexFile) ([]mrpack.IndexFile, int) {
	var ids []string
	seen := map[string]bool{}
	for _, f := range files {
		if id := f.ModrinthProject(); id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return files, 0
	}
	a.wizMsg.Set("Checking which mods are server-side...")
	unsupported, err := a.mr.ServerUnsupported(ctx, ids)
	if err != nil || len(unsupported) == 0 {
		return files, 0
	}
	var out []mrpack.IndexFile
	for _, f := range files {
		if unsupported[f.ModrinthProject()] {
			continue
		}
		out = append(out, f)
	}
	return out, len(files) - len(out)
}

// wizStartModpackInstall descarga el .mrpack, instala sus archivos y
// overrides en la instancia nueva y monta el runtime del loader que pide
// el pack (Fabric, Forge, NeoForge o Quilt).
func (a *app) wizStartModpackInstall() {
	packs, vers := a.wizPacks.Get(), a.wizPackVers.Get()
	pi, vi := a.wizPackIdx.Get(), a.wizPackVerIdx.Get()
	if pi < 0 || pi >= len(packs) || vi < 0 || vi >= len(vers) {
		return
	}
	pack, pv := packs[pi], vers[vi]
	name := a.wizName.Get()
	memMB := a.wizMemoryMB()
	gen := a.wizGen.Get()
	a.wizMsg.Set("Downloading modpack...")
	a.wizStep.Set(wizDownload)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		dir := filepath.Join(a.dataDir, "servers", name)
		packFile := filepath.Join(dir, pv.Filename)
		err := download.DownloadFile(ctx, nil, pv.URL, packFile, func(done, total int64) {
			if total > 0 {
				a.wizMsg.Set(fmt.Sprintf("Downloading %s... %d%%", pv.Filename, done*100/total))
			}
		})
		if err != nil {
			a.wizFail(gen, err)
			return
		}
		ix, err := mrpack.Parse(packFile)
		if err != nil {
			a.wizFail(gen, err)
			return
		}
		ld, err := ix.Loader()
		if err != nil {
			a.wizFail(gen, err)
			return
		}
		files, err := ix.ServerFiles()
		if err != nil {
			a.wizFail(gen, err)
			return
		}
		files, skipped := a.wizDropClientOnly(ctx, files)
		for i, f := range files {
			a.wizMsg.Set(fmt.Sprintf("Downloading server files %d/%d — %s",
				i+1, len(files), path.Base(f.Path)))
			dest := filepath.Join(dir, filepath.FromSlash(f.Path))
			if err := download.DownloadFile(ctx, nil, f.Downloads[0], dest, nil); err != nil {
				a.wizFail(gen, err)
				return
			}
		}
		a.wizMsg.Set("Extracting pack overrides...")
		if err := mrpack.ExtractOverrides(packFile, dir); err != nil {
			a.wizFail(gen, err)
			return
		}
		// El .mrpack ya no hace falta; si no se puede borrar no es grave.
		_ = os.Remove(packFile)
		jarPath, argsDir, err := a.wizInstallLoader(ctx, dir, ld)
		if err != nil {
			a.wizFail(gen, err)
			return
		}
		// El usuario aceptó el EULA en el paso anterior del asistente.
		if err := os.WriteFile(filepath.Join(dir, "eula.txt"), []byte("eula=true\n"), 0o644); err != nil {
			a.wizFail(gen, err)
			return
		}
		// La copia del índice y los IDs del pack permiten actualizar la
		// instancia cuando el modpack publique una versión nueva.
		if err := mrpack.WriteIndex(ix, filepath.Join(dir, mrpack.IndexCopyName)); err != nil {
			a.appendLog(name, "[mc-tui] Warning: could not save the pack index: "+err.Error())
		}
		inst := config.Instance{
			Name:      name,
			Dir:       dir,
			JarPath:   jarPath,
			ArgsDir:   argsDir,
			MemoryMB:  memMB,
			Type:      loaderTypes[ld.Name],
			Version:   ld.MC,
			PackID:    pack.ID,
			PackVerID: pv.ID,
		}
		if err := a.store.Add(inst); err != nil {
			a.wizFail(gen, err)
			return
		}
		if err := a.store.Save(); err != nil {
			a.wizFail(gen, err)
			return
		}
		mgr := server.New(inst)
		a.pumpLogs(mgr)
		a.managers.Update(func(ms []*server.Manager) []*server.Manager {
			return append(ms, mgr)
		})
		a.appendLog(name, fmt.Sprintf("[mc-tui] Modpack installed: %s %s (MC %s, %s %s, %d MB)",
			pack.Title, pv.VersionNumber, ld.MC, ld.Name, ld.Version, memMB))
		if skipped > 0 {
			a.appendLog(name, fmt.Sprintf("[mc-tui] Skipped %d client-only mods (Modrinth marks them server-unsupported)", skipped))
		}
		a.selected.Set(len(a.managers.Get()) - 1)
		a.wizClose()
	}()
}

func (a *app) wizKeyMap() tui.KeyMap {
	esc := tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.wizClose() })
	switch a.wizStep.Get() {
	case wizType:
		return tui.KeyMap{
			tui.OnStop(tui.KeyUp, func(ke tui.KeyEvent) { a.wizMoveType(-1) }),
			tui.OnStop(tui.KeyDown, func(ke tui.KeyEvent) { a.wizMoveType(1) }),
			tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.wizFetchVersions() }),
			esc,
		}
	case wizLoading:
		return tui.KeyMap{esc}
	case wizVersion:
		return tui.KeyMap{
			tui.OnStop(tui.KeyUp, func(ke tui.KeyEvent) { a.wizMoveVersion(-1) }),
			tui.OnStop(tui.KeyDown, func(ke tui.KeyEvent) { a.wizMoveVersion(1) }),
			tui.OnStop(tui.KeyPageUp, func(ke tui.KeyEvent) { a.wizMoveVersion(-10) }),
			tui.OnStop(tui.KeyPageDown, func(ke tui.KeyEvent) { a.wizMoveVersion(10) }),
			tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.wizStep.Set(wizName) }),
			esc,
		}
	case wizPackSearch:
		return tui.KeyMap{
			tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {
				a.wizPackQuery.Update(func(s string) string { return s + string(ke.Rune) })
			}),
			tui.OnStop(tui.KeyBackspace, func(ke tui.KeyEvent) {
				a.wizPackQuery.Update(func(s string) string {
					r := []rune(s)
					if len(r) == 0 {
						return s
					}
					return string(r[:len(r)-1])
				})
			}),
			tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.wizPackSearchSubmit() }),
			esc,
		}
	case wizPackList:
		return tui.KeyMap{
			tui.OnStop(tui.KeyUp, func(ke tui.KeyEvent) { a.wizMovePack(-1) }),
			tui.OnStop(tui.KeyDown, func(ke tui.KeyEvent) { a.wizMovePack(1) }),
			tui.OnStop(tui.KeyPageUp, func(ke tui.KeyEvent) { a.wizMovePack(-10) }),
			tui.OnStop(tui.KeyPageDown, func(ke tui.KeyEvent) { a.wizMovePack(10) }),
			tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.wizFetchPackVersions() }),
			tui.OnStop(tui.Rune('/'), func(ke tui.KeyEvent) {
				a.wizMsg.Set("")
				a.wizStep.Set(wizPackSearch)
			}),
			esc,
		}
	case wizPackVer:
		return tui.KeyMap{
			tui.OnStop(tui.KeyUp, func(ke tui.KeyEvent) { a.wizMovePackVer(-1) }),
			tui.OnStop(tui.KeyDown, func(ke tui.KeyEvent) { a.wizMovePackVer(1) }),
			tui.OnStop(tui.KeyPageUp, func(ke tui.KeyEvent) { a.wizMovePackVer(-10) }),
			tui.OnStop(tui.KeyPageDown, func(ke tui.KeyEvent) { a.wizMovePackVer(10) }),
			tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) {
				a.wizMsg.Set("")
				a.wizStep.Set(wizName)
			}),
			esc,
		}
	case wizImpPath:
		return tui.KeyMap{
			tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {
				if ke.Rune >= ' ' {
					a.wizImpPath.Update(func(s string) string { return s + string(ke.Rune) })
				}
			}),
			tui.OnStop(tui.KeyBackspace, func(ke tui.KeyEvent) {
				a.wizImpPath.Update(func(s string) string {
					r := []rune(s)
					if len(r) == 0 {
						return s
					}
					return string(r[:len(r)-1])
				})
			}),
			tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.wizSubmitImportPath() }),
			esc,
		}
	case wizImpVer:
		return tui.KeyMap{
			tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {
				r := ke.Rune
				if (r >= '0' && r <= '9') || r == '.' || (r >= 'a' && r <= 'z') || r == '-' {
					a.wizImpVer.Update(func(s string) string { return s + string(r) })
				}
			}),
			tui.OnStop(tui.KeyBackspace, func(ke tui.KeyEvent) {
				a.wizImpVer.Update(func(s string) string {
					if len(s) == 0 {
						return s
					}
					return s[:len(s)-1]
				})
			}),
			tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.wizStep.Set(wizEula) }),
			esc,
		}
	case wizName:
		return tui.KeyMap{
			tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {
				if validNameChar(ke.Rune) {
					a.wizName.Update(func(s string) string { return s + string(ke.Rune) })
				}
			}),
			tui.OnStop(tui.KeyBackspace, func(ke tui.KeyEvent) {
				a.wizName.Update(func(s string) string {
					if len(s) == 0 {
						return s
					}
					return s[:len(s)-1]
				})
			}),
			tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.wizSubmitName() }),
			esc,
		}
	case wizMem:
		return tui.KeyMap{
			tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {
				if ke.Rune >= '0' && ke.Rune <= '9' {
					a.wizMemory.Update(func(s string) string { return s + string(ke.Rune) })
				}
			}),
			tui.OnStop(tui.KeyBackspace, func(ke tui.KeyEvent) {
				a.wizMemory.Update(func(s string) string {
					if len(s) == 0 {
						return s
					}
					return s[:len(s)-1]
				})
			}),
			tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) {
				if a.wizIsImport() {
					a.wizStep.Set(wizImpVer)
					return
				}
				a.wizStep.Set(wizEula)
			}),
			esc,
		}
	case wizEula:
		return tui.KeyMap{
			tui.OnStop(tui.Rune('y'), func(ke tui.KeyEvent) {
				switch {
				case a.wizIsImport():
					a.wizFinishImport()
				case a.wizIsModpack():
					a.wizStartModpackInstall()
				default:
					a.wizStartDownload()
				}
			}),
			tui.OnStop(tui.Rune('n'), func(ke tui.KeyEvent) { a.wizClose() }),
			esc,
		}
	case wizDownload:
		// Sin teclas: la descarga no se cancela a mitad en esta versión.
		return tui.KeyMap{tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {})}
	default: // wizError
		return tui.KeyMap{esc, tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.wizClose() })}
	}
}

func (a *app) wizHints() []hint {
	switch a.wizStep.Get() {
	case wizType:
		return []hint{{"↑/↓", "choose"}, {"Enter", "continue"}, {"Esc", "cancel"}}
	case wizLoading:
		return []hint{{"Esc", "cancel"}}
	case wizVersion:
		return []hint{{"↑/↓ PgUp/PgDn", "choose"}, {"Enter", "continue"}, {"Esc", "cancel"}}
	case wizImpPath:
		return []hint{{"type or paste", "folder path"}, {"Enter", "detect"}, {"Esc", "cancel"}}
	case wizImpVer:
		return []hint{{"e.g. 1.20.1", "optional, helps Modrinth filters"}, {"Enter", "continue"}, {"Esc", "cancel"}}
	case wizPackSearch:
		return []hint{{"Enter", "search"}, {"Esc", "cancel"}}
	case wizPackList:
		return []hint{{"↑/↓ PgUp/PgDn", "choose"}, {"Enter", "continue"}, {"/", "new search"}, {"Esc", "cancel"}}
	case wizPackVer:
		return []hint{{"↑/↓ PgUp/PgDn", "choose"}, {"Enter", "continue"}, {"Esc", "cancel"}}
	case wizName:
		return []hint{{"a-z 0-9 - _", "type"}, {"Enter", "continue"}, {"Esc", "cancel"}}
	case wizMem:
		return []hint{{"0-9", "type (empty = 2048)"}, {"Enter", "continue"}, {"Esc", "cancel"}}
	case wizEula:
		return []hint{{"y", "accept & download"}, {"n/Esc", "cancel"}}
	case wizError:
		return []hint{{"Esc", "close"}}
	default: // wizDownload: no hay teclas activas
		return nil
	}
}

func (a *app) wizStepTitle() string {
	if a.wizIsImport() {
		switch a.wizStep.Get() {
		case wizType:
			return "1/6 · Server type"
		case wizImpPath:
			return "2/6 · Folder to import"
		case wizName:
			return "3/6 · Instance name"
		case wizMem:
			return "4/6 · Memory (MB)"
		case wizImpVer:
			return "5/6 · Minecraft version (optional)"
		case wizEula:
			return "6/6 · Minecraft EULA"
		default:
			return "Error"
		}
	}
	// El flujo de modpack tiene dos pasos extra (búsqueda y pack).
	if a.wizIsModpack() {
		switch a.wizStep.Get() {
		case wizType:
			return "1/7 · Server type"
		case wizPackSearch:
			return "2/7 · Modpack search"
		case wizLoading:
			return "Fetching from Modrinth"
		case wizPackList:
			return "3/7 · Modpack"
		case wizPackVer:
			return "4/7 · Modpack version"
		case wizName:
			return "5/7 · Instance name"
		case wizMem:
			return "6/7 · Memory (MB)"
		case wizEula:
			return "7/7 · Minecraft EULA"
		case wizDownload:
			return "Installing modpack"
		default:
			return "Error"
		}
	}
	switch a.wizStep.Get() {
	case wizType:
		return "1/5 · Server type"
	case wizLoading:
		return "2/5 · Fetching versions"
	case wizVersion:
		return "2/5 · Version"
	case wizName:
		return "3/5 · Instance name"
	case wizMem:
		return "4/5 · Memory (MB)"
	case wizEula:
		return "5/5 · Minecraft EULA"
	case wizDownload:
		return "Downloading"
	default:
		return "Error"
	}
}
