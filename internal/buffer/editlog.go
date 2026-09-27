package buffer

// Edit is one change to the text: [From, OldEnd) was replaced by text now
// ending at NewEnd. An insertion has OldEnd == From, a removal NewEnd == From.
// Views that did not make an edit replay it to keep their cursor on the same
// text, as a second pane on the same file must.
type Edit struct {
	Version      uint64 // the buffer's version once this edit was made
	From, OldEnd Pos
	NewEnd       Pos
}

// editLogSize bounds how far back a view can catch up edit by edit; one that
// fell further behind just clamps its positions.
const editLogSize = 256

type editLog struct {
	ring [editLogSize]Edit
	n    int // edits recorded in total
}

func (l *editLog) add(e Edit) {
	l.ring[l.n%editLogSize] = e
	l.n++
}

// EditsSince returns the edits made after version, oldest first. ok is false
// when the log no longer reaches back that far.
func (b *Buffer) EditsSince(version uint64) (edits []Edit, ok bool) {
	l := &b.edits
	if version >= b.version {
		return nil, true
	}
	oldest := max(l.n-editLogSize, 0)
	if l.n == 0 || l.ring[oldest%editLogSize].Version > version+1 {
		return nil, false
	}
	for i := oldest; i < l.n; i++ {
		if e := l.ring[i%editLogSize]; e.Version > version {
			edits = append(edits, e)
		}
	}
	return edits, true
}

// Map moves p across the edit: text before the edit keeps its position, text
// after it shifts with it, and a position inside removed text lands where the
// removal started.
func (e Edit) Map(p Pos) Pos {
	switch {
	case p.Less(e.From):
		return p
	case p.Less(e.OldEnd):
		return e.From
	case p.Line == e.OldEnd.Line:
		return Pos{Line: e.NewEnd.Line, Col: e.NewEnd.Col + p.Col - e.OldEnd.Col}
	default:
		return Pos{Line: p.Line + e.NewEnd.Line - e.OldEnd.Line, Col: p.Col}
	}
}
