package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"
	"github.com/JorMath/mc-tui-server/internal/assets"
	"github.com/JorMath/mc-tui-server/internal/config"
	"github.com/JorMath/mc-tui-server/internal/download"
	"github.com/JorMath/mc-tui-server/internal/metrics"
	"github.com/JorMath/mc-tui-server/internal/modrinth"
	"github.com/JorMath/mc-tui-server/internal/properties"
	"github.com/JorMath/mc-tui-server/internal/server"
	tui "github.com/grindlemire/go-tui"
)

const (
	stopTimeout = 30 * time.Second
	maxLogLines = 5000
)

// Pasos del asistente de nueva instancia (R4).
const (
	wizOff = iota
	wizType
	wizLoading
	wizVersion
	wizName
	wizMem
	wizEula
	wizDownload
	wizError
)

var wizTypes = []config.ServerType{config.Vanilla, config.Paper, config.Purpur, config.Fabric}

// Arte ASCII del splash: solo bloques sólidos y espacios — los caracteres
// de caja (╔═╗) se superponen en algunas fuentes de terminal.
// splashFont: 5 filas por letra, ancho fijo por letra.
var splashFont = map[rune][]string{
	'M': {"█   █", "██ ██", "█ █ █", "█   █", "█   █"},
	'C': {" ███", "█   ", "█   ", "█   ", " ███"},
	'-': {"    ", "    ", " ██ ", "    ", "    "},
	'T': {"█████", "  █  ", "  █  ", "  █  ", "  █  "},
	'U': {"█   █", "█   █", "█   █", "█   █", " ███ "},
	'I': {"███", " █ ", " █ ", " █ ", "███"},
	'S': {" ████", "█    ", " ███ ", "    █", "████ "},
	'E': {"█████", "█    ", "███  ", "█    ", "█████"},
	'R': {"████ ", "█   █", "████ ", "█  █ ", "█   █"},
	'V': {"█   █", "█   █", "█   █", " █ █ ", "  █  "},
}

// blank es el "braille pattern blank" (U+2800): ocupa una celda pero no es
// espacio, así el layout no lo colapsa ni lo recorta.
const blank = "⠀"

// renderWord compone una palabra duplicando cada celda en horizontal
// (píxeles de 2 columnas). Los huecos usan blank en vez de espacios.
func renderWord(word string) []string {
	rows := make([]string, 5)
	for r := 0; r < 5; r++ {
		for i, ch := range word {
			if i > 0 {
				rows[r] += blank + blank
			}
			for _, c := range splashFont[ch][r] {
				if c == '█' {
					rows[r] += "██"
				} else {
					rows[r] += blank + blank
				}
			}
		}
	}
	return rows
}

var splashTitle = append(append(renderWord("MC-TUI"), blank), renderWord("SERVER")...)

// splashLogo es el bloque de césped de Minecraft en píxeles:
// g/G césped (verde claro/oscuro), b/t/d tierra (media/clara/oscura),
// s piedra gris. Cada píxel se pinta como "██" con color hex literal.
var splashLogo = []string{
	"ggGgggGggg",
	"GggGgggggG",
	"dgGdggdggd",
	"ddsddgddbd",
	"bdbbtdbdbb",
	"dbddbbdtdd",
	"bbdsddbbdb",
	"dtbdbddbbd",
	"bddbdsbddb",
}

type logoSeg struct {
	Text string
	Key  string
}

// splashLogoRows agrupa píxeles contiguos del mismo color en un solo
// segmento para no crear un span por píxel.
func splashLogoRows() [][]logoSeg {
	rows := make([][]logoSeg, len(splashLogo))
	for i, row := range splashLogo {
		var segs []logoSeg
		for _, c := range row {
			key := string(c)
			if n := len(segs); n > 0 && segs[n-1].Key == key {
				segs[n-1].Text += "██"
				continue
			}
			segs = append(segs, logoSeg{Text: "██", Key: key})
		}
		rows[i] = segs
	}
	return rows
}

type logEntry struct {
	name string
	line string
}

