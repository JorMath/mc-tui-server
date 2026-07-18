package config

import (
	"os"
	"path/filepath"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(filepath.Join(t.TempDir(), "instances.json"))
}

func sample(name string) Instance {
	return Instance{
		Name:     name,
		Dir:      `C:\servers\` + name,
		JarPath:  "server.jar",
		JavaPath: "java",
		JavaArgs: []string{"-XX:+UseG1GC"},
		MemoryMB: 2048,
		Type:     Paper,
		Version:  "1.21.4",
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	s := testStore(t)
	if err := s.Load(); err != nil {
		t.Fatalf("Load() con archivo inexistente: %v", err)
	}
	if got := len(s.Instances()); got != 0 {
		t.Fatalf("Instances() = %d, quiero 0", got)
	}
}

func TestAddGetAndPersistence(t *testing.T) {
	s := testStore(t)
	if err := s.Add(sample("survival")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Add(sample("creativo")); err != nil {
		t.Fatalf("Add segundo: %v", err)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Un Store nuevo sobre el mismo archivo debe ver lo mismo.
	s2 := NewStore(s.Path())
	if err := s2.Load(); err != nil {
		t.Fatalf("Load tras Save: %v", err)
	}
	if got := len(s2.Instances()); got != 2 {
		t.Fatalf("Instances() = %d, quiero 2", got)
	}
	inst, ok := s2.Get("survival")
	if !ok {
		t.Fatal("Get(survival) no encontrado tras recargar")
	}
	if inst.MemoryMB != 2048 || inst.Type != Paper || inst.Version != "1.21.4" {
		t.Fatalf("instancia recargada no coincide: %+v", inst)
	}
}

func TestAddRejectsEmptyName(t *testing.T) {
	s := testStore(t)
	if err := s.Add(Instance{}); err == nil {
		t.Fatal("Add con nombre vacío debe fallar")
	}
}

func TestAddRejectsDuplicateName(t *testing.T) {
	s := testStore(t)
	if err := s.Add(sample("uno")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Add(sample("uno")); err == nil {
		t.Fatal("Add duplicado debe fallar")
	}
}

func TestUpdateExisting(t *testing.T) {
	s := testStore(t)
	if err := s.Add(sample("uno")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	mod := sample("uno")
	mod.MemoryMB = 4096
	if err := s.Update(mod); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := s.Get("uno")
	if got.MemoryMB != 4096 {
		t.Fatalf("MemoryMB = %d, quiero 4096", got.MemoryMB)
	}
}

func TestUpdateMissingFails(t *testing.T) {
	s := testStore(t)
	if err := s.Update(sample("fantasma")); err == nil {
		t.Fatal("Update de instancia inexistente debe fallar")
	}
}

func TestRemove(t *testing.T) {
	s := testStore(t)
	if err := s.Add(sample("uno")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Remove("uno"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := s.Get("uno"); ok {
		t.Fatal("la instancia sigue presente tras Remove")
	}
	if err := s.Remove("uno"); err == nil {
		t.Fatal("Remove de instancia inexistente debe fallar")
	}
}

func TestSaveCreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "anidado", "mas", "instances.json"))
	if err := s.Add(sample("uno")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("Save con directorios inexistentes: %v", err)
	}
	if _, err := os.Stat(s.Path()); err != nil {
		t.Fatalf("el archivo no existe tras Save: %v", err)
	}
}

func TestLoadCorruptFileFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instances.json")
	if err := os.WriteFile(path, []byte("{esto no es json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewStore(path)
	if err := s.Load(); err == nil {
		t.Fatal("Load con JSON corrupto debe fallar")
	}
}

func TestLoadUnreadableFileFails(t *testing.T) {
	// Un directorio en lugar de archivo provoca un error de lectura
	// distinto a "no existe".
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Load(); err == nil {
		t.Fatal("Load sobre un directorio debe fallar")
	}
}

func TestSaveFailsWhenParentIsFile(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "bloqueo")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewStore(filepath.Join(blocker, "instances.json"))
	if err := s.Save(); err == nil {
		t.Fatal("Save con padre-archivo debe fallar en MkdirAll")
	}
}

func TestSaveFailsWhenPathIsDirectory(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir) // la ruta ya existe como directorio
	if err := s.Save(); err == nil {
		t.Fatal("Save sobre un directorio debe fallar en WriteFile")
	}
}

func TestDefaultPathFailsWithoutConfigDir(t *testing.T) {
	t.Setenv("APPDATA", "")
	if _, err := DefaultPath(); err == nil {
		t.Skip("os.UserConfigDir no depende de APPDATA en esta plataforma")
	}
}

func TestDefaultPath(t *testing.T) {
	p, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if filepath.Base(p) != "instances.json" {
		t.Fatalf("DefaultPath = %q, debe terminar en instances.json", p)
	}
}
