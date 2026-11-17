package doctor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JorMath/mc-tui-server/internal/backup"
	"github.com/JorMath/mc-tui-server/internal/config"
)

// writeFile creates path with parent directories and the given content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating parent of %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// setMtime pins the modification time of path.
func setMtime(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("setting mtime of %s: %v", path, err)
	}
}

// freePort returns a port that was just released.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listenerPort(t, ln)
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return port
}

// listenerPort returns the port a test listener is bound to.
func listenerPort(t *testing.T, ln net.Listener) int {
	t.Helper()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %T is not TCP", ln.Addr())
	}
	return addr.Port
}

// checkNamed returns the check with the given name, failing when it is absent.
func checkNamed(t *testing.T, checks []Check, name string) Check {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %q check in %+v", name, checks)
	return Check{}
}

// javaOK is the usual injected Java lookup, answering with one version.
func javaOK(want int) func(context.Context, string) (int, error) {
	return func(context.Context, string) (int, error) { return want, nil }
}

var fixedNow = time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)

func TestRunAllChecksPass(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)
	writeFile(t, filepath.Join(dir, "eula.txt"), "eula=true\n")
	writeFile(t, filepath.Join(dir, "server.jar"), "jar")
	writeFile(t, filepath.Join(dir, "world", "level.dat"), "level")
	writeFile(t, filepath.Join(dir, "server.properties"),
		fmt.Sprintf("level-name=world\nserver-port=%d\n", port))
	backupPath := filepath.Join(dir, backup.Dir, "world-20260101-000000.zip")
	writeFile(t, backupPath, "zip")
	setMtime(t, backupPath, fixedNow.Add(-time.Hour))

	inst := config.Instance{Name: "alpha", Dir: dir, JarPath: "server.jar", Version: "1.21.4"}
	checks := Run(context.Background(), inst, []config.Instance{inst}, Options{
		JavaVersion: javaOK(21),
		Now:         func() time.Time { return fixedNow },
	})

	wantNames := []string{"Java", "EULA", "Server files", "World", "Port", "Backup"}
	if len(checks) != len(wantNames) {
		t.Fatalf("Run returned %d checks, want %d", len(checks), len(wantNames))
	}
	for i, name := range wantNames {
		if checks[i].Name != name {
			t.Fatalf("check %d is %q, want %q", i, checks[i].Name, name)
		}
		if !checks[i].OK {
			t.Fatalf("%s failed: %s", name, checks[i].Detail)
		}
		if checks[i].Detail == "" {
			t.Fatalf("%s has no detail", name)
		}
	}
}

func TestJavaTooOldUsesSystemJava(t *testing.T) {
	inst := config.Instance{Name: "alpha", Dir: t.TempDir(), Version: "1.21.4"}
	got := checkJava(context.Background(), inst, javaOK(17))
	if got.OK {
		t.Fatal("checkJava accepted Java 17 for Minecraft 1.21.4")
	}
	for _, want := range []string{"17", "21", "system Java from PATH"} {
		if !strings.Contains(got.Detail, want) {
			t.Fatalf("detail %q does not mention %q", got.Detail, want)
		}
	}
}

func TestJavaTooOldUsesTheConfiguredRuntime(t *testing.T) {
	javaPath := filepath.Join("opt", "jdk8", "bin", "java")
	inst := config.Instance{Name: "alpha", Dir: t.TempDir(), JavaPath: javaPath, Version: "1.20.4"}
	got := checkJava(context.Background(), inst, javaOK(8))
	if got.OK {
		t.Fatal("checkJava accepted Java 8 for Minecraft 1.20.4")
	}
	if !strings.Contains(got.Detail, javaPath) {
		t.Fatalf("detail %q does not name the runtime %q", got.Detail, javaPath)
	}
}

func TestJavaLookupError(t *testing.T) {
	inst := config.Instance{Name: "alpha", Dir: t.TempDir(), Version: "1.21.4"}
	got := checkJava(context.Background(), inst, func(context.Context, string) (int, error) {
		return 0, errors.New("java is not installed")
	})
	if got.OK {
		t.Fatal("checkJava passed after a lookup error")
	}
	if !strings.Contains(got.Detail, "java is not installed") {
		t.Fatalf("detail %q does not carry the lookup error", got.Detail)
	}
}

