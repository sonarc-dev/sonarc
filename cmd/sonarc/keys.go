package main

import (
	"fmt"

	"github.com/gdamore/tcell/v2"

	"github.com/sonarc-dev/sonarc/internal/ui"
)

// keySpec identifies a key combination. Commands are bound to these rather than
// handled inline, so one table drives dispatch, the help screen, and rebinding.
type keySpec struct {
	key tcell.Key
	r   rune
	mod tcell.ModMask
}

// specOf normalizes an event into a lookup key. tcell reports the rune
// alongside control keys; zeroing it keeps the table unambiguous.
func specOf(ev *tcell.EventKey) keySpec {
	s := keySpec{key: ev.Key(), mod: ev.Modifiers()}
	if s.key == tcell.KeyRune {
		s.r = ev.Rune()
	} else {
		// tcell already folds Ctrl+letter into its own key constant, so a
		// stray ModCtrl on those would prevent the binding from matching.
		s.mod &^= tcell.ModCtrl
	}
	return s
}

// command is a named editor action.
type command struct {
	help string
	run  func(*app)
}

// commands is the single source of truth for what the editor can do. The help
// overlay and the keymap are both generated from it.
//
// Populated in init rather than as a literal: the help command reads this table
// to describe itself, and a literal would be an initialization cycle.
var commands map[string]command

func init() { commands = commandTable() }

func commandTable() map[string]command {
	return map[string]command{
		"save":           {"save the file", (*app).cmdSave},
		"save-as":        {"save under a new name", (*app).cmdSaveAs},
		"open":           {"open a file", (*app).cmdOpenFile},
		"quit":           {"quit", (*app).cmdQuit},
		"undo":           {"undo", func(a *app) { a.reportIf(a.v().Undo(), "nothing to undo") }},
		"redo":           {"redo", func(a *app) { a.reportIf(a.v().Redo(), "nothing to redo") }},
		"cut":            {"cut", (*app).cmdCut},
		"copy":           {"copy", (*app).cmdCopy},
		"paste":          {"paste", (*app).cmdPaste},
		"select-all":     {"select all", func(a *app) { a.v().SelectAll() }},
		"goto-line":      {"go to line", (*app).cmdGotoLine},
		"delete-line":    {"delete the current line", func(a *app) { a.v().DeleteLine() }},
		"help":           {"show keyboard help", (*app).cmdHelp},
		"open-fuzzy":     {"open a file by name (fuzzy)", (*app).cmdOpenFuzzy},
		"palette":        {"show the command palette", (*app).cmdPalette},
		"toggle-sidebar": {"show or hide the file-tree sidebar", (*app).cmdToggleSidebar},
		"select-theme":   {"choose a color theme", (*app).cmdSelectTheme},
		"edit-keys":      {"change key bindings (keys.conf)", (*app).cmdEditKeys},

		// Split panes.
		"split-right": {"split: show this file again, side by side", func(a *app) { a.cmdSplit(ui.SplitRight) }},
		"split-below": {"split: show this file again, one above the other", func(a *app) { a.cmdSplit(ui.SplitBelow) }},
		"other-pane":  {"switch to the other pane", (*app).cmdOtherPane},
		"only-pane":   {"close the other pane", (*app).cmdOnlyPane},

		// In-file search.
		"find":         {"find in this file", (*app).cmdFind},
		"find-next":    {"next match", (*app).cmdFindNext},
		"find-prev":    {"previous match", (*app).cmdFindPrev},
		"replace":      {"find and replace", (*app).cmdReplace},
		"clear-search": {"clear search highlighting", (*app).cmdClearSearch},

		// Navigation: the reason this editor exists.
		"goto-definition": {"go to definition", (*app).cmdGotoDefinition},
		"peek-definition": {"show the definition here, without going there", (*app).cmdPeekDefinition},
		"find-references": {"find references", (*app).cmdFindReferences},
		"find-callers":    {"find callers (cscope)", (*app).cmdFindCallers},
		"find-callees":    {"find functions called (cscope)", (*app).cmdFindCallees},
		"find-symbol":     {"search symbols by name", (*app).cmdFindSymbol},
		"outline":         {"go to a definition in this file (outline)", (*app).cmdOutline},
		"find-text":       {"search text across the project", (*app).cmdFindText},
		"find-includers":  {"find files including this one (cscope)", (*app).cmdFindIncluders},
		"find-assign":     {"find assignments to a symbol (cscope)", (*app).cmdFindAssignments},
		"jump-back":       {"jump back", (*app).cmdJumpBack},
		"jump-forward":    {"jump forward again", (*app).cmdJumpForward},
		"rebuild-index":   {"rebuild tags and cscope databases", (*app).cmdRebuildIndex},
		"index-status":    {"show which indexes are in use", (*app).cmdIndexStatus},

		// Git: what changed since the last commit.
		"show-changes":  {"diff this file against the last commit", (*app).cmdShowChanges},
		"next-change":   {"next changed block in this file", (*app).cmdNextChange},
		"prev-change":   {"previous changed block in this file", (*app).cmdPrevChange},
		"changed-files": {"go to the list of changed files in the sidebar", (*app).cmdChangesList},

		"close-file":   {"close this file", (*app).cmdCloseFile},
		"reload-file":  {"reload this file from disk", (*app).cmdReloadFile},
		"close-others": {"close all other files", (*app).cmdCloseOthers},
		"open-files":   {"list open files", (*app).cmdOpenFiles},
		"next-buffer":  {"next open file", func(a *app) { a.nextBuffer(1) }},
		"prev-buffer":  {"previous open file", func(a *app) { a.nextBuffer(-1) }},
		"close-panel":  {"close the results panel", func(a *app) { a.ui.Panel.Close() }},
		"next-result":  {"next result", func(a *app) { a.moveResult(1) }},
		"prev-result":  {"previous result", func(a *app) { a.moveResult(-1) }},

		// F3 means "next thing I am looking at": a search match when a search
		// is active, otherwise the next entry in the results panel. Two keys
		// for one idea would be worse than one key that knows the context.
		"next-match-or-result": {"next match or result", func(a *app) { a.stepForward(1) }},
		"prev-match-or-result": {"previous match or result", func(a *app) { a.stepForward(-1) }},
	}
}

