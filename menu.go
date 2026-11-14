// menu.go: contextual actions menu (F3). Enter on the main screen opens a
// menu that groups the actions which used to be global single-letter
// shortcuts; disabled items stay selectable so the footer can explain why
// they cannot run.
package main

import (
	"github.com/JorMath/mc-tui-server/internal/server"
	tui "github.com/grindlemire/go-tui"
)

// menuItem is one row of the actions menu. Section marks the group; the
// view renders a heading whenever it changes. Disabled keeps the row
// selectable: Enter shows Reason instead of running Run.
type menuItem struct {
	Section  string
	Label    string
	Desc     string
	Disabled bool
	Reason   string
	Run      func()
}

// menuSections are the menu groups, in display order.
const (
	menuSectionLifecycle   = "Lifecycle"
	menuSectionContent     = "Content"
	menuSectionMaintenance = "Maintenance"
	menuSectionInstance    = "Instance"
)

// menuItems builds the menu for the selected instance. Enablement uses the
// Manager status rather than the UI status map, so it never depends on the
// last refresh tick.
func (a *app) menuItems() []menuItem {
	mgr := a.current()
	if mgr == nil {
		return nil
	}
	inst := mgr.Instance()
	st := mgr.Status()
	stopped := st == server.Stopped || st == server.Crashed

	startDisabled := !stopped
	startReason := ""
	switch st {
	case server.Running:
		startReason = "The server is already running"
	case server.Stopping:
		startReason = "The server is stopping"
	}

	stopDisabled := st != server.Running
	stopReason := "The server is not running"
	if st == server.Stopping {
		stopReason = "The server is stopping"
	}

	autoLabel := "Auto-restart off"
	if a.autoRestartOn(inst.Name) {
		autoLabel = "Auto-restart on"
	}

	return []menuItem{
		{Section: menuSectionLifecycle, Label: "Start", Desc: "Start the selected server",
			Disabled: startDisabled, Reason: startReason, Run: a.startSelected},
		{Section: menuSectionLifecycle, Label: "Stop", Desc: "Stop the server gracefully",
			Disabled: stopDisabled, Reason: stopReason, Run: a.stopSelected},
		{Section: menuSectionLifecycle, Label: "Restart", Desc: "Restart the server",
			Disabled: st == server.Stopping, Reason: "The server is stopping", Run: a.restartSelected},
		{Section: menuSectionLifecycle, Label: "Send command", Desc: "Open the command bar for this server",
			Disabled: stopDisabled, Reason: stopReason, Run: a.menuSendCommand},

		{Section: menuSectionContent, Label: "Files", Desc: "Properties, worlds, plugins, backups and logs",
			Run: a.fmOpenPanel},
		{Section: menuSectionContent, Label: "Modrinth", Desc: "Install mods, plugins and datapacks",
			Run: a.mrOpenPanel},
		{Section: menuSectionContent, Label: "Players", Desc: "Whitelist, ops and bans",
			Run: a.plOpenPanel},

		{Section: menuSectionMaintenance, Label: "Backup world", Desc: "Zip the active world into backups/",
			Disabled: !stopped, Reason: "Stop the server first", Run: a.backupWorld},
		{Section: menuSectionMaintenance, Label: "Scheduler", Desc: "Automatic backups and a daily restart",
			Run: a.schOpen},
		{Section: menuSectionMaintenance, Label: "Update server software", Desc: "Swap the jar or update the modpack version",
			Disabled: !stopped, Reason: "Stop the server first", Run: a.updateAsk},
		{Section: menuSectionMaintenance, Label: "Java runtime", Desc: "System, managed Temurin or a manual path",
			Disabled: !stopped, Reason: "Stop the server first", Run: a.javaAsk},

		{Section: menuSectionInstance, Label: autoLabel, Desc: "Toggle auto-restart after a crash",
			Run: a.toggleAutoRestart},
		{Section: menuSectionInstance, Label: "Clone", Desc: "Copy the instance into a sandbox instance",
			Disabled: st != server.Stopped, Reason: "Stop the server first", Run: a.cloneOpen},
		{Section: menuSectionInstance, Label: "Rename", Desc: "Rename the instance",
			Disabled: st != server.Stopped, Reason: "Stop the server first", Run: a.renOpen},
		{Section: menuSectionInstance, Label: "Memory", Desc: "Change the memory (MB, applies on next start)",
			Disabled: st != server.Stopped, Reason: "Stop the server first", Run: a.memOpen},
		{Section: menuSectionInstance, Label: "Delete", Desc: "Delete the instance and all its files",
			Disabled: st != server.Stopped, Reason: "Stop the server first", Run: a.delAsk},
	}
}

