package syntax

import (
	"strings"
	"testing"

	"sonarc/internal/buffer"
)

// classes renders one line's highlighting as a string, one character per byte,
// so an expectation reads as a picture of the coloring lined up under the code.
//
//	k keyword   t type   s string   c comment   n number   f func   p preproc
//	. plain
func classes(l *Language, line string, in State) (string, State) {
	toks, out := l.Scan([]byte(line), in, nil)
	b := []byte(strings.Repeat(".", len(line)))
	for _, tk := range toks {
		ch := byte('.')
		switch tk.Class {
		case ClassKeyword:
			ch = 'k'
		case ClassType:
			ch = 't'
		case ClassString:
			ch = 's'
		case ClassComment:
			ch = 'c'
		case ClassNumber:
			ch = 'n'
		case ClassFunc:
			ch = 'f'
		case ClassPreproc:
			ch = 'p'
		}
		for i := tk.Start; i < tk.End && i < len(b); i++ {
			b[i] = ch
		}
	}
	return string(b), out
}

func lang(t *testing.T, name string) *Language {
	t.Helper()
	l := ByName(name)
	if l == nil {
		t.Fatalf("no language named %q", name)
	}
	return l
}

func TestScanC(t *testing.T) {
	l := lang(t, "c")
	tests := []struct {
		line string
		want string
	}{
		{`int x = 42;`, `ttt.....nn.`},
		{`return foo(1);`, `kkkkkk.fff.n..`},
		{`// a comment`, `cccccccccccc`},
		{`x = "str"; // tail`, `....sssss..ccccccc`},
		{`#include <stdio.h>`, `pppppppp..........`},
		{`  #define MAX 10`, `..ppppppp.....nn`},
		{`char c = '\n';`, `tttt.....ssss.`},
		{`if (x) { y(); }`, `kk.....` + `.` + `.f..` + `..` + `.`},
		// A string containing what looks like a comment must stay a string.
		{`s = "// not a comment";`, `....ssssssssssssssssss.`},
		// An escaped quote must not end the string early.
		{`s = "a\"b";`, `....ssssss.`},
	}
	for _, tt := range tests {
		got, _ := classes(l, tt.line, stateNormal)
		if got != tt.want {
			t.Errorf("line %q\n got %s\nwant %s", tt.line, got, tt.want)
		}
	}
}

// A block comment spans lines, so the state carried between them has to be
// right or everything after an opening /* is mis-colored.
func TestBlockCommentSpansLines(t *testing.T) {
	l := lang(t, "c")

	got, st := classes(l, `int a; /* start`, stateNormal)
	if want := `ttt....cccccccc`; got != want {
		t.Errorf("opening line:\n got %s\nwant %s", got, want)
	}
	if st.kind() != stateBlock {
		t.Fatal("state after an unterminated /* is not 'in block comment'")
	}

	got, st = classes(l, `still inside`, st)
	if want := `cccccccccccc`; got != want {
		t.Errorf("middle line:\n got %s\nwant %s", got, want)
	}
	if st.kind() != stateBlock {
		t.Error("state should still be 'in block comment'")
	}

	got, st = classes(l, `end */ int b;`, st)
	if want := `cccccc.ttt...`; got != want {
		t.Errorf("closing line:\n got %s\nwant %s", got, want)
	}
	if st != stateNormal {
		t.Error("state should be normal after the comment closes")
	}
}

func TestNestedBlockComments(t *testing.T) {
	l := lang(t, "rust")
	_, st := classes(l, `/* outer /* inner`, stateNormal)
	if st.kind() != stateBlock || st.depth() != 2 {
		t.Fatalf("depth = %d, want 2 nested comments open", st.depth())
	}
	_, st = classes(l, `*/ still in outer`, st)
	if st.kind() != stateBlock || st.depth() != 1 {
		t.Errorf("depth = %d, want 1 after closing the inner comment", st.depth())
	}
	_, st = classes(l, `*/ done`, st)
	if st != stateNormal {
		t.Error("state should be normal after closing the outer comment")
	}
}

