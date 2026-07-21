// players.go: panel de jugadores (v0.3.0) — whitelist, ops y baneados.
// Con el servidor corriendo aplica con comandos en vivo; detenido edita
// los JSON directamente, resolviendo el UUID según el online-mode.
package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/JorMath/mc-tui-server/internal/config"
	"github.com/JorMath/mc-tui-server/internal/players"
	"github.com/JorMath/mc-tui-server/internal/properties"
	"github.com/JorMath/mc-tui-server/internal/server"
	tui "github.com/grindlemire/go-tui"
)

// plTabsInfo define cada pestaña: archivo, comandos de alta/baja y título.
var plTabsInfo = []struct {
	Title     string
	File      string
	AddCmd    string
	RemoveCmd string
}{
	{"Whitelist", players.WhitelistFile, "whitelist add %s", "whitelist remove %s"},
	{"Ops", players.OpsFile, "op %s", "deop %s"},
	{"Banned", players.BansFile, "ban %s", "pardon %s"},
}

func (a *app) plOpenPanel() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	a.plTab.Set(0)
	a.plIdx.Set(0)
	a.plAdding.Set(false)
	a.plText.Set("")
	a.plMsg.Set("")
	a.plReload()
	a.plOpen.Set(true)
}

func (a *app) plClose() {
	a.plOpen.Set(false)
}

// plReload recarga la lista de la pestaña activa desde el JSON.
func (a *app) plReload() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	tab := plTabsInfo[a.plTab.Get()]
	entries, err := players.Load(filepath.Join(mgr.Instance().Dir, tab.File))
	if err != nil {
		a.plMsg.Set("Error: " + err.Error())
		return
	}
	names := players.Names(entries)
	a.plList.Set(names)
	a.plIdx.Update(func(i int) int {
		if i >= len(names) {
			return 0
		}
		return i
	})
}

func (a *app) plSetTab(t int) {
	a.plTab.Set(t)
	a.plIdx.Set(0)
	a.plMsg.Set("")
	a.plReload()
}

// plDelayedReload recarga tras darle tiempo al server a escribir el JSON
// después de un comando en vivo.
func (a *app) plDelayedReload() {
	time.AfterFunc(1500*time.Millisecond, a.plReload)
}

// validPlayerChar acepta los caracteres de los nombres de Minecraft.
func validPlayerChar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
		return true
	}
	return false
}

// onlineMode lee online-mode de server.properties (true por defecto):
// decide si los UUID se resuelven contra Mojang o con el algoritmo offline.
func onlineMode(inst config.Instance) bool {
	if props, err := properties.Load(filepath.Join(inst.Dir, "server.properties")); err == nil {
		if v, ok := props.Get("online-mode"); ok {
			return v != "false"
		}
	}
	return true
}

// plCommitAdd añade el nombre escrito a la lista activa.
func (a *app) plCommitAdd() {
	mgr := a.current()
	name := a.plText.Get()
	a.plAdding.Set(false)
	a.plText.Set("")
	if mgr == nil || name == "" {
		return
	}
	inst := mgr.Instance()
	tab := plTabsInfo[a.plTab.Get()]
	if mgr.Status() == server.Running {
		if err := mgr.Send(fmt.Sprintf(tab.AddCmd, name)); err != nil {
			a.plMsg.Set("Error: " + err.Error())
			return
		}
		a.plMsg.Set(fmt.Sprintf("Sent %q to the running server", fmt.Sprintf(tab.AddCmd, name)))
		a.plDelayedReload()
		return
	}
	tabIdx := a.plTab.Get()
	a.plMsg.Set("Resolving player UUID...")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var uuid string
		var err error
		if onlineMode(inst) {
			uuid, err = players.MojangUUID(ctx, nil, "", name)
		} else {
			uuid = players.OfflineUUID(name)
		}
		if err != nil {
			a.plMsg.Set("Error: " + err.Error())
			return
		}
		path := filepath.Join(inst.Dir, tab.File)
		entries, err := players.Load(path)
		if err != nil {
			a.plMsg.Set("Error: " + err.Error())
			return
		}
		if players.Has(entries, name) {
			a.plMsg.Set(fmt.Sprintf("%q is already on the list", name))
			return
		}
		switch tabIdx {
		case 0:
			entries = append(entries, players.Whitelist(uuid, name))
		case 1:
			entries = append(entries, players.Op(uuid, name))
		default:
			entries = append(entries, players.Ban(uuid, name, time.Now()))
		}
		if err := players.Save(path, entries); err != nil {
			a.plMsg.Set("Error: " + err.Error())
			return
		}
		a.plMsg.Set(fmt.Sprintf("%q added", name))
		a.plReload()
	}()
}

