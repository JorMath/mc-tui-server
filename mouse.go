// mouse.go: soporte de ratón (v0.4.0) — click para seleccionar instancias
// y pestañas, rueda para scrollear la consola o la lista activa.
package main

import tui "github.com/grindlemire/go-tui"

// HandleMouse implementa tui.MouseListener en la app raíz. Los elementos
// clicables se localizan con RefMaps que las vistas rellenan al renderizar.
func (a *app) HandleMouse(me tui.MouseEvent) bool {
	if a.splash.Get() {
		return false
	}
	switch me.Button {
	case tui.MouseWheelUp:
		return a.wheel(-3)
	case tui.MouseWheelDown:
		return a.wheel(3)
	}
	if me.Button != tui.MouseLeft || me.Action != tui.MousePress {
		return false
	}
	if a.fmOpen.Get() {
		for i, name := range fmTabNames {
			if el := a.fmTabRefs.Get(name); el != nil && el.ContainsPoint(me.X, me.Y) {
				a.fmLogView.Set(false)
				a.fmTab.Set(i)
				return true
			}
		}
	}
	if a.plOpen.Get() {
		for i, tab := range plTabsInfo {
			if el := a.plTabRefs.Get(tab.Title); el != nil && el.ContainsPoint(me.X, me.Y) {
				a.plSetTab(i)
				return true
			}
		}
	}
	for i, mgr := range a.managers.Get() {
		if el := a.rowRefs.Get(mgr.Instance().Name); el != nil && el.ContainsPoint(me.X, me.Y) {
			if i != a.selected.Get() {
				a.logScroll.Set(0)
				a.selected.Set(i)
			}
			return true
		}
	}
	return false
}

// wheel scrollea el contexto activo: la lista del panel abierto o la
// consola en vivo (rueda arriba = ver líneas más viejas).
func (a *app) wheel(delta int) bool {
	switch {
	case a.wizStep.Get() != wizOff:
		return false
	case a.fmOpen.Get():
		a.fmMove(delta)
	case a.mrOpen.Get():
		if a.mrVerMode.Get() {
			a.mrMoveVer(delta)
		} else {
			a.mrMove(delta)
		}
	case a.plOpen.Get():
		a.plMove(delta)
	case a.helpOpen.Get():
		return false
	default:
		a.scrollConsole(-delta)
	}
	return true
}
