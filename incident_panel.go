// incident_panel.go: the incident panel (F5). It explains the last crash of
// the selected instance, shows the excerpt the server wrote and, only after
// an explicit confirmation, uploads the evidence to mclo.gs.
package main

import (
	"context"
	"os"

	"github.com/JorMath/mc-tui-server/internal/incident"
	tui "github.com/grindlemire/go-tui"
)

// incUploadWarning is the confirmation line shown before anything leaves the
// machine. It is deliberately plain: the log is public once uploaded.
const incUploadWarning = "This uploads the log to mclo.gs, a public paste service. It can contain local paths and IP addresses."

// incOpenPanel opens the incident panel for the selected instance and loads
// its evidence in the background.
func (a *app) incOpenPanel() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	inst := mgr.Instance()
	a.incName.Set(inst.Name)
	a.incReport.Set(incident.Report{})
	a.incFound.Set(false)
	a.incScroll.Set(0)
	a.incMsg.Set("")
	a.incURL.Set("")
	a.incConfirm.Set(false)
	a.incBusy.Set(true)
	a.incOpen.Set(true)

	gen := a.incGen.Get() + 1
	a.incGen.Set(gen)
	dir := inst.Dir
	go func() {
		rep, found, err := incident.Latest(context.Background(), dir)
		if a.incGen.Get() != gen {
			return
		}
		a.incBusy.Set(false)
		if err != nil {
			a.incMsg.Set("Error: " + err.Error())
			return
		}
		a.incFound.Set(found)
		if found {
			a.incReport.Set(rep)
		}
	}()
}

// incClose closes the panel and invalidates its goroutines.
func (a *app) incClose() {
	a.incGen.Set(a.incGen.Get() + 1)
	a.incOpen.Set(false)
	a.incFound.Set(false)
	a.incBusy.Set(false)
	a.incConfirm.Set(false)
	a.incMsg.Set("")
	a.incURL.Set("")
	a.incScroll.Set(0)
	a.incReport.Set(incident.Report{})
	a.incName.Set("")
}

// incScrollBy moves the excerpt offset; positive scrolls towards the top.
func (a *app) incScrollBy(delta int) {
	lines := len(a.incReport.Get().Lines)
	a.incScroll.Update(func(s int) int {
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

// incAskUpload shows the confirmation line. Nothing is uploaded until the
// user answers y.
func (a *app) incAskUpload() {
	if a.incBusy.Get() || !a.incFound.Get() {
		return
	}
	a.incMsg.Set("")
	a.incConfirm.Set(true)
}

// incUpload sends the evidence to the paste service in the background.
func (a *app) incUpload() {
	a.incConfirm.Set(false)
	path := incidentContentPath(a.incReport.Get())
	if path == "" {
		a.incMsg.Set("There is no crash evidence to upload")
		return
	}

	gen := a.incGen.Get() + 1
	a.incGen.Set(gen)
	a.incBusy.Set(true)
	a.incMsg.Set("")
	a.incURL.Set("")
	client := a.incClient
	base := a.incBaseURL
	go func() {
		content, err := os.ReadFile(path)
		var url string
		if err == nil {
			url, err = incident.Upload(context.Background(), client, base, string(content))
		}
		if a.incGen.Get() != gen {
			return
		}
		a.incBusy.Set(false)
		if err != nil {
			a.incMsg.Set("Error: " + err.Error())
			return
		}
		a.incURL.Set(url)
	}()
}

// incidentContentPath picks the file to upload: the crash report is the
// primary evidence and the log is the fallback.
func incidentContentPath(report incident.Report) string {
	if report.Path != "" {
		return report.Path
	}
	return report.LogPath
}

// incStatusText names the operation that is running in the background.
func (a *app) incStatusText() string {
	if a.incFound.Get() {
		return "Uploading..."
	}
	return "Reading crash evidence..."
}

func (a *app) incHints() []hint {
	if a.incConfirm.Get() {
		return []hint{{"y", "upload"}, {"n/Esc", "cancel"}}
	}
	return []hint{
		{"↑/↓", "scroll"}, {"PgUp/PgDn", "page"}, {"u", "upload to mclo.gs"},
		{"Esc/i", "close"},
	}
}

// incKeyMap captures every key of the panel so no global shortcut fires
// behind it.
func (a *app) incKeyMap() tui.KeyMap {
	if a.incConfirm.Get() {
		return tui.KeyMap{
			tui.OnStop(tui.Rune('y'), func(ke tui.KeyEvent) { a.incUpload() }),
			tui.OnStop(tui.Rune('n'), func(ke tui.KeyEvent) { a.incConfirm.Set(false) }),
			tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.incConfirm.Set(false) }),
			tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {}),
		}
	}
	return tui.KeyMap{
		tui.OnStop(tui.KeyUp, func(ke tui.KeyEvent) { a.incScrollBy(1) }),
		tui.OnStop(tui.KeyDown, func(ke tui.KeyEvent) { a.incScrollBy(-1) }),
		tui.OnStop(tui.Rune('k'), func(ke tui.KeyEvent) { a.incScrollBy(1) }),
		tui.OnStop(tui.Rune('j'), func(ke tui.KeyEvent) { a.incScrollBy(-1) }),
		tui.OnStop(tui.KeyPageUp, func(ke tui.KeyEvent) { a.incScrollBy(10) }),
		tui.OnStop(tui.KeyPageDown, func(ke tui.KeyEvent) { a.incScrollBy(-10) }),
		tui.OnStop(tui.Rune('u'), func(ke tui.KeyEvent) { a.incAskUpload() }),
		tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.incClose() }),
		tui.OnStop(tui.Rune('i'), func(ke tui.KeyEvent) { a.incClose() }),
		tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {}),
	}
}
