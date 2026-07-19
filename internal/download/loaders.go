// loaders.go: proveedores de Forge, NeoForge y Quilt. Estas distribuciones
// no publican un server.jar directo sino un installer que hay que ejecutar
// en la instancia, así que ResolveJarURL devuelve la URL del INSTALLER;
// internal/installer se encarga de correrlo y detectar el arranque.
package download

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

const (
	defaultForgePromos = "https://files.minecraftforge.net"
	defaultForgeMaven  = "https://maven.minecraftforge.net"
	defaultNeoMaven    = "https://maven.neoforged.net"
	defaultQuiltMeta   = "https://meta.quiltmc.org"
)

// ForgeInstallerURL arma la URL del installer oficial de Forge para la
// versión de Minecraft y de Forge dadas (baseURL vacío usa el maven oficial).
func ForgeInstallerURL(baseURL, mc, version string) string {
	if baseURL == "" {
		baseURL = defaultForgeMaven
	}
	full := mc + "-" + version
	return fmt.Sprintf("%s/net/minecraftforge/forge/%s/forge-%s-installer.jar", baseURL, full, full)
}

// NeoForgeInstallerURL arma la URL del installer oficial de NeoForge. Las
// versiones 47.x (MC 1.20.1) viven en el artefacto legacy net/neoforged/forge
// con prefijo de versión de Minecraft; las demás en net/neoforged/neoforge.
func NeoForgeInstallerURL(baseURL, mc, version string) string {
	if baseURL == "" {
		baseURL = defaultNeoMaven
	}
	if strings.HasPrefix(version, "47.") {
		full := mc + "-" + version
		return fmt.Sprintf("%s/releases/net/neoforged/forge/%s/forge-%s-installer.jar", baseURL, full, full)
	}
	return fmt.Sprintf("%s/releases/net/neoforged/neoforge/%s/neoforge-%s-installer.jar", baseURL, version, version)
}

// QuiltInstallerURL consulta el meta de Quilt y devuelve la URL del
// installer más reciente (sirve para cualquier versión del juego).
func QuiltInstallerURL(ctx context.Context, client *http.Client, baseURL string) (string, error) {
	if baseURL == "" {
		baseURL = defaultQuiltMeta
	}
	var installers []struct {
		URL string `json:"url"`
	}
	if err := GetJSON(ctx, client, baseURL+"/v3/versions/installer", &installers); err != nil {
		return "", err
	}
	if len(installers) == 0 || installers[0].URL == "" {
		return "", fmt.Errorf("the Quilt meta API returned no installers")
	}
	return installers[0].URL, nil
}

// compareVersions compara versiones por segmentos numéricos ("1.21.11" <
// "26.2"). Segmentos no numéricos comparan como texto.
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var av, bv string
		if i < len(as) {
			av = as[i]
		}
		if i < len(bs) {
			bv = bs[i]
		}
		an, aerr := strconv.Atoi(av)
		bn, berr := strconv.Atoi(bv)
		switch {
		case aerr == nil && berr == nil:
			if an != bn {
				return an - bn
			}
		default:
			if av != bv {
				return strings.Compare(av, bv)
			}
		}
	}
	return 0
}

// --- Forge (promotions de files.minecraftforge.net) ------------------------

type Forge struct {
	PromosURL string // base del JSON de promotions
	MavenURL  string // base del maven de installers
	Client    *http.Client
}

func (f *Forge) Name() string { return "forge" }

func (f *Forge) promos(ctx context.Context) (map[string]string, error) {
	var data struct {
		Promos map[string]string `json:"promos"`
	}
	url := base(f.PromosURL, defaultForgePromos) + "/net/minecraftforge/forge/promotions_slim.json"
	if err := GetJSON(ctx, f.Client, url, &data); err != nil {
		return nil, err
	}
	return data.Promos, nil
}

