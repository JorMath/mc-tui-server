package download

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

const (
	defaultVanillaBase = "https://piston-meta.mojang.com"
	defaultPaperBase   = "https://fill.papermc.io"
	defaultPurpurBase  = "https://api.purpurmc.org"
	defaultFabricBase  = "https://meta.fabricmc.net"
)

func base(configured, fallback string) string {
	if configured == "" {
		return fallback
	}
	return configured
}

// --- Vanilla (Mojang piston-meta) -----------------------------------------

type Vanilla struct {
	BaseURL string
	Client  *http.Client
}

func (v *Vanilla) Name() string { return "vanilla" }

type vanillaManifest struct {
	Versions []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"versions"`
}

func (v *Vanilla) manifest(ctx context.Context) (vanillaManifest, error) {
	var m vanillaManifest
	url := base(v.BaseURL, defaultVanillaBase) + "/mc/game/version_manifest_v2.json"
	if err := GetJSON(ctx, v.Client, url, &m); err != nil {
		return m, err
	}
	return m, nil
}

// Versions devuelve solo las versiones estables (releases), más nuevas primero.
func (v *Vanilla) Versions(ctx context.Context) ([]string, error) {
	m, err := v.manifest(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, ver := range m.Versions {
		if ver.Type == "release" {
			out = append(out, ver.ID)
		}
	}
	return out, nil
}

func (v *Vanilla) ResolveJarURL(ctx context.Context, version string) (string, error) {
	m, err := v.manifest(ctx)
	if err != nil {
		return "", err
	}
	for _, ver := range m.Versions {
		if ver.ID != version {
			continue
		}
		var detail struct {
			Downloads struct {
				Server struct {
					URL string `json:"url"`
				} `json:"server"`
			} `json:"downloads"`
		}
		if err := GetJSON(ctx, v.Client, ver.URL, &detail); err != nil {
			return "", err
		}
		if detail.Downloads.Server.URL == "" {
			return "", fmt.Errorf("version %s has no server jar", version)
		}
		return detail.Downloads.Server.URL, nil
	}
	return "", fmt.Errorf("version %q not found in the Mojang manifest", version)
}

// --- Paper (PaperMC fill v3) ---------------------------------------------

type Paper struct {
	BaseURL string
	Client  *http.Client
}

func (p *Paper) Name() string { return "paper" }

type paperDownload struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type paperBuild struct {
	ID        int                      `json:"id"`
	Channel   string                   `json:"channel"`
	Downloads map[string]paperDownload `json:"downloads"`
}

// showPaperVersion descarta los canales de prueba (rc/pre) para no ofrecer
// builds inestables en el asistente.
func showPaperVersion(version string) bool {
	return version != "" && !strings.Contains(version, "-")
}

// Versions devuelve las versiones estables soportadas, más nuevas primero
// (la API v3 ya las lista de nueva a vieja).
func (p *Paper) Versions(ctx context.Context) ([]string, error) {
	var project struct {
		Versions []struct {
			Version struct {
				ID string `json:"id"`
			} `json:"version"`
		} `json:"versions"`
	}
	url := base(p.BaseURL, defaultPaperBase) + "/v3/projects/paper/versions"
	if err := GetJSON(ctx, p.Client, url, &project); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(project.Versions))
	for _, v := range project.Versions {
		if showPaperVersion(v.Version.ID) {
			out = append(out, v.Version.ID)
		}
	}
	return out, nil
}

// paperBuilds consulta la lista de builds de una versión. La API responde
// de build más nuevo a más viejo.
func (p *Paper) paperBuilds(ctx context.Context, url string) ([]paperBuild, error) {
	var builds []paperBuild
	if err := GetJSON(ctx, p.Client, url, &builds); err != nil {
		return nil, err
	}
	return builds, nil
}

// ResolveJarURL devuelve la URL directa del jar de servidor del build estable
// más reciente. Si la versión no tiene ningún build estable (por ejemplo una
// rc), cae al build más nuevo disponible, sin importar el canal.
func (p *Paper) ResolveJarURL(ctx context.Context, version string) (string, error) {
	baseURL := base(p.BaseURL, defaultPaperBase)
	buildsURL := fmt.Sprintf("%s/v3/projects/paper/versions/%s/builds", baseURL, version)

	stableBuilds, err := p.paperBuilds(ctx, buildsURL+"?channel=STABLE")
	if err != nil {
		return "", err
	}
	candidates := make([]paperBuild, 0, len(stableBuilds))
	for _, b := range stableBuilds {
		if b.Channel == "STABLE" {
			candidates = append(candidates, b)
		}
	}
	if len(candidates) == 0 {
		// Solo hay builds beta/alpha: se usa el más nuevo disponible.
		candidates, err = p.paperBuilds(ctx, buildsURL)
		if err != nil {
			return "", err
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("Paper version %s has no builds", version)
	}
	chosen := candidates[0]
	for _, b := range candidates[1:] {
		if b.ID > chosen.ID {
			chosen = b
		}
	}
	if dl, ok := chosen.Downloads["server:default"]; ok && dl.URL != "" {
		return dl.URL, nil
	}
	// Defensivo: cualquier otra clave "server:*" sirve como jar del servidor.
	keys := make([]string, 0, len(chosen.Downloads))
	for key := range chosen.Downloads {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if strings.HasPrefix(key, "server:") && chosen.Downloads[key].URL != "" {
			return chosen.Downloads[key].URL, nil
		}
	}
	return "", fmt.Errorf("Paper build %d has no server download", chosen.ID)
}

// --- Purpur (PurpurMC v2) ----------------------------------------------------

type Purpur struct {
	BaseURL string
	Client  *http.Client
}

func (p *Purpur) Name() string { return "purpur" }

// Versions devuelve las versiones soportadas, más nuevas primero.
func (p *Purpur) Versions(ctx context.Context) ([]string, error) {
	var project struct {
		Versions []string `json:"versions"`
	}
	url := base(p.BaseURL, defaultPurpurBase) + "/v2/purpur"
	if err := GetJSON(ctx, p.Client, url, &project); err != nil {
		return nil, err
	}
	out := make([]string, len(project.Versions))
	for i, v := range project.Versions {
		out[len(out)-1-i] = v
	}
	return out, nil
}

func (p *Purpur) ResolveJarURL(ctx context.Context, version string) (string, error) {
	return fmt.Sprintf("%s/v2/purpur/%s/latest/download", base(p.BaseURL, defaultPurpurBase), version), nil
}

// --- Fabric (FabricMC meta) --------------------------------------------------

type Fabric struct {
	BaseURL string
	Client  *http.Client
}

func (f *Fabric) Name() string { return "fabric" }

type fabricEntry struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
}

func (f *Fabric) stableVersions(ctx context.Context, endpoint string) ([]string, error) {
	var entries []fabricEntry
	url := base(f.BaseURL, defaultFabricBase) + endpoint
	if err := GetJSON(ctx, f.Client, url, &entries); err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.Stable {
			out = append(out, e.Version)
		}
	}
	return out, nil
}

// Versions devuelve las versiones estables del juego, más nuevas primero.
func (f *Fabric) Versions(ctx context.Context) ([]string, error) {
	return f.stableVersions(ctx, "/v2/versions/game")
}

// ResolveJarURL arma la URL del server launcher de Fabric con el loader y
// el installer estables más recientes.
func (f *Fabric) ResolveJarURL(ctx context.Context, version string) (string, error) {
	loaders, err := f.stableVersions(ctx, "/v2/versions/loader")
	if err != nil {
		return "", err
	}
	if len(loaders) == 0 {
		return "", fmt.Errorf("no stable Fabric loader available")
	}
	return f.ServerJarURLFor(ctx, version, loaders[0])
}

// ServerJarURLFor arma la URL del server launcher para una versión exacta
// de juego y de loader (la que exige un modpack), con el installer estable
// más reciente.
func (f *Fabric) ServerJarURLFor(ctx context.Context, gameVersion, loaderVersion string) (string, error) {
	installers, err := f.stableVersions(ctx, "/v2/versions/installer")
	if err != nil {
		return "", err
	}
	if len(installers) == 0 {
		return "", fmt.Errorf("no stable Fabric installer available")
	}
	return fmt.Sprintf("%s/v2/versions/loader/%s/%s/%s/server/jar",
		base(f.BaseURL, defaultFabricBase), gameVersion, loaderVersion, installers[0]), nil
}
