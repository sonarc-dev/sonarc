// Package view is one window onto a buffer: where the cursor is, what is
// selected, which part of the file is on screen, and the editing operations a
// keystroke maps to.
//
// The buffer knows nothing about cursors or scrolling; this is where those
// live, so a single buffer can later be shown in several split panes each with
// its own cursor.
package view

import (
	"bytes"

	"github.com/sonarc-dev/sonarc/internal/buffer"
	"github.com/sonarc-dev/sonarc/internal/syntax"
	"github.com/sonarc-dev/sonarc/internal/text"
)

// scrollMargin is how many lines of context are kept above and below the cursor
// when scrolling, so the cursor doesn't sit against the edge of the screen.
const scrollMargin = 3

// View is a cursor and viewport over a buffer.
type View struct {
	Buf *buffer.Buffer

	// Head is the cursor. Anchor is the other end of the selection; when the
	// two are equal there is no selection.
	Head   buffer.Pos
	Anchor buffer.Pos

	Top    int // first visible line
	Left   int // horizontal scroll, in display columns
	Height int // visible text rows
	Width  int // visible text columns

	TabWidth   int
	ExpandTabs bool // insert spaces instead of a tab character

	// Syntax colors this buffer. It caches per-line state, so it belongs to the
	// view rather than being rebuilt each frame.
	Syntax *syntax.Highlighter

	// goalCol remembers the display column the cursor is aiming for, so moving
	// down through a short line and back out returns to the original column
	// instead of collapsing to the short line's end.
	goalCol   int
	keepGoal  bool
	clipboard []byte

	// seen is the buffer version this view's positions are up to date with.
	// A second view of the same buffer uses it to follow edits made in the
	// first; see Sync.
	seen uint64
}

// New returns a view onto buf with sensible defaults.
func New(buf *buffer.Buffer) *View {
	v := &View{Buf: buf, TabWidth: 4, Height: 24, Width: 80, seen: buf.Version()}
	// A very large file gets no highlighting: the per-line scan is cheap, but
	// nothing about a 50 MB log is improved by coloring it.
	if !buf.Large() {
		v.Syntax = syntax.New(syntax.Detect(buf.Path()))
	}
	return v
}

// Reload replaces the buffer with the file as it is now on disk, keeping the
// cursor and scroll position where they still exist. Undo history belongs to
// the old text and goes with it.
func (v *View) Reload() error {
	b, err := buffer.Open(v.Buf.Path())
	if err != nil {
		return err
	}
	v.Adopt(b)
	return nil
}

// Mirror returns a second view of the same buffer, at the same place: what a
// split pane shows when both panes are on one file. Edits through either are
// edits to the one buffer; each view keeps its own cursor and scroll.
func (v *View) Mirror() *View {
	m := New(v.Buf)
	m.Head, m.Anchor = v.Head, v.Anchor
	m.Top, m.Left = v.Top, v.Left
	m.TabWidth, m.ExpandTabs = v.TabWidth, v.ExpandTabs
	m.seen = v.seen
	return m
}

// Sync brings the view up to date with edits made through another view of
// the same buffer: the cursor, selection and scroll move with the text they
// were on. A view that fell too far behind to replay the edits keeps its line
// numbers, clamped to the text.
func (v *View) Sync() {
	now := v.Buf.Version()
	if now == v.seen {
		return
	}
	if edits, ok := v.Buf.EditsSince(v.seen); ok {
		for _, e := range edits {
			v.Head = e.Map(v.Head)
			v.Anchor = e.Map(v.Anchor)
			v.Top = e.Map(buffer.Pos{Line: v.Top}).Line
		}
	}
	v.MarkSeen()
}

// MarkSeen records that the view's positions are current: it made the edits
// itself, or has just been brought up to date. Positions are clamped, since
// nothing else guarantees they still exist.
func (v *View) MarkSeen() {
	v.Head = v.Buf.Clamp(v.Head)
	v.Anchor = v.Buf.Clamp(v.Anchor)
	v.Top = min(max(v.Top, 0), max(v.Buf.NumLines()-1, 0))
	v.seen = v.Buf.Version()
}

// Adopt moves the view onto b, a fresh copy of the same file (after a reload
// from disk), keeping its place where it still exists.
func (v *View) Adopt(b *buffer.Buffer) {
	v.Buf = b
	if !b.Large() {
		v.Syntax = syntax.New(syntax.Detect(b.Path()))
	} else {
		v.Syntax = nil
	}
	v.Head = b.Clamp(v.Head)
	v.Anchor = v.Head
	v.Top = min(v.Top, max(b.NumLines()-1, 0))
	v.seen = b.Version()
}