func TestJavaUnrecognizedVersion(t *testing.T) {
	inst := config.Instance{Name: "alpha", Dir: t.TempDir(), Version: "dev"}
	got := checkJava(context.Background(), inst, func(context.Context, string) (int, error) {
		t.Fatal("the Java lookup must not run for an unrecognized version")
		return 0, nil
	})
	if !got.OK {
		t.Fatalf("checkJava failed an unrecognized version: %s", got.Detail)
	}
	const want = "Minecraft version not recognized; the Java requirement was not checked"
	if got.Detail != want {
		t.Fatalf("detail = %q, want %q", got.Detail, want)
	}
}

func TestRunUsesRealImplementationsByDefault(t *testing.T) {
	// An unrecognized version keeps the real Java lookup from running, so the
	// zero Options still exercises the default assignment.
	inst := config.Instance{Name: "alpha", Dir: t.TempDir(), Version: "dev"}
	checks := Run(context.Background(), inst, nil, Options{})
	if len(checks) != 6 {
		t.Fatalf("Run returned %d checks, want 6", len(checks))
	}
	if !checkNamed(t, checks, "Java").OK {
		t.Fatal("the Java check failed for an unrecognized version")
	}
}

func TestEULA(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantOK  bool
	}{
		{name: "missing", content: "", wantOK: false},
		{name: "rejected", content: "eula=false\n", wantOK: false},
		{name: "accepted", content: "eula=true\n", wantOK: true},
		{name: "accepted upper case", content: "eula=TRUE\n", wantOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.content != "" {
				writeFile(t, filepath.Join(dir, "eula.txt"), tc.content)
			}
			got := checkEULA(config.Instance{Name: "alpha", Dir: dir})
			if got.OK != tc.wantOK {
				t.Fatalf("checkEULA OK = %v, want %v (%s)", got.OK, tc.wantOK, got.Detail)
			}
			if !tc.wantOK && got.Detail != "eula.txt does not accept the Minecraft EULA" {
				t.Fatalf("detail = %q", got.Detail)
			}
		})
	}
}

func TestEULAUnreadable(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "eula.txt"), 0o755); err != nil {
		t.Fatalf("creating eula.txt folder: %v", err)
	}
	got := checkEULA(config.Instance{Name: "alpha", Dir: dir})
	if got.OK {
		t.Fatal("checkEULA passed an unreadable eula.txt")
	}
	if !strings.Contains(got.Detail, "could not be read") {
		t.Fatalf("detail = %q, want a read error", got.Detail)
	}
}

func TestServerFiles(t *testing.T) {
	argsName := argsFileFor(runtime.GOOS)
	cases := []struct {
		name    string
		inst    func(dir string) config.Instance
		wantOK  bool
		wantHas string
	}{
		{
			name: "args file present",
			inst: func(dir string) config.Instance {
				writeFile(t, filepath.Join(dir, "libraries", argsName), "args")
				return config.Instance{Name: "alpha", Dir: dir, ArgsDir: "libraries"}
			},
			wantOK: true,
		},
		{
			name: "args file missing",
			inst: func(dir string) config.Instance {
				return config.Instance{Name: "alpha", Dir: dir, ArgsDir: "libraries"}
			},
			wantHas: argsName,
		},
		{
			name: "jar present",
			inst: func(dir string) config.Instance {
				writeFile(t, filepath.Join(dir, "server.jar"), "jar")
				return config.Instance{Name: "alpha", Dir: dir, JarPath: "server.jar"}
			},
			wantOK: true,
		},
		{
			name: "jar missing",
			inst: func(dir string) config.Instance {
				return config.Instance{Name: "alpha", Dir: dir, JarPath: "paper.jar"}
			},
			wantHas: "paper.jar",
		},
		{
			name: "nothing configured",
			inst: func(dir string) config.Instance {
				return config.Instance{Name: "alpha", Dir: dir}
			},
			wantHas: "No server jar",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			got := checkServerFiles(tc.inst(dir))
			if got.Name != "Server files" {
				t.Fatalf("name = %q", got.Name)
			}
			if got.OK != tc.wantOK {
				t.Fatalf("OK = %v, want %v (%s)", got.OK, tc.wantOK, got.Detail)
			}
			if tc.wantHas != "" && !strings.Contains(got.Detail, tc.wantHas) {
				t.Fatalf("detail %q does not mention %q", got.Detail, tc.wantHas)
			}
		})
	}
}

