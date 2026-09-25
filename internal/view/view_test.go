package view

import (
	"strings"
	"testing"

	"sonarc/internal/buffer"
)

func newView(s string) *View {
	v := New(buffer.FromBytes([]byte(s)))
	v.Height, v.Width = 10, 40
	return v
}

func all(v *View) string {
	return string(v.Buf.Text(buffer.Pos{}, v.Buf.End()))
}

// Moving down through a short line and back out must return to the original
// column. Editors that forget this make column-aligned code miserable to edit.
func TestVerticalMovementKeepsGoalColumn(t *testing.T) {
	v := newView("longest line here\nshort\nanother long line")
	v.SetCursor(buffer.Pos{Line: 0, Col: 15})

	v.MoveDown(false)
	if v.Head.Line != 1 || v.Head.Col != 5 {
		t.Fatalf("on short line: got %v, want line 1 col 5", v.Head)
	}
	v.MoveDown(false)
	if v.Head.Line != 2 || v.Head.Col != 15 {
		t.Errorf("goal column lost: got %v, want line 2 col 15", v.Head)
	}
}

func TestGoalColumnResetsOnHorizontalMove(t *testing.T) {
	v := newView("longest line here\nshort\nanother long line")
	v.SetCursor(buffer.Pos{Line: 0, Col: 15})
	v.MoveDown(false) // now on "short", col 5
	v.MoveLeft(false) // an explicit horizontal move re-aims the goal
	v.MoveDown(false)
	if v.Head.Col != 4 {
		t.Errorf("after horizontal move, col = %d, want 4", v.Head.Col)
	}
}

func TestSmartHome(t *testing.T) {
	v := newView("\t  indented")
	v.MoveEnd(false)
	v.MoveHome(false)
	if v.Head.Col != 3 {
		t.Errorf("first Home should go to first non-blank (col 3), got %d", v.Head.Col)
	}
	v.MoveHome(false)
	if v.Head.Col != 0 {
		t.Errorf("second Home should go to column 0, got %d", v.Head.Col)
	}
	v.MoveHome(false)
	if v.Head.Col != 3 {
		t.Errorf("third Home should return to first non-blank, got %d", v.Head.Col)
	}
}

func TestWordMovement(t *testing.T) {
	v := newView("foo_bar baz(qux)")
	want := []int{8, 11, 12, 15, 16}
	for i, w := range want {
		v.MoveWordRight(false)
		if v.Head.Col != w {
			t.Errorf("word right %d: col = %d, want %d", i, v.Head.Col, w)
		}
	}
	// Walking back must land on word starts, not the same boundaries reversed.
	backWant := []int{15, 12, 11, 8, 0}
	for i, w := range backWant {
		v.MoveWordLeft(false)
		if v.Head.Col != w {
			t.Errorf("word left %d: col = %d, want %d", i, v.Head.Col, w)
		}
	}
}

func TestWordMovementCrossesLines(t *testing.T) {
	v := newView("one\ntwo")
	v.MoveEnd(false)
	v.MoveWordRight(false)
	if v.Head.Line != 1 || v.Head.Col != 0 {
		t.Errorf("word right at line end: got %v, want line 1 col 0", v.Head)
	}
	v.MoveWordLeft(false)
	if v.Head.Line != 0 || v.Head.Col != 3 {
		t.Errorf("word left at line start: got %v, want line 0 col 3", v.Head)
	}
}

// Typing with text selected must replace it, not insert alongside it.
func TestTypingReplacesSelection(t *testing.T) {
	v := newView("hello world")
	v.Head = buffer.Pos{Line: 0, Col: 0}
	v.Anchor = buffer.Pos{Line: 0, Col: 5}
	v.Insert([]byte("goodbye"))
	if got := all(v); got != "goodbye world" {
		t.Errorf("got %q, want %q", got, "goodbye world")
	}
	if v.HasSelection() {
		t.Error("selection should be collapsed after typing")
	}
}

func TestBackspaceDeletesSelection(t *testing.T) {
	v := newView("hello world")
	v.Head = buffer.Pos{Line: 0, Col: 5}
	v.Anchor = buffer.Pos{Line: 0, Col: 0}
	v.DeleteBackward()
	if got := all(v); got != " world" {
		t.Errorf("got %q, want %q", got, " world")
	}
}

func TestBackspaceJoinsLines(t *testing.T) {
	v := newView("ab\ncd")
	v.SetCursor(buffer.Pos{Line: 1, Col: 0})
	v.DeleteBackward()
	if got := all(v); got != "abcd" {
		t.Errorf("got %q, want %q", got, "abcd")
	}
	if v.Head != (buffer.Pos{Line: 0, Col: 2}) {
		t.Errorf("cursor = %v, want line 0 col 2", v.Head)
	}
}

func TestBackspaceAtStartOfBufferIsNoOp(t *testing.T) {
	v := newView("abc")
	v.SetCursor(buffer.Pos{})
	v.DeleteBackward()
	if got := all(v); got != "abc" {
		t.Errorf("got %q, want %q", got, "abc")
	}
}

