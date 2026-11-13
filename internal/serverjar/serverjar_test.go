package serverjar

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JorMath/mc-tui-server/internal/config"
)

// validJar escribe un archivo con la firma ZIP/JAR mínima.
func validJar(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte{'P', 'K', 3, 4, 0, 0, 0, 0}, 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPath(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "abs.jar")
	cases := []struct {
		name string
		inst config.Instance
		want string
	}{
		{"empty", config.Instance{}, ""},
		{"relative", config.Instance{Dir: dir, JarPath: "server.jar"}, filepath.Join(dir, "server.jar")},
		{"absolute", config.Instance{Dir: dir, JarPath: abs}, abs},
	}
	for _, c := range cases {
		if got := Path(c.inst); got != c.want {
			t.Errorf("%s: Path() = %q, quiero %q", c.name, got, c.want)
		}
	}
}

func TestLatest(t *testing.T) {
	cases := []struct {
		name     string
		versions []string
		current  string
		want     string
		ok       bool
	}{
		{"no versions", nil, "1.20.1", "", false},
		{"unknown current", []string{"1.21.7", "1.21.6"}, "", "1.21.7", true},
		{"newer available", []string{"1.21.7", "1.21.6"}, "1.21.6", "1.21.7", true},
		{"up to date", []string{"1.21.7"}, "1.21.7", "1.21.7", false},
		{"older installed", []string{"1.21.7"}, "26.2", "1.21.7", false},
	}
	for _, c := range cases {
		got, ok := Latest(c.versions, c.current)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: Latest() = (%q, %v), quiero (%q, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestBackupName(t *testing.T) {
	cases := []struct {
		jar, version, want string
	}{
		{"server.jar", "1.21.7", "server.jar.bak-1.21.7"},
		{"server.jar", "", "server.jar.bak"},
		{"server.jar", "1.21 rc/1", "server.jar.bak-1.21_rc_1"},
	}
	for _, c := range cases {
		if got := BackupName(c.jar, c.version); got != c.want {
			t.Errorf("BackupName(%q, %q) = %q, quiero %q", c.jar, c.version, got, c.want)
		}
	}
}

func TestStageAcceptsJar(t *testing.T) {
	dir := t.TempDir()
	jar := filepath.Join(dir, "server.jar")
	write := func(url, dest string, progress func(done, total int64)) error {
		if progress != nil {
			progress(8, 8)
		}
		return os.WriteFile(dest, []byte{'P', 'K', 3, 4, 1, 2, 3, 4}, 0o644)
	}
	staged, err := Stage(jar, "https://example.test/server.jar", write, func(done, total int64) {
		if done != 8 || total != 8 {
			t.Fatalf("progress = (%d, %d), quiero (8, 8)", done, total)
		}
	})
	if err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	if staged != jar+".new" {
		t.Fatalf("Stage() = %q, quiero %q", staged, jar+".new")
	}
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("el staging no existe: %v", err)
	}
}

func TestStageDownloadErrorLeavesNoFile(t *testing.T) {
	dir := t.TempDir()
	jar := filepath.Join(dir, "server.jar")
	wantErr := errors.New("network down")
	_, err := Stage(jar, "x", func(string, string, func(int64, int64)) error {
		return wantErr
	}, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Stage() error = %v, quiero %v", err, wantErr)
	}
	if _, statErr := os.Stat(jar + ".new"); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("el staging quedó en disco tras el error")
	}
}

func TestStageRejectsNonJar(t *testing.T) {
	dir := t.TempDir()
	jar := filepath.Join(dir, "server.jar")
	dl := func(url, dest string, progress func(done, total int64)) error {
		return os.WriteFile(dest, []byte("not a jar"), 0o644)
	}
	_, err := Stage(jar, "x", dl, nil)
	if err == nil || !strings.Contains(err.Error(), "not a valid jar") {
		t.Fatalf("Stage() error = %v, quiero un error de jar inválido", err)
	}
	if _, statErr := os.Stat(jar + ".new"); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("el staging quedó en disco tras el error")
	}
}

func TestValidateErrors(t *testing.T) {
	dir := t.TempDir()
	short := filepath.Join(dir, "short.jar")
	if err := os.WriteFile(short, []byte{'P', 'K'}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validate(short); err == nil {
		t.Fatal("validate() aceptó un archivo truncado")
	}
	if err := validate(filepath.Join(dir, "missing.jar")); err == nil {
		t.Fatal("validate() aceptó un archivo inexistente")
	}
	zipFile := filepath.Join(dir, "other.zip")
	if err := os.WriteFile(zipFile, []byte("RAR!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validate(zipFile); err == nil || !strings.Contains(err.Error(), "PK signature") {
		t.Fatalf("validate() error = %v, quiero el error de firma", err)
	}
}

func TestCommitReplacesAndBacksUp(t *testing.T) {
	dir := t.TempDir()
	jar := filepath.Join(dir, "server.jar")
	if err := os.WriteFile(jar, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	staged := jar + ".new"
	validJar(t, staged)

	backup, err := Commit(jar, staged, "1.21.6")
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if want := jar + ".bak-1.21.6"; backup != want {
		t.Fatalf("Commit() backup = %q, quiero %q", backup, want)
	}
	if got := read(t, backup); got != "old" {
		t.Fatalf("el backup = %q, quiero el jar viejo", got)
	}
	if got := read(t, jar); got != string([]byte{'P', 'K', 3, 4, 0, 0, 0, 0}) {
		t.Fatalf("el jar instalado no es el nuevo")
	}
	if _, statErr := os.Stat(staged); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("el staging quedó en disco tras Commit")
	}
}

func TestCommitFirstInstall(t *testing.T) {
	dir := t.TempDir()
	jar := filepath.Join(dir, "server.jar")
	staged := jar + ".new"
	validJar(t, staged)
	backup, err := Commit(jar, staged, "")
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if backup != "" {
		t.Fatalf("Commit() backup = %q, quiero vacío en la primera instalación", backup)
	}
	if _, err := os.Stat(jar); err != nil {
		t.Fatalf("el jar no se instaló: %v", err)
	}
}

func TestCommitRestoresWhenInstallFails(t *testing.T) {
	dir := t.TempDir()
	jar := filepath.Join(dir, "server.jar")
	if err := os.WriteFile(jar, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Commit(jar, jar+".missing", "1.21.6")
	if err == nil {
		t.Fatal("Commit() aceptó un staged inexistente")
	}
	if got := read(t, jar); got != "old" {
		t.Fatalf("el jar quedó en %q, quiero el anterior restaurado", got)
	}
}

func TestCommitBackupRenameError(t *testing.T) {
	dir := t.TempDir()
	jar := filepath.Join(dir, "server.jar")
	if err := os.WriteFile(jar, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	blocker := BackupName(jar, "1.21.6")
	if err := os.MkdirAll(blocker, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocker, "keep"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Commit(jar, jar+".new", "1.21.6"); err == nil {
		t.Fatal("Commit() aceptó un backup bloqueado por un directorio")
	}
	if got := read(t, jar); got != "old" {
		t.Fatalf("el jar quedó en %q, quiero el anterior intacto", got)
	}
}

func TestDiscard(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "staged")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	Discard(path)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Discard() no borró el archivo")
	}
	Discard("")
}