// keymap holds the direct bindings.
//
// Every entry uses plain Ctrl, Alt, or a function key. Nothing here depends on
// Ctrl+Shift, which tmux does not report unless extended-keys is enabled and
// screen never reports at all. Anything that would want Ctrl+Shift lives in the
// chord table instead.
var keymap = map[keySpec]string{
	{key: tcell.KeyCtrlS}: "save",
	{key: tcell.KeyCtrlQ}: "quit",
	{key: tcell.KeyCtrlZ}: "undo",
	{key: tcell.KeyCtrlY}: "redo",
	{key: tcell.KeyCtrlX}: "cut",
	{key: tcell.KeyCtrlC}: "copy",
	{key: tcell.KeyCtrlV}: "paste",
	{key: tcell.KeyCtrlA}: "select-all",
	{key: tcell.KeyCtrlG}: "goto-line",
	{key: tcell.KeyCtrlO}: "open",
	{key: tcell.KeyF1}:    "help",
	{key: tcell.KeyCtrlF}: "find",
	{key: tcell.KeyCtrlP}: "open-fuzzy",
	{key: tcell.KeyF6}:    "palette",
	{key: tcell.KeyCtrlE}: "toggle-sidebar",
	{key: tcell.KeyCtrlW}: "close-file",
	{key: tcell.KeyCtrlD}: "delete-line",
	{key: tcell.KeyF8}:    "show-changes",
	{key: tcell.KeyF9}:    "other-pane",

	// Replace is on Ctrl+R, not the conventional Ctrl+H: a terminal sends 0x08
	// for Ctrl+H and tcell reports that as Backspace before it ever reaches the
	// control-key branch, so a KeyCtrlH binding could never fire. Guarded by
	// TestNoBindingUsesAnUnreachableKey.
	{key: tcell.KeyCtrlR}: "replace",

	// Navigation. Ctrl+] and Ctrl+T are what long-time vi users reach for, and
	// cost nothing to support; F12 and Alt+Left are what everyone else expects.
	{key: tcell.KeyCtrlRightSq}:              "goto-definition",
	{key: tcell.KeyF12}:                      "goto-definition",
	{key: tcell.KeyCtrlT}:                    "jump-back",
	{key: tcell.KeyLeft, mod: tcell.ModAlt}:  "jump-back",
	{key: tcell.KeyRight, mod: tcell.ModAlt}: "jump-forward",
	{key: tcell.KeyDown, mod: tcell.ModAlt}:  "next-change",
	{key: tcell.KeyUp, mod: tcell.ModAlt}:    "prev-change",
	{key: tcell.KeyF7}:                       "find-references",
	{key: tcell.KeyF3}:                       "next-match-or-result",
	{key: tcell.KeyF3, mod: tcell.ModShift}:  "prev-match-or-result",
	{key: tcell.KeyF2}:                       "prev-match-or-result",
	{key: tcell.KeyF4}:                       "find-symbol",
	{key: tcell.KeyF5}:                       "rebuild-index",
}

// chordPrefix opens a two-key sequence. This is VS Code's own convention, and
// it is the primary way to reach commands here rather than a fallback: in tmux,
// Ctrl+Shift combinations simply do not arrive.
const chordPrefix = tcell.KeyCtrlK

