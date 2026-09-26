package main

import (
	"sort"

	"github.com/gdamore/tcell/v2"

	"github.com/sonarc-dev/sonarc/internal/ui"
)

// updateChangesList refills the sidebar's Changes section from git's status.
func (a *app) updateChangesList() {
	items := make([]ui.ChangeItem, 0, len(a.git.changes))
	for p, st := range a.git.changes {
		items = append(items, ui.ChangeItem{Path: p, Rel: shortPath(a.root, p), Status: st})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Rel < items[j].Rel })
	a.ui.Sidebar.Changes.SetItems(items)
}

// cmdChangesList puts the keyboard in the sidebar's list of changed files,
// opening the sidebar and the section if they are closed.
func (a *app) cmdChangesList() {
	if a.git.repo == nil {
		a.ui.Notify("not in a git repository")
		return
	}
	s := &a.ui.Sidebar
	s.Visible, s.Pinned, s.Focused, s.InChanges = true, true, true, true
	if s.Changes.Collapsed {
		a.setChangesCollapsed(false)
	}
	if len(s.Changes.Items) == 0 {
		a.ui.Notify("no changes since the last commit")
	}
}

func (a *app) setChangesCollapsed(c bool) {
	a.ui.Sidebar.Changes.Collapsed = c
	if c {
		a.ui.Sidebar.InChanges = false
	}
	a.saveState()
}

// changesKey handles keys in the sidebar's Changes list. Moving the selection
// shows that file's diff straight away, so a change set can be read file by
// file with the arrow keys alone.
func (a *app) changesKey(ev *tcell.EventKey) bool {
	s := &a.ui.Sidebar
	c := &s.Changes
	rows := a.ui.ChangesRows()
	show := func() {
		if it, ok := c.Selected(); ok {
			a.openDiff(it.Path)
		}
	}
	switch ev.Key() {
	case tcell.KeyUp:
		c.Move(-1, rows)
		show()
	case tcell.KeyDown:
		c.Move(1, rows)
		show()
	case tcell.KeyEnter, tcell.KeyRight:
		// Into the diff, so the arrows scroll it.
		show()
		if a.ui.Diff.Open {
			s.Focused = false
		}
	case tcell.KeyLeft:
		a.setChangesCollapsed(true)
	case tcell.KeyTab:
		s.InChanges = false
	case tcell.KeyEscape:
		s.Focused = false
	default:
		return false
	}
	return true
}

// changesClick handles a press in the Changes section: the header opens and
// closes it, a file shows its diff.
func (a *app) changesClick(target ui.SidebarTarget, idx int) {
	s := &a.ui.Sidebar
	switch target {
	case ui.TargetChangesHeader:
		a.setChangesCollapsed(!s.Changes.Collapsed)
	case ui.TargetChange:
		s.Focused, s.InChanges = true, true
		s.Changes.Sel = idx
		if it, ok := s.Changes.Selected(); ok {
			a.openDiff(it.Path)
		}
	}
}