// touched tells the highlighter that everything from line onward may have
// changed. Typing "/*" turns the rest of the file into a comment, so cached
// state below the edit can no longer be trusted.
func (v *View) touched(line int) {
	if v.Syntax != nil {
		v.Syntax.Invalidate(line)
	}
}

// HasSelection reports whether any text is selected.
func (v *View) HasSelection() bool { return v.Head != v.Anchor }

// SetCursor places the cursor at p and clears any selection.
//
// Prefer this over assigning Head directly: setting one end of the selection
// without the other leaves a stray selection that the next keystroke would
// silently delete.
func (v *View) SetCursor(p buffer.Pos) { v.setHead(p, false) }

// Selection returns the selected range in document order.
func (v *View) Selection() (from, to buffer.Pos) {
	if v.Head.Less(v.Anchor) {
		return v.Head, v.Anchor
	}
	return v.Anchor, v.Head
}

// SelectedText returns a copy of the selected bytes, or nil if nothing is
// selected.
func (v *View) SelectedText() []byte {
	if !v.HasSelection() {
		return nil
	}
	from, to := v.Selection()
	return v.Buf.Text(from, to)
}

// ClearSelection collapses the selection onto the cursor.
func (v *View) ClearSelection() { v.Anchor = v.Head }

// SelectAll selects the whole buffer.
func (v *View) SelectAll() {
	v.Anchor = buffer.Pos{}
	v.Head = v.Buf.End()
	v.ScrollToCursor()
}

// SelectWord selects the run of like characters around p — a word, a run of
// punctuation, or a run of whitespace — which is what a double-click does.
// It shares classOf with word movement, so there is one definition of "word".
func (v *View) SelectWord(p buffer.Pos) {
	p = v.Buf.Clamp(p)
	ln := v.Buf.Line(p.Line)
	if len(ln) == 0 {
		v.setHead(p, false)
		return
	}
	col := p.Col
	if col >= len(ln) {
		col = text.PrevGrapheme(ln, len(ln)) // past the end: take the last character
	}
	c := classOf(ln[col])
	start := col
	for start > 0 {
		prev := text.PrevGrapheme(ln, start)
		if classOf(ln[prev]) != c {
			break
		}
		start = prev
	}
	end := col
	for end < len(ln) && classOf(ln[end]) == c {
		end = text.NextGrapheme(ln, end)
	}
	v.setHead(buffer.Pos{Line: p.Line, Col: start}, false)
	v.setHead(buffer.Pos{Line: p.Line, Col: end}, true)
}

// SelectLine selects a whole line including its line break, which is what a
// triple-click does. The last line has no break to include.
func (v *View) SelectLine(line int) {
	if line < 0 {
		line = 0
	}
	if max := v.Buf.NumLines() - 1; line > max {
		line = max
	}
	v.setHead(buffer.Pos{Line: line}, false)
	if line < v.Buf.NumLines()-1 {
		v.setHead(buffer.Pos{Line: line + 1}, true)
	} else {
		v.setHead(buffer.Pos{Line: line, Col: v.Buf.LineLen(line)}, true)
	}
}

// CursorCol returns the cursor's display column.
func (v *View) CursorCol() int {
	return text.ByteToCol(v.Buf.Line(v.Head.Line), v.Head.Col, v.TabWidth)
}

// setHead moves the cursor, optionally dragging the selection anchor with it.
// Every cursor movement goes through here so selection and undo grouping stay
// consistent.
func (v *View) setHead(p buffer.Pos, extend bool) {
	p = v.Buf.Clamp(p)
	v.Head = p
	if !extend {
		v.Anchor = p
	}
	// Moving away by navigation ends the current undo group, so typing,
	// arrowing elsewhere, then typing again undoes as two steps.
	v.Buf.BreakUndo()
	if !v.keepGoal {
		v.goalCol = v.CursorCol()
	}
	v.keepGoal = false
	v.ScrollToCursor()
}

// MoveLeft moves one grapheme left, or collapses a selection to its start.
func (v *View) MoveLeft(extend bool) {
	if !extend && v.HasSelection() {
		from, _ := v.Selection()
		v.setHead(from, false)
		return
	}
	p := v.Head
	if p.Col > 0 {
		p.Col = text.PrevGrapheme(v.Buf.Line(p.Line), p.Col)
	} else if p.Line > 0 {
		p.Line--
		p.Col = v.Buf.LineLen(p.Line)
	}
	v.setHead(p, extend)
}

