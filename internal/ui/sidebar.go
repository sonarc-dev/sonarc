package ui

import (
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/sonarc-dev/sonarc/internal/filetree"
	"github.com/sonarc-dev/sonarc/internal/term"
	"github.com/sonarc-dev/sonarc/internal/vcs"
)

// sidebarMinCols is the terminal width below which the sidebar auto-hides,
// unless it has been explicitly pinned open. Below this, a sidebar plus a
// usable gutter and text column no longer both fit comfortably.
const sidebarMinCols = 90

// defaultSidebarWidth is how many columns the sidebar occupies, including its
// one-column separator against the text area.
const defaultSidebarWidth = 25

// Bounds for resizing: narrower than SidebarMinWidth shows too little of a
// name to be useful, and the text keeps at least textMinCols columns.
const (
	SidebarMinWidth = 12
	textMinCols     = 20
)

// SidebarStep is how many columns one resize key press moves the edge.
const SidebarStep = 2

// Sidebar is the project file-tree panel drawn along the left edge.
//
// Visible is the user's intent — on by default. Pinned overrides the
// auto-hide-below-90-columns rule: an explicit toggle always wins over the
// terminal being narrow, so a deliberate action never appears to silently
// fail. Focused controls whether arrow keys navigate the tree or the buffer;
// Escape always returns focus to the text without touching Visible.
type Sidebar struct {
	Visible bool
	Pinned  bool
	Focused bool
	Width   int
	Tree    *filetree.Tree

	// Git is each changed file's status, by absolute path, and GitDirs the
	// directories containing any: the tree badges them so a change is
	// visible without expanding every folder.
	Git     map[string]vcs.Status
	GitDirs map[string]bool

	// Changes is the list of changed files below the tree. InChanges says the
	// keyboard is in that list rather than the tree, while Focused.
	Changes   ChangesList
	InChanges bool
}

// SetGit records which files have changed since the last commit.
func (s *Sidebar) SetGit(changes map[string]vcs.Status) {
	s.Git = changes
	s.GitDirs = map[string]bool{}
	for p := range changes {
		for d := filepath.Dir(p); ; d = filepath.Dir(d) {
			if s.GitDirs[d] {
				break // and so are all its parents
			}
			s.GitDirs[d] = true
			if parent := filepath.Dir(d); parent == d {
				break
			}
		}
	}
}

// IsShown reports whether the sidebar actually occupies columns at terminal
// width w — the same rule Layout uses to size the text area, exposed so
// keyboard commands can toggle the state a person actually sees rather than
// the raw Visible flag.
func (s *Sidebar) IsShown(w int) bool {
	if s.Tree == nil || !s.Visible {
		return false
	}
	return w >= sidebarMinCols || s.Pinned
}

// Toggle is the keyboard entry point to the sidebar, so it must work without a
// mouse from every state. It cycles at terminal width w:
//
//	hidden          → shown, pinned, focused
//	shown, unfocused → focused
//	shown, focused   → hidden
//
// Opening it always pins it visible regardless of width, so a deliberate
// action never appears to do nothing on a narrow terminal. The middle step
// exists because the sidebar is visible by default: without it, the first
// press would hide a panel the user was trying to reach.
func (s *Sidebar) Toggle(w int) {
	switch {
	case !s.IsShown(w):
		s.Visible, s.Pinned, s.Focused = true, true, true
	case !s.Focused:
		s.Focused = true
	default:
		s.Visible, s.Pinned, s.Focused = false, false, false
	}
}

// width returns how many columns the sidebar occupies right now, zero when
// hidden.
func (u *UI) sidebarWidth() int {
	w, _ := u.Screen.Size()
	if !u.Sidebar.IsShown(w) {
		return 0
	}
	width := u.Sidebar.Width
	if width <= 0 {
		width = defaultSidebarWidth
	}
	// Never let the sidebar crowd out a usable text column.
	if max := w - textMinCols; width > max {
		width = max
	}
	if width < 0 {
		width = 0
	}
	return width
}

