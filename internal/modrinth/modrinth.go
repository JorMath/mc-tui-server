// Package modrinth busca e instala plugins/mods desde la API de Modrinth
// (R6), filtrando por loader y versión del juego según la instancia.
package modrinth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/JorMath/mc-tui-server/internal/config"
	"github.com/JorMath/mc-tui-server/internal/download"
)

const defaultBase = "https://api.modrinth.com"

// Project es un resultado de búsqueda. Categories incluye los loaders del
// proyecto (fabric, forge, neoforge, quilt) entre otras etiquetas.
type Project struct {
	ID          string   `json:"project_id"`
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Downloads   int      `json:"downloads"`
	Categories  []string `json:"categories"`
}

// File es el archivo descargable de la versión elegida de un proyecto.
// SHA1 solo se llena en las consultas por hash (LatestByHash).
type File struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
	SHA1     string `json:"-"`
}

// Client habla con la API de Modrinth. BaseURL vacío usa la oficial.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func (c *Client) base() string {
	if c.BaseURL == "" {
		return defaultBase
	}
	return c.BaseURL
}

// loadersFor mapea el tipo de servidor a los loaders de Modrinth y al
// project_type a buscar. Los servidores Paper/Purpur cargan plugins de
// toda la familia Bukkit; Quilt carga también mods de Fabric.
func loadersFor(t config.ServerType) ([]string, string, error) {
	switch t {
	case config.Paper:
		return []string{"paper", "spigot", "bukkit"}, "plugin", nil
	case config.Purpur:
		return []string{"purpur", "paper", "spigot", "bukkit"}, "plugin", nil
	case config.Fabric:
		return []string{"fabric"}, "mod", nil
	case config.Forge:
		return []string{"forge"}, "mod", nil
	case config.NeoForge:
		return []string{"neoforge"}, "mod", nil
	case config.Quilt:
		return []string{"quilt", "fabric"}, "mod", nil
	default:
		return nil, "", fmt.Errorf("server type %q does not support plugins/mods", t)
	}
}

// jsonList serializa una lista de strings como la espera Modrinth: ["a","b"].
func jsonList(items []string) string {
	b, _ := json.Marshal(items)
	return string(b)
}

// search ejecuta /v2/search con los facets dados (AND entre grupos, OR
// dentro de cada grupo).
func (c *Client) search(ctx context.Context, query string, facets [][]string) ([]Project, error) {
	facetsJSON, _ := json.Marshal(facets)

	params := url.Values{}
	params.Set("query", query)
	params.Set("limit", "20")
	params.Set("facets", string(facetsJSON))

	var result struct {
		Hits []Project `json:"hits"`
	}
	endpoint := c.base() + "/v2/search?" + params.Encode()
	if err := download.GetJSON(ctx, c.HTTP, endpoint, &result); err != nil {
		return nil, err
	}
	return result.Hits, nil
}

// Search busca proyectos compatibles con el tipo y la versión de la instancia.
func (c *Client) Search(ctx context.Context, query string, t config.ServerType, gameVersion string) ([]Project, error) {
	loaders, projectType, err := loadersFor(t)
	if err != nil {
		return nil, err
	}
	categories := make([]string, len(loaders))
	for i, l := range loaders {
		categories[i] = "categories:" + l
	}
	return c.search(ctx, query, [][]string{
		{"project_type:" + projectType},
		{"versions:" + gameVersion},
		categories,
	})
}

// SearchDatapacks busca datapacks compatibles con la versión del juego.
// Los datapacks van al mundo, no al loader, así que sirven para cualquier
// tipo de servidor.
func (c *Client) SearchDatapacks(ctx context.Context, query, gameVersion string) ([]Project, error) {
	return c.search(ctx, query, [][]string{
		{"project_type:datapack"},
		{"versions:" + gameVersion},
	})
}

// SearchModpacks busca modpacks instalables en un servidor (server_side
// required u optional) de cualquier loader soportado: Fabric, Forge,
// NeoForge o Quilt.
func (c *Client) SearchModpacks(ctx context.Context, query string) ([]Project, error) {
	return c.search(ctx, query, [][]string{
		{"project_type:modpack"},
		{"categories:fabric", "categories:forge", "categories:neoforge", "categories:quilt"},
		{"server_side:required", "server_side:optional"},
	})
}

// PackVersion es una versión instalable de un modpack con su .mrpack.
type PackVersion struct {
	ID            string
	Name          string
	VersionNumber string
	VersionType   string
	GameVersions  []string
	Loaders       []string
	URL           string
	Filename      string
}

