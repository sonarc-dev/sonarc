// Package ui draws the editor onto the screen.
//
// Rendering is viewport-only: work is proportional to the size of the terminal,
// never to the size of the file, so a 100k-line file scrolls exactly as fast as
// a 10-line one. tcell diffs the resulting cell grid and emits only what
// changed, which is what keeps redraws cheap over ssh.
package ui

import (
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/gdamore/tcell/v2"

	"sonarc/internal/buffer"
	"sonarc/internal/search"
	"sonarc/internal/syntax"
	"sonarc/internal/term"
	"sonarc/internal/text"
	"sonarc/internal/vcs"
	"sonarc/internal/view"
)

// UI holds the screen and what is currently shown on it.
type UI struct {
	Screen *term.Screen
	View   *view.View

	// Msg is a transient line shown at the bottom, cleared on the next
	// keystroke. IsErr styles it as a problem rather than information.
	Msg   string
	IsErr bool

	// Busy is a progress line for work running in the background. It shows on
	// the message line whenever there is no transient message, and unlike Msg it
	// survives keystrokes, because typing while a search runs is the point.
	Busy string

	// Panel is the results list along the bottom; Picker is the modal
	// chooser drawn over everything; Sidebar is the project tree along the
	// left edge.
	Panel   Panel
	Picker  Picker
	Sidebar Sidebar

	// Diff, while open, is drawn in place of the text; see DiffView.
	Diff DiffView

	// PanelRoot shortens result paths for display, usually the project root.
	PanelRoot string

	// Matches are search hits to highlight in the text area.
	Matches []search.Match

	// Changes, when set, returns how the file on screen differs from its
	// last commit, for the marks in the gutter. It is a function because the
	// answer depends on the text being drawn, and is only worth computing for
	// the frame that shows it.
	Changes func() []vcs.Hunk

	gutterW int

	// textX is the left edge of the text area, gutter and all. It is zero
	// unless something (a sidebar) occupies the columns to its left.
	textX int
}

// New returns a UI drawing v onto s.
func New(s *term.Screen, v *view.View) *UI {
	return &UI{Screen: s, View: v, Sidebar: Sidebar{Visible: true, Width: defaultSidebarWidth}}
}

// GutterWidth returns the width of the line-number column, which callers need
// to translate a mouse column into a text column.
func (u *UI) GutterWidth() int { return u.gutterW }

// TextX returns the left edge of the text area (before the gutter), which
// callers need to translate a mouse column into a text column when something
// occupies the columns to its left.
func (u *UI) TextX() int { return u.textX }

// DrawPrompt replaces the message line with a question and the answer typed so
// far, and puts the cursor at the end of the input.
func (u *UI) DrawPrompt(question, input string) {
	w, h := u.Screen.Size()
	y := h - 1
	th := u.Screen.Theme
	u.fill(0, y, w, th.Message)
	x := u.drawText(0, y, question, th.StatusMod, w)
	x = u.drawText(x, y, input, th.Message, w)
	if x < w {
		u.Screen.ShowCursor(x, y)
	}
}

// DrawOverlay draws a bordered box centered on the screen containing lines,
// with title in its top border. Used for the help screen and pickers.
func (u *UI) DrawOverlay(title string, lines []string) {
	w, h := u.Screen.Size()
	th := u.Screen.Theme

	boxW := len(title) + 4
	for _, l := range lines {
		if n := len([]rune(l)) + 4; n > boxW {
			boxW = n
		}
	}
	if boxW > w-2 {
		boxW = w - 2
	}
	boxH := len(lines) + 2
	if boxH > h-2 {
		boxH = h - 2
	}
	if boxW < 4 || boxH < 3 {
		return // no room to draw anything legible
	}
	x0, y0 := (w-boxW)/2, (h-boxH)/2

	style := th.Status
	for y := y0; y < y0+boxH; y++ {
		u.fill(x0, y, x0+boxW, style)
	}
	u.drawBorder(x0, y0, boxW, boxH, style)

	u.drawText(x0+2, y0, " "+title+" ", th.StatusMod, x0+boxW-1)
	for i, l := range lines {
		y := y0 + 1 + i
		if y >= y0+boxH-1 {
			break
		}
		u.drawText(x0+2, y, l, style, x0+boxW-1)
	}
	u.Screen.HideCursor()
}

// Notify shows an informational message on the message line.
func (u *UI) Notify(format string, args ...any) {
	u.Msg, u.IsErr = fmt.Sprintf(format, args...), false
}

