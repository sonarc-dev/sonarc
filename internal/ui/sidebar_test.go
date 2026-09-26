package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/sonarc-dev/sonarc/internal/buffer"
	"github.com/sonarc-dev/sonarc/internal/filetree"
	"github.com/sonarc-dev/sonarc/internal/term"
	"github.com/sonarc-dev/sonarc/internal/view"
)

// newTestUI builds a UI backed by a simulation screen of the given size, so
// Layout can be exercised the same way the real editor drives it.
func newTestUI(t *testing.T, w, h int) *UI {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatalf("init simulation screen: %v", err)
	}
	sim.SetSize(w, h)
	t.Cleanup(sim.Fini)

	v := view.New(buffer.New())
	return New(term.Wrap(sim), v)
}

func treeAt(t *testing.T) *filetree.Tree {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return filetree.New(root)
}

func TestSidebarReducesViewWidthWhenShown(t *testing.T) {
	u := newTestUI(t, 120, 40)
	u.Layout()
	withoutSidebar := u.View.Width

	u.Sidebar.Tree = treeAt(t)
	u.Layout()
	withSidebar := u.View.Width

	if withSidebar >= withoutSidebar {
		t.Errorf("View.Width = %d with a sidebar, want less than %d (no sidebar)", withSidebar, withoutSidebar)
	}
	if got := u.TextX(); got != defaultSidebarWidth {
		t.Errorf("TextX() = %d, want %d", got, defaultSidebarWidth)
	}
}

func TestSidebarAutoHidesBelow90Columns(t *testing.T) {
	u := newTestUI(t, 80, 24)
	u.Sidebar.Tree = treeAt(t)
	u.Layout()

	if u.TextX() != 0 {
		t.Errorf("TextX() = %d at 80 columns, want 0 (auto-hidden)", u.TextX())
	}
}

func TestCtrlEEquivalentPinsSidebarVisibleAt80Columns(t *testing.T) {
	u := newTestUI(t, 80, 24)
	u.Sidebar.Tree = treeAt(t)
	u.Layout()
	if u.TextX() != 0 {
		t.Fatal("sidebar should start auto-hidden at 80 columns")
	}

	u.Sidebar.Toggle(80)
	u.Layout()
	if u.TextX() == 0 {
		t.Error("Toggle at 80 columns should pin the sidebar visible")
	}
	if !u.Sidebar.Focused {
		t.Error("Toggle should focus the sidebar when it turns it on")
	}
}

func TestTogglingOffReturnsColumnsToText(t *testing.T) {
	u := newTestUI(t, 120, 40)
	u.Sidebar.Tree = treeAt(t)
	u.Layout()
	if u.TextX() == 0 {
		t.Fatal("sidebar should be visible by default at 120 columns")
	}

	// Visible but unfocused: the first press focuses, the second hides.
	u.Sidebar.Toggle(120)
	u.Layout()
	if u.TextX() == 0 || !u.Sidebar.Focused {
		t.Fatalf("first Toggle should focus the visible sidebar; TextX=%d Focused=%v", u.TextX(), u.Sidebar.Focused)
	}
	u.Sidebar.Toggle(120)
	u.Layout()
	if u.TextX() != 0 {
		t.Errorf("TextX() = %d after toggling off, want 0", u.TextX())
	}
	if u.Sidebar.Focused {
		t.Error("toggling off should clear focus")
	}
}

func TestSidebarHiddenWithoutATree(t *testing.T) {
	u := newTestUI(t, 120, 40)
	u.Layout()
	if u.TextX() != 0 {
		t.Errorf("TextX() = %d with no tree set, want 0", u.TextX())
	}
}