// Versions devuelve las versiones de Minecraft con Forge disponible, más
// nuevas primero. Las claves de promotions son "<mc>-latest"/"<mc>-recommended".
func (f *Forge) Versions(ctx context.Context) ([]string, error) {
	promos, err := f.promos(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for key := range promos {
		mc, _, ok := strings.Cut(key, "-")
		if !ok || seen[mc] {
			continue
		}
		seen[mc] = true
		out = append(out, mc)
	}
	sort.Slice(out, func(i, j int) bool { return compareVersions(out[i], out[j]) > 0 })
	return out, nil
}

// ResolveJarURL devuelve la URL del installer del build recomendado para
// esa versión de MC (o el último si no hay recomendado).
func (f *Forge) ResolveJarURL(ctx context.Context, version string) (string, error) {
	promos, err := f.promos(ctx)
	if err != nil {
		return "", err
	}
	build := promos[version+"-recommended"]
	if build == "" {
		build = promos[version+"-latest"]
	}
	if build == "" {
		return "", fmt.Errorf("Forge has no builds for Minecraft %s", version)
	}
	return ForgeInstallerURL(f.MavenURL, version, build), nil
}

// --- NeoForge (maven.neoforged.net) -----------------------------------------

type NeoForge struct {
	BaseURL string
	Client  *http.Client
}

func (n *NeoForge) Name() string { return "neoforge" }

func (n *NeoForge) versions(ctx context.Context) ([]string, error) {
	var data struct {
		Versions []string `json:"versions"`
	}
	url := base(n.BaseURL, defaultNeoMaven) + "/api/maven/versions/releases/net/neoforged/neoforge"
	if err := GetJSON(ctx, n.Client, url, &data); err != nil {
		return nil, err
	}
	return data.Versions, nil
}

// neoForgeMC deduce la versión de Minecraft de una versión de NeoForge:
// 47.x → 1.20.1 (era legacy), X.Y.Z → 1.X.Y (1.20.2 a 1.21.x, con Y=0 →
// 1.X), y el esquema nuevo X.Y.Z.W → X.Y. Devuelve "" si no la reconoce.
func neoForgeMC(version string) string {
	version = strings.TrimSuffix(version, "-beta")
	if strings.HasPrefix(version, "47.") {
		return "1.20.1"
	}
	parts := strings.Split(version, ".")
	for _, p := range parts {
		if _, err := strconv.Atoi(p); err != nil {
			return ""
		}
	}
	switch {
	case len(parts) >= 4:
		return parts[0] + "." + parts[1]
	case len(parts) == 3:
		if parts[1] == "0" {
			return "1." + parts[0]
		}
		return "1." + parts[0] + "." + parts[1]
	default:
		return ""
	}
}

// Versions devuelve las versiones de Minecraft con NeoForge disponible,
// más nuevas primero (el maven las lista de vieja a nueva).
func (n *NeoForge) Versions(ctx context.Context) ([]string, error) {
	versions, err := n.versions(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for i := len(versions) - 1; i >= 0; i-- {
		mc := neoForgeMC(versions[i])
		if mc == "" || seen[mc] {
			continue
		}
		seen[mc] = true
		out = append(out, mc)
	}
	return out, nil
}

// ResolveJarURL devuelve la URL del installer del último build de NeoForge
// para esa versión de MC, prefiriendo builds estables sobre betas.
func (n *NeoForge) ResolveJarURL(ctx context.Context, version string) (string, error) {
	versions, err := n.versions(ctx)
	if err != nil {
		return "", err
	}
	var latest, latestBeta string
	for _, v := range versions {
		if neoForgeMC(v) != version {
			continue
		}
		if strings.HasSuffix(v, "-beta") {
			latestBeta = v
		} else {
			latest = v
		}
	}
	if latest == "" {
		latest = latestBeta
	}
	if latest == "" {
		return "", fmt.Errorf("NeoForge has no builds for Minecraft %s", version)
	}
	return NeoForgeInstallerURL(n.BaseURL, version, latest), nil
}

// --- Quilt (meta.quiltmc.org) -------------------------------------------------

type Quilt struct {
	BaseURL string
	Client  *http.Client
}

func (q *Quilt) Name() string { return "quilt" }

// Versions devuelve las versiones estables del juego, más nuevas primero.
func (q *Quilt) Versions(ctx context.Context) ([]string, error) {
	var entries []struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
	}
	url := base(q.BaseURL, defaultQuiltMeta) + "/v3/versions/game"
	if err := GetJSON(ctx, q.Client, url, &entries); err != nil {
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

// ResolveJarURL devuelve la URL del installer de Quilt, que es el mismo
// para cualquier versión del juego (la versión se pasa al ejecutarlo).
func (q *Quilt) ResolveJarURL(ctx context.Context, version string) (string, error) {
	return QuiltInstallerURL(ctx, q.Client, q.BaseURL)
}
