package buffer

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// all returns the entire buffer as text, for comparison against a reference.
func all(b *Buffer) []byte { return b.Text(Pos{}, b.End()) }

// The central promise: opening a file and saving it back without editing must
// reproduce the original bytes exactly, whatever is in it.
func TestRoundTripIsByteIdentical(t *testing.T) {
	inputs := []struct {
		name string
		data string
	}{
		{"empty", ""},
		{"no trailing newline", "hello"},
		{"trailing newline", "hello\n"},
		{"just a newline", "\n"},
		{"blank lines preserved", "a\n\n\nb\n"},
		{"crlf throughout", "a\r\nb\r\n"},
		{"crlf without final newline", "a\r\nb"},
		{"mixed endings must not be normalized", "lf\ncrlf\r\nlf\n"},
		{"bom preserved", "\xEF\xBB\xBFhello\n"},
		{"bom with crlf", "\xEF\xBB\xBFa\r\nb\r\n"},
		{"invalid utf8 survives", "good\n\xff\xfe bad\nend\n"},
		{"nul bytes survive", "a\x00b\n"},
		{"lone cr is not a line break", "a\rb\n"},
		{"trailing whitespace kept", "a   \n\tb\t\n"},
	}
	for _, tt := range inputs {
		t.Run(tt.name, func(t *testing.T) {
			b := FromBytes([]byte(tt.data))
			if got := string(b.Bytes()); got != tt.data {
				t.Errorf("round trip changed the file:\n got %q\nwant %q", got, tt.data)
			}
		})
	}
}

func TestLineSplitting(t *testing.T) {
	tests := []struct {
		data  string
		lines []string
	}{
		{"", []string{""}},
		{"a", []string{"a"}},
		{"a\n", []string{"a"}},
		{"a\nb", []string{"a", "b"}},
		{"a\nb\n", []string{"a", "b"}},
		{"\n\n", []string{"", ""}},
		{"a\r\nb", []string{"a", "b"}},
	}
	for _, tt := range tests {
		b := FromBytes([]byte(tt.data))
		if b.NumLines() != len(tt.lines) {
			t.Errorf("%q: NumLines = %d, want %d", tt.data, b.NumLines(), len(tt.lines))
			continue
		}
		for i, want := range tt.lines {
			if got := string(b.Line(i)); got != want {
				t.Errorf("%q: line %d = %q, want %q", tt.data, i, got, want)
			}
		}
	}
}

func TestInsert(t *testing.T) {
	tests := []struct {
		name  string
		start string
		at    Pos
		text  string
		want  string
		end   Pos
	}{
		{"into empty", "", Pos{0, 0}, "hi", "hi", Pos{0, 2}},
		{"at line start", "world", Pos{0, 0}, "hello ", "hello world", Pos{0, 6}},
		{"at line end", "hello", Pos{0, 5}, "!", "hello!", Pos{0, 6}},
		{"in the middle", "helo", Pos{0, 3}, "l", "hello", Pos{0, 4}},
		{"newline splits a line", "ab", Pos{0, 1}, "\n", "a\nb", Pos{1, 0}},
		{"multi-line insert", "ad", Pos{0, 1}, "b\nc", "ab\ncd", Pos{1, 1}},
		{"trailing newline", "ab", Pos{0, 2}, "\n", "ab\n", Pos{1, 0}},
		{"several lines at once", "x", Pos{0, 1}, "\n1\n2\n3", "x\n1\n2\n3", Pos{3, 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := FromBytes([]byte(tt.start))
			end := b.Insert(tt.at, []byte(tt.text))
			if got := string(all(b)); got != tt.want {
				t.Errorf("text = %q, want %q", got, tt.want)
			}
			if end != tt.end {
				t.Errorf("end = %v, want %v", end, tt.end)
			}
		})
	}
}

func TestDelete(t *testing.T) {
	tests := []struct {
		name     string
		start    string
		from, to Pos
		want     string
	}{
		{"within a line", "hello", Pos{0, 1}, Pos{0, 3}, "hlo"},
		{"whole line content", "hello", Pos{0, 0}, Pos{0, 5}, ""},
		{"joins two lines", "ab\ncd", Pos{0, 2}, Pos{1, 0}, "abcd"},
		{"across lines", "ab\ncd", Pos{0, 1}, Pos{1, 1}, "ad"},
		{"spanning three lines", "a\nb\nc", Pos{0, 1}, Pos{2, 0}, "ac"},
		{"reversed range is normalized", "hello", Pos{0, 3}, Pos{0, 1}, "hlo"},
		{"empty range is a no-op", "hello", Pos{0, 2}, Pos{0, 2}, "hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := FromBytes([]byte(tt.start))
			b.Delete(tt.from, tt.to)
			if got := string(all(b)); got != tt.want {
				t.Errorf("text = %q, want %q", got, tt.want)
			}
		})
	}
}

