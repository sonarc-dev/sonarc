package buffer

import (
	"math/rand"
	"strings"
	"testing"
)

// offsetOf converts a position to a byte offset in text, which the model below
// works in.
func offsetOf(text string, p Pos) int {
	lines := strings.Split(text, "\n")
	off := 0
	for i := 0; i < p.Line; i++ {
		off += len(lines[i]) + 1
	}
	return off + p.Col
}

func posOf(text string, off int) Pos {
	before := text[:off]
	line := strings.Count(before, "\n")
	return Pos{Line: line, Col: off - (strings.LastIndex(before, "\n") + 1)}
}

// A position carried through random edits by Map must land where the same
// edits move an offset in a flat string: shifted by text inserted at or
// before it, pulled back by text removed before it, and to the start of a
// removal that swallowed it.
func TestEditMapFollowsTheText(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 200; trial++ {
		model := "alpha\nbeta\ngamma\ndelta" // no final newline: the buffer keeps that as a flag, not a line
		b := FromBytes([]byte(model))
		marker := rng.Intn(len(model) + 1)
		pos := posOf(model, marker)
		seen := b.Version()

		for step := 0; step < 30; step++ {
			if rng.Intn(2) == 0 || len(model) == 0 {
				o := rng.Intn(len(model) + 1)
				s := []string{"x", "yz", "\n", "a\nb", "\n\n"}[rng.Intn(5)]
				b.Insert(posOf(model, o), []byte(s))
				model = model[:o] + s + model[o:]
				if marker >= o {
					marker += len(s)
				}
			} else {
				o1 := rng.Intn(len(model))
				o2 := o1 + 1 + rng.Intn(min(6, len(model)-o1))
				b.Delete(posOf(model, o1), posOf(model, o2))
				model = model[:o1] + model[o2:]
				switch {
				case marker >= o2:
					marker -= o2 - o1
				case marker > o1:
					marker = o1
				}
			}
		}
		edits, ok := b.EditsSince(seen)
		if !ok {
			t.Fatal("the log should reach back 60 edits")
		}
		for _, e := range edits {
			pos = e.Map(pos)
		}
		if got := offsetOf(model, pos); got != marker {
			t.Fatalf("trial %d: mapped position %v is offset %d, want %d", trial, pos, got, marker)
		}
	}
}

// A view that fell further behind than the log reaches is told so, and
// clamps instead of replaying a partial history.
func TestEditsSinceReportsWhenTheLogIsTooShort(t *testing.T) {
	b := FromBytes([]byte("x"))
	seen := b.Version()
	for i := 0; i < editLogSize+10; i++ {
		b.Insert(Pos{}, []byte("a"))
	}
	if _, ok := b.EditsSince(seen); ok {
		t.Error("EditsSince claimed a complete history past the log's end")
	}
	recent := b.Version() - 5
	if edits, ok := b.EditsSince(recent); !ok || len(edits) != 5 {
		t.Errorf("EditsSince(recent) = %d edits, %v", len(edits), ok)
	}
}
