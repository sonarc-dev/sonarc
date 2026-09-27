package view

import (
	"bytes"
	"sort"

	"github.com/sonarc-dev/sonarc/internal/buffer"
	"github.com/sonarc-dev/sonarc/internal/text"
)

// Multiple cursors.
//
// Head and Anchor are the primary cursor, the one the screen follows. Extra
// holds any others. Typing, deleting and moving happen at every cursor, each
// with its own selection, through ForEach; everything else (going to a line,
// searching, jumping) works on the primary and drops the rest.

// Caret is one cursor and the other end of its selection.
type Caret struct {
	Head, Anchor buffer.Pos
	goal         int
}

// Selection returns the caret's selection in document order.
func (c Caret) Selection() (from, to buffer.Pos) {
	if c.Head.Less(c.Anchor) {
		return c.Head, c.Anchor
	}
	return c.Anchor, c.Head
}

// Carets reports how many cursors there are, the primary included.
func (v *View) Carets() int { return 1 + len(v.Extra) }

// ClearCarets goes back to one cursor, the primary.
func (v *View) ClearCarets() { v.Extra = nil }

// All returns every cursor, the primary first.
func (v *View) All() []Caret {
	return append([]Caret{{Head: v.Head, Anchor: v.Anchor, goal: v.goalCol}}, v.Extra...)
}

// setAll stores cursors, the first as the primary, merging any that touch.
func (v *View) setAll(cs []Caret) {
	primary := cs[0]
	v.Head, v.Anchor, v.goalCol = primary.Head, primary.Anchor, primary.goal
	v.Extra = append(v.Extra[:0], cs[1:]...)
	v.merge()
}

// merge folds cursors that sit on the same place or whose selections overlap
// into one, keeping the primary as primary.
func (v *View) merge() {
	if len(v.Extra) == 0 {
		return
	}
	all := v.All()
	type item struct {
		c       Caret
		primary bool
	}
	items := make([]item, len(all))
	for i, c := range all {
		items[i] = item{c, i == 0}
	}
	sort.SliceStable(items, func(i, j int) bool {
		fi, _ := items[i].c.Selection()
		fj, _ := items[j].c.Selection()
		return fi.Less(fj)
	})
	var out []item
	for _, it := range items {
		if n := len(out); n > 0 {
			last := &out[n-1]
			_, lastTo := last.c.Selection()
			from, to := it.c.Selection()
			if from.Less(lastTo) || from == lastTo && (it.c.Head == it.c.Anchor || last.c.Head == last.c.Anchor) {
				// Overlapping or touching: one selection covering both.
				lf, _ := last.c.Selection()
				end := lastTo
				if end.Less(to) {
					end = to
				}
				keep := last.c
				if it.primary {
					keep = it.c
				}
				if keep.Head.Less(keep.Anchor) {
					keep.Head, keep.Anchor = lf, end
				} else {
					keep.Anchor, keep.Head = lf, end
				}
				last.c, last.primary = keep, last.primary || it.primary
				continue
			}
		}
		out = append(out, it)
	}
	v.Extra = v.Extra[:0]
	for _, it := range out {
		if it.primary {
			v.Head, v.Anchor, v.goalCol = it.c.Head, it.c.Anchor, it.c.goal
		} else {
			v.Extra = append(v.Extra, it.c)
		}
	}
}

// ForEach runs op once for every cursor, each time with that cursor as Head
// and Anchor, last in the file first. After each run the other cursors are
// carried across its edits, so each keeps its place in the text. All the
// edits undo as one step. With one cursor it is just op.
func (v *View) ForEach(op func()) {
	if len(v.Extra) == 0 {
		op()
		return
	}
	all := v.All()
	order := make([]int, len(all))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool { return all[order[j]].Head.Less(all[order[i]].Head) })

	top := v.Top
	before := all[0].Head
	v.Buf.Group(before, func() buffer.Pos {
		for _, i := range order {
			start := v.Buf.Version()
			v.Head, v.Anchor, v.goalCol = all[i].Head, all[i].Anchor, all[i].goal
			op()
			all[i] = Caret{Head: v.Head, Anchor: v.Anchor, goal: v.goalCol}
			edits, ok := v.Buf.EditsSince(start)
			if !ok {
				continue
			}
			for j := range all {
				if j == i {
					continue
				}
				for _, e := range edits {
					all[j].Head, all[j].Anchor = e.Map(all[j].Head), e.Map(all[j].Anchor)
				}
			}
		}
		return all[0].Head
	})
	v.setAll(all)
	v.Top = top
	v.ScrollToCursor()
}