// The gap buffer is the part most likely to hide an index bug, and the bug
// would corrupt the user's file. Hammer it against a trivially-correct model.
func TestGapBufferAgainstReferenceModel(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	b := FromBytes([]byte("initial\ntext\nhere"))
	ref := []byte("initial\ntext\nhere")

	// flat converts a Pos to an offset in the reference byte slice.
	flat := func(p Pos) int {
		off, line := 0, 0
		for i := 0; i < len(ref) && line < p.Line; i++ {
			if ref[i] == '\n' {
				line++
				off = i + 1
			}
		}
		return off + p.Col
	}
	randPos := func() Pos {
		l := rng.Intn(b.NumLines())
		c := 0
		if n := b.LineLen(l); n > 0 {
			c = rng.Intn(n + 1)
		}
		return Pos{Line: l, Col: c}
	}

	words := []string{"x", "ab", "\n", "a\nb", "\n\n", "hello world", "\tq"}
	for step := 0; step < 4000; step++ {
		if rng.Intn(2) == 0 {
			p := randPos()
			s := words[rng.Intn(len(words))]
			b.Insert(p, []byte(s))
			off := flat(p)
			ref = append(ref[:off], append([]byte(s), ref[off:]...)...)
		} else {
			p, q := randPos(), randPos()
			if q.Less(p) {
				p, q = q, p
			}
			b.Delete(p, q)
			lo, hi := flat(p), flat(q)
			ref = append(ref[:lo], ref[hi:]...)
		}
		if got := all(b); !bytes.Equal(got, ref) {
			t.Fatalf("step %d: buffer diverged from model\n got %q\nwant %q", step, got, ref)
		}
	}
}

func TestUndoRedo(t *testing.T) {
	b := FromBytes([]byte("hello"))
	b.Insert(Pos{0, 5}, []byte(" world"))
	if got := string(all(b)); got != "hello world" {
		t.Fatalf("after insert = %q", got)
	}

	pos, ok := b.Undo()
	if !ok {
		t.Fatal("Undo returned false")
	}
	if got := string(all(b)); got != "hello" {
		t.Errorf("after undo = %q, want %q", got, "hello")
	}
	if pos != (Pos{0, 5}) {
		t.Errorf("undo cursor = %v, want {0 5}", pos)
	}

	pos, ok = b.Redo()
	if !ok {
		t.Fatal("Redo returned false")
	}
	if got := string(all(b)); got != "hello world" {
		t.Errorf("after redo = %q, want %q", got, "hello world")
	}
	if pos != (Pos{0, 11}) {
		t.Errorf("redo cursor = %v, want {0 11}", pos)
	}
}

func TestUndoOnEmptyStackIsSafe(t *testing.T) {
	b := New()
	if _, ok := b.Undo(); ok {
		t.Error("Undo on a fresh buffer should report false")
	}
	if _, ok := b.Redo(); ok {
		t.Error("Redo on a fresh buffer should report false")
	}
}

// Typing a word should undo as one step, but a deliberate break must split it,
// so undo rewinds in units matching how the text was written.
func TestUndoCoalescing(t *testing.T) {
	b := New()
	p := Pos{0, 0}
	for _, c := range "hello" {
		p = b.Insert(p, []byte(string(c)))
	}
	if _, ok := b.Undo(); !ok {
		t.Fatal("Undo returned false")
	}
	if got := string(all(b)); got != "" {
		t.Errorf("typing a word should undo in one step, got %q", got)
	}

	b = New()
	p = Pos{0, 0}
	p = b.Insert(p, []byte("abc"))
	b.BreakUndo()
	b.Insert(p, []byte("def"))
	b.Undo()
	if got := string(all(b)); got != "abc" {
		t.Errorf("after break, undo = %q, want %q", got, "abc")
	}
}

// A newline must never merge into a coalesced run, or undo swallows structure.
func TestUndoDoesNotCoalesceAcrossNewline(t *testing.T) {
	b := New()
	p := b.Insert(Pos{0, 0}, []byte("a"))
	p = b.Insert(p, []byte("\n"))
	b.Insert(p, []byte("b"))
	b.Undo()
	if got := string(all(b)); got != "a\n" {
		t.Errorf("undo = %q, want %q", got, "a\n")
	}
}

