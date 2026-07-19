package download

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestForgeInstallerURL(t *testing.T) {
	got := ForgeInstallerURL("", "1.20.1", "47.4.18")
	want := "https://maven.minecraftforge.net/net/minecraftforge/forge/1.20.1-47.4.18/forge-1.20.1-47.4.18-installer.jar"
	if got != want {
		t.Fatalf("url = %q, quiero %q", got, want)
	}
}

func TestNeoForgeInstallerURLModernAndLegacy(t *testing.T) {
	got := NeoForgeInstallerURL("", "1.21.1", "21.1.77")
	want := "https://maven.neoforged.net/releases/net/neoforged/neoforge/21.1.77/neoforge-21.1.77-installer.jar"
	if got != want {
		t.Fatalf("moderno = %q, quiero %q", got, want)
	}
	// NeoForge 47.x (MC 1.20.1) vive en el artefacto legacy "forge".
	got = NeoForgeInstallerURL("", "1.20.1", "47.1.84")
	want = "https://maven.neoforged.net/releases/net/neoforged/forge/1.20.1-47.1.84/forge-1.20.1-47.1.84-installer.jar"
	if got != want {
		t.Fatalf("legacy = %q, quiero %q", got, want)
	}
}

func TestQuiltInstallerURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v3/versions/installer", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"version":"0.15.0","url":"https://maven.quiltmc.org/x/quilt-installer-0.15.0.jar"}]`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	url, err := QuiltInstallerURL(ctx(), nil, srv.URL)
	if err != nil {
		t.Fatalf("QuiltInstallerURL: %v", err)
	}
	if url != "https://maven.quiltmc.org/x/quilt-installer-0.15.0.jar" {
		t.Fatalf("url = %q", url)
	}
}

func TestQuiltInstallerURLEmptyFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v3/versions/installer", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[]`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	if _, err := QuiltInstallerURL(ctx(), nil, srv.URL); err == nil {
		t.Fatal("meta sin installers debe fallar")
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		sign int
	}{
		{"1.21.11", "1.21.9", 1},
		{"26.2", "1.21.11", 1},
		{"1.20.1", "1.20.1", 0},
		{"1.20", "1.20.1", -1},
	}
	for _, c := range cases {
		got := compareVersions(c.a, c.b)
		switch {
		case c.sign > 0 && got <= 0, c.sign < 0 && got >= 0, c.sign == 0 && got != 0:
			t.Fatalf("compareVersions(%q, %q) = %d, quiero signo %d", c.a, c.b, got, c.sign)
		}
	}
}

// --- Forge -----------------------------------------------------------------

func forgeServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/net/minecraftforge/forge/promotions_slim.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"promos":{
			"1.20.1-latest":"47.4.18","1.20.1-recommended":"47.2.0",
			"1.21.11-latest":"61.0.5",
			"26.2-latest":"62.1.0"
		}}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestForgeVersionsSortedNewestFirst(t *testing.T) {
	srv := forgeServer(t)
	p := &Forge{PromosURL: srv.URL, MavenURL: "http://maven"}
	got, err := p.Versions(ctx())
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if strings.Join(got, ",") != "26.2,1.21.11,1.20.1" {
		t.Fatalf("Versions = %v", got)
	}
}

func TestForgeResolvePrefersRecommended(t *testing.T) {
	srv := forgeServer(t)
	p := &Forge{PromosURL: srv.URL, MavenURL: "http://maven"}
	url, err := p.ResolveJarURL(ctx(), "1.20.1")
	if err != nil {
		t.Fatalf("ResolveJarURL: %v", err)
	}
	// 1.20.1 tiene recommended 47.2.0, que gana sobre latest 47.4.18.
	if url != "http://maven/net/minecraftforge/forge/1.20.1-47.2.0/forge-1.20.1-47.2.0-installer.jar" {
		t.Fatalf("url = %q", url)
	}
	url, err = p.ResolveJarURL(ctx(), "26.2")
	if err != nil {
		t.Fatalf("ResolveJarURL 26.2: %v", err)
	}
	if !strings.Contains(url, "26.2-62.1.0") {
		t.Fatalf("url = %q, debe usar latest sin recommended", url)
	}
	if _, err := p.ResolveJarURL(ctx(), "9.9.9"); err == nil {
		t.Fatal("versión sin builds debe fallar")
	}
}

// --- NeoForge ----------------------------------------------------------------

func TestNeoForgeMCMapping(t *testing.T) {
	cases := map[string]string{
		"47.1.84":                 "1.20.1",
		"20.2.86":                 "1.20.2",
		"21.0.30-beta":            "1.21",
		"21.1.77":                 "1.21.1",
		"26.2.0.25":               "26.2",
		"0.25w14craftmine.3-beta": "",
	}
	for v, want := range cases {
		if got := neoForgeMC(v); got != want {
			t.Fatalf("neoForgeMC(%q) = %q, quiero %q", v, got, want)
		}
	}
}

func neoServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/maven/versions/releases/net/neoforged/neoforge", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"versions":["20.2.86","21.1.50","21.1.77","26.2.0.24-beta","26.2.0.25-beta"]}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestNeoForgeVersionsNewestFirst(t *testing.T) {
	srv := neoServer(t)
	p := &NeoForge{BaseURL: srv.URL}
	got, err := p.Versions(ctx())
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if strings.Join(got, ",") != "26.2,1.21.1,1.20.2" {
		t.Fatalf("Versions = %v", got)
	}
}

func TestNeoForgeResolvePrefersStable(t *testing.T) {
	srv := neoServer(t)
	p := &NeoForge{BaseURL: srv.URL}
	url, err := p.ResolveJarURL(ctx(), "1.21.1")
	if err != nil {
		t.Fatalf("ResolveJarURL: %v", err)
	}
	if !strings.Contains(url, "neoforge/21.1.77/") {
		t.Fatalf("url = %q, debe usar el último estable", url)
	}
	// 26.2 solo tiene betas: se usa la última.
	url, err = p.ResolveJarURL(ctx(), "26.2")
	if err != nil {
		t.Fatalf("ResolveJarURL 26.2: %v", err)
	}
	if !strings.Contains(url, "neoforge/26.2.0.25-beta/") {
		t.Fatalf("url = %q", url)
	}
	if _, err := p.ResolveJarURL(ctx(), "9.9.9"); err == nil {
		t.Fatal("versión sin builds debe fallar")
	}
}

// --- Quilt ---------------------------------------------------------------------

func TestQuiltVersionsFiltersUnstable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v3/versions/game", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[
			{"version":"26.3-snapshot-4","stable":false},
			{"version":"26.2","stable":true},
			{"version":"1.21.11","stable":true}
		]`)
	})
	mux.HandleFunc("/v3/versions/installer", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"url":"https://maven.quiltmc.org/x/quilt-installer-0.15.0.jar"}]`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p := &Quilt{BaseURL: srv.URL}
	got, err := p.Versions(ctx())
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if strings.Join(got, ",") != "26.2,1.21.11" {
		t.Fatalf("Versions = %v", got)
	}
	url, err := p.ResolveJarURL(ctx(), "26.2")
	if err != nil {
		t.Fatalf("ResolveJarURL: %v", err)
	}
	if !strings.Contains(url, "quilt-installer") {
		t.Fatalf("url = %q", url)
	}
}
