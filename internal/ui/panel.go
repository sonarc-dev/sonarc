package ui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"

	"sonarc/internal/index/provider"
	"sonarc/internal/search"
)

// Panel is the results list shown along the bottom: the output of find
// references, a cscope query, or a project search.
//
// It is a persistent panel rather than a modal list because walking a call
// graph means going back to the same results repeatedly. A modal popup that
// vanishes on the first jump would force the query to be re-run each time.
type Panel struct {
	Title   string
	Results []provider.Location
	Sel     int
	Open    bool

	// Height is how many rows the panel occupies, including its header.
	Height int
}

// Show replaces the panel contents and opens it.
func (p *Panel) Show(title string, results []provider.Location) {
	p.Title = title
	p.Results = results
	p.Sel = 0
	p.Open = len(results) > 0
	if p.Height == 0 {
		p.Height = 10
	}
}

// Close hides the panel.
func (p *Panel) Close() { p.Open = false }

// Current returns the selected result.
func (p *Panel) Current() (provider.Location, bool) {
	if !p.Open || p.Sel < 0 || p.Sel >= len(p.Results) {
		return provider.Location{}, false
	}
	return p.Results[p.Sel], true
}

// Move changes the selection by n, clamped to the list.
func (p *Panel) Move(n int) {
	p.Sel += n
	if p.Sel < 0 {
		p.Sel = 0
	}
	if p.Sel >= len(p.Results) {
		p.Sel = len(p.Results) - 1
	}
}

// rows returns the panel height in screen rows, zero when closed.
func (p *Panel) rows() int {
	if !p.Open {
		return 0
	}
	h := p.Height
	if n := len(p.Results) + 1; n < h {
		h = n
	}
	if h < 2 {
		h = 2
	}
	return h
}

// firstRow is the index of the topmost visible result: the list scrolls just
// far enough to keep the selection on screen. Drawing and mouse hit-testing
// both need it, so they cannot disagree about which row is under the pointer.
func (p *Panel) firstRow(listRows int) int {
	if p.Sel >= listRows {
		return p.Sel - listRows + 1
	}
	return 0
}

// drawPanel renders the results list into the rows above the status bar,
// starting at the left edge x0.
func (u *UI) drawPanel(x0, w, top, height int) {
	if height < 2 {
		return
	}
	th := u.Screen.Theme
	p := &u.Panel

	// Header.
	u.fill(x0, top, w, th.Status)
	head := fmt.Sprintf(" %s — %d result", p.Title, len(p.Results))
	if len(p.Results) != 1 {
		head += "s"
	}
	u.drawText(x0, top, head, th.StatusMod, w)
	hint := "Enter open   n/p next/prev   Esc close "
	if x := w - len(hint); x > x0+len(head) {
		u.drawText(x, top, hint, th.StatusDim, w)
	}

	listRows := height - 1
	first := p.firstRow(listRows)

	for i := 0; i < listRows; i++ {
		idx := first + i
		y := top + 1 + i
		if idx >= len(p.Results) {
			u.fill(x0, y, w, th.Text)
			continue
		}
		r := p.Results[idx]
		style := th.Text
		if idx == p.Sel {
			style = th.Selection
			u.fill(x0, y, w, style)
		}

		// "path:line" then the source text, with the path shortened so the
		// code stays readable on an 80-column terminal.
		loc := fmt.Sprintf("%s:%d", shorten(r.Path, u.PanelRoot), r.Line)
		x := u.drawText(x0+1, y, loc, style, w)
		x = u.drawText(x, y, "  ", style, w)
		u.drawText(x, y, strings.TrimSpace(r.Text), style, w)
	}
}

// bgOf is a style's background, so a gutter drawn inside a highlighted row
// keeps the highlight.
func bgOf(s tcell.Style) tcell.Color {
	_, bg, _ := s.Decompose()
	return bg
}