// Undoing back to the last saved state must clear the modified flag, or the
// editor nags about changes that no longer exist.
func TestModifiedFlagTracksSavedState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if b.Modified() {
		t.Error("freshly opened buffer reports modified")
	}
	b.Insert(Pos{0, 5}, []byte("!"))
	if !b.Modified() {
		t.Error("edited buffer does not report modified")
	}
	if _, ok := b.Undo(); !ok {
		t.Fatal("Undo returned false")
	}
	if b.Modified() {
		t.Error("undoing back to the saved state should clear modified")
	}
}

func TestSaveIsAtomicAndPreservesMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	b.Insert(Pos{0, 0}, []byte("new "))
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new original\n" {
		t.Errorf("saved %q, want %q", got, "new original\n")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	if b.Modified() {
		t.Error("buffer still reports modified after save")
	}

	// No temp files may survive a successful save.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "sonarc") {
			t.Errorf("leftover temp file %s", e.Name())
		}
	}
}

// Saving through a symlink must update the target, not replace the link.
func TestSaveFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.txt")
	link := filepath.Join(dir, "link.txt")
	if err := os.WriteFile(target, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	b, err := Open(link)
	if err != nil {
		t.Fatal(err)
	}
	b.Insert(Pos{0, 0}, []byte("y"))
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("save replaced the symlink instead of writing through it")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "yx\n" {
		t.Errorf("target = %q, want %q", got, "yx\n")
	}
	// The buffer keeps the name it was opened by, and a second save still
	// sees no outside change.
	if b.Path() != link {
		t.Errorf("path after save = %q, want the link %q", b.Path(), link)
	}
	if st, _ := b.OnDisk(); st != DiskSame {
		t.Errorf("after save the file reads as changed on disk (%v)", st)
	}
}

func TestOpenMissingFileGivesEmptyBoundBuffer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.txt")
	b, err := Open(path)
	if err != nil {
		t.Fatalf("opening a nonexistent file should succeed, got %v", err)
	}
	if b.NumLines() != 1 || b.LineLen(0) != 0 {
		t.Errorf("expected one empty line, got %d lines", b.NumLines())
	}
	if b.Path() != path {
		t.Errorf("path = %q, want %q", b.Path(), path)
	}
	b.Insert(Pos{0, 0}, []byte("created"))
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "created" {
		t.Errorf("file = %q, want %q", got, "created")
	}
}

func TestClampSnapsToGraphemeBoundary(t *testing.T) {
	b := FromBytes([]byte("中文\nok"))
	if got := b.Clamp(Pos{0, 1}); got != (Pos{0, 0}) {
		t.Errorf("mid-rune clamp = %v, want {0 0}", got)
	}
	if got := b.Clamp(Pos{0, 99}); got != (Pos{0, 6}) {
		t.Errorf("past-end clamp = %v, want {0 6}", got)
	}
	if got := b.Clamp(Pos{99, 0}); got != (Pos{1, 2}) {
		t.Errorf("past-last-line clamp = %v, want {1 2}", got)
	}
	if got := b.Clamp(Pos{-1, -1}); got != (Pos{0, 0}) {
		t.Errorf("negative clamp = %v, want {0 0}", got)
	}
}

// benchTyping measures one keystroke typed and deleted mid-line, so the line
// stays at its real length. An earlier benchmark kept inserting into one line,
// which grew it to tens of thousands of characters and reported 34 KB and
// 115 µs per keystroke for a cost that does not occur in practice.
//
// Each keystroke copies the line it edits. That is deliberate: lines alias the
// bytes read from the file, and a fresh copy is what keeps an insert from
// overwriting the next line. It is linear in the length of the line only, and
// measured at 1.3 µs for 80 columns and 0.9 ms for a 100,000-character
// minified line — nowhere near a frame.
func benchTyping(b *testing.B, lineLen int) {
	line := bytes.Repeat([]byte("x"), lineLen)
	buf := FromBytes(bytes.Repeat(append(line, '\n'), 1000))
	p := Pos{Line: 500, Col: lineLen / 2}
	b.ReportAllocs()
	for b.Loop() {
		end := buf.Insert(p, []byte("y"))
		buf.Delete(p, end)
	}
}

func BenchmarkTypeShortLine(b *testing.B)    { benchTyping(b, 80) }
func BenchmarkTypeMinifiedLine(b *testing.B) { benchTyping(b, 100_000) }

func BenchmarkInsertLine(b *testing.B) {
	buf := FromBytes(bytes.Repeat([]byte("some line of source code\n"), 10000))
	b.ResetTimer()
	for b.Loop() {
		buf.Insert(Pos{Line: 5000, Col: 0}, []byte("new\n"))
	}
}
