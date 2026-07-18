package modrinth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mc-tui-server/internal/config"
)

func ctx() context.Context { return context.Background() }

func TestLoadersFor(t *testing.T) {
	cases := []struct {
		typ         config.ServerType
		wantFirst   string
		wantType    string
		expectError bool
	}{
		{config.Paper, "paper", "plugin", false},
		{config.Purpur, "purpur", "plugin", false},
		{config.Fabric, "fabric", "mod", false},
		{config.Vanilla, "", "", true},
	}
	for _, c := range cases {
		loaders, pt, err := loadersFor(c.typ)
		if c.expectError {
			if err == nil {
				t.Fatalf("loadersFor(%s) debe fallar", c.typ)
			}
			continue
		}
		if err != nil {
			t.Fatalf("loadersFor(%s): %v", c.typ, err)
		}
		if loaders[0] != c.wantFirst || pt != c.wantType {
			t.Fatalf("loadersFor(%s) = %v,%q", c.typ, loaders, pt)
		}
	}
}

func TestDefaultBase(t *testing.T) {
	c := &Client{}
	if got := c.base(); got != "https://api.modrinth.com" {
		t.Fatalf("base() = %q", got)
	}
	c.BaseURL = "http://x"
	if got := c.base(); got != "http://x" {
		t.Fatalf("base() con BaseURL = %q", got)
	}
}

func searchServer(t *testing.T, assertQuery func(r *http.Request)) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/search", func(w http.ResponseWriter, r *http.Request) {
		if assertQuery != nil {
			assertQuery(r)
		}
		fmt.Fprint(w, `{"hits":[
			{"project_id":"AAAA","slug":"essentialsx","title":"EssentialsX","description":"Core plugin","downloads":9000},
			{"project_id":"BBBB","slug":"worldedit","title":"WorldEdit","description":"Editor","downloads":5000}
		]}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestSearchBuildsFacetsAndParses(t *testing.T) {
	var gotQuery, gotFacets string
	srv := searchServer(t, func(r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		gotFacets = r.URL.Query().Get("facets")
	})
	c := &Client{BaseURL: srv.URL}
	projects, err := c.Search(ctx(), "essentials", config.Paper, "1.21.4")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotQuery != "essentials" {
		t.Fatalf("query = %q", gotQuery)
	}
	for _, want := range []string{`"project_type:plugin"`, `"versions:1.21.4"`, `"categories:paper"`, `"categories:spigot"`, `"categories:bukkit"`} {
		if !strings.Contains(gotFacets, want) {
			t.Fatalf("facets = %q, falta %s", gotFacets, want)
		}
	}
	if len(projects) != 2 || projects[0].Title != "EssentialsX" || projects[0].Downloads != 9000 {
		t.Fatalf("projects = %+v", projects)
	}
}

func TestSearchVanillaFails(t *testing.T) {
	c := &Client{BaseURL: "http://x"}
	if _, err := c.Search(ctx(), "x", config.Vanilla, "1.21.4"); err == nil {
		t.Fatal("Search con vanilla debe fallar")
	}
}

func TestSearchErrorPropagates(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1"}
	if _, err := c.Search(ctx(), "x", config.Paper, "1.21.4"); err == nil {
		t.Fatal("Search contra servidor inexistente debe fallar")
	}
}

func versionServer(t *testing.T, body string, assertQuery func(r *http.Request)) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/project/AAAA/version", func(w http.ResponseWriter, r *http.Request) {
		if assertQuery != nil {
			assertQuery(r)
		}
		fmt.Fprint(w, body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestLatestFilePicksPrimary(t *testing.T) {
	var gotLoaders, gotVersions string
	srv := versionServer(t, `[
		{"files":[
			{"url":"http://x/extra.jar","filename":"extra.jar","primary":false},
			{"url":"http://x/plugin.jar","filename":"plugin.jar","primary":true}
		]},
		{"files":[{"url":"http://x/old.jar","filename":"old.jar","primary":true}]}
	]`, func(r *http.Request) {
		gotLoaders = r.URL.Query().Get("loaders")
		gotVersions = r.URL.Query().Get("game_versions")
	})
	c := &Client{BaseURL: srv.URL}
	f, err := c.LatestFile(ctx(), "AAAA", config.Fabric, "1.21.4")
	if err != nil {
		t.Fatalf("LatestFile: %v", err)
	}
	if f.Filename != "plugin.jar" || f.URL != "http://x/plugin.jar" {
		t.Fatalf("file = %+v, debe elegir el primario de la versión más nueva", f)
	}
	if !strings.Contains(gotLoaders, `"fabric"`) {
		t.Fatalf("loaders = %q", gotLoaders)
	}
	if !strings.Contains(gotVersions, `"1.21.4"`) {
		t.Fatalf("game_versions = %q", gotVersions)
	}
}

func TestLatestFileFallsBackToFirstFile(t *testing.T) {
	srv := versionServer(t, `[
		{"files":[
			{"url":"http://x/a.jar","filename":"a.jar","primary":false},
			{"url":"http://x/b.jar","filename":"b.jar","primary":false}
		]}
	]`, nil)
	c := &Client{BaseURL: srv.URL}
	f, err := c.LatestFile(ctx(), "AAAA", config.Paper, "1.21.4")
	if err != nil {
		t.Fatalf("LatestFile: %v", err)
	}
	if f.Filename != "a.jar" {
		t.Fatalf("file = %+v, sin primario debe usar el primero", f)
	}
}

func TestLatestFileNoVersionsFails(t *testing.T) {
	srv := versionServer(t, `[]`, nil)
	c := &Client{BaseURL: srv.URL}
	if _, err := c.LatestFile(ctx(), "AAAA", config.Paper, "1.21.4"); err == nil {
		t.Fatal("sin versiones compatibles debe fallar")
	}
}

func TestLatestFileNoFilesFails(t *testing.T) {
	srv := versionServer(t, `[{"files":[]}]`, nil)
	c := &Client{BaseURL: srv.URL}
	if _, err := c.LatestFile(ctx(), "AAAA", config.Paper, "1.21.4"); err == nil {
		t.Fatal("versión sin archivos debe fallar")
	}
}

func TestLatestFileVanillaFails(t *testing.T) {
	c := &Client{BaseURL: "http://x"}
	if _, err := c.LatestFile(ctx(), "AAAA", config.Vanilla, "1.21.4"); err == nil {
		t.Fatal("LatestFile con vanilla debe fallar")
	}
}

func TestLatestFileErrorPropagates(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1"}
	if _, err := c.LatestFile(ctx(), "AAAA", config.Paper, "1.21.4"); err == nil {
		t.Fatal("LatestFile contra servidor inexistente debe fallar")
	}
}