// shorten makes a path readable in a narrow list by expressing it relative to
// the project root where possible.
func shorten(path, root string) string {
	if root != "" {
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	// Otherwise keep the last two components, which is usually enough to tell
	// two files apart.
	dir, file := filepath.Split(path)
	dir = strings.TrimSuffix(dir, string(filepath.Separator))
	if dir == "" {
		return file
	}
	return filepath.Join(filepath.Base(dir), file)
}

// Picker is a modal chooser, used when a query returns several candidates and
// the user has to disambiguate.
//
// Goto-definition deliberately goes through this rather than jumping to the
// first hit: in C, a name commonly has a prototype, a definition and several
// static variants, and silently guessing wrong is worse than asking.
type Picker struct {
	Title   string
	Items   []PickerItem
	Sel     int
	Open    bool
	Filter  string
	visible []int // indices into Items matching Filter
}

// maxRankedMatches bounds how many filtered results are retained. The picker
// shows at most a screenful, so keeping more only costs sorting time.
const maxRankedMatches = 500

// PickerItem is one choice.
type PickerItem struct {
	Label  string // primary text, e.g. the symbol name
	Detail string // secondary text, e.g. kind and file
	Loc    provider.Location
}

// ShowPicker opens the picker over items.
func (u *UI) ShowPicker(title string, items []PickerItem) {
	u.Picker = Picker{Title: title, Items: items, Open: len(items) > 0}
	u.Picker.refilter()
}

// refilter recomputes which items match the current filter, best first.
//
// Matching is fuzzy rather than substring: typing "ifr" should find
// "index/find_references.go". Results are ranked so consecutive matches and
// matches at path or word boundaries float to the top.
func (p *Picker) refilter() {
	p.visible = p.visible[:0]
	if p.Filter == "" {
		for i := range p.Items {
			p.visible = append(p.visible, i)
		}
	} else {
		type hit struct{ idx, score int }
		var hits []hit
		for i, it := range p.Items {
			best, ok := search.FuzzyScore(it.Label, p.Filter)
			if s, ok2 := search.FuzzyScore(it.Detail, p.Filter); ok2 && (!ok || s > best) {
				best, ok = s, true
			}
			if ok {
				hits = append(hits, hit{i, best})
			}
		}
		sort.SliceStable(hits, func(a, b int) bool { return hits[a].score > hits[b].score })
		// Rank across every candidate, then keep only the best few. Truncating
		// the candidates instead would hide whole regions of a large tree: on a
		// kernel checkout the first 5000 paths never reach fs/, so no amount of
		// typing could reach fs/namei.c.
		if len(hits) > maxRankedMatches {
			hits = hits[:maxRankedMatches]
		}
		for _, h := range hits {
			p.visible = append(p.visible, h.idx)
		}
	}
	if p.Sel >= len(p.visible) {
		p.Sel = len(p.visible) - 1
	}
	if p.Sel < 0 {
		p.Sel = 0
	}
}

// SetFilter narrows the visible items.
func (p *Picker) SetFilter(s string) {
	p.Filter = s
	p.refilter()
}

// Move changes the selection by n.
func (p *Picker) Move(n int) {
	p.Sel += n
	if p.Sel < 0 {
		p.Sel = 0
	}
	if p.Sel >= len(p.visible) {
		p.Sel = len(p.visible) - 1
	}
}

// Current returns the selected item.
func (p *Picker) Current() (PickerItem, bool) {
	if !p.Open || p.Sel < 0 || p.Sel >= len(p.visible) {
		return PickerItem{}, false
	}
	return p.Items[p.visible[p.Sel]], true
}

// Count returns how many items match the filter.
func (p *Picker) Count() int { return len(p.visible) }

// DrawPicker renders the modal chooser centered on screen.
func (u *UI) DrawPicker() {
	p := &u.Picker
	if !p.Open {
		return
	}
	w, h := u.Screen.Size()
	th := u.Screen.Theme

	boxW := w * 3 / 4
	if boxW > 100 {
		boxW = 100
	}
	if boxW < 20 {
		boxW = w - 2
	}
	rows := len(p.visible)
	if rows > 15 {
		rows = 15
	}
	if rows < 1 {
		rows = 1
	}
	boxH := rows + 4 // border, title, filter, border
	if boxH > h-2 {
		boxH = h - 2
	}
	if boxW < 4 || boxH < 5 {
		return
	}
	x0, y0 := (w-boxW)/2, (h-boxH)/3

	for y := y0; y < y0+boxH; y++ {
		u.fill(x0, y, x0+boxW, th.Status)
	}
	u.drawBorder(x0, y0, boxW, boxH, th.Status)
	u.drawText(x0+2, y0, " "+p.Title+" ", th.StatusMod, x0+boxW-1)

	// Filter line.
	fy := y0 + 1
	u.drawText(x0+2, fy, "> "+p.Filter, th.StatusMod, x0+boxW-1)

	listRows := boxH - 4
	first := 0
	if p.Sel >= listRows {
		first = p.Sel - listRows + 1
	}
	for i := 0; i < listRows; i++ {
		idx := first + i
		y := fy + 1 + i
		if idx >= len(p.visible) {
			break
		}
		it := p.Items[p.visible[idx]]
		style := th.Status
		if idx == p.Sel {
			style = th.Selection
			u.fill(x0+1, y, x0+boxW-1, style)
		}
		x := u.drawText(x0+2, y, it.Label, style, x0+boxW-1)
		if it.Detail != "" {
			u.drawText(x+2, y, it.Detail, style, x0+boxW-1)
		}
	}
	u.Screen.ShowCursor(x0+4+len([]rune(p.Filter)), fy)
}