type app struct {
	store    *config.Store
	dataDir  string
	managers *tui.State[[]*server.Manager]
	// splash se muestra al arrancar hasta pulsar una tecla o agotar los
	// ticks del timer de refresh. splashTicks solo lo toca ese timer.
	splash      *tui.State[bool]
	splashTicks int
	// logCh agrega los logs de todas las instancias: los watchers se
	// registran al montar, así que un canal único permite añadir
	// instancias en caliente.
	logCh chan logEntry

	selected  *tui.State[int]
	statuses  *tui.State[map[string]server.Status]
	logs      *tui.State[map[string][]string]
	cmdActive *tui.State[bool]
	cmdText   *tui.State[string]

	// Gestión de instancias (v0.1.1): renombrar y eliminar con confirmación.
	// delTarget guarda el nombre pendiente de confirmar ("" = sin diálogo).
	renActive *tui.State[bool]
	renText   *tui.State[string]
	renMsg    *tui.State[string]
	delTarget *tui.State[string]

	collector *metrics.Collector
	samples   *tui.State[map[string]metrics.Sample]
	// lastPIDs solo se toca desde el timer de refresh (una goroutine).
	lastPIDs map[string]int

	// Estado del asistente. wizGen invalida goroutines de un asistente
	// cancelado para que no re-abran la UI.
	wizStep     *tui.State[int]
	wizGen      *tui.State[int]
	wizTypeIdx  *tui.State[int]
	wizVersions *tui.State[[]string]
	wizVerIdx   *tui.State[int]
	wizName     *tui.State[string]
	wizMemory   *tui.State[string]
	wizMsg      *tui.State[string]

	// Panel de archivos (R3). fmProps se muta fuera de un State; fmRev
	// se incrementa tras cada mutación para forzar el re-render.
	fmOpen      *tui.State[bool]
	fmTab       *tui.State[int]
	fmProps     *properties.File
	fmRev       *tui.State[int]
	fmPropsIdx  *tui.State[int]
	fmEditing   *tui.State[bool]
	fmEditText  *tui.State[string]
	fmDirty     *tui.State[bool]
	fmWorlds    *tui.State[[]string]
	fmPlugins   *tui.State[[]string]
	fmWorldIdx  *tui.State[int]
	fmPluginIdx *tui.State[int]
	fmConfirm   *tui.State[string]
	fmMsg       *tui.State[string]

	// Buscador de Modrinth (R6). mrGen invalida goroutines de un panel
	// cerrado, igual que wizGen.
	mr        *modrinth.Client
	mrOpen    *tui.State[bool]
	mrTyping  *tui.State[bool]
	mrQuery   *tui.State[string]
	mrResults *tui.State[[]modrinth.Project]
	mrIdx     *tui.State[int]
	mrBusy    *tui.State[bool]
	mrGen     *tui.State[int]
	mrMsg     *tui.State[string]
}

func App(store *config.Store, managers []*server.Manager) *app {
	statuses := map[string]server.Status{}
	logs := map[string][]string{}
	for _, m := range managers {
		statuses[m.Instance().Name] = m.Status()
		logs[m.Instance().Name] = nil
	}
	a := &app{
		store:     store,
		dataDir:   filepath.Dir(store.Path()),
		managers:  tui.NewState(managers),
		splash:    tui.NewState(true),
		logCh:     make(chan logEntry, 2048),
		selected:  tui.NewState(0),
		statuses:  tui.NewState(statuses),
		logs:      tui.NewState(logs),
		cmdActive: tui.NewState(false),
		cmdText:   tui.NewState(""),
		renActive: tui.NewState(false),
		renText:   tui.NewState(""),
		renMsg:    tui.NewState(""),
		delTarget: tui.NewState(""),
		collector: metrics.NewCollector(),
		samples:   tui.NewState(map[string]metrics.Sample{}),
		lastPIDs:  map[string]int{},

		wizStep:     tui.NewState(wizOff),
		wizGen:      tui.NewState(0),
		wizTypeIdx:  tui.NewState(0),
		wizVersions: tui.NewState([]string{}),
		wizVerIdx:   tui.NewState(0),
		wizName:     tui.NewState(""),
		wizMemory:   tui.NewState(""),
		wizMsg:      tui.NewState(""),

		fmOpen:      tui.NewState(false),
		fmTab:       tui.NewState(0),
		fmProps:     &properties.File{},
		fmRev:       tui.NewState(0),
		fmPropsIdx:  tui.NewState(0),
		fmEditing:   tui.NewState(false),
		fmEditText:  tui.NewState(""),
		fmDirty:     tui.NewState(false),
		fmWorlds:    tui.NewState([]string{}),
		fmPlugins:   tui.NewState([]string{}),
		fmWorldIdx:  tui.NewState(0),
		fmPluginIdx: tui.NewState(0),
		fmConfirm:   tui.NewState(""),
		fmMsg:       tui.NewState(""),

		mr:        &modrinth.Client{},
		mrOpen:    tui.NewState(false),
		mrTyping:  tui.NewState(false),
		mrQuery:   tui.NewState(""),
		mrResults: tui.NewState([]modrinth.Project{}),
		mrIdx:     tui.NewState(0),
		mrBusy:    tui.NewState(false),
		mrGen:     tui.NewState(0),
		mrMsg:     tui.NewState(""),
	}
	for _, m := range managers {
		a.pumpLogs(m)
	}
	return a
}

// pumpLogs reenvía los logs de un manager al canal agregado de la app.
// El nombre se lee por línea para que un rename no deje logs huérfanos;
// la goroutine termina cuando el manager se cierra (Close).
func (a *app) pumpLogs(m *server.Manager) {
	go func() {
		for line := range m.Logs() {
			a.logCh <- logEntry{name: m.Instance().Name, line: line}
		}
	}()
}

func (a *app) current() *server.Manager {
	managers := a.managers.Get()
	i := a.selected.Get()
	if i < 0 || i >= len(managers) {
		return nil
	}
	return managers[i]
}

