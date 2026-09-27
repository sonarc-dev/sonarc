package main

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/sonarc-dev/sonarc/internal/buffer"
	"github.com/sonarc-dev/sonarc/internal/view"
)

func altShift(h *harness, k tcell.Key) {
	h.app.handle(tcell.NewEventKey(k, 0, tcell.ModAlt|tcell.ModShift))
	h.settle()
}

func alt(h *harness, r rune) {
	h.app.handle(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModAlt))
	h.settle()
}

func TestColumnEditingFromTheKeyboard(t *testing.T) {
	h := newHarness(t, "one\ntwo\nthree\n")
	altShift(h, tcell.KeyDown)
	altShift(h, tcell.KeyDown)
	h.typeText("- ")
	if h.text() != "- one\n- two\n- three" {
		t.Fatalf("text = %q", h.text())
	}
	if !strings.Contains(h.screen(), "3 cursors") {
		t.Errorf("the status bar should say 3 cursors:\n%s", h.screen())
	}
	h.key(tcell.KeyCtrlZ)
	if h.text() != "one\ntwo\nthree" {
		t.Errorf("one undo should take back what was typed at every cursor, left %q", h.text())
	}
	h.key(tcell.KeyEscape)
	if h.app.v().Carets() != 1 {
		t.Errorf("Esc should go back to one cursor")
	}
}

// Alt+N selects the word, then adds its next occurrence; typing replaces
// each, and one undo takes the replacement back everywhere.
func TestRenameByNextOccurrence(t *testing.T) {
	h := newHarness(t, "count = count + 1\nprint(count)\n")
	alt(h, 'n')
	alt(h, 'n')
	alt(h, 'n')
	h.typeText("total")
	if h.text() != "total = total + 1\nprint(total)" {
		t.Fatalf("text = %q", h.text())
	}
	// Replacing the selections is one step and the rest of the word another,
	// each at every cursor at once.
	h.key(tcell.KeyCtrlZ)
	if h.text() != "t = t + 1\nprint(t)" {
		t.Errorf("first undo left %q", h.text())
	}
	h.key(tcell.KeyCtrlZ)
	if h.text() != "count = count + 1\nprint(count)" {
		t.Errorf("second undo left %q", h.text())
	}
}

func TestEveryOccurrenceByChord(t *testing.T) {
	h := newHarness(t, "x := f(x) + x\n")
	h.key(tcell.KeyCtrlK)
	h.typeText("*")
	if n := h.app.v().Carets(); n != 3 {
		t.Fatalf("carets = %d, want 3", n)
	}
}

// Copying at three cursors and pasting at three puts one piece at each.
func TestCopyPasteAcrossCursors(t *testing.T) {
	h := newHarness(t, "a1 b2 c3\n\n\n\n")
	v := h.app.v()
	v.Anchor.Col, v.Head.Col = 0, 2
	v.Extra = []view.Caret{
		{Anchor: buffer.Pos{Col: 3}, Head: buffer.Pos{Col: 5}},
		{Anchor: buffer.Pos{Col: 6}, Head: buffer.Pos{Col: 8}},
	}
	h.key(tcell.KeyCtrlC)
	if string(h.app.clip) != "a1\nb2\nc3" {
		t.Fatalf("clipboard = %q", h.app.clip)
	}
	v.SetCursor(buffer.Pos{Line: 1})
	altShift(h, tcell.KeyDown)
	altShift(h, tcell.KeyDown)
	h.key(tcell.KeyCtrlV)
	if h.text() != "a1 b2 c3\na1\nb2\nc3" {
		t.Errorf("text = %q", h.text())
	}
}

// Ctrl+D deletes the line at every cursor.
func TestDeleteLineAtEveryCursor(t *testing.T) {
	h := newHarness(t, "keep\ndrop\nkeep\ndrop\n")
	h.key(tcell.KeyDown)     // line 2
	h.app.v().AltClick(3, 0) // and line 4
	h.key(tcell.KeyCtrlD)
	if h.text() != "keep\nkeep" {
		t.Errorf("text = %q", h.text())
	}
}

// Extra cursors are drawn where they are.
func TestExtraCursorsAreDrawn(t *testing.T) {
	h := newHarness(t, "abc\nabc\n")
	altShift(h, tcell.KeyDown)
	h.draw()
	cells, w, _ := h.sim.GetContents()
	x := h.app.ui.GutterWidth()
	if cells[1*w+x].Style == cells[0*w+x+1].Style {
		t.Error("the second cursor is not marked on screen")
	}
}
