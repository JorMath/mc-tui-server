package mrpack

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePack crea un .mrpack sintético en un directorio temporal.
func writePack(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pack.mrpack")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creando zip: %v", err)
	}
	w := zip.NewWriter(f)
	for name, content := range entries {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatalf("entrada %s: %v", name, err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatalf("escribiendo %s: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("cerrando zip: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("cerrando archivo: %v", err)
	}
	return path
}

const fabricIndex = `{
	"formatVersion": 1,
	"game": "minecraft",
	"versionId": "1.0.0",
	"name": "Test Pack",
	"files": [
		{"path": "mods/a.jar", "env": {"client": "required", "server": "required"},
		 "downloads": ["https://cdn.example/a.jar"], "fileSize": 10},
		{"path": "mods/client-only.jar", "env": {"client": "required", "server": "unsupported"},
		 "downloads": ["https://cdn.example/c.jar"], "fileSize": 10},
		{"path": "mods/no-env.jar", "downloads": ["https://cdn.example/n.jar"], "fileSize": 10}
	],
	"dependencies": {"minecraft": "1.21.4", "fabric-loader": "0.16.9"}
}`

func TestParseReadsIndex(t *testing.T) {
	path := writePack(t, map[string]string{indexName: fabricIndex})
	ix, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ix.Name != "Test Pack" || len(ix.Files) != 3 {
		t.Fatalf("ix = %+v", ix)
	}
	if ix.Dependencies["minecraft"] != "1.21.4" {
		t.Fatalf("dependencies = %v", ix.Dependencies)
	}
}

func TestParseWithoutIndexFails(t *testing.T) {
	path := writePack(t, map[string]string{"otro.txt": "x"})
	if _, err := Parse(path); err == nil {
		t.Fatal("un zip sin modrinth.index.json debe fallar")
	}
}

func TestParseNotAZipFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-zip.mrpack")
	if err := os.WriteFile(path, []byte("hola"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(path); err == nil {
		t.Fatal("un archivo que no es zip debe fallar")
	}
}

func TestLoaderDetectsEach(t *testing.T) {
	cases := []struct {
		dep, name string
	}{
		{"fabric-loader", "fabric"},
		{"quilt-loader", "quilt"},
		{"forge", "forge"},
		{"neoforge", "neoforge"},
	}
	for _, c := range cases {
		ix := Index{Dependencies: map[string]string{"minecraft": "1.20.1", c.dep: "1.2.3"}}
		ld, err := ix.Loader()
		if err != nil {
			t.Fatalf("Loader con %s: %v", c.dep, err)
		}
		if ld.Name != c.name || ld.MC != "1.20.1" || ld.Version != "1.2.3" {
			t.Fatalf("Loader con %s = %+v", c.dep, ld)
		}
	}
}

func TestLoaderRequiresDeps(t *testing.T) {
	ix := Index{Dependencies: map[string]string{"fabric-loader": "0.16.9"}}
	if _, err := ix.Loader(); err == nil {
		t.Fatal("sin versión de minecraft debe fallar")
	}
	ix = Index{Dependencies: map[string]string{"minecraft": "1.21.4"}}
	if _, err := ix.Loader(); err == nil || !strings.Contains(err.Error(), "loader") {
		t.Fatalf("sin loader debe fallar, err = %v", err)
	}
}

func TestServerFilesFiltersClientOnly(t *testing.T) {
	path := writePack(t, map[string]string{indexName: fabricIndex})
	ix, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	files, err := ix.ServerFiles()
	if err != nil {
		t.Fatalf("ServerFiles: %v", err)
	}
	// Se queda a.jar (server required) y no-env.jar (sin env = se instala);
	// client-only.jar (server unsupported) se descarta.
	if len(files) != 2 || files[0].Path != "mods/a.jar" || files[1].Path != "mods/no-env.jar" {
		t.Fatalf("files = %+v", files)
	}
}

func TestServerFilesRejectsUnsafePath(t *testing.T) {
	ix := Index{Files: []IndexFile{{Path: "../fuera.jar", Downloads: []string{"https://x/a.jar"}}}}
	if _, err := ix.ServerFiles(); err == nil {
		t.Fatal("una ruta que escapa de la instancia debe fallar")
	}
	ix = Index{Files: []IndexFile{{Path: "mods/a.jar"}}}
	if _, err := ix.ServerFiles(); err == nil {
		t.Fatal("un archivo sin URL de descarga debe fallar")
	}
}

func TestExtractOverrides(t *testing.T) {
	path := writePack(t, map[string]string{
		indexName:                          fabricIndex,
		"overrides/config/mod.toml":        "base",
		"overrides/server.properties":      "motd=hola",
		"server-overrides/config/mod.toml": "server-wins",
	})
	dest := t.TempDir()
	if err := ExtractOverrides(path, dest); err != nil {
		t.Fatalf("ExtractOverrides: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "config", "mod.toml"))
	if err != nil {
		t.Fatalf("leyendo override: %v", err)
	}
	// server-overrides se aplica después y pisa a overrides.
	if string(got) != "server-wins" {
		t.Fatalf("config/mod.toml = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "server.properties")); err != nil {
		t.Fatalf("server.properties no extraído: %v", err)
	}
}

func TestExtractOverridesRejectsZipSlip(t *testing.T) {
	path := writePack(t, map[string]string{"overrides/../../evil.txt": "x"})
	if err := ExtractOverrides(path, t.TempDir()); err == nil {
		t.Fatal("una entrada con .. debe fallar")
	}
}
