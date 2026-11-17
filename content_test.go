package main

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JorMath/mc-tui-server/internal/config"
	"github.com/JorMath/mc-tui-server/internal/hangar"
	"github.com/JorMath/mc-tui-server/internal/modrinth"
)

func TestMergeContentItems(t *testing.T) {
	mr := []contentItem{
		{Source: sourceModrinth, Title: "ViaVersion", Downloads: 100},
		{Source: sourceModrinth, Title: "Other", Downloads: 500},
	}
	hg := []contentItem{
		{Source: sourceHangar, Title: "via version", Downloads: 900},
		{Source: sourceHangar, Title: "Hangar Only", Downloads: 1000},
	}

	got := mergeContentItems(mr, hg)
	if len(got) != 3 {
		t.Fatalf("merged rows = %d, want 3 (dedupe by normalized title)", len(got))
	}
	wantOrder := []string{"Hangar Only", "Other", "ViaVersion"}
	for i, want := range wantOrder {
		if got[i].Title != want {
			t.Errorf("row %d = %q, want %q", i, got[i].Title, want)
		}
	}
	// The Modrinth row survives the dedupe and records the second source.
	if got[2].Source != sourceModrinth || got[2].AlsoOn != sourceHangar {
		t.Fatalf("duplicate row = %+v, want Modrinth row with AlsoOn Hangar", got[2])
	}
	if got[0].AlsoOn != "" {
		t.Errorf("Hangar-only row must not carry AlsoOn: %+v", got[0])
	}
}

func TestMergeContentItemsWithoutHangarKeepsOrder(t *testing.T) {
	mr := []contentItem{
		{Source: sourceModrinth, Title: "B", Downloads: 1},
		{Source: sourceModrinth, Title: "A", Downloads: 2},
	}
	got := mergeContentItems(mr, nil)
	if len(got) != 2 || got[0].Title != "B" || got[1].Title != "A" {
		t.Fatalf("merged = %+v, want original Modrinth order", got)
	}
}

func TestNextSource(t *testing.T) {
	cases := []struct {
		name     string
		cur      string
		hangarOK bool
		want     string
	}{
		{"all to modrinth", sourceAll, true, sourceModrinth},
		{"modrinth to hangar", sourceModrinth, true, sourceHangar},
		{"hangar to all", sourceHangar, true, sourceAll},
		{"unknown falls back to all", "bogus", true, sourceAll},
		{"gated without hangar", sourceAll, false, sourceAll},
		{"hangar filter gated off", sourceHangar, false, sourceAll},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextSource(tc.cur, tc.hangarOK); got != tc.want {
				t.Fatalf("nextSource(%q, %v) = %q, want %q", tc.cur, tc.hangarOK, got, tc.want)
			}
		})
	}
}

func TestHangarAvailable(t *testing.T) {
	cases := []struct {
		name string
		kind string
		typ  config.ServerType
		want bool
	}{
		{"paper plugins", "plugins", config.Paper, true},
		{"purpur plugins", "plugins", config.Purpur, true},
		{"paper mods", "mods", config.Paper, false},
		{"paper datapacks", "datapacks", config.Paper, false},
		{"fabric plugins", "plugins", config.Fabric, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hangarAvailable(tc.typ, tc.kind); got != tc.want {
				t.Fatalf("hangarAvailable(%v, %q) = %v, want %v", tc.typ, tc.kind, got, tc.want)
			}
		})
	}
}

