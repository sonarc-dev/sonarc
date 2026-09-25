package main

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestUndoRedoKeys(t *testing.T) {
	h := newHarness(t, "base")
	h.key(tcell.KeyEnd)
	h.typeText("+more")
	if got := h.text(); got != "base+more" {
		t.Fatalf("after typing = %q", got)
	}
	h.key(tcell.KeyCtrlZ)
	if got := h.text(); got != "base" {
		t.Errorf("after undo = %q, want %q", got, "base")
	}
	h.key(tcell.KeyCtrlY)
	if got := h.text(); got != "base+more" {
		t.Errorf("after redo = %q, want %q", got, "base+more")
	}
}

func TestSelectAllThenTypeReplaces(t *testing.T) {
	h := newHarness(t, "old content here")
	h.key(tcell.KeyCtrlA)
	h.typeText("new")
	if got := h.text(); got != "new" {
		t.Errorf("got %q, want %q", got, "new")
	}
}

func TestCutAndPaste(t *testing.T) {
	h := newHarness(t, "hello world")
	// Select "hello" with Shift+Right five times.
	for i := 0; i < 5; i++ {
		h.key(tcell.KeyRight, tcell.ModShift)
	}
	h.key(tcell.KeyCtrlX)
	if got := h.text(); got != " world" {
		t.Fatalf("after cut = %q, want %q", got, " world")
	}
	h.key(tcell.KeyEnd)
	h.key(tcell.KeyCtrlV)
	if got := h.text(); got != " worldhello" {
		t.Errorf("after paste = %q, want %q", got, " worldhello")
	}
}

// A pasted block must land as literal text, not as a stream of keystrokes that
// would each trigger auto-indent and produce a staircase.
func TestBracketedPasteDoesNotReIndent(t *testing.T) {
	h := newHarness(t, "")
	h.typeText("    ") // an indented starting line
	h.app.handle(tcell.NewEventPaste(true))
	for _, r := range "if (x) {" {
		h.app.handle(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	h.app.handle(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	for _, r := range "body();" {
		h.app.handle(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	h.app.handle(tcell.NewEventPaste(false))

	want := "    if (x) {\nbody();"
	if got := h.text(); got != want {
		t.Errorf("paste was re-indented:\n got %q\nwant %q", got, want)
	}
}

func TestEnterKeepsIndent(t *testing.T) {
	h := newHarness(t, "")
	h.typeText("        indented")
	h.key(tcell.KeyEnter)
	h.typeText("next")
	want := "        indented\n        next"
	if got := h.text(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Ctrl+D deleting the line is documented in the README; it was once lost when
// the key map moved between files, and nothing noticed.
func TestCtrlDDeletesTheLine(t *testing.T) {
	h := newHarness(t, "one\ntwo\nthree\n")
	h.app.v().Goto(2)
	h.key(tcell.KeyCtrlD)
	if h.text() != "one\nthree" {
		t.Errorf("after Ctrl+D: %q", h.text())
	}
}
