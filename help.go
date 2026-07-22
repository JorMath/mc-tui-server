// help.go: overlay de ayuda (v0.3.1) — todos los atajos por contexto.
package main

import tui "github.com/grindlemire/go-tui"

// helpSection agrupa atajos de un contexto de la TUI.
type helpSection struct {
	Title string
	Keys  []hint
}

var helpSections = []helpSection{
	{"Instances", []hint{
		{"↑/↓ · j/k", "select instance"},
		{"s / x / r", "start / stop / restart"},
		{"c · Enter", "command bar (sends to the server console)"},
		{"a", "toggle auto-restart on crash (↻)"},
		{"PgUp/PgDn · End", "scroll the console / follow live"},
	}},
	{"Panels", []hint{
		{"e", "files: properties, worlds, plugins, backups, logs"},
		{"m", "Modrinth: mods/plugins/datapacks (Tab switches, u updates all)"},
		{"p", "players: whitelist / ops / bans"},
		{"n", "new instance wizard (7 distros, modpacks, import)"},
	}},
	{"Instance management", []hint{
		{"b", "back up the active world"},
		{"S", "schedule automatic backups (with retention) and daily restart"},
		{"U", "update a modpack instance to the pack's latest version"},
		{"C", "clone the instance as a sandbox copy"},
		{"R / M / d", "rename / memory / delete"},
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