// Error shows an error on the message line.
func (u *UI) Error(format string, args ...any) {
	u.Msg, u.IsErr = fmt.Sprintf(format, args...), true
}

// digits returns how many characters n needs when written in base 10.
func digits(n int) int {
	if n < 10 {
		return 1
	}
	d := 0
	for n > 0 {
		d++
		n /= 10
	}
	return d
}

// Layout recomputes the gutter width and tells the view how much room it has.
// It must run before Draw and after any resize.
func (u *UI) Layout() {
	w, h := u.Screen.Size()
	u.textX = u.sidebarWidth()
	// Gutter is "%*d " — the widest line number plus a trailing space.
	u.gutterW = digits(u.View.Buf.NumLines()) + 1
	if u.gutterW < 3 {
		u.gutterW = 3
	}

	// Two rows are reserved at the bottom for the status bar and the message
	// line, plus however many the results panel is using.
	u.View.Height = h - 2 - u.Panel.rows()
	if u.View.Height < 1 {
		u.View.Height = 1
	}
	u.View.Width = w - u.textX - u.gutterW
	if u.View.Width < 1 {
		u.View.Width = 1
	}
	u.View.ScrollToCursor()
}

// drawText writes s at (x, y), stopping at maxX. It returns the column after
// the last cell written.
func (u *UI) drawText(x, y int, s string, style tcell.Style, maxX int) int {
	for _, r := range s {
		if x >= maxX {
			break
		}
		u.Screen.SetContent(x, y, r, nil, style)
		x++
	}
	return x
}

// fill paints a run of blanks, used to extend a styled bar to the screen edge.
func (u *UI) fill(x, y, toX int, style tcell.Style) {
	for ; x < toX; x++ {
		u.Screen.SetContent(x, y, ' ', nil, style)
	}
}

// Draw renders the whole editor. Call Show afterwards to flush.
func (u *UI) Draw() {
	u.Layout()
	w, h := u.Screen.Size()
	th := u.Screen.Theme
	v := u.View

	u.Screen.Clear()

	selFrom, selTo := v.Selection()
	hasSel := v.HasSelection()

	var scratch []rune
	var comb []rune
	var toks []syntax.Token

	var marks []mark
	if u.Changes != nil && !u.Diff.Open {
		marks = changeMarks(u.Changes(), v.Top, v.Height)
	}

	if u.Diff.Open {
		u.drawDiff(u.textX, w, v.Height, v.TabWidth)
	}
	for row := 0; row < v.Height && !u.Diff.Open; row++ {
		y := row
		lineNo := v.Top + row
		if lineNo >= v.Buf.NumLines() {
			// Past the end of the file. Leave it blank rather than drawing the
			// tilde column vi uses; blank reads as "nothing here" to everyone.
			continue
		}
		onCursorLine := lineNo == v.Head.Line

		// Gutter.
		numStyle := th.Gutter
		if onCursorLine {
			numStyle = th.GutterCurrent
		}
		num := strconv.Itoa(lineNo + 1)
		u.drawText(u.textX+u.gutterW-1-len(num), y, num, numStyle, w)
		if marks != nil {
			if r, st := u.markCell(marks[row]); r != 0 {
				u.Screen.SetContent(u.textX+u.gutterW-1, y, r, nil, st)
			}
		}

		ln := v.Buf.Line(lineNo)
		lineStyle := th.Text
		if onCursorLine {
			lineStyle = th.CursorLine
			// Extend the cursor-line highlight across the full width so it
			// reads as a band rather than stopping at the end of the text.
			u.fill(u.textX+u.gutterW, y, w, lineStyle)
		}

		// Syntax colors for this line. The highlighter caches the state each
		// line inherits, so this costs a single scan of the visible line.
		toks = v.Syntax.Tokens(v.Buf, lineNo, toks)

		text.Iterate(ln, v.TabWidth, func(c text.Cell) bool {
			if c.Col+c.Width <= v.Left {
				return true // entirely scrolled off to the left
			}
			x := u.textX + u.gutterW + c.Col - v.Left
			if x >= w {
				return false // past the right edge; nothing further is visible
			}

			style := lineStyle
			if cl := syntax.ClassAt(toks, c.ByteOff); cl != syntax.ClassNone {
				style = classStyle(th, cl, lineStyle)
			}
			pos := buffer.Pos{Line: lineNo, Col: c.ByteOff}
			// Search hits are painted first so that an active selection, which
			// is where the cursor actually is, still stands out among them.
			if inMatch(u.Matches, pos) {
				style = th.Match
			}
			if hasSel && !pos.Less(selFrom) && pos.Less(selTo) {
				style = th.Selection
			}

			if c.Kind == text.KindNormal {
				main, cm := c.ClusterRunes(ln, comb)
				comb = cm
				if x >= u.textX+u.gutterW {
					u.Screen.SetContent(x, y, main, cm, style)
				}
			} else {
				// Tabs, control characters and invalid bytes expand to exactly
				// Width runes, so they tile their cells one rune each.
				scratch = c.Append(scratch[:0])
				for i, r := range scratch {
					cx := x + i
					if cx < u.textX+u.gutterW {
						continue // partially scrolled off
					}
					if cx >= w {
						break
					}
					u.Screen.SetContent(cx, y, r, nil, style)
				}
			}
			return true
		})

		// A selection spanning a line break should show the newline as a
		// highlighted cell, or multi-line selections look ragged.
		if hasSel && lineNo >= selFrom.Line && lineNo < selTo.Line {
			x := u.textX + u.gutterW + text.Width(ln, v.TabWidth) - v.Left
			if x >= u.textX+u.gutterW && x < w {
				u.Screen.SetContent(x, y, ' ', nil, th.Selection)
			}
		}
	}

	if rows := u.Panel.rows(); rows > 0 {
		u.drawPanel(u.textX, w, v.Height, rows)
	}
	if u.textX > 0 {
		u.drawSidebar(u.textX, h-2)
	}
	u.drawStatus(w, h-2)
	u.drawMessage(w, h-1)

	if u.Picker.Open {
		u.DrawPicker()
		return
	}

	// While the sidebar has keyboard focus, arrow keys move the tree
	// selection rather than the text cursor; showing the caret in the text
	// area would be misleading about where input is actually going.
	if u.Sidebar.Focused || u.Diff.Open {
		u.Screen.HideCursor()
		return
	}

	// Put the real terminal cursor where the caret is, so the terminal's own
	// cursor shape and blink apply and screen readers can follow it.
	cx := u.textX + u.gutterW + v.CursorCol() - v.Left
	cy := v.Head.Line - v.Top
	if cx >= u.textX+u.gutterW && cx < w && cy >= 0 && cy < v.Height {
		u.Screen.ShowCursor(cx, cy)
	} else {
		u.Screen.HideCursor()
	}
}

