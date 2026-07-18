// files.go: panel de archivos (R3) — editor de server.properties y
// gestión de mundos y plugins/mods con confirmación de borrado.
package main

import (
	"fmt"
	"path/filepath"

	"github.com/JorMath/mc-tui-server/internal/assets"
	"github.com/JorMath/mc-tui-server/internal/config"
	"github.com/JorMath/mc-tui-server/internal/properties"
	"github.com/JorMath/mc-tui-server/internal/server"
	tui "github.com/grindlemire/go-tui"
)

func (a *app) fmOpenPanel() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	inst := mgr.Instance()
	a.fmTab.Set(0)
	a.fmPropsIdx.Set(0)
	a.fmWorldIdx.Set(0)
	a.fmPluginIdx.Set(0)
	a.fmEditing.Set(false)
	a.fmDirty.Set(false)
	a.fmConfirm.Set("")
	a.fmMsg.Set("")

	props, err := properties.Load(filepath.Join(inst.Dir, "server.properties"))
	if err != nil {
		a.fmMsg.Set("Error: " + err.Error())
		props = &properties.File{}
	}
	a.fmProps = props
	a.fmReloadLists(inst)
	a.fmRev.Update(func(r int) int { return r + 1 })
	a.fmOpen.Set(true)
}

func (a *app) fmReloadLists(inst config.Instance) {
	worlds, err := assets.Worlds(inst.Dir)
	if err != nil {
		a.fmMsg.Set("Error: " + err.Error())
	}
	a.fmWorlds.Set(worlds)
	plugins, err := assets.Plugins(inst.Dir, inst.Type)
	if err != nil {
		a.fmMsg.Set("Error: " + err.Error())
	}
	a.fmPlugins.Set(plugins)
}

func (a *app) fmClose() {
	a.fmOpen.Set(false)
}

func (a *app) fmPropLines() []string {
	keys := a.fmProps.Keys()
	out := make([]string, len(keys))
	for i, k := range keys {
		v, _ := a.fmProps.Get(k)
		out[i] = k + "=" + v
	}
	return out
}

func (a *app) fmItems() []listItem {
	switch a.fmTab.Get() {
	case 0:
		return fullItems(a.fmPropLines(), a.fmPropsIdx.Get())
	case 1:
		return fullItems(a.fmWorlds.Get(), a.fmWorldIdx.Get())
	default:
		return fullItems(a.fmPlugins.Get(), a.fmPluginIdx.Get())
	}
}

// fmScrollY sigue la selección de la pestaña activa.
func (a *app) fmScrollY() int {
	switch a.fmTab.Get() {
	case 0:
		return scrollTo(a.fmPropsIdx.Get())
	case 1:
		return scrollTo(a.fmWorldIdx.Get())
	default:
		return scrollTo(a.fmPluginIdx.Get())
	}
}