func TestDeleteForwardJoinsLines(t *testing.T) {
	v := newView("ab\ncd")
	v.MoveEnd(false)
	v.DeleteForward()
	if got := all(v); got != "abcd" {
		t.Errorf("got %q, want %q", got, "abcd")
	}
}

// A new line should start at the same indentation as the one it came from.
func TestNewlineCarriesIndent(t *testing.T) {
	v := newView("\t\tcode here")
	v.MoveEnd(false)
	v.InsertNewline()
	if got := all(v); got != "\t\tcode here\n\t\t" {
		t.Errorf("got %q, want %q", got, "\t\tcode here\n\t\t")
	}
	if v.Head.Col != 2 {
		t.Errorf("cursor should be after the indent, col = %d, want 2", v.Head.Col)
	}
}

// Splitting inside the indentation shouldn't invent whitespace that wasn't
// there to begin with.
func TestNewlineInsideIndentDoesNotInventWhitespace(t *testing.T) {
	v := newView("\t\tcode")
	v.SetCursor(buffer.Pos{Line: 0, Col: 1})
	v.InsertNewline()
	if got := all(v); got != "\t\n\t\tcode" {
		t.Errorf("got %q, want %q", got, "\t\n\t\tcode")
	}
}

func TestInsertTabAlignsToTabStop(t *testing.T) {
	v := newView("ab")
	v.TabWidth = 4
	v.ExpandTabs = true
	v.MoveEnd(false)
	v.InsertTab()
	if got := all(v); got != "ab  " {
		t.Errorf("tab from column 2 should reach column 4, got %q", got)
	}
	v.InsertTab()
	if got := all(v); got != "ab      " {
		t.Errorf("second tab should reach column 8, got %q", got)
	}
}

func TestInsertTabLiteralWhenNotExpanding(t *testing.T) {
	v := newView("")
	v.ExpandTabs = false
	v.InsertTab()
	if got := all(v); got != "\t" {
		t.Errorf("got %q, want a literal tab", got)
	}
}