// AddCaretVertical adds a cursor on the line below the lowest cursor (n = 1)
// or above the highest (n = -1), at the same display column: how a column of
// text is edited at once.
func (v *View) AddCaretVertical(n int) bool {
	all := v.All()
	edge := all[0]
	for _, c := range all[1:] {
		if (n > 0 && edge.Head.Line < c.Head.Line) || (n < 0 && c.Head.Line < edge.Head.Line) {
			edge = c
		}
	}
	line := edge.Head.Line + n
	if line < 0 || line >= v.Buf.NumLines() {
		return false
	}
	goal := text.ByteToCol(v.Buf.Line(edge.Head.Line), edge.Head.Col, v.TabWidth)
	if edge.Head == v.Head {
		goal = v.goalCol
	}
	p := buffer.Pos{Line: line, Col: text.ColToByte(v.Buf.Line(line), goal, v.TabWidth)}
	v.Extra = append(v.Extra, Caret{Head: p, Anchor: p, goal: goal})
	v.merge()
	return true
}

// AddNextOccurrence adds a cursor selecting the next occurrence of what the
// newest cursor has selected, after it, wrapping at the end of the file. With
// nothing selected it first selects the word under the cursor, which is the
// usual first press. It reports false when there is nothing more to add.
func (v *View) AddNextOccurrence() bool {
	if !v.HasSelection() && len(v.Extra) == 0 {
		v.SelectWord(v.Head)
		return v.HasSelection()
	}
	newest := Caret{Head: v.Head, Anchor: v.Anchor}
	if len(v.Extra) > 0 {
		newest = v.Extra[len(v.Extra)-1]
	}
	from, to := newest.Selection()
	needle := v.Buf.Text(from, to)
	if len(needle) == 0 || bytes.IndexByte(needle, '\n') >= 0 {
		return false
	}
	taken := map[buffer.Pos]bool{}
	for _, c := range v.All() {
		f, _ := c.Selection()
		taken[f] = true
	}
	n := v.Buf.NumLines()
	for i := 0; i <= n; i++ {
		line := (to.Line + i) % n
		ln := v.Buf.Line(line)
		start := 0
		if i == 0 {
			start = to.Col
		}
		for start <= len(ln) {
			j := bytes.Index(ln[start:], needle)
			if j < 0 {
				break
			}
			at := buffer.Pos{Line: line, Col: start + j}
			if !taken[at] {
				end := buffer.Pos{Line: line, Col: at.Col + len(needle)}
				v.Extra = append(v.Extra, Caret{Anchor: at, Head: end})
				v.merge()
				return true
			}
			start += j + 1
		}
	}
	return false
}

// SelectAllOccurrences puts a cursor on every occurrence of the primary
// selection, or of the word under the cursor.
func (v *View) SelectAllOccurrences() int {
	if !v.HasSelection() {
		v.SelectWord(v.Head)
	}
	for v.AddNextOccurrence() {
	}
	return v.Carets()
}

// SplitSelectionIntoLines puts a cursor at the end of each line the primary
// selection covers, for editing the lines of a block at once.
func (v *View) SplitSelectionIntoLines() int {
	if !v.HasSelection() {
		return v.Carets()
	}
	from, to := v.Selection()
	var cs []Caret
	for l := from.Line; l <= to.Line; l++ {
		end := buffer.Pos{Line: l, Col: v.Buf.LineLen(l)}
		if l == to.Line {
			end = to
			if to.Col == 0 && l > from.Line {
				break // a selection ending at a line's start does not cover it
			}
		}
		cs = append(cs, Caret{Head: end, Anchor: end})
	}
	v.setAll(cs)
	return v.Carets()
}

// CopyAll is what copying takes with several cursors: each selection, or its
// cursor's whole line when it selects nothing, in document order.
func (v *View) CopyAll() [][]byte {
	all := v.All()
	sort.Slice(all, func(i, j int) bool { return all[i].Head.Less(all[j].Head) })
	var parts [][]byte
	for _, c := range all {
		from, to := c.Selection()
		if from == to {
			ln := v.Buf.Line(c.Head.Line)
			parts = append(parts, append(append([]byte{}, ln...), '\n'))
			continue
		}
		parts = append(parts, v.Buf.Text(from, to))
	}
	return parts
}

// PasteEach pastes parts[i] at the i-th cursor in document order when there
// is one part per cursor, and all of text at every cursor otherwise.
func (v *View) PasteEach(text []byte, parts [][]byte) {
	if len(parts) != v.Carets() || len(parts) < 2 {
		v.ForEach(func() { v.Insert(text) })
		return
	}
	// ForEach runs last-first, so hand out parts from the end.
	i := len(parts)
	v.ForEach(func() {
		i--
		v.Insert(bytes.TrimSuffix(parts[i], []byte("\n")))
	})
}
