package properties

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `#Minecraft server properties
#Fri Jul 18 12:00:00 UTC 2026
motd=Un servidor
max-players=20

difficulty=easy
linea rara sin igual
level-name=world
`

func TestParseAndKeysPreservesOrder(t *testing.T) {
	f := Parse([]byte(sample))
	want := []string{"motd", "max-players", "difficulty", "level-name"}
	got := f.Keys()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Keys = %v, quiero %v", got, want)
	}
}

func TestGet(t *testing.T) {
	f := Parse([]byte(sample))
	if v, ok := f.Get("max-players"); !ok || v != "20" {
		t.Fatalf("Get(max-players) = %q,%v", v, ok)
	}
	if _, ok := f.Get("inexistente"); ok {
		t.Fatal("Get de clave inexistente debe devolver false")
	}
}

func TestRoundTripPreservesEverything(t *testing.T) {
	f := Parse([]byte(sample))
	if got := string(f.Bytes()); got != sample {
		t.Fatalf("round-trip alteró el contenido:\n%q\n!=\n%q", got, sample)
	}
}

func TestSetExistingKeepsPosition(t *testing.T) {
	f := Parse([]byte(sample))
	f.Set("max-players", "50")
	out := string(f.Bytes())
	if !strings.Contains(out, "max-players=50") {
		t.Fatalf("no se actualizó el valor:\n%s", out)
	}
	// La clave sigue antes de difficulty y los comentarios se conservan.
	if strings.Index(out, "max-players=50") > strings.Index(out, "difficulty=") {
		t.Fatal("Set movió la clave de lugar")
	}
	if !strings.Contains(out, "#Minecraft server properties") {
		t.Fatal("Set perdió los comentarios")
	}
}

func TestSetNewAppends(t *testing.T) {
	f := Parse([]byte(sample))
	f.Set("pvp", "false")
	if v, ok := f.Get("pvp"); !ok || v != "false" {
		t.Fatalf("Get(pvp) tras Set = %q,%v", v, ok)
	}
	lines := strings.Split(strings.TrimRight(string(f.Bytes()), "\n"), "\n")
	if lines[len(lines)-1] != "pvp=false" {
		t.Fatalf("la clave nueva debe ir al final, última línea: %q", lines[len(lines)-1])
	}
}

func TestValueWithEquals(t *testing.T) {
	f := Parse([]byte("motd=a=b=c\n"))
	if v, _ := f.Get("motd"); v != "a=b=c" {
		t.Fatalf("Get(motd) = %q, quiero a=b=c", v)
	}
}

func TestParseCRLF(t *testing.T) {
	f := Parse([]byte("motd=hola\r\nmax-players=10\r\n"))
	if v, _ := f.Get("motd"); v != "hola" {
		t.Fatalf("Get(motd) = %q, el \\r debe eliminarse", v)
	}
}

func TestParseEmpty(t *testing.T) {
	f := Parse(nil)
	if len(f.Keys()) != 0 {
		t.Fatal("Parse(nil) debe dar File vacío")
	}
	if len(f.Bytes()) != 0 {
		t.Fatal("Bytes() de File vacío debe ser vacío")
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	f, err := Load(filepath.Join(t.TempDir(), "no-existe.properties"))
	if err != nil {
		t.Fatalf("Load inexistente: %v", err)
	}
	if len(f.Keys()) != 0 {
		t.Fatal("archivo inexistente debe dar File vacío")
	}
}

func TestLoadUnreadableFails(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("Load sobre un directorio debe fallar")
	}
}

func TestLoadAndSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.properties")
	if err := os.WriteFile(path, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	f.Set("motd", "Otro nombre")
	if err := f.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	f2, err := Load(path)
	if err != nil {
		t.Fatalf("Load tras Save: %v", err)
	}
	if v, _ := f2.Get("motd"); v != "Otro nombre" {
		t.Fatalf("motd = %q tras recargar", v)
	}
}

func TestSaveFailsOnDirectory(t *testing.T) {
	f := Parse([]byte(sample))
	if err := f.Save(t.TempDir()); err == nil {
		t.Fatal("Save sobre un directorio debe fallar")
	}
}

func TestEmptyValueAllowed(t *testing.T) {
	f := Parse([]byte("resource-pack=\n"))
	if v, ok := f.Get("resource-pack"); !ok || v != "" {
		t.Fatalf("Get(resource-pack) = %q,%v; quiero cadena vacía y true", v, ok)
	}
	f.Set("resource-pack", "http://x")
	if v, _ := f.Get("resource-pack"); v != "http://x" {
		t.Fatalf("Set sobre valor vacío no funcionó: %q", v)
	}
}