// ResizeSidebar sets the sidebar to width columns, separator included, kept
// within what the terminal can give it. It returns the width in effect, which
// is what the caller should remember.
func (u *UI) ResizeSidebar(width int) int {
	w, _ := u.Screen.Size()
	width = min(width, w-textMinCols)
	width = max(width, SidebarMinWidth)
	u.Sidebar.Width = width
	return width
}

// SidebarWidth is the width the sidebar is set to, whether or not it is shown.
func (u *UI) SidebarWidth() int {
	if u.Sidebar.Width <= 0 {
		return defaultSidebarWidth
	}
	return u.Sidebar.Width
}

// gitBadge is the letter shown after a changed file's name — M, A, D, R, U, or
// ? for untracked — or a dot after a directory holding changes.
func (u *UI) gitBadge(n *filetree.Node, rowStyle tcell.Style, selected bool) (rune, tcell.Style) {
	th := u.Screen.Theme
	var r rune
	var st tcell.Style
	switch {
	case n.IsDir && u.Sidebar.GitDirs[n.Path]:
		r, st = '•', th.GitModified
	case !n.IsDir:
		s, ok := u.Sidebar.Git[n.Path]
		if !ok {
			return 0, rowStyle
		}
		r, st = rune(s), statusStyle(th, s)
	default:
		return 0, rowStyle
	}
	if selected {
		st = st.Background(bgOf(rowStyle))
	}
	return r, st
}

// statusStyle colors a git status letter: green for new, red for gone or
// conflicted, blue for changed.
func statusStyle(th term.Theme, s vcs.Status) tcell.Style {
	switch s {
	case 'A', '?':
		return th.GitAdded
	case 'D', 'U':
		return th.GitDeleted
	}
	return th.GitModified
}

// drawSidebar renders the tree into columns [0, width) and rows [0, height).
func (u *UI) drawSidebar(width, height int) {
	if width <= 0 || height <= 0 || u.Sidebar.Tree == nil {
		return
	}
	th := u.Screen.Theme
	s := &u.Sidebar
	tr := s.Tree

	innerW := width - 1 // last column is the separator against the text area
	if innerW < 1 {
		innerW = 1
	}

	// Header: the project's directory name.
	u.fill(0, 0, width, th.Status)
	u.drawText(0, 0, " "+filepath.Base(tr.Root()), th.StatusMod, innerW)

	g := u.sidebarLayout()
	listRows := g.treeRows
	tr.Fit(listRows)

	rows := tr.Rows()
	for i := 0; i < listRows; i++ {
		idx := tr.Top + i
		y := 1 + i
		if idx >= len(rows) {
			u.fill(0, y, width, th.Text)
			continue
		}
		n := rows[idx].Node

		style := th.Text
		if n.IsDir {
			style = th.Type
		}
		focused := idx == tr.Sel && !(s.Focused && s.InChanges)
		if focused {
			style = th.Selection
		}
		u.fill(0, y, width, style)

		icon := "  "
		if n.IsDir {
			if n.Expanded {
				icon = "▾ "
			} else {
				icon = "▸ "
			}
		}
		label := strings.Repeat(" ", n.Depth*2) + icon + n.Name
		badge, badgeStyle := u.gitBadge(n, style, focused)
		maxX := innerW
		if badge != 0 {
			maxX = innerW - 2 // keep a space between the name and the badge
		}
		u.drawText(1, y, label, style, maxX)
		if badge != 0 && innerW-1 > 1 {
			u.Screen.SetContent(innerW-1, y, badge, nil, badgeStyle)
		}
	}

	if g.changesY >= 0 {
		u.drawChanges(g, innerW)
	}

	// Separator column between the sidebar and the text area.
	for y := 0; y < height; y++ {
		u.Screen.SetContent(width-1, y, tcell.RuneVLine, nil, th.Gutter)
	}
}
