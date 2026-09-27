package buffer

import "time"

// coalesceWindow is how long a run of similar single-character edits keeps
// merging into one undo step. Typing a word then pausing leaves two steps, so
// undo rewinds in units that match how the text was actually written.
const coalesceWindow = 600 * time.Millisecond

type opKind uint8

const (
	opInsert opKind = iota
	opDelete
)

// op is one primitive edit, recorded so it can be inverted.
type op struct {
	kind opKind
	pos  Pos    // where the text was inserted, or where it was removed from
	text []byte // what was inserted or removed
}

// txn is a group of ops undone and redone as a unit, along with where the
// cursor sat on either side so undo restores the view, not just the text.
type txn struct {
	id     uint64
	ops    []op
	before Pos
	after  Pos
	at     time.Time

	// width is how many edits one keystroke made, for a transaction made
	// by a Group: typing at several cursors. Zero for anything else.
	width int
}

type undoStack struct {
	done   []txn
	undone []txn
	nextID uint64
	saved  uint64 // id of the transaction on top when the file was last saved
	open   bool   // the newest done entry is still accepting coalesced edits

	// grouping makes every edit join one transaction, groupID, whatever
	// its shape: several cursors typing at once undo together.
	grouping bool
	groupID  uint64
	// groupOpen says the newest transaction is a Group of plain typing (or
	// of deleting) that the next keystroke's Group may continue.
	groupOpen bool
}

// advance returns the position reached by inserting s at p.
func advance(p Pos, s []byte) Pos {
	nl, last := 0, -1
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			nl++
			last = i
		}
	}
	if nl == 0 {
		return Pos{Line: p.Line, Col: p.Col + len(s)}
	}
	return Pos{Line: p.Line + nl, Col: len(s) - last - 1}
}

func hasNewline(s []byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return true
		}
	}
	return false
}

// canCoalesce reports whether o can join the transaction on top of the stack.
// Only single-line edits that continue directly from the previous one merge,
// so undo boundaries land where a person would expect them.
func (u *undoStack) canCoalesce(o op, now time.Time) bool {
	if !u.open || len(u.done) == 0 {
		return false
	}
	t := &u.done[len(u.done)-1]
	if len(t.ops) == 0 || now.Sub(t.at) > coalesceWindow {
		return false
	}
	prev := t.ops[len(t.ops)-1]
	if prev.kind != o.kind || hasNewline(prev.text) || hasNewline(o.text) {
		return false
	}
	switch o.kind {
	case opInsert:
		// Typing forward: this insert begins exactly where the last ended.
		end := advance(prev.pos, prev.text)
		return end == o.pos
	default:
		// Backspace: this delete ends exactly where the last one began.
		return o.pos.Line == prev.pos.Line && advance(o.pos, o.text) == prev.pos
	}
}

// record appends an op, either merging it into the open transaction or opening
// a new one. Any redo history is discarded, since the timeline just forked.
func (u *undoStack) record(o op, before, after Pos, now time.Time) {
	u.undone = u.undone[:0]
	if u.grouping && u.groupID != 0 && u.topID() == u.groupID {
		t := &u.done[len(u.done)-1]
		t.ops = append(t.ops, o)
		t.at = now
		return
	}
	if !u.grouping && u.canCoalesce(o, now) {
		t := &u.done[len(u.done)-1]
		t.ops = append(t.ops, o)
		t.after = after
		t.at = now
		return
	}
	u.nextID++
	u.done = append(u.done, txn{
		id:     u.nextID,
		ops:    []op{o},
		before: before,
		after:  after,
		at:     now,
	})
	u.open = !u.grouping
	if u.grouping {
		u.groupID = u.nextID
	}
}

// topID returns the id of the newest applied transaction, or 0 if none.
func (u *undoStack) topID() uint64 {
	if len(u.done) == 0 {
		return 0
	}
	return u.done[len(u.done)-1].id
}

// Group makes every edit fn makes a single undo step, which puts the cursor
// back at before when undone and at after when redone. Editing at several
// cursors at once goes through here, so one keystroke undoes as one.
func (b *Buffer) Group(before Pos, fn func() (after Pos)) {
	u := &b.undo
	continuing := u.groupOpen
	u.open, u.groupOpen = false, false
	u.grouping, u.groupID = true, 0
	after := fn()
	if id := u.groupID; id != 0 && u.topID() == id {
		n := len(u.done)
		t := &u.done[n-1]
		t.before, t.after, t.width = before, after, len(t.ops)
		// Typing a word at three cursors undoes as the word, not letter by
		// letter: a keystroke of the same plain kind, at as many cursors,
		// soon after the last, joins it.
		if kind, ok := plainKind(t.ops); ok {
			if n > 1 && continuing {
				prev := &u.done[n-2]
				if pk, ok := plainKind(prev.ops); ok && pk == kind && prev.width == t.width &&
					t.at.Sub(prev.at) <= coalesceWindow {
					prev.ops = append(prev.ops, t.ops...)
					prev.after, prev.at = after, t.at
					u.done = u.done[:n-1]
				}
			}
			u.groupOpen = true
		}
	}
	u.grouping, u.groupID = false, 0
	u.open = false
	b.modified = u.topID() != u.saved
}