func (a *app) moveSelection(delta int) {
	n := len(a.managers.Get())
	if n == 0 {
		return
	}
	a.selected.Update(func(i int) int {
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

func (a *app) appendLog(name, line string) {
	a.logs.Update(func(m map[string][]string) map[string][]string {
		lines := append(m[name], line)
		if len(lines) > maxLogLines {
			lines = lines[len(lines)-maxLogLines:]
		}
		m[name] = lines
		return m
	})
}

func (a *app) refreshStatuses() {
	// Auto-descarta el splash tras ~3s (6 ticks de 500ms).
	if a.splash.Get() {
		a.splashTicks++
		if a.splashTicks >= 6 {
			a.splash.Set(false)
		}
	}
	a.statuses.Update(func(m map[string]server.Status) map[string]server.Status {
		for _, mgr := range a.managers.Get() {
			m[mgr.Instance().Name] = mgr.Status()
		}
		return m
	})
	a.refreshSamples()
}

// refreshSamples muestrea CPU/RAM (R5) de las instancias corriendo.
func (a *app) refreshSamples() {
	a.samples.Update(func(m map[string]metrics.Sample) map[string]metrics.Sample {
		for _, mgr := range a.managers.Get() {
			name := mgr.Instance().Name
			pid := mgr.PID()
			if old := a.lastPIDs[name]; old != 0 && old != pid {
				a.collector.Forget(old)
			}
			a.lastPIDs[name] = pid
			if pid == 0 {
				delete(m, name)
				continue
			}
			s, err := a.collector.Sample(pid)
			if err != nil {
				// El proceso pudo morir entre PID() y Sample(); se limpia solo.
				delete(m, name)
				continue
			}
			m[name] = s
		}
		return m
	})
}

func (a *app) metricText(name string) string {
	s, ok := a.samples.Get()[name]
	if !ok {
		return ""
	}
	return fmt.Sprintf("cpu %.1f%% · ram %dM", s.CPUPercent, s.RSSBytes/(1024*1024))
}

// startSelected/stopSelected/restartSelected corren en goroutines porque
// Stop puede bloquear hasta stopTimeout y no debe congelar la UI.
func (a *app) startSelected() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	go func() {
		if err := mgr.Start(); err != nil {
			a.appendLog(mgr.Instance().Name, "[mc-tui] "+err.Error())
		}
	}()
}

func (a *app) stopSelected() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	go func() {
		if err := mgr.Stop(stopTimeout); err != nil {
			a.appendLog(mgr.Instance().Name, "[mc-tui] "+err.Error())
		}
	}()
}

func (a *app) restartSelected() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	go func() {
		if err := mgr.Restart(stopTimeout); err != nil {
			a.appendLog(mgr.Instance().Name, "[mc-tui] "+err.Error())
		}
	}()
}

// Barra de comandos (R2): en modo comando se capturan todas las teclas
// con OnStop para que escribir no dispare los atajos globales.
func (a *app) appendCmdChar(ke tui.KeyEvent) {
	a.cmdText.Update(func(s string) string { return s + string(ke.Rune) })
}

func (a *app) deleteCmdChar(ke tui.KeyEvent) {
	a.cmdText.Update(func(s string) string {
		r := []rune(s)
		if len(r) == 0 {
			return s
		}
		return string(r[:len(r)-1])
	})
}

func (a *app) submitCmd(ke tui.KeyEvent) {
	text := a.cmdText.Get()
	a.cmdText.Set("")
	if text == "" {
		return
	}
	mgr := a.current()
	if mgr == nil {
		return
	}
	a.appendLog(mgr.Instance().Name, "> "+text)
	if err := mgr.Send(text); err != nil {
		a.appendLog(mgr.Instance().Name, "[mc-tui] "+err.Error())
	}
}

func (a *app) closeCmd(ke tui.KeyEvent) {
	a.cmdActive.Set(false)
	a.cmdText.Set("")
}

// --- Gestión de instancias: renombrar y eliminar (v0.1.1) -------------------
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

