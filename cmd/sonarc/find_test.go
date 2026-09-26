package main

import (
	"github.com/sonarc-dev/sonarc/internal/buffer"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// Find-and-step must work end to end through the real key dispatch.
func TestFindAndStepThroughMatches(t *testing.T) {
	h := project(t, map[string]string{
		"main.c": "alpha\nbeta\nalpha\ngamma\nalpha\n",
	}, "main.c")

	// Drive the incremental prompt: type "alpha" then Enter.
	go func() {
		for _, r := range "alpha" {
			h.sim.InjectKey(tcell.KeyRune, r, tcell.ModNone)
		}
		h.sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	}()
	h.app.cmdFind()

	if h.app.matcher == nil {
		t.Fatal("search was not accepted")
	}
	if n := len(h.app.ui.Matches); n != 3 {
		t.Fatalf("highlighted %d matches, want 3", n)
	}
	if got := h.app.v().Head.Line; got != 0 {
		t.Errorf("first match on line %d, want 0", got)
	}

	// F3 steps forward through them and wraps.
	for _, want := range []int{2, 4, 0} {
		h.key(tcell.KeyF3)
		if got := h.app.v().Head.Line; got != want {
			t.Errorf("after F3 cursor on line %d, want %d", got, want)
		}
	}
}

// Abandoning a search must leave the cursor where it started.
func TestCancelledFindRestoresCursor(t *testing.T) {
	h := project(t, map[string]string{
		"main.c": "alpha\nbeta\nalpha\n",
	}, "main.c")
	h.app.v().SetCursor(buffer.Pos{Line: 1, Col: 2})
	before := h.app.v().Head

	go func() {
		for _, r := range "alpha" {
			h.sim.InjectKey(tcell.KeyRune, r, tcell.ModNone)
		}
		h.sim.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	}()
	h.app.cmdFind()

	if got := h.app.v().Head; got != before {
		t.Errorf("cancelled search left the cursor at %v, want %v", got, before)
	}
	if len(h.app.ui.Matches) != 0 {
		t.Error("cancelled search left highlighting behind")
	}
}
