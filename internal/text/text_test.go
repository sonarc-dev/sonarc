package text

import (
	"testing"
)

func TestWidth(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		tabWidth int
		want     int
	}{
		{"empty", "", 4, 0},
		{"ascii", "hello", 4, 5},
		{"leading tab", "\tx", 4, 5},
		{"tab mid-word snaps to stop", "ab\tc", 4, 5},
		{"tab already at stop advances full width", "abcd\te", 4, 9},
		{"tab width 8", "\t", 8, 8},
		{"two tabs", "\t\t", 4, 8},
		{"control char is caret notation", "a\x01b", 4, 4},
		{"del is caret notation", "\x7f", 4, 2},
		{"cjk is double width", "中文", 4, 4},
		{"invalid byte renders as hex", "a\xffb", 4, 6},
		{"combining mark joins base", "é", 4, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Width([]byte(tt.line), tt.tabWidth); got != tt.want {
				t.Errorf("Width(%q) = %d, want %d", tt.line, got, tt.want)
			}
		})
	}
}

func TestByteToCol(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		byteOff  int
		tabWidth int
		want     int
	}{
		{"start", "hello", 0, 4, 0},
		{"mid ascii", "hello", 3, 4, 3},
		{"end", "hello", 5, 4, 5},
		{"past end clamps", "hello", 99, 4, 5},
		{"negative clamps", "hello", -5, 4, 0},
		{"after tab", "\tx", 1, 4, 4},
		{"after cjk", "中x", 3, 4, 2},
		{"after invalid byte", "\xffx", 1, 4, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ByteToCol([]byte(tt.line), tt.byteOff, tt.tabWidth); got != tt.want {
				t.Errorf("ByteToCol(%q, %d) = %d, want %d", tt.line, tt.byteOff, got, tt.want)
			}
		})
	}
}

func TestColToByte(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		col      int
		tabWidth int
		want     int
	}{
		{"start", "hello", 0, 4, 0},
		{"mid", "hello", 3, 4, 3},
		{"past end clamps to len", "hello", 99, 4, 5},
		// A column inside a tab must resolve to the tab's own offset, not past
		// it, or the cursor lands in the middle of the whitespace run.
		{"inside tab resolves to tab start", "\tx", 2, 4, 0},
		{"just after tab", "\tx", 4, 4, 1},
		// Likewise the trailing half of a wide rune belongs to that rune.
		{"second half of cjk", "中x", 1, 4, 0},
		{"after cjk", "中x", 2, 4, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ColToByte([]byte(tt.line), tt.col, tt.tabWidth); got != tt.want {
				t.Errorf("ColToByte(%q, %d) = %d, want %d", tt.line, tt.col, got, tt.want)
			}
		})
	}
}

// The cursor must never land mid-character, so converting a column to a byte
// offset and back must be stable: the second conversion changes nothing.
func TestColByteRoundTripIsStable(t *testing.T) {
	lines := []string{
		"hello world",
		"\tindented",
		"a\tb\tc",
		"中文mixed中",
		"café é",
		"bad\xffbytes\xfe",
		"ctrl\x01\x02chars",
		"",
	}
	for _, s := range lines {
		line := []byte(s)
		w := Width(line, 4)
		for col := 0; col <= w+2; col++ {
			off := ColToByte(line, col, 4)
			back := ByteToCol(line, off, 4)
			again := ColToByte(line, back, 4)
			if off != again {
				t.Errorf("line %q col %d: unstable round trip: off=%d back=%d again=%d",
					s, col, off, back, again)
			}
			if off < 0 || off > len(line) {
				t.Errorf("line %q col %d: offset %d out of range", s, col, off)
			}
		}
	}
}

