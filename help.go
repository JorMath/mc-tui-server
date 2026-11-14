// help.go: overlay de ayuda (v0.3.1) — todos los atajos por contexto.
package main

import tui "github.com/grindlemire/go-tui"

// helpSection agrupa atajos de un contexto de la TUI.
type helpSection struct {
	Title string
	Keys  []hint
}

var helpSections = []helpSection{
	{"Main screen", []hint{
		{"↑/↓ · j/k", "select instance"},
		{"Enter", "open the actions menu"},
		{"s / x / r", "start / stop / restart"},
		{"c", "command bar (sends to the server console)"},
		{"n", "new instance wizard"},
		{"PgUp/PgDn · End", "scroll the console / follow live"},
		{"?", "this help"},
		{"q", "quit (stops running servers gracefully)"},
	}},
	{"Actions menu", []hint{
		{"↑/↓ · j/k", "choose an action"},
		{"Enter", "run the selected action"},
		{"Esc", "close the menu"},
		{"Lifecycle", "start, stop, restart, send command"},
		{"Content", "files, Modrinth, players"},
		{"Maintenance", "backup world, scheduler, server update, Java runtime"},
		{"Instance", "auto-restart, clone, rename, memory, delete"},
	}},
	{"Panels (opened from the actions menu)", []hint{
		{"Files", "properties, worlds, plugins, backups, logs"},
		{"Modrinth", "mods/plugins/datapacks (Tab switches, u updates all)"},
		{"Players", "whitelist / ops / bans"},
	}},
	{"Everywhere", []hint{
		{"Esc", "close panel or cancel dialog"},
		{"?", "this help"},
		{"q", "quit (stops running servers gracefully)"},
	}},
}

func (a *app) helpKeyMap() tui.KeyMap {
	close := func(ke tui.KeyEvent) { a.helpOpen.Set(false) }
	return tui.KeyMap{
		tui.OnStop(tui.Rune('?'), close),
		tui.OnStop(tui.KeyEscape, close),
		tui.OnStop(tui.Rune('q'), close),
		tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {}),
	}
}
