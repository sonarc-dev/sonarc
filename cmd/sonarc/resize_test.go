package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// Dragging the separator moves the edge to the pointer, the text follows it,
// and the width is saved once the button is let go, not on every step.
func TestDraggingTheSidebarEdgeResizesIt(t *testing.T) {
	h := sidebarHarness(t, map[string]string{"a/x.c": "", "m.c": "hello world\n"}, "m.c")
	path := saving(t, h)
	edge := h.app.ui.TextX() - 1

	h.mouse(edge, 5, tcell.Button1)
	h.mouse(edge+8, 5, tcell.Button1)
	h.draw()
	if got := readState(t, path); got != "" {
		t.Errorf("saved %q mid-drag; the width is only final on release", got)
	}
	h.mouse(edge+15, 5, tcell.Button1)
	h.mouse(edge+15, 5, tcell.ButtonNone)
	h.draw()

	if got, want := h.app.ui.TextX(), edge+16; got != want {
		t.Errorf("text starts at column %d, want %d (the separator is under the pointer)", got, want)
	}
	if got := readState(t, path); got != `{"sidebar_width":40}` {
		t.Errorf("state file = %q", got)
	}

	// Clicks in the text still land on the right byte after the move.
	h.click(h.app.ui.TextX()+h.app.ui.GutterWidth()+6, 0)
	if c := h.app.v().Head; c.Line != 0 || c.Col != 6 {
		t.Errorf("click after resize put the cursor at %+v, want line 0 col 6", c)
	}
	// Pressing on the edge must not open or toggle a tree entry.
	if h.screenHas("x.c") {
		t.Error("a drag on the edge expanded a directory")
	}
}

// The edge cannot be dragged so far that either side becomes useless.
func TestSidebarResizeIsBounded(t *testing.T) {
	h := sidebarHarness(t, map[string]string{"m.c": ""}, "m.c")
	w, _ := h.sim.Size()
	edge := h.app.ui.TextX() - 1

	h.mouse(edge, 3, tcell.Button1)
	h.mouse(0, 3, tcell.Button1)
	h.draw()
	if got := h.app.ui.TextX(); got != 12 {
		t.Errorf("dragged to the far left: sidebar is %d wide, want the 12-column minimum", got)
	}
	h.mouse(w-1, 3, tcell.Button1)
	h.mouse(w-1, 3, tcell.ButtonNone)
	h.draw()
	if got := w - h.app.ui.TextX(); got != 20 {
		t.Errorf("dragged to the far right: text keeps %d columns, want 20", got)
	}
}

// < and > resize from the keyboard while the tree has focus, and only then:
// in the text they are characters to type.
func TestSidebarResizeKeys(t *testing.T) {
	h := sidebarHarness(t, map[string]string{"m.c": ""}, "m.c")
	path := saving(t, h)
	start := h.app.ui.TextX()

	h.typeText("<")
	h.draw()
	if h.app.ui.TextX() != start {
		t.Fatal("< resized the tree while the text had focus")
	}
	if !strings.Contains(string(h.app.v().Buf.Bytes()), "<") {
		t.Error("< was not typed into the file")
	}

	h.key(tcell.KeyCtrlE) // focus the tree
	h.typeText(">>>")
	h.draw()
	if got := h.app.ui.TextX(); got != start+6 {
		t.Errorf("three > presses: sidebar %d wide, want %d", got, start+6)
	}
	h.typeText("<")
	h.draw()
	if got := h.app.ui.TextX(); got != start+4 {
		t.Errorf("then <: sidebar %d wide, want %d", got, start+4)
	}
	if got, want := readState(t, path), `{"sidebar_width":29}`; got != want {
		t.Errorf("state file = %q, want %q", got, want)
	}
}

// A saved width comes back next session; a damaged or silly file is ignored
// rather than stopping the editor or collapsing the tree.
func TestSavedSidebarWidthIsRestored(t *testing.T) {
	for _, tc := range []struct {
		name, file string
		want       int
	}{
		{"saved", `{"sidebar_width":40}`, 40},
		{"too narrow", `{"sidebar_width":1}`, 12},
		{"damaged", `{"sidebar_wid`, 25},
		{"missing", "", 25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := sidebarHarness(t, map[string]string{"m.c": ""}, "m.c")
			path := saving(t, h)
			if tc.file != "" {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tc.file), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			h.app.loadState()
			h.draw()
			if got := h.app.ui.TextX(); got != tc.want {
				t.Errorf("sidebar is %d wide, want %d", got, tc.want)
			}
		})
	}
}
