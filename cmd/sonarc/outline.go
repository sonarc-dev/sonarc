package main

import (
	"fmt"
	"path/filepath"

	"sonarc/internal/index/builtin"
	"sonarc/internal/ui"
)

// cmdOutline lists the functions, types and macros defined in the file on
// screen, in file order, to jump to one by name. The list starts on the
// definition the cursor is in, so it also answers "where am I?".
func (a *app) cmdOutline() {
	v := a.v()
	path := v.Buf.Path()
	syms := builtin.Outline(path, v.Buf.NumLines(), v.Buf.Line)
	if len(syms) == 0 {
		a.ui.Notify("no outline for %s: definitions are recognized in C, Go and Python", displayName(v))
		return
	}
	items := make([]ui.PickerItem, 0, len(syms))
	here := 0
	for i, s := range syms {
		items = append(items, ui.PickerItem{
			Label:  s.Name,
			Detail: fmt.Sprintf("%-7s line %d", s.Kind, s.Loc.Line),
			Loc:    s.Loc,
		})
		if s.Loc.Line <= v.Head.Line+1 {
			here = i
		}
	}
	a.ui.ShowPicker(fmt.Sprintf("outline of %s  (%d)", filepath.Base(path), len(syms)), items)
	a.ui.Picker.Move(here)
	a.pickAction = func(it ui.PickerItem) { a.goToSymbol(it.Loc, it.Label) }
}
