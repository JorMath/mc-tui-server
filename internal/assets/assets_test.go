package assets

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mc-tui-server/internal/config"
)

// makeWorld crea un directorio de mundo válido (con level.dat).
func makeWorld(t *testing.T, dir, name string) {
	t.Helper()
	w := filepath.Join(dir, name)
	if err := os.MkdirAll(w, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w, "level.dat"), []byte("nbt"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWorldsDetectsLevelDat(t *testing.T) {
	dir := t.TempDir()
	makeWorld(t, dir, "world")
	makeWorld(t, dir, "world_nether")
	// Directorios que NO son mundos:
	if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.jar"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Worlds(dir)
	if err != nil {
		t.Fatalf("Worlds: %v", err)
	}
	if strings.Join(got, ",") != "world,world_nether" {
		t.Fatalf("Worlds = %v", got)
	}
}

func TestWorldsMissingDirIsEmpty(t *testing.T) {
	got, err := Worlds(filepath.Join(t.TempDir(), "no-existe"))
	if err != nil {
		t.Fatalf("Worlds sobre dir inexistente: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Worlds = %v, quiero vacío", got)
	}
}

func TestWorldsInvalidPathFails(t *testing.T) {
	// Un NUL en la ruta produce un error de E/S distinto a "no existe"
	// en Windows y Linux por igual.
	if _, err := Worlds("ruta\x00invalida"); err == nil {
		t.Fatal("Worlds con ruta inválida debe fallar")
	}
}

func TestPluginsDirByType(t *testing.T) {
	cases := []struct {
		typ  config.ServerType
		want string
		ok   bool
	}{
		{config.Paper, "plugins", true},
		{config.Purpur, "plugins", true},
		{config.Fabric, "mods", true},
		{config.Vanilla, "", false},
	}
	for _, c := range cases {
		got, ok := PluginsDir(c.typ)
		if got != c.want || ok != c.ok {
			t.Fatalf("PluginsDir(%s) = %q,%v; quiero %q,%v", c.typ, got, ok, c.want, c.ok)
		}
	}
}

func TestPluginsListsJars(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "plugins")
	if err := os.MkdirAll(filepath.Join(pdir, "subcarpeta"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"essentials.jar", "worldedit.jar", "config.yml"} {
		if err := os.WriteFile(filepath.Join(pdir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := Plugins(dir, config.Paper)
	if err != nil {
		t.Fatalf("Plugins: %v", err)
	}
	if strings.Join(got, ",") != "essentials.jar,worldedit.jar" {
		t.Fatalf("Plugins = %v", got)
	}
}

func TestPluginsVanillaIsEmpty(t *testing.T) {
	got, err := Plugins(t.TempDir(), config.Vanilla)
	if err != nil {
		t.Fatalf("Plugins vanilla: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Plugins vanilla = %v, quiero vacío", got)
	}
}

func TestPluginsMissingSubdirIsEmpty(t *testing.T) {
	got, err := Plugins(t.TempDir(), config.Fabric)
	if err != nil {
		t.Fatalf("Plugins sin mods/: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Plugins = %v, quiero vacío", got)
	}
}

func TestPluginsInvalidPathFails(t *testing.T) {
	if _, err := Plugins("ruta\x00invalida", config.Paper); err == nil {
		t.Fatal("Plugins con ruta inválida debe fallar")
	}
}

func TestDeleteWorld(t *testing.T) {
	dir := t.TempDir()
	makeWorld(t, dir, "world")
	if err := DeleteWorld(dir, "world"); err != nil {
		t.Fatalf("DeleteWorld: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "world")); !os.IsNotExist(err) {
		t.Fatal("el mundo sigue existiendo tras DeleteWorld")
	}
}

func TestDeleteWorldRejectsBadNames(t *testing.T) {
	dir := t.TempDir()
	makeWorld(t, dir, "world")
	for _, name := range []string{"", "..", "../world", `sub\world`, "sub/world"} {
		if err := DeleteWorld(dir, name); err == nil {
			t.Fatalf("DeleteWorld(%q) debe fallar", name)
		}
	}
}

func TestDeleteWorldRejectsNonWorld(t *testing.T) {
	dir := t.TempDir()
	// Directorio sin level.dat: no es un mundo, no debe borrarse.
	if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := DeleteWorld(dir, "plugins"); err == nil {
		t.Fatal("DeleteWorld sobre un directorio sin level.dat debe fallar")
	}
	if err := DeleteWorld(dir, "no-existe"); err == nil {
		t.Fatal("DeleteWorld sobre un mundo inexistente debe fallar")
	}
}

func TestDeleteWorldRemoveErrorPropagates(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("depende del bloqueo de archivos abiertos de Windows")
	}
	dir := t.TempDir()
	makeWorld(t, dir, "world")
	// Con level.dat abierto, RemoveAll falla en Windows.
	f, err := os.Open(filepath.Join(dir, "world", "level.dat"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := DeleteWorld(dir, "world"); err == nil {
		t.Fatal("DeleteWorld con archivo bloqueado debe fallar")
	}
}

func TestDeletePlugin(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "mods")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	jar := filepath.Join(pdir, "sodium.jar")
	if err := os.WriteFile(jar, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := DeletePlugin(dir, config.Fabric, "sodium.jar"); err != nil {
		t.Fatalf("DeletePlugin: %v", err)
	}
	if _, err := os.Stat(jar); !os.IsNotExist(err) {
		t.Fatal("el jar sigue existiendo tras DeletePlugin")
	}
}

func TestDeletePluginRejectsBadNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"", "..", "../x.jar", "config.yml", "sub/x.jar"} {
		if err := DeletePlugin(dir, config.Paper, name); err == nil {
			t.Fatalf("DeletePlugin(%q) debe fallar", name)
		}
	}
}

func TestDeletePluginVanillaFails(t *testing.T) {
	if err := DeletePlugin(t.TempDir(), config.Vanilla, "x.jar"); err == nil {
		t.Fatal("DeletePlugin en vanilla debe fallar (no hay carpeta de plugins)")
	}
}

func TestDeletePluginMissingFails(t *testing.T) {
	if err := DeletePlugin(t.TempDir(), config.Paper, "no-existe.jar"); err == nil {
		t.Fatal("DeletePlugin de jar inexistente debe fallar")
	}
}
