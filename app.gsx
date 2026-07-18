package main

import (
	"context"
	"fmt"
	"math"
	"mc-tui-server/internal/assets"
	"mc-tui-server/internal/config"
	"mc-tui-server/internal/download"
	"mc-tui-server/internal/metrics"
	"mc-tui-server/internal/properties"
	"mc-tui-server/internal/server"
	"os"
	"path/filepath"
	"strconv"
	"time"
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

// renderWord compone una palabra duplicando cada celda en horizontal
// (píxeles de 2 columnas, como los del creeper).
func renderWord(word string) []string {
	rows := make([]string, 5)
	for r := 0; r < 5; r++ {
		for i, ch := range word {
			if i > 0 {
				rows[r] += "  "
			}
			for _, c := range splashFont[ch][r] {
				if c == '█' {
					rows[r] += "██"
				} else {
					rows[r] += "  "
				}
			}
		}
	}
	return rows
}

var splashTitle = append(append(renderWord("MC-TUI"), ""), renderWord("SERVER")...)

var splashCreeper = []string{
	"  ████    ████  ",
	"  ████    ████  ",
	"      ████      ",
	"    ████████    ",
	"    ████████    ",
	"    ██    ██    ",
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
	}
	for _, m := range managers {
		a.pumpLogs(m)
	}
	return a
}

