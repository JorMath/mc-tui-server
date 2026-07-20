package modrinth

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JorMath/mc-tui-server/internal/config"
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

// --- Modpacks (v0.1.2) -----------------------------------------------------

func TestSearchModpacksBuildsFacets(t *testing.T) {
	var gotQuery, gotFacets string
	srv := searchServer(t, func(r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		gotFacets = r.URL.Query().Get("facets")
	})
	c := &Client{BaseURL: srv.URL}
	packs, err := c.SearchModpacks(ctx(), "optimized")
	if err != nil {
		t.Fatalf("SearchModpacks: %v", err)
	}
	if len(packs) != 2 || packs[0].Title != "EssentialsX" {
		t.Fatalf("packs = %+v", packs)
	}
	if gotQuery != "optimized" {
		t.Fatalf("query = %q", gotQuery)
	}
	wanted := []string{
		`"project_type:modpack"`,
		`"categories:fabric","categories:forge","categories:neoforge","categories:quilt"`,
		`"server_side:required","server_side:optional"`,
	}
	for _, want := range wanted {
		if !strings.Contains(gotFacets, want) {
			t.Fatalf("facets %q sin %q", gotFacets, want)
		}
	}
}

func TestSearchModpacksErrorPropagates(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1"}
	if _, err := c.SearchModpacks(ctx(), "x"); err == nil {
		t.Fatal("SearchModpacks contra servidor inexistente debe fallar")
	}
}

func TestModpackVersionsPicksPrimaryMrpack(t *testing.T) {
	srv := versionServer(t, `[
		{"id":"v1","name":"1.2.0 for 1.21.4","version_number":"1.2.0","version_type":"release",
		 "game_versions":["1.21.4"],"loaders":["forge"],
		 "files":[
			{"url":"https://cdn/x.zip","filename":"extra.zip","primary":false},
			{"url":"https://cdn/p.mrpack","filename":"pack.mrpack","primary":true}
		 ]},
		{"id":"v0","name":"sin archivos","version_number":"1.1.0","version_type":"beta",
		 "game_versions":["1.21.3"],"loaders":["fabric"],"files":[]}
	]`, nil)
	c := &Client{BaseURL: srv.URL}
	vers, err := c.ModpackVersions(ctx(), "AAAA")
	if err != nil {
		t.Fatalf("ModpackVersions: %v", err)
	}
	// La versión sin archivos se descarta; la otra usa el archivo primario
	// y conserva sus loaders.
	if len(vers) != 1 || vers[0].Filename != "pack.mrpack" || vers[0].VersionNumber != "1.2.0" {
		t.Fatalf("vers = %+v", vers)
	}
	if len(vers[0].Loaders) != 1 || vers[0].Loaders[0] != "forge" {
		t.Fatalf("loaders = %v", vers[0].Loaders)
	}
}

func TestModpackVersionsEmptyFails(t *testing.T) {
	srv := versionServer(t, `[]`, nil)
	c := &Client{BaseURL: srv.URL}
	if _, err := c.ModpackVersions(ctx(), "AAAA"); err == nil {
		t.Fatal("modpack sin versiones Fabric debe fallar")
	}
}

// --- Datapacks (v0.1.3) ------------------------------------------------------

func TestSearchDatapacksBuildsFacets(t *testing.T) {
	var gotFacets string
	srv := searchServer(t, func(r *http.Request) {
		gotFacets = r.URL.Query().Get("facets")
	})
	c := &Client{BaseURL: srv.URL}
	packs, err := c.SearchDatapacks(ctx(), "terralith", "1.21.4")
	if err != nil {
		t.Fatalf("SearchDatapacks: %v", err)
	}
	if len(packs) != 2 {
		t.Fatalf("packs = %+v", packs)
	}
	for _, want := range []string{`"project_type:datapack"`, `"versions:1.21.4"`} {
		if !strings.Contains(gotFacets, want) {
			t.Fatalf("facets %q sin %q", gotFacets, want)
		}
	}
}

