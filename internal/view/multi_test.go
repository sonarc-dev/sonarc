package view

import (
	"testing"

	"github.com/sonarc-dev/sonarc/internal/buffer"
)

func contents(v *View) string { return string(v.Buf.Bytes()) }

func TestColumnInsertAndOneUndo(t *testing.T) {
	v := New(buffer.FromBytes([]byte("alpha\nbeta\ngamma\ndelta")))
	v.SetCursor(buffer.Pos{Line: 0, Col: 0})
	v.AddCaretVertical(1)
	v.AddCaretVertical(1)
	if v.Carets() != 3 {
		t.Fatalf("carets = %d, want 3", v.Carets())
	}
	v.ForEach(func() { v.Insert([]byte("// ")) })
	if got := contents(v); got != "// alpha\n// beta\n// gamma\ndelta" {
		t.Fatalf("after typing at three cursors: %q", got)
	}
	v.Undo()
	if got := contents(v); got != "alpha\nbeta\ngamma\ndelta" || v.Carets() != 1 {
		t.Errorf("one undo should revert all three: %q, %d carets", got, v.Carets())
	}
}

// Cursors on the same line keep their places as text goes in before them.
func TestCursorsOnOneLine(t *testing.T) {
	v := New(buffer.FromBytes([]byte("ab cd ef")))
	v.SetCursor(buffer.Pos{Col: 0})
	v.AltClick(0, 3)
	v.AltClick(0, 6)
	v.ForEach(func() { v.Insert([]byte("<")) })
	v.ForEach(func() { v.MoveRight(false) })
	v.ForEach(func() { v.MoveRight(false) })
	v.ForEach(func() { v.Insert([]byte(">")) })
	if got := contents(v); got != "<ab> <cd> <ef>" {
		t.Errorf("got %q", got)
	}
}

func TestBackspaceAndNewlineAtEveryCursor(t *testing.T) {
	v := New(buffer.FromBytes([]byte("x1\nx2\nx3")))
	v.SetCursor(buffer.Pos{Line: 0, Col: 1})
	v.AddCaretVertical(1)
	v.AddCaretVertical(1)
	v.ForEach(v.DeleteBackward)
	if got := contents(v); got != "1\n2\n3" {
		t.Fatalf("after backspace: %q", got)
	}
	v.ForEach(func() { v.MoveEnd(false) })
	v.ForEach(v.InsertNewline)
	v.ForEach(func() { v.Insert([]byte("-")) })
	if got := contents(v); got != "1\n-\n2\n-\n3\n-" {
		t.Errorf("after newline and typing: %q", got)
	}
}

// Cursors that land on the same place become one.
func TestCursorsMerge(t *testing.T) {
	v := New(buffer.FromBytes([]byte("abcdef")))
	v.SetCursor(buffer.Pos{Col: 2})
	v.AltClick(0, 4)
	v.ForEach(func() { v.MoveHome(false) })
	if v.Carets() != 1 {
		t.Errorf("carets = %d after both went home, want 1", v.Carets())
	}
}

func TestNextAndAllOccurrences(t *testing.T) {
	v := New(buffer.FromBytes([]byte("foo bar foo\nbaz foo foobar")))
	v.SetCursor(buffer.Pos{Col: 1})
	v.AddNextOccurrence() // selects the word
	if string(v.SelectedText()) != "foo" || v.Carets() != 1 {
		t.Fatalf("first press should select the word: %q", v.SelectedText())
	}
	v.AddNextOccurrence()
	v.AddNextOccurrence()
	if v.Carets() != 3 {
		t.Fatalf("carets = %d, want 3", v.Carets())
	}
	v.ForEach(func() { v.Insert([]byte("qux")) })
	if got := contents(v); got != "qux bar qux\nbaz qux foobar" {
		t.Errorf("after replacing three: %q", got)
	}

	w := New(buffer.FromBytes([]byte("a.b a.b a.b")))
	w.SetCursor(buffer.Pos{})
	w.Anchor, w.Head = buffer.Pos{Col: 0}, buffer.Pos{Col: 3}
	if n := w.SelectAllOccurrences(); n != 3 {
		t.Errorf("all occurrences of a.b = %d, want 3", n)
	}
}

func TestSplitSelectionIntoLines(t *testing.T) {
	v := New(buffer.FromBytes([]byte("one\ntwo\nthree\nfour")))
	v.SetCursor(buffer.Pos{Line: 0, Col: 1})
	v.Head = buffer.Pos{Line: 2, Col: 2}
	if n := v.SplitSelectionIntoLines(); n != 3 {
		t.Fatalf("carets = %d, want 3", n)
	}
	v.ForEach(func() { v.Insert([]byte(";")) })
	if got := contents(v); got != "one;\ntwo;\nth;ree\nfour" {
		t.Errorf("got %q", got)
	}
}

// Copying at three cursors and pasting at three puts one piece at each.
func TestCopyAndPasteDistribute(t *testing.T) {
	v := New(buffer.FromBytes([]byte("a1 b2 c3\n\n\n\n"))) // three empty lines, then the final newline
	v.SetCursor(buffer.Pos{Col: 0})
	v.Head = buffer.Pos{Col: 2}
	v.SelectAllOccurrences() // just "a1": only one
	v.ClearCarets()
	v.Anchor, v.Head = buffer.Pos{Col: 0}, buffer.Pos{Col: 2}
	v.Extra = []Caret{{Anchor: buffer.Pos{Col: 3}, Head: buffer.Pos{Col: 5}}, {Anchor: buffer.Pos{Col: 6}, Head: buffer.Pos{Col: 8}}}
	parts := v.CopyAll()
	if len(parts) != 3 || string(parts[1]) != "b2" {
		t.Fatalf("copied %q", parts)
	}
	v.SetCursor(buffer.Pos{Line: 1})
	v.AddCaretVertical(1)
	v.AddCaretVertical(1)
	if v.Carets() != 3 {
		t.Fatalf("carets = %d, want 3", v.Carets())
	}
	v.PasteEach([]byte("a1\nb2\nc3"), parts)
	if got := contents(v); got != "a1 b2 c3\na1\nb2\nc3\n" {
		t.Errorf("after pasting: %q", got)
	}
}
