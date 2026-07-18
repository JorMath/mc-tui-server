// Package assets gestiona los archivos de una instancia (R3): carpetas de
// mundos y jars de plugins/mods.
package assets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mc-tui-server/internal/config"
)

// validName rechaza nombres vacíos o con separadores de ruta para que
// nunca se opere fuera del directorio de la instancia.
func validName(name string) error {
	if name == "" || name == ".." || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("nombre inválido: %q", name)
	}
	return nil
}

// Worlds lista las carpetas de mundos (subdirectorios con level.dat) del
// directorio de la instancia, ordenadas alfabéticamente.
func Worlds(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("listando %s: %w", dir, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "level.dat")); err == nil {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// PluginsDir devuelve la subcarpeta de plugins según el tipo de servidor,
// y false si la distribución no soporta plugins/mods.
func PluginsDir(t config.ServerType) (string, bool) {
	switch t {
	case config.Paper, config.Purpur:
		return "plugins", true
	case config.Fabric:
		return "mods", true
	default:
		return "", false
	}
}

// Plugins lista los .jar de la carpeta de plugins/mods de la instancia,
// ordenados alfabéticamente. Para tipos sin soporte devuelve lista vacía.
func Plugins(dir string, t config.ServerType) ([]string, error) {
	sub, ok := PluginsDir(t)
	if !ok {
		return nil, nil
	}
	entries, err := os.ReadDir(filepath.Join(dir, sub))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("listando %s: %w", filepath.Join(dir, sub), err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jar") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// DeleteWorld elimina la carpeta de un mundo. Solo borra directorios que
// realmente son mundos (contienen level.dat).
func DeleteWorld(dir, name string) error {
	if err := validName(name); err != nil {
		return err
	}
	target := filepath.Join(dir, name)
	if _, err := os.Stat(filepath.Join(target, "level.dat")); err != nil {
		return fmt.Errorf("%q no es una carpeta de mundo (falta level.dat)", name)
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("borrando el mundo %q: %w", name, err)
	}
	return nil
}

// DeletePlugin elimina un jar de la carpeta de plugins/mods.
func DeletePlugin(dir string, t config.ServerType, name string) error {
	if err := validName(name); err != nil {
		return err
	}
	if !strings.HasSuffix(name, ".jar") {
		return fmt.Errorf("%q no es un .jar", name)
	}
	sub, ok := PluginsDir(t)
	if !ok {
		return fmt.Errorf("el tipo %q no soporta plugins/mods", t)
	}
	if err := os.Remove(filepath.Join(dir, sub, name)); err != nil {
		return fmt.Errorf("borrando %q: %w", name, err)
	}
	return nil
}
