package main

import (
	"fmt"
	"sonarc/internal/index/builtin"
	"sonarc/internal/index/cscope"
	"sonarc/internal/index/provider"
	"sonarc/internal/index/tags"

	"github.com/gdamore/tcell/v2"
)

// startIndex wires up the code-intelligence providers.
//
// They are registered in descending order of authority: cscope knows the most,
// a tags file knows definitions, and the built-in indexer knows enough to keep
// navigation working when neither tool is installed.
func (a *app) startIndex() {
	a.index = &provider.Registry{}

	if p := cscope.Discover(a.root); p != nil {
		a.cscope = p
		a.index.Add(p)
	}
	if p := tags.Discover(a.root); p != nil {
		a.tags = p
		a.index.Add(p)
	}
	// Always available, and always last. When a real index is already
	// answering, the built-in one only needs to supply the file list: building
	// its symbol map over a kernel-sized tree costs gigabytes for answers
	// cscope and ctags have already given.
	a.builtin = builtin.New(a.root)
	if a.cscope != nil || a.tags != nil {
		a.builtin.SkipSymbols()
	}
	a.builtin.Start()
	a.index.Add(a.builtin)
}

func (a *app) closeIndex() {
	if a.index != nil {
		a.index.Close()
	}
}

// indexSummary describes which providers are active, for the status line.
func (a *app) indexSummary() string {
	var names []string
	if a.cscope != nil && a.cscope.Available() {
		names = append(names, "cscope")
	}
	if a.tags != nil && a.tags.Available() {
		names = append(names, "tags")
	}
	if a.builtin != nil && a.builtin.Available() {
		f, s := a.builtin.Stats()
		names = append(names, fmt.Sprintf("builtin(%d files, %d syms)", f, s))
	}
	if len(names) == 0 {
		return "indexing..."
	}
	out := names[0]
	for _, n := range names[1:] {
		out += " + " + n
	}
	return out
}

// cmdIndexStatus reports which providers are answering queries and where their
// databases live, so "no definition found" is diagnosable rather than mysterious.
func (a *app) cmdIndexStatus() {
	lines := []string{"", "Providers, in the order they are consulted:", ""}

	if a.cscope != nil && a.cscope.Available() {
		lines = append(lines, "  cscope    "+a.cscope.Path())
	} else if !cscope.Available() {
		lines = append(lines, "  cscope    not installed")
	} else {
		lines = append(lines, "  cscope    no cscope.out found (Ctrl+K x builds one)")
	}

	if a.tags != nil && a.tags.Available() {
		s := "  ctags     " + a.tags.Path()
		if a.tags.Stale() {
			s += "  (STALE - rebuild with Ctrl+K x)"
		}
		lines = append(lines, s)
	} else if _, ok := tags.CtagsAvailable(); !ok {
		lines = append(lines, "  ctags     no usable ctags installed")
		lines = append(lines, "            (macOS ships BSD ctags, which cannot generate one)")
	} else {
		lines = append(lines, "  ctags     no tags file found (Ctrl+K x builds one)")
	}

	if a.builtin != nil {
		if a.builtin.Available() {
			f, s := a.builtin.Stats()
			src, truncated := a.builtin.Coverage()
			line := fmt.Sprintf("  builtin   %d files (from %s), %d symbols indexed", f, src, s)
			if truncated {
				line += "  (INCOMPLETE: size cap reached)"
			}
			lines = append(lines, line)
		} else {
			lines = append(lines, "  builtin   still indexing...")
		}
	}
	lines = append(lines,
		"",
		"  project   "+a.root,
		"",
		"Press any key to close.")

	a.ui.Draw()
	a.ui.DrawOverlay("index status", lines)
	a.scr.Show()
	for {
		if _, ok := a.poll().(*tcell.EventKey); ok {
			return
		}
	}
}
