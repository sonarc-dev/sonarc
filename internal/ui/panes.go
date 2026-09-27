package ui

import (
	"path/filepath"

	"github.com/sonarc-dev/sonarc/internal/view"
)

// SplitMode is how the text area is divided.
type SplitMode int

const (
	// SplitNone shows one file.
	SplitNone SplitMode = iota
	// SplitRight shows two panes side by side.
	SplitRight
	// SplitBelow shows two panes one above the other.
	SplitBelow
)

// Below these sizes a split would leave panes too small to read, so the
// focused pane takes the whole area until the terminal grows again.
const (
	minPaneCols = 30
	minPaneRows = 3
)

// pane is where one view is drawn, from the last Layout.
type pane struct {
	v       *view.View
	x0, x1  int // text area columns [x0, x1), gutter included
	y0      int // first row of text
	rows    int
	gutter  int
	header  int // row of the pane's title line, -1 for none
	focused bool
}

// Layout recomputes where everything goes and tells each view how much room
// it has. It must run before Draw and after any resize.
func (u *UI) Layout() {
	w, h := u.Screen.Size()
	u.textX = u.sidebarWidth()
	// Two rows are reserved at the bottom for the status bar and the message
	// line, plus however many the results panel is using.
	u.textH = max(h-2-u.Panel.rows(), 1)
	u.sepX = -1

	u.panes = u.panes[:0]
	split := u.Split
	if u.Other == nil {
		split = SplitNone
	}
	if split == SplitRight && (w-u.textX-1)/2 < minPaneCols {
		split = SplitBelow // too narrow to sit side by side
	}
	if split == SplitBelow && u.textH/2 < minPaneRows+1 {
		split = SplitNone // too short to stack: show the focused pane only
	}

	switch split {
	case SplitNone:
		u.panes = append(u.panes, pane{v: u.View, x0: u.textX, x1: w, rows: u.textH, header: -1, focused: true})
	default:
		first, second := u.View, u.Other
		if u.SecondFocused {
			first, second = u.Other, u.View
		}
		a := pane{v: first, x0: u.textX, x1: w, y0: 1, header: 0, focused: !u.SecondFocused}
		b := pane{v: second, x0: u.textX, x1: w, focused: u.SecondFocused}
		if split == SplitRight {
			lw := (w - u.textX - 1) / 2
			a.x1 = u.textX + lw
			u.sepX = a.x1
			b.x0 = a.x1 + 1
			b.y0, b.header = 1, 0
			a.rows, b.rows = u.textH-1, u.textH-1
		} else {
			top := u.textH / 2
			a.rows = top - 1
			b.header, b.y0 = top, top+1
			b.rows = u.textH - top - 1
		}
		u.panes = append(u.panes, a, b)
	}

	for i := range u.panes {
		p := &u.panes[i]
		p.rows = max(p.rows, 1)
		// Gutter is "%*d " — the widest line number plus a trailing space.
		p.gutter = max(digits(p.v.Buf.NumLines())+1, 3)
		p.v.Height = p.rows
		p.v.Width = max(p.x1-p.x0-p.gutter, 1)
		if p.focused {
			// The focused view made any edits since the last frame itself.
			p.v.MarkSeen()
			p.v.ScrollToCursor()
			u.gutterW = p.gutter
		} else {
			// The other view follows edits made through the focused one. It
			// keeps its own scroll: only its owner moves it.
			p.v.Sync()
		}
	}
}

// focusedPane is the pane with the keyboard.
func (u *UI) focusedPane() pane {
	for _, p := range u.panes {
		if p.focused {
			return p
		}
	}
	return pane{v: u.View, x0: u.textX, rows: u.textH, header: -1, focused: true}
}

// Panes reports how many panes are on screen.
func (u *UI) Panes() int { return len(u.panes) }

// TextTop is the first row of text in the focused pane.
func (u *UI) TextTop() int { return u.focusedPane().y0 }

// PaneAt finds the pane under (x, y) and where in it the point is: the row
// from the pane's first text row and the column from the start of its text.
// header reports a press on the pane's title line.
func (u *UI) PaneAt(x, y int) (v *view.View, row, col int, header, ok bool) {
	for _, p := range u.panes {
		if x < p.x0 || x >= p.x1 {
			continue
		}
		if y == p.header {
			return p.v, 0, 0, true, true
		}
		if y >= p.y0 && y < p.y0+p.rows {
			return p.v, y - p.y0, x - p.x0 - p.gutter, false, true
		}
	}
	return nil, 0, 0, false, false
}

// drawPaneHeader is a pane's title line: the file it shows, marked when it
// has unsaved changes, bright for the pane with the keyboard.
func (u *UI) drawPaneHeader(p pane) {
	th := u.Screen.Theme
	style, name := th.Status, th.StatusDim
	if p.focused {
		name = th.StatusMod
	}
	u.fill(p.x0, p.header, p.x1, style)
	title := "[No Name]"
	dir := ""
	if path := p.v.Buf.Path(); path != "" {
		title = filepath.Base(path)
		dir = shorten(filepath.Dir(path), u.PanelRoot)
	}
	x := u.drawText(p.x0+1, p.header, title, name, p.x1)
	if p.v.Buf.Modified() {
		x = u.drawText(x, p.header, " ●", name, p.x1)
	}
	if dir != "" && dir != "." {
		u.drawText(x+2, p.header, dir+"/", th.StatusDim, p.x1)
	}
}

// drawSeparator is the line between side-by-side panes.
func (u *UI) drawSeparator() {
	if u.sepX < 0 {
		return
	}
	for y := 0; y < u.textH; y++ {
		u.Screen.SetContent(u.sepX, y, '│', nil, u.Screen.Theme.Gutter)
	}
}
