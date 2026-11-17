// doctor_panel.go: the doctor panel (F5). It runs the pre-flight checks of an
// instance in the background and lists what is missing before the server can
// start.
package main

import (
	"context"

	"github.com/JorMath/mc-tui-server/internal/doctor"
	tui "github.com/grindlemire/go-tui"
)

// docOpenPanel opens the doctor panel for the selected instance.
func (a *app) docOpenPanel() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	a.docName.Set(mgr.Instance().Name)
	a.docChecks.Set(nil)
	a.docScroll.Set(0)
	a.docMsg.Set("")
	a.docOpen.Set(true)
	a.docRunChecks()
}

// docRunChecks runs the checks of the open instance in the background. The
// instance is read again here so a re-run after a rename or a deletion is
// reported instead of touching a stale copy.
func (a *app) docRunChecks() {
	name := a.docName.Get()
	inst, ok := a.store.Get(name)
	if !ok {
		a.docClose()
		a.appendLog(name, "[mc-tui] Doctor: the instance is gone")
		return
	}

	a.docChecks.Set(nil)
	a.docScroll.Set(0)
	a.docMsg.Set("")
	a.docBusy.Set(true)
	gen := a.docGen.Get() + 1
	a.docGen.Set(gen)
	others := a.store.Instances()
	go func() {
		checks := doctor.Run(context.Background(), inst, others, doctor.Options{})
		if a.docGen.Get() != gen {
			return
		}
		a.docBusy.Set(false)
		a.docChecks.Set(checks)
	}()
}

// docClose closes the panel and invalidates its goroutines.
func (a *app) docClose() {
	a.docGen.Set(a.docGen.Get() + 1)
	a.docOpen.Set(false)
	a.docBusy.Set(false)
	a.docMsg.Set("")
	a.docScroll.Set(0)
	a.docChecks.Set(nil)
	a.docName.Set("")
}

// docScrollBy moves the list offset; positive scrolls towards the top.
func (a *app) docScrollBy(delta int) {
	lines := len(a.docChecks.Get())
	a.docScroll.Update(func(s int) int {
		s += delta
		if s < 0 {
			s = 0
		}
		if s > lines {
			s = lines
		}
		return s
	})
}

func (a *app) docHints() []hint {
	return []hint{
		{"↑/↓", "scroll"}, {"PgUp/PgDn", "page"}, {"r", "re-run"},
		{"Esc/D", "close"},
	}
}

// docKeyMap captures every key of the panel so no global shortcut fires
// behind it.
func (a *app) docKeyMap() tui.KeyMap {
	return tui.KeyMap{
		tui.OnStop(tui.KeyUp, func(ke tui.KeyEvent) { a.docScrollBy(1) }),
		tui.OnStop(tui.KeyDown, func(ke tui.KeyEvent) { a.docScrollBy(-1) }),
		tui.OnStop(tui.Rune('k'), func(ke tui.KeyEvent) { a.docScrollBy(1) }),
		tui.OnStop(tui.Rune('j'), func(ke tui.KeyEvent) { a.docScrollBy(-1) }),
		tui.OnStop(tui.KeyPageUp, func(ke tui.KeyEvent) { a.docScrollBy(10) }),
		tui.OnStop(tui.KeyPageDown, func(ke tui.KeyEvent) { a.docScrollBy(-10) }),
		tui.OnStop(tui.Rune('r'), func(ke tui.KeyEvent) { a.docRunChecks() }),
		tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.docClose() }),
		tui.OnStop(tui.Rune('D'), func(ke tui.KeyEvent) { a.docClose() }),
		tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {}),
	}
}
