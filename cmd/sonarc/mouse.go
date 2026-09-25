package main

import (
	"sonarc/internal/ui"
	"time"

	"github.com/gdamore/tcell/v2"
)

// multiClickWindow is how long after a press a repeat still counts as part of
// the same multi-click. It is short enough not to merge two deliberate clicks
// and long enough for the latency of an ssh session.
const multiClickWindow = 400 * time.Millisecond

// clickState tracks consecutive presses on one cell.
type clickState struct {
	x, y  int
	at    time.Time
	count int
}

// register records a press at (x, y) and returns how many presses in a row
// this makes: 1 single, 2 double, 3 triple. A fourth starts over, so a fast
// clicker is not stuck selecting lines.
func (c *clickState) register(x, y int, now time.Time) int {
	if c.count > 0 && c.x == x && c.y == y && now.Sub(c.at) <= multiClickWindow {
		c.count++
		if c.count > 3 {
			c.count = 1
		}
	} else {
		c.count = 1
	}
	c.x, c.y, c.at = x, y, now
	return c.count
}

// onMouse routes a mouse event to whatever is under the pointer, in the same
// precedence keys follow: picker, then sidebar, then results panel, then text.
func (a *app) onMouse(ev *tcell.EventMouse) {
	x, y := ev.Position()
	btn := ev.Buttons()

	// A modal picker owns the mouse exactly as it owns the keyboard.
	if a.ui.Picker.Open {
		switch btn {
		case tcell.WheelUp:
			a.ui.Picker.Move(-1)
		case tcell.WheelDown:
			a.ui.Picker.Move(1)
		}
		return
	}

	region := a.ui.RegionAt(x, y)

	switch btn {
	case tcell.WheelUp, tcell.WheelDown:
		delta := 3
		if btn == tcell.WheelUp {
			delta = -3
		}
		switch {
		case (region == ui.RegionSidebar || region == ui.RegionSidebarEdge) && a.ui.InChangesArea(y):
			a.ui.Sidebar.Changes.ScrollBy(delta, a.ui.ChangesRows())
		case (region == ui.RegionSidebar || region == ui.RegionSidebarEdge) && a.ui.Sidebar.Tree != nil:
			a.ui.Sidebar.Tree.ScrollBy(delta, a.ui.SidebarListRows())
		case a.ui.Diff.Open && region != ui.RegionPanel:
			a.ui.Diff.Scroll(delta, a.ui.View.Height)
		default:
			a.v().Scroll(delta)
		}

	case tcell.Button1:
		first := !a.pressed
		if first {
			a.pressed = true
			a.pressRegion = region
		}
		switch a.pressRegion {
		case ui.RegionText, ui.RegionGutter:
			if a.ui.Diff.Open {
				if first {
					a.diffClick(x, y)
				}
				break
			}
			if a.pressRegion == ui.RegionText {
				a.textPointer(x, y, first)
			}
		case ui.RegionSidebar:
			if first {
				a.sidebarClick(y)
			}
		case ui.RegionSidebarEdge:
			// The edge follows the pointer: the separator lands in the
			// column under it, so the sidebar is x+1 columns wide.
			a.ui.ResizeSidebar(x + 1)
			a.resized = true
		case ui.RegionPanel:
			if first {
				a.panelClick(y)
			}
		}

	case tcell.ButtonNone:
		a.pressed = false
		if a.resized {
			a.resized = false
			a.saveState()
		}
	}
}

// textPointer handles a press or drag that began in the text area. Dragging
// past the left edge clamps to the first column rather than being dropped, so
// a selection can be pulled to the start of a line.
func (a *app) textPointer(x, y int, first bool) {
	v := a.v()
	if y >= v.Height {
		return // dragged down into the panel or status bar
	}
	col := a.ui.TextCol(x)
	if col < 0 {
		col = 0
	}

	if !first {
		// Motion within the cell the press landed on changes nothing, and must
		// not collapse a word or line a multi-click just selected.
		if x == a.click.x && y == a.click.y {
			return
		}
		v.Click(y, col, true) // dragging extends the selection
		return
	}

	a.ui.Sidebar.Focused = false // clicking the text puts the keyboard there
	switch a.click.register(x, y, a.now()) {
	case 2:
		v.SelectWord(v.PosAt(y, col))
	case 3:
		v.SelectLine(v.PosAt(y, col).Line)
	default:
		v.Click(y, col, false)
	}
}

// panelClick selects a result and shows it, leaving the panel open: walking a
// call graph means coming back to the same list. Like n/p it previews without
// pushing the tag stack.
func (a *app) panelClick(y int) {
	idx, ok := a.ui.PanelRowAt(y)
	if !ok {
		return // header row
	}
	a.ui.Sidebar.Focused = false
	a.ui.SetPanelSel(idx)
	a.previewResult()
}
