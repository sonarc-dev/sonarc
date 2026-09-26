package search

import (
	"testing"
	"time"

	"github.com/sonarc-dev/sonarc/internal/buffer"
)

func buf(s string) *buffer.Buffer { return buffer.FromBytes([]byte(s)) }

func mustCompile(t *testing.T, pattern string, opts Options) *Matcher {
	t.Helper()
	m, err := Compile(pattern, opts)
	if err != nil {
		t.Fatalf("Compile(%q): %v", pattern, err)
	}
	return m
}

func TestNextFindsForward(t *testing.T) {
	b := buf("alpha beta\ngamma beta\ndelta")
	m := mustCompile(t, "beta", Options{})

	got, ok := m.Next(b, buffer.Pos{})
	if !ok {
		t.Fatal("no match found")
	}
	if got.From != (buffer.Pos{Line: 0, Col: 6}) {
		t.Errorf("first match at %v, want line 0 col 6", got.From)
	}
	if got.To != (buffer.Pos{Line: 0, Col: 10}) {
		t.Errorf("match ends at %v, want line 0 col 10", got.To)
	}

	got, ok = m.Next(b, got.To)
	if !ok {
		t.Fatal("second match not found")
	}
	if got.From != (buffer.Pos{Line: 1, Col: 6}) {
		t.Errorf("second match at %v, want line 1 col 6", got.From)
	}
}

// Repeated Find must cycle rather than stopping at the end of the file.
func TestNextWrapsAround(t *testing.T) {
	b := buf("target\nnothing\nnothing")
	m := mustCompile(t, "target", Options{})

	got, ok := m.Next(b, buffer.Pos{Line: 2, Col: 0})
	if !ok {
		t.Fatal("search did not wrap to the top")
	}
	if got.From.Line != 0 {
		t.Errorf("wrapped match on line %d, want 0", got.From.Line)
	}
}

func TestPrevFindsBackward(t *testing.T) {
	b := buf("one match\ntwo match\nthree")
	m := mustCompile(t, "match", Options{})

	got, ok := m.Prev(b, buffer.Pos{Line: 2, Col: 0})
	if !ok {
		t.Fatal("no match found searching backwards")
	}
	if got.From != (buffer.Pos{Line: 1, Col: 4}) {
		t.Errorf("match at %v, want line 1 col 4", got.From)
	}

	got, ok = m.Prev(b, got.From)
	if !ok {
		t.Fatal("second backward match not found")
	}
	if got.From != (buffer.Pos{Line: 0, Col: 4}) {
		t.Errorf("match at %v, want line 0 col 4", got.From)
	}
}

func TestPrevWrapsAround(t *testing.T) {
	b := buf("nothing\nnothing\ntarget")
	m := mustCompile(t, "target", Options{})
	got, ok := m.Prev(b, buffer.Pos{Line: 0, Col: 0})
	if !ok {
		t.Fatal("backward search did not wrap")
	}
	if got.From.Line != 2 {
		t.Errorf("wrapped match on line %d, want 2", got.From.Line)
	}
}

// Smart case is the behavior people already expect: type lowercase and get a
// loose match, type a capital and mean it.
func TestSmartCase(t *testing.T) {
	b := buf("Value\nvalue\nVALUE")

	lower := mustCompile(t, "value", Options{})
	if got := lower.All(b, 100); len(got) != 3 {
		t.Errorf("lowercase pattern matched %d lines, want all 3", len(got))
	}

	upper := mustCompile(t, "Value", Options{})
	got := upper.All(b, 100)
	if len(got) != 1 {
		t.Fatalf("pattern with a capital matched %d times, want 1", len(got))
	}
	if got[0].From.Line != 0 {
		t.Errorf("matched line %d, want 0", got[0].From.Line)
	}

	forced := mustCompile(t, "value", Options{Case: true})
	if got := forced.All(b, 100); len(got) != 1 {
		t.Errorf("forced case-sensitive matched %d times, want 1", len(got))
	}
}

func TestRegexSearch(t *testing.T) {
	b := buf("foo123bar\nfoo456bar\nnope")
	m := mustCompile(t, `foo\d+bar`, Options{Regex: true})
	got := m.All(b, 100)
	if len(got) != 2 {
		t.Fatalf("got %d matches, want 2", len(got))
	}
	if got[0].From.Col != 0 || got[0].To.Col != 9 {
		t.Errorf("match span = %v..%v", got[0].From, got[0].To)
	}
}

func TestInvalidRegexReportsError(t *testing.T) {
	if _, err := Compile("foo(", Options{Regex: true}); err == nil {
		t.Error("an unbalanced group should fail to compile")
	}
}

func TestWholeWordSearch(t *testing.T) {
	b := buf("read\nthread\nreadv\nread()")
	m := mustCompile(t, "read", Options{Word: true})
	got := m.All(b, 100)
	if len(got) != 2 {
		t.Fatalf("got %d matches at %+v, want 2 (lines 0 and 3)", len(got), got)
	}
	if got[0].From.Line != 0 || got[1].From.Line != 3 {
		t.Errorf("matched lines %d and %d, want 0 and 3", got[0].From.Line, got[1].From.Line)
	}
}

