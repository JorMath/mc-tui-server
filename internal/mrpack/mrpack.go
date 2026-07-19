// Package mrpack lee e instala modpacks de Modrinth (.mrpack): un zip con
// un modrinth.index.json (archivos a descargar + dependencias de loader) y
// carpetas overrides/ y server-overrides/ que se copian a la instancia.
package mrpack

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const indexName = "modrinth.index.json"

// Env indica en qué lado aplica un archivo: required, optional o unsupported.
type Env struct {
	Client string `json:"client"`
	Server string `json:"server"`
}

// IndexFile es un archivo del modpack a descargar en la instancia.
type IndexFile struct {
	Path      string   `json:"path"`
	Env       *Env     `json:"env"`
	Downloads []string `json:"downloads"`
	FileSize  int64    `json:"fileSize"`
}

// Index es el contenido de modrinth.index.json.
type Index struct {
	FormatVersion int               `json:"formatVersion"`
	Game          string            `json:"game"`
	VersionID     string            `json:"versionId"`
	Name          string            `json:"name"`
	Files         []IndexFile       `json:"files"`
	Dependencies  map[string]string `json:"dependencies"`
}

// Parse abre el .mrpack (zip) y decodifica su modrinth.index.json.
func Parse(path string) (Index, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return Index{}, fmt.Errorf("opening %s: %w", path, err)
	}
	defer r.Close()
	for _, f := range r.File {
		if f.Name != indexName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return Index{}, fmt.Errorf("opening %s in the pack: %w", indexName, err)
		}
		defer rc.Close()
		var ix Index
		if err := json.NewDecoder(rc).Decode(&ix); err != nil {
			return Index{}, fmt.Errorf("parsing %s: %w", indexName, err)
		}
		return ix, nil
	}
	return Index{}, fmt.Errorf("the pack has no %s (is it a .mrpack?)", indexName)
}

// Loader identifica el loader que exige el modpack y sus versiones.
type Loader struct {
	Name    string // fabric, forge, neoforge o quilt
	MC      string // versión de Minecraft
	Version string // versión del loader
}

// loaderDeps mapea la clave de dependencies del índice al nombre del loader.
var loaderDeps = []struct{ dep, name string }{
	{"fabric-loader", "fabric"},
	{"quilt-loader", "quilt"},
	{"forge", "forge"},
	{"neoforge", "neoforge"},
}

// Loader valida el índice y devuelve el loader que pide el pack.
func (ix Index) Loader() (Loader, error) {
	mc := ix.Dependencies["minecraft"]
	if mc == "" {
		return Loader{}, fmt.Errorf("the pack index does not declare a minecraft version")
	}
	for _, d := range loaderDeps {
		if v, ok := ix.Dependencies[d.dep]; ok && v != "" {
			return Loader{Name: d.name, MC: mc, Version: v}, nil
		}
	}
	return Loader{}, fmt.Errorf("the pack index does not declare a supported loader (fabric, quilt, forge or neoforge)")
}

// ModrinthProject extrae el ID de proyecto de Modrinth de la URL de
// descarga del archivo (cdn.modrinth.com/data/<id>/...), o "" si el
// archivo no viene del CDN de Modrinth.
func (f IndexFile) ModrinthProject() string {
	for _, u := range f.Downloads {
		const marker = "cdn.modrinth.com/data/"
		_, rest, ok := strings.Cut(u, marker)
		if !ok {
			continue
		}
		id, _, _ := strings.Cut(rest, "/")
		return id
	}
	return ""
}

// ServerFiles devuelve los archivos que aplican al servidor (env.server
// distinto de unsupported), validando que las rutas queden dentro de la
// instancia y tengan URL de descarga.
func (ix Index) ServerFiles() ([]IndexFile, error) {
	var out []IndexFile
	for _, f := range ix.Files {
		if f.Env != nil && f.Env.Server == "unsupported" {
			continue
		}
		if !filepath.IsLocal(filepath.FromSlash(f.Path)) {
			return nil, fmt.Errorf("the pack index has an unsafe file path: %q", f.Path)
		}
		if len(f.Downloads) == 0 {
			return nil, fmt.Errorf("file %q has no download URL", f.Path)
		}
		out = append(out, f)
	}
	return out, nil
}

// ExtractOverrides copia overrides/ y luego server-overrides/ del .mrpack
// al directorio de la instancia (los server-overrides pisan a los overrides).
func ExtractOverrides(path, dest string) error {
	r, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer r.Close()
	for _, prefix := range []string{"overrides/", "server-overrides/"} {
		for _, f := range r.File {
			if !strings.HasPrefix(f.Name, prefix) || f.FileInfo().IsDir() {
				continue
			}
			rel := strings.TrimPrefix(f.Name, prefix)
			if rel == "" || !filepath.IsLocal(filepath.FromSlash(rel)) {
				return fmt.Errorf("the pack has an unsafe override path: %q", f.Name)
			}
			if err := extractFile(f, filepath.Join(dest, filepath.FromSlash(rel))); err != nil {
				return err
			}
		}
	}
	return nil
}

func extractFile(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("opening %s in the pack: %w", f.Name, err)
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
