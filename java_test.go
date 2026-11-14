package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JorMath/mc-tui-server/internal/config"
	"github.com/JorMath/mc-tui-server/internal/javaenv"
	"github.com/JorMath/mc-tui-server/internal/server"
)

// newJavaApp crea una app con una instancia Paper 1.21.7 y su Manager.
func newJavaApp(t *testing.T) (*app, config.Instance, *server.Manager) {
	t.Helper()
	dir := t.TempDir()
	store := config.NewStore(filepath.Join(dir, "instances.json"))
	inst := config.Instance{
		Name:     "alpha",
		Dir:      filepath.Join(dir, "alpha"),
		JarPath:  "server.jar",
		Type:     config.Paper,
		Version:  "1.21.7",
		JavaPath: "C:\\old\\java.exe",
	}
	if err := store.Add(inst); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	mgr := server.New(inst)
	a := App(store, []*server.Manager{mgr})
	return a, inst, mgr
}

// TestJavaSetPersistsMode fija el contrato del modal de Java: el modo y la ruta
// se guardan en el Store, en disco y en el Manager de la instancia.
func TestJavaSetPersistsMode(t *testing.T) {
	a, inst, mgr := newJavaApp(t)
	a.javaTarget = inst.Name
	a.javaVersion = inst.Version

	path := javaenv.JavaPath(filepath.Join(a.dataDir, "java", "temurin-21"))
	a.javaSet("managed", path)

	got, ok := a.store.Get(inst.Name)
	if !ok {
		t.Fatal("the instance disappeared from the store")
	}
	if got.JavaMode != "managed" || got.JavaPath != path {
		t.Fatalf("mode/path = %q/%q, want managed/%q", got.JavaMode, got.JavaPath, path)
	}
	if mgr.Instance().JavaMode != "managed" || mgr.Instance().JavaPath != path {
		t.Fatalf("manager not updated: %+v", mgr.Instance())
	}

	reloaded := config.NewStore(a.store.Path())
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	persisted, ok := reloaded.Get(inst.Name)
	if !ok {
		t.Fatal("the instance was not saved to disk")
	}
	if persisted.JavaMode != "managed" || persisted.JavaPath != path {
		t.Fatalf("persisted mode/path = %q/%q, want managed/%q",
			persisted.JavaMode, persisted.JavaPath, path)
	}
	if a.javaOpen.Get() {
		t.Fatal("the modal should close after applying")
	}
}

// TestWizardSkipsJavaForUnknownVersion cubre javacheck.Required == 0: sin
// versión reconocida no se pide Java ni se descarga nada.
func TestWizardSkipsJavaForUnknownVersion(t *testing.T) {
	a, _, _ := newJavaApp(t)
	a.wizTypeIdx.Set(1) // paper
	a.wizVersions.Set([]string{"snapshot-unknown"})
	a.wizVerIdx.Set(0)

	a.wizJavaEnter(wizName)

	if a.wizStep.Get() != wizName {
		t.Fatalf("step = %v, want wizName", a.wizStep.Get())
	}
	if a.wizJavaMode.Get() != "" || a.wizJavaPath.Get() != "" {
		t.Fatalf("unexpected Java selection %q/%q", a.wizJavaMode.Get(), a.wizJavaPath.Get())
	}
}

// TestWizardReusesInstalledRuntime evita descargas repetidas: si el Temurin
// de la major ya está en disco, Enter lo elige y sigue sin tocar la red.
func TestWizardReusesInstalledRuntime(t *testing.T) {
	a, _, _ := newJavaApp(t)
	need := 17
	bin := javaenv.JavaPath(filepath.Join(a.dataDir, "java", "temurin-17"))
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}

	a.wizJavaNeed = need
	a.wizJavaNext = wizName
	a.wizJavaIdx.Set(0)
	a.wizStep.Set(wizJava)
	a.wizJavaProvision()

	if a.wizJavaMode.Get() != "managed" || a.wizJavaPath.Get() != bin {
		t.Fatalf("mode/path = %q/%q, want managed/%q",
			a.wizJavaMode.Get(), a.wizJavaPath.Get(), bin)
	}
	if a.wizStep.Get() != wizName {
		t.Fatalf("step = %v, want wizName", a.wizStep.Get())
	}
	if a.wizJavaBusy.Get() {
		t.Fatal("reusing an installed runtime must not start a download")
	}
}