// menuSendCommand opens the command bar for the selected instance.
func (a *app) menuSendCommand() {
	a.cmdActive.Set(true)
}

// menuRow is a renderable row: Heading is only set on the first row of each
// section.
type menuRow struct {
	Heading string
	Item    menuItem
	Sel     bool
}

// menuRows flattens the menu for the view, marking headings and selection.
func (a *app) menuRows() []menuRow {
	items := a.menuItems()
	rows := make([]menuRow, 0, len(items))
	sel := a.menuIdx.Get()
	prev := ""
	for i, it := range items {
		row := menuRow{Item: it, Sel: i == sel}
		if it.Section != prev {
			row.Heading = it.Section
			prev = it.Section
		}
		rows = append(rows, row)
	}
	return rows
}

// openMenu opens the menu on the first action. Without a selected instance
// there is nothing to show.
func (a *app) openMenu() {
	if a.current() == nil {
		return
	}
	a.menuIdx.Set(0)
	a.menuMsg.Set("")
	a.menuOpen.Set(true)
}

// closeMenu closes the menu and clears the selection and message.
func (a *app) closeMenu() {
	a.menuOpen.Set(false)
	a.menuIdx.Set(0)
	a.menuMsg.Set("")
}

// menuMove moves the selection delta rows and clamps it to the valid range.
func (a *app) menuMove(delta int) {
	n := len(a.menuItems())
	if n == 0 {
		return
	}
	a.menuMsg.Set("")
	a.menuIdx.Update(func(i int) int {
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

// menuChoose runs the selected action. Disabled items only show their
// reason; enabled ones close the menu before running so their panel or modal
// takes over the screen.
func (a *app) menuChoose() {
	items := a.menuItems()
	i := a.menuIdx.Get()
	if i < 0 || i >= len(items) {
		return
	}
	it := items[i]
	if it.Disabled {
		a.menuMsg.Set(it.Reason)
		return
	}
	a.closeMenu()
	if it.Run != nil {
		it.Run()
	}
}

// menuKeyMap handles menu navigation and execution; it captures every other
// key so global shortcuts do not fire behind the menu.
func (a *app) menuKeyMap() tui.KeyMap {
	return tui.KeyMap{
		tui.OnStop(tui.KeyUp, func(ke tui.KeyEvent) { a.menuMove(-1) }),
		tui.OnStop(tui.KeyDown, func(ke tui.KeyEvent) { a.menuMove(1) }),
		tui.OnStop(tui.Rune('k'), func(ke tui.KeyEvent) { a.menuMove(-1) }),
		tui.OnStop(tui.Rune('j'), func(ke tui.KeyEvent) { a.menuMove(1) }),
		tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.menuChoose() }),
		tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.closeMenu() }),
		tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {}),
	}
}

// menuHints are the shortcuts shown at the bottom of the menu.
func (a *app) menuHints() []hint {
	return []hint{
		{"↑/↓", "choose"}, {"Enter", "run"}, {"Esc", "close"},
	}
}

// menuScrollY keeps the selected row visible on short terminals. It counts
// the rows up to the selection, adds one line per section heading and leaves
// four lines of context above.
func (a *app) menuScrollY() int {
	items := a.menuItems()
	if len(items) == 0 {
		return 0
	}
	idx := a.menuIdx.Get()
	if idx >= len(items) {
		idx = len(items) - 1
	}
	y := 0
	prev := ""
	for i := 0; i <= idx; i++ {
		if items[i].Section != prev {
			y++
			prev = items[i].Section
		}
		y++
	}
	y -= 4
	if y < 0 {
		return 0
	}
	return y
}
