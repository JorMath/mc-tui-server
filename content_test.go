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
	"strconv"
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
		Dependencies: []hangar.Dependency{
			{ProjectID: 5, Name: "Vault", Required: true},
			{ProjectID: 6, Name: "Optional", Required: false},
			{ProjectID: 0, Name: "Outside", Required: true, ExternalURL: "https://outside.invalid/x.jar"},
		},
	}

	cv := contentVersionFromHangar(hv)
	if cv.Source != sourceHangar || cv.Label != hv.Name || cv.Channel != hv.Channel {
		t.Fatalf("converted = %+v", cv)
	}
	if cv.File.URL != hv.DownloadURL || cv.File.Filename != hv.FileName {
		t.Fatalf("file = %+v, want URL/filename from Hangar", cv.File)
	}
	if cv.SHA256 != hv.SHA256 {
		t.Fatalf("hash = %q", cv.SHA256)
	}
	// The row lists required dependencies, marking the external one.
	if got, want := strings.Join(cv.PluginDeps, ","), "Vault,Outside (external)"; got != want {
		t.Fatalf("plugin deps = %q, want %q", got, want)
	}
	if len(cv.HangarDeps) != len(hv.Dependencies) {
		t.Fatalf("hangar deps = %+v, want every declared dependency", cv.HangarDeps)
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
	if len(back.Dependencies) != len(hv.Dependencies) || back.Dependencies[0].ProjectID != 5 {
		t.Fatalf("roundtrip dependencies = %+v, want %+v", back.Dependencies, hv.Dependencies)
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

// fakeHangarDep is one pluginDependencies entry served to the install path.
type fakeHangarDep struct {
	name      string
	projectID int
	required  bool
	external  string
}

// fakeHangarVersionFile is one version served by the fake install server.
type fakeHangarVersionFile struct {
	name     string
	file     string
	external string
	deps     []fakeHangarDep
}

// fakeHangarInstall serves the three endpoints a Hangar install touches:
// versions by owner/slug, versions by numeric id and the jar files.
type fakeHangarInstall struct {
	root  fakeHangarVersionFile
	deps  map[int]fakeHangarVersionFile
	files map[string][]byte
	base  string
}

func (f *fakeHangarInstall) writeVersion(t *testing.T, w http.ResponseWriter, v fakeHangarVersionFile) {
	t.Helper()
	downloads := map[string]any{
		"fileInfo": map[string]any{"name": v.file, "sha256Hash": sha256Hex(f.files[v.file])},
	}
	if v.external != "" {
		downloads["externalUrl"] = v.external
	} else {
		downloads["downloadUrl"] = f.base + "/files/" + v.file
	}
	deps := make([]any, 0, len(v.deps))
	for _, d := range v.deps {
		entry := map[string]any{
			"name":        d.name,
			"projectId":   d.projectID,
			"required":    d.required,
			"externalUrl": nil,
		}
		if d.external != "" {
			entry["externalUrl"] = d.external
		}
		deps = append(deps, entry)
	}
	writeJSON(t, w, map[string]any{
		"result": []any{map[string]any{
			"name":                 v.name,
			"channel":              map[string]any{"name": "Release"},
			"downloads":            map[string]any{"PAPER": downloads},
			"pluginDependencies":   map[string]any{"PAPER": deps},
			"platformDependencies": map[string]any{"PAPER": []string{"1.21.7"}},
		}},
	})
}

func newHangarInstallServer(t *testing.T, f *fakeHangarInstall) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/projects/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/projects/"), "/")
		switch {
		case len(parts) == 2 && parts[1] == "versions":
			id, err := strconv.Atoi(parts[0])
			if err != nil {
				http.NotFound(w, r)
				return
			}
			v, ok := f.deps[id]
			if !ok {
				_, _ = w.Write([]byte(`{"result":[]}`))
				return
			}
			f.writeVersion(t, w, v)
		case len(parts) == 3 && parts[2] == "versions":
			f.writeVersion(t, w, f.root)
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		body, ok := f.files[strings.TrimPrefix(r.URL.Path, "/files/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	f.base = srv.URL
	return srv
}

func TestMrInstallHangarInstallsRequiredDependencies(t *testing.T) {
	files := map[string][]byte{
		"root.jar":     []byte("root jar"),
		"dep-a.jar":    []byte("dependency a"),
		"dep-b.jar":    []byte("dependency b"),
		"optional.jar": []byte("optional dependency"),
	}
	fake := &fakeHangarInstall{
		files: files,
		root: fakeHangarVersionFile{name: "1.0.0", file: "root.jar", deps: []fakeHangarDep{
			{name: "DepA", projectID: 2, required: true},
			{name: "DepB", projectID: 3, required: true},
			{name: "ExternalDep", projectID: 4, required: true},
			{name: "OptionalDep", projectID: 5, required: false},
		}},
		deps: map[int]fakeHangarVersionFile{
			2: {name: "2.0.0", file: "dep-a.jar"},
			3: {name: "2.1.0", file: "dep-b.jar"},
			4: {name: "3.0.0", file: "external.jar", external: "https://outside.invalid/external.jar"},
			5: {name: "1.0.0", file: "optional.jar"},
		},
	}
	srv := newHangarInstallServer(t, fake)
	a, inst, _ := newJavaApp(t)
	a.hangar = &hangar.Client{BaseURL: srv.URL, HTTP: srv.Client()}
	a.mrKind.Set("plugins")
	a.mrResults.Set([]contentItem{{
		Source: sourceHangar, HangarID: 1, Owner: "Acme", Slug: "root", Title: "Root",
	}})
	a.mrIdx.Set(0)

	a.mrInstall()
	waitMrIdle(t, a)

	dir := filepath.Join(inst.Dir, "plugins")
	for _, name := range []string{"root.jar", "dep-a.jar", "dep-b.jar"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got) != string(files[name]) {
			t.Fatalf("%s content = %q, want %q", name, got, files[name])
		}
	}
	for _, name := range []string{"external.jar", "optional.jar"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s must not be installed (external or optional), stat err = %v", name, err)
		}
	}
	want := "Installed root.jar (+2 dependencies) into plugins/ · restart the server to load it"
	if got := a.mrMsg.Get(); got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
	logs := strings.Join(a.logs.Get()[inst.Name], "\n")
	if !strings.Contains(logs, "Skipped dependency ExternalDep: hosted externally (https://outside.invalid/external.jar)") {
		t.Fatalf("log = %q, want the external dependency reported", logs)
	}
	if strings.Contains(logs, "optional.jar") {
		t.Fatalf("log = %q, an optional dependency must not be followed", logs)
	}
}
