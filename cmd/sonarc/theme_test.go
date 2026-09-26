package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sonarc-dev/sonarc/internal/term"

	"github.com/gdamore/tcell/v2"
)

// A picked theme is applied at once and comes back next session; the
// sidebar width saved alongside it survives the write.
func TestSelectedThemeIsAppliedAndRemembered(t *testing.T) {
	h := sidebarHarness(t, map[string]string{"m.c": ""}, "m.c")
	path := saving(t, h)
	h.sim.SetSize(100, 30)
	h.app.scr.Caps.Colors = 256
	h.app.ui.ResizeSidebar(30)

	before := h.app.scr.Theme
	h.app.cmdSelectTheme()
	h.typeText("gruvbox")
	h.key(tcell.KeyEnter)
	if h.app.scr.Theme == before {
		t.Fatal("theme did not change")
	}
	if got := readState(t, path); got != `{"sidebar_width":30,"theme":"gruvbox"}` {
		t.Errorf("state file = %q", got)
	}

	h2 := sidebarHarness(t, map[string]string{"m.c": ""}, "m.c")
	h2.app.scr.Caps.Colors = 256
	h2.app.statePath = path
	h2.app.loadState()
	if h2.app.themeName != "gruvbox" || h2.app.scr.Theme == before {
		t.Errorf("theme after restart: %q", h2.app.themeName)
	}
}

func TestEveryThemeIsDistinct(t *testing.T) {
	seen := map[term.Theme]string{}
	for _, n := range term.ThemeNames() {
		th, ok := term.NamedTheme(n, term.Caps{Colors: 256})
		if !ok {
			t.Fatalf("theme %q listed but missing", n)
		}
		if other, dup := seen[th]; dup {
			t.Errorf("themes %s and %s are identical", n, other)
		}
		seen[th] = n
		if th.GitAdded == th.GitDeleted || th.GitAdded == th.Text {
			t.Errorf("theme %s: git colors are indistinguishable", n)
		}
	}
	if _, ok := term.NamedTheme("nope", term.Caps{Colors: 256}); ok {
		t.Error("an unknown theme was accepted")
	}
}

// State saved under the editor's former name moves over once, and a newer
// sonarc directory is never overwritten by it.
func TestConfigMovesFromFormerName(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "keditor", "sessions")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keditor", "state.json"), []byte(`{"theme":"light"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	migrateConfig(dir)
	if data, err := os.ReadFile(filepath.Join(dir, "sonarc", "state.json")); err != nil || string(data) != `{"theme":"light"}` {
		t.Fatalf("state.json after migration = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sonarc", "sessions")); err != nil {
		t.Errorf("sessions did not move: %v", err)
	}

	// Once sonarc exists, a stray keditor directory is left alone.
	if err := os.MkdirAll(filepath.Join(dir, "keditor"), 0o755); err != nil {
		t.Fatal(err)
	}
	migrateConfig(dir)
	if _, err := os.Stat(filepath.Join(dir, "keditor")); err != nil {
		t.Errorf("keditor directory was touched although sonarc existed: %v", err)
	}
}
