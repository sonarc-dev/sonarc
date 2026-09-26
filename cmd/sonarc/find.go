package main

import (
	"fmt"

	"github.com/gdamore/tcell/v2"

	"github.com/sonarc-dev/sonarc/internal/buffer"
	"github.com/sonarc-dev/sonarc/internal/search"
)

// maxHighlights bounds how many occurrences are highlighted at once. On a file
// with tens of thousands of matches, drawing every one costs more than it tells
// the reader.
const maxHighlights = 2000

// setMatches recompiles the search and refreshes what the renderer highlights.
func (a *app) setMatches(m *search.Matcher) {
	a.matcher = m
	if m == nil || m.Empty() {
		a.ui.Matches = nil
		return
	}
	a.ui.Matches = m.All(a.v().Buf, maxHighlights)
}

// selectMatch moves the cursor onto a match and selects it, so the next
// keystroke replaces it the way a GUI editor behaves.
func (a *app) selectMatch(mt search.Match) {
	v := a.v()
	v.SetCursor(mt.From)
	v.Anchor = mt.From
	v.Head = mt.To
	v.ScrollToCursor()
}

// cmdFind runs an incremental search.
//
// Results update on every keystroke and the view follows them, so the pattern
// can be refined while watching where it lands. Escape restores the original
// position: an abandoned search must not move the cursor.
func (a *app) cmdFind() {
	origin := a.v().Head
	pattern := a.lastSearch
	accepted := false

	defer func() {
		if !accepted {
			a.v().SetCursor(origin)
			a.setMatches(nil)
		}
	}()

	for {
		var (
			m       *search.Matcher
			err     error
			status  string
			matches int
		)
		if pattern != "" {
			m, err = search.Compile(pattern, a.searchOpts)
			if err != nil {
				status = "  [bad pattern]"
			} else {
				a.setMatches(m)
				matches = len(a.ui.Matches)
				if mt, ok := m.Next(a.v().Buf, origin); ok {
					a.selectMatch(mt)
					status = fmt.Sprintf("  [%d matches]", matches)
				} else {
					status = "  [no matches]"
					a.v().SetCursor(origin)
				}
			}
		} else {
			a.setMatches(nil)
			a.v().SetCursor(origin)
		}

		a.ui.Draw()
		a.ui.DrawPrompt(a.findPromptLabel(), pattern+status)
		a.scr.Show()

		ev := a.poll()
		key, ok := ev.(*tcell.EventKey)
		if !ok {
			if _, isResize := ev.(*tcell.EventResize); isResize {
				a.scr.Sync()
			}
			continue
		}

		switch key.Key() {
		case tcell.KeyEnter:
			if m != nil && err == nil && matches > 0 {
				accepted = true
				a.lastSearch = pattern
				a.matcher = m
				a.ui.Notify("%d matches — F3 next, Shift+F3 previous, Esc clears", matches)
				return
			}
			a.ui.Notify("no matches for %q", pattern)
			return
		case tcell.KeyEscape, tcell.KeyCtrlC:
			a.ui.Notify("search cancelled")
			return
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if r := []rune(pattern); len(r) > 0 {
				pattern = string(r[:len(r)-1])
			}
		case tcell.KeyCtrlU:
			pattern = ""
		case tcell.KeyDown, tcell.KeyCtrlN, tcell.KeyF3:
			// Step through matches without leaving the prompt.
			if m != nil && err == nil {
				if mt, ok := m.Next(a.v().Buf, a.v().Head); ok {
					a.selectMatch(mt)
					origin = mt.From
				}
			}
		case tcell.KeyUp, tcell.KeyCtrlP:
			if m != nil && err == nil {
				if mt, ok := m.Prev(a.v().Buf, a.v().Head); ok {
					a.selectMatch(mt)
					origin = mt.From
				}
			}
		case tcell.KeyCtrlR:
			// Toggle regex mode mid-search rather than forcing a restart.
			a.searchOpts.Regex = !a.searchOpts.Regex
		case tcell.KeyCtrlW:
			a.searchOpts.Word = !a.searchOpts.Word
		case tcell.KeyRune:
			pattern += string(key.Rune())
		}
	}
}

