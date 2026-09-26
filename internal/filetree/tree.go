// Package filetree is the model behind the sidebar: a lazily-expanded view of
// a directory on disk. It has no tcell dependency and does no drawing — it
// only tracks which directories are expanded and flattens that into rows a
// renderer can draw.
//
// Loading is lazy by design. A tree that read every file under a project
// root up front would repeat the mistake the built-in indexer made on a
// kernel-sized tree: it walked eagerly and cost gigabytes. Only a directory
// the user actually expands is ever read.
package filetree

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sonarc-dev/sonarc/internal/ignore"
)

// Node is one entry in the tree: a file or a directory.
type Node struct {
	Path     string // absolute
	Name     string
	IsDir    bool
	Expanded bool
	Depth    int

	children []*Node // only populated for a directory once expanded
	loaded   bool
}

// Row is one line of the flattened, currently-visible tree, as the sidebar
// draws it.
type Row struct {
	Node *Node
}

// Tree is the file-tree model rooted at a directory.
type Tree struct {
	root *Node
	ig   *ignore.Ignorer

	// Sel indexes the currently selected row within Rows().
	Sel int
	// Top is the first visible row, for scrolling.
	Top int

	// follow asks the next Fit to scroll the selection into view. Keyboard
	// movement and Reveal set it; wheel scrolling clears it, so scrolling away
	// from the selection is not undone on the next redraw.
	follow bool
}

// New returns a Tree rooted at dir. The root itself starts expanded so its
// immediate children are visible right away; nothing below that is read
// until the user expands it.
func New(dir string) *Tree {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	t := &Tree{
		root: &Node{Path: abs, Name: filepath.Base(abs), IsDir: true, Expanded: true},
		ig:   ignore.Load(abs),
	}
	t.load(t.root)
	return t
}

// Root returns the tree's root path.
func (t *Tree) Root() string { return t.root.Path }

// load reads dir's children from disk, if it hasn't already. Directories are
// listed before files, each lexical, so the tree reads the way a project
// actually looks rather than in raw directory order.
func (t *Tree) load(n *Node) {
	if n.loaded || !n.IsDir {
		return
	}
	n.loaded = true

	entries, err := os.ReadDir(n.Path)
	if err != nil {
		return // unreadable directory: show it empty rather than erroring
	}

	rootRel, _ := filepath.Rel(t.root.Path, n.Path)
	if rootRel == "." {
		rootRel = ""
	}

	var dirs, files []*Node
	for _, e := range entries {
		name := e.Name()
		rel := name
		if rootRel != "" {
			rel = filepath.Join(rootRel, name)
		}
		isDir := e.IsDir()
		if isDir {
			if t.ig.SkipDir(rel, name) {
				continue
			}
		} else {
			if !e.Type().IsRegular() {
				continue // symlinks, devices, sockets
			}
			if t.ig.SkipFile(rel, name) {
				continue
			}
		}
		child := &Node{
			Path:  filepath.Join(n.Path, name),
			Name:  name,
			IsDir: isDir,
			Depth: n.Depth + 1,
		}
		if isDir {
			dirs = append(dirs, child)
		} else {
			files = append(files, child)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })

	n.children = append(dirs, files...)
}

// Refresh rereads every directory that has been read, so files that other
// programs created or deleted appear and disappear. Only directories already
// loaded are touched, so on a kernel tree it costs what the user has opened,
// not a walk. Existing entries are kept, open directories stay open, and the
// selection stays on the same file when that file still exists.
func (t *Tree) Refresh() {
	selected := ""
	if n, ok := t.Selected(); ok {
		selected = n.Path
	}
	t.refresh(t.root)
	if selected != "" {
		for i, r := range t.Rows() {
			if r.Node.Path == selected {
				t.Sel = i
				return
			}
		}
	}
	t.Sel = min(t.Sel, max(len(t.Rows())-1, 0))
}

func (t *Tree) refresh(n *Node) {
	if !n.loaded {
		return
	}
	old := make(map[string]*Node, len(n.children))
	for _, c := range n.children {
		old[c.Name] = c
	}
	n.loaded = false
	t.load(n)
	for i, c := range n.children {
		if prev, ok := old[c.Name]; ok && prev.IsDir == c.IsDir {
			n.children[i] = prev
		}
	}
	for _, c := range n.children {
		t.refresh(c)
	}
}

// Rows flattens the currently-expanded tree into the rows a renderer draws,
// depth-first, directories first at every level.
func (t *Tree) Rows() []Row {
	var rows []Row
	var walk func(n *Node)
	walk = func(n *Node) {
		for _, c := range n.children {
			rows = append(rows, Row{Node: c})
			if c.IsDir && c.Expanded {
				walk(c)
			}
		}
	}
	walk(t.root)
	return rows
}

