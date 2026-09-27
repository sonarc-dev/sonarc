package main

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sonarc-dev/sonarc/internal/buffer"
	"github.com/sonarc-dev/sonarc/internal/index/cscope"
	"github.com/sonarc-dev/sonarc/internal/index/provider"
)

// symbolUnderCursor returns the symbol to look up.
//
// A selection is honored, since selecting text is how you look up something the
// identifier rules would not find. But a selection that is only *part* of an
// identifier is ignored in favor of the whole one: an incremental search for
// "compute" leaves exactly that selected while the cursor sits inside
// "compute_total", and looking up the fragment finds nothing.
func (a *app) symbolUnderCursor() string {
	v := a.v()
	whole := provider.SymbolAt(v.Buf.Line(v.Head.Line), v.Head.Col)

	if v.HasSelection() {
		s := v.SelectedText()
		if len(s) == 0 || len(s) >= 200 {
			return whole
		}
		sel := string(s)
		// Only override the selection when it is a strict fragment of the
		// identifier the cursor is in. A selection spanning punctuation or
		// several words is deliberate and stands.
		if whole != "" && sel != whole && strings.Contains(whole, sel) && isIdentifier(sel) {
			return whole
		}
		return sel
	}
	return whole
}

// isIdentifier reports whether s is made entirely of identifier characters.
func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !provider.IsSymbolChar(s[i]) {
			return false
		}
	}
	return true
}

// goTo opens a location and puts the cursor on it, recording where we came
// from so the jump can be undone. It reports whether the jump happened.
func (a *app) goTo(loc provider.Location) bool {
	return a.goToSymbol(loc, "")
}

// goToSymbol jumps to a location and, when sym is given, places the cursor on
// that identifier within the target line rather than at column 0.
//
// This matters more than it looks: landing at column 0 puts the cursor on
// whatever starts the line, which for a C definition is the return type. The
// next find-references would then search for "int" instead of the function you
// just navigated to.
func (a *app) goToSymbol(loc provider.Location, sym string) bool {
	a.pushJump()
	if loc.Path != "" && loc.Path != a.v().Buf.Path() {
		if err := a.openFile(loc.Path); err != nil {
			a.ui.Error("cannot open %s: %v", filepath.Base(loc.Path), err)
			a.jumps = a.jumps[:len(a.jumps)-1] // the jump did not happen
			return false
		}
	}
	if loc.Line <= 0 {
		return true
	}
	a.v().Goto(loc.Line)
	line := loc.Line - 1
	if loc.Col > 0 {
		// A language server gives the exact column; trust it over a search.
		a.v().SetCursor(a.v().Buf.Clamp(buffer.Pos{Line: line, Col: loc.Col - 1}))
		return true
	}
	if sym == "" {
		return true
	}
	if col := indexWord(a.v().Buf.Line(line), sym); col >= 0 {
		a.v().SetCursor(buffer.Pos{Line: line, Col: col})
	}
	return true
}

// indexWord finds sym in line as a whole identifier, returning -1 if absent.
func indexWord(line []byte, sym string) int {
	for off := 0; ; {
		i := bytes.Index(line[off:], []byte(sym))
		if i < 0 {
			return -1
		}
		at := off + i
		beforeOK := at == 0 || !provider.IsSymbolChar(line[at-1])
		end := at + len(sym)
		afterOK := end >= len(line) || !provider.IsSymbolChar(line[end])
		if beforeOK && afterOK {
			return at
		}
		off = at + 1
	}
}

// cmdGotoDefinition jumps to where the symbol under the cursor is defined.
//
// When several definitions exist it offers a picker rather than guessing. In C
// that is the normal case — a prototype in a header, the definition in a .c
// file, and often several static variants across the tree — and picking the
// wrong one silently is worse than asking.
func (a *app) cmdGotoDefinition() {
	sym := a.symbolUnderCursor()
	if sym == "" {
		a.ui.Notify("put the cursor on a symbol first")
		return
	}
	at, precise := a.cursorAt()
	a.startQuery("definition of "+sym, func(ctx context.Context) applyFunc {
		var syms []provider.Symbol
		if precise {
			syms = a.index.DefinitionsAt(ctx, at, sym)
		} else {
			syms = a.index.Definitions(ctx, sym)
		}
		return func(untouched bool) { a.showDefinitions(sym, syms, untouched) }
	})
}

// showDefinitions jumps to a lone definition or offers a picker for several.
//
// If the user kept typing while the lookup ran, neither happens: jumping would
// move the cursor mid-edit, and the picker is modal and would swallow the next
// keystrokes. The answer is reported instead, and Ctrl+] again acts on it.
func (a *app) showDefinitions(sym string, syms []provider.Symbol, untouched bool) {
	switch {
	case len(syms) == 0:
		a.ui.Error("no definition found for %s  (%s)", sym, a.indexSummary())
	case !untouched && len(syms) == 1:
		a.ui.Notify("%s is defined at %s:%d — not jumping while you type; Ctrl+] goes there",
			sym, shortPath(a.root, syms[0].Loc.Path), syms[0].Loc.Line)
	case !untouched:
		a.ui.Notify("%d definitions of %s found — not interrupting your typing; Ctrl+] lists them", len(syms), sym)
	case len(syms) == 1:
		if a.goToSymbol(syms[0].Loc, sym) {
			a.ui.Notify("%s  —  %s:%d  via %s", sym,
				shortPath(a.root, syms[0].Loc.Path), syms[0].Loc.Line, syms[0].Source)
		}
	default:
		a.pickAction = nil
		a.ui.ShowPicker(fmt.Sprintf("%d definitions of %s", len(syms), sym), symbolPickerItems(a.root, syms, true))
	}
}

