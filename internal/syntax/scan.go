package syntax

// State is what a line inherits from the one before it. Block comments and raw
// strings span lines, so a line cannot be highlighted in isolation.
//
// It is a single integer so it can be cached cheaply, one per line, for files
// with hundreds of thousands of lines.
type State uint32

const (
	stateNormal State = 0
	stateBlock  State = 1 // inside a block comment; the depth is in the high bits
	stateRaw    State = 2 // inside a raw string
	stateTriple State = 3 // inside a Python triple-quoted string
)

const (
	kindMask   = 0xF
	depthShift = 4
	quoteShift = 8
)

func (s State) kind() State { return s & kindMask }
func (s State) depth() int  { return int(s>>depthShift) & 0xF }
func (s State) quote() byte { return byte(s >> quoteShift) }

func mkState(kind State, depth int, quote byte) State {
	return kind | State(depth)<<depthShift | State(quote)<<quoteShift
}

func isIdentStart(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= 0x80
}

func isIdent(b byte) bool { return isIdentStart(b) || b >= '0' && b <= '9' }

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// hasAt reports whether line has s at offset i.
func hasAt(line []byte, i int, s string) bool {
	if s == "" || i+len(s) > len(line) {
		return false
	}
	for j := 0; j < len(s); j++ {
		if line[i+j] != s[j] {
			return false
		}
	}
	return true
}

// Scan tokenizes one line, given the state it inherits, and returns the tokens
// along with the state the next line inherits.
//
// dst is reused across lines to keep the render loop free of allocation.
func (l *Language) Scan(line []byte, in State, dst []Token) ([]Token, State) {
	toks := dst[:0]
	if l == nil {
		return toks, stateNormal
	}
	i := 0
	state := in

	emit := func(start, end int, c Class) {
		if end > start {
			toks = append(toks, Token{Start: start, End: end, Class: c})
		}
	}

	// Finish whatever construct carried over from the previous line.
	switch state.kind() {
	case stateBlock:
		start := i
		depth := state.depth()
		for i < len(line) {
			if l.NestedBlocks && hasAt(line, i, l.BlockStart) {
				depth++
				i += len(l.BlockStart)
				continue
			}
			if hasAt(line, i, l.BlockEnd) {
				i += len(l.BlockEnd)
				depth--
				if depth <= 0 {
					state = stateNormal
					break
				}
				continue
			}
			i++
		}
		// The loop above already ran to i == len(line) when the comment did not
		// close, so this single emit covers both cases.
		emit(start, i, ClassComment)
		if state.kind() == stateBlock {
			return toks, mkState(stateBlock, depth, 0)
		}
	case stateRaw:
		start := i
		for i < len(line) && line[i] != l.RawQuote {
			i++
		}
		if i < len(line) {
			i++ // consume the closing quote
			state = stateNormal
		}
		emit(start, i, ClassString)
		if state.kind() == stateRaw {
			return toks, state
		}
	case stateTriple:
		q := state.quote()
		start := i
		for i < len(line) {
			if line[i] == q && hasAt(line, i, string([]byte{q, q, q})) {
				i += 3
				state = stateNormal
				break
			}
			i++
		}
		emit(start, i, ClassString)
		if state.kind() == stateTriple {
			return toks, state
		}
	}

	// A C preprocessor directive colors the whole leading token.
	if l.Preproc && state == stateNormal {
		j := i
		for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
			j++
		}
		if j < len(line) && line[j] == '#' {
			k := j + 1
			for k < len(line) && (line[k] == ' ' || line[k] == '\t') {
				k++
			}
			for k < len(line) && isIdent(line[k]) {
				k++
			}
			emit(j, k, ClassPreproc)
			i = k
		}
	}

	for i < len(line) {
		c := line[i]

		// Line comment runs to end of line.
		if l.LineComment != "" && hasAt(line, i, l.LineComment) {
			emit(i, len(line), ClassComment)
			return toks, stateNormal
		}

		// Block comment.
		if l.BlockStart != "" && hasAt(line, i, l.BlockStart) {
			start := i
			i += len(l.BlockStart)
			depth := 1
			for i < len(line) {
				if l.NestedBlocks && hasAt(line, i, l.BlockStart) {
					depth++
					i += len(l.BlockStart)
					continue
				}
				if hasAt(line, i, l.BlockEnd) {
					i += len(l.BlockEnd)
					depth--
					if depth == 0 {
						break
					}
					continue
				}
				i++
			}
			emit(start, i, ClassComment)
			if depth > 0 {
				return toks, mkState(stateBlock, depth, 0)
			}
			continue
		}

		// Raw string, e.g. Go's backtick.
		if l.RawQuote != 0 && c == l.RawQuote {
			start := i
			i++
			for i < len(line) && line[i] != l.RawQuote {
				i++
			}
			if i < len(line) {
				i++
				emit(start, i, ClassString)
				continue
			}
			emit(start, len(line), ClassString)
			return toks, mkState(stateRaw, 0, 0)
		}

		// Triple-quoted string.
		if l.TripleQuote && (c == '"' || c == '\'') && hasAt(line, i, string([]byte{c, c, c})) {
			start := i
			i += 3
			closed := false
			for i < len(line) {
				if line[i] == c && hasAt(line, i, string([]byte{c, c, c})) {
					i += 3
					closed = true
					break
				}
				i++
			}
			emit(start, i, ClassString)
			if !closed {
				return toks, mkState(stateTriple, 0, c)
			}
			continue
		}

		// Ordinary string or character literal.
		if strIndex(l.Quotes, c) >= 0 {
			start := i
			i++
			for i < len(line) {
				if line[i] == '\\' {
					i += 2
					continue
				}
				if line[i] == c {
					i++
					break
				}
				i++
			}
			emit(start, i, ClassString)
			continue
		}

		// Number: a digit, or a dot followed by a digit.
		if isDigit(c) || (c == '.' && i+1 < len(line) && isDigit(line[i+1])) {
			// Only start a number at a token boundary, so the "1" in "x1" is
			// part of the identifier rather than a number of its own.
			if i == 0 || !isIdent(line[i-1]) {
				start := i
				for i < len(line) && (isIdent(line[i]) || line[i] == '.' ||
					// Exponent signs, as in 1e-9.
					((line[i] == '+' || line[i] == '-') && i > start &&
						(line[i-1] == 'e' || line[i-1] == 'E'))) {
					i++
				}
				emit(start, i, ClassNumber)
				continue
			}
		}

		// Identifier: keyword, type, function call, or nothing special.
		if isIdentStart(c) {
			start := i
			for i < len(line) && isIdent(line[i]) {
				i++
			}
			word := string(line[start:i])
			switch {
			case l.Keywords[word]:
				emit(start, i, ClassKeyword)
			case l.Types[word]:
				emit(start, i, ClassType)
			default:
				// A name immediately followed by "(" reads as a call, which is
				// the cheapest useful signal for finding functions by eye.
				j := i
				for j < len(line) && line[j] == ' ' {
					j++
				}
				if j < len(line) && line[j] == '(' {
					emit(start, i, ClassFunc)
				}
			}
			continue
		}

		i++
	}
	return toks, stateNormal
}

func strIndex(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
