package installer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func ctx() context.Context { return context.Background() }

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
