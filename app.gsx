package main

import (
	"fmt"
	"math"
	"mc-tui-server/internal/server"
	"time"
	tui "github.com/grindlemire/go-tui"
)

const (
	stopTimeout = 30 * time.Second
	maxLogLines = 5000
)

type app struct {
	managers []*server.Manager
	selected *tui.State[int]
	statuses *tui.State[map[string]server.Status]
	logs     *tui.State[map[string][]string]
}

func App(managers []*server.Manager) *app {
	statuses := map[string]server.Status{}
	logs := map[string][]string{}
	for _, m := range managers {
		statuses[m.Instance().Name] = m.Status()
		logs[m.Instance().Name] = nil
	}
	return &app{
		managers: managers,
		selected: tui.NewState(0),
		statuses: tui.NewState(statuses),
		logs:     tui.NewState(logs),
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

func (a *app) KeyMap() tui.KeyMap {
	return tui.KeyMap{
		tui.On(tui.Rune('q'), func(ke tui.KeyEvent) { ke.App().Stop() }),
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
					<div class="flex justify-between">
						<span class={a.rowClass(i)}>{mgr.Instance().Name}</span>
						<span class={a.statusClass(mgr.Instance().Name)}>{a.statusText(mgr.Instance().Name)}</span>
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
		<span class="font-dim shrink-0">↑/↓ seleccionar | s iniciar | x detener | r reiniciar | q salir</span>
	</div>
}