// --- Asistente de nueva instancia (R4) -------------------------------------
func (a *app) wizOpen() {
	a.wizGen.Update(func(g int) int { return g + 1 })
	a.wizTypeIdx.Set(0)
	a.wizVersions.Set([]string{})
	a.wizVerIdx.Set(0)
	a.wizName.Set("")
	a.wizMemory.Set("")
	a.wizMsg.Set("")
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
	typ := wizTypes[a.wizTypeIdx.Get()]
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

func validNameChar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		return true
	}
	return false
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
	typ := wizTypes[a.wizTypeIdx.Get()]
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
		// El usuario aceptó el EULA en el paso anterior del asistente.
		if err := os.WriteFile(filepath.Join(dir, "eula.txt"), []byte("eula=true\n"), 0o644); err != nil {
			a.wizFail(gen, err)
			return
		}
		inst := config.Instance{
			Name:     name,
			Dir:      dir,
			JarPath:  "server.jar",
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

type wizItem struct {
	Text string
	Sel  bool
}

func (a *app) wizTypeItems() []wizItem {
	items := make([]wizItem, len(wizTypes))
	for i, t := range wizTypes {
		items[i] = wizItem{Text: string(t), Sel: i == a.wizTypeIdx.Get()}
	}
	return items
}

// wizVersionItems devuelve la lista completa; el contenedor scrollable
// del render se encarga de recortar y seguir la selección.
func (a *app) wizVersionItems() []wizItem {
	return fullItems(a.wizVersions.Get(), a.wizVerIdx.Get())
}

func (a *app) wizMoveType(delta int) {
	a.wizTypeIdx.Update(func(i int) int {
		i += delta
		if i < 0 {
			i = 0
		}
		if i >= len(wizTypes) {
			i = len(wizTypes) - 1
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
			tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.wizStep.Set(wizEula) }),
			esc,
		}
	case wizEula:
		return tui.KeyMap{
			tui.OnStop(tui.Rune('y'), func(ke tui.KeyEvent) { a.wizStartDownload() }),
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

// --- Panel de archivos (R3) -------------------------------------------------
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
	a.fmEditing.Set(false)
	a.fmDirty.Set(false)
	a.fmConfirm.Set("")
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

// fullItems marca la selección sobre la lista completa; el recorte lo
// hace el contenedor scrollable del render.
func fullItems(list []string, sel int) []wizItem {
	items := make([]wizItem, len(list))
	for i, s := range list {
		items[i] = wizItem{Text: s, Sel: i == sel}
	}
	return items
}

// scrollTo calcula el desplazamiento vertical para mantener visible la
// selección, dejando unas líneas de contexto por encima.
func scrollTo(sel int) int {
	y := sel - 4
	if y < 0 {
		return 0
	}
	return y
}

func (a *app) fmItems() []wizItem {
	switch a.fmTab.Get() {
	case 0:
		return fullItems(a.fmPropLines(), a.fmPropsIdx.Get())
	case 1:
		return fullItems(a.fmWorlds.Get(), a.fmWorldIdx.Get())
	default:
		return fullItems(a.fmPlugins.Get(), a.fmPluginIdx.Get())
	}
}

// fmScrollY sigue la selección de la pestaña activa.
func (a *app) fmScrollY() int {
	switch a.fmTab.Get() {
	case 0:
		return scrollTo(a.fmPropsIdx.Get())
	case 1:
		return scrollTo(a.fmWorldIdx.Get())
	default:
		return scrollTo(a.fmPluginIdx.Get())
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
	default:
		move(a.fmPluginIdx, len(a.fmPlugins.Get()))
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
	if a.fmTab.Get() == 1 {
		err = assets.DeleteWorld(inst.Dir, name)
	} else {
		err = assets.DeletePlugin(inst.Dir, inst.Type, name)
	}
	if err != nil {
		a.fmMsg.Set("Error: " + err.Error())
		return
	}
	a.fmMsg.Set(fmt.Sprintf("%q deleted", name))
	a.fmWorldIdx.Set(0)
	a.fmPluginIdx.Set(0)
	a.fmReloadLists(inst)
}

func (a *app) fmTitle() string {
	_ = a.fmRev.Get() // dependencia explícita: re-render tras mutar fmProps
	title := fmt.Sprintf("Files — %s · %s", a.currentName(), a.fmTabName())
	if a.fmDirty.Get() {
		title += " (unsaved)"
	}
	return title
}

// hint es un atajo de teclado mostrado al pie: tecla en cyan + etiqueta.
type hint struct {
	K string
	L string
}

func (a *app) mainHints() []hint {
	return []hint{
		{"↑/↓", "select"}, {"s", "start"}, {"x", "stop"}, {"r", "restart"},
		{"c/Enter", "command"}, {"e", "files"}, {"m", "modrinth"},
		{"n", "new"}, {"R", "rename"}, {"d", "delete"}, {"q", "quit"},
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

func (a *app) fmHints() []hint {
	if a.fmEditing.Get() {
		return []hint{{"Enter", "apply"}, {"Esc", "cancel"}}
	}
	if a.fmConfirm.Get() != "" {
		return []hint{{"y", "delete"}, {"n/Esc", "keep"}}
	}
	if a.fmTab.Get() == 0 {
		return []hint{{"↑/↓", "select"}, {"Enter", "edit"}, {"w", "save"}, {"1/2/3 Tab", "switch tab"}, {"Esc", "close"}}
	}
	return []hint{{"↑/↓", "select"}, {"d", "delete"}, {"1/2/3 Tab", "switch tab"}, {"Esc", "close"}}
}

func (a *app) mrHints() []hint {
	if a.mrTyping.Get() {
		return []hint{{"Enter", "search"}, {"Esc", "close"}}
	}
	return []hint{{"↑/↓", "select"}, {"Enter", "install"}, {"/", "new search"}, {"Esc", "close"}}
}

func (a *app) fmTabName() string {
	switch a.fmTab.Get() {
	case 0:
		return "Properties"
	case 1:
		return "Worlds"
	default:
		return "Plugins/Mods"
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
	return tui.KeyMap{
		tui.OnStop(tui.Rune('1'), func(ke tui.KeyEvent) { a.fmTab.Set(0) }),
		tui.OnStop(tui.Rune('2'), func(ke tui.KeyEvent) { a.fmTab.Set(1) }),
		tui.OnStop(tui.Rune('3'), func(ke tui.KeyEvent) { a.fmTab.Set(2) }),
		tui.OnStop(tui.KeyTab, func(ke tui.KeyEvent) { a.fmTab.Update(func(t int) int { return (t + 1) % 3 }) }),
		tui.OnStop(tui.KeyUp, func(ke tui.KeyEvent) { a.fmMove(-1) }),
		tui.OnStop(tui.KeyDown, func(ke tui.KeyEvent) { a.fmMove(1) }),
		tui.OnStop(tui.KeyPageUp, func(ke tui.KeyEvent) { a.fmMove(-10) }),
		tui.OnStop(tui.KeyPageDown, func(ke tui.KeyEvent) { a.fmMove(10) }),
		tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) {
			if a.fmTab.Get() == 0 {
				a.fmBeginEdit()
			}
		}),
		tui.OnStop(tui.Rune('w'), func(ke tui.KeyEvent) {
			if a.fmTab.Get() == 0 {
				a.fmSave()
			}
		}),
		tui.OnStop(tui.Rune('d'), func(ke tui.KeyEvent) {
			if a.fmTab.Get() != 0 {
				a.fmAskDelete()
			}
		}),
		tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.fmClose() }),
	}
}

// --- Buscador de Modrinth (R6) -----------------------------------------------
func (a *app) mrOpenPanel() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	inst := mgr.Instance()
	if _, ok := assets.PluginsDir(inst.Type); !ok {
		a.appendLog(inst.Name, "[mc-tui] vanilla servers do not support plugins/mods")
		return
	}
	a.mrGen.Update(func(g int) int { return g + 1 })
	a.mrQuery.Set("")
	a.mrResults.Set([]modrinth.Project{})
	a.mrIdx.Set(0)
	a.mrBusy.Set(false)
	a.mrMsg.Set("")
	a.mrTyping.Set(true)
	a.mrOpen.Set(true)
}

func (a *app) mrClose() {
	a.mrGen.Update(func(g int) int { return g + 1 })
	a.mrOpen.Set(false)
}

func (a *app) mrSearch() {
	mgr := a.current()
	query := a.mrQuery.Get()
	if mgr == nil || query == "" || a.mrBusy.Get() {
		return
	}
	inst := mgr.Instance()
	gen := a.mrGen.Get()
	a.mrBusy.Set(true)
	a.mrMsg.Set("Searching Modrinth...")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		results, err := a.mr.Search(ctx, query, inst.Type, inst.Version)
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
			a.mrMsg.Set(fmt.Sprintf("No results for %q compatible with %s %s", query, inst.Type, inst.Version))
			return
		}
		a.mrMsg.Set(fmt.Sprintf("%d results · Enter installs into the selected instance", len(results)))
	}()
}

