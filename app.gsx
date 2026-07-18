package main

import (
	"context"
	"fmt"
	"math"
	"mc-tui-server/internal/config"
	"mc-tui-server/internal/download"
	"mc-tui-server/internal/metrics"
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

type logEntry struct {
	name string
	line string
}

type app struct {
	store    *config.Store
	dataDir  string
	managers *tui.State[[]*server.Manager]
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
	a.wizMsg.Set(fmt.Sprintf("Consultando versiones de %s...", typ))
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
			a.wizFail(gen, fmt.Errorf("la API de %s no devolvió versiones", typ))
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
		a.wizMsg.Set("El nombre no puede estar vacío")
		return
	}
	if _, exists := a.store.Get(name); exists {
		a.wizMsg.Set(fmt.Sprintf("Ya existe una instancia llamada %q", name))
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
	a.wizMsg.Set("Resolviendo URL de descarga...")
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
				a.wizMsg.Set(fmt.Sprintf("Descargando... %d%% (%dMB de %dMB)",
					done*100/total, done/(1024*1024), total/(1024*1024)))
			} else {
				a.wizMsg.Set(fmt.Sprintf("Descargando... %dMB", done/(1024*1024)))
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
		a.appendLog(name, fmt.Sprintf("[mc-tui] Instancia creada: %s %s (%d MB)", typ, version, memMB))
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
	const window = 12
	versions := a.wizVersions.Get()
	sel := a.wizVerIdx.Get()
	start := sel - window/2
	if start < 0 {
		start = 0
	}
	end := start + window
	if end > len(versions) {
		end = len(versions)
		if start = end - window; start < 0 {
			start = 0
		}
	}
	items := make([]wizItem, 0, end-start)
	for i := start; i < end; i++ {
		items = append(items, wizItem{Text: versions[i], Sel: i == sel})
	}
	return items
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
			tui.OnStop(tui.Rune('s'), func(ke tui.KeyEvent) { a.wizStartDownload() }),
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

func (a *app) KeyMap() tui.KeyMap {
	if a.wizStep.Get() != wizOff {
		return a.wizKeyMap()
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
		return "sin instancia"
	}
	return mgr.Instance().Name
}

func (a *app) wizStepTitle() string {
	switch a.wizStep.Get() {
	case wizType:
		return "1/5 · Tipo de servidor"
	case wizLoading:
		return "2/5 · Consultando versiones"
	case wizVersion:
		return "2/5 · Versión"
	case wizName:
		return "3/5 · Nombre de la instancia"
	case wizMem:
		return "4/5 · Memoria (MB)"
	case wizEula:
		return "5/5 · EULA de Minecraft"
	case wizDownload:
		return "Descargando"
	default:
		return "Error"
	}
}

templ (a *app) Render() {
	<div class="flex-col h-full p-1 gap-1">
		<div class="flex justify-between shrink-0">
			<span class="font-bold text-cyan">mc-tui-server</span>
			<span class="font-dim">{fmt.Sprintf("%d instancias", len(a.managers.Get()))}</span>
		</div>
		<div class="flex gap-1 flex-grow">
			<div class="flex-col border-rounded p-1 shrink-0" minWidth={30}>
				<span class="font-bold shrink-0">Instancias</span>
				if len(a.managers.Get()) == 0 {
					<span class="font-dim">No hay instancias.</span>
					<span class="font-dim">Pulsa n para crear una</span>
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
					<span class="font-bold text-cyan shrink-0">{fmt.Sprintf("Nueva instancia — %s", a.wizStepTitle())}</span>
					if a.wizStep.Get() == wizType {
						for _, item := range a.wizTypeItems() {
							if item.Sel {
								<span class="font-bold text-cyan">{fmt.Sprintf("> %s", item.Text)}</span>
							} else {
								<span>{fmt.Sprintf("  %s", item.Text)}</span>
							}
						}
						<span class="font-dim">↑/↓ elegir | Enter continuar | Esc cancelar</span>
					}
					if a.wizStep.Get() == wizLoading {
						<span class="text-yellow">{a.wizMsg.Get()}</span>
						<span class="font-dim">Esc cancelar</span>
					}
					if a.wizStep.Get() == wizVersion {
						for _, item := range a.wizVersionItems() {
							if item.Sel {
								<span class="font-bold text-cyan">{fmt.Sprintf("> %s", item.Text)}</span>
							} else {
								<span>{fmt.Sprintf("  %s", item.Text)}</span>
							}
						}
						<span class="font-dim">↑/↓/PgUp/PgDn elegir | Enter continuar | Esc cancelar</span>
					}
					if a.wizStep.Get() == wizName {
						<div class="flex gap-1">
							<span class="text-cyan font-bold">Nombre:</span>
							<span>{a.wizName.Get()}</span>
							<span class="text-cyan blink">_</span>
						</div>
						if a.wizMsg.Get() != "" {
							<span class="text-red">{a.wizMsg.Get()}</span>
						}
						<span class="font-dim">letras, números, - y _ | Enter continuar | Esc cancelar</span>
					}
					if a.wizStep.Get() == wizMem {
						<div class="flex gap-1">
							<span class="text-cyan font-bold">Memoria (MB):</span>
							<span>{a.wizMemory.Get()}</span>
							<span class="text-cyan blink">_</span>
						</div>
						<span class="font-dim">vacío = 2048 | Enter continuar | Esc cancelar</span>
					}
					if a.wizStep.Get() == wizEula {
						<span>Para ejecutar el servidor debes aceptar el EULA de Minecraft:</span>
						<span class="text-cyan">{"https://aka.ms/MinecraftEULA"}</span>
						<span class="font-bold">¿Aceptas? (s = sí y descargar, n/Esc = cancelar)</span>
					}
					if a.wizStep.Get() == wizDownload {
						<span class="text-yellow">{a.wizMsg.Get()}</span>
					}
					if a.wizStep.Get() == wizError {
						<span class="text-red">{a.wizMsg.Get()}</span>
						<span class="font-dim">Esc cerrar</span>
					}
				</div>
			} else {
				<div
					class="flex-col border-rounded p-1 flex-grow"
					scrollable={tui.ScrollVertical}
					scrollOffset={0, math.MaxInt}
				>
					<span class="font-bold shrink-0">{fmt.Sprintf("Consola — %s", a.currentName())}</span>
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
				<span class="font-dim">(Enter envía | Esc cierra)</span>
			</div>
		} else {
			<span class="font-dim shrink-0">↑/↓ seleccionar | s iniciar | x detener | r reiniciar | c/Enter comando | n nueva | q salir</span>
		}
	</div>
}
