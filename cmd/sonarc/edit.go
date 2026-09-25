package main

import (
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
		a.v().Insert(a.pasteBuf)
	}
}

func (a *app) cmdCut() {
	if s := a.v().Cut(); s != nil {
		a.setClipboard(s)
	}
}

func (a *app) cmdCopy() {
	a.setClipboard(a.v().Copy())
	a.ui.Notify("copied")
}

func (a *app) cmdPaste() { a.v().Paste(a.clip) }

// setClipboard stores text internally and also offers it to the system
// clipboard over OSC 52, which is what carries a copy from a server back to the
// machine you are actually sitting at.
func (a *app) setClipboard(s []byte) {
	a.clip = append(a.clip[:0], s...)
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

// editKey handles ordinary editing and movement.
func (a *app) editKey(ev *tcell.EventKey) {
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
