// command.go: barra de comandos (R2). En modo comando se capturan todas
// las teclas con OnStop para que escribir no dispare los atajos globales.
package main

import (
	tui "github.com/grindlemire/go-tui"
)

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