func TestGoRawStrings(t *testing.T) {
	l := lang(t, "go")
	got, st := classes(l, "s := `raw", stateNormal)
	if want := `.....ssss`; got != want {
		t.Errorf("opening line:\n got %s\nwant %s", got, want)
	}
	if st.kind() != stateRaw {
		t.Fatal("state after an unterminated backtick is not 'in raw string'")
	}
	// A raw string ignores escapes and comment markers entirely.
	got, st = classes(l, "// still raw", st)
	if want := `ssssssssssss`; got != want {
		t.Errorf("continuation:\n got %s\nwant %s", got, want)
	}
	got, st = classes(l, "end` + x", st)
	if want := `ssss....`; got != want {
		t.Errorf("closing line:\n got %s\nwant %s", got, want)
	}
	if st != stateNormal {
		t.Error("state should be normal after the raw string closes")
	}
}

func TestPythonTripleQuotes(t *testing.T) {
	l := lang(t, "python")
	got, st := classes(l, `doc = """start`, stateNormal)
	if want := `......ssssssss`; got != want {
		t.Errorf("opening:\n got %s\nwant %s", got, want)
	}
	if st.kind() != stateTriple {
		t.Fatal("state is not 'in triple-quoted string'")
	}
	// A single quote of the other kind must not close it.
	_, st = classes(l, `it's fine ''' no`, st)
	if st.kind() != stateTriple {
		t.Error(`''' should not close a """ string`)
	}
	_, st = classes(l, `end"""`, st)
	if st != stateNormal {
		t.Error("state should be normal after the closing triple quote")
	}
}

func TestScanGo(t *testing.T) {
	l := lang(t, "go")
	tests := []struct{ line, want string }{
		{`func main() {`, `kkkk.ffff....`},
		{`var x int = 3`, `kkk...ttt...n`},
		{`return nil`, `kkkkkk.ttt`},
		{`s := "hi" // c`, `.....ssss.cccc`},
	}
	for _, tt := range tests {
		got, _ := classes(l, tt.line, stateNormal)
		if got != tt.want {
			t.Errorf("line %q\n got %s\nwant %s", tt.line, got, tt.want)
		}
	}
}

func TestScanPython(t *testing.T) {
	l := lang(t, "python")
	tests := []struct{ line, want string }{
		{`def main():`, `kkk.ffff...`},
		{`x = 3.14  # pi`, `....nnnn..cccc`},
		{`if True: pass`, `kk.tttt..kkkk`},
	}
	for _, tt := range tests {
		got, _ := classes(l, tt.line, stateNormal)
		if got != tt.want {
			t.Errorf("line %q\n got %s\nwant %s", tt.line, got, tt.want)
		}
	}
}

// A digit inside an identifier is part of the name, not a number.
func TestNumbersOnlyAtTokenBoundaries(t *testing.T) {
	l := lang(t, "c")
	for _, tt := range []struct{ line, want string }{
		{`utf8_len`, `........`},
		{`x1 = 1`, `.....n`},
		{`0xFF`, `nnnn`},
		{`1e-9`, `nnnn`},
		{`3.14f`, `nnnnn`},
	} {
		got, _ := classes(l, tt.line, stateNormal)
		if got != tt.want {
			t.Errorf("line %q\n got %s\nwant %s", tt.line, got, tt.want)
		}
	}
}

func TestDetect(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"main.c", "c"},
		{"vfs.h", "c"},
		{"widget.cpp", "c"},
		{"server.go", "go"},
		{"app.py", "python"},
		{"lib.rs", "rust"},
		{"index.tsx", "javascript"},
		{"Main.java", "java"},
		{"build.sh", "shell"},
		{"Makefile", "make"},
		{"GNUmakefile", "make"},
		{"data.json", "json"},
		{"conf.yml", "yaml"},
		{"/deep/path/to/file.C", "c"}, // extension match is case-insensitive
		{"README", ""},
		{"noext", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := Detect(tt.path)
		name := ""
		if got != nil {
			name = got.Name
		}
		if name != tt.want {
			t.Errorf("Detect(%q) = %q, want %q", tt.path, name, tt.want)
		}
	}
}