func (a *app) mrInstall() {
	mgr := a.current()
	results := a.mrResults.Get()
	idx := a.mrIdx.Get()
	if mgr == nil || a.mrBusy.Get() || idx < 0 || idx >= len(results) {
		return
	}
	inst := mgr.Instance()
	project := results[idx]
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
		file, err := a.mr.LatestFile(ctx, project.ID, inst.Type, inst.Version)
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
		a.mrBusy.Set(false)
		if err != nil {
			a.mrMsg.Set("Error: " + err.Error())
			return
		}
		a.mrMsg.Set(fmt.Sprintf("Installed %s into %s/ · restart the server to load it", file.Filename, sub))
		a.appendLog(inst.Name, fmt.Sprintf("[mc-tui] Installed %s (%s)", project.Title, file.Filename))
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

func (a *app) mrItems() []wizItem {
	results := a.mrResults.Get()
	lines := make([]string, len(results))
	for i, p := range results {
		desc := p.Description
		if r := []rune(desc); len(r) > 50 {
			desc = string(r[:50]) + "…"
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

func (a *app) mrKeyMap() tui.KeyMap {
	esc := tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.mrClose() })
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
		esc,
	}
}

func (a *app) KeyMap() tui.KeyMap {
	if a.splash.Get() {
		dismiss := func(ke tui.KeyEvent) { a.splash.Set(false) }
		return tui.KeyMap{
			tui.OnStop(tui.AnyRune, dismiss),
			tui.OnStop(tui.KeyEnter, dismiss),
			tui.OnStop(tui.KeyEscape, dismiss),
		}
	}
	if a.wizStep.Get() != wizOff {
		return a.wizKeyMap()
	}
	if a.fmOpen.Get() {
		return a.fmKeyMap()
	}
	if a.mrOpen.Get() {
		return a.mrKeyMap()
	}
	if a.cmdActive.Get() {
		return tui.KeyMap{
			tui.OnStop(tui.AnyRune, a.appendCmdChar),
			tui.OnStop(tui.KeyBackspace, a.deleteCmdChar),
			tui.OnStop(tui.KeyEnter, a.submitCmd),
			tui.OnStop(tui.KeyEscape, a.closeCmd),
		}
	}
	if a.renActive.Get() {
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
	if a.delTarget.Get() != "" {
		return tui.KeyMap{
			tui.OnStop(tui.Rune('y'), func(ke tui.KeyEvent) { a.delDo() }),
			tui.OnStop(tui.Rune('n'), func(ke tui.KeyEvent) { a.delTarget.Set("") }),
			tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.delTarget.Set("") }),
		}
	}
	return tui.KeyMap{
		tui.On(tui.Rune('q'), func(ke tui.KeyEvent) { ke.App().Stop() }),
		tui.On(tui.Rune('c'), func(ke tui.KeyEvent) { a.cmdActive.Set(true) }),
		tui.On(tui.KeyEnter, func(ke tui.KeyEvent) { a.cmdActive.Set(true) }),
		tui.On(tui.Rune('n'), func(ke tui.KeyEvent) { a.wizOpen() }),
		tui.On(tui.Rune('e'), func(ke tui.KeyEvent) { a.fmOpenPanel() }),
		tui.On(tui.Rune('m'), func(ke tui.KeyEvent) { a.mrOpenPanel() }),
		tui.On(tui.Rune('R'), func(ke tui.KeyEvent) { a.renOpen() }),
		tui.On(tui.Rune('d'), func(ke tui.KeyEvent) { a.delAsk() }),
		tui.On(tui.KeyUp, func(ke tui.KeyEvent) { a.moveSelection(-1) }),
		tui.On(tui.KeyDown, func(ke tui.KeyEvent) { a.moveSelection(1) }),
		tui.On(tui.Rune('k'), func(ke tui.KeyEvent) { a.moveSelection(-1) }),
		tui.On(tui.Rune('j'), func(ke tui.KeyEvent) { a.moveSelection(1) }),
		tui.On(tui.Rune('s'), func(ke tui.KeyEvent) { a.startSelected() }),
		tui.On(tui.Rune('x'), func(ke tui.KeyEvent) { a.stopSelected() }),
		tui.On(tui.Rune('r'), func(ke tui.KeyEvent) { a.restartSelected() }),
	}
}

