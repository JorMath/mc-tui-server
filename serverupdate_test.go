package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JorMath/mc-tui-server/internal/config"
	"github.com/JorMath/mc-tui-server/internal/download"
	"github.com/JorMath/mc-tui-server/internal/server"
)

// fakeProvider evita la red en los tests de applyServerUpdate.
type fakeProvider struct {
	versions    []string
	versionsErr error
	url         string
	resolveErr  error
}

func (f fakeProvider) Name() string { return "fake" }
func (f fakeProvider) Versions(context.Context) ([]string, error) {
	return f.versions, f.versionsErr
}
func (f fakeProvider) ResolveJarURL(context.Context, string) (string, error) {
	if f.resolveErr != nil {
		return "", f.resolveErr
	}
	return f.url, nil
}

// jarWrite devuelve un DownloadFunc que escribe un jar mínimo válido.
func jarWrite(content []byte) func(string, string, func(int64, int64)) error {
	return func(_, dest string, progress func(done, total int64)) error {
		if progress != nil {
			progress(int64(len(content)), int64(len(content)))
		}
		return os.WriteFile(dest, content, 0o644)
	}
}

func newJarInstance(t *testing.T) config.Instance {
	t.Helper()
	dir := t.TempDir()
	return config.Instance{Name: "test", Dir: dir, JarPath: "server.jar", Type: config.Paper, Version: "1.21.7"}
}

func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestJarUpdatable(t *testing.T) {
	cases := []struct {
		typ  config.ServerType
		want bool
	}{
		{config.Vanilla, true},
		{config.Paper, true},
		{config.Purpur, true},
		{config.Fabric, true},
		{config.Forge, false},
		{config.NeoForge, false},
		{config.Quilt, false},
		{config.ServerType("unknown"), false},
	}
	for _, c := range cases {
		if got := jarUpdatable(c.typ); got != c.want {
			t.Errorf("jarUpdatable(%q) = %v, quiero %v", c.typ, got, c.want)
		}
	}
}

func TestApplyServerUpdateBacksUpAndRotates(t *testing.T) {
	inst := newJarInstance(t)
	jarPath := filepath.Join(inst.Dir, "server.jar")
	writeFile(t, jarPath, []byte("old jar"))
	worldDir := filepath.Join(inst.Dir, "world")
	if err := os.MkdirAll(worldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(worldDir, "level.dat"), []byte("world"))

	var logs []string
	bak, err := applyServerUpdate(context.Background(), inst, "1.21.8", fakeProvider{url: "https://example.test/server.jar"},
		jarWrite([]byte{'P', 'K', 3, 4, 9, 9}), nil, func(msg string) { logs = append(logs, msg) })
	if err != nil {
		t.Fatalf("applyServerUpdate() error = %v", err)
	}
	if want := jarPath + ".bak-1.21.7"; bak != want {
		t.Fatalf("backup = %q, quiero %q", bak, want)
	}
	if got := readFile(t, bak); got != "old jar" {
		t.Fatalf("el backup contiene %q, quiero el jar anterior", got)
	}
	if got := readFile(t, jarPath); got != string([]byte{'P', 'K', 3, 4, 9, 9}) {
		t.Fatalf("el jar instalado no es el nuevo: %q", got)
	}
	if _, statErr := os.Stat(jarPath + ".new"); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("el staging quedó en disco tras la actualización")
	}
	backups, err := os.ReadDir(filepath.Join(inst.Dir, "backups"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups del mundo = %v (err %v), quiero 1", backups, err)
	}
	if len(logs) == 0 || !strings.Contains(logs[0], "World backed up") {
		t.Fatalf("logs = %v, quiero el aviso de backup", logs)
	}
}

func TestApplyServerUpdateWithoutWorld(t *testing.T) {
	inst := newJarInstance(t)
	jarPath := filepath.Join(inst.Dir, "server.jar")
	writeFile(t, jarPath, []byte("old jar"))
	if _, err := applyServerUpdate(context.Background(), inst, "1.21.8", fakeProvider{url: "x"},
		jarWrite([]byte{'P', 'K', 3, 4}), nil, nil); err != nil {
		t.Fatalf("applyServerUpdate() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(inst.Dir, "backups")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("se creó backups/ sin mundo que respaldar")
	}
}

func TestApplyServerUpdateDownloadErrorLeavesJar(t *testing.T) {
	inst := newJarInstance(t)
	jarPath := filepath.Join(inst.Dir, "server.jar")
	writeFile(t, jarPath, []byte("old jar"))
	wantErr := errors.New("network down")
	dl := func(string, string, func(int64, int64)) error { return wantErr }
	_, err := applyServerUpdate(context.Background(), inst, "1.21.8", fakeProvider{url: "x"}, dl, nil, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("applyServerUpdate() error = %v, quiero %v", err, wantErr)
	}
	if got := readFile(t, jarPath); got != "old jar" {
		t.Fatalf("el jar quedó en %q, quiero el anterior intacto", got)
	}
}

func TestApplyServerUpdateRejectsNonJar(t *testing.T) {
	inst := newJarInstance(t)
	jarPath := filepath.Join(inst.Dir, "server.jar")
	writeFile(t, jarPath, []byte("old jar"))
	_, err := applyServerUpdate(context.Background(), inst, "1.21.8", fakeProvider{url: "x"},
		jarWrite([]byte("this is not a jar")), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "not a valid jar") {
		t.Fatalf("applyServerUpdate() error = %v, quiero el error de jar inválido", err)
	}
	if got := readFile(t, jarPath); got != "old jar" {
		t.Fatalf("el jar quedó en %q, quiero el anterior intacto", got)
	}
	if _, statErr := os.Stat(jarPath + ".new"); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("el staging inválido quedó en disco")
	}
}

