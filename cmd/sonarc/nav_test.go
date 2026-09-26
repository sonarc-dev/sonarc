package main

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/sonarc-dev/sonarc/internal/buffer"
	"github.com/sonarc-dev/sonarc/internal/index/provider"
)

// The headline feature: put the cursor on a call and jump to the definition,
// across files.
func TestGotoDefinitionJumpsAcrossFiles(t *testing.T) {
	h := project(t, map[string]string{
		"main.c": mainC,
		"util.c": utilC,
		"util.h": utilH,
	}, "main.c")

	h.putCursorOn(t, "compute_total")
	h.app.cmdGotoDefinition()
	h.settle()

	if h.app.ui.Picker.Open {
		t.Fatalf("expected a single definition, got a picker with %d items",
			h.app.ui.Picker.Count())
	}
	if got := h.curFile(); got != "util.c" {
		t.Fatalf("jumped to %s, want util.c (the definition, not the prototype)", got)
	}
	if line := h.app.v().Head.Line + 1; line != 3 {
		t.Errorf("landed on line %d, want 3", line)
	}
}

// With several definitions the editor must ask rather than guess. Guessing
// wrong in C is common and silently lands you in the wrong file.
func TestAmbiguousDefinitionOffersAPicker(t *testing.T) {
	h := project(t, map[string]string{
		"a.c":    "int shared_name(void)\n{\n\treturn 1;\n}\n",
		"b.c":    "int shared_name(void)\n{\n\treturn 2;\n}\n",
		"main.c": "int main(void)\n{\n\treturn shared_name();\n}\n",
	}, "main.c")

	h.putCursorOn(t, "shared_name")
	h.app.cmdGotoDefinition()
	h.settle()

	if !h.app.ui.Picker.Open {
		t.Fatal("two definitions should have opened a picker")
	}
	if n := h.app.ui.Picker.Count(); n != 2 {
		t.Errorf("picker shows %d items, want 2", n)
	}
	// The picker must render, and choosing must navigate.
	h.app.ui.Draw()
	h.app.handle(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if h.app.ui.Picker.Open {
		t.Error("picker stayed open after Enter")
	}
	if f := h.curFile(); f != "a.c" && f != "b.c" {
		t.Errorf("picker selection opened %s", f)
	}
}

func TestGotoDefinitionWithoutSymbolExplains(t *testing.T) {
	h := project(t, map[string]string{"main.c": "\n\nint main(void) { return 0; }\n"}, "main.c")
	h.app.v().SetCursor(buffer.Pos{Line: 0, Col: 0}) // a blank line
	h.app.cmdGotoDefinition()
	h.settle()
	if !strings.Contains(h.app.ui.Msg, "symbol") {
		t.Errorf("message = %q, want a hint about placing the cursor", h.app.ui.Msg)
	}
}

func TestUnknownSymbolReportsWhichIndexesWereUsed(t *testing.T) {
	h := project(t, map[string]string{"main.c": "int main(void)\n{\n\tno_such_thing();\n}\n"}, "main.c")
	h.putCursorOn(t, "no_such_thing")
	h.app.cmdGotoDefinition()
	h.settle()

	if !strings.Contains(h.app.ui.Msg, "no definition") {
		t.Errorf("message = %q, want it to say no definition was found", h.app.ui.Msg)
	}
	// Saying which index answered is what makes a miss diagnosable.
	if !strings.Contains(h.app.ui.Msg, "builtin") {
		t.Errorf("message = %q, want it to name the index consulted", h.app.ui.Msg)
	}
}

// Find-references fills the results panel, which must render and be navigable.
func TestFindReferencesPopulatesThePanel(t *testing.T) {
	h := project(t, map[string]string{
		"main.c": mainC,
		"util.c": utilC,
		"util.h": utilH,
	}, "main.c")

	h.putCursorOn(t, "compute_total")
	h.app.cmdFindReferences()
	h.settle()

	p := &h.app.ui.Panel
	if !p.Open {
		t.Fatal("results panel did not open")
	}
	// main.c call, util.c definition, util.c recursive call, util.h prototype.
	if len(p.Results) != 4 {
		t.Errorf("got %d references, want 4: %+v", len(p.Results), p.Results)
	}

	// The panel must actually be drawn.
	screen := strings.Join(h.draw(), "\n")
	if !strings.Contains(screen, "references to compute_total") {
		t.Errorf("panel title not rendered; screen:\n%s", screen)
	}
	if !strings.Contains(screen, "util.c") {
		t.Errorf("panel does not list util.c; screen:\n%s", screen)
	}
}

// Walking results with F3/F2 is how a call graph is explored, so moving must
// follow the selection into other files.
func TestResultNavigationFollowsSelection(t *testing.T) {
	h := project(t, map[string]string{
		"main.c": mainC,
		"util.c": utilC,
		"util.h": utilH,
	}, "main.c")

	h.putCursorOn(t, "compute_total")
	h.app.cmdFindReferences()
	h.settle()

	seen := map[string]bool{h.curFile(): true}
	for i := 0; i < len(h.app.ui.Panel.Results)-1; i++ {
		h.key(tcell.KeyF3)
		seen[h.curFile()] = true
	}
	if len(seen) < 2 {
		t.Errorf("walking results never left %v; it should follow across files", seen)
	}

	// Escape closes the panel and returns the rows to the text area.
	before := h.app.v().Height
	h.key(tcell.KeyEscape)
	if h.app.ui.Panel.Open {
		t.Error("Escape did not close the results panel")
	}
	h.draw()
	if h.app.v().Height <= before {
		t.Error("closing the panel did not give its rows back to the text area")
	}
}

// Callers and callees need cscope; without it the editor must say so plainly
// rather than reporting "no results" and leaving the user guessing.
func TestCallGraphWithoutCscopeExplainsWhy(t *testing.T) {
	h := project(t, map[string]string{"main.c": mainC}, "main.c")
	h.putCursorOn(t, "compute_total")

	h.app.cmdFindCallers()

	h.settle()
	if !strings.Contains(h.app.ui.Msg, "cscope") {
		t.Errorf("message = %q, want it to mention cscope", h.app.ui.Msg)
	}
	h.app.cmdFindCallees()
	h.settle()
	if !strings.Contains(h.app.ui.Msg, "cscope") {
		t.Errorf("message = %q, want it to mention cscope", h.app.ui.Msg)
	}
}

func TestSymbolUnderCursor(t *testing.T) {
	h := project(t, map[string]string{"main.c": "int compute_total(int x);\n"}, "main.c")

	tests := []struct {
		col  int
		want string
	}{
		{0, "int"},
		{4, "compute_total"},
		{10, "compute_total"},
		{17, "compute_total"}, // just past the end still resolves the word
		{18, "int"},
	}
	for _, tt := range tests {
		h.app.v().SetCursor(buffer.Pos{Line: 0, Col: tt.col})
		if got := h.app.symbolUnderCursor(); got != tt.want {
			t.Errorf("col %d: symbol = %q, want %q", tt.col, got, tt.want)
		}
	}
}

func TestSymbolAtIgnoresNumbers(t *testing.T) {
	line := []byte("x = 12345;")
	if got := provider.SymbolAt(line, 5); got != "" {
		t.Errorf("SymbolAt on a number returned %q, want empty", got)
	}
	if got := provider.SymbolAt(line, 0); got != "x" {
		t.Errorf("SymbolAt = %q, want x", got)
	}
}

// The pty run showed the status line reporting a successful jump while the
// screen still displayed the previous file. Asserting on a.v() alone missed it,
// so this checks what is actually drawn.
func TestGotoDefinitionActuallyRedrawsTheNewFile(t *testing.T) {
	h := project(t, map[string]string{
		"main.c": mainC,
		"util.c": utilC,
		"util.h": utilH,
	}, "main.c")

	h.putCursorOn(t, "compute_total")
	h.app.cmdGotoDefinition()
	h.settle()

	screen := strings.Join(h.draw(), "\n")
	if !strings.Contains(screen, "return base * 2") {
		t.Errorf("screen still shows the old file after the jump:\n%s", screen)
	}
	if strings.Contains(screen, "int n = compute_total(argc)") {
		t.Errorf("screen still shows main.c after jumping to util.c:\n%s", screen)
	}
}

// After jumping, the cursor should sit on the symbol, not at column 0. Landing
// on column 0 means the very next navigation command operates on whatever word
// happens to start the line -- usually the return type.
func TestGotoDefinitionPutsCursorOnTheSymbol(t *testing.T) {
	h := project(t, map[string]string{
		"main.c": mainC,
		"util.c": utilC,
		"util.h": utilH,
	}, "main.c")

	h.putCursorOn(t, "compute_total")
	h.app.cmdGotoDefinition()
	h.settle()

	if got := h.app.symbolUnderCursor(); got != "compute_total" {
		t.Errorf("after the jump the cursor is on %q, want %q", got, "compute_total")
	}
}

// Found by driving the real editor: searching for "compute" leaves that partial
// match selected, and goto-definition then looked up "compute" -- which is not
// a symbol -- instead of the "compute_total" the cursor is sitting inside.
// A partial selection should not defeat navigation.
func TestGotoDefinitionIgnoresPartialWordSelection(t *testing.T) {
	h := project(t, map[string]string{
		"main.c": mainC,
		"util.c": utilC,
		"util.h": utilH,
	}, "main.c")

	// Select just "compute", the way an incremental search would.
	h.putCursorOn(t, "compute_total")
	start := h.app.v().Head
	h.app.v().Anchor = start
	h.app.v().Head = buffer.Pos{Line: start.Line, Col: start.Col + len("compute")}

	if got := h.app.symbolUnderCursor(); got != "compute_total" {
		t.Errorf("symbol = %q, want the full identifier %q", got, "compute_total")
	}

	h.app.cmdGotoDefinition()

	h.settle()
	if got := h.curFile(); got != "util.c" {
		t.Errorf("jumped to %s, want util.c", got)
	}
}

// A deliberate selection of something that is not an identifier at all should
// still be honored, since that is the only way to look up a phrase.
func TestSelectionOfNonIdentifierIsHonored(t *testing.T) {
	h := project(t, map[string]string{"main.c": mainC}, "main.c")
	h.putCursorOn(t, "int n = compute_total")
	start := h.app.v().Head
	h.app.v().Anchor = start
	h.app.v().Head = buffer.Pos{Line: start.Line, Col: start.Col + len("int n")}

	if got := h.app.symbolUnderCursor(); got != "int n" {
		t.Errorf("symbol = %q, want the literal selection %q", got, "int n")
	}
}