func (a *app) fmMove(delta int) {
	move := func(st *tui.State[int], n int) {
		if n == 0 {
			return
		}
		st.Update(func(i int) int {
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
	switch a.fmTab.Get() {
	case 0:
		move(a.fmPropsIdx, len(a.fmProps.Keys()))
	case 1:
		move(a.fmWorldIdx, len(a.fmWorlds.Get()))
	default:
		move(a.fmPluginIdx, len(a.fmPlugins.Get()))
	}
}

func (a *app) fmSelectedKey() string {
	keys := a.fmProps.Keys()
	i := a.fmPropsIdx.Get()
	if i < 0 || i >= len(keys) {
		return ""
	}
	return keys[i]
}

func (a *app) fmBeginEdit() {
	key := a.fmSelectedKey()
	if key == "" {
		return
	}
	v, _ := a.fmProps.Get(key)
	a.fmEditText.Set(v)
	a.fmEditing.Set(true)
	a.fmMsg.Set("")
}

func (a *app) fmCommitEdit() {
	key := a.fmSelectedKey()
	a.fmProps.Set(key, a.fmEditText.Get())
	a.fmDirty.Set(true)
	a.fmEditing.Set(false)
	a.fmRev.Update(func(r int) int { return r + 1 })
	a.fmMsg.Set("Changed in memory; press w to save")
}

func (a *app) fmSave() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	path := filepath.Join(mgr.Instance().Dir, "server.properties")
	if err := a.fmProps.Save(path); err != nil {
		a.fmMsg.Set("Error: " + err.Error())
		return
	}
	a.fmDirty.Set(false)
	a.fmMsg.Set("Saved. Restart the server to apply the changes")
}

// fmAskDelete pide confirmación para borrar el mundo o plugin seleccionado.
func (a *app) fmAskDelete() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	if mgr.Status() != server.Stopped {
		a.fmMsg.Set("Stop the server before deleting files")
		return
	}
	var name string
	switch a.fmTab.Get() {
	case 1:
		if ws := a.fmWorlds.Get(); len(ws) > 0 {
			name = ws[a.fmWorldIdx.Get()]
		}
	case 2:
		if ps := a.fmPlugins.Get(); len(ps) > 0 {
			name = ps[a.fmPluginIdx.Get()]
		}
	}
	if name == "" {
		return
	}
	a.fmConfirm.Set(name)
}

func (a *app) fmDoDelete() {
	mgr := a.current()
	name := a.fmConfirm.Get()
	a.fmConfirm.Set("")
	if mgr == nil || name == "" {
		return
	}
	inst := mgr.Instance()
	var err error
	if a.fmTab.Get() == 1 {
		err = assets.DeleteWorld(inst.Dir, name)
	} else {
		err = assets.DeletePlugin(inst.Dir, inst.Type, name)
	}
	if err != nil {
		a.fmMsg.Set("Error: " + err.Error())
		return
	}
	a.fmMsg.Set(fmt.Sprintf("%q deleted", name))
	a.fmWorldIdx.Set(0)
	a.fmPluginIdx.Set(0)
	a.fmReloadLists(inst)
}

func (a *app) fmTitle() string {
	_ = a.fmRev.Get() // dependencia explícita: re-render tras mutar fmProps
	title := fmt.Sprintf("Files — %s · %s", a.currentName(), a.fmTabName())
	if a.fmDirty.Get() {
		title += " (unsaved)"
	}
	return title
}

func (a *app) fmTabName() string {
	switch a.fmTab.Get() {
	case 0:
		return "Properties"
	case 1:
		return "Worlds"
	default:
		return "Plugins/Mods"
	}
}

func (a *app) fmHints() []hint {
	if a.fmEditing.Get() {
		return []hint{{"Enter", "apply"}, {"Esc", "cancel"}}
	}
	if a.fmConfirm.Get() != "" {
		return []hint{{"y", "delete"}, {"n/Esc", "keep"}}
	}
	if a.fmTab.Get() == 0 {
		return []hint{{"↑/↓", "select"}, {"Enter", "edit"}, {"w", "save"}, {"1/2/3 Tab", "switch tab"}, {"Esc", "close"}}
	}
	return []hint{{"↑/↓", "select"}, {"d", "delete"}, {"1/2/3 Tab", "switch tab"}, {"Esc", "close"}}
}

func (a *app) fmKeyMap() tui.KeyMap {
	if a.fmEditing.Get() {
		return tui.KeyMap{
			tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {
				a.fmEditText.Update(func(s string) string { return s + string(ke.Rune) })
			}),
			tui.OnStop(tui.KeyBackspace, func(ke tui.KeyEvent) {
				a.fmEditText.Update(func(s string) string {
					r := []rune(s)
					if len(r) == 0 {
						return s
					}
					return string(r[:len(r)-1])
				})
			}),
			tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.fmCommitEdit() }),
			tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.fmEditing.Set(false) }),
		}
	}
	if a.fmConfirm.Get() != "" {
		return tui.KeyMap{
			tui.OnStop(tui.Rune('y'), func(ke tui.KeyEvent) { a.fmDoDelete() }),
			tui.OnStop(tui.Rune('n'), func(ke tui.KeyEvent) { a.fmConfirm.Set("") }),
			tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.fmConfirm.Set("") }),
		}
	}
	return tui.KeyMap{
		tui.OnStop(tui.Rune('1'), func(ke tui.KeyEvent) { a.fmTab.Set(0) }),
		tui.OnStop(tui.Rune('2'), func(ke tui.KeyEvent) { a.fmTab.Set(1) }),
		tui.OnStop(tui.Rune('3'), func(ke tui.KeyEvent) { a.fmTab.Set(2) }),
		tui.OnStop(tui.KeyTab, func(ke tui.KeyEvent) { a.fmTab.Update(func(t int) int { return (t + 1) % 3 }) }),
		tui.OnStop(tui.KeyUp, func(ke tui.KeyEvent) { a.fmMove(-1) }),
		tui.OnStop(tui.KeyDown, func(ke tui.KeyEvent) { a.fmMove(1) }),
		tui.OnStop(tui.KeyPageUp, func(ke tui.KeyEvent) { a.fmMove(-10) }),
		tui.OnStop(tui.KeyPageDown, func(ke tui.KeyEvent) { a.fmMove(10) }),
		tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) {
			if a.fmTab.Get() == 0 {
				a.fmBeginEdit()
			}
		}),
		tui.OnStop(tui.Rune('w'), func(ke tui.KeyEvent) {
			if a.fmTab.Get() == 0 {
				a.fmSave()
			}
		}),
		tui.OnStop(tui.Rune('d'), func(ke tui.KeyEvent) {
			if a.fmTab.Get() != 0 {
				a.fmAskDelete()
			}
		}),
		tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.fmClose() }),
	}
}
