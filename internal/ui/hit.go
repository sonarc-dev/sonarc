package ui

// Region names a part of the screen, for routing mouse events.
type Region int

const (
	// RegionText is the editable text, right of the gutter.
	RegionText Region = iota
	// RegionGutter is the line-number column.
	RegionGutter
	// RegionSidebar is the file tree, header included.
	RegionSidebar
	// RegionPanel is the results list, header included.
	RegionPanel
	// RegionBar is the status bar and message line.
	RegionBar
	// RegionSidebarEdge is the separator column on the sidebar's right; a
	// drag from it resizes the sidebar.
	RegionSidebarEdge
)

// RegionAt reports which part of the screen a cell belongs to. It relies on
// the geometry from the last Layout, which always runs before an event is
// polled, so it describes what the user is actually looking at.
func (u *UI) RegionAt(x, y int) Region {
	_, h := u.Screen.Size()
	switch {
	case y >= h-2:
		return RegionBar
	case u.textX > 0 && x == u.textX-1:
		return RegionSidebarEdge
	case x < u.textX:
		return RegionSidebar
	case u.Panel.rows() > 0 && y >= u.View.Height:
		return RegionPanel
	case x < u.textX+u.gutterW:
		return RegionGutter
	}
	return RegionText
}

// TextCol converts a screen column in the text area to a column relative to
// the start of the text, after the sidebar and gutter.
func (u *UI) TextCol(x int) int { return x - u.textX - u.gutterW }

// SidebarRowAt maps a screen row to an index into the tree's rows. The header
// occupies the first row and is not an entry.
func (u *UI) SidebarRowAt(y int) (int, bool) {
	if u.Sidebar.Tree == nil || y < 1 || y > u.sidebarLayout().treeRows {
		return 0, false
	}
	idx := u.Sidebar.Tree.Top + y - 1
	if idx >= len(u.Sidebar.Tree.Rows()) {
		return 0, false
	}
	return idx, true
}

// SidebarListRows is how many tree entries fit below the sidebar header and
// above the Changes section.
func (u *UI) SidebarListRows() int { return u.sidebarLayout().treeRows }

// PanelRowAt maps a screen row to an index into the results. The header is
// the first row of the panel and is not a result.
func (u *UI) PanelRowAt(y int) (int, bool) {
	rows := u.Panel.rows()
	if rows < 2 {
		return 0, false
	}
	listRows := rows - 1
	i := y - u.View.Height - 1
	if i < 0 || i >= listRows {
		return 0, false
	}
	idx := u.Panel.firstRow(listRows) + i
	if idx >= len(u.Panel.Results) {
		return 0, false
	}
	return idx, true
}

// SetPanelSel selects result i, for a mouse click.
func (u *UI) SetPanelSel(i int) {
	if i >= 0 && i < len(u.Panel.Results) {
		u.Panel.Sel = i
	}
}
