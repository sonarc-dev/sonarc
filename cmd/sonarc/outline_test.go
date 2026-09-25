package main

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestOutlineJumpsWithinTheFile(t *testing.T) {
	h := project(t, map[string]string{"m.c": mainC}, "m.c")
	h.putCursorOn(t, "compute_total") // inside main()
	h.key(tcell.KeyCtrlK)
	h.typeText("l")
	if !h.app.ui.Picker.Open {
		t.Fatalf("no outline picker; message %q", h.app.ui.Msg)
	}
	if it, _ := h.app.ui.Picker.Current(); it.Label != "main" {
		t.Errorf("outline starts on %q, want main, where the cursor is", it.Label)
	}
	h.app.cmdOutline()
	h.typeText("main")
	h.key(tcell.KeyEnter)
	if l := h.app.v().Buf.Line(h.app.v().Head.Line); !strings.Contains(string(l), "main") {
		t.Errorf("landed on %q", l)
	}
	h.app.cmdJumpBack() // an outline jump is navigation, so it can be undone
	if !strings.Contains(string(h.app.v().Buf.Line(h.app.v().Head.Line)), "compute_total") {
		t.Error("jump back after an outline jump did not return")
	}
}
