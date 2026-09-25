// Package text implements the three coordinate spaces a terminal editor has to
// keep straight, and the conversions between them:
//
//	byte offset     index into the line's raw []byte
//	grapheme        a user-perceived character (may span several bytes/runes)
//	display column  a terminal cell, after tabs expand and wide runes take two
//
// Confusing these is the classic source of editor bugs, so every conversion in
// sonarc goes through Iterate below. Nothing else is allowed to reimplement it.
//
// Lines are stored as raw bytes and may contain invalid UTF-8. Such bytes are
// displayed as <XX> but always round-trip unchanged on save: sonarc never
// corrupts a file it doesn't understand.
package text

import (
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// Kind classifies a display unit, which decides how it is drawn.
type Kind uint8

const (
	// KindNormal is ordinary text drawn as-is.
	KindNormal Kind = iota
	// KindTab is a tab, drawn as spaces out to the next tab stop.
	KindTab
	// KindControl is a C0/DEL control character, drawn as ^X.
	KindControl
	// KindInvalid is a byte that is not valid UTF-8, drawn as <XX>.
	KindInvalid
)

// Cell is one display unit of a line: the bytes it came from and the terminal
// cells it occupies. ByteLen is always >= 1, so iteration always advances.
type Cell struct {
	ByteOff int  // byte offset in the line where this unit starts
	ByteLen int  // number of bytes consumed
	Col     int  // starting display column
	Width   int  // number of terminal cells occupied
	Rune    rune // the primary rune (for KindNormal); undefined for KindInvalid
	Byte    byte // the raw byte (for KindControl and KindInvalid)
	Kind    Kind
}

const hexDigits = "0123456789ABCDEF"

// Append writes the runes this cell should draw onto dst and returns it.
// Callers reuse dst across cells to avoid allocating in the render loop.
func (c Cell) Append(dst []rune) []rune {
	switch c.Kind {
	case KindTab:
		for i := 0; i < c.Width; i++ {
			dst = append(dst, ' ')
		}
	case KindControl:
		// ^@ for NUL through ^_ for US, and ^? for DEL.
		dst = append(dst, '^', rune(c.Byte^0x40))
	case KindInvalid:
		dst = append(dst, '<', rune(hexDigits[c.Byte>>4]), rune(hexDigits[c.Byte&0xf]), '>')
	default:
		dst = append(dst, c.Rune)
	}
	return dst
}

// ClusterRunes decodes the grapheme cluster this cell came from into its base
// rune and any combining runes that follow. Renderers need both: drawing only
// Rune would silently drop accents from decomposed text.
//
// dst is reused to avoid allocating per cell; pass the previous result sliced
// to zero length.
func (c Cell) ClusterRunes(line []byte, dst []rune) (rune, []rune) {
	dst = dst[:0]
	end := c.ByteOff + c.ByteLen
	if end > len(line) {
		end = len(line)
	}
	main, size := utf8.DecodeRune(line[c.ByteOff:end])
	for i := c.ByteOff + size; i < end; {
		r, s := utf8.DecodeRune(line[i:end])
		if s <= 0 {
			break
		}
		dst = append(dst, r)
		i += s
	}
	return main, dst
}

// Iterate walks line as display units, calling fn for each. Iteration stops
// early if fn returns false.
//
// This is the single definition of how bytes map to columns. ByteToCol,
// ColToByte, Width, and the renderer are all thin wrappers over it.
func Iterate(line []byte, tabWidth int, fn func(Cell) bool) {
	if tabWidth < 1 {
		tabWidth = 8
	}
	col := 0
	state := -1
	for i := 0; i < len(line); {
		b := line[i]

		// Tabs and control characters are always their own display unit. Tab
		// width depends on the current column, which grapheme segmentation
		// knows nothing about, so these never go through uniseg.
		var c Cell
		switch {
		case b == '\t':
			// A tab advances to the next multiple of tabWidth, so its width
			// depends on where it starts. Always at least 1.
			c = Cell{ByteOff: i, ByteLen: 1, Col: col, Width: tabWidth - col%tabWidth, Kind: KindTab}
		case b < 0x20 || b == 0x7f:
			c = Cell{ByteOff: i, ByteLen: 1, Col: col, Width: 2, Byte: b, Kind: KindControl}
		case b < utf8.RuneSelf && (i+1 >= len(line) || line[i+1] < utf8.RuneSelf):
			// Fast path: plain ASCII followed by more ASCII. Source code is
			// overwhelmingly this, and skipping segmentation matters on large
			// files. Guarding on the *next* byte is essential: an ASCII base
			// character followed by a combining mark forms one grapheme, so it
			// must fall through to uniseg rather than split here.
			c = Cell{ByteOff: i, ByteLen: 1, Col: col, Width: 1, Rune: rune(b), Kind: KindNormal}
		}
		if c.Width > 0 {
			if !fn(c) {
				return
			}
			col += c.Width
			i += c.ByteLen
			state = -1
			continue
		}

		// Not ASCII. Reject invalid UTF-8 before segmenting, so a stray byte
		// can be shown as <XX> and preserved rather than silently replaced.
		if r, size := utf8.DecodeRune(line[i:]); r == utf8.RuneError && size <= 1 {
			c = Cell{ByteOff: i, ByteLen: 1, Col: col, Width: 4, Byte: b, Kind: KindInvalid}
			if !fn(c) {
				return
			}
			col += c.Width
			i++
			state = -1
			continue
		}

		// Valid multi-byte text: take a whole grapheme cluster so that
		// combining marks and emoji ZWJ sequences move as a single unit.
		cluster, _, w, newState := uniseg.FirstGraphemeCluster(line[i:], state)
		state = newState
		if len(cluster) == 0 { // defensive: never allow a zero-byte advance
			cluster = line[i : i+1]
		}
		if w < 1 {
			// A defective cluster: a combining mark with no base, which only
			// happens at a line start or after a control character. Give it a
			// cell of its own so columns stay a bijection with byte offsets and
			// the cursor can still be placed on it.
			w = 1
		}
		r, _ := utf8.DecodeRune(cluster)
		c = Cell{ByteOff: i, ByteLen: len(cluster), Col: col, Width: w, Rune: r, Kind: KindNormal}
		if !fn(c) {
			return
		}
		col += c.Width
		i += c.ByteLen
	}
}

// ByteToCol returns the display column at which the grapheme containing
// byteOff begins. Offsets past the end of the line clamp to the line width.
func ByteToCol(line []byte, byteOff, tabWidth int) int {
	if byteOff <= 0 {
		return 0
	}
	col := 0
	Iterate(line, tabWidth, func(c Cell) bool {
		if c.ByteOff >= byteOff {
			return false
		}
		col = c.Col + c.Width
		return true
	})
	return col
}

// ColToByte returns the byte offset of the grapheme occupying the given display
// column. A column landing inside a tab or a wide rune resolves to the start of
// that unit, so the cursor never lands mid-character.
func ColToByte(line []byte, col, tabWidth int) int {
	if col <= 0 {
		return 0
	}
	off := len(line)
	Iterate(line, tabWidth, func(c Cell) bool {
		if col < c.Col+c.Width {
			off = c.ByteOff
			return false
		}
		return true
	})
	return off
}

// Width returns the total display width of the line.
func Width(line []byte, tabWidth int) int {
	w := 0
	Iterate(line, tabWidth, func(c Cell) bool {
		w = c.Col + c.Width
		return true
	})
	return w
}

// NextGrapheme returns the byte offset one grapheme after byteOff, clamped to
// the end of the line. Use this to move the cursor right; never byteOff+1.
func NextGrapheme(line []byte, byteOff int) int {
	if byteOff >= len(line) {
		return len(line)
	}
	next := len(line)
	Iterate(line, 8, func(c Cell) bool {
		if c.ByteOff > byteOff {
			next = c.ByteOff
			return false
		}
		return true
	})
	return next
}

// PrevGrapheme returns the byte offset one grapheme before byteOff, clamped to
// zero. Use this to move the cursor left; never byteOff-1.
func PrevGrapheme(line []byte, byteOff int) int {
	if byteOff <= 0 {
		return 0
	}
	prev := 0
	Iterate(line, 8, func(c Cell) bool {
		if c.ByteOff >= byteOff {
			return false
		}
		prev = c.ByteOff
		return true
	})
	return prev
}

// GraphemeAt returns the byte offset of the start of the grapheme containing
// byteOff, so a cursor handed a mid-character offset snaps to a valid position.
func GraphemeAt(line []byte, byteOff int) int {
	if byteOff <= 0 {
		return 0
	}
	if byteOff >= len(line) {
		return len(line)
	}
	start := 0
	Iterate(line, 8, func(c Cell) bool {
		if c.ByteOff > byteOff {
			return false
		}
		start = c.ByteOff
		return true
	})
	return start
}
