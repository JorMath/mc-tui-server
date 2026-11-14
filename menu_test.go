package main

import (
	"path/filepath"
	"testing"

	"github.com/JorMath/mc-tui-server/internal/config"
)

// menuIndex returns the position of the item with the given label.
func menuIndex(t *testing.T, a *app, label string) int {
	t.Helper()
	for i, it := range a.menuItems() {
		if it.Label == label {
			return i
		}
	}
	t.Fatalf("no menu item labeled %q", label)
	return -1
}

// TestMenuItemsForStoppedInstance covers the enabled/disabled matrix and the
// section order for a server that is not running.
func TestMenuItemsForStoppedInstance(t *testing.T) {
	a, _, _ := newJavaApp(t)

	items := a.menuItems()
	if len(items) == 0 {
		t.Fatal("the menu is empty with a selected instance")
	}

	byLabel := make(map[string]menuItem, len(items))
	sections := make([]string, 0, len(items))
	prev := ""
	for _, it := range items {
		if it.Label == "" {
			t.Errorf("item in section %q has an empty label", it.Section)
		}
		if it.Desc == "" {
			t.Errorf("item %q has an empty description", it.Label)
		}
		if it.Section != prev {
			sections = append(sections, it.Section)
			prev = it.Section
		}
		byLabel[it.Label] = it
	}

	wantSections := []string{"Lifecycle", "Content", "Maintenance", "Instance"}
	if len(sections) != len(wantSections) {
		t.Fatalf("sections = %v, want %v", sections, wantSections)
	}
	for i, want := range wantSections {
		if sections[i] != want {
			t.Fatalf("sections = %v, want %v", sections, wantSections)
		}
	}

	for _, label := range []string{
		"Start", "Backup world", "Update server software", "Java runtime",
		"Clone", "Rename", "Memory", "Delete", "Scheduler", "Files",
		"Modrinth", "Players", "Restart", "Auto-restart off",
	} {
		it, ok := byLabel[label]
		if !ok {
			t.Fatalf("missing menu item %q", label)
		}
		if it.Disabled {
			t.Errorf("%q should be enabled for a stopped instance (reason %q)", label, it.Reason)
		}
	}

	for _, label := range []string{"Stop", "Send command"} {
		it, ok := byLabel[label]
		if !ok {
			t.Fatalf("missing menu item %q", label)
		}
		if !it.Disabled {
			t.Errorf("%q should be disabled for a stopped instance", label)
		}
		if it.Reason == "" {
			t.Errorf("%q should explain why it is disabled", label)
		}
	}
}

// TestMenuChooseDisabledKeepsMenuOpen protects the read-the-reason path:
// Enter on a disabled item shows the message and does not run anything.
func TestMenuChooseDisabledKeepsMenuOpen(t *testing.T) {
	a, _, _ := newJavaApp(t)
	a.openMenu()
	a.menuIdx.Set(menuIndex(t, a, "Stop"))

	a.menuChoose()

	if !a.menuOpen.Get() {
		t.Fatal("the menu should stay open after choosing a disabled item")
	}
	if a.menuMsg.Get() == "" {
		t.Fatal("the menu should show the reason")
	}
}

// TestMenuChooseOpensRename checks that an enabled item closes the menu
// before running its action.
func TestMenuChooseOpensRename(t *testing.T) {
	a, _, _ := newJavaApp(t)
	a.openMenu()
	a.menuIdx.Set(menuIndex(t, a, "Rename"))

	a.menuChoose()

	if !a.renActive.Get() {
		t.Fatal("Rename should open the rename editor")
	}
	if a.menuOpen.Get() {
		t.Fatal("the menu should close before the action runs")
	}
}

// TestMenuMoveClamps keeps the selection inside the menu at both ends.
func TestMenuMoveClamps(t *testing.T) {
	a, _, _ := newJavaApp(t)
	a.openMenu()

	a.menuMove(-1)
	if got := a.menuIdx.Get(); got != 0 {
		t.Fatalf("index after moving up from 0 = %d, want 0", got)
	}

	n := len(a.menuItems())
	a.menuMove(n + 5)
	if got := a.menuIdx.Get(); got != n-1 {
		t.Fatalf("index after moving past the end = %d, want %d", got, n-1)
	}

	a.menuMove(-1000)
	if got := a.menuIdx.Get(); got != 0 {
		t.Fatalf("index after moving far up = %d, want 0", got)
	}
	a.menuMove(1)
	if got := a.menuIdx.Get(); got != 1 {
		t.Fatalf("index after a single step = %d, want 1", got)
	}
}

// TestOpenMenuWithoutInstances: Enter on an empty instance list does nothing.
func TestOpenMenuWithoutInstances(t *testing.T) {
	store := config.NewStore(filepath.Join(t.TempDir(), "instances.json"))
	a := App(store, nil)

	a.openMenu()

	if a.menuOpen.Get() {
		t.Fatal("the menu must not open without a selected instance")
	}
	if items := a.menuItems(); items != nil {
		t.Fatalf("menuItems without instances = %v, want nil", items)
	}
}

// TestMenuScrollY covers the heading accounting and the top clamp.
func TestMenuScrollY(t *testing.T) {
	a, _, _ := newJavaApp(t)
	a.openMenu()

	if got := a.menuScrollY(); got != 0 {
		t.Fatalf("scroll at the first item = %d, want 0", got)
	}

	// First Content item: 5 rows plus 2 headings minus the 4 context lines.
	a.menuIdx.Set(menuIndex(t, a, "Files"))
	if got := a.menuScrollY(); got != 3 {
		t.Fatalf("scroll at the first Content item = %d, want 3", got)
	}

	a.menuIdx.Set(len(a.menuItems()) - 1)
	if got := a.menuScrollY(); got <= 0 {
		t.Fatalf("scroll at the last item = %d, want > 0", got)
	}
}
