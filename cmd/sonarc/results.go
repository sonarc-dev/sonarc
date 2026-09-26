package main

import (
	"github.com/sonarc-dev/sonarc/internal/index/provider"
	"path/filepath"

	"github.com/gdamore/tcell/v2"
)

// panelKey handles keys that belong to the results panel. It reports whether
// the key was consumed.
func (a *app) panelKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyEscape:
		a.ui.Panel.Close()
		return true
	case tcell.KeyEnter:
		if ev.Modifiers()&tcell.ModAlt != 0 {
			a.openSelectedResult()
			return true
		}
	}
	return false
}

// showResults puts locations in the bottom panel and jumps to the first.
//
// note says how the results were obtained (timings, whether they are complete)
// and comes before the key hints so a narrow terminal clips the hints, not it.
//
// jump says whether to move to the first result. It is false when the user kept
// typing while the search ran: the panel still opens, but the cursor is not
// pulled into another file mid-sentence.
func (a *app) showResults(title string, locs []provider.Location, note string, jump bool) {
	a.ui.Panel.Show(title, locs)
	if note != "" {
		a.ui.Notify("%d result(s) — %s — Enter opens, Esc closes", len(locs), note)
	} else {
		a.ui.Notify("%d result(s) — Enter opens, F3/F2 next and previous, Esc closes", len(locs))
	}
	if jump && len(locs) > 0 {
		a.previewResult()
	}
}

// moveResult changes the panel selection and previews it.
func (a *app) moveResult(n int) {
	if !a.ui.Panel.Open {
		a.ui.Notify("no results open")
		return
	}
	a.ui.Panel.Move(n)
	a.previewResult()
}

// previewResult shows the selected result without disturbing the tag stack, so
// scanning a long list does not fill the stack with positions nobody chose.
func (a *app) previewResult() {
	loc, ok := a.ui.Panel.Current()
	if !ok {
		return
	}
	if loc.Path != "" && loc.Path != a.v().Buf.Path() {
		if err := a.openFile(loc.Path); err != nil {
			a.ui.Error("cannot open %s: %v", filepath.Base(loc.Path), err)
			return
		}
	}
	if loc.Line > 0 {
		a.v().Goto(loc.Line)
	}
}

// openSelectedResult jumps to the selected result and closes the panel.
func (a *app) openSelectedResult() {
	loc, ok := a.ui.Panel.Current()
	if !ok {
		return
	}
	a.goTo(loc)
	a.ui.Panel.Close()
}

// stepForward advances whichever list the user is working through: an active
// search takes priority, otherwise the results panel.
func (a *app) stepForward(n int) {
	if a.matcher != nil && !a.matcher.Empty() && len(a.ui.Matches) > 0 {
		a.findStep(n > 0)
		return
	}
	if a.ui.Panel.Open {
		a.moveResult(n)
		return
	}
	a.ui.Notify("nothing to step through — Ctrl+F searches, Ctrl+K r finds references")
}