// plRemove quita el jugador seleccionado de la lista activa.
func (a *app) plRemove() {
	mgr := a.current()
	list := a.plList.Get()
	idx := a.plIdx.Get()
	if mgr == nil || idx < 0 || idx >= len(list) {
		return
	}
	name := list[idx]
	tab := plTabsInfo[a.plTab.Get()]
	if mgr.Status() == server.Running {
		if err := mgr.Send(fmt.Sprintf(tab.RemoveCmd, name)); err != nil {
			a.plMsg.Set("Error: " + err.Error())
			return
		}
		a.plMsg.Set(fmt.Sprintf("Sent %q to the running server", fmt.Sprintf(tab.RemoveCmd, name)))
		a.plDelayedReload()
		return
	}
	path := filepath.Join(mgr.Instance().Dir, tab.File)
	entries, err := players.Load(path)
	if err != nil {
		a.plMsg.Set("Error: " + err.Error())
		return
	}
	entries, removed := players.Remove(entries, name)
	if !removed {
		a.plMsg.Set(fmt.Sprintf("%q is not on the list", name))
		return
	}
	if err := players.Save(path, entries); err != nil {
		a.plMsg.Set("Error: " + err.Error())
		return
	}
	a.plMsg.Set(fmt.Sprintf("%q removed", name))
	a.plReload()
}

func (a *app) plItems() []listItem {
	return fullItems(a.plList.Get(), a.plIdx.Get())
}

func (a *app) plMove(delta int) {
	n := len(a.plList.Get())
	if n == 0 {
		return
	}
	a.plIdx.Update(func(i int) int {
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

func (a *app) plHints() []hint {
	if a.plAdding.Get() {
		return []hint{{"Enter", "add"}, {"Esc", "cancel"}}
	}
	return []hint{{"↑/↓", "select"}, {"a", "add"}, {"d", "remove"}, {"1/2/3 Tab", "switch tab"}, {"Esc", "close"}}
}

func (a *app) plKeyMap() tui.KeyMap {
	if a.plAdding.Get() {
		return tui.KeyMap{
			tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {
				if validPlayerChar(ke.Rune) {
					a.plText.Update(func(s string) string { return s + string(ke.Rune) })
				}
			}),
			tui.OnStop(tui.KeyBackspace, func(ke tui.KeyEvent) {
				a.plText.Update(func(s string) string {
					if len(s) == 0 {
						return s
					}
					return s[:len(s)-1]
				})
			}),
			tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.plCommitAdd() }),
			tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) {
				a.plAdding.Set(false)
				a.plText.Set("")
			}),
		}
	}
	return tui.KeyMap{
		tui.OnStop(tui.Rune('1'), func(ke tui.KeyEvent) { a.plSetTab(0) }),
		tui.OnStop(tui.Rune('2'), func(ke tui.KeyEvent) { a.plSetTab(1) }),
		tui.OnStop(tui.Rune('3'), func(ke tui.KeyEvent) { a.plSetTab(2) }),
		tui.OnStop(tui.KeyTab, func(ke tui.KeyEvent) { a.plSetTab((a.plTab.Get() + 1) % len(plTabsInfo)) }),
		tui.OnStop(tui.KeyUp, func(ke tui.KeyEvent) { a.plMove(-1) }),
		tui.OnStop(tui.KeyDown, func(ke tui.KeyEvent) { a.plMove(1) }),
		tui.OnStop(tui.KeyPageUp, func(ke tui.KeyEvent) { a.plMove(-10) }),
		tui.OnStop(tui.KeyPageDown, func(ke tui.KeyEvent) { a.plMove(10) }),
		tui.OnStop(tui.Rune('a'), func(ke tui.KeyEvent) {
			a.plAdding.Set(true)
			a.plMsg.Set("")
		}),
		tui.OnStop(tui.Rune('d'), func(ke tui.KeyEvent) { a.plRemove() }),
		tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.plClose() }),
	}
}
