package main

import (
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// prompt asks a question on the message line and reads a reply, returning false
// if the user cancelled with Escape.
//
// It runs its own event loop rather than adding a mode to the main one. The
// editor is modeless by design, and the few genuinely modal moments — naming a
// file, confirming a discard — are easier to reason about when they read as
// straight-line code.
func (a *app) prompt(question, initial string) (string, bool) {
	input := []rune(initial)
	for {
		a.ui.Draw()
		a.ui.DrawPrompt(question, string(input))
		a.scr.Show()

		ev := a.poll()
		switch ev := ev.(type) {
		case nil:
			return "", false
		case *tcell.EventResize:
			a.scr.Sync()
		case *tcell.EventKey:
			switch ev.Key() {
			case tcell.KeyEnter:
				return string(input), true
			case tcell.KeyEscape, tcell.KeyCtrlC, tcell.KeyCtrlQ:
				return "", false
			case tcell.KeyBackspace, tcell.KeyBackspace2:
				if len(input) > 0 {
					input = input[:len(input)-1]
				}
			case tcell.KeyCtrlU:
				input = input[:0]
			case tcell.KeyCtrlW:
				// Drop the trailing path segment, which is what you want when
				// correcting a filename.
				s := strings.TrimRight(string(input), "/")
				if i := strings.LastIndexAny(s, "/ "); i >= 0 {
					input = []rune(s[:i+1])
				} else {
					input = input[:0]
				}
			case tcell.KeyRune:
				input = append(input, ev.Rune())
			}
		}
	}
}

// cmdHelp shows the keys that actually work on this terminal.
//
// The list is filtered by detected capability rather than being a fixed sheet:
// showing a binding the terminal cannot deliver is how editors earn a
// reputation for not working over ssh.
func (a *app) cmdHelp() {
	a.ui.Draw()
	a.ui.DrawOverlay("sonarc help", a.helpLines())
	a.scr.Show()
	for {
		if _, ok := a.poll().(*tcell.EventKey); ok {
			return
		}
	}
}

// helpLines builds the help text. Split out from cmdHelp so it can be tested
// without the overlay's blocking wait for a keypress.
func (a *app) helpLines() []string {
	var lines []string
	add := func(key, desc string) {
		lines = append(lines, padRight(key, 14)+desc)
	}

	// Bindings from the keymap, so help and behavior cannot drift apart.
	type row struct{ key, desc string }
	var rows []row
	for spec, name := range keymap {
		c, ok := commands[name]
		if !ok {
			continue
		}
		rows = append(rows, row{keyName(spec), c.help})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].desc < rows[j].desc })

	add("Editing", "")
	for _, r := range rows {
		add("  "+r.key, r.desc)
	}
	add("", "")
	add("Movement", "")
	add("  Arrows", "move; hold Shift to select")
	add("  Ctrl+←/→", "move by word")
	add("  Home/End", "start/end of line (Home twice: column 0)")
	add("  Ctrl+Home/End", "start/end of file")
	add("  PgUp/PgDn", "move by a screen")
	switch {
	case a.scr.Caps.MouseBlocked != "":
		add("  Mouse", a.scr.Caps.MouseBlocked)
	case a.scr.Caps.Mouse:
		add("  Mouse", "click to place the cursor, drag to select, wheel to scroll")
	default:
		add("  Mouse", "unavailable on this terminal")
	}
	add("", "")
	add("File tree (Ctrl+E to focus)", "")
	add("  ↑/↓", "move")
	add("  →/Enter, ←", "open or expand, collapse")
	add("  < >", "narrower, wider (or drag its right edge)")
	add("  Esc", "back to the text")
	add("", "")
	add("Terminal", a.scr.Caps.Describe())
	if a.scr.Caps.Tier != 0 {
		add("", "Ctrl+Shift keys are unavailable here; run sonarc -doctor.")
	}
	add("", "")
	add("", "Press any key to close.")
	return lines
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s + " "
	}
	return s + strings.Repeat(" ", n-len(s))
}

// keyName renders a key spec the way a person would say it.
func keyName(s keySpec) string {
	prefix := ""
	if s.mod&tcell.ModAlt != 0 {
		prefix += "Alt+"
	}
	if s.mod&tcell.ModShift != 0 {
		prefix += "Shift+"
	}
	if s.key == tcell.KeyRune {
		return prefix + string(s.r)
	}
	if n, ok := ctrlNames[s.key]; ok {
		return prefix + n
	}
	return prefix + tcell.KeyNames[s.key]
}

var ctrlNames = map[tcell.Key]string{
	tcell.KeyCtrlA: "Ctrl+A", tcell.KeyCtrlB: "Ctrl+B", tcell.KeyCtrlC: "Ctrl+C",
	tcell.KeyCtrlD: "Ctrl+D", tcell.KeyCtrlE: "Ctrl+E", tcell.KeyCtrlF: "Ctrl+F",
	tcell.KeyCtrlG: "Ctrl+G", tcell.KeyCtrlK: "Ctrl+K", tcell.KeyCtrlL: "Ctrl+L",
	tcell.KeyCtrlN: "Ctrl+N", tcell.KeyCtrlO: "Ctrl+O", tcell.KeyCtrlP: "Ctrl+P",
	tcell.KeyCtrlQ: "Ctrl+Q", tcell.KeyCtrlR: "Ctrl+R", tcell.KeyCtrlS: "Ctrl+S",
	tcell.KeyCtrlT: "Ctrl+T", tcell.KeyCtrlU: "Ctrl+U", tcell.KeyCtrlV: "Ctrl+V",
	tcell.KeyCtrlW: "Ctrl+W", tcell.KeyCtrlX: "Ctrl+X", tcell.KeyCtrlY: "Ctrl+Y",
	tcell.KeyCtrlZ: "Ctrl+Z", tcell.KeyCtrlRightSq: "Ctrl+]",
}
