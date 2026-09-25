package main

import (
	"sonarc/internal/ui"

	"github.com/gdamore/tcell/v2"
)

// sidebarClick selects and activates the tree entry under a press. A click
// always gives the sidebar keyboard focus, so arrow keys carry on from there.
func (a *app) sidebarClick(y int) {
	s := &a.ui.Sidebar
	if s.Tree == nil {
		return
	}
	s.Focused = true
	target, idx := a.ui.SidebarHit(y)
	switch target {
	case ui.TargetTree:
		s.InChanges = false
		s.Tree.SelectRow(idx)
		a.sidebarActivate()
	case ui.TargetChangesHeader, ui.TargetChange:
		a.changesClick(target, idx)
	}
}

// cmdToggleSidebar shows or hides the file-tree sidebar. This is the only way
// to reach it without a mouse: opening it always pins it visible regardless
// of terminal width and gives it keyboard focus.
func (a *app) cmdToggleSidebar() {
	w, _ := a.scr.Size()
	a.ui.Sidebar.Toggle(w)
}

// sidebarListRows is how many rows of the tree are visible, matching the
// layout drawSidebar uses (one row reserved for the header).
func (a *app) sidebarListRows() int { return a.ui.SidebarListRows() }

// sidebarKey handles a keystroke while the sidebar has focus. It reports
// whether the key was consumed.
func (a *app) sidebarKey(ev *tcell.EventKey) bool {
	s := &a.ui.Sidebar
	if s.Tree == nil {
		s.Focused = false
		return false
	}
	if s.InChanges && s.Changes.Enabled && !s.Changes.Collapsed {
		return a.changesKey(ev)
	}
	switch ev.Key() {
	case tcell.KeyTab:
		// Tab moves between the tree and the list of changed files.
		if s.Changes.Enabled {
			if s.Changes.Collapsed {
				a.setChangesCollapsed(false)
			}
			s.InChanges = true
		}
		return true
	case tcell.KeyUp:
		s.Tree.Move(-1, a.sidebarListRows())
		return true
	case tcell.KeyDown:
		s.Tree.Move(1, a.sidebarListRows())
		return true
	case tcell.KeyRight, tcell.KeyEnter:
		a.sidebarActivate()
		return true
	case tcell.KeyLeft:
		a.sidebarCollapse()
		return true
	case tcell.KeyRune:
		switch ev.Rune() {
		case '<':
			a.resizeSidebarBy(-ui.SidebarStep)
			return true
		case '>':
			a.resizeSidebarBy(ui.SidebarStep)
			return true
		}
	case tcell.KeyEscape:
		// Leaves the tree exactly as it is; only focus moves back to the
		// text. Closing the sidebar entirely is Ctrl+E's job.
		s.Focused = false
		return true
	}
	return false
}

// resizeSidebarBy moves the sidebar's edge by delta columns and keeps the
// result for the next session.
func (a *app) resizeSidebarBy(delta int) {
	before := a.ui.SidebarWidth()
	if a.ui.ResizeSidebar(before+delta) != before {
		a.saveState()
	}
}

// sidebarActivate opens the selected file, or expands/collapses the selected
// directory. Shared with mouse handling so a click and Enter behave the same.
func (a *app) sidebarActivate() {
	s := &a.ui.Sidebar
	n, ok := s.Tree.Selected()
	if !ok {
		return
	}
	if n.IsDir {
		s.Tree.Toggle(s.Tree.Sel)
		return
	}
	if err := a.openFile(n.Path); err != nil {
		a.ui.Error("cannot open: %v", err)
		return
	}
	s.Focused = false // the opened file is now what the keyboard edits
}

// sidebarCollapse closes the selected directory if it is open.
func (a *app) sidebarCollapse() {
	s := &a.ui.Sidebar
	n, ok := s.Tree.Selected()
	if !ok || !n.IsDir || !n.Expanded {
		return
	}
	s.Tree.Toggle(s.Tree.Sel)
}
