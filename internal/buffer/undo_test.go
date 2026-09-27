package buffer

import "testing"

// Edits made in a Group undo and redo as one step, whatever their shape.
func TestGroupUndoesAsOneStep(t *testing.T) {
	b := FromBytes([]byte("one\ntwo\nthree"))
	b.Insert(Pos{Line: 0, Col: 0}, []byte(">"))
	b.BreakUndo()
	b.Group(Pos{Line: 1, Col: 0}, func() Pos {
		b.Insert(Pos{Line: 2, Col: 0}, []byte("# "))
		b.Insert(Pos{Line: 1, Col: 0}, []byte("# "))
		b.Delete(Pos{Line: 0, Col: 0}, Pos{Line: 0, Col: 1})
		return Pos{Line: 1, Col: 2}
	})
	if got := string(b.Bytes()); got != "one\n# two\n# three" {
		t.Fatalf("after the group: %q", got)
	}
	p, ok := b.Undo()
	if !ok || string(b.Bytes()) != ">one\ntwo\nthree" || p != (Pos{Line: 1, Col: 0}) {
		t.Errorf("undo = %q at %v, want the whole group undone and the cursor at 1:0", b.Bytes(), p)
	}
	p, _ = b.Redo()
	if string(b.Bytes()) != "one\n# two\n# three" || p != (Pos{Line: 1, Col: 2}) {
		t.Errorf("redo = %q at %v", b.Bytes(), p)
	}
	// Typing after a group starts its own step.
	b.Insert(Pos{Line: 0, Col: 3}, []byte("!"))
	b.Undo()
	if string(b.Bytes()) != "one\n# two\n# three" {
		t.Errorf("typing after a group joined it: %q", b.Bytes())
	}
}

// Typing a word at several cursors undoes as the word.
func TestGroupsOfTypingCoalesce(t *testing.T) {
	b := FromBytes([]byte("a\nb"))
	typeAt := func(s string, col int) {
		b.Group(Pos{Line: 0, Col: col}, func() Pos {
			b.Insert(Pos{Line: 1, Col: col}, []byte(s))
			b.Insert(Pos{Line: 0, Col: col}, []byte(s))
			return Pos{Line: 0, Col: col + 1}
		})
	}
	typeAt("x", 1)
	typeAt("y", 2)
	typeAt("z", 3)
	if string(b.Bytes()) != "axyz\nbxyz" {
		t.Fatalf("text = %q", b.Bytes())
	}
	b.Undo()
	if string(b.Bytes()) != "a\nb" {
		t.Errorf("one undo should take back the word at both cursors, left %q", b.Bytes())
	}
	// Moving the cursor in between keeps them apart.
	typeAt("x", 1)
	b.BreakUndo()
	typeAt("y", 2)
	b.Undo()
	if string(b.Bytes()) != "ax\nbx" {
		t.Errorf("after a move, undo should take back only the last keystroke, left %q", b.Bytes())
	}
}
