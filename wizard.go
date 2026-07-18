// wizard.go: asistente de nueva instancia (R4) — tipo → versión → nombre
// → memoria → EULA → descarga del jar.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/JorMath/mc-tui-server/internal/config"
	"github.com/JorMath/mc-tui-server/internal/download"
	"github.com/JorMath/mc-tui-server/internal/server"
	tui "github.com/grindlemire/go-tui"
)

// Pasos del asistente de nueva instancia (R4).
const (
	wizOff = iota
	wizType
	wizLoading
	wizVersion
	wizName
	wizMem
	wizEula
	wizDownload
	wizError
)

var wizTypes = []config.ServerType{config.Vanilla, config.Paper, config.Purpur, config.Fabric}

func validNameChar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		return true
	}
	return false
}

func (a *app) wizOpen() {
	a.wizGen.Update(func(g int) int { return g + 1 })
	a.wizTypeIdx.Set(0)
	a.wizVersions.Set([]string{})
	a.wizVerIdx.Set(0)
	a.wizName.Set("")
	a.wizMemory.Set("")
	a.wizMsg.Set("")
	a.wizStep.Set(wizType)
}

func (a *app) wizClose() {
	a.wizGen.Update(func(g int) int { return g + 1 })
	a.wizStep.Set(wizOff)
}

// wizFail muestra el error si el asistente sigue en la misma generación.
func (a *app) wizFail(gen int, err error) {
	if a.wizGen.Get() != gen {
		return
	}
	a.wizMsg.Set("Error: " + err.Error())
	a.wizStep.Set(wizError)
}

func (a *app) wizFetchVersions() {
	typ := wizTypes[a.wizTypeIdx.Get()]
	gen := a.wizGen.Get()
	a.wizMsg.Set(fmt.Sprintf("Fetching %s versions...", typ))
	a.wizStep.Set(wizLoading)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		prov, err := download.For(typ, nil)
		if err != nil {
			a.wizFail(gen, err)
			return
		}
		versions, err := prov.Versions(ctx)
		if err != nil {
			a.wizFail(gen, err)
			return
		}
		if len(versions) == 0 {
			a.wizFail(gen, fmt.Errorf("the %s API returned no versions", typ))
			return
		}
		if a.wizGen.Get() != gen {
			return
		}
		a.wizVersions.Set(versions)
		a.wizVerIdx.Set(0)
		a.wizStep.Set(wizVersion)
	}()
}

func (a *app) wizSubmitName() {
	name := a.wizName.Get()
	if name == "" {
		a.wizMsg.Set("The name cannot be empty")
		return
	}
	if _, exists := a.store.Get(name); exists {
		a.wizMsg.Set(fmt.Sprintf("An instance named %q already exists", name))
		return
	}
	a.wizMsg.Set("")
	a.wizStep.Set(wizMem)
}

func (a *app) wizMemoryMB() int {
	mb, err := strconv.Atoi(a.wizMemory.Get())
	if err != nil || mb <= 0 {
		return 2048
	}
	return mb
}

func (a *app) wizStartDownload() {
	typ := wizTypes[a.wizTypeIdx.Get()]
	version := a.wizVersions.Get()[a.wizVerIdx.Get()]
	name := a.wizName.Get()
	memMB := a.wizMemoryMB()
	gen := a.wizGen.Get()
	a.wizMsg.Set("Resolving download URL...")
	a.wizStep.Set(wizDownload)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		prov, err := download.For(typ, nil)
		if err != nil {
			a.wizFail(gen, err)
			return
		}
		url, err := prov.ResolveJarURL(ctx, version)
		if err != nil {
			a.wizFail(gen, err)
			return
		}
		dir := filepath.Join(a.dataDir, "servers", name)
		jar := filepath.Join(dir, "server.jar")
		err = download.DownloadFile(ctx, nil, url, jar, func(done, total int64) {
			if total > 0 {
				a.wizMsg.Set(fmt.Sprintf("Downloading... %d%% (%dMB of %dMB)",
					done*100/total, done/(1024*1024), total/(1024*1024)))
			} else {
				a.wizMsg.Set(fmt.Sprintf("Downloading... %dMB", done/(1024*1024)))
			}
		})
		if err != nil {
			a.wizFail(gen, err)
			return
		}
		// El usuario aceptó el EULA en el paso anterior del asistente.
		if err := os.WriteFile(filepath.Join(dir, "eula.txt"), []byte("eula=true\n"), 0o644); err != nil {
			a.wizFail(gen, err)
			return
		}
		inst := config.Instance{
			Name:     name,
			Dir:      dir,
			JarPath:  "server.jar",
			MemoryMB: memMB,
			Type:     typ,
			Version:  version,
		}
		if err := a.store.Add(inst); err != nil {
			a.wizFail(gen, err)
			return
		}
		if err := a.store.Save(); err != nil {
			a.wizFail(gen, err)
			return
		}
		mgr := server.New(inst)
		a.pumpLogs(mgr)
		a.managers.Update(func(ms []*server.Manager) []*server.Manager {
			return append(ms, mgr)
		})
		a.appendLog(name, fmt.Sprintf("[mc-tui] Instance created: %s %s (%d MB)", typ, version, memMB))
		a.selected.Set(len(a.managers.Get()) - 1)
		a.wizClose()
	}()
}