// MoveRight moves one grapheme right, or collapses a selection to its end.
func (v *View) MoveRight(extend bool) {
	if !extend && v.HasSelection() {
		_, to := v.Selection()
		v.setHead(to, false)
		return
	}
	p := v.Head
	if p.Col < v.Buf.LineLen(p.Line) {
		p.Col = text.NextGrapheme(v.Buf.Line(p.Line), p.Col)
	} else if p.Line < v.Buf.NumLines()-1 {
		p.Line++
		p.Col = 0
	}
	v.setHead(p, extend)
}

// moveVertical moves the cursor by n lines, preserving the goal column.
func (v *View) moveVertical(n int, extend bool) {
	line := v.Head.Line + n
	if line < 0 {
		line = 0
	}
	if max := v.Buf.NumLines() - 1; line > max {
		line = max
	}
	col := text.ColToByte(v.Buf.Line(line), v.goalCol, v.TabWidth)
	v.keepGoal = true
	v.setHead(buffer.Pos{Line: line, Col: col}, extend)
}

// MoveUp moves one line up, keeping the goal column.
func (v *View) MoveUp(extend bool) { v.moveVertical(-1, extend) }

// MoveDown moves one line down, keeping the goal column.
func (v *View) MoveDown(extend bool) { v.moveVertical(1, extend) }

// PageUp moves up by one screen.
func (v *View) PageUp(extend bool) {
	v.Top -= v.Height
	if v.Top < 0 {
		v.Top = 0
	}
	v.moveVertical(-v.Height, extend)
}

// PageDown moves down by one screen.
func (v *View) PageDown(extend bool) {
	v.Top += v.Height
	v.moveVertical(v.Height, extend)
}

// MoveHome moves to the first non-blank character, or to column zero if
// already there. This "smart home" is what makes indented code comfortable.
func (v *View) MoveHome(extend bool) {
	ln := v.Buf.Line(v.Head.Line)
	indent := 0
	for indent < len(ln) && (ln[indent] == ' ' || ln[indent] == '\t') {
		indent++
	}
	col := indent
	if v.Head.Col == indent {
		col = 0
	}
	v.setHead(buffer.Pos{Line: v.Head.Line, Col: col}, extend)
}

// MoveEnd moves to the end of the line.
func (v *View) MoveEnd(extend bool) {
	v.setHead(buffer.Pos{Line: v.Head.Line, Col: v.Buf.LineLen(v.Head.Line)}, extend)
}

// MoveDocStart moves to the start of the buffer.
func (v *View) MoveDocStart(extend bool) { v.setHead(buffer.Pos{}, extend) }

// MoveDocEnd moves to the end of the buffer.
func (v *View) MoveDocEnd(extend bool) { v.setHead(v.Buf.End(), extend) }

// charClass groups characters so word movement stops where a person expects:
// between identifiers, punctuation runs, and whitespace.
type charClass int

const (
	classSpace charClass = iota
	classWord
	classPunct
)

func classOf(b byte) charClass {
	switch {
	case b == ' ' || b == '\t':
		return classSpace
	case b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b >= 0x80:
		return classWord
	default:
		return classPunct
	}
}

// MoveWordRight moves to the start of the next word.
func (v *View) MoveWordRight(extend bool) {
	p := v.Head
	ln := v.Buf.Line(p.Line)
	if p.Col >= len(ln) {
		if p.Line < v.Buf.NumLines()-1 {
			v.setHead(buffer.Pos{Line: p.Line + 1}, extend)
		}
		return
	}
	// Step over the current run, then over any whitespace after it, landing on
	// the start of the next word rather than the end of this one.
	c := classOf(ln[p.Col])
	for p.Col < len(ln) && classOf(ln[p.Col]) == c {
		p.Col = text.NextGrapheme(ln, p.Col)
	}
	for p.Col < len(ln) && classOf(ln[p.Col]) == classSpace {
		p.Col = text.NextGrapheme(ln, p.Col)
	}
	v.setHead(p, extend)
}

// MoveWordLeft moves to the start of the previous word.
func (v *View) MoveWordLeft(extend bool) {
	p := v.Head
	if p.Col == 0 {
		if p.Line > 0 {
			v.setHead(buffer.Pos{Line: p.Line - 1, Col: v.Buf.LineLen(p.Line - 1)}, extend)
		}
		return
	}
	ln := v.Buf.Line(p.Line)
	p.Col = text.PrevGrapheme(ln, p.Col)
	for p.Col > 0 && classOf(ln[p.Col]) == classSpace {
		p.Col = text.PrevGrapheme(ln, p.Col)
	}
	c := classOf(ln[p.Col])
	for p.Col > 0 {
		prev := text.PrevGrapheme(ln, p.Col)
		if classOf(ln[prev]) != c {
			break
		}
		p.Col = prev
	}
	v.setHead(p, extend)
}