// cmdFindReferences lists every use of the symbol under the cursor.
func (a *app) cmdFindReferences() {
	at, precise := a.cursorAt()
	a.runQuery("references to", func(ctx context.Context, sym string) ([]provider.Location, provider.Report, string) {
		if precise {
			locs, rep := a.index.ReferencesReportAt(ctx, at, sym)
			return locs, rep, ""
		}
		locs, rep := a.index.ReferencesReport(ctx, sym)
		return locs, rep, ""
	})
}

// cmdFindCallers lists the functions calling the one under the cursor.
func (a *app) cmdFindCallers() {
	a.runQuery("callers of", func(ctx context.Context, sym string) ([]provider.Location, provider.Report, string) {
		locs, ok := a.index.Callers(ctx, sym)
		if !ok {
			return nil, provider.Report{}, "callers needs a cscope database: run Ctrl+K x to build one"
		}
		return locs, provider.Report{}, ""
	})
}

// cmdFindCallees lists the functions called by the one under the cursor.
func (a *app) cmdFindCallees() {
	a.runQuery("functions called by", func(ctx context.Context, sym string) ([]provider.Location, provider.Report, string) {
		locs, ok := a.index.Callees(ctx, sym)
		if !ok {
			return nil, provider.Report{}, "call graph needs a cscope database: run Ctrl+K x to build one"
		}
		return locs, provider.Report{}, ""
	})
}

// cmdFindIncluders lists files that #include the current one.
func (a *app) cmdFindIncluders() {
	if a.cscope == nil || !a.cscope.Available() {
		a.ui.Error("this needs a cscope database: run Ctrl+K x to build one")
		return
	}
	name := filepath.Base(a.v().Buf.Path())
	if name == "" {
		a.ui.Notify("save the file first")
		return
	}
	cs := a.cscope
	a.startQuery("files including "+name, func(ctx context.Context) applyFunc {
		locs, err := cs.Query(ctx, cscope.FindIncluding, name)
		return func(untouched bool) {
			if err != nil {
				a.ui.Error("cscope: %v", err)
				return
			}
			a.showResults(fmt.Sprintf("files including %s", name), locs, "", untouched)
		}
	})
}

// cmdFindAssignments lists assignments to the symbol under the cursor.
func (a *app) cmdFindAssignments() {
	if a.cscope == nil || !a.cscope.Available() {
		a.ui.Error("this needs a cscope database: run Ctrl+K x to build one")
		return
	}
	cs := a.cscope
	a.runQuery("assignments to", func(ctx context.Context, sym string) ([]provider.Location, provider.Report, string) {
		locs, err := cs.Query(ctx, cscope.FindAssignments, sym)
		if err != nil {
			return nil, provider.Report{}, fmt.Sprintf("cscope: %v", err)
		}
		return locs, provider.Report{}, ""
	})
}

// queryFunc does the lookup for runQuery. It runs on a background goroutine, so
// it must not touch the UI: a problem is returned as text for the UI goroutine
// to show.
type queryFunc func(ctx context.Context, sym string) (locs []provider.Location, rep provider.Report, problem string)

// runQuery looks up the symbol under the cursor in the background and shows the
// results, saying how long each provider took and whether the answer is
// complete.
func (a *app) runQuery(what string, fn queryFunc) {
	sym := a.symbolUnderCursor()
	if sym == "" {
		a.ui.Notify("put the cursor on a symbol first")
		return
	}
	a.startQuery(what+" "+sym, func(ctx context.Context) applyFunc {
		start := time.Now()
		locs, rep, problem := fn(ctx, sym)
		note := queryNote(rep, time.Since(start))

		return func(untouched bool) {
			switch {
			case problem != "":
				a.ui.Error("%s", problem)
			case len(locs) == 0:
				a.ui.Notify("no %s %s  (%s)  %s", what, sym, a.indexSummary(), note)
			default:
				title := fmt.Sprintf("%s %s", what, sym)
				if rep.Partial() {
					title += " (partial)"
				}
				a.showResults(title, locs, note, untouched)
			}
		}
	})
}

// queryNote describes how a result set was obtained: per-provider timings when
// the registry supplied them, otherwise just the total, plus a pointer to the
// text search when a whole-tree scan was deliberately left out.
func queryNote(rep provider.Report, total time.Duration) string {
	note := rep.Summary()
	if note == "" {
		note = provider.FormatDuration(total)
	}
	if rep.SkippedScan() {
		note += " · Ctrl+K f also searches text"
	}
	return note
}

// cmdFindSymbol offers a searchable list of symbols across the project.
func (a *app) cmdFindSymbol() {
	q, ok := a.prompt("Symbol: ", a.symbolUnderCursor())
	if !ok || q == "" {
		return
	}
	root := a.root
	a.startQuery(fmt.Sprintf("symbols matching %q", q), func(ctx context.Context) applyFunc {
		items := symbolPickerItems(root, a.index.Search(ctx, q, 500), false)
		return func(untouched bool) {
			switch {
			case len(items) == 0:
				a.ui.Notify("no symbol matching %q  (%s)", q, a.indexSummary())
			case !untouched:
				// The picker is modal; opening it now would eat what is being typed.
				a.ui.Notify("%d symbols match %q — F4 again to choose", len(items), q)
			default:
				a.pickAction = nil
				a.ui.ShowPicker(fmt.Sprintf("symbols matching %q", q), items)
			}
		}
	})
}