// chords maps the second key of a Ctrl+K sequence to a command. Letters are
// matched case-insensitively.
var chords = map[rune]string{
	'r': "find-references",
	'c': "find-callers",
	'd': "find-callees",
	's': "find-symbol",
	'f': "find-text",
	'i': "find-includers",
	'a': "find-assign",
	'g': "goto-definition",
	'b': "jump-back",
	'j': "jump-forward",
	'u': "open-files",
	'l': "outline",
	'v': "peek-definition",
	'!': "reload-file",
	'=': "show-changes",
	']': "next-change",
	'[': "prev-change",
	'm': "changed-files",
	'z': "close-others",
	'n': "next-buffer",
	'p': "prev-buffer",
	'w': "save-as",
	'q': "close-panel",
	'x': "rebuild-index",
	'/': "find",
	'o': "open-fuzzy",
	'k': "palette",
	'e': "replace",
	'?': "index-status",
	'h': "help",
	't': "toggle-sidebar",
	'1': "only-pane",
	'2': "split-below",
	'3': "split-right",
	';': "other-pane",
}

func (a *app) onKey(ev *tcell.EventKey) {
	// A pasted block arrives as key events between paste start and end; collect
	// it verbatim instead of treating it as typing.
	if a.pasting {
		switch ev.Key() {
		case tcell.KeyRune:
			a.pasteBuf = append(a.pasteBuf, []byte(string(ev.Rune()))...)
		case tcell.KeyEnter:
			a.pasteBuf = append(a.pasteBuf, '\n')
		case tcell.KeyTab:
			a.pasteBuf = append(a.pasteBuf, '\t')
		}
		return
	}

	a.keyCount++

	// The picker is modal: it owns every keystroke until dismissed.
	if a.ui.Picker.Open {
		a.pickerKey(ev)
		return
	}

	// Esc stops a running search before it does anything else, since waiting
	// out a slow one is the alternative.
	if a.running != nil && ev.Key() == tcell.KeyEscape {
		a.cancelQuery("cancelled")
		return
	}

	// Second key of a chord.
	if a.chord != 0 {
		prefix := a.chord
		a.chord = 0
		a.ui.Msg = ""
		if prefix == chordPrefix {
			if a.runChord(ev) {
				return
			}
			a.ui.Notify("unknown chord: Ctrl+K %s", describeKey(ev))
			return
		}
	}

	// Any keystroke clears a transient message.
	a.ui.Msg = ""

	if ev.Key() == chordPrefix {
		a.chord = chordPrefix
		a.ui.Notify("%s", a.chordHint())
		return
	}

	if name, ok := a.keys.keys[specOf(ev)]; ok {
		if c, ok := commands[name]; ok {
			c.run(a)
			return
		}
	}

	// The sidebar takes navigation keys while it has focus, mirroring the
	// picker → sidebar → panel → text precedence mouse events follow.
	if a.ui.Sidebar.Focused && a.sidebarKey(ev) {
		return
	}

	// A diff on screen takes the rest: the text under it is not what the user
	// is looking at, so keys must not edit it.
	if a.ui.Diff.Open {
		a.diffKey(ev)
		return
	}

	// The results panel takes navigation keys while it has focus, so walking a
	// call graph does not require reaching for the mouse.
	if a.ui.Panel.Open && a.panelKey(ev) {
		return
	}

	a.editKey(ev)
}

// chordHint is the message shown after Ctrl+K: the chords for the commands
// people reach for most, as they are bound now.
func (a *app) chordHint() string {
	hint := "Ctrl+K ..."
	for _, c := range []struct{ name, label string }{
		{"find-references", "refs"}, {"find-callers", "callers"}, {"find-callees", "callees"},
		{"find-symbol", "symbol"}, {"find-text", "text"}, {"index-status", "status"},
	} {
		if r, ok := a.keys.chordFor(c.name); ok {
			hint += fmt.Sprintf("  %c %s", r, c.label)
		}
	}
	return hint
}

// runChord dispatches the second key of a Ctrl+K sequence.
func (a *app) runChord(ev *tcell.EventKey) bool {
	r := ev.Rune()
	if ev.Key() != tcell.KeyRune {
		return false
	}
	if r >= 'A' && r <= 'Z' {
		r += 'a' - 'A' // chords are case-insensitive
	}
	name, ok := a.keys.chords[r]
	if !ok {
		return false
	}
	c, ok := commands[name]
	if !ok {
		return false
	}
	c.run(a)
	return true
}

// describeKey renders a key for an error message.
func describeKey(ev *tcell.EventKey) string {
	if ev.Key() == tcell.KeyRune {
		return string(ev.Rune())
	}
	if n, ok := ctrlNames[ev.Key()]; ok {
		return n
	}
	return tcell.KeyNames[ev.Key()]
}