// deleteSelection removes the selection if there is one and reports whether it
// did. Editing operations call this first so typing replaces a selection.
func (v *View) deleteSelection() bool {
	if !v.HasSelection() {
		return false
	}
	from, to := v.Selection()
	v.touched(from.Line)
	v.Head = v.Buf.Delete(from, to)
	v.Anchor = v.Head
	return true
}

// Insert types text at the cursor, replacing any selection.
func (v *View) Insert(s []byte) {
	v.deleteSelection()
	v.touched(v.Head.Line)
	v.Head = v.Buf.Insert(v.Head, s)
	v.Anchor = v.Head
	v.goalCol = v.CursorCol()
	v.ScrollToCursor()
}

// indentOf returns the leading whitespace of a line.
func indentOf(ln []byte) []byte {
	i := 0
	for i < len(ln) && (ln[i] == ' ' || ln[i] == '\t') {
		i++
	}
	return ln[:i]
}

// InsertNewline splits the line, carrying the current indentation onto the new
// line so typing in indented code stays where it belongs.
func (v *View) InsertNewline() {
	v.deleteSelection()
	indent := indentOf(v.Buf.Line(v.Head.Line))
	// Only carry indentation that precedes the cursor; splitting mid-indent
	// shouldn't invent whitespace.
	if v.Head.Col < len(indent) {
		indent = indent[:v.Head.Col]
	}
	s := make([]byte, 0, len(indent)+1)
	s = append(s, '\n')
	s = append(s, indent...)
	v.Insert(s)
}

// InsertTab inserts a tab or the equivalent spaces, aligning to the next tab
// stop rather than inserting a fixed number of spaces.
func (v *View) InsertTab() {
	if !v.ExpandTabs {
		v.Insert([]byte{'\t'})
		return
	}
	n := v.TabWidth - v.CursorCol()%v.TabWidth
	if n < 1 {
		n = v.TabWidth
	}
	v.Insert(bytes.Repeat([]byte{' '}, n))
}

// DeleteBackward implements Backspace: removes the selection, or the grapheme
// before the cursor, joining lines at a line start.
func (v *View) DeleteBackward() {
	if v.deleteSelection() {
		v.ScrollToCursor()
		return
	}
	p := v.Head
	if p.Col == 0 && p.Line == 0 {
		return
	}
	from := p
	if p.Col > 0 {
		from.Col = text.PrevGrapheme(v.Buf.Line(p.Line), p.Col)
	} else {
		from.Line = p.Line - 1
		from.Col = v.Buf.LineLen(from.Line)
	}
	v.touched(from.Line)
	v.Head = v.Buf.Delete(from, p)
	v.Anchor = v.Head
	v.goalCol = v.CursorCol()
	v.ScrollToCursor()
}

// DeleteForward implements Delete: removes the selection, or the grapheme after
// the cursor, joining lines at a line end.
func (v *View) DeleteForward() {
	if v.deleteSelection() {
		v.ScrollToCursor()
		return
	}
	p := v.Head
	to := p
	if p.Col < v.Buf.LineLen(p.Line) {
		to.Col = text.NextGrapheme(v.Buf.Line(p.Line), p.Col)
	} else if p.Line < v.Buf.NumLines()-1 {
		to.Line = p.Line + 1
		to.Col = 0
	} else {
		return
	}
	v.touched(p.Line)
	v.Head = v.Buf.Delete(p, to)
	v.Anchor = v.Head
	v.ScrollToCursor()
}

// DeleteWordBackward removes the word before the cursor.
func (v *View) DeleteWordBackward() {
	if v.deleteSelection() {
		return
	}
	end := v.Head
	v.MoveWordLeft(false)
	start := v.Head
	v.touched(start.Line)
	v.Head = v.Buf.Delete(start, end)
	v.Anchor = v.Head
	v.ScrollToCursor()
}

