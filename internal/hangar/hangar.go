// Package hangar searches for plugins on Hangar (hangar.papermc.io) and
// downloads their jars for Paper and Purpur instances. It is the second
// content source of the Modrinth panel.
package hangar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/JorMath/mc-tui-server/internal/config"
)

const (
	defaultBase = "https://hangar.papermc.io"
	// paperPlatform is the Hangar platform shared by Paper and Purpur.
	paperPlatform = "PAPER"
	// pageSize keeps the panel's first page small; both endpoints support
	// offset-based paging if it is ever needed.
	pageSize = 25
)

// Project is a search result or a project resolved by file hash.
type Project struct {
	ID          int
	Name        string
	Owner       string
	Slug        string
	Description string
	Downloads   int
}

// Dependency is one required or optional plugin dependency declared by a
// version, identified by its Hangar project id.
type Dependency struct {
	ProjectID   int
	Name        string
	Required    bool
	ExternalURL string
}

// Version is a concrete plugin version as published on Hangar.
type Version struct {
	Name         string
	Channel      string
	DownloadURL  string
	FileName     string
	SHA256       string
	SizeBytes    int64
	ExternalURL  string
	GameVersions []string // platformDependencies.PAPER
	GameRange    string   // platformDependenciesFormatted.PAPER[0], may be empty
	// Dependencies holds every plugin dependency declared for the requested
	// platform. Required and optional entries are both kept: the UI reports
	// them and the installer follows the required ones.
	Dependencies []Dependency
}

// External reports whether Hangar hosts the file itself. Externally hosted
// projects expose a page URL instead of a download URL and must never be
// fetched as a jar.
func (v Version) External() bool {
	return v.ExternalURL != "" || v.DownloadURL == ""
}

// Client talks to the Hangar API. An empty BaseURL uses the official host.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func (c *Client) base() string {
	if c.BaseURL == "" {
		return defaultBase
	}
	return strings.TrimSuffix(c.BaseURL, "/")
}

func clientOr(c *http.Client) *http.Client {
	if c == nil {
		return http.DefaultClient
	}
	return c
}

// errorResponse is Hangar's error envelope. Some failures come back with an
// empty body, so decoding it is best effort.
type errorResponse struct {
	Message string `json:"message"`
	Error   string `json:"error"`
}

func (e errorResponse) text() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Error
}

// errorSuffix decodes the response body into errorResponse and returns it
// prefixed with ": ", or an empty string when there is nothing to report.
func errorSuffix(resp *http.Response) string {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return ""
	}
	var er errorResponse
	if json.Unmarshal(body, &er) != nil {
		return ""
	}
	if msg := er.text(); msg != "" {
		return ": " + msg
	}
	return ""
}

// getJSON performs a GET and decodes the JSON body into v. It also returns
// the status code so callers can treat 404 as a normal answer.
func (c *Client) getJSON(ctx context.Context, endpoint string, v any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, fmt.Errorf("creating request to %s: %w", endpoint, err)
	}
	resp, err := clientOr(c.HTTP).Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetching %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("GET %s: HTTP %d%s", endpoint, resp.StatusCode, errorSuffix(resp))
	}
	if v != nil {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			return resp.StatusCode, fmt.Errorf("parsing response from %s: %w", endpoint, err)
		}
	}
	return resp.StatusCode, nil
}

// projectJSON mirrors the project object returned by search and by the hash
// lookup.
type projectJSON struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Namespace struct {
		Owner string `json:"owner"`
		Slug  string `json:"slug"`
	} `json:"namespace"`
	Description string `json:"description"`
	Stats       struct {
		Downloads int `json:"downloads"`
	} `json:"stats"`
}

func (p projectJSON) project() Project {
	return Project{
		ID:          p.ID,
		Name:        p.Name,
		Owner:       p.Namespace.Owner,
		Slug:        p.Namespace.Slug,
		Description: p.Description,
		Downloads:   p.Stats.Downloads,
	}
}