func TestDeleteLine(t *testing.T) {
	tests := []struct {
		name string
		text string
		line int
		want string
	}{
		{"middle line", "a\nb\nc", 1, "a\nc"},
		{"first line", "a\nb\nc", 0, "b\nc"},
		{"last line takes preceding newline", "a\nb\nc", 2, "a\nb"},
		{"only line empties buffer", "solo", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newView(tt.text)
			v.SetCursor(buffer.Pos{Line: tt.line})
			v.DeleteLine()
			if got := all(v); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSelectAll(t *testing.T) {
	v := newView("a\nbc\nd")
	v.SelectAll()
	if got := string(v.SelectedText()); got != "a\nbc\nd" {
		t.Errorf("selected %q, want the whole buffer", got)
	}
}

func TestCopyWithoutSelectionTakesLine(t *testing.T) {
	v := newView("first\nsecond")
	v.SetCursor(buffer.Pos{Line: 1})
	if got := string(v.Copy()); got != "second\n" {
		t.Errorf("got %q, want %q", got, "second\n")
	}
}

func TestCutRemovesSelection(t *testing.T) {
	v := newView("hello world")
	v.Anchor = buffer.Pos{Line: 0, Col: 0}
	v.Head = buffer.Pos{Line: 0, Col: 6}
	if got := string(v.Cut()); got != "hello " {
		t.Errorf("cut returned %q", got)
	}
	if got := all(v); got != "world" {
		t.Errorf("after cut = %q, want %q", got, "world")
	}
}

// The cursor must stay on screen, with context around it where the file allows.
func TestScrollKeepsCursorVisibleWithMargin(t *testing.T) {
	v := newView(strings.Repeat("line\n", 100))
	v.Height = 10

	v.Goto(50)
	if v.Head.Line < v.Top || v.Head.Line >= v.Top+v.Height {
		t.Errorf("cursor line %d outside viewport [%d,%d)", v.Head.Line, v.Top, v.Top+v.Height)
	}
	if v.Head.Line-v.Top < scrollMargin {
		t.Errorf("no top margin: cursor %d, top %d", v.Head.Line, v.Top)
	}

	// At the very top of the file there is no room for a margin, and the view
	// must not scroll to negative lines to invent one.
	v.Goto(1)
	if v.Top != 0 {
		t.Errorf("at file start Top = %d, want 0", v.Top)
	}
}

func TestScrollDoesNotMoveCursor(t *testing.T) {
	v := newView(strings.Repeat("line\n", 100))
	v.Goto(1)
	before := v.Head
	v.Scroll(20)
	if v.Head != before {
		t.Errorf("mouse scroll moved the cursor from %v to %v", before, v.Head)
	}
	if v.Top != 20 {
		t.Errorf("Top = %d, want 20", v.Top)
	}
}

func TestScrollClampsAtBounds(t *testing.T) {
	v := newView("a\nb\nc")
	v.Scroll(-10)
	if v.Top != 0 {
		t.Errorf("Top = %d, want 0", v.Top)
	}
	v.Scroll(1000)
	if v.Top != v.Buf.NumLines()-1 {
		t.Errorf("Top = %d, want %d", v.Top, v.Buf.NumLines()-1)
	}
}

// Clicking maps screen cells to buffer positions through the tab expansion, so
// a click lands where the character was drawn.
func TestClickAccountsForTabs(t *testing.T) {
	v := newView("\tab")
	v.TabWidth = 4
	v.Click(0, 4, false) // first cell after the expanded tab
	if v.Head.Col != 1 {
		t.Errorf("click col = %d, want 1", v.Head.Col)
	}
	v.Click(0, 2, false) // inside the tab resolves to the tab itself
	if v.Head.Col != 0 {
		t.Errorf("click inside tab col = %d, want 0", v.Head.Col)
	}
}

func TestShiftMovementExtendsSelection(t *testing.T) {
	v := newView("hello")
	v.MoveRight(true)
	v.MoveRight(true)
	if got := string(v.SelectedText()); got != "he" {
		t.Errorf("selected %q, want %q", got, "he")
	}
	// Moving without extend collapses to the selection edge rather than
	// stepping one further, matching how GUI editors behave.
	v.MoveRight(false)
	if v.HasSelection() {
		t.Error("selection should be cleared")
	}
	if v.Head.Col != 2 {
		t.Errorf("cursor = %d, want 2 (selection end)", v.Head.Col)
	}
}

func TestUndoRedoThroughView(t *testing.T) {
	v := newView("hello")
	v.MoveEnd(false)
	v.Insert([]byte("!"))
	if !v.Undo() {
		t.Fatal("Undo returned false")
	}
	if got := all(v); got != "hello" {
		t.Errorf("after undo = %q", got)
	}
	if !v.Redo() {
		t.Fatal("Redo returned false")
	}
	if got := all(v); got != "hello!" {
		t.Errorf("after redo = %q", got)
	}
}

func sel(v *View) string { return string(v.SelectedText()) }

func TestSelectWordCoversTheWholeIdentifier(t *testing.T) {
	v := newView("foo_bar baz(qux)")
	// Anywhere inside the word, including its first and last characters.
	for _, col := range []int{0, 3, 6} {
		v.SelectWord(buffer.Pos{Line: 0, Col: col})
		if got := sel(v); got != "foo_bar" {
			t.Errorf("SelectWord at col %d = %q, want %q", col, got, "foo_bar")
		}
	}
}

func TestSelectWordOnPunctuationAndSpace(t *testing.T) {
	v := newView("foo  ((bar")
	v.SelectWord(buffer.Pos{Col: 4}) // inside the run of spaces
	if got := sel(v); got != "  " {
		t.Errorf("whitespace run = %q, want two spaces", got)
	}
	v.SelectWord(buffer.Pos{Col: 5})
	if got := sel(v); got != "((" {
		t.Errorf("punctuation run = %q, want %q", got, "((")
	}
}

func TestSelectWordPastEndOfLineTakesLastWord(t *testing.T) {
	v := newView("hello world")
	v.SelectWord(buffer.Pos{Col: 999})
	if got := sel(v); got != "world" {
		t.Errorf("past-end click = %q, want %q", got, "world")
	}
}

func TestSelectWordOnEmptyLineSelectsNothing(t *testing.T) {
	v := newView("a\n\nb")
	v.SelectWord(buffer.Pos{Line: 1})
	if v.HasSelection() {
		t.Errorf("empty line produced selection %q", sel(v))
	}
	if v.Head.Line != 1 {
		t.Errorf("cursor line = %d, want 1", v.Head.Line)
	}
}

func TestSelectWordHandlesMultibyteText(t *testing.T) {
	v := newView("héllo wörld")
	v.SelectWord(buffer.Pos{Col: 3}) // inside "héllo" (é is two bytes)
	if got := sel(v); got != "héllo" {
		t.Errorf("SelectWord = %q, want %q", got, "héllo")
	}
}

func TestSelectLineIncludesTheLineBreak(t *testing.T) {
	v := newView("one\ntwo\nthree")
	v.SelectLine(1)
	if got := sel(v); got != "two\n" {
		t.Errorf("SelectLine(1) = %q, want %q", got, "two\n")
	}
}

func TestSelectLineOnLastLineHasNoBreakToInclude(t *testing.T) {
	v := newView("one\ntwo")
	v.SelectLine(1)
	if got := sel(v); got != "two" {
		t.Errorf("SelectLine(last) = %q, want %q", got, "two")
	}
}

func TestSelectLineClampsOutOfRange(t *testing.T) {
	v := newView("one\ntwo")
	v.SelectLine(99)
	if got := sel(v); got != "two" {
		t.Errorf("SelectLine(99) = %q, want the last line", got)
	}
	v.SelectLine(-5)
	if got := sel(v); got != "one\n" {
		t.Errorf("SelectLine(-5) = %q, want the first line", got)
	}
}
