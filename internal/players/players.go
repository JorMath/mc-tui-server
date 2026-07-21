// Package players gestiona las listas de jugadores del servidor:
// whitelist.json, ops.json y banned-players.json. Las entradas se
// manipulan como mapas crudos para preservar campos que no conocemos.
package players

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/JorMath/mc-tui-server/internal/download"
)

// Archivos de listas en la raíz de la instancia.
const (
	WhitelistFile = "whitelist.json"
	OpsFile       = "ops.json"
	BansFile      = "banned-players.json"
)

// Entry es una entrada cruda de cualquiera de las listas.
type Entry map[string]any

// Load lee una lista; si el archivo no existe devuelve lista vacía (el
// server los crea en el primer arranque).
func Load(path string) ([]Entry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return entries, nil
}

// Save escribe la lista con el formato que usa el servidor.
func Save(path string, entries []Entry) error {
	if entries == nil {
		entries = []Entry{}
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// Names devuelve los nombres de la lista, en su orden.
func Names(entries []Entry) []string {
	var out []string
	for _, e := range entries {
		if n, ok := e["name"].(string); ok {
			out = append(out, n)
		}
	}
	return out
}

// Has indica si la lista contiene el nombre (sin distinguir mayúsculas).
func Has(entries []Entry, name string) bool {
	for _, e := range entries {
		if n, ok := e["name"].(string); ok && strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

// Remove quita el nombre de la lista (sin distinguir mayúsculas).
func Remove(entries []Entry, name string) ([]Entry, bool) {
	for i, e := range entries {
		if n, ok := e["name"].(string); ok && strings.EqualFold(n, name) {
			return append(entries[:i], entries[i+1:]...), true
		}
	}
	return entries, false
}

// Whitelist arma la entrada de whitelist.json.
func Whitelist(uuid, name string) Entry {
	return Entry{"uuid": uuid, "name": name}
}

// Op arma la entrada de ops.json (nivel 4 = todos los permisos).
func Op(uuid, name string) Entry {
	return Entry{"uuid": uuid, "name": name, "level": 4, "bypassesPlayerLimit": false}
}

// Ban arma la entrada de banned-players.json con el formato del vanilla.
func Ban(uuid, name string, now time.Time) Entry {
	return Entry{
		"uuid":    uuid,
		"name":    name,
		"created": now.Format("2006-01-02 15:04:05 -0700"),
		"source":  "mc-tui-server",
		"expires": "forever",
		"reason":  "Banned by an operator.",
	}
}

// dashes formatea un UUID hex de 32 caracteres con guiones 8-4-4-4-12.
func dashes(hex string) string {
	if len(hex) != 32 {
		return hex
	}
	return hex[0:8] + "-" + hex[8:12] + "-" + hex[12:16] + "-" + hex[16:20] + "-" + hex[20:32]
}

const defaultMojangBase = "https://api.mojang.com"

// MojangUUID resuelve el UUID oficial de un nombre (servers online-mode).
func MojangUUID(ctx context.Context, client *http.Client, baseURL, name string) (string, error) {
	if baseURL == "" {
		baseURL = defaultMojangBase
	}
	var resp struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	url := baseURL + "/users/profiles/minecraft/" + name
	if err := download.GetJSON(ctx, client, url, &resp); err != nil {
		return "", fmt.Errorf("looking up %q on Mojang (does the player exist?): %w", name, err)
	}
	if resp.ID == "" {
		return "", fmt.Errorf("player %q not found on Mojang", name)
	}
	return dashes(resp.ID), nil
}

// OfflineUUID calcula el UUID que usan los servers con online-mode=false:
// un UUID v3 (md5) de "OfflinePlayer:<nombre>".
func OfflineUUID(name string) string {
	h := md5.Sum([]byte("OfflinePlayer:" + name))
	h[6] = (h[6] & 0x0f) | 0x30 // versión 3
	h[8] = (h[8] & 0x3f) | 0x80 // variante RFC 4122
	return dashes(fmt.Sprintf("%x", h))
}
