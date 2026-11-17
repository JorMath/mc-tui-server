package main

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/JorMath/mc-tui-server/internal/config"
	"github.com/JorMath/mc-tui-server/internal/ports"
	"github.com/JorMath/mc-tui-server/internal/properties"
)

// freeTestPort returns a port that was just released and is expected to stay
// free for the rest of the test.
func freeTestPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return port
}

// writeProps stores a server.properties file in dir, creating it if needed.
func writeProps(t *testing.T, dir, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	path := filepath.Join(dir, "server.properties")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

func TestWizWritePortCreatesTheFile(t *testing.T) {
	dir := t.TempDir()
	if err := wizWritePort(dir, 25570); err != nil {
		t.Fatalf("wizWritePort: %v", err)
	}
	props, err := properties.Load(filepath.Join(dir, "server.properties"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if v, _ := props.Get("server-port"); v != "25570" {
		t.Fatalf("server-port = %q, want 25570", v)
	}
}

func TestWizWritePortKeepsAnExistingValueAndOtherKeys(t *testing.T) {
	dir := t.TempDir()
	path := writeProps(t, dir, "level-name=survival\nserver-port=25570\n")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	// The file already declares the chosen port: it must not be rewritten.
	if err := wizWritePort(dir, 25570); err != nil {
		t.Fatalf("wizWritePort: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if string(after) != string(before) {
		t.Fatalf("file changed when the port already matched:\n%s", after)
	}

	if err := wizWritePort(dir, 25571); err != nil {
		t.Fatalf("wizWritePort: %v", err)
	}
	props, err := properties.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if v, _ := props.Get("server-port"); v != "25571" {
		t.Fatalf("server-port = %q, want 25571", v)
	}
	if v, _ := props.Get("level-name"); v != "survival" {
		t.Fatalf("level-name = %q, want survival", v)
	}
}

func TestWizPortStepSuggestsAFreePortAndFallsBackToIt(t *testing.T) {
	a, _, _ := newJavaApp(t)
	a.wizEnterPort()

	if a.wizStep.Get() != wizPort {
		t.Fatalf("step = %v, want wizPort", a.wizStep.Get())
	}
	if a.wizPortDefault.Get() < 25565 {
		t.Fatalf("suggested port = %d, want at least 25565", a.wizPortDefault.Get())
	}
	if ports.InUse(a.wizPortDefault.Get()) {
		t.Fatalf("suggested port %d is in use", a.wizPortDefault.Get())
	}

	// Empty input falls back to the suggestion.
	a.wizPort.Set("")
	a.wizSubmitPort()
	if a.wizStep.Get() != wizEula {
		t.Fatalf("step = %v, want wizEula (message %q)", a.wizStep.Get(), a.wizMsg.Get())
	}
}

func TestWizSubmitPortRejectsReservedAndInvalidPorts(t *testing.T) {
	a, _, _ := newJavaApp(t)
	port := freeTestPort(t)
	betaDir := filepath.Join(t.TempDir(), "beta")
	writeProps(t, betaDir, "server-port="+strconv.Itoa(port)+"\n")
	beta := config.Instance{Name: "beta", Dir: betaDir}
	if err := a.store.Add(beta); err != nil {
		t.Fatalf("Add: %v", err)
	}

	a.wizEnterPort()
	a.wizPort.Set(strconv.Itoa(port))
	a.wizSubmitPort()
	if a.wizStep.Get() != wizPort {
		t.Fatalf("step = %v, want to stay in wizPort", a.wizStep.Get())
	}
	if !strings.Contains(a.wizMsg.Get(), "beta") {
		t.Fatalf("message %q does not name the reserving instance", a.wizMsg.Get())
	}

	a.wizPort.Set("99999")
	a.wizSubmitPort()
	if a.wizStep.Get() != wizPort || a.wizMsg.Get() == "" {
		t.Fatalf("out-of-range port: step = %v, message %q", a.wizStep.Get(), a.wizMsg.Get())
	}
}

func TestWizSuggestPortSkipsReservedPorts(t *testing.T) {
	a, _, _ := newJavaApp(t)
	first := a.wizSuggestPort()

	dir := filepath.Join(t.TempDir(), "delta")
	writeProps(t, dir, "server-port="+strconv.Itoa(first)+"\n")
	if err := a.store.Add(config.Instance{Name: "delta", Dir: dir}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if second := a.wizSuggestPort(); second == first {
		t.Fatalf("suggested %d again even though another instance reserves it", first)
	}
}

func TestInstancePortsAndPortText(t *testing.T) {
	a, inst, _ := newJavaApp(t)
	writeProps(t, inst.Dir, "server-port=25570\n")

	missing := filepath.Join(t.TempDir(), "missing")
	invalid := filepath.Join(t.TempDir(), "invalid")
	writeProps(t, invalid, "server-port=lots\n")
	instances := a.store.Instances()
	instances = append(instances,
		config.Instance{Name: "missing", Dir: missing},
		config.Instance{Name: "invalid", Dir: invalid},
	)

	cached := instancePorts(instances)
	if cached[inst.Name] != 25570 {
		t.Fatalf("cached %s = %d, want 25570", inst.Name, cached[inst.Name])
	}
	if cached["missing"] != 25565 {
		t.Fatalf("cached missing = %d, want the 25565 default", cached["missing"])
	}
	if _, ok := cached["invalid"]; ok {
		t.Fatalf("cached invalid = %v, want no entry", cached["invalid"])
	}

	a.ports.Set(cached)
	if got := a.portText(inst.Name); got != ":25570" {
		t.Fatalf("portText = %q, want :25570", got)
	}
	if got := a.portText("nobody"); got != "" {
		t.Fatalf("portText for an unknown instance = %q, want empty", got)
	}
}

func TestFmSaveValidatesAnEditedPort(t *testing.T) {
	a, inst, _ := newJavaApp(t)
	if err := os.MkdirAll(inst.Dir, 0o755); err != nil {
		t.Fatalf("creating %s: %v", inst.Dir, err)
	}
	path := filepath.Join(inst.Dir, "server.properties")

	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	busy := ln.Addr().(*net.TCPAddr).Port
	defer ln.Close()

	// A busy port that no other instance owns is rejected and nothing is written.
	a.fmProps = properties.Parse([]byte("server-port=" + strconv.Itoa(busy) + "\n"))
	a.fmPortEdited.Set(true)
	a.fmSave()
	if !strings.HasPrefix(a.fmMsg.Get(), "Error:") {
		t.Fatalf("message = %q, want a save error", a.fmMsg.Get())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("server.properties was written despite the error (stat err = %v)", err)
	}

	// A non-numeric value is rejected too.
	a.fmProps = properties.Parse([]byte("server-port=lots\n"))
	a.fmSave()
	if !strings.HasPrefix(a.fmMsg.Get(), "Error:") {
		t.Fatalf("message = %q, want a save error", a.fmMsg.Get())
	}

	// Editing another key keeps working even while the port stays busy.
	a.fmProps = properties.Parse([]byte("server-port=" + strconv.Itoa(busy) + "\nlevel-name=survival\n"))
	a.fmPortEdited.Set(false)
	a.fmSave()
	if !strings.HasPrefix(a.fmMsg.Get(), "Saved") {
		t.Fatalf("message = %q, want a saved confirmation", a.fmMsg.Get())
	}

	// Once the file declares the port itself, saving it again is allowed
	// (the running server binds its own port).
	a.fmPortEdited.Set(true)
	a.fmSave()
	if !strings.HasPrefix(a.fmMsg.Get(), "Saved") {
		t.Fatalf("message = %q, want the own port to be accepted", a.fmMsg.Get())
	}
	if got := a.portText(inst.Name); got != ":"+strconv.Itoa(busy) {
		t.Fatalf("portText = %q, want :%d after saving", got, busy)
	}
}
