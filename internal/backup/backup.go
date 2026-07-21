// Package backup comprime y restaura mundos de una instancia como zips
// con timestamp en la carpeta backups/ de la instancia.
package backup

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Dir es la subcarpeta de la instancia donde viven los backups.
const Dir = "backups"

// Name arma el nombre de archivo de un backup nuevo para el mundo dado.
func Name(world string, now time.Time) string {
	return fmt.Sprintf("%s-%s.zip", world, now.Format("20060102-150405"))
}

// Create comprime worldDir (recursivo) en destZip, con rutas relativas a
// la raíz del mundo. Crea los directorios padre del zip. Los archivos que
// no se pueden abrir (p. ej. session.lock, bloqueado por el proceso java
// durante un backup en caliente) se saltan y se cuentan en skipped.
func Create(worldDir, destZip string) (skipped int, err error) {
	info, err := os.Stat(worldDir)
	if err != nil {
		return 0, fmt.Errorf("world folder: %w", err)
	}
	if !info.IsDir() {
		return 0, fmt.Errorf("%s is not a folder", worldDir)
	}
	if err := os.MkdirAll(filepath.Dir(destZip), 0o755); err != nil {
		return 0, fmt.Errorf("creating backups folder: %w", err)
	}
	f, err := os.Create(destZip)
	if err != nil {
		return 0, fmt.Errorf("creating %s: %w", destZip, err)
	}
	defer f.Close()
	w := zip.NewWriter(f)

	err = filepath.WalkDir(worldDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		src, err := os.Open(path)
		if err != nil {
			skipped++
			return nil
		}
		defer src.Close()
		rel, err := filepath.Rel(worldDir, path)
		if err != nil {
			return err
		}
		entry, err := w.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		_, err = io.Copy(entry, src)
		return err
	})
	if err != nil {
		w.Close()
		return skipped, fmt.Errorf("zipping %s: %w", worldDir, err)
	}
	if err := w.Close(); err != nil {
		return skipped, fmt.Errorf("finishing %s: %w", destZip, err)
	}
	return skipped, nil
}

// Restore reemplaza worldDir con el contenido del zip (zip-slip safe).
// El mundo actual se elimina por completo antes de extraer.
func Restore(zipPath, worldDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", zipPath, err)
	}
	defer r.Close()
	for _, f := range r.File {
		if !filepath.IsLocal(filepath.FromSlash(f.Name)) {
			return fmt.Errorf("the backup has an unsafe path: %q", f.Name)
		}
	}
	if err := os.RemoveAll(worldDir); err != nil {
		return fmt.Errorf("removing current world: %w", err)
	}
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if err := extract(f, filepath.Join(worldDir, filepath.FromSlash(f.Name))); err != nil {
			return err
		}
	}
	return nil
}

func extract(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("opening %s in the backup: %w", f.Name, err)
	}
	defer rc.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("creating directory for %s: %w", dest, err)
	}
	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dest, err)
	}
	defer out.Close()
	if _, err := io.Copy(out, rc); err != nil {
		return fmt.Errorf("writing %s: %w", dest, err)
	}
	return nil
}

// List devuelve los zips de la carpeta de backups de la instancia, más
// nuevos primero. Sin carpeta devuelve lista vacía.
func List(instDir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(instDir, Dir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading backups folder: %w", err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".zip") {
			out = append(out, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out, nil
}

// WorldOf deduce el nombre del mundo de un backup ("world-20260720-....zip"
// → "world"). Devuelve "" si el nombre no sigue la convención.
func WorldOf(backupName string) string {
	base := strings.TrimSuffix(backupName, ".zip")
	i := strings.LastIndex(base, "-")
	if i <= 0 {
		return ""
	}
	j := strings.LastIndex(base[:i], "-")
	if j <= 0 {
		return ""
	}
	return base[:j]
}
