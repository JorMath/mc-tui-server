// Package javaenv provisions a managed Eclipse Temurin JRE per Java major
// version so an instance can run without a system-wide JDK, and resolves
// which java binary an instance should use.
package javaenv

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/JorMath/mc-tui-server/internal/download"
	"github.com/JorMath/mc-tui-server/internal/javacheck"
)

// defaultBaseURL es la API de Adoptium; los tests la sustituyen.
const defaultBaseURL = "https://api.adoptium.net"

// lockWait es el sondeo entre intentos de lock y staleLock la antigüedad a
// partir de la cual un lock huérfano se considera abandonado.
const (
	lockWait  = 250 * time.Millisecond
	staleLock = 15 * time.Minute
)

// Runtime es un JRE gestionado ya extraído en disco.
type Runtime struct {
	Major int
	// Path apunta al binario, no al directorio: <base>/java/temurin-21/bin/java.
	Path string
}

// DownloadFunc descarga url en dest reportando bytes. Es inyectable para
// que los tests no toquen la red.
type DownloadFunc func(ctx context.Context, url, dest string, progress func(done, total int64)) error

// Config controla de dónde se baja el runtime. El valor cero usa los
// defaults de producción (Adoptium, HTTP por defecto, GOOS/GOARCH reales).
type Config struct {
	BaseURL  string
	Client   *http.Client
	OS       string
	Arch     string
	Download DownloadFunc
}

func (c Config) withDefaults() Config {
	if c.BaseURL == "" {
		c.BaseURL = defaultBaseURL
	}
	if c.OS == "" {
		c.OS = runtime.GOOS
	}
	if c.Arch == "" {
		c.Arch = runtime.GOARCH
	}
	if c.Download == nil {
		client := c.Client
		c.Download = func(ctx context.Context, url, dest string, progress func(done, total int64)) error {
			return download.DownloadFile(ctx, client, url, dest, progress)
		}
	}
	return c
}

// Dir devuelve el directorio donde vive el runtime de una major concreta.
func Dir(baseDir string, major int) string {
	return filepath.Join(baseDir, "java", fmt.Sprintf("temurin-%d", major))
}

// JavaPath devuelve la ruta estable del binario dentro de dir.
func JavaPath(dir string) string {
	name := "java"
	if runtime.GOOS == "windows" {
		name = "java.exe"
	}
	return filepath.Join(dir, "bin", name)
}

// Installed indica si el runtime de esa major ya está completo en disco.
func Installed(baseDir string, major int) (Runtime, bool) {
	p := JavaPath(Dir(baseDir, major))
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return Runtime{}, false
	}
	return Runtime{Major: major, Path: p}, true
}

// Detect delega en javacheck: la detección vive en un solo sitio.
func Detect(ctx context.Context, javaPath string) (int, error) {
	return javacheck.Version(ctx, javaPath)
}

// Ensure descarga y extrae el JRE de la major pedida si no está ya
// instalado. Es idempotente y seguro entre procesos concurrentes.
func Ensure(ctx context.Context, major int, baseDir string, progress func(done, total int64)) (Runtime, error) {
	return EnsureWith(ctx, Config{}, major, baseDir, progress)
}

