package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"sonarc/internal/vcs"
)

// ChangeItem is one changed file in the sidebar's Changes section.
type ChangeItem struct {
	Path   string // absolute
	Rel    string // as shown, relative to the project
	Status vcs.Status
}

// ChangesList is the Changes section under the file tree: every file that
// differs from the last commit, for reviewing them one by one. It exists only
// in a git work tree.
type ChangesList struct {
	Enabled   bool
	Collapsed bool
	Items     []ChangeItem
	Sel       int
	Top       int
	// Active is the file whose diff is showing, marked in the list.
	Active string
}

// SetItems replaces the list, keeping the selection on the same file when it
// is still there.
func (c *ChangesList) SetItems(items []ChangeItem) {
	keep := ""
	if c.Sel >= 0 && c.Sel < len(c.Items) {
		keep = c.Items[c.Sel].Path
	}
	c.Items = items
	c.Sel = 0
	for i, it := range items {
		if it.Path == keep {
			c.Sel = i
			break
		}
	}
}

// Selected returns the selected file.
func (c *ChangesList) Selected() (ChangeItem, bool) {
	if c.Sel < 0 || c.Sel >= len(c.Items) {
		return ChangeItem{}, false
	}
	return c.Items[c.Sel], true
}

// Move changes the selection by n, keeping it on screen in rows rows.
func (c *ChangesList) Move(n, rows int) {
	if len(c.Items) == 0 {
		return
	}
	c.Sel = min(max(c.Sel+n, 0), len(c.Items)-1)
	c.scrollTo(rows)
}

// ScrollBy scrolls the list without moving the selection.
func (c *ChangesList) ScrollBy(n, rows int) {
	c.Top = min(max(c.Top+n, 0), max(len(c.Items)-rows, 0))
}

func (c *ChangesList) scrollTo(rows int) {
	if rows <= 0 {
		return
	}
	if c.Sel < c.Top {
		c.Top = c.Sel
	}
	if c.Sel >= c.Top+rows {
		c.Top = c.Sel - rows + 1
	}
}

// sidebarLayout splits the sidebar's height between the tree and the Changes
// section. Drawing and hit-testing both use it, so they cannot disagree about
// which row is where.
//
// Row 0 is the tree's header; treeRows rows of tree follow. Then, when there
// is a Changes section, its header at changesY and changesRows rows of files.
// Expanded, the section takes what its files need up to a third of the
// sidebar, and the tree always keeps at least a few rows.
type sidebarGeom struct {
	treeRows    int
	changesY    int // -1 when there is no Changes section
	changesRows int
}

func (u *UI) sidebarLayout() sidebarGeom {
	_, h := u.Screen.Size()
	height := h - 2 // the status bar and message line are full width
	g := sidebarGeom{treeRows: max(height-1, 0), changesY: -1}
	c := &u.Sidebar.Changes
	if !c.Enabled || height < 4 {
		return g
	}
	list := 0
	if !c.Collapsed {
		want := max(len(c.Items), 1) // one row says "no changes"
		list = min(want, max(height/3, 3))
	}
	// The tree keeps at least 3 rows; the section shrinks first.
	list = max(min(list, height-1-3-1), 0)
	g.changesRows = list
	g.treeRows = height - 1 - 1 - list
	g.changesY = 1 + g.treeRows
	return g
}

// drawChanges renders the Changes section: a header that shows whether it is
// open and how many files changed, then one row per file with its status.
func (u *UI) drawChanges(g sidebarGeom, innerW int) {
	th := u.Screen.Theme
	s := &u.Sidebar
	c := &s.Changes

	y := g.changesY
	u.fill(0, y, innerW, th.Status)
	arrow := "▾"
	if c.Collapsed {
		arrow = "▸"
	}
	u.drawText(0, y, fmt.Sprintf(" %s CHANGES  %d", arrow, len(c.Items)), th.StatusMod, innerW)

	if len(c.Items) == 0 && g.changesRows > 0 {
		u.fill(0, y+1, innerW, th.Text)
		u.drawText(1, y+1, "  no changes since the last commit", th.StatusDim.Background(bgOf(th.Text)), innerW)
		for i := 1; i < g.changesRows; i++ {
			u.fill(0, y+1+i, innerW, th.Text)
		}
		return
	}
	c.scrollTo(g.changesRows)
	for i := 0; i < g.changesRows; i++ {
		row := y + 1 + i
		idx := c.Top + i
		if idx >= len(c.Items) {
			u.fill(0, row, innerW, th.Text)
			continue
		}
		it := c.Items[idx]
		style := th.Text
		selected := s.Focused && s.InChanges && idx == c.Sel
		switch {
		case selected:
			style = th.Selection
		case it.Path == c.Active:
			style = th.CursorLine
		}
		u.fill(0, row, innerW, style)

		letter := statusStyle(th, it.Status).Background(bgOf(style))
		u.Screen.SetContent(2, row, rune(it.Status), nil, letter)
		name := filepath.Base(it.Rel)
		x := u.drawText(4, row, name, style, innerW)
		if dir := filepath.Dir(it.Rel); dir != "." {
			dim := th.Gutter.Background(bgOf(style))
			u.drawText(x+1, row, strings.TrimSuffix(dir, "/")+"/", dim, innerW)
		}
	}
}

// SidebarTarget is what a sidebar row is.
type SidebarTarget int

const (
	TargetNone SidebarTarget = iota
	TargetTree
	TargetChangesHeader
	TargetChange
)

// SidebarHit says what the sidebar row y is, and for a tree entry or a changed
// file, its index.
func (u *UI) SidebarHit(y int) (SidebarTarget, int) {
	g := u.sidebarLayout()
	switch {
	case y >= 1 && y < 1+g.treeRows:
		if idx, ok := u.SidebarRowAt(y); ok {
			return TargetTree, idx
		}
	case g.changesY >= 0 && y == g.changesY:
		return TargetChangesHeader, 0
	case g.changesY >= 0 && y > g.changesY && y <= g.changesY+g.changesRows:
		idx := u.Sidebar.Changes.Top + y - g.changesY - 1
		if idx < len(u.Sidebar.Changes.Items) {
			return TargetChange, idx
		}
	}
	return TargetNone, 0
}

// ChangesRows is how many changed files are visible.
func (u *UI) ChangesRows() int { return u.sidebarLayout().changesRows }

// InChangesArea reports whether row y belongs to the Changes section, for
// routing the mouse wheel.
func (u *UI) InChangesArea(y int) bool {
	g := u.sidebarLayout()
	return g.changesY >= 0 && y >= g.changesY
}
