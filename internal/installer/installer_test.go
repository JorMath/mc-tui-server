package installer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func ctx() context.Context { return context.Background() }

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

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectLaunchModernForge(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "libraries", "net", "minecraftforge", "forge", "1.20.1-47.4.18", "win_args.txt"))
	argsDir, jar, err := DetectLaunch(dir)
	if err != nil {
		t.Fatalf("DetectLaunch: %v", err)
	}
	want := filepath.Join("libraries", "net", "minecraftforge", "forge", "1.20.1-47.4.18")
	if argsDir != want || jar != "" {
		t.Fatalf("argsDir=%q jar=%q, quiero argsDir=%q", argsDir, jar, want)
	}
}

func TestDetectLaunchNeoForge(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "libraries", "net", "neoforged", "neoforge", "21.1.77", "unix_args.txt"))
	argsDir, _, err := DetectLaunch(dir)
	if err != nil {
		t.Fatalf("DetectLaunch: %v", err)
	}
	if argsDir != filepath.Join("libraries", "net", "neoforged", "neoforge", "21.1.77") {
		t.Fatalf("argsDir = %q", argsDir)
	}
}

func TestDetectLaunchLegacyJarSkipsInstaller(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "forge-1.12.2-14.23.5.2860-installer.jar"))
	mustWrite(t, filepath.Join(dir, "forge-1.12.2-14.23.5.2860.jar"))
	argsDir, jar, err := DetectLaunch(dir)
	if err != nil {
		t.Fatalf("DetectLaunch: %v", err)
	}
	if argsDir != "" || jar != "forge-1.12.2-14.23.5.2860.jar" {
		t.Fatalf("argsDir=%q jar=%q", argsDir, jar)
	}
}

func TestDetectLaunchQuilt(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "quilt-server-launch.jar"))
	_, jar, err := DetectLaunch(dir)
	if err != nil {
		t.Fatalf("DetectLaunch: %v", err)
	}
	if jar != "quilt-server-launch.jar" {
		t.Fatalf("jar = %q", jar)
	}
}

func TestDetectLaunchNothingFails(t *testing.T) {
	if _, _, err := DetectLaunch(t.TempDir()); err == nil {
		t.Fatal("instancia vacía debe fallar")
	}
}

func TestRunForgeLikeStreamsOutputAndFails(t *testing.T) {
	// Un "java" falso: go run no está disponible en tests unitarios, así
	// que se usa un comando que no existe para el caso de error.
	err := RunForgeLike(ctx(), "programa-que-no-existe-xyz", "inst.jar", t.TempDir(), nil)
	if err == nil {
		t.Fatal("un java inexistente debe fallar")
	}
}