// Search lists projects matching query on the given platform. Results come
// back in relevance order.
func (c *Client) Search(ctx context.Context, query, platform string) ([]Project, error) {
	params := url.Values{}
	params.Set("query", query)
	if platform != "" {
		params.Set("platform", platform)
	}
	params.Set("limit", strconv.Itoa(pageSize))
	params.Set("offset", "0")

	var resp struct {
		Result []projectJSON `json:"result"`
	}
	endpoint := c.base() + "/api/v1/projects?" + params.Encode()
	if _, err := c.getJSON(ctx, endpoint, &resp); err != nil {
		return nil, err
	}
	out := make([]Project, len(resp.Result))
	for i, p := range resp.Result {
		out[i] = p.project()
	}
	return out, nil
}

// versionJSON mirrors one entry of the versions endpoint.
type versionJSON struct {
	Name    string `json:"name"`
	Channel struct {
		Name string `json:"name"`
	} `json:"channel"`
	Downloads map[string]struct {
		FileInfo struct {
			Name      string `json:"name"`
			SizeBytes int64  `json:"sizeBytes"`
			SHA256    string `json:"sha256Hash"`
		} `json:"fileInfo"`
		ExternalURL string `json:"externalUrl"`
		DownloadURL string `json:"downloadUrl"`
	} `json:"downloads"`
	PluginDependencies map[string][]struct {
		Name        string `json:"name"`
		ProjectID   int    `json:"projectId"`
		Required    bool   `json:"required"`
		ExternalURL string `json:"externalUrl"`
	} `json:"pluginDependencies"`
	PlatformDependencies map[string][]string `json:"platformDependencies"`
	PlatformFormatted    map[string][]string `json:"platformDependenciesFormatted"`
}

func (v versionJSON) version(platform string) Version {
	if platform == "" {
		platform = paperPlatform
	}
	file := v.Downloads[platform]
	deps := make([]Dependency, 0, len(v.PluginDependencies[platform]))
	for _, dep := range v.PluginDependencies[platform] {
		deps = append(deps, Dependency{
			ProjectID:   dep.ProjectID,
			Name:        dep.Name,
			Required:    dep.Required,
			ExternalURL: dep.ExternalURL,
		})
	}
	gameRange := ""
	if list := v.PlatformFormatted[platform]; len(list) > 0 {
		gameRange = list[0]
	}
	return Version{
		Name:         v.Name,
		Channel:      v.Channel.Name,
		DownloadURL:  file.DownloadURL,
		FileName:     file.FileInfo.Name,
		SHA256:       file.FileInfo.SHA256,
		SizeBytes:    file.FileInfo.SizeBytes,
		ExternalURL:  file.ExternalURL,
		GameVersions: v.PlatformDependencies[platform],
		GameRange:    gameRange,
		Dependencies: deps,
	}
}

// versionParams builds the query shared by the versions endpoints.
// empty filters are omitted; limit and offset are always sent.
func versionParams(platform, platformVersion, channel string, limit int) url.Values {
	params := url.Values{}
	if platform != "" {
		params.Set("platform", platform)
	}
	if platformVersion != "" {
		params.Set("platformVersion", platformVersion)
	}
	if channel != "" {
		params.Set("channel", channel)
	}
	params.Set("limit", strconv.Itoa(limit))
	params.Set("offset", "0")
	return params
}

// Versions lists the versions of a project on the given platform, newest
// first. platformVersion and channel narrow the query server-side and are
// omitted when empty.
func (c *Client) Versions(ctx context.Context, owner, slug, platform, platformVersion, channel string) ([]Version, error) {
	if owner == "" || slug == "" {
		return nil, fmt.Errorf("hangar: project owner and slug are required")
	}
	params := versionParams(platform, platformVersion, channel, pageSize)

	var resp struct {
		Result []versionJSON `json:"result"`
	}
	endpoint := fmt.Sprintf("%s/api/v1/projects/%s/%s/versions?%s",
		c.base(), url.PathEscape(owner), url.PathEscape(slug), params.Encode())
	if _, err := c.getJSON(ctx, endpoint, &resp); err != nil {
		return nil, err
	}
	out := make([]Version, 0, len(resp.Result))
	for _, v := range resp.Result {
		out = append(out, v.version(platform))
	}
	return out, nil
}