// TestWizardChoosesSystemJava acepta el Java del PATH cuando alcanza.
func TestWizardChoosesSystemJava(t *testing.T) {
	a, _, _ := newJavaApp(t)
	a.wizJavaNeed = 21
	a.wizJavaNext = wizName
	a.wizJavaSysMajor.Set(21)
	a.wizJavaIdx.Set(1)
	a.wizStep.Set(wizJava)

	a.wizJavaChoose()

	if a.wizJavaMode.Get() != "system" || a.wizJavaPath.Get() != "" {
		t.Fatalf("mode/path = %q/%q, want system/\"\"", a.wizJavaMode.Get(), a.wizJavaPath.Get())
	}
	if a.wizStep.Get() != wizName {
		t.Fatalf("step = %v, want wizName", a.wizStep.Get())
	}
}

// TestWizardRejectsOldSystemJava no deja avanzar con un Java insuficiente.
func TestWizardRejectsOldSystemJava(t *testing.T) {
	a, _, _ := newJavaApp(t)
	a.wizJavaNeed = 21
	a.wizJavaNext = wizName
	a.wizJavaSysMajor.Set(17)
	a.wizJavaIdx.Set(1)
	a.wizStep.Set(wizJava)

	a.wizJavaChoose()

	if a.wizStep.Get() != wizJava {
		t.Fatalf("step = %v, want to stay in wizJava", a.wizStep.Get())
	}
	if a.wizJavaMode.Get() != "" {
		t.Fatalf("mode = %q, want no selection", a.wizJavaMode.Get())
	}
	if a.wizJavaMsg.Get() == "" {
		t.Fatal("the user should get an explanation")
	}
}

// TestWizardManualRequiresPath valida el submit manual sin ejecutar java.
func TestWizardManualRequiresPath(t *testing.T) {
	a, _, _ := newJavaApp(t)
	a.wizJavaNext = wizName
	a.wizJavaTyping.Set(true)
	a.wizJavaPath.Set("   ")
	a.wizStep.Set(wizJava)

	a.wizJavaManualSubmit()

	if a.wizStep.Get() != wizJava {
		t.Fatalf("step = %v, want to stay in wizJava", a.wizStep.Get())
	}
	if a.wizJavaMsg.Get() == "" {
		t.Fatal("the user should get an explanation")
	}
}

// TestJavaCancelRunClearsBusy deja el modal utilizable tras cancelar.
func TestJavaCancelRunClearsBusy(t *testing.T) {
	a, _, _ := newJavaApp(t)
	a.javaBusy.Set(true)
	a.javaMsg.Set("Downloading...")

	a.javaCancelRun()

	if a.javaBusy.Get() {
		t.Fatal("cancel must clear the busy flag")
	}
	if a.javaMsg.Get() != "Download cancelled" {
		t.Fatalf("message = %q", a.javaMsg.Get())
	}
}

// TestJavaItemsDescribeCurrentChoice comprueba las etiquetas del modal.
func TestJavaItemsDescribeCurrentChoice(t *testing.T) {
	a, inst, _ := newJavaApp(t)
	a.javaTarget = inst.Name
	a.javaVersion = inst.Version
	a.javaSysLabel.Set("Java 21")
	a.javaManLabel.Set("installed (C:\\java)")
	a.javaIdx.Set(2)
	a.javaPath.Set("C:\\manual\\java.exe")

	items := a.javaItems()
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	wants := []string{
		"System Java on PATH — Java 21",
		"Managed Temurin JRE 21 — installed (C:\\java)",
		"Manual path — C:\\manual\\java.exe",
	}
	for i, want := range wants {
		if items[i].Text != want {
			t.Errorf("item %d = %q, want %q", i, items[i].Text, want)
		}
		if items[i].Sel != (i == 2) {
			t.Errorf("item %d selected = %v", i, items[i].Sel)
		}
	}
}
