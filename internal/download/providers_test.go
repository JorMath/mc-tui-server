package download

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func ctx() context.Context { return context.Background() }

// --- Vanilla -------------------------------------------------------------

func vanillaServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/mc/game/version_manifest_v2.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"versions":[
			{"id":"1.21.4","type":"release","url":"%s/v1/1.21.4.json"},
			{"id":"25w03a","type":"snapshot","url":"%s/v1/25w03a.json"},
			{"id":"1.21.3","type":"release","url":"%s/v1/1.21.3.json"}
		]}`, srv.URL, srv.URL, srv.URL)
	})
	mux.HandleFunc("/v1/1.21.4.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"downloads":{"server":{"url":"%s/jars/server-1.21.4.jar"}}}`, srv.URL)
	})
	mux.HandleFunc("/v1/1.21.3.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"downloads":{}}`)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestVanillaVersionsFiltersSnapshots(t *testing.T) {
	srv := vanillaServer(t)
	p := &Vanilla{BaseURL: srv.URL}
	got, err := p.Versions(ctx())
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	want := []string{"1.21.4", "1.21.3"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Versions = %v, quiero %v", got, want)
	}
}

func TestVanillaVersionsErrorPropagates(t *testing.T) {
	p := &Vanilla{BaseURL: "http://127.0.0.1:1"}
	if _, err := p.Versions(ctx()); err == nil {
		t.Fatal("Versions contra servidor inexistente debe fallar")
	}
}

func TestVanillaResolveJarURL(t *testing.T) {
	srv := vanillaServer(t)
	p := &Vanilla{BaseURL: srv.URL}
	url, err := p.ResolveJarURL(ctx(), "1.21.4")
	if err != nil {
		t.Fatalf("ResolveJarURL: %v", err)
	}
	if !strings.HasSuffix(url, "/jars/server-1.21.4.jar") {
		t.Fatalf("url = %q", url)
	}
}

func TestVanillaResolveUnknownVersionFails(t *testing.T) {
	srv := vanillaServer(t)
	p := &Vanilla{BaseURL: srv.URL}
	if _, err := p.ResolveJarURL(ctx(), "0.0.0"); err == nil {
		t.Fatal("versión inexistente debe fallar")
	}
}

func TestVanillaResolveNoServerJarFails(t *testing.T) {
	srv := vanillaServer(t)
	p := &Vanilla{BaseURL: srv.URL}
	if _, err := p.ResolveJarURL(ctx(), "1.21.3"); err == nil {
		t.Fatal("versión sin jar de servidor debe fallar")
	}
}

func TestVanillaResolveManifestErrorPropagates(t *testing.T) {
	p := &Vanilla{BaseURL: "http://127.0.0.1:1"}
	if _, err := p.ResolveJarURL(ctx(), "1.21.4"); err == nil {
		t.Fatal("error del manifiesto debe propagarse en ResolveJarURL")
	}
}

func TestVanillaResolveVersionJSONErrorPropagates(t *testing.T) {
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/mc/game/version_manifest_v2.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"versions":[{"id":"1.21.4","type":"release","url":"%s/v1/roto.json"}]}`, srv.URL)
	})
	srv = httptest.NewServer(mux) // /v1/roto.json responde 404
	t.Cleanup(srv.Close)
	p := &Vanilla{BaseURL: srv.URL}
	if _, err := p.ResolveJarURL(ctx(), "1.21.4"); err == nil {
		t.Fatal("error del JSON de versión debe propagarse")
	}
}

// --- Paper ---------------------------------------------------------------

func paperServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/projects/paper", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"versions":["1.21.3","1.21.4"]}`)
	})
	mux.HandleFunc("/v2/projects/paper/versions/1.21.4/builds", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"builds":[
			{"build":90,"channel":"default","downloads":{"application":{"name":"paper-1.21.4-90.jar"}}},
			{"build":91,"channel":"default","downloads":{"application":{"name":"paper-1.21.4-91.jar"}}},
			{"build":92,"channel":"experimental","downloads":{"application":{"name":"paper-1.21.4-92.jar"}}}
		]}`)
	})
	mux.HandleFunc("/v2/projects/paper/versions/1.21.3/builds", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"builds":[
			{"build":10,"channel":"experimental","downloads":{"application":{"name":"paper-1.21.3-10.jar"}}}
		]}`)
	})
	mux.HandleFunc("/v2/projects/paper/versions/vacia/builds", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"builds":[]}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestPaperVersions(t *testing.T) {
	srv := paperServer(t)
	p := &Paper{BaseURL: srv.URL}
	got, err := p.Versions(ctx())
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	// Se invierte para mostrar las más nuevas primero.
	if strings.Join(got, ",") != "1.21.4,1.21.3" {
		t.Fatalf("Versions = %v", got)
	}
}

