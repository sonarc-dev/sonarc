package vcs

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func lines(s string) [][]byte {
	if s == "" {
		return nil
	}
	var out [][]byte
	for _, l := range strings.Split(s, "\n") {
		out = append(out, []byte(l))
	}
	return out
}

// apply rebuilds new from old and the hunks, which is what makes a diff
// correct, whatever hunks it chose.
func apply(old, new [][]byte, hs []Hunk) (string, error) {
	var out []string
	i := 0
	for _, h := range hs {
		if h.OldStart < i {
			return "", fmt.Errorf("hunks out of order: %+v", hs)
		}
		for ; i < h.OldStart; i++ {
			out = append(out, string(old[i]))
		}
		for j := 0; j < h.NewLines; j++ {
			out = append(out, string(new[h.NewStart+j]))
		}
		i += h.OldLines
	}
	for ; i < len(old); i++ {
		out = append(out, string(old[i]))
	}
	return strings.Join(out, "\n"), nil
}

func cost(hs []Hunk) int {
	n := 0
	for _, h := range hs {
		n += h.OldLines + h.NewLines
	}
	return n
}

func TestLinesFindsTheEdit(t *testing.T) {
	tests := []struct {
		name, old, new string
		want           []Hunk
	}{
		{"same", "a\nb\nc", "a\nb\nc", nil},
		{"insert", "a\nb\nc", "a\nb\nX\nc", []Hunk{{2, 0, 2, 1}}},
		{"delete", "a\nb\nc", "a\nc", []Hunk{{1, 1, 1, 0}}},
		{"change", "a\nb\nc", "a\nB\nc", []Hunk{{1, 1, 1, 1}}},
		{"two apart", "a\nb\nc\nd\ne", "A\nb\nc\nd\nE", []Hunk{{0, 1, 0, 1}, {4, 1, 4, 1}}},
		{"from empty", "", "a\nb", []Hunk{{0, 0, 0, 2}}},
		{"to empty", "a\nb", "", []Hunk{{0, 2, 0, 0}}},
		{"moved line", "a\nb\nc\nd", "b\nc\na\nd", []Hunk{{0, 1, 0, 0}, {3, 0, 2, 1}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Lines(lines(tt.old), lines(tt.new))
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// Random edits: the hunks must always rebuild the new text, and must be no
// larger than the edits that produced it.
func TestLinesRebuildsRandomEdits(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for trial := 0; trial < 500; trial++ {
		var old []string
		for i := 0; i < r.Intn(40); i++ {
			old = append(old, fmt.Sprint(r.Intn(8)))
		}
		cur := append([]string(nil), old...)
		edits := r.Intn(6)
		for e := 0; e < edits; e++ {
			switch p := r.Intn(len(cur) + 1); {
			case r.Intn(2) == 0 || len(cur) == 0 || p == len(cur):
				cur = append(cur[:p], append([]string{"new" + fmt.Sprint(e)}, cur[p:]...)...)
			default:
				cur = append(cur[:p], cur[p+1:]...)
			}
		}
		o, n := lines(strings.Join(old, "\n")), lines(strings.Join(cur, "\n"))
		hs := Lines(o, n)
		got, err := apply(o, n, hs)
		if err != nil || got != strings.Join(cur, "\n") {
			t.Fatalf("trial %d: %v\nold %q\nnew %q\nhunks %+v\nrebuilt %q", trial, err, old, cur, hs, got)
		}
		if c := cost(hs); c > edits {
			t.Fatalf("trial %d: diff costs %d lines for %d single-line edits\nold %q\nnew %q\n%+v", trial, c, edits, old, cur, hs)
		}
	}
}

// A wholesale rewrite past the search bound still gives a correct answer,
// quickly, as one hunk.
func TestLinesBoundedOnRewrite(t *testing.T) {
	var a, b []string
	for i := 0; i < 5000; i++ {
		a = append(a, fmt.Sprint("a", i))
		b = append(b, fmt.Sprint("b", i))
	}
	a[0], b[0] = "same", "same"
	o, n := lines(strings.Join(a, "\n")), lines(strings.Join(b, "\n"))
	hs := Lines(o, n)
	if len(hs) != 1 || hs[0] != (Hunk{1, 4999, 1, 4999}) {
		t.Errorf("got %+v", hs)
	}
}
