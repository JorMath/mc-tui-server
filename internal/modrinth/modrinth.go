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

// Project es un resultado de búsqueda.
type Project struct {
	ID          string `json:"project_id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Downloads   int    `json:"downloads"`
}

// File es el archivo descargable de la versión elegida de un proyecto.
type File struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
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
// toda la familia Bukkit.
func loadersFor(t config.ServerType) ([]string, string, error) {
	switch t {
	case config.Paper:
		return []string{"paper", "spigot", "bukkit"}, "plugin", nil
	case config.Purpur:
		return []string{"purpur", "paper", "spigot", "bukkit"}, "plugin", nil
	case config.Fabric:
		return []string{"fabric"}, "mod", nil
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

// SearchModpacks busca modpacks de Fabric instalables en un servidor
// (server_side required u optional). Los packs de Forge/NeoForge quedan
// fuera: el instalador solo soporta el server launcher de Fabric.
func (c *Client) SearchModpacks(ctx context.Context, query string) ([]Project, error) {
	return c.search(ctx, query, [][]string{
		{"project_type:modpack"},
		{"categories:fabric"},
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
	URL           string
	Filename      string
}

// ModpackVersions lista las versiones Fabric de un modpack, más recientes
// primero (orden de la API), con el archivo .mrpack de cada una.
func (c *Client) ModpackVersions(ctx context.Context, projectID string) ([]PackVersion, error) {
	params := url.Values{}
	params.Set("loaders", jsonList([]string{"fabric"}))

	var versions []struct {
		ID            string   `json:"id"`
		Name          string   `json:"name"`
		VersionNumber string   `json:"version_number"`
		VersionType   string   `json:"version_type"`
		GameVersions  []string `json:"game_versions"`
		Files         []struct {
			URL      string `json:"url"`
			Filename string `json:"filename"`
			Primary  bool   `json:"primary"`
		} `json:"files"`
	}
	endpoint := fmt.Sprintf("%s/v2/project/%s/version?%s", c.base(), projectID, params.Encode())
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
			URL:           chosen.URL,
			Filename:      chosen.Filename,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("modpack %s has no Fabric versions with files", projectID)
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
