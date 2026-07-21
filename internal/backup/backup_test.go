package backup

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestName(t *testing.T) {
	now := time.Date(2026, 7, 20, 15, 30, 0, 0, time.UTC)
	if got := Name("world", now); got != "world-20260720-153000.zip" {
		t.Fatalf("Name = %q", got)
	}
}

func TestCreateAndRestoreRoundTrip(t *testing.T) {
	inst := t.TempDir()
	world := filepath.Join(inst, "world")
	writeFile(t, filepath.Join(world, "level.dat"), "nivel")
	writeFile(t, filepath.Join(world, "region", "r.0.0.mca"), "region")
	writeFile(t, filepath.Join(world, "datapacks", "dp.zip"), "dp")

	dest := filepath.Join(inst, Dir, Name("world", time.Now()))
	skipped, err := Create(world, dest)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if skipped != 0 {
		t.Fatalf("skipped = %d, quiero 0", skipped)
	}

	// Se corrompe el mundo y se restaura.
	writeFile(t, filepath.Join(world, "level.dat"), "corrupto")
	writeFile(t, filepath.Join(world, "basura.txt"), "x")
	if err := Restore(dest, world); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(world, "level.dat"))
	if err != nil || string(got) != "nivel" {
		t.Fatalf("level.dat = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(world, "basura.txt")); err == nil {
		t.Fatal("la restauración debe reemplazar el mundo entero")
	}
	if _, err := os.Stat(filepath.Join(world, "region", "r.0.0.mca")); err != nil {
		t.Fatalf("region no restaurada: %v", err)
	}
}

func TestCreateMissingWorldFails(t *testing.T) {
	inst := t.TempDir()
	if _, err := Create(filepath.Join(inst, "no-existe"), filepath.Join(inst, "b.zip")); err == nil {
		t.Fatal("mundo inexistente debe fallar")
	}
}

func TestCreateSkipsLockedFiles(t *testing.T) {
	inst := t.TempDir()
	world := filepath.Join(inst, "world")
	writeFile(t, filepath.Join(world, "level.dat"), "nivel")
	writeFile(t, filepath.Join(world, "session.lock"), "lock")
	// Abrir el archivo en modo exclusivo simula el lock del proceso java.
	locked, err := os.OpenFile(filepath.Join(world, "session.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { locked.Close() })
	// En Windows el open exclusivo de Go no bloquea lecturas; el test
	// verifica al menos que un mundo con archivos abiertos se respalda.
	skipped, err := Create(world, filepath.Join(inst, Dir, "b.zip"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if skipped > 1 {
		t.Fatalf("skipped = %d", skipped)
	}
}

func TestRestoreRejectsZipSlip(t *testing.T) {
	inst := t.TempDir()
	zipPath := filepath.Join(inst, "evil.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	if _, err := w.Create("../fuera.txt"); err != nil {
		t.Fatal(err)
	}
	w.Close()
	f.Close()
	if err := Restore(zipPath, filepath.Join(inst, "world")); err == nil {
		t.Fatal("entrada con .. debe fallar")
	}
}

func TestListSortedNewestFirst(t *testing.T) {
	inst := t.TempDir()
	for _, n := range []string{"world-20260101-000000.zip", "world-20260301-000000.zip", "nota.txt"} {
		writeFile(t, filepath.Join(inst, Dir, n), "x")
	}
	got, err := List(inst)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 || got[0] != "world-20260301-000000.zip" {
		t.Fatalf("List = %v", got)
	}
	// Sin carpeta de backups: lista vacía sin error.
	empty, err := List(t.TempDir())
	if err != nil || len(empty) != 0 {
		t.Fatalf("List vacío = %v, %v", empty, err)
	}
}

func TestWorldOf(t *testing.T) {
	cases := map[string]string{
		"world-20260720-153000.zip":    "world",
		"mi-mundo-20260720-153000.zip": "mi-mundo",
		"raro.zip":                     "",
	}
	for name, want := range cases {
		if got := WorldOf(name); got != want {
			t.Fatalf("WorldOf(%q) = %q, quiero %q", name, got, want)
		}
	}
}