// mark is what the gutter shows for one line about its change since the last
// commit.
type mark uint8

const (
	markNone         mark = iota
	markAdded             // a new line
	markModified          // a changed line
	markDeletedBelow      // lines were removed after this one
	markDeletedAbove      // lines were removed before the first line
)

// changeMarks works out the mark for each of the height lines from top.
func changeMarks(hunks []vcs.Hunk, top, height int) []mark {
	out := make([]mark, height)
	set := func(line int, m mark) {
		if i := line - top; i >= 0 && i < height {
			// A line that is itself changed says more than a deletion next to it.
			if out[i] == markNone || m == markAdded || m == markModified {
				out[i] = m
			}
		}
	}
	for _, h := range hunks {
		if h.NewStart >= top+height {
			break
		}
		switch {
		case h.NewLines == 0 && h.NewStart == 0:
			set(0, markDeletedAbove)
		case h.NewLines == 0:
			set(h.NewStart-1, markDeletedBelow)
		default:
			m := markModified
			if h.OldLines == 0 {
				m = markAdded
			}
			for l := max(h.NewStart, top); l < h.NewStart+h.NewLines && l < top+height; l++ {
				set(l, m)
			}
		}
	}
	return out
}

// markCell is the character and style for a mark: a bar beside an added or
// changed line, a low or high rule where lines were removed.
func (u *UI) markCell(m mark) (rune, tcell.Style) {
	th := u.Screen.Theme
	switch m {
	case markAdded:
		return '▎', th.GitAdded
	case markModified:
		return '▎', th.GitModified
	case markDeletedBelow:
		return '▁', th.GitDeleted
	case markDeletedAbove:
		return '▔', th.GitDeleted
	}
	return 0, th.Text
}

