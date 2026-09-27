package main

import (
	"github.com/sonarc-dev/sonarc/internal/ui"
	"github.com/sonarc-dev/sonarc/internal/view"
)

// Split panes.
//
// The keyboard is always in one pane, whose view is a.v() and a.ui.View; the
// other pane's view is a.ui.Other. a.views stays the list of open files, one
// view each, so saving, quitting and sessions work as before. When both panes
// show the same file, the second is a mirror: another view of the same
// buffer, not in a.views, with its own cursor and scroll.

// cmdSplit shows the file on screen in a second pane, beside or below the
// first, and moves the keyboard there. Split again with the other direction
// to turn the panes.
func (a *app) cmdSplit(mode ui.SplitMode) {
	if a.ui.Other != nil {
		if a.ui.Split != mode {
			a.ui.Split = mode
			return
		}
		a.ui.Notify("already split: F9 or Ctrl+K ; switches panes, Ctrl+K 1 goes back to one")
		return
	}
	a.closeDiff()
	cur := a.v()
	a.ui.Split = mode
	a.ui.Other = cur
	a.ui.View = cur.Mirror()
	a.ui.SecondFocused = true
	a.ui.Notify("split: F9 or Ctrl+K ; switches panes, Ctrl+K 1 goes back to one")
}

// cmdOtherPane moves the keyboard to the other pane.
func (a *app) cmdOtherPane() {
	if a.ui.Other == nil {
		a.ui.Notify("one pane: Ctrl+K 3 splits side by side, Ctrl+K 2 one above the other")
		return
	}
	a.closeDiff()
	a.ui.View, a.ui.Other = a.ui.Other, a.ui.View
	a.ui.SecondFocused = !a.ui.SecondFocused
	a.ui.View.Sync() // it may have fallen behind edits made in this pane
	a.cur = a.fileIndex(a.ui.View)
	if a.ui.Sidebar.Tree != nil {
		a.ui.Sidebar.Tree.Reveal(a.ui.View.Buf.Path())
	}
}

// cmdOnlyPane goes back to one pane, keeping the one with the keyboard.
func (a *app) cmdOnlyPane() {
	if a.ui.Other == nil {
		a.ui.Notify("only one pane is open")
		return
	}
	a.dropOther()
}

// dropOther closes the pane without the keyboard. If the remaining pane was
// a mirror, the file's own view takes over from it, where the mirror was.
func (a *app) dropOther() {
	other := a.ui.Other
	if other == nil {
		return
	}
	a.ui.Other, a.ui.Split, a.ui.SecondFocused = nil, ui.SplitNone, false
	if !a.isFileView(other) {
		a.forgetView(other)
	}
	if v := a.ui.View; !a.isFileView(v) {
		own := a.views[a.fileIndex(v)]
		own.Sync()
		own.Head, own.Anchor, own.Top, own.Left = v.Head, v.Anchor, v.Top, v.Left
		own.MarkSeen()
		a.forgetView(v)
		a.ui.View = own
	}
}

// isFileView reports whether v is a file's own view, one of a.views, rather
// than a mirror.
func (a *app) isFileView(v *view.View) bool {
	for _, f := range a.views {
		if f == v {
			return true
		}
	}
	return false
}

// fileIndex is the index in a.views of the file v shows.
func (a *app) fileIndex(v *view.View) int {
	for i, f := range a.views {
		if f == v {
			return i
		}
	}
	for i, f := range a.views {
		if f.Buf == v.Buf {
			return i
		}
	}
	return 0
}

// fileView is the open file's own view for the file on screen: what saving,
// reloading and warnings about the disk are tracked against.
func (a *app) fileView() *view.View { return a.views[a.cur] }

// paneFor decides what the focused pane shows when it switches to file f.
// If the other pane already shows f's own view, this pane gets a mirror, so
// the two keep separate cursors.
func (a *app) paneFor(f *view.View) *view.View {
	if a.ui.Other != f {
		return f
	}
	if cur := a.ui.View; cur.Buf == f.Buf && !a.isFileView(cur) {
		return cur // already a mirror of it
	}
	return f.Mirror()
}

// reload rereads v's file from disk. Every view of the file, the open file's
// own and any pane mirroring it, moves to the new text together, so no pane
// is left editing the old copy.
func (a *app) reload(v *view.View) error {
	old := v.Buf
	if err := v.Reload(); err != nil {
		return err
	}
	for _, o := range append(append([]*view.View{}, a.views...), a.ui.View, a.ui.Other) {
		if o != nil && o != v && o.Buf == old {
			o.Adopt(v.Buf)
		}
	}
	return nil
}
