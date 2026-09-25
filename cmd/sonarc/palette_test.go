package main

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestPickerFilterNarrowsItems(t *testing.T) {
	h := project(t, map[string]string{
		"a.c":    "int shared_name(void)\n{\n\treturn 1;\n}\n",
		"b.c":    "int shared_name(void)\n{\n\treturn 2;\n}\n",
		"main.c": "int main(void)\n{\n\treturn shared_name();\n}\n",
	}, "main.c")

	h.putCursorOn(t, "shared_name")
	h.app.cmdGotoDefinition()
	h.settle()
	if !h.app.ui.Picker.Open {
		t.Fatal("expected a picker")
	}
	before := h.app.ui.Picker.Count()
	h.app.handle(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone))
	h.app.handle(tcell.NewEventKey(tcell.KeyRune, '.', tcell.ModNone))
	h.app.handle(tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModNone))
	if after := h.app.ui.Picker.Count(); after >= before {
		t.Errorf("filtering to %q left %d of %d items", h.app.ui.Picker.Filter, after, before)
	}

	// Escape must abandon it without navigating.
	h.app.handle(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	if h.app.ui.Picker.Open {
		t.Error("Escape did not close the picker")
	}
}

func TestFuzzyFileOpener(t *testing.T) {
	h := project(t, map[string]string{
		"main.c":           mainC,
		"util.c":           utilC,
		"util.h":           utilH,
		"sub/deep/other.c": "int other(void)\n{\n\treturn 0;\n}\n",
	}, "main.c")

	h.app.cmdOpenFuzzy()
	if !h.app.ui.Picker.Open {
		t.Fatal("the file picker did not open")
	}
	if n := h.app.ui.Picker.Count(); n != 4 {
		t.Errorf("picker lists %d files, want 4", n)
	}

	// Typing narrows it, and the fuzzy match reaches into the path.
	h.app.ui.Picker.SetFilter("deep")
	if n := h.app.ui.Picker.Count(); n != 1 {
		t.Fatalf("filtering by %q left %d items, want 1", "deep", n)
	}
	h.app.handle(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if got := h.curFile(); got != "other.c" {
		t.Errorf("picker opened %s, want other.c", got)
	}
	// Opening a file is not navigation; it must not fill the tag stack.
	if len(h.app.jumps) != 0 {
		t.Errorf("opening a file pushed %d jumps, want 0", len(h.app.jumps))
	}
}

func TestCommandPaletteRunsCommands(t *testing.T) {
	h := project(t, map[string]string{"main.c": mainC}, "main.c")

	h.app.cmdPalette()
	if !h.app.ui.Picker.Open {
		t.Fatal("the palette did not open")
	}
	if h.app.ui.Picker.Count() != len(commands) {
		t.Errorf("palette lists %d of %d commands", h.app.ui.Picker.Count(), len(commands))
	}

	// Every entry must be labelled with its command's help text.
	found := false
	for _, it := range h.app.ui.Picker.Items {
		if it.Label == commands["select-all"].help {
			found = true
		}
	}
	if !found {
		t.Error("palette does not list select-all by its description")
	}

	// Choosing an entry runs it.
	h.app.ui.Picker.SetFilter(commands["select-all"].help)
	h.app.handle(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if !h.app.v().HasSelection() {
		t.Error("choosing 'select all' from the palette did not run it")
	}
}

// The palette teaches shortcuts, so entries that have one must show it.
func TestPaletteShowsKeyBindings(t *testing.T) {
	h := project(t, map[string]string{"main.c": mainC}, "main.c")
	h.app.cmdPalette()

	for _, it := range h.app.ui.Picker.Items {
		if it.Label == commands["save"].help {
			if !strings.Contains(it.Detail, "Ctrl+S") {
				t.Errorf("save entry shows %q, want it to mention Ctrl+S", it.Detail)
			}
			return
		}
	}
	t.Error("palette does not list save")
}

// A picker action must not leak into the next picker: opening a file and then
// jumping to a definition must do the right thing each time.
func TestPickerActionDoesNotLeak(t *testing.T) {
	h := project(t, map[string]string{
		"a.c":    "int shared_name(void)\n{\n\treturn 1;\n}\n",
		"b.c":    "int shared_name(void)\n{\n\treturn 2;\n}\n",
		"main.c": "int main(void)\n{\n\treturn shared_name();\n}\n",
	}, "main.c")

	h.app.cmdOpenFuzzy()
	h.app.ui.Picker.SetFilter("main")
	h.app.handle(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if h.app.pickAction != nil {
		t.Fatal("the picker action was not cleared after use")
	}

	// Now an ambiguous goto-definition must navigate, not re-open a file.
	h.putCursorOn(t, "shared_name")
	h.app.cmdGotoDefinition()
	h.settle()
	if !h.app.ui.Picker.Open {
		t.Fatal("expected a definition picker")
	}
	h.app.handle(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if f := h.curFile(); f != "a.c" && f != "b.c" {
		t.Errorf("definition picker opened %s", f)
	}
	if len(h.app.jumps) == 0 {
		t.Error("goto-definition through the picker did not record a jump")
	}
}
