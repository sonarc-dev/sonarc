package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/sonarc-dev/sonarc/internal/index/provider"
)

// cmdPeekDefinition shows the definition of the symbol under the cursor on
// the message line, without leaving where you are: the quick "what does this
// take?" that otherwise costs a jump there and a jump back.
func (a *app) cmdPeekDefinition() {
	sym := a.symbolUnderCursor()
	if sym == "" {
		a.ui.Notify("put the cursor on a symbol first")
		return
	}
	a.startQuery("definition of "+sym, func(ctx context.Context) applyFunc {
		syms := a.index.Definitions(ctx, sym)
		var sig string
		if len(syms) > 0 {
			sig = signature(syms[0].Loc)
		}
		return func(bool) {
			if len(syms) == 0 {
				a.ui.Error("no definition found for %s  (%s)", sym, a.indexSummary())
				return
			}
			d := syms[0]
			more := ""
			if len(syms) > 1 {
				more = fmt.Sprintf("  (+%d more)", len(syms)-1)
			}
			a.ui.Notify("%s:%d  %s%s", shortPath(a.root, d.Loc.Path), d.Loc.Line, sig, more)
		}
	})
}

// maxSignatureLines bounds how far a definition is followed onto the lines
// after it. Kernel style wraps long parameter lists, so the first line alone
// often stops mid-argument.
const maxSignatureLines = 4

// signature is a definition as one line of text: its first line, continued
// while a parameter list is still open, with runs of whitespace collapsed.
func signature(loc provider.Location) string {
	lines := readLines(loc.Path, loc.Line, maxSignatureLines)
	if len(lines) == 0 {
		return strings.Join(strings.Fields(loc.Text), " ")
	}
	var parts []string
	depth := 0
	for _, l := range lines {
		parts = append(parts, l)
		depth += strings.Count(l, "(") - strings.Count(l, ")")
		if depth <= 0 {
			break
		}
	}
	s := strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
	return strings.TrimSuffix(strings.TrimSpace(strings.TrimSuffix(s, "{")), " ")
}

// readLines returns up to n lines of path starting at the 1-based line from.
func readLines(path string, from, n int) []string {
	f, err := os.Open(path)
	if err != nil || from <= 0 {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	var out []string
	for i := 1; sc.Scan(); i++ {
		if i >= from {
			out = append(out, sc.Text())
			if len(out) == n {
				break
			}
		}
	}
	return out
}