// EnsureWith es Ensure con configuración inyectable (tests).
func EnsureWith(ctx context.Context, cfg Config, major int, baseDir string, progress func(done, total int64)) (Runtime, error) {
	cfg = cfg.withDefaults()
	if major <= 0 {
		return Runtime{}, fmt.Errorf("invalid Java major version %d", major)
	}
	if baseDir == "" {
		return Runtime{}, errors.New("the Java base directory is empty")
	}
	if rt, ok := Installed(baseDir, major); ok {
		return rt, nil
	}
	javaDir := filepath.Join(baseDir, "java")
	if err := os.MkdirAll(javaDir, 0o755); err != nil {
		return Runtime{}, fmt.Errorf("creating the Java folder: %w", err)
	}

	release, err := acquireLock(ctx, lockPath(baseDir, major))
	if err != nil {
		// Otra instancia puede haber terminado mientras esperábamos.
		if rt, ok := Installed(baseDir, major); ok {
			return rt, nil
		}
		return Runtime{}, err
	}
	defer release()

	// Re-chequeo tras el lock: el proceso que lo tenía pudo instalarlo.
	if rt, ok := Installed(baseDir, major); ok {
		return rt, nil
	}

	osName, err := mapOS(cfg.OS)
	if err != nil {
		return Runtime{}, err
	}
	arch, err := mapArch(cfg.Arch)
	if err != nil {
		return Runtime{}, err
	}
	a, err := fetchAsset(ctx, cfg, major, osName, arch)
	if err != nil {
		return Runtime{}, err
	}

	tmp, err := os.MkdirTemp(javaDir, ".install-")
	if err != nil {
		return Runtime{}, fmt.Errorf("creating the staging folder: %w", err)
	}
	defer os.RemoveAll(tmp)

	link := a.Binary.Package.Link
	archive := filepath.Join(tmp, archiveName(link))
	if err := cfg.Download(ctx, link, archive, progress); err != nil {
		return Runtime{}, fmt.Errorf("downloading the Java runtime: %w", err)
	}
	if err := checkSHA256(archive, a.Checksum()); err != nil {
		return Runtime{}, err
	}
	raw := filepath.Join(tmp, "raw")
	if err := os.MkdirAll(raw, 0o755); err != nil {
		return Runtime{}, err
	}
	if err := extractArchive(archive, raw); err != nil {
		return Runtime{}, err
	}
	root, err := normalizeRoot(raw)
	if err != nil {
		return Runtime{}, err
	}
	final := Dir(baseDir, major)
	if err := os.Rename(root, final); err != nil {
		// Otra instancia ganó la carrera: reutiliza su runtime.
		if rt, ok := Installed(baseDir, major); ok {
			return rt, nil
		}
		return Runtime{}, fmt.Errorf("installing the Java runtime in %s: %w", final, err)
	}
	return Runtime{Major: major, Path: JavaPath(final)}, nil
}

// lockPath usa un lock por major para no serializar descargas distintas.
func lockPath(baseDir string, major int) string {
	return filepath.Join(baseDir, "java", fmt.Sprintf(".lock-%d", major))
}

// acquireLock crea el lock con O_CREATE|O_EXCL. Si ya existe espera hasta
// que desaparezca, salvo que esté huérfano (más viejo que staleLock).
func acquireLock(ctx context.Context, p string) (func(), error) {
	for {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d %s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
			_ = f.Close()
			return func() { _ = os.Remove(p) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("creating the Java lock: %w", err)
		}
		if info, statErr := os.Stat(p); statErr == nil && time.Since(info.ModTime()) > staleLock {
			_ = os.Remove(p)
			continue
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(lockWait):
		}
	}
}

// asset es el subconjunto de /v3/assets/latest que usamos.
type asset struct {
	ReleaseName string `json:"release_name"`
	Binary      struct {
		Package struct {
			Name     string `json:"name"`
			Link     string `json:"link"`
			Checksum string `json:"checksum"`
			Size     int64  `json:"size"`
		} `json:"package"`
	} `json:"binary"`
}

func (a asset) Checksum() string { return a.Binary.Package.Checksum }