func (a *app) Watchers() []tui.Watcher {
	return []tui.Watcher{
		tui.OnTimer(500*time.Millisecond, a.refreshStatuses),
		tui.Watch(a.logCh, func(e logEntry) { a.appendLog(e.name, e.line) }),
	}
}

func (a *app) statusText(name string) string {
	st := a.statuses.Get()[name]
	if st == "" {
		st = server.Stopped
	}
	return string(st)
}

func (a *app) currentLogs() []string {
	mgr := a.current()
	if mgr == nil {
		return nil
	}
	return a.logs.Get()[mgr.Instance().Name]
}

func (a *app) currentName() string {
	mgr := a.current()
	if mgr == nil {
		return "no instance"
	}
	return mgr.Instance().Name
}

func (a *app) wizStepTitle() string {
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

templ (a *app) Render() {
	if a.splash.Get() {
		<div class="flex-col h-full items-center justify-center gap-1">
			<div class="flex-col">
				for _, row := range splashLogoRows() {
					<div class="flex">
						for _, seg := range row {
							if seg.Key == "g" {
								<span class="text-[#7cc65c]">{seg.Text}</span>
							} else if seg.Key == "G" {
								<span class="text-[#4a9e31]">{seg.Text}</span>
							} else if seg.Key == "d" {
								<span class="text-[#5c3d24]">{seg.Text}</span>
							} else if seg.Key == "b" {
								<span class="text-[#8b6244]">{seg.Text}</span>
							} else if seg.Key == "t" {
								<span class="text-[#a0764c]">{seg.Text}</span>
							} else {
								<span class="text-[#9a8f8a]">{seg.Text}</span>
							}
						}
					</div>
				}
			</div>
			<div class="flex-col">
				for _, line := range splashTitle {
					<span class="text-[#7cc65c]">{line}</span>
				}
			</div>
			<span class="font-dim">Press any key to start</span>
		</div>
	} else {
		<div class="flex-col h-full p-1 gap-1">
			<div class="flex justify-between shrink-0">
				<span class="font-bold text-cyan">mc-tui-server</span>
				<span class="font-dim">{fmt.Sprintf("%d instances", len(a.managers.Get()))}</span>
			</div>
			<div class="flex gap-1 flex-grow">
				<div class="flex-col border-rounded p-1 shrink-0" minWidth={30}>
					<span class="font-bold shrink-0">Instances</span>
					if len(a.managers.Get()) == 0 {
						<span class="font-dim">No instances yet.</span>
						<span class="font-dim">Press n to create one</span>
					}
					for i, mgr := range a.managers.Get() {
						<div class="flex-col">
							<div class="flex justify-between">
								if i == a.selected.Get() {
									<span class="font-bold text-cyan">{fmt.Sprintf("> %s", mgr.Instance().Name)}</span>
								} else {
									<span>{fmt.Sprintf("  %s", mgr.Instance().Name)}</span>
								}
								if a.statusText(mgr.Instance().Name) == string(server.Running) {
									<span class="text-green">{a.statusText(mgr.Instance().Name)}</span>
								} else if a.statusText(mgr.Instance().Name) == string(server.Stopping) {
									<span class="text-yellow">{a.statusText(mgr.Instance().Name)}</span>
								} else {
									<span class="font-dim">{a.statusText(mgr.Instance().Name)}</span>
								}
							</div>
							if a.metricText(mgr.Instance().Name) != "" {
								<span class="font-dim">{a.metricText(mgr.Instance().Name)}</span>
							}
						</div>
					}
				</div>
				if a.wizStep.Get() != wizOff {
					<div class="flex-col border-rounded border-cyan p-1 flex-grow">
						<span class="font-bold text-cyan shrink-0">{fmt.Sprintf("New instance — %s", a.wizStepTitle())}</span>
						if a.wizStep.Get() == wizType {
							for _, item := range a.wizTypeItems() {
								if item.Sel {
									<span class="font-bold text-cyan">{fmt.Sprintf("> %s", item.Text)}</span>
								} else {
									<span>{fmt.Sprintf("  %s", item.Text)}</span>
								}
							}
						}
						if a.wizStep.Get() == wizLoading {
							<span class="text-yellow">{a.wizMsg.Get()}</span>
						}
						if a.wizStep.Get() == wizVersion {
							<div
								class="flex-col flex-grow"
								scrollable={tui.ScrollVertical}
								scrollOffset={0, scrollTo(a.wizVerIdx.Get())}
							>
								for _, item := range a.wizVersionItems() {
									if item.Sel {
										<span class="font-bold text-cyan">{fmt.Sprintf("> %s", item.Text)}</span>
									} else {
										<span>{fmt.Sprintf("  %s", item.Text)}</span>
									}
								}
							</div>
						}
						if a.wizStep.Get() == wizName {
							<div class="flex gap-1">
								<span class="text-cyan font-bold">Name:</span>
								<span>{a.wizName.Get()}</span>
								<span class="text-cyan blink">_</span>
							</div>
							if a.wizMsg.Get() != "" {
								<span class="text-red">{a.wizMsg.Get()}</span>
							}
						}
						if a.wizStep.Get() == wizMem {
							<div class="flex gap-1">
								<span class="text-cyan font-bold">Memory (MB):</span>
								<span>{a.wizMemory.Get()}</span>
								<span class="text-cyan blink">_</span>
							</div>
						}
						if a.wizStep.Get() == wizEula {
							<span>To run the server you must accept the Minecraft EULA:</span>
							<span class="text-cyan">{"https://aka.ms/MinecraftEULA"}</span>
							<span class="font-bold">Do you accept?</span>
						}
						if a.wizStep.Get() == wizDownload {
							<span class="text-yellow">{a.wizMsg.Get()}</span>
						}
						if a.wizStep.Get() == wizError {
							<span class="text-red">{a.wizMsg.Get()}</span>
						}
						<div class="flex gap-1 shrink-0">
							for i, h := range a.wizHints() {
								if i > 0 {
									<span class="font-dim">|</span>
								}
								<span class="text-cyan font-bold">{h.K}</span>
								<span class="font-dim">{h.L}</span>
							}
						</div>
					</div>
				} else if a.fmOpen.Get() {
					<div class="flex-col border-rounded border-cyan p-1 flex-grow">
						<span class="font-bold text-cyan shrink-0">{a.fmTitle()}</span>
						<div class="flex gap-1 shrink-0">
							<span class="text-cyan font-bold">1</span>
							if a.fmTab.Get() == 0 {
								<span class="text-cyan">Properties</span>
							} else {
								<span class="font-dim">Properties</span>
							}
							<span class="text-cyan font-bold">2</span>
							if a.fmTab.Get() == 1 {
								<span class="text-cyan">Worlds</span>
							} else {
								<span class="font-dim">Worlds</span>
							}
							<span class="text-cyan font-bold">3</span>
							if a.fmTab.Get() == 2 {
								<span class="text-cyan">Plugins/Mods</span>
							} else {
								<span class="font-dim">Plugins/Mods</span>
							}
						</div>
						if len(a.fmItems()) == 0 {
							<span class="font-dim">(empty)</span>
						}
						<div
							class="flex-col flex-grow"
							scrollable={tui.ScrollVertical}
							scrollOffset={0, a.fmScrollY()}
						>
							for _, item := range a.fmItems() {
								if item.Sel {
									<span class="font-bold text-cyan">{fmt.Sprintf("> %s", item.Text)}</span>
								} else {
									<span>{fmt.Sprintf("  %s", item.Text)}</span>
								}
							}
						</div>
						if a.fmEditing.Get() {
							<div class="flex gap-1">
								<span class="text-cyan font-bold">{fmt.Sprintf("%s =", a.fmSelectedKey())}</span>
								<span>{a.fmEditText.Get()}</span>
								<span class="text-cyan blink">_</span>
								<span class="font-dim">(Enter applies | Esc cancels)</span>
							</div>
						}
						if a.fmConfirm.Get() != "" {
							<span class="text-red font-bold">{fmt.Sprintf("Delete %q permanently? (y = yes, n = no)", a.fmConfirm.Get())}</span>
						}
						if a.fmMsg.Get() != "" {
							<span class="text-yellow">{a.fmMsg.Get()}</span>
						}
						<div class="flex gap-1 shrink-0">
							for i, h := range a.fmHints() {
								if i > 0 {
									<span class="font-dim">|</span>
								}
								<span class="text-cyan font-bold">{h.K}</span>
								<span class="font-dim">{h.L}</span>
							}
						</div>
					</div>
				} else if a.mrOpen.Get() {
					<div class="flex-col border-rounded border-green p-1 flex-grow">
						<span class="font-bold text-green shrink-0">{fmt.Sprintf("Modrinth — %s", a.currentName())}</span>
						<div class="flex gap-1 shrink-0">
							<span class="text-green font-bold">Search:</span>
							<span>{a.mrQuery.Get()}</span>
							if a.mrTyping.Get() {
								<span class="text-green blink">_</span>
							}
						</div>
						<div
							class="flex-col flex-grow"
							scrollable={tui.ScrollVertical}
							scrollOffset={0, scrollTo(a.mrIdx.Get())}
						>
							for _, item := range a.mrItems() {
								if item.Sel {
									<span class="font-bold text-green">{fmt.Sprintf("> %s", item.Text)}</span>
								} else {
									<span>{fmt.Sprintf("  %s", item.Text)}</span>
								}
							}
						</div>
						if a.mrMsg.Get() != "" {
							<span class="text-yellow">{a.mrMsg.Get()}</span>
						}
						<div class="flex gap-1 shrink-0">
							for i, h := range a.mrHints() {
								if i > 0 {
									<span class="font-dim">|</span>
								}
								<span class="text-green font-bold">{h.K}</span>
								<span class="font-dim">{h.L}</span>
							}
						</div>
					</div>
				} else {
					<div
						class="flex-col border-rounded p-1 flex-grow"
						scrollable={tui.ScrollVertical}
						scrollOffset={0, math.MaxInt}
					>
						<span class="font-bold shrink-0">{fmt.Sprintf("Console — %s", a.currentName())}</span>
						for _, line := range a.currentLogs() {
							<span>{line}</span>
						}
					</div>
				}
			</div>
			if a.cmdActive.Get() {
				<div class="flex gap-1 shrink-0 px-1">
					<span class="text-cyan font-bold">{fmt.Sprintf("%s >", a.currentName())}</span>
					<span>{a.cmdText.Get()}</span>
					<span class="text-cyan blink">_</span>
					<span class="text-cyan font-bold">Enter</span>
					<span class="font-dim">sends</span>
					<span class="font-dim">|</span>
					<span class="text-cyan font-bold">Esc</span>
					<span class="font-dim">closes</span>
				</div>
			} else if a.renActive.Get() {
				<div class="flex gap-1 shrink-0 px-1">
					<span class="text-cyan font-bold">{fmt.Sprintf("Rename %s to:", a.currentName())}</span>
					<span>{a.renText.Get()}</span>
					<span class="text-cyan blink">_</span>
					if a.renMsg.Get() != "" {
						<span class="text-red">{a.renMsg.Get()}</span>
					}
					<span class="text-cyan font-bold">Enter</span>
					<span class="font-dim">applies</span>
					<span class="font-dim">|</span>
					<span class="text-cyan font-bold">Esc</span>
					<span class="font-dim">cancels</span>
				</div>
			} else if a.delTarget.Get() != "" {
				<div class="flex gap-1 shrink-0 px-1">
					<span class="text-red font-bold">{fmt.Sprintf("Delete instance %q and ALL its files (worlds included)?", a.delTarget.Get())}</span>
					<span class="text-red font-bold">y</span>
					<span class="font-dim">delete</span>
					<span class="font-dim">|</span>
					<span class="text-red font-bold">n/Esc</span>
					<span class="font-dim">keep</span>
				</div>
			} else {
				<div class="flex gap-1 shrink-0">
					for i, h := range a.mainHints() {
						if i > 0 {
							<span class="font-dim">|</span>
						}
						<span class="text-cyan font-bold">{h.K}</span>
						<span class="font-dim">{h.L}</span>
					}
				</div>
			}
		</div>
	}
}
