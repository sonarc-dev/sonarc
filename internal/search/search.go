// Package search finds text within a buffer.
//
// It supports literal and regular-expression matching, and defaults to "smart
// case": a lowercase pattern matches case-insensitively, while a pattern
// containing an uppercase letter is treated as case-sensitive. That gives the
// convenience of a case-insensitive search without losing the ability to ask
// for an exact one, and it is what people already expect from vim and ripgrep.
package search

import (
	"bytes"
	"regexp"
	"strings"

	"sonarc/internal/buffer"
)

// Options controls how a pattern is interpreted.
type Options struct {
	Regex bool // treat the pattern as a regular expression
	Case  bool // force case-sensitive matching
	Word  bool // match whole identifiers only
}

// Matcher is a compiled search pattern.
type Matcher struct {
	pattern string
	opts    Options
	re      *regexp.Regexp
	// lower holds the folded pattern used for case-insensitive literal search.
	lower string
	fold  bool
}

// Compile prepares a pattern for searching.
func Compile(pattern string, opts Options) (*Matcher, error) {
	m := &Matcher{pattern: pattern, opts: opts}

	// Smart case: fold only when the user typed no capitals.
	m.fold = !opts.Case && strings.ToLower(pattern) == pattern

	if opts.Regex {
		expr := pattern
		if opts.Word {
			expr = `\b(?:` + expr + `)\b`
		}
		if m.fold {
			expr = "(?i)" + expr
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, err
		}
		m.re = re
		return m, nil
	}

	if opts.Word {
		// A literal whole-word search is expressed as a regex so the boundary
		// rules live in one place.
		expr := `\b` + regexp.QuoteMeta(pattern) + `\b`
		if m.fold {
			expr = "(?i)" + expr
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, err
		}
		m.re = re
		return m, nil
	}

	m.lower = strings.ToLower(pattern)
	return m, nil
}

// Pattern returns the original pattern text.
func (m *Matcher) Pattern() string { return m.pattern }

// Empty reports whether the pattern matches nothing.
func (m *Matcher) Empty() bool { return m.pattern == "" }

// matchLine returns the byte ranges matching within one line.
func (m *Matcher) matchLine(line []byte, from int) (start, end int, ok bool) {
	if from > len(line) {
		return 0, 0, false
	}
	if m.re != nil {
		// Search the whole line and skip matches before `from`, rather than
		// slicing at it. Slicing would show the regex engine a false line
		// start, so ^, $ and \b would match in the middle of a line.
		for _, loc := range m.re.FindAllIndex(line, -1) {
			if loc[0] >= from {
				return loc[0], loc[1], true
			}
		}
		return 0, 0, false
	}
	hay := line[from:]
	var i int
	if m.fold {
		i = bytes.Index(bytes.ToLower(hay), []byte(m.lower))
	} else {
		i = bytes.Index(hay, []byte(m.pattern))
	}
	if i < 0 {
		return 0, 0, false
	}
	return from + i, from + i + len(m.pattern), true
}

// matchLineLast returns the last match at or before limit within one line,
// used when searching backwards.
func (m *Matcher) matchLineLast(line []byte, limit int) (start, end int, ok bool) {
	if limit < 0 {
		return 0, 0, false
	}
	if limit > len(line) {
		limit = len(line)
	}
	pos := 0
	for {
		s, e, found := m.matchLine(line, pos)
		if !found || s >= limit {
			break
		}
		start, end, ok = s, e, true
		// Advance by one byte rather than to the match end, so overlapping
		// matches are all considered.
		pos = s + 1
		if pos > len(line) {
			break
		}
	}
	return start, end, ok
}

// Match is one located occurrence.
type Match struct {
	From buffer.Pos
	To   buffer.Pos
}

// Next finds the first match at or after start, wrapping to the top of the
// buffer. Wrapping rather than stopping is what makes repeated Find feel like
// a cycle instead of a dead end.
func (m *Matcher) Next(b *buffer.Buffer, start buffer.Pos) (Match, bool) {
	if m.Empty() {
		return Match{}, false
	}
	n := b.NumLines()
	col := start.Col
	for i := 0; i <= n; i++ {
		ln := (start.Line + i) % n
		if s, e, ok := m.matchLine(b.Line(ln), col); ok {
			return Match{
				From: buffer.Pos{Line: ln, Col: s},
				To:   buffer.Pos{Line: ln, Col: e},
			}, true
		}
		col = 0
	}
	return Match{}, false
}

// Prev finds the last match strictly before start, wrapping to the bottom.
func (m *Matcher) Prev(b *buffer.Buffer, start buffer.Pos) (Match, bool) {
	if m.Empty() {
		return Match{}, false
	}
	n := b.NumLines()
	limit := start.Col
	for i := 0; i <= n; i++ {
		ln := ((start.Line-i)%n + n) % n
		if i > 0 {
			limit = len(b.Line(ln)) + 1
		}
		if s, e, ok := m.matchLineLast(b.Line(ln), limit); ok {
			return Match{
				From: buffer.Pos{Line: ln, Col: s},
				To:   buffer.Pos{Line: ln, Col: e},
			}, true
		}
	}
	return Match{}, false
}

// All returns every match in the buffer, capped at limit. Used to highlight
// occurrences and to count them for the status line.
func (m *Matcher) All(b *buffer.Buffer, limit int) []Match {
	if m.Empty() {
		return nil
	}
	var out []Match
	for ln := 0; ln < b.NumLines() && len(out) < limit; ln++ {
		line := b.Line(ln)

		if m.re != nil {
			// One pass per line. FindAllIndex already returns non-overlapping
			// matches and handles zero-width ones such as "^" correctly, which
			// a manual advance-and-rescan loop gets wrong.
			for _, loc := range m.re.FindAllIndex(line, -1) {
				out = append(out, Match{
					From: buffer.Pos{Line: ln, Col: loc[0]},
					To:   buffer.Pos{Line: ln, Col: loc[1]},
				})
				if len(out) >= limit {
					break
				}
			}
			continue
		}

		for col := 0; col <= len(line); {
			s, e, ok := m.matchLine(line, col)
			if !ok {
				break
			}
			out = append(out, Match{
				From: buffer.Pos{Line: ln, Col: s},
				To:   buffer.Pos{Line: ln, Col: e},
			})
			if len(out) >= limit {
				break
			}
			col = e
			if e == s {
				col = s + 1 // never allow a zero-width match to stall
			}
		}
	}
	return out
}

// Expand produces the replacement text for a match. With a regex pattern the
// usual $1 group references apply; a literal pattern is substituted verbatim.
func (m *Matcher) Expand(b *buffer.Buffer, mt Match, replacement string) []byte {
	if m.re == nil || !m.opts.Regex {
		return []byte(replacement)
	}
	src := b.Text(mt.From, mt.To)
	loc := m.re.FindSubmatchIndex(src)
	if loc == nil {
		return []byte(replacement)
	}
	return m.re.Expand(nil, []byte(replacement), src, loc)
}