// drawStatus renders the status bar: what file this is, whether it is dirty,
// and where the cursor sits.
func (u *UI) drawStatus(w, y int) {
	if y < 0 {
		return
	}
	if u.Diff.Open {
		u.drawDiffStatus(w, y)
		return
	}
	th := u.Screen.Theme
	v := u.View
	u.fill(0, y, w, th.Status)

	name := v.Buf.Path()
	if name == "" {
		name = "[No Name]"
	} else {
		name = filepath.Base(name)
	}

	x := u.drawText(1, y, name, th.Status, w)
	if v.Buf.Modified() {
		x = u.drawText(x, y, " ●", th.StatusMod, w) // filled dot: unsaved
	}
	if v.Buf.Large() {
		x = u.drawText(x, y, "  [large file]", th.StatusDim, w)
	}
	_ = x

	// Right-aligned position and file properties.
	eol := "LF"
	if v.Buf.CRLF() {
		eol = "CRLF"
	}
	indent := fmt.Sprintf("Tab:%d", v.TabWidth)
	if v.ExpandTabs {
		indent = fmt.Sprintf("Spaces:%d", v.TabWidth)
	}
	lang := "text"
	if l := v.Syntax.Language(); l != nil {
		lang = l.Name
	}
	right := fmt.Sprintf("Ln %d, Col %d   %s   %s   %s   %d lines ",
		v.Head.Line+1, v.CursorCol()+1, lang, indent, eol, v.Buf.NumLines())
	if rx := w - len(right); rx > 0 {
		u.drawText(rx, y, right, th.Status, w)
	}
}

// drawMessage renders the bottom line: a transient message, or a short hint at
// the keys that matter most when there is nothing to say.
func (u *UI) drawMessage(w, y int) {
	if y < 0 {
		return
	}
	th := u.Screen.Theme
	if u.Msg != "" {
		style := th.Message
		if u.IsErr {
			style = th.Error
		}
		u.drawText(0, y, u.Msg, style, w)
		return
	}
	if u.Busy != "" {
		u.drawText(0, y, u.Busy, th.StatusMod, w)
		return
	}
	// Every key named here is asserted to exist by TestAdvertisedBindingsExist.
	// Advertising a shortcut that does nothing is how a terminal editor earns
	// a reputation for being broken over ssh.
	u.drawText(0, y,
		"^S save  ^Q quit  ^F find  ^O open  ^E tree  ^] def  ^K r refs  F1 help",
		th.StatusDim, w)
}

// drawBorder draws a single-line box border.
func (u *UI) drawBorder(x0, y0, w, h int, style tcell.Style) {
	for x := x0 + 1; x < x0+w-1; x++ {
		u.Screen.SetContent(x, y0, tcell.RuneHLine, nil, style)
		u.Screen.SetContent(x, y0+h-1, tcell.RuneHLine, nil, style)
	}
	for y := y0 + 1; y < y0+h-1; y++ {
		u.Screen.SetContent(x0, y, tcell.RuneVLine, nil, style)
		u.Screen.SetContent(x0+w-1, y, tcell.RuneVLine, nil, style)
	}
	u.Screen.SetContent(x0, y0, tcell.RuneULCorner, nil, style)
	u.Screen.SetContent(x0+w-1, y0, tcell.RuneURCorner, nil, style)
	u.Screen.SetContent(x0, y0+h-1, tcell.RuneLLCorner, nil, style)
	u.Screen.SetContent(x0+w-1, y0+h-1, tcell.RuneLRCorner, nil, style)
}

// inMatch reports whether pos falls inside any highlighted search match.
//
// Matches arrive in document order, so the scan can stop as soon as it passes
// the line in question. Only visible lines are ever asked about, which keeps
// this proportional to the viewport rather than to the match count.
func inMatch(matches []search.Match, pos buffer.Pos) bool {
	for _, m := range matches {
		if m.From.Line > pos.Line {
			return false
		}
		if m.From.Line != pos.Line {
			continue
		}
		if pos.Col >= m.From.Col && pos.Col < m.To.Col {
			return true
		}
	}
	return false
}

// classStyle maps a syntax class to a theme style, keeping the background of
// the line it sits on so the cursor-line band shows through the coloring.
func classStyle(th term.Theme, c syntax.Class, base tcell.Style) tcell.Style {
	var fg tcell.Style
	switch c {
	case syntax.ClassKeyword:
		fg = th.Keyword
	case syntax.ClassType:
		fg = th.Type
	case syntax.ClassString:
		fg = th.String
	case syntax.ClassComment:
		fg = th.Comment
	case syntax.ClassNumber:
		fg = th.Number
	case syntax.ClassFunc:
		fg = th.Func
	case syntax.ClassPreproc:
		fg = th.Preproc
	default:
		return base
	}
	f, _, attr := fg.Decompose()
	_, bg, _ := base.Decompose()
	return tcell.StyleDefault.Foreground(f).Background(bg).Attributes(attr)
}
