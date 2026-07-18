// modrinth_panel.go: buscador e instalador de plugins/mods de Modrinth (R6),
// filtrado por loader y versión de la instancia seleccionada.
package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"github.com/JorMath/mc-tui-server/internal/assets"
	"github.com/JorMath/mc-tui-server/internal/download"
	"github.com/JorMath/mc-tui-server/internal/modrinth"
	tui "github.com/grindlemire/go-tui"
)

func (a *app) mrOpenPanel() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	inst := mgr.Instance()
	if _, ok := assets.PluginsDir(inst.Type); !ok {
		a.appendLog(inst.Name, "[mc-tui] vanilla servers do not support plugins/mods")
		return
	}
	a.mrGen.Update(func(g int) int { return g + 1 })
	a.mrQuery.Set("")
	a.mrResults.Set([]modrinth.Project{})
	a.mrIdx.Set(0)
	a.mrBusy.Set(false)
	a.mrMsg.Set("")
	a.mrTyping.Set(true)
	a.mrOpen.Set(true)
}

func (a *app) mrClose() {
	a.mrGen.Update(func(g int) int { return g + 1 })
	a.mrOpen.Set(false)
}

func (a *app) mrSearch() {
	mgr := a.current()
	query := a.mrQuery.Get()
	if mgr == nil || query == "" || a.mrBusy.Get() {
		return
	}
	inst := mgr.Instance()
	gen := a.mrGen.Get()
	a.mrBusy.Set(true)
	a.mrMsg.Set("Searching Modrinth...")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		results, err := a.mr.Search(ctx, query, inst.Type, inst.Version)
		if a.mrGen.Get() != gen {
			return
		}
		a.mrBusy.Set(false)
		if err != nil {
			a.mrMsg.Set("Error: " + err.Error())
			return
		}
		a.mrResults.Set(results)
		a.mrIdx.Set(0)
		a.mrTyping.Set(false)
		if len(results) == 0 {
			a.mrMsg.Set(fmt.Sprintf("No results for %q compatible with %s %s", query, inst.Type, inst.Version))
			return
		}
		a.mrMsg.Set(fmt.Sprintf("%d results · Enter installs into the selected instance", len(results)))
	}()
}

func (a *app) mrInstall() {
	mgr := a.current()
	results := a.mrResults.Get()
	idx := a.mrIdx.Get()
	if mgr == nil || a.mrBusy.Get() || idx < 0 || idx >= len(results) {
		return
	}
	inst := mgr.Instance()
	project := results[idx]
	sub, ok := assets.PluginsDir(inst.Type)
	if !ok {
		return
	}
	gen := a.mrGen.Get()
	a.mrBusy.Set(true)
	a.mrMsg.Set(fmt.Sprintf("Resolving %s...", project.Title))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		file, err := a.mr.LatestFile(ctx, project.ID, inst.Type, inst.Version)
		if err == nil {
			dest := filepath.Join(inst.Dir, sub, file.Filename)
			err = download.DownloadFile(ctx, nil, file.URL, dest, func(done, total int64) {
				if a.mrGen.Get() != gen {
					return
				}
				if total > 0 {
					a.mrMsg.Set(fmt.Sprintf("Downloading %s... %d%%", file.Filename, done*100/total))
				}
			})
		}
		if a.mrGen.Get() != gen {
			return
		}
		a.mrBusy.Set(false)
		if err != nil {
			a.mrMsg.Set("Error: " + err.Error())
			return
		}
		a.mrMsg.Set(fmt.Sprintf("Installed %s into %s/ · restart the server to load it", file.Filename, sub))
		a.appendLog(inst.Name, fmt.Sprintf("[mc-tui] Installed %s (%s)", project.Title, file.Filename))
	}()
}

// mrDownloadsText formatea el contador de descargas (9000 → 9.0k).
func mrDownloadsText(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return strconv.Itoa(n)
	}
}

func (a *app) mrItems() []listItem {
	results := a.mrResults.Get()
	lines := make([]string, len(results))
	for i, p := range results {
		desc := p.Description
		if r := []rune(desc); len(r) > 50 {
			desc = string(r[:50]) + "…"
		}
		lines[i] = fmt.Sprintf("%s (%s ⇩) — %s", p.Title, mrDownloadsText(p.Downloads), desc)
	}
	return fullItems(lines, a.mrIdx.Get())
}

func (a *app) mrMove(delta int) {
	n := len(a.mrResults.Get())
	if n == 0 {
		return
	}
	a.mrIdx.Update(func(i int) int {
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

func (a *app) mrHints() []hint {
	if a.mrTyping.Get() {
		return []hint{{"Enter", "search"}, {"Esc", "close"}}
	}
	return []hint{{"↑/↓", "select"}, {"Enter", "install"}, {"/", "new search"}, {"Esc", "close"}}
}

func (a *app) mrKeyMap() tui.KeyMap {
	esc := tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.mrClose() })
	if a.mrTyping.Get() {
		return tui.KeyMap{
			tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {
				a.mrQuery.Update(func(s string) string { return s + string(ke.Rune) })
			}),
			tui.OnStop(tui.KeyBackspace, func(ke tui.KeyEvent) {
				a.mrQuery.Update(func(s string) string {
					r := []rune(s)
					if len(r) == 0 {
						return s
					}
					return string(r[:len(r)-1])
				})
			}),
			tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.mrSearch() }),
			esc,
		}
	}
	return tui.KeyMap{
		tui.OnStop(tui.KeyUp, func(ke tui.KeyEvent) { a.mrMove(-1) }),
		tui.OnStop(tui.KeyDown, func(ke tui.KeyEvent) { a.mrMove(1) }),
		tui.OnStop(tui.KeyPageUp, func(ke tui.KeyEvent) { a.mrMove(-10) }),
		tui.OnStop(tui.KeyPageDown, func(ke tui.KeyEvent) { a.mrMove(10) }),
		tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.mrInstall() }),
		tui.OnStop(tui.Rune('/'), func(ke tui.KeyEvent) { a.mrTyping.Set(true) }),
		esc,
	}
}
