// packupdate.go: actualización de instancias creadas desde un modpack de
// Modrinth (v0.3.0) — tecla U: busca la versión nueva del pack, confirma,
// respalda el mundo y aplica el diff del índice.
package main

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/JorMath/mc-tui-server/internal/backup"
	"github.com/JorMath/mc-tui-server/internal/download"
	"github.com/JorMath/mc-tui-server/internal/mrpack"
	"github.com/JorMath/mc-tui-server/internal/server"
	tui "github.com/grindlemire/go-tui"
)

// pkUpdateAsk (tecla U) busca si el modpack de la instancia tiene una
// versión más nueva y pide confirmación antes de tocar nada.
func (a *app) pkUpdateAsk() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	inst, ok := a.store.Get(mgr.Instance().Name)
	if !ok {
		return
	}
	if inst.PackID == "" {
		a.appendLog(inst.Name, "[mc-tui] This instance was not created from a modpack (or predates pack tracking)")
		return
	}
	if st := mgr.Status(); st == server.Running || st == server.Stopping {
		a.appendLog(inst.Name, "[mc-tui] Stop the server before updating the modpack")
		return
	}
	name := inst.Name
	a.appendLog(name, "[mc-tui] Checking for modpack updates...")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		vers, err := a.mr.ModpackVersions(ctx, inst.PackID)
		if err != nil {
			a.appendLog(name, "[mc-tui] Error: "+err.Error())
			return
		}
		latest := vers[0]
		if latest.ID == inst.PackVerID {
			a.appendLog(name, fmt.Sprintf("[mc-tui] Modpack is up to date (%s)", latest.VersionNumber))
			return
		}
		a.pkPending = latest
		a.pkConfirm.Set(fmt.Sprintf(
			"Update %q to modpack version %s? The world is backed up first", name, latest.VersionNumber))
	}()
}

func (a *app) pkKeyMap() tui.KeyMap {
	cancel := func(ke tui.KeyEvent) { a.pkConfirm.Set("") }
	return tui.KeyMap{
		tui.OnStop(tui.Rune('y'), func(ke tui.KeyEvent) { a.pkApply() }),
		tui.OnStop(tui.Rune('n'), cancel),
		tui.OnStop(tui.KeyEscape, cancel),
	}
}

// pkApply aplica la actualización confirmada: backup del mundo, diff del
// índice (borra lo removido, descarga lo nuevo o cambiado), overrides y
// runtime del loader si cambió.
func (a *app) pkApply() {
	a.pkConfirm.Set("")
	mgr := a.current()
	pv := a.pkPending
	if mgr == nil || pv.ID == "" {
		return
	}
	inst, ok := a.store.Get(mgr.Instance().Name)
	if !ok {
		return
	}
	if st := mgr.Status(); st == server.Running || st == server.Stopping {
		a.appendLog(inst.Name, "[mc-tui] Stop the server before updating the modpack")
		return
	}
	name := inst.Name
	dir := inst.Dir
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		fail := func(err error) { a.appendLog(name, "[mc-tui] Modpack update failed: "+err.Error()) }

		// 1. Backup del mundo antes de tocar nada.
		world := worldName(inst)
		worldDir := filepath.Join(dir, world)
		if _, err := os.Stat(worldDir); err == nil {
			bname := backup.Name(world, time.Now())
			if _, err := backup.Create(worldDir, filepath.Join(dir, backup.Dir, bname)); err != nil {
				fail(fmt.Errorf("world backup: %w", err))
				return
			}
			a.appendLog(name, fmt.Sprintf("[mc-tui] World backed up to %s/%s", backup.Dir, bname))
		}

		// 2. Descargar y parsear el pack nuevo.
		a.appendLog(name, fmt.Sprintf("[mc-tui] Downloading modpack %s...", pv.VersionNumber))
		packFile := filepath.Join(dir, pv.Filename)
		if err := download.DownloadFile(ctx, nil, pv.URL, packFile, nil); err != nil {
			fail(err)
			return
		}
		ix, err := mrpack.Parse(packFile)
		if err != nil {
			fail(err)
			return
		}
		ld, err := ix.Loader()
		if err != nil {
			fail(err)
			return
		}
		files, err := ix.ServerFiles()
		if err != nil {
			fail(err)
			return
		}
		files, skippedClient := a.wizDropClientOnly(ctx, files)

		// 3. Borrar archivos del pack viejo que ya no existen en el nuevo.
		newPaths := map[string]bool{}
		for _, f := range files {
			newPaths[f.Path] = true
		}
		removed := 0
		if old, err := mrpack.LoadIndex(filepath.Join(dir, mrpack.IndexCopyName)); err == nil {
			oldFiles, err := old.ServerFiles()
			if err == nil {
				for _, f := range oldFiles {
					if newPaths[f.Path] {
						continue
					}
					if err := os.Remove(filepath.Join(dir, filepath.FromSlash(f.Path))); err == nil {
						removed++
					}
				}
			}
		} else {
			a.appendLog(name, "[mc-tui] No saved pack index; nothing will be deleted, only added/updated")
		}

		// 4. Descargar los archivos nuevos o cambiados (por sha1).
		changed := 0
		for i, f := range files {
			dest := filepath.Join(dir, filepath.FromSlash(f.Path))
			if f.Hashes.SHA1 != "" {
				if h, err := sha1Of(dest); err == nil && h == f.Hashes.SHA1 {
					continue
				}
			}
			a.appendLog(name, fmt.Sprintf("[mc-tui] Downloading %d/%d — %s", i+1, len(files), path.Base(f.Path)))
			if err := download.DownloadFile(ctx, nil, f.Downloads[0], dest, nil); err != nil {
				fail(err)
				return
			}
			changed++
		}

		// 5. Overrides del pack nuevo (pueden pisar configs locales).
		if err := mrpack.ExtractOverrides(packFile, dir); err != nil {
			fail(err)
			return
		}
		_ = os.Remove(packFile)

		// 6. Runtime del loader, solo si la versión cambió.
		jarPath, argsDir := inst.JarPath, inst.ArgsDir
		oldLd := mrpack.Loader{}
		if old, err := mrpack.LoadIndex(filepath.Join(dir, mrpack.IndexCopyName)); err == nil {
			oldLd, _ = old.Loader()
		}
		if oldLd != ld {
			a.appendLog(name, fmt.Sprintf("[mc-tui] Loader changed (%s %s → %s %s), reinstalling runtime...",
				oldLd.Name, oldLd.Version, ld.Name, ld.Version))
			jarPath, argsDir, err = a.wizInstallLoader(ctx, dir, ld)
			if err != nil {
				fail(err)
				return
			}
		}

		// 7. Persistir instancia e índice nuevos.
		if err := mrpack.WriteIndex(ix, filepath.Join(dir, mrpack.IndexCopyName)); err != nil {
			a.appendLog(name, "[mc-tui] Warning: could not save the pack index: "+err.Error())
		}
		inst.JarPath = jarPath
		inst.ArgsDir = argsDir
		inst.Type = loaderTypes[ld.Name]
		inst.Version = ld.MC
		inst.PackVerID = pv.ID
		if err := a.store.Update(inst); err != nil {
			fail(err)
			return
		}
		if err := a.store.Save(); err != nil {
			fail(err)
			return
		}
		_ = mgr.SetInstance(inst)
		summary := fmt.Sprintf("[mc-tui] Modpack updated to %s: %d files updated, %d removed", pv.VersionNumber, changed, removed)
		if skippedClient > 0 {
			summary += fmt.Sprintf(", %d client-only skipped", skippedClient)
		}
		a.appendLog(name, summary)
	}()
}
