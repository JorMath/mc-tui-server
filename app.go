// app.go define el estado central de la TUI: la struct app, su
// construcción, la selección de instancias, el log agregado y el enrutado
// global de teclado. Cada panel (wizard, archivos, Modrinth, gestión)
// vive en su propio archivo.
package main

import (
	"path/filepath"
	"time"

	"github.com/JorMath/mc-tui-server/internal/config"
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

type logEntry struct {
	name string
	line string
}

// listItem es una fila seleccionable de cualquier lista de la TUI.
type listItem struct {
	Text string
	Sel  bool
}

// fullItems marca la selección sobre la lista completa; el recorte lo
// hace el contenedor scrollable del render.
func fullItems(list []string, sel int) []listItem {
	items := make([]listItem, len(list))
	for i, s := range list {
		items[i] = listItem{Text: s, Sel: i == sel}
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

// hint es un atajo de teclado mostrado al pie: tecla en color + etiqueta.
type hint struct {
	K string
	L string
}

// newState crea un State y lo registra en reg para BindApp. La struct app
// vive fuera de app.gsx, así que el generador ya no emite la vinculación
// de estados: sin BindApp, Set() no marca dirty y la UI queda congelada
// (el splash nunca se descartaba en v0.1.1).
func newState[T any](reg *[]tui.AppBinder, v T) *tui.State[T] {
	s := tui.NewState(v)
	*reg = append(*reg, s)
	return s
}

type app struct {
	store   *config.Store
	dataDir string
	// binders acumula todos los State para vincularlos en BindApp.
	binders  []tui.AppBinder
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

	// Flujo de modpacks de Modrinth dentro del asistente (v0.1.2).
	wizPackQuery  *tui.State[string]
	wizPacks      *tui.State[[]modrinth.Project]
	wizPackIdx    *tui.State[int]
	wizPackVers   *tui.State[[]modrinth.PackVersion]
	wizPackVerIdx *tui.State[int]

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
	var reg []tui.AppBinder
	a := &app{
		store:     store,
		dataDir:   filepath.Dir(store.Path()),
		managers:  newState(&reg, managers),
		splash:    newState(&reg, true),
		logCh:     make(chan logEntry, 2048),
		selected:  newState(&reg, 0),
		statuses:  newState(&reg, statuses),
		logs:      newState(&reg, logs),
		cmdActive: newState(&reg, false),
		cmdText:   newState(&reg, ""),
		renActive: newState(&reg, false),
		renText:   newState(&reg, ""),
		renMsg:    newState(&reg, ""),
		delTarget: newState(&reg, ""),
		collector: metrics.NewCollector(),
		samples:   newState(&reg, map[string]metrics.Sample{}),
		lastPIDs:  map[string]int{},

		wizStep:     newState(&reg, wizOff),
		wizGen:      newState(&reg, 0),
		wizTypeIdx:  newState(&reg, 0),
		wizVersions: newState(&reg, []string{}),
		wizVerIdx:   newState(&reg, 0),
		wizName:     newState(&reg, ""),
		wizMemory:   newState(&reg, ""),
		wizMsg:      newState(&reg, ""),

		wizPackQuery:  newState(&reg, ""),
		wizPacks:      newState(&reg, []modrinth.Project{}),
		wizPackIdx:    newState(&reg, 0),
		wizPackVers:   newState(&reg, []modrinth.PackVersion{}),
		wizPackVerIdx: newState(&reg, 0),

		fmOpen:      newState(&reg, false),
		fmTab:       newState(&reg, 0),
		fmProps:     &properties.File{},
		fmRev:       newState(&reg, 0),
		fmPropsIdx:  newState(&reg, 0),
		fmEditing:   newState(&reg, false),
		fmEditText:  newState(&reg, ""),
		fmDirty:     newState(&reg, false),
		fmWorlds:    newState(&reg, []string{}),
		fmPlugins:   newState(&reg, []string{}),
		fmWorldIdx:  newState(&reg, 0),
		fmPluginIdx: newState(&reg, 0),
		fmConfirm:   newState(&reg, ""),
		fmMsg:       newState(&reg, ""),

		mr:        &modrinth.Client{},
		mrOpen:    newState(&reg, false),
		mrTyping:  newState(&reg, false),
		mrQuery:   newState(&reg, ""),
		mrResults: newState(&reg, []modrinth.Project{}),
		mrIdx:     newState(&reg, 0),
		mrBusy:    newState(&reg, false),
		mrGen:     newState(&reg, 0),
		mrMsg:     newState(&reg, ""),
	}
	a.binders = reg
	for _, m := range managers {
		a.pumpLogs(m)
	}
	return a
}

// BindApp vincula todos los State a la tui.App para que Set() marque dirty
// y dispare el re-render. Lo llama SetRootComponent al arrancar; el
// generador lo emitía cuando la struct app vivía dentro de app.gsx.
func (a *app) BindApp(app *tui.App) {
	for _, b := range a.binders {
		b.BindApp(app)
	}
}

var _ tui.AppBinder = (*app)(nil)

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

func (a *app) currentName() string {
	mgr := a.current()
	if mgr == nil {
		return "no instance"
	}
	return mgr.Instance().Name
}

func (a *app) currentLogs() []string {
	mgr := a.current()
	if mgr == nil {
		return nil
	}
	return a.logs.Get()[mgr.Instance().Name]
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

func (a *app) mainHints() []hint {
	return []hint{
		{"↑/↓", "select"}, {"s", "start"}, {"x", "stop"}, {"r", "restart"},
		{"c/Enter", "command"}, {"e", "files"}, {"m", "modrinth"},
		{"n", "new"}, {"R", "rename"}, {"d", "delete"}, {"q", "quit"},
	}
}

// KeyMap enruta el teclado según el modo activo: cada panel o barra modal
// captura todas las teclas (OnStop) para no disparar atajos globales.
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
		return a.renKeyMap()
	}
	if a.delTarget.Get() != "" {
		return a.delKeyMap()
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
