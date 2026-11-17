// doctor_panel_test.go: tests for the doctor panel wiring.
package main

import (
	"testing"
	"time"

	"github.com/JorMath/mc-tui-server/internal/config"
)

// waitDocIdle waits until the panel background work finishes.
func waitDocIdle(t *testing.T, a *app) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for a.docBusy.Get() {
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting for the doctor panel")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestDoctorPanelRunsAndReruns uses an instance without a Minecraft version:
// the Java check then skips the real java lookup, so the test stays hermetic.
func TestDoctorPanelRunsAndReruns(t *testing.T) {
	inst := config.Instance{Name: "alpha", Dir: t.TempDir(), Version: ""}
	a := newTestApp(t, inst)

	a.docOpenPanel()
	waitDocIdle(t, a)

	if !a.docOpen.Get() {
		t.Fatal("the doctor panel did not open")
	}
	checks := a.docChecks.Get()
	if len(checks) == 0 {
		t.Fatal("the doctor panel ran no checks")
	}
	if checks[0].Name != "Java" || !checks[0].OK {
		t.Fatalf("first check = %+v, want the unknown-version Java check", checks[0])
	}

	a.docScrollBy(2)
	if got := a.docScroll.Get(); got != 2 {
		t.Fatalf("scroll offset = %d, want 2", got)
	}

	a.docRunChecks()
	waitDocIdle(t, a)
	if got := a.docScroll.Get(); got != 0 {
		t.Fatalf("re-running must reset the scroll offset, got %d", got)
	}
	if len(a.docChecks.Get()) != len(checks) {
		t.Fatalf("re-run returned %d checks, want %d", len(a.docChecks.Get()), len(checks))
	}

	a.docClose()
	if a.docOpen.Get() || a.docName.Get() != "" || len(a.docChecks.Get()) != 0 {
		t.Fatal("closing the panel must reset its state")
	}
}

// TestDoctorPanelReportsAGoneInstance keeps the panel from running checks for
// an instance that no longer exists in the registry.
func TestDoctorPanelReportsAGoneInstance(t *testing.T) {
	a := newTestApp(t, config.Instance{Name: "alpha", Dir: t.TempDir()})
	a.docName.Set("ghost")

	a.docRunChecks()

	if a.docOpen.Get() {
		t.Fatal("the panel must stay closed for a gone instance")
	}
	logs := a.logs.Get()["ghost"]
	if len(logs) == 0 || logs[len(logs)-1] != "[mc-tui] Doctor: the instance is gone" {
		t.Fatalf("logs = %v, want the gone-instance notice", logs)
	}
}
