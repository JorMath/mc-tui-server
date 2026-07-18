// Package properties lee y edita archivos server.properties (R3)
// preservando comentarios, líneas en blanco y el orden original.
package properties

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

type lineKind int

const (
	kindRaw  lineKind = iota // comentario, línea en blanco o sin '='
	kindProp                 // clave=valor
)

type line struct {
	kind  lineKind
	raw   string // solo para kindRaw
	key   string
	value string
}

// File es un server.properties en memoria.
type File struct {
	lines []line
}

// Parse construye un File desde el contenido crudo. Acepta finales de
// línea LF y CRLF.
func Parse(data []byte) *File {
	f := &File{}
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return f
	}
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSuffix(l, "\r")
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "!") {
			f.lines = append(f.lines, line{kind: kindRaw, raw: l})
			continue
		}
		key, value, found := strings.Cut(l, "=")
		if !found {
			f.lines = append(f.lines, line{kind: kindRaw, raw: l})
			continue
		}
		f.lines = append(f.lines, line{kind: kindProp, key: key, value: value})
	}
	return f
}

// Load lee el archivo dado. Si no existe devuelve un File vacío sin error
// (el server.properties se crea en el primer arranque del servidor).
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &File{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("leyendo %s: %w", path, err)
	}
	return Parse(data), nil
}

// Keys devuelve las claves en el orden del archivo.
func (f *File) Keys() []string {
	var out []string
	for _, l := range f.lines {
		if l.kind == kindProp {
			out = append(out, l.key)
		}
	}
	return out
}

// Get devuelve el valor de una clave.
func (f *File) Get(key string) (string, bool) {
	for _, l := range f.lines {
		if l.kind == kindProp && l.key == key {
			return l.value, true
		}
	}
	return "", false
}

// Set actualiza una clave existente en su lugar, o la añade al final.
func (f *File) Set(key, value string) {
	for i := range f.lines {
		if f.lines[i].kind == kindProp && f.lines[i].key == key {
			f.lines[i].value = value
			return
		}
	}
	f.lines = append(f.lines, line{kind: kindProp, key: key, value: value})
}

// Bytes serializa el archivo con finales de línea LF.
func (f *File) Bytes() []byte {
	var b strings.Builder
	for _, l := range f.lines {
		if l.kind == kindProp {
			b.WriteString(l.key + "=" + l.value + "\n")
			continue
		}
		b.WriteString(l.raw + "\n")
	}
	return []byte(b.String())
}

// Save escribe el archivo en disco.
func (f *File) Save(path string) error {
	if err := os.WriteFile(path, f.Bytes(), 0o644); err != nil {
		return fmt.Errorf("escribiendo %s: %w", path, err)
	}
	return nil
}
