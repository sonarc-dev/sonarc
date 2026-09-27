package main

import (
	"bytes"
	"fmt"

	"github.com/gdamore/tcell/v2"
)

func (a *app) onPaste(ev *tcell.EventPaste) {
	if ev.Start() {
		a.pasting = true
		a.pasteBuf = a.pasteBuf[:0]
		return
	}
	a.pasting = false
	if len(a.pasteBuf) > 0 {
		v := a.v()
		v.ForEach(func() { v.Insert(a.pasteBuf) })
	}
}

func (a *app) cmdCut() {
	v := a.v()
	if v.Carets() > 1 {
		a.copyAll()
		v.ForEach(func() { v.Cut() })
		return
	}
	if s := v.Cut(); s != nil {
		a.setClipboard(s)
	}
}

func (a *app) cmdCopy() {
	v := a.v()
	if v.Carets() > 1 {
		a.copyAll()
		a.ui.Notify("copied from %d cursors", v.Carets())
		return
	}
	a.setClipboard(v.Copy())
	a.ui.Notify("copied")
}

// copyAll puts every cursor's selection on the clipboard, one per line, and
// remembers them apart so pasting at as many cursors gives one to each.
func (a *app) copyAll() {
	parts := a.v().CopyAll()
	trimmed := make([][]byte, len(parts))
	for i, p := range parts {
		trimmed[i] = bytes.TrimSuffix(p, []byte("\n"))
	}
	a.setClipboard(bytes.Join(trimmed, []byte("\n")))
	a.clipParts = parts
}

func (a *app) cmdPaste() {
	v := a.v()
	if v.Carets() > 1 {
		v.PasteEach(a.clip, a.clipParts)
		return
	}
	v.Paste(a.clip)
}

// setClipboard stores text internally and also offers it to the system
// clipboard over OSC 52, which is what carries a copy from a server back to the
// machine you are actually sitting at.
func (a *app) setClipboard(s []byte) {
	a.clip = append(a.clip[:0], s...)
	a.clipParts = nil
	a.scr.SetClipboard(s)
}

func (a *app) cmdGotoLine() {
	s, ok := a.prompt("Go to line: ", "")
	if !ok || s == "" {
		return
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n < 1 {
		a.ui.Error("not a line number: %s", s)
		return
	}
	a.v().Goto(n)
}

// editKey handles ordinary editing and movement, at every cursor when there
// are several. Esc with several cursors goes back to one.
func (a *app) editKey(ev *tcell.EventKey) {
	v := a.v()
	if v.Carets() > 1 {
		if ev.Key() == tcell.KeyEscape {
			v.ClearCarets()
			return
		}
		v.ForEach(func() { a.editOne(ev) })
		return
	}
	a.editOne(ev)
}

// editOne applies one key at the cursor.
func (a *app) editOne(ev *tcell.EventKey) {
	extend := ev.Modifiers()&tcell.ModShift != 0
	word := ev.Modifiers()&tcell.ModCtrl != 0
	v := a.v()

	switch ev.Key() {
	case tcell.KeyLeft:
		if word {
			v.MoveWordLeft(extend)
		} else {
			v.MoveLeft(extend)
		}
	case tcell.KeyRight:
		if word {
			v.MoveWordRight(extend)
		} else {
			v.MoveRight(extend)
		}
	case tcell.KeyUp:
		v.MoveUp(extend)
	case tcell.KeyDown:
		v.MoveDown(extend)
	case tcell.KeyHome:
		// Ctrl+Home arrives as Home with ModCtrl rather than as its own key.
		if word {
			v.MoveDocStart(extend)
		} else {
			v.MoveHome(extend)
		}
	case tcell.KeyEnd:
		if word {
			v.MoveDocEnd(extend)
		} else {
			v.MoveEnd(extend)
		}
	case tcell.KeyPgUp:
		v.PageUp(extend)
	case tcell.KeyPgDn:
		v.PageDown(extend)
	case tcell.KeyEnter:
		v.InsertNewline()
	case tcell.KeyTab:
		v.InsertTab()
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if word {
			v.DeleteWordBackward()
		} else {
			v.DeleteBackward()
		}
	case tcell.KeyDelete:
		v.DeleteForward()
	case tcell.KeyEscape:
		if a.ui.Panel.Open {
			a.ui.Panel.Close()
		} else {
			v.ClearSelection()
		}
	case tcell.KeyRune:
		v.Insert([]byte(string(ev.Rune())))
	}
}

// addCaret reports on adding a cursor: how many there are, or why none was
// added.
func (a *app) addCaret(added bool) {
	v := a.v()
	if !added {
		a.ui.Notify("no more places to add a cursor")
		return
	}
	a.reportCarets(v.Carets())
}

func (a *app) reportCarets(n int) {
	if n > 1 {
		a.ui.Notify("%d cursors: typing goes to all of them; Esc goes back to one", n)
	}
}