func TestGraphemeMovement(t *testing.T) {
	tests := []struct {
		name string
		line string
		// offsets that NextGrapheme should visit, walking from 0 to len.
		want []int
	}{
		{"ascii", "abc", []int{1, 2, 3}},
		{"cjk", "中文", []int{3, 6}},
		{"combining mark moves as one", "éx", []int{3, 4}},
		{"invalid bytes step one at a time", "\xff\xfe", []int{1, 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line := []byte(tt.line)
			var got []int
			for off := 0; off < len(line); {
				off = NextGrapheme(line, off)
				got = append(got, off)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("NextGrapheme walk = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("NextGrapheme walk = %v, want %v", got, tt.want)
				}
			}

			// Walking back must retrace exactly the same boundaries.
			var back []int
			for off := len(line); off > 0; {
				off = PrevGrapheme(line, off)
				back = append(back, off)
			}
			for i, j := 0, len(back)-1; i < j; i, j = i+1, j-1 {
				back[i], back[j] = back[j], back[i]
			}
			wantBack := append([]int{0}, tt.want[:len(tt.want)-1]...)
			for i := range back {
				if back[i] != wantBack[i] {
					t.Fatalf("PrevGrapheme walk = %v, want %v", back, wantBack)
				}
			}
		})
	}
}

func TestGraphemeMovementClamps(t *testing.T) {
	line := []byte("abc")
	if got := NextGrapheme(line, 3); got != 3 {
		t.Errorf("NextGrapheme at end = %d, want 3", got)
	}
	if got := PrevGrapheme(line, 0); got != 0 {
		t.Errorf("PrevGrapheme at start = %d, want 0", got)
	}
	if got := NextGrapheme(line, 99); got != 3 {
		t.Errorf("NextGrapheme past end = %d, want 3", got)
	}
}

// A mid-character offset must snap to the start of its grapheme, so a cursor
// restored from a stale position can't corrupt the next edit.
func TestGraphemeAtSnapsToBoundary(t *testing.T) {
	line := []byte("中文") // two 3-byte runes
	for _, off := range []int{0, 1, 2} {
		if got := GraphemeAt(line, off); got != 0 {
			t.Errorf("GraphemeAt(%d) = %d, want 0", off, got)
		}
	}
	for _, off := range []int{3, 4, 5} {
		if got := GraphemeAt(line, off); got != 3 {
			t.Errorf("GraphemeAt(%d) = %d, want 3", off, got)
		}
	}
	if got := GraphemeAt(line, 6); got != 6 {
		t.Errorf("GraphemeAt(6) = %d, want 6", got)
	}
}

func TestCellAppendRendering(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{"tab expands to tab stop", "\tx", "    x"},
		{"control as caret", "\x01", "^A"},
		{"nul as caret-at", "\x00", "^@"},
		{"del as caret-question", "\x7f", "^?"},
		{"invalid as hex", "\xff", "<FF>"},
		{"plain text unchanged", "hi", "hi"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out []rune
			Iterate([]byte(tt.line), 4, func(c Cell) bool {
				out = c.Append(out)
				return true
			})
			if string(out) != tt.want {
				t.Errorf("render(%q) = %q, want %q", tt.line, string(out), tt.want)
			}
		})
	}
}

// Every cell must consume at least one byte and the cells must exactly tile the
// line. If this breaks, the renderer loops forever or drops text.
func TestIterateTilesLineExactly(t *testing.T) {
	lines := []string{
		"hello", "\t\t", "a\xffb", "中文", "é", "\x00\x01", "",
		"mixed 中\t\xff é end",
	}
	for _, s := range lines {
		line := []byte(s)
		nextOff, nextCol := 0, 0
		Iterate(line, 4, func(c Cell) bool {
			if c.ByteOff != nextOff {
				t.Errorf("line %q: gap at byte %d, cell starts at %d", s, nextOff, c.ByteOff)
			}
			if c.ByteLen < 1 {
				t.Fatalf("line %q: zero-length cell at %d would loop forever", s, c.ByteOff)
			}
			if c.Col != nextCol {
				t.Errorf("line %q: gap at col %d, cell starts at %d", s, nextCol, c.Col)
			}
			if c.Width < 1 {
				t.Errorf("line %q: cell at %d has width %d", s, c.ByteOff, c.Width)
			}
			nextOff = c.ByteOff + c.ByteLen
			nextCol = c.Col + c.Width
			return true
		})
		if nextOff != len(line) {
			t.Errorf("line %q: consumed %d bytes, want %d", s, nextOff, len(line))
		}
	}
}

func BenchmarkIterateASCII(b *testing.B) {
	line := []byte("\tif (err != NULL) { return handle_error(err, ctx, flags); }")
	b.SetBytes(int64(len(line)))
	for b.Loop() {
		Iterate(line, 4, func(Cell) bool { return true })
	}
}