func TestLatestDatapackFileUsesDatapackLoader(t *testing.T) {
	var gotLoaders, gotVersions string
	srv := versionServer(t, `[
		{"files":[{"url":"https://cdn/t.zip","filename":"terralith.zip","primary":true}]}
	]`, func(r *http.Request) {
		gotLoaders = r.URL.Query().Get("loaders")
		gotVersions = r.URL.Query().Get("game_versions")
	})
	c := &Client{BaseURL: srv.URL}
	f, err := c.LatestDatapackFile(ctx(), "AAAA", "1.21.4")
	if err != nil {
		t.Fatalf("LatestDatapackFile: %v", err)
	}
	if f.Filename != "terralith.zip" {
		t.Fatalf("file = %+v", f)
	}
	if gotLoaders != `["datapack"]` || gotVersions != `["1.21.4"]` {
		t.Fatalf("loaders = %q, game_versions = %q", gotLoaders, gotVersions)
	}
}

func TestLatestDatapackFileNoVersionsFails(t *testing.T) {
	srv := versionServer(t, `[]`, nil)
	c := &Client{BaseURL: srv.URL}
	if _, err := c.LatestDatapackFile(ctx(), "AAAA", "1.21.4"); err == nil {
		t.Fatal("sin versiones datapack compatibles debe fallar")
	}
}

func TestServerUnsupportedBatches(t *testing.T) {
	var gotIDs []string
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/projects", func(w http.ResponseWriter, r *http.Request) {
		gotIDs = append(gotIDs, r.URL.Query().Get("ids"))
		fmt.Fprint(w, `[
			{"id":"AAAA","server_side":"required"},
			{"id":"BBBB","server_side":"unsupported"},
			{"id":"CCCC","server_side":"optional"}
		]`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := &Client{BaseURL: srv.URL}
	unsupported, err := c.ServerUnsupported(ctx(), []string{"AAAA", "BBBB", "CCCC"})
	if err != nil {
		t.Fatalf("ServerUnsupported: %v", err)
	}
	if len(unsupported) != 1 || !unsupported["BBBB"] {
		t.Fatalf("unsupported = %v", unsupported)
	}
	if len(gotIDs) != 1 || gotIDs[0] != `["AAAA","BBBB","CCCC"]` {
		t.Fatalf("ids = %v", gotIDs)
	}
}

func TestServerUnsupportedErrorPropagates(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1"}
	if _, err := c.ServerUnsupported(ctx(), []string{"AAAA"}); err == nil {
		t.Fatal("ServerUnsupported contra servidor inexistente debe fallar")
	}
}

func TestLatestByHash(t *testing.T) {
	var gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/version_files/update", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		fmt.Fprint(w, `{
			"aaa111":{"files":[{"url":"https://cdn/new.jar","filename":"new.jar","primary":true,"hashes":{"sha1":"bbb222"}}]},
			"ccc333":{"files":[{"url":"https://cdn/same.jar","filename":"same.jar","primary":true,"hashes":{"sha1":"ccc333"}}]}
		}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := &Client{BaseURL: srv.URL}
	latest, err := c.LatestByHash(ctx(), config.Forge, "1.20.1", []string{"aaa111", "ccc333", "desconocido"})
	if err != nil {
		t.Fatalf("LatestByHash: %v", err)
	}
	for _, want := range []string{`"algorithm":"sha1"`, `"forge"`, `"1.20.1"`, `"aaa111"`} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("body %q sin %q", gotBody, want)
		}
	}
	// aaa111 tiene versión nueva (sha1 distinto); ccc333 está al día.
	if f := latest["aaa111"]; f.Filename != "new.jar" || f.SHA1 != "bbb222" {
		t.Fatalf("aaa111 = %+v", f)
	}
	if f := latest["ccc333"]; f.SHA1 != "ccc333" {
		t.Fatalf("ccc333 = %+v", f)
	}
	if _, ok := latest["desconocido"]; ok {
		t.Fatal("hash desconocido no debe aparecer")
	}
}

func TestLatestByHashVanillaFails(t *testing.T) {
	c := &Client{BaseURL: "http://x"}
	if _, err := c.LatestByHash(ctx(), config.Vanilla, "1.20.1", []string{"a"}); err == nil {
		t.Fatal("vanilla no tiene loaders de mods; debe fallar")
	}
}
