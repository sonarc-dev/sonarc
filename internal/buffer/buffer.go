// Package buffer holds the text of one open file.
//
// Storage is a gap buffer of lines: a []line with a movable run of empty slots
// at the edit point. That gives O(1) random access by line index, which the
// renderer needs for every visible line on every frame, plus O(1) amortized
// line insert and delete near the cursor, which is where edits actually happen.
//
// Text is stored as raw bytes, never as strings or runes. Lines may contain
// invalid UTF-8, and it survives a load/save round trip untouched.
package buffer

import (
	"github.com/sonarc-dev/sonarc/internal/text"
)

// minGap is the smallest gap opened when growing, so a run of line insertions
// doesn't reallocate on every line.
const minGap = 32

// line is one line of the file: its bytes, without any terminator, plus the
// terminator it originally had. Tracking CRLF per line rather than per file
// keeps mixed-ending files byte-identical on save instead of silently
// normalizing them into a whole-file diff.
type line struct {
	text []byte
	crlf bool
}

// Pos is a location in the buffer. Col is a byte offset within the line, not a
// rune index and not a display column; use the text package to convert.
type Pos struct {
	Line int
	Col  int
}

// Less reports whether p comes before q.
func (p Pos) Less(q Pos) bool {
	if p.Line != q.Line {
		return p.Line < q.Line
	}
	return p.Col < q.Col
}

// Buffer is the text of one file.
type Buffer struct {
	lines  []line // physical storage, including the gap
	gap    int    // logical index at which the gap begins
	gaplen int    // number of empty slots in the gap

	path     string
	hadBOM   bool // file began with a UTF-8 BOM, which must be restored on save
	finalEOL bool // file ended with a line terminator
	crlf     bool // dominant line ending, used for newly created lines
	large    bool // over largeFileBytes: syntax highlighting is suppressed

	modified bool
	undo     undoStack

	// disk is the file as last read or written, to notice when something
	// else changes it.
	disk Stamp

	// version counts changes to the text, so work derived from it (the git
	// gutter) can tell whether it is still current.
	version uint64

	// edits records recent changes, for views that did not make them.
	edits editLog
}

// New returns an empty buffer holding a single blank line.
//
// It delegates to FromBytes so a new buffer is indistinguishable from one
// opened on an empty file. In particular saving an untouched new buffer writes
// an empty file, and typing "x" writes exactly "x": sonarc never appends a
// trailing newline the user did not type. Adding one is an editor-level policy
// (insertFinalNewline), not something the buffer does silently.
func New() *Buffer { return FromBytes(nil) }

// NumLines returns the number of lines. Always at least 1: an empty buffer is
// one empty line, so there is always a valid cursor position.
func (b *Buffer) NumLines() int { return len(b.lines) - b.gaplen }

// Version changes whenever the text does, undo and redo included.
func (b *Buffer) Version() uint64 { return b.version }

// Path returns the file path backing this buffer, empty for an unsaved buffer.
func (b *Buffer) Path() string { return b.path }

// Modified reports whether the buffer has unsaved changes.
func (b *Buffer) Modified() bool { return b.modified }

// Large reports whether the file is big enough that expensive per-line work
// such as syntax highlighting should be skipped.
func (b *Buffer) Large() bool { return b.large }

// CRLF reports the dominant line ending.
func (b *Buffer) CRLF() bool { return b.crlf }

// at returns a pointer to the physical slot for logical line i.
func (b *Buffer) at(i int) *line {
	if i < b.gap {
		return &b.lines[i]
	}
	return &b.lines[i+b.gaplen]
}

// Line returns the bytes of line i, without a terminator.
//
// The returned slice aliases buffer storage: read it, don't retain or mutate
// it. Callers that need to keep the bytes past the next edit must copy.
func (b *Buffer) Line(i int) []byte {
	if i < 0 || i >= b.NumLines() {
		return nil
	}
	return b.at(i).text
}

// LineLen returns the length of line i in bytes.
func (b *Buffer) LineLen(i int) int { return len(b.Line(i)) }

// Clamp adjusts p to a valid position: a line that exists, a byte offset within
// that line, and a grapheme boundary, so a stale or computed position can never
// put the cursor mid-character where the next edit would corrupt the encoding.
func (b *Buffer) Clamp(p Pos) Pos {
	if p.Line < 0 {
		return Pos{}
	}
	if n := b.NumLines(); p.Line >= n {
		p.Line = n - 1
		p.Col = b.LineLen(p.Line)
		return p
	}
	ln := b.Line(p.Line)
	if p.Col <= 0 {
		p.Col = 0
	} else if p.Col >= len(ln) {
		p.Col = len(ln)
	} else {
		p.Col = text.GraphemeAt(ln, p.Col)
	}
	return p
}

// End returns the position just past the last character in the buffer.
func (b *Buffer) End() Pos {
	last := b.NumLines() - 1
	return Pos{Line: last, Col: b.LineLen(last)}
}

// moveGap repositions the gap so that logical index to sits at its start.
func (b *Buffer) moveGap(to int) {
	if to == b.gap || b.gaplen == 0 {
		b.gap = to
		return
	}
	if to < b.gap {
		// Shift [to, gap) right by gaplen to open the gap before them.
		copy(b.lines[to+b.gaplen:b.gap+b.gaplen], b.lines[to:b.gap])
		// Clear the slots the gap now covers, [to, to+gaplen), so the buffer
		// doesn't pin byte slices that are no longer reachable. Only those:
		// when more lines moved than the gap is wide, the slots past it hold
		// lines just moved there, and clearing by the count moved erased them.
		clear(b.lines[to : to+b.gaplen])
	} else {
		// Shift [gap+gaplen, to+gaplen) left by gaplen.
		n := copy(b.lines[b.gap:to], b.lines[b.gap+b.gaplen:to+b.gaplen])
		clear(b.lines[b.gap+n : to+b.gaplen])
	}
	b.gap = to
}

