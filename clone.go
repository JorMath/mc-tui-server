// clone.go: clonar una instancia (v0.4.0) — copia completa de la carpeta
// (sin backups/) como sandbox para probar mods o updates sin riesgo.
package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/JorMath/mc-tui-server/internal/backup"
	"github.com/JorMath/mc-tui-server/internal/server"
	tui "github.com/grindlemire/go-tui"
)

// copyDir copia src en dst recursivamente, saltando las carpetas de skip
// (relativas a la raíz). Devuelve cuántos archivos copió.
func copyDir(src, dst string, skip map[string]bool) (int, error) {
	copied := 0
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		first, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
		if skip[first] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := copyFile(path, target); err != nil {
			return err
		}
		copied++
		return nil
	})
	return copied, err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening %s: %w", src, err)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dst, err)
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// cloneOpen abre el input de nombre para clonar la instancia seleccionada.
func (a *app) cloneOpen() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	if st := mgr.Status(); st == server.Running || st == server.Stopping {
		a.appendLog(mgr.Instance().Name, "[mc-tui] Stop the server before cloning it")
		return
	}
	a.cloneText.Set(mgr.Instance().Name + "-copy")
	a.cloneMsg.Set("")
	a.cloneActive.Set(true)
}

func (a *app) cloneClose() {
	a.cloneActive.Set(false)
	a.cloneText.Set("")
	a.cloneMsg.Set("")
}

// cloneCommit valida el nombre y lanza la copia en segundo plano.
func (a *app) cloneCommit() {
	mgr := a.current()
	if mgr == nil {
		a.cloneClose()
		return
	}
	name := a.cloneText.Get()
	if name == "" {
		a.cloneMsg.Set("The name cannot be empty")
		return
	}
	if _, exists := a.store.Get(name); exists {
		a.cloneMsg.Set(fmt.Sprintf("An instance named %q already exists", name))
		return
	}
	if st := mgr.Status(); st == server.Running || st == server.Stopping {
		a.cloneMsg.Set("Stop the server before cloning it")
		return
	}
	src := mgr.Instance()
	dstDir := filepath.Join(a.dataDir, "servers", name)
	if _, err := os.Stat(dstDir); err == nil {
		a.cloneMsg.Set(fmt.Sprintf("Folder %s already exists", dstDir))
		return
	}
	a.cloneClose()
	a.appendLog(src.Name, fmt.Sprintf("[mc-tui] Cloning into %q...", name))
	go func() {
		// Los backups pertenecen al original; el clon arranca limpio.
		copied, err := copyDir(src.Dir, dstDir, map[string]bool{backup.Dir: true})
		if err != nil {
			a.appendLog(src.Name, "[mc-tui] Clone failed: "+err.Error())
			_ = os.RemoveAll(dstDir)
			return
		}
		inst := src
		inst.Name = name
		inst.Dir = dstDir
		if err := a.store.Add(inst); err != nil {
			a.appendLog(src.Name, "[mc-tui] Clone failed: "+err.Error())
			return
		}
		if err := a.store.Save(); err != nil {
			a.appendLog(src.Name, "[mc-tui] Clone failed: "+err.Error())
			return
		}
		clone := server.New(inst)
		a.pumpLogs(clone)
		a.managers.Update(func(ms []*server.Manager) []*server.Manager {
			return append(ms, clone)
		})
		a.appendLog(name, fmt.Sprintf("[mc-tui] Cloned from %q (%d files)", src.Name, copied))
	}()
}

func (a *app) cloneKeyMap() tui.KeyMap {
	return tui.KeyMap{
		tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {
			if validNameChar(ke.Rune) {
				a.cloneText.Update(func(s string) string { return s + string(ke.Rune) })
			}
		}),
		tui.OnStop(tui.KeyBackspace, func(ke tui.KeyEvent) {
			a.cloneText.Update(func(s string) string {
				if len(s) == 0 {
					return s
				}
				return s[:len(s)-1]
			})
		}),
		tui.OnStop(tui.KeyEnter, func(ke tui.KeyEvent) { a.cloneCommit() }),
		tui.OnStop(tui.KeyEscape, func(ke tui.KeyEvent) { a.cloneClose() }),
	}
}