func TestWorld(t *testing.T) {
	cases := []struct {
		name   string
		layout func(t *testing.T, dir string)
		wantOK bool
		wantIn string
	}{
		{
			name: "default world ready",
			layout: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "world", "level.dat"), "level")
			},
			wantOK: true,
		},
		{
			name: "custom level name",
			layout: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "server.properties"), "level-name=survival\n")
				writeFile(t, filepath.Join(dir, "survival", "level.dat"), "level")
			},
			wantOK: true,
			wantIn: "survival",
		},
		{
			name:   "world missing",
			layout: func(t *testing.T, dir string) {},
			wantIn: "world folder world is missing",
		},
		{
			name: "level.dat missing",
			layout: func(t *testing.T, dir string) {
				if err := os.MkdirAll(filepath.Join(dir, "world"), 0o755); err != nil {
					t.Fatalf("creating world folder: %v", err)
				}
			},
			wantIn: "no level.dat",
		},
		{
			name: "world path is a file",
			layout: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "world"), "not a folder")
			},
			wantIn: "is missing",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.layout(t, dir)
			got := checkWorld(config.Instance{Name: "alpha", Dir: dir})
			if got.OK != tc.wantOK {
				t.Fatalf("OK = %v, want %v (%s)", got.OK, tc.wantOK, got.Detail)
			}
			if tc.wantIn != "" && !strings.Contains(got.Detail, tc.wantIn) {
				t.Fatalf("detail %q does not mention %q", got.Detail, tc.wantIn)
			}
		})
	}
}

func TestPortInvalidRange(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "server.properties"), "server-port=70000\n")
	got := checkPort(config.Instance{Name: "alpha", Dir: dir}, nil)
	if got.OK {
		t.Fatal("checkPort accepted an out-of-range port")
	}
	if !strings.HasPrefix(got.Detail, "Port 70000") || !strings.HasSuffix(got.Detail, ".") {
		t.Fatalf("detail = %q, want a capitalized sentence", got.Detail)
	}
}

func TestPortReservedByAnotherInstance(t *testing.T) {
	alphaDir := t.TempDir()
	betaDir := t.TempDir()
	port := freePort(t)
	writeFile(t, filepath.Join(alphaDir, "server.properties"), fmt.Sprintf("server-port=%d\n", port))
	writeFile(t, filepath.Join(betaDir, "server.properties"), fmt.Sprintf("server-port=%d\n", port))
	alpha := config.Instance{Name: "alpha", Dir: alphaDir}
	beta := config.Instance{Name: "beta", Dir: betaDir}

	got := checkPort(alpha, []config.Instance{beta, alpha})
	if got.OK {
		t.Fatal("checkPort accepted a port reserved by another instance")
	}
	if !strings.Contains(got.Detail, "beta") {
		t.Fatalf("detail %q does not name the other instance", got.Detail)
	}
	if !strings.HasPrefix(got.Detail, "Port ") {
		t.Fatalf("detail = %q, want a capitalized sentence", got.Detail)
	}
}

func TestPortBusy(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	dir := t.TempDir()
	port := listenerPort(t, ln)
	writeFile(t, filepath.Join(dir, "server.properties"), fmt.Sprintf("server-port=%d\n", port))

	got := checkPort(config.Instance{Name: "alpha", Dir: dir}, nil)
	if got.OK {
		t.Fatal("checkPort accepted a busy port")
	}
	if !strings.Contains(got.Detail, strconv.Itoa(port)) {
		t.Fatalf("detail %q does not name port %d", got.Detail, port)
	}
}