func TestApplyServerUpdateResolveError(t *testing.T) {
	inst := newJarInstance(t)
	writeFile(t, filepath.Join(inst.Dir, "server.jar"), []byte("old jar"))
	wantErr := errors.New("no build")
	_, err := applyServerUpdate(context.Background(), inst, "1.21.8",
		fakeProvider{resolveErr: wantErr}, jarWrite([]byte{'P', 'K', 3, 4}), nil, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("applyServerUpdate() error = %v, quiero %v", err, wantErr)
	}
}

func TestApplyServerUpdateWithoutJarPath(t *testing.T) {
	inst := config.Instance{Dir: t.TempDir()}
	_, err := applyServerUpdate(context.Background(), inst, "1.21.8", fakeProvider{url: "x"}, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "no server jar path") {
		t.Fatalf("applyServerUpdate() error = %v, quiero el aviso de jar ausente", err)
	}
}

func TestSuMoveClamps(t *testing.T) {
	a := App(config.NewStore(filepath.Join(t.TempDir(), "instances.json")), nil)
	a.suVersions.Set([]string{"1.21.8", "1.21.7", "1.21.6"})
	a.suIdx.Set(0)
	a.suMove(-1)
	if got := a.suIdx.Get(); got != 0 {
		t.Fatalf("suMove(-1) desde 0 = %d, quiero 0", got)
	}
	a.suMove(10)
	if got := a.suIdx.Get(); got != 2 {
		t.Fatalf("suMove(10) = %d, quiero 2", got)
	}
}

func TestSuAskApplyConfirmsAndWarnsDowngrade(t *testing.T) {
	a := App(config.NewStore(filepath.Join(t.TempDir(), "instances.json")), nil)
	a.suCurrent = "1.21.7"
	a.suVersions.Set([]string{"1.21.8", "1.21.6"})
	a.suIdx.Set(0)
	a.suAskApply()
	if !strings.Contains(a.suConfirm.Get(), "1.21.8") {
		t.Fatalf("confirmación = %q, quiero la versión elegida", a.suConfirm.Get())
	}
	a.suConfirm.Set("")
	a.suIdx.Set(1)
	a.suAskApply()
	if !strings.Contains(a.suConfirm.Get(), "downgrade") {
		t.Fatalf("confirmación = %q, quiero el aviso de downgrade", a.suConfirm.Get())
	}
}

func TestSuAskApplyRejectsSameVersion(t *testing.T) {
	a := App(config.NewStore(filepath.Join(t.TempDir(), "instances.json")), nil)
	a.suCurrent = "1.21.7"
	a.suVersions.Set([]string{"1.21.7"})
	a.suIdx.Set(0)
	a.suAskApply()
	if a.suConfirm.Get() != "" {
		t.Fatalf("se pidió confirmación para la versión ya instalada: %q", a.suConfirm.Get())
	}
	if !strings.Contains(a.suMsg.Get(), "Already on") {
		t.Fatalf("msg = %q, quiero el aviso de versión instalada", a.suMsg.Get())
	}
}

// waitSuIdle espera a que la goroutine del panel suelte suBusy.
func waitSuIdle(t *testing.T, a *app) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for a.suBusy.Get() {
		if time.Now().After(deadline) {
			t.Fatal("timeout esperando la respuesta del provider")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newTestApp(t *testing.T, inst config.Instance) *app {
	t.Helper()
	store := config.NewStore(filepath.Join(t.TempDir(), "instances.json"))
	if err := store.Add(inst); err != nil {
		t.Fatal(err)
	}
	mgr := server.New(inst)
	t.Cleanup(func() { _ = mgr.Close() })
	return App(store, []*server.Manager{mgr})
}

func TestUpdateAskOpensPanelForJarInstance(t *testing.T) {
	inst := config.Instance{Name: "paper-srv", Dir: t.TempDir(), JarPath: "server.jar", Type: config.Paper, Version: "1.21.7"}
	a := newTestApp(t, inst)
	a.newProvider = func(config.ServerType) (download.Provider, error) {
		return fakeProvider{versions: []string{"1.21.8", "1.21.7"}}, nil
	}
	a.updateAsk()
	waitSuIdle(t, a)
	if !a.suOpen.Get() {
		t.Fatal("el panel de actualización no se abrió")
	}
	if got := len(a.suVersions.Get()); got != 2 {
		t.Fatalf("versiones = %d, quiero 2", got)
	}
	if msg := a.suMsg.Get(); !strings.Contains(msg, "1.21.7 → 1.21.8") {
		t.Fatalf("msg = %q, quiero el aviso de actualización disponible", msg)
	}
	if got := a.suIdx.Get(); got != 0 {
		t.Fatalf("índice inicial = %d, quiero la versión más nueva (0)", got)
	}
}

func TestUpdateAskInstallerTypeIsUnsupported(t *testing.T) {
	inst := config.Instance{Name: "forge-srv", Dir: t.TempDir(), Type: config.Forge, Version: "1.21.7"}
	a := newTestApp(t, inst)
	a.updateAsk()
	if a.suOpen.Get() {
		t.Fatal("se abrió el panel para una instancia con installer")
	}
	logs := a.logs.Get()[inst.Name]
	if len(logs) == 0 || !strings.Contains(logs[len(logs)-1], "not supported yet") {
		t.Fatalf("logs = %v, quiero el aviso de no soportado", logs)
	}
}
