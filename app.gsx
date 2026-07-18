package main

import (
	"fmt"
	"math"
	"mc-tui-server/internal/metrics"
	"mc-tui-server/internal/server"
	"time"
	tui "github.com/grindlemire/go-tui"
)

const (
	stopTimeout = 30 * time.Second
	maxLogLines = 5000
)

type app struct {
	managers  []*server.Manager
	selected  *tui.State[int]
	statuses  *tui.State[map[string]server.Status]
	logs      *tui.State[map[string][]string]
	cmdActive *tui.State[bool]
	cmdText   *tui.State[string]

	collector *metrics.Collector
	samples   *tui.State[map[string]metrics.Sample]
	// lastPIDs solo se toca desde el timer de refresh (una goroutine).
	lastPIDs map[string]int
}

func App(managers []*server.Manager) *app {
	statuses := map[string]server.Status{}
	logs := map[string][]string{}
	for _, m := range managers {
		statuses[m.Instance().Name] = m.Status()
		logs[m.Instance().Name] = nil
	}
	return &app{
		managers:  managers,
		selected:  tui.NewState(0),
		statuses:  tui.NewState(statuses),
		logs:      tui.NewState(logs),
		cmdActive: tui.NewState(false),
		cmdText:   tui.NewState(""),
		collector: metrics.NewCollector(),
		samples:   tui.NewState(map[string]metrics.Sample{}),
		lastPIDs:  map[string]int{},
	}
}

func (a *app) current() *server.Manager {
	i := a.selected.Get()
	if i < 0 || i >= len(a.managers) {
		return nil
	}
	return a.managers[i]
}

func (a *app) moveSelection(delta int) {
	if len(a.managers) == 0 {
		return
	}
	a.selected.Update(func(i int) int {
		i += delta
		if i < 0 {
			i = 0
		}
		if i >= len(a.managers) {
			i = len(a.managers) - 1
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
		for _, mgr := range a.managers {
			m[mgr.Instance().Name] = mgr.Status()
		}
		return m
	})
	a.refreshSamples()
}

// refreshSamples muestrea CPU/RAM (R5) de las instancias corriendo.
func (a *app) refreshSamples() {
	a.samples.Update(func(m map[string]metrics.Sample) map[string]metrics.Sample {
		for _, mgr := range a.managers {
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

func (a *app) KeyMap() tui.KeyMap {
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
	watchers := []tui.Watcher{
		tui.OnTimer(500*time.Millisecond, a.refreshStatuses),
	}
	for _, m := range a.managers {
		mgr := m
		watchers = append(watchers, tui.Watch(mgr.Logs(), func(line string) {
			a.appendLog(mgr.Instance().Name, line)
		}))
	}
	return watchers
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

templ (a *app) Render() {
	<div class="flex-col h-full p-1 gap-1">
		<div class="flex justify-between shrink-0">
			<span class="font-bold text-cyan">mc-tui-server</span>
			<span class="font-dim">{fmt.Sprintf("%d instancias", len(a.managers))}</span>
		</div>
		<div class="flex gap-1 flex-grow">
			<div class="flex-col border-rounded p-1 shrink-0" minWidth={30}>
				<span class="font-bold shrink-0">Instancias</span>
				if len(a.managers) == 0 {
					<span class="font-dim">No hay instancias.</span>
					<span class="font-dim">Agrega una en instances.json</span>
				}
				for i, mgr := range a.managers {
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
		</div>
		if a.cmdActive.Get() {
			<div class="flex gap-1 shrink-0 px-1">
				<span class="text-cyan font-bold">{fmt.Sprintf("%s >", a.currentName())}</span>
				<span>{a.cmdText.Get()}</span>
				<span class="text-cyan blink">_</span>
				<span class="font-dim">(Enter envía | Esc cierra)</span>
			</div>
		} else {
			<span class="font-dim shrink-0">↑/↓ seleccionar | s iniciar | x detener | r reiniciar | c/Enter comando | q salir</span>
		}
	</div>
}