func TestPortAvailable(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)
	writeFile(t, filepath.Join(dir, "server.properties"), fmt.Sprintf("server-port=%d\n", port))
	got := checkPort(config.Instance{Name: "alpha", Dir: dir}, nil)
	if !got.OK {
		t.Fatalf("checkPort failed a free port: %s", got.Detail)
	}
	if !strings.Contains(got.Detail, strconv.Itoa(port)) {
		t.Fatalf("detail %q does not name port %d", got.Detail, port)
	}
}

func TestBackupStates(t *testing.T) {
	cases := []struct {
		name   string
		age    time.Duration
		wantOK bool
		wantIn string
	}{
		{name: "recent", age: time.Hour, wantOK: true, wantIn: "0 days old"},
		{name: "stale", age: 10 * 24 * time.Hour, wantIn: "10 days old"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, backup.Dir, "world-20260101-000000.zip")
			writeFile(t, path, "zip")
			setMtime(t, path, fixedNow.Add(-tc.age))

			got := checkBackup(config.Instance{Name: "alpha", Dir: dir}, func() time.Time { return fixedNow })
			if got.OK != tc.wantOK {
				t.Fatalf("OK = %v, want %v (%s)", got.OK, tc.wantOK, got.Detail)
			}
			if !strings.Contains(got.Detail, tc.wantIn) {
				t.Fatalf("detail %q does not mention %q", got.Detail, tc.wantIn)
			}
		})
	}
}

func TestBackupMissing(t *testing.T) {
	got := checkBackup(config.Instance{Name: "alpha", Dir: t.TempDir()}, time.Now)
	if got.OK {
		t.Fatal("checkBackup passed without any backups")
	}
	if got.Detail != "No backups yet" {
		t.Fatalf("detail = %q", got.Detail)
	}
}

func TestBackupPicksTheNewestByModTime(t *testing.T) {
	dir := t.TempDir()
	// The alphabetically first name is the newest by modification time.
	fresh := filepath.Join(dir, backup.Dir, "world-20260101-000000.zip")
	stale := filepath.Join(dir, backup.Dir, "world-20260102-000000.zip")
	writeFile(t, fresh, "zip")
	writeFile(t, stale, "zip")
	setMtime(t, fresh, fixedNow.Add(-time.Hour))
	setMtime(t, stale, fixedNow.Add(-30*24*time.Hour))

	got := checkBackup(config.Instance{Name: "alpha", Dir: dir}, func() time.Time { return fixedNow })
	if !got.OK {
		t.Fatalf("checkBackup used the stale backup: %s", got.Detail)
	}
	if !strings.Contains(got.Detail, "0 days old") {
		t.Fatalf("detail = %q, want the fresh backup age", got.Detail)
	}
}

func TestBackupListError(t *testing.T) {
	// A NUL byte is rejected by the OS on every platform with an error that
	// is not os.ErrNotExist, which is the only portable way to make the
	// backups listing fail: a regular file at that path reports ErrNotExist.
	got := checkBackup(config.Instance{Name: "alpha", Dir: "\x00"}, time.Now)
	if got.OK {
		t.Fatal("checkBackup passed an unreadable backups folder")
	}
	if !strings.Contains(got.Detail, "could not be read") {
		t.Fatalf("detail = %q, want a read error", got.Detail)
	}
}

func TestModTimeMissingFile(t *testing.T) {
	if got := modTime(filepath.Join(t.TempDir(), "missing.zip")); !got.IsZero() {
		t.Fatalf("modTime(missing) = %v, want the zero time", got)
	}
}

func TestCapitalize(t *testing.T) {
	cases := map[string]string{"": "", "port 1 is busy": "Port 1 is busy", "Port": "Port"}
	for in, want := range cases {
		if got := capitalize(in); got != want {
			t.Fatalf("capitalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestArgsFileFor(t *testing.T) {
	if got := argsFileFor("windows"); got != "win_args.txt" {
		t.Fatalf("argsFileFor(windows) = %q", got)
	}
	if got := argsFileFor("linux"); got != "unix_args.txt" {
		t.Fatalf("argsFileFor(linux) = %q", got)
	}
}