func TestContentVersionFromHangar(t *testing.T) {
	hv := hangar.Version{
		Name:         "2.1.0",
		Channel:      "Release",
		DownloadURL:  "https://example.invalid/plugin.jar",
		FileName:     "plugin-2.1.0.jar",
		SHA256:       "ABCDEF",
		GameVersions: []string{"1.20", "1.21", "1.22", "1.23", "1.24"},
		GameRange:    "1.20-1.24",
		Dependencies: []string{"Vault"},
	}

	cv := contentVersionFromHangar(hv)
	if cv.Source != sourceHangar || cv.Label != hv.Name || cv.Channel != hv.Channel {
		t.Fatalf("converted = %+v", cv)
	}
	if cv.File.URL != hv.DownloadURL || cv.File.Filename != hv.FileName {
		t.Fatalf("file = %+v, want URL/filename from Hangar", cv.File)
	}
	if cv.SHA256 != hv.SHA256 || len(cv.PluginDeps) != 1 || cv.PluginDeps[0] != "Vault" {
		t.Fatalf("hash/deps = %q/%v", cv.SHA256, cv.PluginDeps)
	}
	if cv.external() {
		t.Fatal("a Hangar download URL is not external")
	}
	if got, want := gameVersionsLabel(cv), hv.GameRange; got != want {
		t.Fatalf("label = %q, want game range %q", got, want)
	}

	back := toHangarVersion(cv)
	if back.Name != hv.Name || back.Channel != hv.Channel || back.DownloadURL != hv.DownloadURL ||
		back.FileName != hv.FileName || back.SHA256 != hv.SHA256 {
		t.Fatalf("roundtrip = %+v, want %+v", back, hv)
	}
}

func TestContentVersionExternalAndShortGameList(t *testing.T) {
	ext := contentVersionFromHangar(hangar.Version{
		Name:         "3.0.0",
		ExternalURL:  "https://example.invalid/download",
		GameVersions: []string{"1.21"},
	})
	if !ext.external() {
		t.Fatal("externalUrl must mark the version as external")
	}
	missing := contentVersion{Source: sourceHangar}
	if !missing.external() {
		t.Fatal("a Hangar version without download URL must be external")
	}

	short := contentVersion{
		Source:       sourceHangar,
		GameVersions: []string{"1.20", "1.21"},
		GameRange:    "1.20-1.21",
	}
	if got, want := gameVersionsLabel(short), "1.20, 1.21"; got != want {
		t.Fatalf("label = %q, want explicit versions %q", got, want)
	}
}

// fakeHangarVersion is what the fake versions endpoint publishes for one
// project slug.
type fakeHangarVersion struct {
	name   string
	file   string
	sha256 string
}

type fakeHangar struct {
	byHash   map[string]hangar.Project
	versions map[string]fakeHangarVersion
	// modrinthDown and hangarDown simulate an unreachable source so the
	// best-effort update pass can be exercised.
	modrinthDown bool
	hangarDown   bool
}