func TestAllHonorsLimit(t *testing.T) {
	b := buf("x x x x x x x x x x")
	m := mustCompile(t, "x", Options{})
	if got := m.All(b, 3); len(got) != 3 {
		t.Errorf("got %d matches, want 3", len(got))
	}
}

// An anchor-only regex matches an empty string, which would spin forever if
// the scan advanced by the match width.
func TestEmptyMatchDoesNotLoopForever(t *testing.T) {
	b := buf("a\nb\nc")
	m := mustCompile(t, "^", Options{Regex: true})
	done := make(chan []Match, 1)
	go func() { done <- m.All(b, 100) }()
	select {
	case got := <-done:
		if len(got) != 3 {
			t.Errorf("got %d matches, want one per line", len(got))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("All did not terminate on a zero-width match")
	}
}

func TestNoMatchReturnsFalse(t *testing.T) {
	b := buf("nothing here")
	m := mustCompile(t, "absent", Options{})
	if _, ok := m.Next(b, buffer.Pos{}); ok {
		t.Error("Next reported a match that does not exist")
	}
	if _, ok := m.Prev(b, b.End()); ok {
		t.Error("Prev reported a match that does not exist")
	}
	if got := m.All(b, 10); len(got) != 0 {
		t.Errorf("All returned %d matches", len(got))
	}
}

func TestEmptyPatternMatchesNothing(t *testing.T) {
	b := buf("some text")
	m := mustCompile(t, "", Options{})
	if !m.Empty() {
		t.Error("an empty pattern should report Empty")
	}
	if _, ok := m.Next(b, buffer.Pos{}); ok {
		t.Error("an empty pattern should not match")
	}
}

func TestExpandLiteralAndRegexGroups(t *testing.T) {
	b := buf("hello world")

	lit := mustCompile(t, "world", Options{})
	mt, _ := lit.Next(b, buffer.Pos{})
	if got := string(lit.Expand(b, mt, "there")); got != "there" {
		t.Errorf("literal replacement = %q, want %q", got, "there")
	}

	// A regex replacement can reference capture groups.
	re := mustCompile(t, `(\w+) (\w+)`, Options{Regex: true})
	mt, ok := re.Next(b, buffer.Pos{})
	if !ok {
		t.Fatal("regex did not match")
	}
	if got := string(re.Expand(b, mt, "$2 $1")); got != "world hello" {
		t.Errorf("group replacement = %q, want %q", got, "world hello")
	}
}

func TestSearchAcrossUnicode(t *testing.T) {
	b := buf("héllo wörld\n中文 test")
	m := mustCompile(t, "wörld", Options{})
	got, ok := m.Next(b, buffer.Pos{})
	if !ok {
		t.Fatal("did not find a multi-byte pattern")
	}
	// Positions are byte offsets: "héllo " is 7 bytes because é is two.
	if got.From.Col != 7 {
		t.Errorf("match at byte %d, want 7", got.From.Col)
	}

	m2 := mustCompile(t, "中文", Options{})
	if _, ok := m2.Next(b, buffer.Pos{}); !ok {
		t.Error("did not find CJK text")
	}
}

func TestFuzzyScoreMatching(t *testing.T) {
	tests := []struct {
		candidate, pattern string
		want               bool
	}{
		{"internal/index/find_references.go", "ifr", true},
		{"internal/index/find_references.go", "findref", true},
		{"main.c", "mc", true},
		{"main.c", "main", true},
		{"main.c", "xyz", false},
		{"main.c", "cm", false}, // out of order
		{"short", "shorter", false},
		{"anything", "", true},
	}
	for _, tt := range tests {
		_, ok := FuzzyScore(tt.candidate, tt.pattern)
		if ok != tt.want {
			t.Errorf("FuzzyScore(%q, %q) matched = %v, want %v",
				tt.candidate, tt.pattern, ok, tt.want)
		}
	}
}

// Ranking is the whole point: the obvious candidate has to come first.
func TestFuzzyScoreRanking(t *testing.T) {
	tests := []struct {
		pattern       string
		better, worse string
	}{
		// A consecutive run beats scattered letters.
		{"read", "read_write.c", "r_e_a_d_x.c"},
		// A path-segment start beats a mid-word match.
		{"buf", "lib/buffer.go", "hugebuffering.go"},
		// Shorter wins when both match well.
		{"main", "main.c", "domain_controller_main.c"},
	}
	for _, tt := range tests {
		b, okb := FuzzyScore(tt.better, tt.pattern)
		w, okw := FuzzyScore(tt.worse, tt.pattern)
		if !okb || !okw {
			t.Fatalf("pattern %q: both candidates should match (%v, %v)", tt.pattern, okb, okw)
		}
		if b <= w {
			t.Errorf("pattern %q: %q scored %d but %q scored %d; the first should rank higher",
				tt.pattern, tt.better, b, tt.worse, w)
		}
	}
}

func TestFuzzyScoreSmartCase(t *testing.T) {
	// A lowercase pattern matches regardless of case.
	if _, ok := FuzzyScore("MakeFile", "makefile"); !ok {
		t.Error("lowercase pattern should match case-insensitively")
	}
	// A pattern with a capital is taken literally.
	if _, ok := FuzzyScore("makefile", "MakeFile"); ok {
		t.Error("pattern with capitals should not match lowercase text")
	}
}
