package javaenv

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// zipArchive arma un zip con las entradas dadas (nombre -> contenido).
func zipArchive(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type tarEntry struct {
	name     string
	body     string
	typeflag byte
	link     string
}

func tarGzArchive(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		tf := e.typeflag
		if tf == 0 {
			tf = tar.TypeReg
		}
		hdr := &tar.Header{Name: e.name, Typeflag: tf, Linkname: e.link, Mode: 0o755}
		if tf == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if tf == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// adoptiumServer sirve el JSON de /v3/assets/latest y el archivo pedido.
// Devuelve el server, el contador de descargas y el de metadatos.
func adoptiumServer(t *testing.T, archive []byte, checksum string) (*httptest.Server, *int32, *int32) {
	t.Helper()
	if checksum == "" {
		sum := sha256.Sum256(archive)
		checksum = hex.EncodeToString(sum[:])
	}
	var downloads, queries int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/archive" {
			atomic.AddInt32(&downloads, 1)
			_, _ = w.Write(archive)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v3/assets/latest/") {
			atomic.AddInt32(&queries, 1)
			if got := r.URL.Query().Get("image_type"); got != "jre" {
				t.Errorf("image_type = %q, quiero jre", got)
			}
			payload := []map[string]any{{
				"release_name": "jdk-21-test",
				"binary": map[string]any{
					"package": map[string]any{
						"name":     "OpenJDK21U-jre.zip",
						"link":     srv.URL + "/archive",
						"checksum": checksum,
						"size":     len(archive),
					},
				},
			}}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(payload)
			return
		}
		http.NotFound(w, r)
	}))
	return srv, &downloads, &queries
}

func testConfig(srv *httptest.Server) Config {
	return Config{BaseURL: srv.URL, Client: srv.Client(), OS: "linux", Arch: "amd64"}
}

func stagingLeftovers(t *testing.T, base string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(base, "java"))
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".install-") || strings.HasPrefix(e.Name(), ".lock-") {
			left = append(left, e.Name())
		}
	}
	return left
}

func TestEnsureDownloadsAndExtractsZip(t *testing.T) {
	archive := zipArchive(t, map[string]string{
		"jdk-21-test/bin/java":     "#!/bin/sh\n",
		"jdk-21-test/bin/java.exe": "MZ",
		"jdk-21-test/release":      "JAVA_VERSION=21\n",
	})
	srv, downloads, queries := adoptiumServer(t, archive, "")
	defer srv.Close()
	base := t.TempDir()

	var progress int32
	rt, err := EnsureWith(context.Background(), testConfig(srv), 21, base, func(done, total int64) {
		atomic.AddInt32(&progress, 1)
	})
	if err != nil {
		t.Fatalf("EnsureWith() error = %v", err)
	}
	if want := JavaPath(Dir(base, 21)); rt.Path != want {
		t.Fatalf("Path = %q, quiero %q", rt.Path, want)
	}
	if rt.Major != 21 {
		t.Fatalf("Major = %d, quiero 21", rt.Major)
	}
	if info, err := os.Stat(rt.Path); err != nil || info.IsDir() {
		t.Fatalf("el binario no quedó en disco: %v", err)
	}
	if got := atomic.LoadInt32(downloads); got != 1 {
		t.Fatalf("descargas = %d, quiero 1", got)
	}
	if got := atomic.LoadInt32(queries); got != 1 {
		t.Fatalf("consultas = %d, quiero 1", got)
	}
	if got := atomic.LoadInt32(&progress); got == 0 {
		t.Fatal("no se reportó progreso de descarga")
	}
	if left := stagingLeftovers(t, base); len(left) != 0 {
		t.Fatalf("quedaron restos de staging: %v", left)
	}
}

func TestEnsureRejectsBadChecksum(t *testing.T) {
	archive := zipArchive(t, map[string]string{"jdk-21/bin/java": "x", "jdk-21/bin/java.exe": "x"})
	srv, _, _ := adoptiumServer(t, archive, strings.Repeat("0", 64))
	defer srv.Close()
	base := t.TempDir()

	_, err := EnsureWith(context.Background(), testConfig(srv), 21, base, nil)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("EnsureWith() error = %v, quiero el error de checksum", err)
	}
	if _, ok := Installed(base, 21); ok {
		t.Fatal("se instaló un runtime con checksum inválido")
	}
}

func TestEnsureRejectsZipSlip(t *testing.T) {
	archive := zipArchive(t, map[string]string{
		"jdk-21/bin/java":     "x",
		"jdk-21/bin/java.exe": "x",
		"../evil.txt":         "pwned",
	})
	srv, _, _ := adoptiumServer(t, archive, "")
	defer srv.Close()
	base := t.TempDir()

	_, err := EnsureWith(context.Background(), testConfig(srv), 21, base, nil)
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("EnsureWith() error = %v, quiero el rechazo de zip-slip", err)
	}
	_ = filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
		if err == nil && info != nil && info.Name() == "evil.txt" {
			t.Fatalf("la entrada maliciosa se escribió en %s", p)
		}
		return nil
	})
}

func TestEnsureReusesExistingRuntime(t *testing.T) {
	base := t.TempDir()
	p := JavaPath(Dir(base, 21))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("installed"), 0o755); err != nil {
		t.Fatal(err)
	}
	// BaseURL inválida: si se llamara a la red, el test fallaría.
	_, err := EnsureWith(context.Background(), Config{BaseURL: "http://127.0.0.1:1"}, 21, base, nil)
	if err != nil {
		t.Fatalf("EnsureWith() reutilizando el runtime existente: %v", err)
	}
}