// ModpackVersions lista las versiones de un modpack, más recientes primero
// (orden de la API), con el archivo .mrpack y los loaders de cada una.
func (c *Client) ModpackVersions(ctx context.Context, projectID string) ([]PackVersion, error) {
	var versions []struct {
		ID            string   `json:"id"`
		Name          string   `json:"name"`
		VersionNumber string   `json:"version_number"`
		VersionType   string   `json:"version_type"`
		GameVersions  []string `json:"game_versions"`
		Loaders       []string `json:"loaders"`
		Files         []struct {
			URL      string `json:"url"`
			Filename string `json:"filename"`
			Primary  bool   `json:"primary"`
		} `json:"files"`
	}
	endpoint := fmt.Sprintf("%s/v2/project/%s/version", c.base(), projectID)
	if err := download.GetJSON(ctx, c.HTTP, endpoint, &versions); err != nil {
		return nil, err
	}
	var out []PackVersion
	for _, v := range versions {
		if len(v.Files) == 0 {
			continue
		}
		chosen := v.Files[0]
		for _, f := range v.Files {
			if f.Primary {
				chosen = f
				break
			}
		}
		out = append(out, PackVersion{
			ID:            v.ID,
			Name:          v.Name,
			VersionNumber: v.VersionNumber,
			VersionType:   v.VersionType,
			GameVersions:  v.GameVersions,
			Loaders:       v.Loaders,
			URL:           chosen.URL,
			Filename:      chosen.Filename,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("modpack %s has no versions with files", projectID)
	}
	return out, nil
}

// latestFile devuelve el archivo primario de la versión más reciente del
// proyecto que sea compatible con los loaders y la versión del juego.
// compat describe la combinación para los mensajes de error.
func (c *Client) latestFile(ctx context.Context, projectID string, loaders []string, gameVersion, compat string) (File, error) {
	params := url.Values{}
	params.Set("loaders", jsonList(loaders))
	params.Set("game_versions", jsonList([]string{gameVersion}))

	var versions []struct {
		Files []struct {
			URL      string `json:"url"`
			Filename string `json:"filename"`
			Primary  bool   `json:"primary"`
		} `json:"files"`
	}
	endpoint := fmt.Sprintf("%s/v2/project/%s/version?%s", c.base(), projectID, params.Encode())
	if err := download.GetJSON(ctx, c.HTTP, endpoint, &versions); err != nil {
		return File{}, err
	}
	if len(versions) == 0 {
		return File{}, fmt.Errorf("no version of %s is compatible with %s", projectID, compat)
	}
	files := versions[0].Files
	if len(files) == 0 {
		return File{}, fmt.Errorf("the latest version of %s has no files", projectID)
	}
	chosen := files[0]
	for _, f := range files {
		if f.Primary {
			chosen = f
			break
		}
	}
	return File{URL: chosen.URL, Filename: chosen.Filename}, nil
}

// LatestByHash consulta, para cada sha1 de un archivo local, la última
// versión del proyecto compatible con el loader y la versión del juego
// (POST /v2/version_files/update). Devuelve hash local → archivo primario
// de esa última versión; si el sha1 del resultado coincide con la clave,
// el archivo ya está al día. Hashes desconocidos no aparecen en el mapa.
func (c *Client) LatestByHash(ctx context.Context, t config.ServerType, gameVersion string, hashes []string) (map[string]File, error) {
	loaders, _, err := loadersFor(t)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"hashes":        hashes,
		"algorithm":     "sha1",
		"loaders":       loaders,
		"game_versions": []string{gameVersion},
	}
	var resp map[string]struct {
		Files []struct {
			URL      string `json:"url"`
			Filename string `json:"filename"`
			Primary  bool   `json:"primary"`
			Hashes   struct {
				SHA1 string `json:"sha1"`
			} `json:"hashes"`
		} `json:"files"`
	}
	endpoint := c.base() + "/v2/version_files/update"
	if err := download.PostJSON(ctx, c.HTTP, endpoint, body, &resp); err != nil {
		return nil, err
	}
	out := map[string]File{}
	for hash, version := range resp {
		if len(version.Files) == 0 {
			continue
		}
		chosen := version.Files[0]
		for _, f := range version.Files {
			if f.Primary {
				chosen = f
				break
			}
		}
		out[hash] = File{URL: chosen.URL, Filename: chosen.Filename, SHA1: chosen.Hashes.SHA1}
	}
	return out, nil
}

// ServerUnsupported consulta en lote los proyectos dados y devuelve el
// conjunto de IDs cuya ficha en Modrinth dice server_side "unsupported".
// Muchos modpacks marcan mods solo-cliente como requeridos en el servidor
// en su índice; la ficha del proyecto es la fuente de verdad.
func (c *Client) ServerUnsupported(ctx context.Context, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	// Lotes de 200 para no exceder el largo máximo de URL.
	for start := 0; start < len(ids); start += 200 {
		end := min(start+200, len(ids))
		params := url.Values{}
		params.Set("ids", jsonList(ids[start:end]))

		var projects []struct {
			ID         string `json:"id"`
			ServerSide string `json:"server_side"`
		}
		endpoint := c.base() + "/v2/projects?" + params.Encode()
		if err := download.GetJSON(ctx, c.HTTP, endpoint, &projects); err != nil {
			return nil, err
		}
		for _, p := range projects {
			if p.ServerSide == "unsupported" {
				out[p.ID] = true
			}
		}
	}
	return out, nil
}

// LatestFile devuelve el archivo de la versión más reciente del proyecto
// compatible con el loader y la versión del juego.
func (c *Client) LatestFile(ctx context.Context, projectID string, t config.ServerType, gameVersion string) (File, error) {
	loaders, _, err := loadersFor(t)
	if err != nil {
		return File{}, err
	}
	return c.latestFile(ctx, projectID, loaders, gameVersion, fmt.Sprintf("%s %s", t, gameVersion))
}

// LatestDatapackFile devuelve el zip de datapack más reciente del proyecto
// compatible con la versión del juego.
func (c *Client) LatestDatapackFile(ctx context.Context, projectID, gameVersion string) (File, error) {
	return c.latestFile(ctx, projectID, []string{"datapack"}, gameVersion, "datapacks for "+gameVersion)
}
