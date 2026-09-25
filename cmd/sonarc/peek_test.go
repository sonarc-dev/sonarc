package main

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestPeekShowsTheDefinitionWithoutMoving(t *testing.T) {
	util := "#include \"util.h\"\n\nstatic long compute_total(int base,\n\t\t\t  long extra)\n{\n\treturn base;\n}\n"
	h := project(t, map[string]string{"main.c": mainC, "util.c": util}, "main.c")
	h.putCursorOn(t, "compute_total")
	before := h.app.v().Head

	h.key(tcell.KeyCtrlK)
	h.typeText("v")
	h.settle()
	if h.curFile() != "main.c" || h.app.v().Head != before {
		t.Fatal("peek moved the cursor")
	}
	want := "util.c:3  static long compute_total(int base, long extra)"
	if h.app.ui.Msg != want {
		t.Errorf("message %q\n     want %q", h.app.ui.Msg, want)
	}
}