func TestPaperVersionsErrorPropagates(t *testing.T) {
	p := &Paper{BaseURL: "http://127.0.0.1:1"}
	if _, err := p.Versions(ctx()); err == nil {
		t.Fatal("Versions contra servidor inexistente debe fallar")
	}
}

func TestPaperResolvePrefersLatestDefaultBuild(t *testing.T) {
	srv := paperServer(t)
	p := &Paper{BaseURL: srv.URL}
	url, err := p.ResolveJarURL(ctx(), "1.21.4")
	if err != nil {
		t.Fatalf("ResolveJarURL: %v", err)
	}
	want := srv.URL + "/v2/projects/paper/versions/1.21.4/builds/91/downloads/paper-1.21.4-91.jar"
	if url != want {
		t.Fatalf("url = %q, quiero %q", url, want)
	}
}

func TestPaperResolveFallsBackToLastBuild(t *testing.T) {
	srv := paperServer(t)
	p := &Paper{BaseURL: srv.URL}
	url, err := p.ResolveJarURL(ctx(), "1.21.3")
	if err != nil {
		t.Fatalf("ResolveJarURL: %v", err)
	}
	if !strings.HasSuffix(url, "/builds/10/downloads/paper-1.21.3-10.jar") {
		t.Fatalf("url = %q", url)
	}
}

func TestPaperResolveNoBuildsFails(t *testing.T) {
	srv := paperServer(t)
	p := &Paper{BaseURL: srv.URL}
	if _, err := p.ResolveJarURL(ctx(), "vacia"); err == nil {
		t.Fatal("versión sin builds debe fallar")
	}
}

func TestPaperResolveErrorPropagates(t *testing.T) {
	srv := paperServer(t)
	p := &Paper{BaseURL: srv.URL}
	if _, err := p.ResolveJarURL(ctx(), "inexistente"); err == nil {
		t.Fatal("versión inexistente debe fallar (404 de builds)")
	}
}

// --- Purpur --------------------------------------------------------------

func purpurServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/purpur", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"versions":["1.21.3","1.21.4"]}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestPurpurVersions(t *testing.T) {
	srv := purpurServer(t)
	p := &Purpur{BaseURL: srv.URL}
	got, err := p.Versions(ctx())
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if strings.Join(got, ",") != "1.21.4,1.21.3" {
		t.Fatalf("Versions = %v", got)
	}
}

func TestPurpurVersionsErrorPropagates(t *testing.T) {
	p := &Purpur{BaseURL: "http://127.0.0.1:1"}
	if _, err := p.Versions(ctx()); err == nil {
		t.Fatal("Versions contra servidor inexistente debe fallar")
	}
}

func TestPurpurResolveJarURLDefaultBase(t *testing.T) {
	// Sin BaseURL configurada se usa la URL oficial (no hay red: la URL
	// se arma sin consultar la API).
	p := &Purpur{}
	url, err := p.ResolveJarURL(ctx(), "1.21.4")
	if err != nil {
		t.Fatalf("ResolveJarURL: %v", err)
	}
	if url != "https://api.purpurmc.org/v2/purpur/1.21.4/latest/download" {
		t.Fatalf("url = %q", url)
	}
}

func TestPurpurResolveJarURL(t *testing.T) {
	p := &Purpur{BaseURL: "https://api.ejemplo.org"}
	url, err := p.ResolveJarURL(ctx(), "1.21.4")
	if err != nil {
		t.Fatalf("ResolveJarURL: %v", err)
	}
	if url != "https://api.ejemplo.org/v2/purpur/1.21.4/latest/download" {
		t.Fatalf("url = %q", url)
	}
}

// --- Fabric --------------------------------------------------------------

func fabricServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/versions/game", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[
			{"version":"1.21.4","stable":true},
			{"version":"25w03a","stable":false},
			{"version":"1.21.3","stable":true}
		]`)
	})
	mux.HandleFunc("/v2/versions/loader", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"version":"0.17.0-beta","stable":false},{"version":"0.16.9","stable":true}]`)
	})
	mux.HandleFunc("/v2/versions/installer", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"version":"1.0.1","stable":true},{"version":"1.0.0","stable":true}]`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFabricVersionsFiltersUnstable(t *testing.T) {
	srv := fabricServer(t)
	p := &Fabric{BaseURL: srv.URL}
	got, err := p.Versions(ctx())
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if strings.Join(got, ",") != "1.21.4,1.21.3" {
		t.Fatalf("Versions = %v", got)
	}
}

func TestFabricVersionsErrorPropagates(t *testing.T) {
	p := &Fabric{BaseURL: "http://127.0.0.1:1"}
	if _, err := p.Versions(ctx()); err == nil {
		t.Fatal("Versions contra servidor inexistente debe fallar")
	}
}

func TestFabricResolveJarURL(t *testing.T) {
	srv := fabricServer(t)
	p := &Fabric{BaseURL: srv.URL}
	url, err := p.ResolveJarURL(ctx(), "1.21.4")
	if err != nil {
		t.Fatalf("ResolveJarURL: %v", err)
	}
	want := srv.URL + "/v2/versions/loader/1.21.4/0.16.9/1.0.1/server/jar"
	if url != want {
		t.Fatalf("url = %q, quiero %q", url, want)
	}
}

func TestFabricResolveLoaderErrorPropagates(t *testing.T) {
	mux := http.NewServeMux() // /v2/versions/loader responde 404
	mux.HandleFunc("/v2/versions/installer", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"version":"1.0.1","stable":true}]`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p := &Fabric{BaseURL: srv.URL}
	if _, err := p.ResolveJarURL(ctx(), "1.21.4"); err == nil {
		t.Fatal("error del endpoint de loader debe propagarse")
	}
}

func TestFabricResolveNoStableLoaderFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/versions/loader", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"version":"0.17.0-beta","stable":false}]`)
	})
	mux.HandleFunc("/v2/versions/installer", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"version":"1.0.1","stable":true}]`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p := &Fabric{BaseURL: srv.URL}
	if _, err := p.ResolveJarURL(ctx(), "1.21.4"); err == nil {
		t.Fatal("sin loader estable debe fallar")
	}
}

func TestFabricResolveInstallerErrorPropagates(t *testing.T) {
	mux := http.NewServeMux() // /v2/versions/installer responde 404
	mux.HandleFunc("/v2/versions/loader", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"version":"0.16.9","stable":true}]`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p := &Fabric{BaseURL: srv.URL}
	if _, err := p.ResolveJarURL(ctx(), "1.21.4"); err == nil {
		t.Fatal("error del endpoint de installer debe propagarse")
	}
}

func TestFabricServerJarURLForUsesExactLoader(t *testing.T) {
	srv := fabricServer(t)
	p := &Fabric{BaseURL: srv.URL}
	url, err := p.ServerJarURLFor(ctx(), "1.20.1", "0.15.11")
	if err != nil {
		t.Fatalf("ServerJarURLFor: %v", err)
	}
	want := srv.URL + "/v2/versions/loader/1.20.1/0.15.11/1.0.1/server/jar"
	if url != want {
		t.Fatalf("url = %q, quiero %q", url, want)
	}
}

func TestFabricServerJarURLForInstallerErrorPropagates(t *testing.T) {
	p := &Fabric{BaseURL: "http://127.0.0.1:1"}
	if _, err := p.ServerJarURLFor(ctx(), "1.20.1", "0.15.11"); err == nil {
		t.Fatal("error del endpoint de installer debe propagarse")
	}
}

func TestFabricResolveNoStableInstallerFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/versions/loader", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"version":"0.16.9","stable":true}]`)
	})
	mux.HandleFunc("/v2/versions/installer", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"version":"1.0.1","stable":false}]`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p := &Fabric{BaseURL: srv.URL}
	if _, err := p.ResolveJarURL(ctx(), "1.21.4"); err == nil {
		t.Fatal("sin installer estable debe fallar")
	}
}