// ProjectByID fetches a project by its numeric Hangar id.
func (c *Client) ProjectByID(ctx context.Context, id int) (Project, error) {
	if id <= 0 {
		return Project{}, fmt.Errorf("hangar: project id is required")
	}
	var p projectJSON
	endpoint := fmt.Sprintf("%s/api/v1/projects/%d", c.base(), id)
	if _, err := c.getJSON(ctx, endpoint, &p); err != nil {
		return Project{}, err
	}
	return p.project(), nil
}

// LatestVersionByID returns the newest version of a project for the given
// platform, Minecraft version and channel. found is false when the project
// publishes no such version.
func (c *Client) LatestVersionByID(ctx context.Context, id int, platform, platformVersion, channel string) (Version, bool, error) {
	if id <= 0 {
		return Version{}, false, fmt.Errorf("hangar: project id is required")
	}
	params := versionParams(platform, platformVersion, channel, 1)
	var resp struct {
		Result []versionJSON `json:"result"`
	}
	endpoint := fmt.Sprintf("%s/api/v1/projects/%d/versions?%s", c.base(), id, params.Encode())
	if _, err := c.getJSON(ctx, endpoint, &resp); err != nil {
		return Version{}, false, err
	}
	if len(resp.Result) == 0 {
		return Version{}, false, nil
	}
	return resp.Result[0].version(platform), true, nil
}

// ByHash returns the project that published a file with the given SHA-256.
// found is false (and err nil) when Hangar does not know the hash.
func (c *Client) ByHash(ctx context.Context, sha256sum string) (Project, bool, error) {
	var p projectJSON
	endpoint := c.base() + "/api/v1/versions/hash/" + url.PathEscape(sha256sum)
	status, err := c.getJSON(ctx, endpoint, &p)
	if status == http.StatusNotFound {
		return Project{}, false, nil
	}
	if err != nil {
		return Project{}, false, err
	}
	return p.project(), true, nil
}

// Download streams v to dest, verifying the SHA-256 published by Hangar. The
// bytes are written to a temporary ".part" file first so a failed or corrupt
// download never leaves a truncated jar under its final name.
func (c *Client) Download(ctx context.Context, v Version, dest string) error {
	if v.External() {
		target := v.ExternalURL
		if target == "" {
			target = v.DownloadURL
		}
		return fmt.Errorf("version %s is hosted externally (%s)", v.Name, target)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.DownloadURL, nil)
	if err != nil {
		return fmt.Errorf("creating request to %s: %w", v.DownloadURL, err)
	}
	resp, err := clientOr(c.HTTP).Do(req)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", v.DownloadURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", v.DownloadURL, resp.StatusCode)
	}

	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating directory for %s: %w", dest, err)
	}
	part := dest + ".part"
	f, err := os.Create(part)
	if err != nil {
		return fmt.Errorf("creating %s: %w", part, err)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		f.Close()
		_ = os.Remove(part)
		return fmt.Errorf("writing %s: %w", part, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(part)
		return fmt.Errorf("closing %s: %w", part, err)
	}

	actual := hex.EncodeToString(h.Sum(nil))
	if v.SHA256 != "" && !strings.EqualFold(actual, v.SHA256) {
		_ = os.Remove(part)
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", v.FileName, strings.ToLower(v.SHA256), actual)
	}
	if err := os.Rename(part, dest); err != nil {
		_ = os.Remove(part)
		return fmt.Errorf("saving %s: %w", dest, err)
	}
	return nil
}

// Platform maps a server type to the Hangar platform. Only Paper and Purpur
// run Hangar plugins.
func Platform(t config.ServerType) (string, bool) {
	switch t {
	case config.Paper, config.Purpur:
		return paperPlatform, true
	default:
		return "", false
	}
}