func fetchAsset(ctx context.Context, cfg Config, major int, osName, arch string) (asset, error) {
	q := url.Values{}
	q.Set("os", osName)
	q.Set("architecture", arch)
	q.Set("image_type", "jre")
	q.Set("vendor", "eclipse")
	endpoint := fmt.Sprintf("%s/v3/assets/latest/%d/hotspot?%s",
		strings.TrimRight(cfg.BaseURL, "/"), major, q.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return asset{}, fmt.Errorf("creating the Adoptium request: %w", err)
	}
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return asset{}, fmt.Errorf("querying Adoptium: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return asset{}, fmt.Errorf("Adoptium answered %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var assets []asset
	if err := json.NewDecoder(resp.Body).Decode(&assets); err != nil {
		return asset{}, fmt.Errorf("decoding the Adoptium response: %w", err)
	}
	if len(assets) == 0 {
		return asset{}, fmt.Errorf("Adoptium has no JRE %d for %s/%s", major, osName, arch)
	}
	a := assets[0]
	if a.Binary.Package.Link == "" || a.Binary.Package.Checksum == "" {
		return asset{}, errors.New("the Adoptium asset has no download link or checksum")
	}
	return a, nil
}

func mapOS(goos string) (string, error) {
	switch goos {
	case "windows", "linux":
		return goos, nil
	case "darwin":
		return "mac", nil
	}
	return "", fmt.Errorf("Adoptium has no JRE builds for %s", goos)
}

func mapArch(goarch string) (string, error) {
	switch goarch {
	case "amd64":
		return "x64", nil
	case "arm64":
		return "aarch64", nil
	case "386":
		return "x32", nil
	}
	return "", fmt.Errorf("Adoptium has no JRE builds for %s", goarch)
}

// archiveName deriva el nombre local del asset; la extensión decide el
// extractor (zip en Windows, tar.gz en Linux/macOS).
func archiveName(link string) string {
	if u, err := url.Parse(link); err == nil && u.Path != "" {
		if base := path.Base(u.Path); base != "." && base != "/" {
			return base
		}
	}
	return "runtime.zip"
}

func checkSHA256(file, want string) error {
	if want == "" {
		return errors.New("the Adoptium asset has no sha256 checksum")
	}
	f, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("opening the downloaded runtime: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hashing the downloaded runtime: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(strings.TrimSpace(want), got) {
		return fmt.Errorf("the Java runtime checksum does not match (got %s, want %s)", got, want)
	}
	return nil
}

// extractArchive detecta el formato por magic bytes para no depender del
// nombre del asset.
func extractArchive(file, dest string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	magic := make([]byte, 4)
	n, _ := io.ReadFull(f, magic)
	_ = f.Close()
	magic = magic[:n]
	switch {
	case len(magic) >= 4 && magic[0] == 'P' && magic[1] == 'K':
		return extractZip(file, dest)
	case len(magic) >= 2 && magic[0] == 0x1f && magic[1] == 0x8b:
		return extractTarGz(file, dest)
	}
	return fmt.Errorf("unsupported runtime archive %q", filepath.Base(file))
}

// safeTarget sanea una entrada del archivo y rechaza rutas que escapen de
// dest (zip-slip).
func safeTarget(dest, name string) (string, error) {
	name = filepath.FromSlash(name)
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("archive entry %q uses an absolute path", name)
	}
	target := filepath.Join(dest, name)
	rel, err := filepath.Rel(dest, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry %q escapes the destination", name)
	}
	return target, nil
}

func within(dest, target string) bool {
	rel, err := filepath.Rel(dest, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func extractZip(file, dest string) error {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return fmt.Errorf("opening the runtime zip: %w", err)
	}
	defer zr.Close()
	for _, entry := range zr.File {
		target, err := safeTarget(dest, entry.Name)
		if err != nil {
			return err
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := entry.Open()
		if err != nil {
			return fmt.Errorf("reading %s from the runtime zip: %w", entry.Name, err)
		}
		mode := entry.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			_ = rc.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			_ = out.Close()
			_ = rc.Close()
			return fmt.Errorf("extracting %s: %w", entry.Name, err)
		}
		_ = rc.Close()
		if err := out.Close(); err != nil {
			return err
		}
	}
	return nil
}

func extractTarGz(file, dest string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("opening the runtime tarball: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("reading the runtime tarball: %w", err)
		}
		target, err := safeTarget(dest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(hdr.Mode).Perm()
			if mode == 0 {
				mode = 0o644
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return fmt.Errorf("extracting %s: %w", hdr.Name, err)
			}
			if err := out.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			link := filepath.Join(filepath.Dir(target), filepath.FromSlash(hdr.Linkname))
			if !within(dest, link) {
				return fmt.Errorf("archive entry %q links outside the destination", hdr.Name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			// En Windows sin privilegios el symlink falla; el runtime de
			// Windows llega como zip, así que se ignora sin romper.
			_ = os.Symlink(hdr.Linkname, target)
		case tar.TypeLink:
			link, err := safeTarget(dest, hdr.Linkname)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Link(link, target); err != nil {
				_ = os.Symlink(link, target)
			}
		}
	}
	return nil
}

// normalizeRoot encuentra el binario java dentro de lo extraído y devuelve
// el directorio que debe convertirse en Dir(baseDir, major). Soporta el
// layout plano (Windows/Linux) y el de macOS (Contents/Home).
func normalizeRoot(raw string) (string, error) {
	root := raw
	if entries, err := os.ReadDir(raw); err == nil && len(entries) == 1 && entries[0].IsDir() {
		root = filepath.Join(raw, entries[0].Name())
	}
	if hasBin(root) {
		return root, nil
	}
	home := filepath.Join(root, "Contents", "Home")
	if hasBin(home) {
		return home, nil
	}
	return "", fmt.Errorf("the downloaded runtime has no bin/java under %s", root)
}

func hasBin(dir string) bool {
	for _, name := range []string{"java", "java.exe"} {
		if info, err := os.Stat(filepath.Join(dir, "bin", name)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}
