package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/sonarc-dev/sonarc/internal/text"
)

// DiffLine is one row of a diff view.
type DiffLine struct {
	// Kind is ' ' for an unchanged line shown for context, '-' for a line
	// only in the last commit, '+' for a line only in the current text, and
	// '~' for the unchanged stretch between two blocks, which is not shown.
	Kind byte
	// Old and New are 1-based line numbers in each version, 0 where the line
	// does not exist. A removed line's New is where it would be now: the line
	// that took its place, so going there lands on the change.
	Old, New int
	Text     string
}

// DiffView shows how one file differs from its last commit, in place of the
// text, until it is closed. It is read-only: Enter opens the file at a line.
type DiffView struct {
	Open    bool
	Path    string
	Title   string
	Lines   []DiffLine
	Sel     int
	Top     int
	Added   int
	Removed int
}

// Close hides the view.
func (d *DiffView) Close() { *d = DiffView{} }

// Current is the selected line.
func (d *DiffView) Current() (DiffLine, bool) {
	if d.Sel < 0 || d.Sel >= len(d.Lines) {
		return DiffLine{}, false
	}
	return d.Lines[d.Sel], true
}

// Move changes the selection by n, keeping it within rows rows on screen.
func (d *DiffView) Move(n, rows int) {
	if len(d.Lines) == 0 {
		return
	}
	d.Sel = min(max(d.Sel+n, 0), len(d.Lines)-1)
	if d.Sel < d.Top {
		d.Top = d.Sel
	}
	if rows > 0 && d.Sel >= d.Top+rows {
		d.Top = d.Sel - rows + 1
	}
}

// Scroll moves the view without moving the selection off screen.
func (d *DiffView) Scroll(n, rows int) {
	d.Top = min(max(d.Top+n, 0), max(len(d.Lines)-rows, 0))
	d.Sel = min(max(d.Sel, d.Top), d.Top+max(rows-1, 0))
	d.Sel = min(d.Sel, max(len(d.Lines)-1, 0))
}

// StepBlock moves to the start of the next (dir > 0) or previous block of
// changes, wrapping around, and reports whether there is one.
func (d *DiffView) StepBlock(dir, rows int) bool {
	var starts []int
	for i, l := range d.Lines {
		if l.Kind != ' ' && l.Kind != '~' && (i == 0 || d.Lines[i-1].Kind == ' ' || d.Lines[i-1].Kind == '~') {
			starts = append(starts, i)
		}
	}
	if len(starts) == 0 {
		return false
	}
	target := -1
	if dir > 0 {
		target = starts[0]
		for _, s := range starts {
			if s > d.Sel {
				target = s
				break
			}
		}
	} else {
		target = starts[len(starts)-1]
		for i := len(starts) - 1; i >= 0; i-- {
			if starts[i] < d.Sel {
				target = starts[i]
				break
			}
		}
	}
	d.Move(target-d.Sel, rows)
	// Leave a little context above the block when there is room.
	if d.Top > 0 && d.Sel-d.Top < 3 {
		d.Top = max(d.Sel-3, 0)
	}
	return true
}

// drawDiff renders the view into the text area: both line numbers, a +/-
// marker, and the line, colored by kind. Tabs expand as they do in the text.
func (u *UI) drawDiff(x0, w, y0, height, tabWidth int) {
	th := u.Screen.Theme
	d := &u.Diff
	if d.Sel >= d.Top+height {
		d.Top = d.Sel - height + 1
	}
	numW := 4
	for _, l := range d.Lines {
		numW = max(numW, len(strconv.Itoa(max(l.Old, l.New))))
	}
	for row := 0; row < height; row++ {
		idx := d.Top + row
		if idx >= len(d.Lines) {
			break
		}
		l := d.Lines[idx]
		base := th.Text
		if idx == d.Sel {
			base = th.CursorLine
			u.fill(x0, y0+row, w, base)
		}
		num := func(n int) string {
			if n == 0 {
				return strings.Repeat(" ", numW)
			}
			return fmt.Sprintf("%*d", numW, n)
		}
		gutter := th.Gutter.Background(bgOf(base))
		if l.Kind == '~' {
			u.drawText(x0, y0+row, strings.Repeat(" ", numW*2+2)+"⋯", gutter, w)
			continue
		}
		newNum := l.New
		if l.Kind == '-' {
			newNum = 0 // its New is only where to go, not a line of its own
		}
		x := u.drawText(x0, y0+row, num(l.Old)+" "+num(newNum)+" ", gutter, w)
		style := base
		switch l.Kind {
		case '-':
			style = th.GitDeleted.Background(bgOf(base))
		case '+':
			style = th.GitAdded.Background(bgOf(base))
		}
		x = u.drawText(x, y0+row, string(l.Kind)+" ", style, w)
		u.drawExpanded(x, y0+row, l.Text, style, w, tabWidth)
	}
}

// drawExpanded draws a line with tabs expanded to the next tab stop.
func (u *UI) drawExpanded(x0, y int, s string, style tcell.Style, maxX, tabWidth int) {
	var scratch []rune
	var comb []rune
	b := []byte(s)
	text.Iterate(b, tabWidth, func(c text.Cell) bool {
		x := x0 + c.Col
		if x >= maxX {
			return false
		}
		if c.Kind == text.KindNormal {
			main, cm := c.ClusterRunes(b, comb)
			comb = cm
			u.Screen.SetContent(x, y, main, cm, style)
			return true
		}
		scratch = c.Append(scratch[:0])
		for i, r := range scratch {
			if x+i < maxX {
				u.Screen.SetContent(x+i, y, r, nil, style)
			}
		}
		return true
	})
}

// drawDiffStatus is the status bar while a diff is showing.
func (u *UI) drawDiffStatus(w, y int) {
	th := u.Screen.Theme
	d := &u.Diff
	u.fill(0, y, w, th.Status)
	x := u.drawText(1, y, d.Title, th.StatusMod, w)
	x = u.drawText(x, y, fmt.Sprintf("   +%d −%d", d.Added, d.Removed), th.Status, w)
	hint := "Enter go to line   n/p next/prev change   Esc close "
	if hx := w - len([]rune(hint)); hx > x+2 {
		u.drawText(hx, y, hint, th.StatusDim, w)
	}
}
