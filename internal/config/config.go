// Package config persiste la lista de instancias de servidores Minecraft
// gestionadas por la TUI en un archivo JSON local.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ServerType identifica la distribución del servidor.
type ServerType string

const (
	Vanilla  ServerType = "vanilla"
	Paper    ServerType = "paper"
	Purpur   ServerType = "purpur"
	Fabric   ServerType = "fabric"
	Forge    ServerType = "forge"
	NeoForge ServerType = "neoforge"
	Quilt    ServerType = "quilt"
)

// Instance describe un servidor Minecraft local registrado en la app.
// ArgsDir (relativo a Dir) apunta a la carpeta con win_args.txt/unix_args.txt
// de los servidores Forge/NeoForge modernos, que no se lanzan con -jar;
// cuando está vacío se usa JarPath.
type Instance struct {
	Name     string     `json:"name"`
	Dir      string     `json:"dir"`
	JarPath  string     `json:"jar_path"`
	ArgsDir  string     `json:"args_dir,omitempty"`
	JavaPath string     `json:"java_path"`
	JavaArgs []string   `json:"java_args,omitempty"`
	MemoryMB int        `json:"memory_mb"`
	Type     ServerType `json:"type"`
	Version  string     `json:"version"`
}

// Store maneja la colección de instancias respaldada por un archivo JSON.
type Store struct {
	path      string
	instances []Instance
}

// NewStore crea un Store vacío asociado al archivo dado. No toca el disco.
func NewStore(path string) *Store {
	return &Store{path: path}
}

// Path devuelve la ruta del archivo de respaldo.
func (s *Store) Path() string { return s.path }

// DefaultPath devuelve la ruta estándar del archivo de instancias
// (directorio de configuración del usuario / mc-tui-server / instances.json).
func DefaultPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("could not resolve the user config directory: %w", err)
	}
	return filepath.Join(base, "mc-tui-server", "instances.json"), nil
}

// Load lee las instancias desde el archivo. Si el archivo no existe,
// deja la lista vacía sin error.
func (s *Store) Load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.instances = nil
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", s.path, err)
	}
	var instances []Instance
	if err := json.Unmarshal(data, &instances); err != nil {
		return fmt.Errorf("parsing %s: %w", s.path, err)
	}
	s.instances = instances
	return nil
}

// Save escribe las instancias al archivo, creando los directorios padre.
func (s *Store) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("creating directory for %s: %w", s.path, err)
	}
	// []Instance solo contiene tipos serializables; Marshal no puede fallar.
	data, _ := json.MarshalIndent(s.instances, "", "  ")
	if err := os.WriteFile(s.path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", s.path, err)
	}
	return nil
}

// Instances devuelve una copia de la lista de instancias.
func (s *Store) Instances() []Instance {
	out := make([]Instance, len(s.instances))
	copy(out, s.instances)
	return out
}

// Get busca una instancia por nombre.
func (s *Store) Get(name string) (Instance, bool) {
	for _, inst := range s.instances {
		if inst.Name == name {
			return inst, true
		}
	}
	return Instance{}, false
}

// Add registra una instancia nueva. Falla con nombre vacío o duplicado.
func (s *Store) Add(inst Instance) error {
	if inst.Name == "" {
		return errors.New("the instance needs a name")
	}
	if _, exists := s.Get(inst.Name); exists {
		return fmt.Errorf("an instance named %q already exists", inst.Name)
	}
	s.instances = append(s.instances, inst)
	return nil
}

// Update reemplaza la instancia con el mismo nombre. Falla si no existe.
func (s *Store) Update(inst Instance) error {
	for i := range s.instances {
		if s.instances[i].Name == inst.Name {
			s.instances[i] = inst
			return nil
		}
	}
	return fmt.Errorf("no instance named %q", inst.Name)
}

// Rename cambia el nombre de una instancia preservando su posición en la
// lista. Falla con nombre nuevo vacío, duplicado o instancia inexistente.
func (s *Store) Rename(oldName, newName string) error {
	if newName == "" {
		return errors.New("the instance needs a name")
	}
	if newName != oldName {
		if _, exists := s.Get(newName); exists {
			return fmt.Errorf("an instance named %q already exists", newName)
		}
	}
	for i := range s.instances {
		if s.instances[i].Name == oldName {
			s.instances[i].Name = newName
			return nil
		}
	}
	return fmt.Errorf("no instance named %q", oldName)
}

// Remove elimina la instancia por nombre. Falla si no existe.
func (s *Store) Remove(name string) error {
	for i := range s.instances {
		if s.instances[i].Name == name {
			s.instances = append(s.instances[:i], s.instances[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("no instance named %q", name)
}