func TestEnsureConcurrentSingleDownload(t *testing.T) {
	archive := zipArchive(t, map[string]string{
		"jdk-21/bin/java":     "x",
		"jdk-21/bin/java.exe": "x",
	})
	srv, downloads, _ := adoptiumServer(t, archive, "")
	defer srv.Close()
	base := t.TempDir()

	type result struct {
		rt  Runtime
		err error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			rt, err := EnsureWith(context.Background(), testConfig(srv), 21, base, nil)
			results <- result{rt, err}
		}()
	}
	var paths []string
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil {
			t.Fatalf("EnsureWith() concurrente error = %v", r.err)
		}
		paths = append(paths, r.rt.Path)
	}
	if paths[0] != paths[1] {
		t.Fatalf("rutas distintas: %q y %q", paths[0], paths[1])
	}
	if got := atomic.LoadInt32(downloads); got != 1 {
		t.Fatalf("descargas = %d, quiero 1 con dos Ensure simultáneos", got)
	}
}

func TestEnsureExtractsTarGz(t *testing.T) {
	archive := tarGzArchive(t, []tarEntry{
		{name: "jdk-21-test/bin", typeflag: tar.TypeDir},
		{name: "jdk-21-test/bin/java", body: "#!/bin/sh\n"},
		{name: "jdk-21-test/bin/java.exe", body: "MZ"},
		{name: "jdk-21-test/release", body: "JAVA_VERSION=21\n"},
	})
	srv, _, _ := adoptiumServer(t, archive, "")
	defer srv.Close()
	base := t.TempDir()

	rt, err := EnsureWith(context.Background(), testConfig(srv), 21, base, nil)
	if err != nil {
		t.Fatalf("EnsureWith() tar.gz error = %v", err)
	}
	data, err := os.ReadFile(rt.Path)
	if err != nil || len(data) == 0 {
		t.Fatalf("no se extrajo el binario: %v", err)
	}
}

func TestEnsureHandlesMacLayout(t *testing.T) {
	archive := zipArchive(t, map[string]string{
		"jdk-21-test/Contents/Home/bin/java":     "#!/bin/sh\n",
		"jdk-21-test/Contents/Home/bin/java.exe": "MZ",
	})
	srv, _, _ := adoptiumServer(t, archive, "")
	defer srv.Close()
	base := t.TempDir()

	if _, err := EnsureWith(context.Background(), testConfig(srv), 21, base, nil); err != nil {
		t.Fatalf("EnsureWith() con layout de macOS error = %v", err)
	}
	if _, ok := Installed(base, 21); !ok {
		t.Fatal("el layout Contents/Home no se normalizó")
	}
}

func TestEnsureRejectsSymlinkEscape(t *testing.T) {
	archive := tarGzArchive(t, []tarEntry{
		{name: "jdk-21/bin/java", body: "x"},
		{name: "jdk-21/bin/java.exe", body: "x"},
		{name: "jdk-21/escape", typeflag: tar.TypeSymlink, link: "../../etc/passwd"},
	})
	srv, _, _ := adoptiumServer(t, archive, "")
	defer srv.Close()

	_, err := EnsureWith(context.Background(), testConfig(srv), 21, t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "links outside") {
		t.Fatalf("EnsureWith() error = %v, quiero el rechazo del symlink", err)
	}
}

func TestEnsureRemovesStaleLock(t *testing.T) {
	archive := zipArchive(t, map[string]string{
		"jdk-21/bin/java":     "x",
		"jdk-21/bin/java.exe": "x",
	})
	srv, _, _ := adoptiumServer(t, archive, "")
	defer srv.Close()
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "java"), 0o755); err != nil {
		t.Fatal(err)
	}
	lock := lockPath(base, 21)
	if err := os.WriteFile(lock, []byte("999 dead\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}

	if _, err := EnsureWith(context.Background(), testConfig(srv), 21, base, nil); err != nil {
		t.Fatalf("EnsureWith() con lock huérfano error = %v", err)
	}
}

func TestEnsureRejectsNoAssets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()

	_, err := EnsureWith(context.Background(), testConfig(srv), 21, t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "no JRE") {
		t.Fatalf("EnsureWith() error = %v, quiero el aviso de asset vacío", err)
	}
}

func TestEnsureRejectsInvalidMajor(t *testing.T) {
	if _, err := EnsureWith(context.Background(), Config{}, 0, t.TempDir(), nil); err == nil {
		t.Fatal("EnsureWith() aceptó una major inválida")
	}
}

func TestMapOSAndArch(t *testing.T) {
	oses := map[string]string{"windows": "windows", "linux": "linux", "darwin": "mac"}
	for in, want := range oses {
		if got, err := mapOS(in); err != nil || got != want {
			t.Fatalf("mapOS(%q) = %q, %v; quiero %q", in, got, err, want)
		}
	}
	if _, err := mapOS("plan9"); err == nil {
		t.Fatal("mapOS(plan9) no falló")
	}
	arches := map[string]string{"amd64": "x64", "arm64": "aarch64", "386": "x32"}
	for in, want := range arches {
		if got, err := mapArch(in); err != nil || got != want {
			t.Fatalf("mapArch(%q) = %q, %v; quiero %q", in, got, err, want)
		}
	}
	if _, err := mapArch("mips"); err == nil {
		t.Fatal("mapArch(mips) no falló")
	}
}

func TestDirAndJavaPath(t *testing.T) {
	dir := Dir("/data", 17)
	if filepath.Base(dir) != "temurin-17" {
		t.Fatalf("Dir() = %q, quiero terminar en temurin-17", dir)
	}
	if p := JavaPath(dir); filepath.Base(filepath.Dir(p)) != "bin" {
		t.Fatalf("JavaPath() = %q, quiero .../bin/java", p)
	}
}
