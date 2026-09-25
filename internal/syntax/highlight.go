package syntax

import "sonarc/internal/buffer"

// maxLookback bounds how far back the scanner will go to establish the state of
// a line.
//
// Highlighting line N correctly requires knowing whether line N-1 ended inside
// a block comment, which in principle means scanning from the top of the file.
// Jumping into the middle of a 500k-line file would then stall. Beyond this
// many lines the scan restarts from a neutral state instead: a comment or raw
// string spanning more than this is not something real code does, and a bounded
// wrong answer far up the file beats an unbounded pause.
const maxLookback = 20_000

// Highlighter colors a buffer, caching the state each line inherits so that
// scrolling does not rescan from the top every frame.
type Highlighter struct {
	lang   *Language
	states []State // states[i] is the state entering line i
	valid  int     // states[0:valid] are known correct
}

// New returns a highlighter for a language. A nil language is valid and
// produces no tokens, which is what plain text should do.
func New(lang *Language) *Highlighter {
	return &Highlighter{lang: lang, states: []State{stateNormal}, valid: 1}
}

// Language returns the language in use, nil for plain text.
func (h *Highlighter) Language() *Language {
	if h == nil {
		return nil
	}
	return h.lang
}

// Invalidate discards cached state from line onward. The editor calls this
// after an edit: everything above the change is still correct, and typing a
// "/*" only has to invalidate what follows it.
func (h *Highlighter) Invalidate(line int) {
	if h == nil {
		return
	}
	if line < 0 {
		line = 0
	}
	if line+1 < h.valid {
		h.valid = line + 1
	}
}

// ensure computes cached states up to and including line.
func (h *Highlighter) ensure(b *buffer.Buffer, line int) {
	if line < h.valid {
		return
	}
	n := b.NumLines()
	if line >= n {
		line = n - 1
	}

	start := h.valid - 1
	if start < 0 {
		start = 0
	}
	// If the cache is far behind, restart nearby rather than scanning the
	// whole file. See maxLookback.
	if line-start > maxLookback {
		start = line - maxLookback
		if start < 0 {
			start = 0
		}
		h.grow(start + 1)
		h.states[start] = stateNormal
		h.valid = start + 1
	}

	h.grow(line + 2)
	var toks []Token
	state := h.states[start]
	for i := start; i <= line; i++ {
		toks, state = h.lang.Scan(b.Line(i), state, toks)
		h.states[i+1] = state
	}
	if line+2 > h.valid {
		h.valid = line + 2
	}
}

// grow makes room for n cached states.
func (h *Highlighter) grow(n int) {
	for len(h.states) < n {
		h.states = append(h.states, stateNormal)
	}
}

// Tokens returns the colored spans of one line. dst is reused across calls to
// keep the render loop allocation-free.
func (h *Highlighter) Tokens(b *buffer.Buffer, line int, dst []Token) []Token {
	if h == nil || h.lang == nil || line < 0 || line >= b.NumLines() {
		return dst[:0]
	}
	h.ensure(b, line)
	state := stateNormal
	if line < len(h.states) {
		state = h.states[line]
	}
	toks, _ := h.lang.Scan(b.Line(line), state, dst)
	return toks
}

// ClassAt reports the class covering a byte offset within a line's tokens, or
// ClassNone. Tokens are in order, so this is a short forward scan.
func ClassAt(toks []Token, off int) Class {
	for _, t := range toks {
		if off < t.Start {
			return ClassNone
		}
		if off < t.End {
			return t.Class
		}
	}
	return ClassNone
}