// plainKind reports whether ops are all of one kind with no line breaks,
// which is what typing or deleting within lines looks like.
func plainKind(ops []op) (opKind, bool) {
	if len(ops) == 0 {
		return 0, false
	}
	for _, o := range ops {
		if o.kind != ops[0].kind || hasNewline(o.text) {
			return 0, false
		}
	}
	return ops[0].kind, true
}

// BreakUndo ends the current coalescing group, so the next edit starts a fresh
// undo step. The editor calls this when the cursor moves by navigation: typing,
// arrowing away, then typing again should undo as two steps, not one.
func (b *Buffer) BreakUndo() { b.undo.open, b.undo.groupOpen = false, false }

// Insert places s at p and returns the position just past it. Newlines in s
// split lines. The edit is recorded for undo.
func (b *Buffer) Insert(p Pos, s []byte) Pos {
	p = b.Clamp(p)
	if len(s) == 0 {
		return p
	}
	// Copy: the caller's slice may be reused, and undo holds this for the life
	// of the buffer.
	rec := make([]byte, len(s))
	copy(rec, s)

	end := b.insert(p, rec)
	b.undo.record(op{kind: opInsert, pos: p, text: rec}, p, end, time.Now())
	b.modified = b.undo.topID() != b.undo.saved
	return end
}

// Delete removes the text between from and to, in either order, and returns the
// position where the removed text began. The edit is recorded for undo.
func (b *Buffer) Delete(from, to Pos) Pos {
	from, to = b.Clamp(from), b.Clamp(to)
	if to.Less(from) {
		from, to = to, from
	}
	if from == to {
		return from
	}
	removed := b.Text(from, to)
	b.remove(from, to)
	b.undo.record(op{kind: opDelete, pos: from, text: removed}, to, from, time.Now())
	b.modified = b.undo.topID() != b.undo.saved
	return from
}

// apply runs a transaction's ops in reverse, inverting each. Used by Undo.
func (b *Buffer) revert(t txn) {
	for i := len(t.ops) - 1; i >= 0; i-- {
		o := t.ops[i]
		switch o.kind {
		case opInsert:
			b.remove(o.pos, advance(o.pos, o.text))
		default:
			b.insert(o.pos, o.text)
		}
	}
}

// replay runs a transaction's ops forward. Used by Redo.
func (b *Buffer) replay(t txn) {
	for _, o := range t.ops {
		switch o.kind {
		case opInsert:
			b.insert(o.pos, o.text)
		default:
			b.remove(o.pos, advance(o.pos, o.text))
		}
	}
}

// Undo reverses the most recent transaction and reports where the cursor
// should go. It returns false if there is nothing to undo.
func (b *Buffer) Undo() (Pos, bool) {
	if len(b.undo.done) == 0 {
		return Pos{}, false
	}
	t := b.undo.done[len(b.undo.done)-1]
	b.undo.done = b.undo.done[:len(b.undo.done)-1]
	b.revert(t)
	b.undo.undone = append(b.undo.undone, t)
	b.undo.open = false
	b.modified = b.undo.topID() != b.undo.saved
	return b.Clamp(t.before), true
}

// Redo reapplies the most recently undone transaction and reports where the
// cursor should go. It returns false if there is nothing to redo.
func (b *Buffer) Redo() (Pos, bool) {
	if len(b.undo.undone) == 0 {
		return Pos{}, false
	}
	t := b.undo.undone[len(b.undo.undone)-1]
	b.undo.undone = b.undo.undone[:len(b.undo.undone)-1]
	b.replay(t)
	b.undo.done = append(b.undo.done, t)
	b.undo.open = false
	b.modified = b.undo.topID() != b.undo.saved
	return b.Clamp(t.after), true
}

// markSaved records that the current state matches what is on disk, so undoing
// back to this point correctly clears the modified flag.
func (b *Buffer) markSaved() {
	b.undo.saved = b.undo.topID()
	b.undo.open = false
	b.modified = false
}