// newContentServer serves the two endpoints mrUpdateAll uses: Modrinth's
// hash lookup (always empty here) and Hangar's hash lookup and versions.
func newContentServer(t *testing.T, fake fakeHangar) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/version_files/update", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Modrinth hash lookup method = %s, want POST", r.Method)
		}
		if fake.modrinthDown {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	})
	mux.HandleFunc("/api/v1/versions/hash/", func(w http.ResponseWriter, r *http.Request) {
		if fake.hangarDown {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		sum := strings.TrimPrefix(r.URL.Path, "/api/v1/versions/hash/")
		p, ok := fake.byHash[sum]
		if !ok {
			// The real API answers 404 with an empty body for unknown jars.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeProjectJSON(t, w, p)
	})
	mux.HandleFunc("/api/v1/projects/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/projects/"), "/")
		if len(parts) != 3 || parts[2] != "versions" {
			http.NotFound(w, r)
			return
		}
		v, ok := fake.versions[parts[1]]
		if !ok {
			_, _ = w.Write([]byte(`{"result":[]}`))
			return
		}
		writeJSON(t, w, map[string]any{
			"result": []any{map[string]any{
				"name":    v.name,
				"channel": map[string]any{"name": "Release"},
				"downloads": map[string]any{
					"PAPER": map[string]any{
						"fileInfo":    map[string]any{"name": v.file, "sha256Hash": v.sha256},
						"downloadUrl": "https://example.invalid/" + v.file,
					},
				},
				"platformDependencies": map[string]any{"PAPER": []string{"1.21.7"}},
			}},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeProjectJSON(t *testing.T, w http.ResponseWriter, p hangar.Project) {
	t.Helper()
	writeJSON(t, w, map[string]any{
		"name":      p.Name,
		"namespace": map[string]any{"owner": p.Owner, "slug": p.Slug},
		"stats":     map[string]any{"downloads": p.Downloads},
	})
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encoding fixture: %v", err)
	}
}

// updateAllApp wires a Paper instance to fake Modrinth/Hangar clients, opens
// the plugins kind and writes the given jars into plugins/.
func updateAllApp(t *testing.T, srv *httptest.Server, jars map[string][]byte) (*app, config.Instance) {
	t.Helper()
	a, inst, _ := newJavaApp(t)
	a.mr = &modrinth.Client{BaseURL: srv.URL, HTTP: srv.Client()}
	a.hangar = &hangar.Client{BaseURL: srv.URL, HTTP: srv.Client()}
	a.mrKind.Set("plugins")
	dir := filepath.Join(inst.Dir, "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range jars {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return a, inst
}

func waitMrIdle(t *testing.T, a *app) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !a.mrBusy.Get() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("mrBusy never cleared after update-all")
}

func TestMrUpdateAllHangar(t *testing.T) {
	oldBody := []byte("old plugin build")
	newBody := []byte("new plugin build")
	currentBody := []byte("already current plugin build")

	fake := fakeHangar{
		byHash: map[string]hangar.Project{
			sha256Hex(oldBody):     {Name: "Old", Owner: "Acme", Slug: "old"},
			sha256Hex(currentBody): {Name: "Current", Owner: "Acme", Slug: "current"},
		},
		versions: map[string]fakeHangarVersion{
			"old":     {name: "2.0.0", file: "old-2.0.0.jar", sha256: sha256Hex(newBody)},
			"current": {name: "1.0.0", file: "current-1.0.0.jar", sha256: sha256Hex(currentBody)},
		},
	}
	srv := newContentServer(t, fake)
	a, _ := updateAllApp(t, srv, map[string][]byte{
		"old.jar":     oldBody,
		"current.jar": currentBody,
		"unknown.jar": []byte("nobody knows this jar"),
	})

	a.mrUpdateAll()
	waitMrIdle(t, a)

	if !a.mrUpd.Get() {
		t.Fatalf("update prompt not shown, message = %q", a.mrMsg.Get())
	}
	pending := a.mrPending
	if len(pending) != 1 {
		t.Fatalf("pending = %+v, want exactly the Hangar update", pending)
	}
	u := pending[0]
	if u.Source != sourceHangar || u.Old != "old.jar" || u.File.Filename != "old-2.0.0.jar" ||
		u.File.VersionNumber != "2.0.0" || u.SHA256 != sha256Hex(newBody) {
		t.Fatalf("pending update = %+v", u)
	}
	want := "1 updates available (0 Modrinth, 1 Hangar; 1 current, 1 unknown) — update now?"
	if got := a.mrMsg.Get(); got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

func TestMrUpdateAllUnknownHash(t *testing.T) {
	body := []byte("a jar neither source knows")
	fake := fakeHangar{
		byHash:   map[string]hangar.Project{},
		versions: map[string]fakeHangarVersion{},
	}
	srv := newContentServer(t, fake)
	a, _ := updateAllApp(t, srv, map[string][]byte{"unknown.jar": body})

	a.mrUpdateAll()
	waitMrIdle(t, a)

	if a.mrUpd.Get() || len(a.mrPending) != 0 {
		t.Fatalf("a 404 must be unknown, not an update: prompt=%v pending=%+v", a.mrUpd.Get(), a.mrPending)
	}
	want := "Everything up to date (0 current, 1 not on Modrinth or Hangar)"
	if got := a.mrMsg.Get(); got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

func TestMrUpdateAllNoUpdate(t *testing.T) {
	currentBody := []byte("already current plugin build")
	fake := fakeHangar{
		byHash: map[string]hangar.Project{
			sha256Hex(currentBody): {Name: "Current", Owner: "Acme", Slug: "current"},
		},
		versions: map[string]fakeHangarVersion{
			"current": {name: "1.0.0", file: "current-1.0.0.jar", sha256: sha256Hex(currentBody)},
		},
	}
	srv := newContentServer(t, fake)
	a, _ := updateAllApp(t, srv, map[string][]byte{"current.jar": currentBody})

	a.mrUpdateAll()
	waitMrIdle(t, a)

	if a.mrUpd.Get() || len(a.mrPending) != 0 {
		t.Fatalf("a matching hash must not prompt: prompt=%v pending=%+v", a.mrUpd.Get(), a.mrPending)
	}
	want := "Everything up to date (1 current, 0 not on Modrinth or Hangar)"
	if got := a.mrMsg.Get(); got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

func TestMrUpdateAllSurvivesModrinthOutage(t *testing.T) {
	oldBody := []byte("old hangar build")
	newBody := []byte("new hangar build")
	fake := fakeHangar{
		modrinthDown: true,
		byHash: map[string]hangar.Project{
			sha256Hex(oldBody): {Name: "HangarOnly", Owner: "Acme", Slug: "hangar-only"},
		},
		versions: map[string]fakeHangarVersion{
			"hangar-only": {name: "2.0.0", file: "hangar-only-2.0.0.jar", sha256: sha256Hex(newBody)},
		},
	}
	srv := newContentServer(t, fake)
	a, _ := updateAllApp(t, srv, map[string][]byte{"hangar-only.jar": oldBody})

	a.mrUpdateAll()
	waitMrIdle(t, a)

	if !a.mrUpd.Get() || len(a.mrPending) != 1 {
		t.Fatalf("a Modrinth outage must not block Hangar updates: prompt=%v pending=%+v msg=%q",
			a.mrUpd.Get(), a.mrPending, a.mrMsg.Get())
	}
	if a.mrPending[0].Source != sourceHangar {
		t.Fatalf("pending = %+v, want the Hangar update", a.mrPending[0])
	}
	if msg := a.mrMsg.Get(); !strings.Contains(msg, "Modrinth failed") {
		t.Fatalf("message must name the failed source: %q", msg)
	}
}

func TestMrUpdateAllHangarOutageIsNotUnknown(t *testing.T) {
	fake := fakeHangar{hangarDown: true}
	srv := newContentServer(t, fake)
	a, _ := updateAllApp(t, srv, map[string][]byte{"plugin.jar": []byte("some plugin")})

	a.mrUpdateAll()
	waitMrIdle(t, a)

	if a.mrUpd.Get() || len(a.mrPending) != 0 {
		t.Fatalf("an unreachable Hangar must not produce updates: prompt=%v", a.mrUpd.Get())
	}
	msg := a.mrMsg.Get()
	if !strings.Contains(msg, "1 unchecked") || !strings.Contains(msg, "Hangar failed") {
		t.Fatalf("a network failure must not be reported as an unknown file: %q", msg)
	}
}

func TestHashesOf(t *testing.T) {
	body := []byte("hash me")
	path := filepath.Join(t.TempDir(), "file.jar")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	sha1Want := sha1Hex(body)
	sha256Want := sha256Hex(body)

	sha1Got, sha256Got, err := hashesOf(path)
	if err != nil {
		t.Fatal(err)
	}
	if sha1Got != sha1Want || sha256Got != sha256Want {
		t.Fatalf("hashes = %q/%q, want %q/%q", sha1Got, sha256Got, sha1Want, sha256Want)
	}
}

func sha1Hex(b []byte) string {
	sum := sha1.Sum(b)
	return hex.EncodeToString(sum[:])
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