// pumpLogs reenvía los logs de un manager al canal agregado de la app.
func (a *app) pumpLogs(m *server.Manager) {
	name := m.Instance().Name
	go func() {
		for line := range m.Logs() {
			a.logCh <- logEntry{name: name, line: line}
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

// wizVersionItems devuelve una ventana de versiones alrededor de la selección.
func (a *app) wizVersionItems() []wizItem {
	return windowItems(a.wizVersions.Get(), a.wizVerIdx.Get(), 12)
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

// windowItems devuelve una ventana de elementos alrededor de la selección.
func windowItems(list []string, sel, window int) []wizItem {
	start := sel - window/2
	if start < 0 {
		start = 0
	}
	end := start + window
	if end > len(list) {
		end = len(list)
		if start = end - window; start < 0 {
			start = 0
		}
	}
	items := make([]wizItem, 0, end-start)
	for i := start; i < end; i++ {
		items = append(items, wizItem{Text: list[i], Sel: i == sel})
	}
	return items
}

func (a *app) fmItems() []wizItem {
	switch a.fmTab.Get() {
	case 0:
		return windowItems(a.fmPropLines(), a.fmPropsIdx.Get(), 16)
	case 1:
		return windowItems(a.fmWorlds.Get(), a.fmWorldIdx.Get(), 16)
	default:
		return windowItems(a.fmPlugins.Get(), a.fmPluginIdx.Get(), 16)
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

func (a *app) fmHelp() string {
	if a.fmTab.Get() == 0 {
		return "↑/↓ select | Enter edit | w save | 1/2/3 or Tab switch | Esc close"
	}
	return "↑/↓ select | d delete | 1/2/3 or Tab switch | Esc close"
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
	if a.cmdActive.Get() {
		return tui.KeyMap{
			tui.OnStop(tui.AnyRune, a.appendCmdChar),
			tui.OnStop(tui.KeyBackspace, a.deleteCmdChar),
			tui.OnStop(tui.KeyEnter, a.submitCmd),
			tui.OnStop(tui.KeyEscape, a.closeCmd),
		}
	}
	return tui.KeyMap{
		tui.On(tui.Rune('q'), func(ke tui.KeyEvent) { ke.App().Stop() }),
		tui.On(tui.Rune('c'), func(ke tui.KeyEvent) { a.cmdActive.Set(true) }),
		tui.On(tui.KeyEnter, func(ke tui.KeyEvent) { a.cmdActive.Set(true) }),
		tui.On(tui.Rune('n'), func(ke tui.KeyEvent) { a.wizOpen() }),
		tui.On(tui.Rune('e'), func(ke tui.KeyEvent) { a.fmOpenPanel() }),
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

func (a *app) rowClass(i int) string {
	if i == a.selected.Get() {
		return "font-bold text-cyan"
	}
	return ""
}

func (a *app) statusClass(name string) string {
	switch a.statuses.Get()[name] {
	case server.Running:
		return "text-green"
	case server.Stopping:
		return "text-yellow"
	default:
		return "font-dim"
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
				for _, line := range splashCreeper {
					<span class="text-green font-bold">{line}</span>
				}
			</div>
			<div class="flex-col">
				for _, line := range splashTitle {
					<span class="text-green">{line}</span>
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
								<span class={a.rowClass(i)}>{mgr.Instance().Name}</span>
								<span class={a.statusClass(mgr.Instance().Name)}>{a.statusText(mgr.Instance().Name)}</span>
							</div>
							if a.metricText(mgr.Instance().Name) != "" {
								<span class="font-dim">{a.metricText(mgr.Instance().Name)}</span>
							}
						</div>
					}
				</div>
				if a.wizStep.Get() != wizOff {
					<div class="flex-col border-rounded border-cyan p-1 flex-grow gap-1">
						<span class="font-bold text-cyan shrink-0">{fmt.Sprintf("New instance — %s", a.wizStepTitle())}</span>
						if a.wizStep.Get() == wizType {
							for _, item := range a.wizTypeItems() {
								if item.Sel {
									<span class="font-bold text-cyan">{fmt.Sprintf("> %s", item.Text)}</span>
								} else {
									<span>{fmt.Sprintf("  %s", item.Text)}</span>
								}
							}
							<span class="font-dim">↑/↓ choose | Enter continue | Esc cancel</span>
						}
						if a.wizStep.Get() == wizLoading {
							<span class="text-yellow">{a.wizMsg.Get()}</span>
							<span class="font-dim">Esc cancel</span>
						}
						if a.wizStep.Get() == wizVersion {
							for _, item := range a.wizVersionItems() {
								if item.Sel {
									<span class="font-bold text-cyan">{fmt.Sprintf("> %s", item.Text)}</span>
								} else {
									<span>{fmt.Sprintf("  %s", item.Text)}</span>
								}
							}
							<span class="font-dim">↑/↓/PgUp/PgDn choose | Enter continue | Esc cancel</span>
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
							<span class="font-dim">letters, digits, - and _ | Enter continue | Esc cancel</span>
						}
						if a.wizStep.Get() == wizMem {
							<div class="flex gap-1">
								<span class="text-cyan font-bold">Memory (MB):</span>
								<span>{a.wizMemory.Get()}</span>
								<span class="text-cyan blink">_</span>
							</div>
							<span class="font-dim">empty = 2048 | Enter continue | Esc cancel</span>
						}
						if a.wizStep.Get() == wizEula {
							<span>To run the server you must accept the Minecraft EULA:</span>
							<span class="text-cyan">{"https://aka.ms/MinecraftEULA"}</span>
							<span class="font-bold">Accept? (y = yes, download | n/Esc = cancel)</span>
						}
						if a.wizStep.Get() == wizDownload {
							<span class="text-yellow">{a.wizMsg.Get()}</span>
						}
						if a.wizStep.Get() == wizError {
							<span class="text-red">{a.wizMsg.Get()}</span>
							<span class="font-dim">Esc close</span>
						}
					</div>
				} else if a.fmOpen.Get() {
					<div class="flex-col border-rounded border-cyan p-1 flex-grow">
						<span class="font-bold text-cyan shrink-0">{a.fmTitle()}</span>
						<span class="font-dim shrink-0">1 Properties | 2 Worlds | 3 Plugins/Mods</span>
						if len(a.fmItems()) == 0 {
							<span class="font-dim">(empty)</span>
						}
						for _, item := range a.fmItems() {
							if item.Sel {
								<span class="font-bold text-cyan">{fmt.Sprintf("> %s", item.Text)}</span>
							} else {
								<span>{fmt.Sprintf("  %s", item.Text)}</span>
							}
						}
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
						<span class="font-dim shrink-0">{a.fmHelp()}</span>
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
					<span class="font-dim">(Enter sends | Esc closes)</span>
				</div>
			} else {
				<span class="font-dim shrink-0">↑/↓ select | s start | x stop | r restart | c/Enter command | e files | n new | q quit</span>
			}
		</div>
	}
}