// The state cache must produce the same answer as scanning from the top, or
// scrolling into a file would color it differently than opening at that point.
func TestHighlighterCacheMatchesFullScan(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		switch i % 7 {
		case 0:
			sb.WriteString("/* a block comment\n")
		case 1:
			sb.WriteString("still in the comment\n")
		case 2:
			sb.WriteString("closing it */ int x;\n")
		case 3:
			sb.WriteString("#define FOO 1\n")
		default:
			sb.WriteString("int value = 42; // trailing\n")
		}
	}
	b := buffer.FromBytes([]byte(sb.String()))
	l := lang(t, "c")

	// Reference: scan every line in order from the top.
	want := make([]string, b.NumLines())
	state := stateNormal
	for i := 0; i < b.NumLines(); i++ {
		var s string
		s, state = classes(l, string(b.Line(i)), state)
		want[i] = s
	}

	// Jumping straight to a line deep in the file must agree.
	for _, probe := range []int{0, 1, 5, 100, 250, 499} {
		h := New(l)
		toks := h.Tokens(b, probe, nil)
		got := renderTokens(string(b.Line(probe)), toks)
		if got != want[probe] {
			t.Errorf("line %d after a direct jump:\n got %s\nwant %s", probe, got, want[probe])
		}
	}
}

// renderTokens is classes() for an already-scanned line.
func renderTokens(line string, toks []Token) string {
	b := []byte(strings.Repeat(".", len(line)))
	for _, tk := range toks {
		ch := byte('.')
		switch tk.Class {
		case ClassKeyword:
			ch = 'k'
		case ClassType:
			ch = 't'
		case ClassString:
			ch = 's'
		case ClassComment:
			ch = 'c'
		case ClassNumber:
			ch = 'n'
		case ClassFunc:
			ch = 'f'
		case ClassPreproc:
			ch = 'p'
		}
		for i := tk.Start; i < tk.End && i < len(b); i++ {
			b[i] = ch
		}
	}
	return string(b)
}

// Editing a line must re-color the lines after it: typing "/*" turns the rest
// of the file into a comment, and the cache has to notice.
func TestInvalidateRecolorsFollowingLines(t *testing.T) {
	b := buffer.FromBytes([]byte("int a;\nint b;\nint c;\n"))
	l := lang(t, "c")
	h := New(l)

	if got := renderTokens(string(b.Line(2)), h.Tokens(b, 2, nil)); got != "ttt..." {
		t.Fatalf("initial line 2 = %s", got)
	}

	// Open a block comment on line 0 and tell the highlighter about it.
	b.Insert(buffer.Pos{Line: 0, Col: 0}, []byte("/* "))
	h.Invalidate(0)

	if got := renderTokens(string(b.Line(2)), h.Tokens(b, 2, nil)); got != "cccccc" {
		t.Errorf("after opening a comment, line 2 = %s, want all comment", got)
	}
}

func TestNilLanguageProducesNoTokens(t *testing.T) {
	b := buffer.FromBytes([]byte("anything at all\n"))
	h := New(nil)
	if toks := h.Tokens(b, 0, nil); len(toks) != 0 {
		t.Errorf("a nil language produced %d tokens", len(toks))
	}
	// And must not panic on out-of-range lines.
	if toks := h.Tokens(b, 99, nil); len(toks) != 0 {
		t.Errorf("out-of-range line produced %d tokens", len(toks))
	}
}

func TestClassAt(t *testing.T) {
	toks := []Token{
		{Start: 0, End: 3, Class: ClassKeyword},
		{Start: 8, End: 12, Class: ClassString},
	}
	for _, tt := range []struct {
		off  int
		want Class
	}{
		{0, ClassKeyword},
		{2, ClassKeyword},
		{3, ClassNone},
		{5, ClassNone},
		{8, ClassString},
		{11, ClassString},
		{12, ClassNone},
		{99, ClassNone},
	} {
		if got := ClassAt(toks, tt.off); got != tt.want {
			t.Errorf("ClassAt(%d) = %v, want %v", tt.off, got, tt.want)
		}
	}
}

func BenchmarkScanCLine(b *testing.B) {
	l := ByName("c")
	line := []byte("\tif (unlikely(err = vfs_read(file, buf, 4096, &pos)) < 0) { /* x */")
	var toks []Token
	b.SetBytes(int64(len(line)))
	for b.Loop() {
		toks, _ = l.Scan(line, stateNormal, toks)
	}
}

func BenchmarkHighlightViewport(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 5000; i++ {
		sb.WriteString("\tint value = compute(a, b); // a trailing comment\n")
	}
	buf := buffer.FromBytes([]byte(sb.String()))
	l := ByName("c")
	b.ResetTimer()
	for b.Loop() {
		h := New(l)
		var toks []Token
		// One screenful, the way a frame renders it.
		for i := 2000; i < 2050; i++ {
			toks = h.Tokens(buf, i, toks)
		}
	}
}