func (a *app) wizTypeItems() []listItem {
	items := make([]listItem, len(wizTypes))
	for i, t := range wizTypes {
		items[i] = listItem{Text: string(t), Sel: i == a.wizTypeIdx.Get()}
	}
	return items
}

// wizVersionItems devuelve la lista completa; el contenedor scrollable
// del render se encarga de recortar y seguir la selección.
func (a *app) wizVersionItems() []listItem {
	return fullItems(a.wizVersions.Get(), a.wizVerIdx.Get())
}

func (a *app) wizMoveType(delta int) {
	a.wizTypeIdx.Update(func(i int) int {
		i += delta
		if i < 0 {
			i = 0
		}
		if i >= len(wizTypes) {
			i = len(wizTypes) - 1
		}
		return i
	})
}

func (a *app) wizMoveVersion(delta int) {
	n := len(a.wizVersions.Get())
	a.wizVerIdx.Update(func(i int) int {
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

func (a *app) wizKeyMap() tui.KeyMap {
	esc := tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.wizClose() })
	switch a.wizStep.Get() {
	case wizType:
		return tui.KeyMap{
			tui.OnStop(tui.KeyUp, func(ke tui.KeyEvent) { a.wizMoveType(-1) }),
			tui.OnStop(tui.KeyDown, func(ke tui.KeyEvent) { a.wizMoveType(1) }),
			tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.wizFetchVersions() }),
			esc,
		}
	case wizLoading:
		return tui.KeyMap{esc}
	case wizVersion:
		return tui.KeyMap{
			tui.OnStop(tui.KeyUp, func(ke tui.KeyEvent) { a.wizMoveVersion(-1) }),
			tui.OnStop(tui.KeyDown, func(ke tui.KeyEvent) { a.wizMoveVersion(1) }),
			tui.OnStop(tui.KeyPageUp, func(ke tui.KeyEvent) { a.wizMoveVersion(-10) }),
			tui.OnStop(tui.KeyPageDown, func(ke tui.KeyEvent) { a.wizMoveVersion(10) }),
			tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.wizStep.Set(wizName) }),
			esc,
		}
	case wizName:
		return tui.KeyMap{
			tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {
				if validNameChar(ke.Rune) {
					a.wizName.Update(func(s string) string { return s + string(ke.Rune) })
				}
			}),
			tui.OnStop(tui.KeyBackspace, func(ke tui.KeyEvent) {
				a.wizName.Update(func(s string) string {
					if len(s) == 0 {
						return s
					}
					return s[:len(s)-1]
				})
			}),
			tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.wizSubmitName() }),
			esc,
		}
	case wizMem:
		return tui.KeyMap{
			tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {
				if ke.Rune >= '0' && ke.Rune <= '9' {
					a.wizMemory.Update(func(s string) string { return s + string(ke.Rune) })
				}
			}),
			tui.OnStop(tui.KeyBackspace, func(ke tui.KeyEvent) {
				a.wizMemory.Update(func(s string) string {
					if len(s) == 0 {
						return s
					}
					return s[:len(s)-1]
				})
			}),
			tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.wizStep.Set(wizEula) }),
			esc,
		}
	case wizEula:
		return tui.KeyMap{
			tui.OnStop(tui.Rune('y'), func(ke tui.KeyEvent) { a.wizStartDownload() }),
			tui.OnStop(tui.Rune('n'), func(ke tui.KeyEvent) { a.wizClose() }),
			esc,
		}
	case wizDownload:
		// Sin teclas: la descarga no se cancela a mitad en esta versión.
		return tui.KeyMap{tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {})}
	default: // wizError
		return tui.KeyMap{esc, tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.wizClose() })}
	}
}

func (a *app) wizHints() []hint {
	switch a.wizStep.Get() {
	case wizType:
		return []hint{{"↑/↓", "choose"}, {"Enter", "continue"}, {"Esc", "cancel"}}
	case wizLoading:
		return []hint{{"Esc", "cancel"}}
	case wizVersion:
		return []hint{{"↑/↓ PgUp/PgDn", "choose"}, {"Enter", "continue"}, {"Esc", "cancel"}}
	case wizName:
		return []hint{{"a-z 0-9 - _", "type"}, {"Enter", "continue"}, {"Esc", "cancel"}}
	case wizMem:
		return []hint{{"0-9", "type (empty = 2048)"}, {"Enter", "continue"}, {"Esc", "cancel"}}
	case wizEula:
		return []hint{{"y", "accept & download"}, {"n/Esc", "cancel"}}
	case wizError:
		return []hint{{"Esc", "close"}}
	default: // wizDownload: no hay teclas activas
		return nil
	}
}

func (a *app) wizStepTitle() string {
	switch a.wizStep.Get() {
	case wizType:
		return "1/5 · Server type"
	case wizLoading:
		return "2/5 · Fetching versions"
	case wizVersion:
		return "2/5 · Version"
	case wizName:
		return "3/5 · Instance name"
	case wizMem:
		return "4/5 · Memory (MB)"
	case wizEula:
		return "5/5 · Minecraft EULA"
	case wizDownload:
		return "Downloading"
	default:
		return "Error"
	}
}