// DeleteLine removes the cursor's whole line.
func (v *View) DeleteLine() {
	l := v.Head.Line
	from := buffer.Pos{Line: l}
	to := buffer.Pos{Line: l + 1}
	if l == v.Buf.NumLines()-1 {
		// Last line: take the preceding newline instead so no blank line is
		// left behind.
		if l == 0 {
			to = buffer.Pos{Line: l, Col: v.Buf.LineLen(l)}
		} else {
			from = buffer.Pos{Line: l - 1, Col: v.Buf.LineLen(l - 1)}
			to = buffer.Pos{Line: l, Col: v.Buf.LineLen(l)}
		}
	}
	v.touched(from.Line)
	v.Head = v.Buf.Delete(from, to)
	v.Anchor = v.Head
	v.ScrollToCursor()
}

// Undo reverts the last edit and moves the cursor to where it happened.
func (v *View) Undo() bool {
	p, ok := v.Buf.Undo()
	if !ok {
		return false
	}
	v.touched(p.Line)
	v.Head, v.Anchor = p, p
	v.goalCol = v.CursorCol()
	v.ScrollToCursor()
	return true
}

// Redo reapplies the last undone edit.
func (v *View) Redo() bool {
	p, ok := v.Buf.Redo()
	if !ok {
		return false
	}
	v.touched(p.Line)
	v.Head, v.Anchor = p, p
	v.goalCol = v.CursorCol()
	v.ScrollToCursor()
	return true
}

// ScrollToCursor adjusts the viewport so the cursor is visible, keeping a few
// lines of context around it where the file allows.
func (v *View) ScrollToCursor() {
	if v.Height < 1 {
		return
	}
	margin := scrollMargin
	// On a short window the margin would fight itself; give it up rather than
	// oscillating.
	if v.Height < 2*margin+1 {
		margin = 0
	}
	if v.Head.Line-margin < v.Top {
		v.Top = v.Head.Line - margin
	}
	if v.Head.Line+margin >= v.Top+v.Height {
		v.Top = v.Head.Line + margin - v.Height + 1
	}
	if maxTop := v.Buf.NumLines() - 1; v.Top > maxTop {
		v.Top = maxTop
	}
	if v.Top < 0 {
		v.Top = 0
	}

	// Horizontal scrolling has no margin: it tracks the cursor exactly, since
	// long lines are common and jittering sideways is worse than being tight.
	col := v.CursorCol()
	if col < v.Left {
		v.Left = col
	}
	if v.Width > 0 && col >= v.Left+v.Width {
		v.Left = col - v.Width + 1
	}
	if v.Left < 0 {
		v.Left = 0
	}
}

// Scroll moves the viewport by n lines without moving the cursor, for the
// mouse wheel. The cursor is not dragged along; that matches every GUI editor.
func (v *View) Scroll(n int) {
	v.Top += n
	if maxTop := v.Buf.NumLines() - 1; v.Top > maxTop {
		v.Top = maxTop
	}
	if v.Top < 0 {
		v.Top = 0
	}
}

// Goto places the cursor on a 1-based line number, centering the view.
func (v *View) Goto(line int) {
	p := v.Buf.Clamp(buffer.Pos{Line: line - 1})
	v.Head, v.Anchor = p, p
	v.Top = p.Line - v.Height/2
	if v.Top < 0 {
		v.Top = 0
	}
	v.goalCol = v.CursorCol()
	v.ScrollToCursor()
}

// PosAt maps a screen cell within the text area to a buffer position, for mouse
// clicks. Row and col are relative to the text area's top-left corner.
func (v *View) PosAt(row, col int) buffer.Pos {
	line := v.Top + row
	if line < 0 {
		line = 0
	}
	if max := v.Buf.NumLines() - 1; line > max {
		line = max
	}
	off := text.ColToByte(v.Buf.Line(line), col+v.Left, v.TabWidth)
	return buffer.Pos{Line: line, Col: off}
}

// Click places the cursor at a screen position, starting a selection if
// extending.
func (v *View) Click(row, col int, extend bool) {
	v.setHead(v.PosAt(row, col), extend)
}

// Cut removes the selection and returns it for the clipboard.
func (v *View) Cut() []byte {
	if !v.HasSelection() {
		return nil
	}
	s := v.SelectedText()
	v.deleteSelection()
	v.ScrollToCursor()
	return s
}

// Copy returns the selection, or the whole current line when nothing is
// selected, matching what editors do with an empty-selection copy.
func (v *View) Copy() []byte {
	if v.HasSelection() {
		return v.SelectedText()
	}
	ln := v.Buf.Line(v.Head.Line)
	out := make([]byte, 0, len(ln)+1)
	out = append(out, ln...)
	return append(out, '\n')
}

// Paste inserts s at the cursor, replacing any selection.
func (v *View) Paste(s []byte) { v.Insert(s) }