// Toggle expands or collapses the directory at row i. Expanding a directory
// for the first time reads it from disk; collapsing never re-reads.
func (t *Tree) Toggle(i int) {
	rows := t.Rows()
	if i < 0 || i >= len(rows) {
		return
	}
	n := rows[i].Node
	if !n.IsDir {
		return
	}
	if !n.Expanded {
		t.load(n)
	}
	n.Expanded = !n.Expanded
}

// Selected returns the node at the current selection, if any.
func (t *Tree) Selected() (*Node, bool) {
	rows := t.Rows()
	if t.Sel < 0 || t.Sel >= len(rows) {
		return nil, false
	}
	return rows[t.Sel].Node, true
}

// Move changes the selection by n, clamped to the visible rows, and keeps Top
// in range so the selection stays on screen within a viewport of height.
func (t *Tree) Move(n, height int) {
	rows := t.Rows()
	t.Sel += n
	if t.Sel < 0 {
		t.Sel = 0
	}
	if t.Sel >= len(rows) {
		t.Sel = len(rows) - 1
	}
	if t.Sel < 0 {
		t.Sel = 0
	}
	t.follow = true
	t.scrollTo(height)
}

// SelectRow selects row i, ignoring an out-of-range index. It is what a mouse
// click uses; the clicked row is already on screen, so nothing scrolls.
func (t *Tree) SelectRow(i int) bool {
	if i < 0 || i >= len(t.Rows()) {
		return false
	}
	t.Sel = i
	return true
}

// ScrollBy moves the viewport by n rows without touching the selection.
func (t *Tree) ScrollBy(n, height int) {
	t.follow = false
	t.Top += n
	t.clampTop(height)
}

// clampTop keeps Top within the rows that exist, so collapsing a directory or
// scrolling past the end never leaves the viewport showing nothing.
func (t *Tree) clampTop(height int) {
	max := len(t.Rows()) - height
	if max < 0 {
		max = 0
	}
	if t.Top > max {
		t.Top = max
	}
	if t.Top < 0 {
		t.Top = 0
	}
}

// Fit reconciles the viewport with a window of height rows. Call it on every
// redraw: the height can change between key presses (a terminal resize), and
// Reveal cannot scroll by itself because it does not know the height.
func (t *Tree) Fit(height int) {
	if t.follow {
		t.scrollTo(height)
		t.follow = false
	}
	t.clampTop(height)
}

// scrollTo adjusts Top so the current selection is within a viewport of
// height rows.
func (t *Tree) scrollTo(height int) {
	if height <= 0 {
		return
	}
	if t.Sel < t.Top {
		t.Top = t.Sel
	}
	if t.Sel >= t.Top+height {
		t.Top = t.Sel - height + 1
	}
	if t.Top < 0 {
		t.Top = 0
	}
}

// Reveal expands every ancestor of path and selects it, so the tree shows
// where an externally-opened file lives. It reports whether path was found
// in the tree; when it was not (outside the root, or hidden by an ignore
// rule) nothing is expanded or selected.
func (t *Tree) Reveal(path string) bool {
	cur, ancestors, ok := t.find(path)
	if !ok {
		return false
	}
	for _, n := range ancestors {
		n.Expanded = true
	}

	for i, r := range t.Rows() {
		if r.Node == cur {
			t.Sel = i
			t.follow = true
			return true
		}
	}
	return false
}

// Expanded lists the open directories below the root, parents before their
// children, for saving and restoring with Expand.
func (t *Tree) Expanded() []string {
	var out []string
	var walk func(n *Node)
	walk = func(n *Node) {
		for _, c := range n.children {
			if c.IsDir && c.Expanded {
				out = append(out, c.Path)
				walk(c)
			}
		}
	}
	walk(t.root)
	return out
}

// Expand opens the directory at path and every directory above it. A path
// that no longer exists is skipped.
func (t *Tree) Expand(path string) {
	n, ancestors, ok := t.find(path)
	if !ok || !n.IsDir {
		return
	}
	for _, a := range ancestors {
		a.Expanded = true
	}
	t.load(n)
	n.Expanded = true
}

// find locates the node for path, reading directories on the way, along with
// the directories between it and the root. Nothing is expanded, so a miss
// leaves the tree exactly as the user left it.
func (t *Tree) find(path string) (*Node, []*Node, bool) {
	if path == "" {
		return nil, nil, false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	rel, err := filepath.Rel(t.root.Path, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, nil, false // not under this tree
	}

	parts := strings.Split(rel, string(filepath.Separator))
	var ancestors []*Node
	cur := t.root
	for i, part := range parts {
		t.load(cur)
		var next *Node
		for _, c := range cur.children {
			if c.Name == part {
				next = c
				break
			}
		}
		if next == nil {
			return nil, nil, false
		}
		if i < len(parts)-1 {
			ancestors = append(ancestors, next)
		}
		cur = next
	}
	return cur, ancestors, true
}
