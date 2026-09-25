package main

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// Jumping must be reversible: that is what makes exploring a codebase safe.
func TestJumpBackReturnsToTheCallSite(t *testing.T) {
	h := project(t, map[string]string{
		"main.c": mainC,
		"util.c": utilC,
		"util.h": utilH,
	}, "main.c")

	h.putCursorOn(t, "compute_total")
	before := h.app.v().Head
	beforeFile := h.curFile()

	h.app.cmdGotoDefinition()

	h.settle()
	if h.curFile() == beforeFile {
		t.Fatal("goto-definition did not change file")
	}

	h.app.cmdJumpBack()
	if got := h.curFile(); got != beforeFile {
		t.Errorf("jump back landed in %s, want %s", got, beforeFile)
	}
	if got := h.app.v().Head; got.Line != before.Line {
		t.Errorf("jump back landed on line %d, want %d", got.Line, before.Line)
	}
}

func TestJumpBackWithEmptyStackIsSafe(t *testing.T) {
	h := project(t, map[string]string{"main.c": mainC}, "main.c")
	h.app.cmdJumpBack()
	if h.app.ui.Msg == "" {
		t.Error("jumping back with no history should explain itself")
	}
}

// Browsing results must not fill the tag stack: only a deliberate jump should.
func TestBrowsingResultsDoesNotPolluteTheJumpStack(t *testing.T) {
	h := project(t, map[string]string{
		"main.c": mainC,
		"util.c": utilC,
		"util.h": utilH,
	}, "main.c")

	h.putCursorOn(t, "compute_total")
	h.app.cmdFindReferences()
	h.settle()
	depth := len(h.app.jumps)

	h.key(tcell.KeyF3)
	h.key(tcell.KeyF3)
	if len(h.app.jumps) != depth {
		t.Errorf("browsing pushed %d jumps; it should push none",
			len(h.app.jumps)-depth)
	}
}

// Back then forward returns to the definition; a fresh jump from the middle of
// history drops the old way forward, as a browser does.
func TestJumpForwardRetracesJumpBack(t *testing.T) {
	h := project(t, map[string]string{
		"main.c": mainC,
		"util.c": utilC,
		"util.h": utilH,
	}, "main.c")

	h.putCursorOn(t, "compute_total")
	h.app.cmdGotoDefinition()
	h.settle()
	defPos := h.app.v().Head

	h.app.cmdJumpBack()
	if h.curFile() != "main.c" {
		t.Fatalf("jump back went to %s", h.curFile())
	}
	h.key(tcell.KeyRight, tcell.ModAlt)
	if h.curFile() != "util.c" || h.app.v().Head != defPos {
		t.Fatalf("Alt+Right went to %s %+v, want util.c %+v", h.curFile(), h.app.v().Head, defPos)
	}
	h.app.cmdJumpForward()
	if !strings.Contains(h.app.ui.Msg, "no later position") {
		t.Errorf("forward at the end of history: message %q", h.app.ui.Msg)
	}

	// Back twice is not possible past the start; back once, then a new jump.
	h.app.cmdJumpBack()
	h.putCursorOn(t, "compute_total")
	h.app.cmdGotoDefinition()
	h.settle()
	if len(h.app.forward) != 0 {
		t.Errorf("a new jump kept %d forward entries", len(h.app.forward))
	}
}