// grow ensures the gap can hold at least n more lines.
func (b *Buffer) grow(n int) {
	if b.gaplen >= n {
		return
	}
	count := b.NumLines()
	want := count + n + minGap
	next := make([]line, want)
	// Copy the logical contents into the front, leaving the gap at the end.
	copy(next, b.lines[:b.gap])
	copy(next[b.gap:], b.lines[b.gap+b.gaplen:])
	b.lines = next
	b.gaplen = want - count
	// The gap now sits at the end; record that so moveGap starts from truth.
	b.gap = count
}

// insertLines splices n new lines into the buffer at logical index i.
func (b *Buffer) insertLines(i int, ls []line) {
	b.grow(len(ls))
	b.moveGap(i)
	copy(b.lines[i:], ls)
	b.gap += len(ls)
	b.gaplen -= len(ls)
}

// removeLines deletes count lines starting at logical index i.
func (b *Buffer) removeLines(i, count int) {
	b.moveGap(i)
	clear(b.lines[b.gap+b.gaplen : b.gap+b.gaplen+count])
	b.gaplen += count
}

// Text returns a copy of the bytes between from and to, with lines joined by
// '\n' regardless of the file's actual line endings. This is the buffer's
// internal representation; Save re-applies the real terminators.
func (b *Buffer) Text(from, to Pos) []byte {
	from, to = b.Clamp(from), b.Clamp(to)
	if to.Less(from) {
		from, to = to, from
	}
	if from.Line == to.Line {
		ln := b.Line(from.Line)
		out := make([]byte, to.Col-from.Col)
		copy(out, ln[from.Col:to.Col])
		return out
	}
	var out []byte
	out = append(out, b.Line(from.Line)[from.Col:]...)
	for i := from.Line + 1; i < to.Line; i++ {
		out = append(out, '\n')
		out = append(out, b.Line(i)...)
	}
	out = append(out, '\n')
	out = append(out, b.Line(to.Line)[:to.Col]...)
	return out
}

// splitLines splits s on '\n' without allocating a copy of each piece.
func splitLines(s []byte) [][]byte {
	out := [][]byte{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// insert places s at p and returns the position just past the inserted text.
// It performs no undo bookkeeping; callers go through Insert.
func (b *Buffer) insert(p Pos, s []byte) Pos {
	if len(s) == 0 {
		return p
	}
	end := b.insertText(p, s)
	b.edits.add(Edit{Version: b.version, From: p, OldEnd: p, NewEnd: end})
	return end
}

func (b *Buffer) insertText(p Pos, s []byte) Pos {
	b.version++
	parts := splitLines(s)
	cur := b.at(p.Line)

	if len(parts) == 1 {
		// Fast path: no newline, so only one line changes.
		nl := make([]byte, 0, len(cur.text)+len(parts[0]))
		nl = append(nl, cur.text[:p.Col]...)
		nl = append(nl, parts[0]...)
		nl = append(nl, cur.text[p.Col:]...)
		cur.text = nl
		return Pos{Line: p.Line, Col: p.Col + len(parts[0])}
	}

	// The tail of the current line moves to the end of the last inserted line.
	tail := make([]byte, len(cur.text)-p.Col)
	copy(tail, cur.text[p.Col:])

	head := make([]byte, 0, p.Col+len(parts[0]))
	head = append(head, cur.text[:p.Col]...)
	head = append(head, parts[0]...)
	// The original line keeps its own terminator only if it isn't being split;
	// since it is, the new final line inherits it.
	origCRLF := cur.crlf
	cur.text = head
	cur.crlf = b.crlf

	news := make([]line, len(parts)-1)
	for i := 1; i < len(parts); i++ {
		ln := line{crlf: b.crlf}
		if i == len(parts)-1 {
			ln.text = append(append(make([]byte, 0, len(parts[i])+len(tail)), parts[i]...), tail...)
			ln.crlf = origCRLF
		} else {
			ln.text = append(make([]byte, 0, len(parts[i])), parts[i]...)
		}
		news[i-1] = ln
	}
	b.insertLines(p.Line+1, news)

	last := p.Line + len(parts) - 1
	return Pos{Line: last, Col: len(parts[len(parts)-1])}
}

// remove deletes the text between from and to. It performs no undo
// bookkeeping; callers go through Delete.
func (b *Buffer) remove(from, to Pos) {
	b.version++
	b.edits.add(Edit{Version: b.version, From: from, OldEnd: to, NewEnd: from})
	if from.Line == to.Line {
		cur := b.at(from.Line)
		cur.text = append(cur.text[:from.Col], cur.text[to.Col:]...)
		return
	}
	// Join the head of the first line to the tail of the last, then drop the
	// lines in between along with the last.
	first := b.at(from.Line)
	last := b.at(to.Line)
	joined := make([]byte, 0, from.Col+len(last.text)-to.Col)
	joined = append(joined, first.text[:from.Col]...)
	joined = append(joined, last.text[to.Col:]...)
	first.text = joined
	first.crlf = last.crlf
	b.removeLines(from.Line+1, to.Line-from.Line)
}