// findPromptLabel shows which search modes are active, so the toggles are
// discoverable rather than hidden state.
func (a *app) findPromptLabel() string {
	label := "Find"
	if a.searchOpts.Regex {
		label += " [regex]"
	}
	if a.searchOpts.Word {
		label += " [word]"
	}
	return label + ": "
}

// findStep moves to the next or previous match of the last accepted search.
func (a *app) findStep(forward bool) {
	if a.matcher == nil || a.matcher.Empty() {
		a.ui.Notify("nothing to search for yet — press Ctrl+F")
		return
	}
	v := a.v()
	var (
		mt search.Match
		ok bool
	)
	if forward {
		// Start past the current match so repeated presses advance.
		from := v.Head
		if v.HasSelection() {
			_, to := v.Selection()
			from = to
		}
		mt, ok = a.matcher.Next(v.Buf, from)
	} else {
		from := v.Head
		if v.HasSelection() {
			f, _ := v.Selection()
			from = f
		}
		mt, ok = a.matcher.Prev(v.Buf, from)
	}
	if !ok {
		a.ui.Notify("no matches for %q", a.matcher.Pattern())
		return
	}
	a.setMatches(a.matcher)
	a.selectMatch(mt)
	a.ui.Notify("%s  [%d matches]", a.matcher.Pattern(), len(a.ui.Matches))
}

// cmdFindNext and cmdFindPrev step through the current search.
func (a *app) cmdFindNext() { a.findStep(true) }
func (a *app) cmdFindPrev() { a.findStep(false) }

// cmdClearSearch removes the highlighting.
func (a *app) cmdClearSearch() {
	a.setMatches(nil)
	a.ui.Notify("search cleared")
}

// cmdReplace performs a search and replace, confirming each occurrence.
//
// Every replacement is offered individually by default, with an explicit "all"
// option. A silent replace-all across a file is the kind of thing that is only
// noticed after it has been saved.
func (a *app) cmdReplace() {
	pattern, ok := a.prompt(a.findPromptLabel(), a.lastSearch)
	if !ok || pattern == "" {
		return
	}
	m, err := search.Compile(pattern, a.searchOpts)
	if err != nil {
		a.ui.Error("bad pattern: %v", err)
		return
	}
	replacement, ok := a.prompt("Replace with: ", "")
	if !ok {
		a.ui.Notify("cancelled")
		return
	}

	a.lastSearch = pattern
	a.setMatches(m)
	if len(a.ui.Matches) == 0 {
		a.ui.Notify("no matches for %q", pattern)
		return
	}

	v := a.v()
	count := 0
	all := false
	// Work from the end backwards so each replacement cannot shift the
	// positions of the matches still to be considered.
	matches := m.All(v.Buf, maxHighlights)
	for i := len(matches) - 1; i >= 0; i-- {
		mt := matches[i]
		if !all {
			a.selectMatch(mt)
			a.ui.Draw()
			a.ui.DrawPrompt("Replace? ", "[y]es  [n]o  [a]ll  [q]uit")
			a.scr.Show()

			ev := a.poll()
			key, isKey := ev.(*tcell.EventKey)
			if !isKey {
				i++ // redraw and ask again about the same match
				continue
			}
			switch key.Rune() {
			case 'y', 'Y':
			case 'a', 'A':
				all = true
			case 'q', 'Q':
				a.finishReplace(count)
				return
			default:
				if key.Key() == tcell.KeyEscape {
					a.finishReplace(count)
					return
				}
				continue // 'n' and anything else skips
			}
		}
		text := m.Expand(v.Buf, mt, replacement)
		v.Buf.Delete(mt.From, mt.To)
		v.Buf.Insert(mt.From, text)
		v.SetCursor(buffer.Pos{Line: mt.From.Line, Col: mt.From.Col + len(text)})
		count++
	}
	a.finishReplace(count)
}

func (a *app) finishReplace(count int) {
	a.setMatches(nil)
	a.v().ClearSelection()
	switch count {
	case 0:
		a.ui.Notify("nothing replaced")
	case 1:
		a.ui.Notify("1 replacement made")
	default:
		a.ui.Notify("%d replacements made", count)
	}
}
