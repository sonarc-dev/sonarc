package main

import (
	"fmt"
	"path/filepath"
	"sort"

	"sonarc/internal/index/provider"
	"sonarc/internal/ui"

	"github.com/gdamore/tcell/v2"
)

// cmdOpenFuzzy opens the file picker.
//
// The candidate list comes from the built-in indexer: git's file list in a git
// work tree, a walk otherwise. Either way it respects .gitignore and skips build
// output. That is the difference between a
// list of the project's source files and a list of every object file in it.
func (a *app) cmdOpenFuzzy() {
	if a.builtin == nil || !a.builtin.Available() {
		a.ui.Notify("still indexing the project — use Ctrl+O to open by name")
		return
	}
	files := a.builtin.Files()
	if len(files) == 0 {
		a.ui.Notify("no files indexed under %s", a.root)
		return
	}
	items := make([]ui.PickerItem, 0, len(files))
	for _, f := range files {
		rel := shortPath(a.root, f)
		items = append(items, ui.PickerItem{
			Label:  filepath.Base(f),
			Detail: rel,
			Loc:    provider.Location{Path: f},
		})
	}
	a.ui.ShowPicker(fmt.Sprintf("open file  (%d indexed)", len(files)), items)
	a.pickAction = a.openPicked
}

// openPicked opens the chosen file without recording a jump: opening a file is
// not navigation you would want to undo with Ctrl+T.
func (a *app) openPicked(it ui.PickerItem) {
	if err := a.openFile(it.Loc.Path); err != nil {
		a.ui.Error("cannot open: %v", err)
		return
	}
	a.ui.Notify("%s", shortPath(a.root, it.Loc.Path))
}

// cmdPalette lists every command so they can be found by name.
//
// This is what makes the editor discoverable: nothing is reachable only by a
// shortcut you would have to already know. Each entry shows its key binding, so
// the palette also teaches the shortcuts.
func (a *app) cmdPalette() {
	type entry struct{ name, help, keys string }
	var entries []entry

	for name, c := range commands {
		entries = append(entries, entry{name: name, help: c.help, keys: bindingsFor(name)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].help < entries[j].help })

	items := make([]ui.PickerItem, 0, len(entries))
	for _, e := range entries {
		detail := e.keys
		if detail == "" {
			detail = "(no shortcut)"
		}
		items = append(items, ui.PickerItem{
			Label:  e.help,
			Detail: detail,
			// Command palette entries carry a name rather than a location.
			Loc: provider.Location{Path: "", Text: e.name},
		})
	}
	a.ui.ShowPicker("command palette", items)
	a.pickAction = a.runPicked
}

// runPicked executes the command chosen from the palette.
func (a *app) runPicked(it ui.PickerItem) {
	c, ok := commands[it.Loc.Text]
	if !ok {
		a.ui.Error("unknown command %q", it.Loc.Text)
		return
	}
	c.run(a)
}

// bindingsFor renders every key bound to a command, direct keys and chords.
func bindingsFor(name string) string {
	var out []string
	for spec, n := range keymap {
		if n == name {
			out = append(out, keyName(spec))
		}
	}
	for r, n := range chords {
		if n == name {
			out = append(out, fmt.Sprintf("Ctrl+K %c", r))
		}
	}
	sort.Strings(out)
	s := ""
	for i, b := range out {
		if i > 0 {
			s += "  /  "
		}
		s += b
	}
	return s
}

// symbolPickerItems builds picker entries from symbols, shared by the symbol
// search and goto-definition disambiguation. It takes the root rather than
// reading it from the app because the symbol search calls it off the UI
// goroutine.
func symbolPickerItems(root string, syms []provider.Symbol, showSource bool) []ui.PickerItem {
	items := make([]ui.PickerItem, 0, len(syms))
	for _, s := range syms {
		detail := fmt.Sprintf("%s:%d", shortPath(root, s.Loc.Path), s.Loc.Line)
		if k := s.Kind.String(); k != "" {
			detail = k + "  " + detail
		}
		if s.Signature != "" {
			detail += "  " + s.Signature
		}
		if showSource {
			detail += "  [" + s.Source + "]"
		}
		items = append(items, ui.PickerItem{Label: s.Name, Detail: detail, Loc: s.Loc})
	}
	return items
}

// pickerKey drives the modal chooser.
func (a *app) pickerKey(ev *tcell.EventKey) {
	p := &a.ui.Picker
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		p.Open = false
		a.pickAction = nil
		a.ui.Notify("cancelled")
	case tcell.KeyEnter:
		item, ok := p.Current()
		p.Open = false
		action := a.pickAction
		a.pickAction = nil
		if !ok {
			return
		}
		if action != nil {
			action(item)
			return
		}
		if a.goToSymbol(item.Loc, item.Label) {
			a.ui.Notify("%s  —  %s", item.Label, item.Detail)
		}
	case tcell.KeyUp, tcell.KeyCtrlP:
		p.Move(-1)
	case tcell.KeyDown, tcell.KeyCtrlN:
		p.Move(1)
	case tcell.KeyPgUp:
		p.Move(-10)
	case tcell.KeyPgDn:
		p.Move(10)
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if f := []rune(p.Filter); len(f) > 0 {
			p.SetFilter(string(f[:len(f)-1]))
		}
	case tcell.KeyCtrlU:
		p.SetFilter("")
	case tcell.KeyRune:
		p.SetFilter(p.Filter + string(ev.Rune()))
	}
}
