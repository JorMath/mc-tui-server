package download

import (
	"context"
	"fmt"
	"net/http"
)

const (
	defaultVanillaBase = "https://piston-meta.mojang.com"
	defaultPaperBase   = "https://api.papermc.io"
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

// --- Paper (PaperMC v2) ----------------------------------------------------

type Paper struct {
	BaseURL string
	Client  *http.Client
}

func (p *Paper) Name() string { return "paper" }

// Versions devuelve las versiones soportadas, más nuevas primero
// (la API las lista de vieja a nueva).
func (p *Paper) Versions(ctx context.Context) ([]string, error) {
	var project struct {
		Versions []string `json:"versions"`
	}
	url := base(p.BaseURL, defaultPaperBase) + "/v2/projects/paper"
	if err := GetJSON(ctx, p.Client, url, &project); err != nil {
		return nil, err
	}
	out := make([]string, len(project.Versions))
	for i, v := range project.Versions {
		out[len(out)-1-i] = v
	}
	return out, nil
}

func (p *Paper) ResolveJarURL(ctx context.Context, version string) (string, error) {
	var builds struct {
		Builds []struct {
			Build     int    `json:"build"`
			Channel   string `json:"channel"`
			Downloads struct {
				Application struct {
					Name string `json:"name"`
				} `json:"application"`
			} `json:"downloads"`
		} `json:"builds"`
	}
	baseURL := base(p.BaseURL, defaultPaperBase)
	url := fmt.Sprintf("%s/v2/projects/paper/versions/%s/builds", baseURL, version)
	if err := GetJSON(ctx, p.Client, url, &builds); err != nil {
		return "", err
	}
	if len(builds.Builds) == 0 {
		return "", fmt.Errorf("Paper version %s has no builds", version)
	}
	// Preferir el build estable (canal "default") más reciente; si no hay,
	// usar el último build disponible.
	chosen := builds.Builds[len(builds.Builds)-1]
	for i := len(builds.Builds) - 1; i >= 0; i-- {
		if builds.Builds[i].Channel == "default" {
			chosen = builds.Builds[i]
			break
		}
	}
	return fmt.Sprintf("%s/v2/projects/paper/versions/%s/builds/%d/downloads/%s",
		baseURL, version, chosen.Build, chosen.Downloads.Application.Name), nil
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
