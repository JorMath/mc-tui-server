// content.go: source-agnostic rows for the content panel. Modrinth and
// Hangar results are normalized into the same slice so the panel can merge
// them without parallel slices or index mapping.
package main

import (
	"sort"
	"strings"
	"unicode"

	"github.com/JorMath/mc-tui-server/internal/config"
	"github.com/JorMath/mc-tui-server/internal/hangar"
	"github.com/JorMath/mc-tui-server/internal/modrinth"
)

// Source identifiers used by contentItem.Source and the source filter.
const (
	sourceAll      = "all"
	sourceModrinth = "modrinth"
	sourceHangar   = "hangar"
)

// contentItem is one search result. Either ID (Modrinth) or Owner/Slug
// (Hangar) is set, depending on Source.
type contentItem struct {
	Source string
	ID     string
	// HangarID is the numeric Hangar project id, the canonical handle used
	// for dependencies. It is zero for Modrinth rows.
	HangarID    int
	Owner       string
	Slug        string
	Title       string
	Description string
	Downloads   int
	// AlsoOn is "hangar" when the same project was found on both sources and
	// the Modrinth row is the one kept.
	AlsoOn string
}

// contentVersion is one installable version of a contentItem.
type contentVersion struct {
	Source       string
	Label        string // Modrinth version_number or Hangar version name
	GameVersions []string
	GameRange    string        // Hangar formatted range, e.g. "1.8-26.3"
	Channel      string        // Modrinth version_type or Hangar channel name
	File         modrinth.File // Filename, URL and (Modrinth only) SHA1
	// SHA256 and ExternalURL are Hangar-only. Modrinth files have no
	// SHA-256 in the version endpoints this panel uses.
	SHA256      string
	ExternalURL string
	// Deps are Modrinth dependencies, installed alongside the project.
	Deps []modrinth.Dependency
	// HangarDeps are the plugin dependencies declared by a Hangar version.
	// They are kept next to the display names so a version picked from the
	// selector installs exactly the dependencies it declared.
	HangarDeps []hangar.Dependency
	// PluginDeps lists the required Hangar plugin dependencies by name,
	// for the version row.
	PluginDeps []string
}

// external reports whether a Hangar version is hosted outside Hangar and
// therefore cannot be installed as a jar.
func (v contentVersion) external() bool {
	return v.Source == sourceHangar && (v.ExternalURL != "" || v.File.URL == "")
}

func contentItemFromModrinth(p modrinth.Project) contentItem {
	return contentItem{
		Source:      sourceModrinth,
		ID:          p.ID,
		Title:       p.Title,
		Description: p.Description,
		Downloads:   p.Downloads,
	}
}

func contentItemFromHangar(p hangar.Project) contentItem {
	return contentItem{
		Source:      sourceHangar,
		HangarID:    p.ID,
		Owner:       p.Owner,
		Slug:        p.Slug,
		Title:       p.Name,
		Description: p.Description,
		Downloads:   p.Downloads,
	}
}

func contentVersionFromModrinth(v modrinth.ModVersion) contentVersion {
	return contentVersion{
		Source:       sourceModrinth,
		Label:        v.VersionNumber,
		GameVersions: v.GameVersions,
		Channel:      v.VersionType,
		File:         v.File,
		Deps:         v.Deps,
	}
}

func contentVersionFromHangar(v hangar.Version) contentVersion {
	return contentVersion{
		Source:       sourceHangar,
		Label:        v.Name,
		GameVersions: v.GameVersions,
		GameRange:    v.GameRange,
		Channel:      v.Channel,
		File:         modrinth.File{URL: v.DownloadURL, Filename: v.FileName},
		SHA256:       v.SHA256,
		ExternalURL:  v.ExternalURL,
		HangarDeps:   v.Dependencies,
		PluginDeps:   hangarDependencyNames(v.Dependencies),
	}
}

// hangarDependencyNames lists the dependency names shown on a version row.
// Required dependencies come first; externally hosted ones are marked
// because they cannot be installed from Hangar.
func hangarDependencyNames(deps []hangar.Dependency) []string {
	var hosted, external []string
	for _, d := range deps {
		if !d.Required || d.Name == "" {
			continue
		}
		if d.ExternalURL != "" {
			external = append(external, d.Name+" (external)")
			continue
		}
		hosted = append(hosted, d.Name)
	}
	return append(hosted, external...)
}

// toHangarVersion rebuilds the Hangar version needed to download a
// contentVersion created by contentVersionFromHangar.
func toHangarVersion(v contentVersion) hangar.Version {
	return hangar.Version{
		Name:         v.Label,
		Channel:      v.Channel,
		DownloadURL:  v.File.URL,
		FileName:     v.File.Filename,
		SHA256:       v.SHA256,
		ExternalURL:  v.ExternalURL,
		Dependencies: v.HangarDeps,
	}
}

// normalizeTitle lowercases a title and keeps only letters and digits, so
// "ViaVersion" and "via version" compare equal when deduplicating rows.
func normalizeTitle(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// mergeContentItems merges Hangar rows into the Modrinth rows. When a
// project exists on both sources the Modrinth row is kept and marked as
// AlsoOn "hangar". The merged list is sorted by downloads (desc) and then
// title; with no Hangar data the Modrinth order is preserved.
func mergeContentItems(mrItems, hgItems []contentItem) []contentItem {
	if len(hgItems) == 0 {
		return mrItems
	}
	merged := append([]contentItem(nil), mrItems...)
	byTitle := make(map[string]int, len(merged))
	for i, it := range merged {
		byTitle[normalizeTitle(it.Title)] = i
	}
	for _, h := range hgItems {
		key := normalizeTitle(h.Title)
		if key == "" {
			continue
		}
		if i, ok := byTitle[key]; ok {
			if merged[i].AlsoOn == "" {
				merged[i].AlsoOn = sourceHangar
			}
			continue
		}
		byTitle[key] = len(merged)
		merged = append(merged, h)
	}
	sortContentItems(merged)
	return merged
}

// sortContentItems orders rows by downloads descending, then title.
func sortContentItems(items []contentItem) {
	sort.SliceStable(items, func(i, j int) bool {
		return contentItemLess(items[i], items[j])
	})
}

func contentItemLess(a, b contentItem) bool {
	if a.Downloads != b.Downloads {
		return a.Downloads > b.Downloads
	}
	return strings.ToLower(a.Title) < strings.ToLower(b.Title)
}

// nextSource cycles All → Modrinth → Hangar → All. When Hangar does not
// apply the filter always falls back to All.
func nextSource(cur string, hangarOK bool) string {
	if !hangarOK {
		return sourceAll
	}
	switch cur {
	case sourceAll:
		return sourceModrinth
	case sourceModrinth:
		return sourceHangar
	default:
		return sourceAll
	}
}

// hangarAvailable reports whether Hangar can serve the current content kind
// on this server type.
func hangarAvailable(t config.ServerType, kind string) bool {
	_, ok := hangar.Platform(t)
	return ok && kind == "plugins"
}

// gameVersionsLabel prefers the formatted Hangar range when the raw version
// list would be too long for a row.
func gameVersionsLabel(v contentVersion) string {
	const maxListed = 4
	if v.GameRange != "" && len(v.GameVersions) > maxListed {
		return v.GameRange
	}
	return strings.Join(v.GameVersions, ", ")
}
