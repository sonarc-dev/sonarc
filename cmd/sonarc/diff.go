package main

import (
	"bytes"
	"context"
	"github.com/sonarc-dev/sonarc/internal/buffer"
	"github.com/sonarc-dev/sonarc/internal/index/provider"
	"github.com/sonarc-dev/sonarc/internal/ui"
	"github.com/sonarc-dev/sonarc/internal/vcs"
	"os"

	"github.com/gdamore/tcell/v2"
)

// diffContext is how many unchanged lines are shown around each change.
const diffContext = 3

// cmdShowChanges opens the diff of the file on screen.
func (a *app) cmdShowChanges() {
	path := a.v().Buf.Path()
	switch {
	case a.git.repo == nil:
		a.ui.Notify("not in a git repository")
	case path == "":
		a.ui.Notify("this buffer has no file")
	default:
		a.openDiff(path)
	}
}

// openDiff shows how path differs from its last commit, in place of the text.
// An open file is compared as it stands in the editor, unsaved edits and all,
// the same text the gutter marks describe; any other file as it is on disk.
func (a *app) openDiff(path string) bool {
	g := &a.git
	if g.repo == nil {
		return false
	}
	var cur [][]byte
	unsaved := false
	if v := a.viewOf(path); v != nil {
		if v.Buf.Large() {
			a.ui.Notify("%s is too large to diff", displayName(v))
			return false
		}
		cur = linesOf(v.Buf)
		unsaved = v.Buf.Modified()
	} else if data, err := os.ReadFile(path); err == nil {
		if isBinary(data) {
			a.ui.Notify("%s is a binary file", shortPath(a.root, path))
			return false
		}
		cur = linesOf(buffer.FromBytes(data))
	} // else deleted: every committed line is removed

	old, err := a.committedLines(path)
	if err != nil {
		a.ui.Error("cannot read the committed version of %s: %v", shortPath(a.root, path), err)
		return false
	}
	if old == nil && isBinaryLines(cur) {
		a.ui.Notify("%s is a binary file", shortPath(a.root, path))
		return false
	}

	hunks := vcs.Lines(old, cur)
	lines, added, removed := buildDiff(old, cur, hunks, diffContext)
	title := "diff  " + shortPath(a.root, path)
	switch {
	case len(hunks) == 0:
		title += "  (no changes)"
	case old == nil:
		title += "  (new file)"
	case cur == nil:
		title += "  (deleted)"
	case unsaved:
		title += "  (with unsaved edits)"
	}
	a.ui.Diff = ui.DiffView{Open: true, Path: path, Title: title, Lines: lines, Added: added, Removed: removed}
	a.ui.Diff.StepBlock(1, a.ui.View.Height) // start on the first change
	a.ui.Sidebar.Changes.Active = path
	return true
}

// committedLines is path's last committed version as lines, nil when the
// last commit does not have it. It uses the gutter's copy when there is one.
func (a *app) committedLines(path string) ([][]byte, error) {
	if b := a.git.bases[path]; b != nil && !b.loading {
		return b.lines, nil
	}
	data, ok, err := a.git.repo.Committed(context.Background(), path)
	if err != nil || !ok {
		return nil, err
	}
	if isBinary(data) {
		return nil, nil
	}
	return linesOf(buffer.FromBytes(data)), nil
}

func locationAt(path string, line int) provider.Location {
	return provider.Location{Path: path, Line: line}
}

func linesOf(b *buffer.Buffer) [][]byte {
	out := make([][]byte, b.NumLines())
	for i := range out {
		out[i] = b.Line(i)
	}
	return out
}

// isBinary is git's own test: a NUL byte in the first 8000.
func isBinary(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0
}

func isBinaryLines(ls [][]byte) bool {
	for _, l := range ls[:min(len(ls), 200)] {
		if bytes.IndexByte(l, 0) >= 0 {
			return true
		}
	}
	return false
}

// buildDiff lays hunks out as the rows of a diff view: each block of changes
// with ctx unchanged lines around it, and a gap row for what lies between.
func buildDiff(old, cur [][]byte, hunks []vcs.Hunk, ctx int) (out []ui.DiffLine, added, removed int) {
	shown := 0 // old lines before this index have been shown or skipped
	for i, h := range hunks {
		from := max(h.OldStart-ctx, shown)
		if from > shown || (i == 0 && from > 0) {
			out = append(out, ui.DiffLine{Kind: '~'})
		}
		delta := h.NewStart - h.OldStart // new index = old index + delta, outside hunks
		for o := from; o < h.OldStart; o++ {
			out = append(out, ui.DiffLine{Kind: ' ', Old: o + 1, New: o + delta + 1, Text: string(old[o])})
		}
		for k := 0; k < h.OldLines; k++ {
			out = append(out, ui.DiffLine{Kind: '-', Old: h.OldStart + k + 1,
				New: min(h.NewStart+1, max(len(cur), 1)), Text: string(old[h.OldStart+k])})
		}
		for k := 0; k < h.NewLines; k++ {
			out = append(out, ui.DiffLine{Kind: '+', New: h.NewStart + k + 1, Text: string(cur[h.NewStart+k])})
		}
		added += h.NewLines
		removed += h.OldLines

		shown = h.OldStart + h.OldLines
		next := len(old)
		if i+1 < len(hunks) {
			next = hunks[i+1].OldStart
		}
		after := h.NewStart + h.NewLines - shown
		// Trailing context stops where the next block's leading context would
		// begin, so the lines between two close blocks are shown once.
		for o := shown; o < min(shown+ctx, next); o++ {
			out = append(out, ui.DiffLine{Kind: ' ', Old: o + 1, New: o + after + 1, Text: string(old[o])})
		}
		shown = min(shown+ctx, next)
	}
	if len(hunks) > 0 && shown < len(old) {
		out = append(out, ui.DiffLine{Kind: '~'})
	}
	return out, added, removed
}

// diffKey handles keys while a diff is showing. It takes every key: the text
// underneath is not what is on screen, so typing must not reach it.
func (a *app) diffKey(ev *tcell.EventKey) {
	d := &a.ui.Diff
	rows := a.ui.View.Height
	switch ev.Key() {
	case tcell.KeyUp:
		d.Move(-1, rows)
	case tcell.KeyDown:
		d.Move(1, rows)
	case tcell.KeyPgUp:
		d.Move(-rows, rows)
	case tcell.KeyPgDn:
		d.Move(rows, rows)
	case tcell.KeyHome:
		d.Move(-len(d.Lines), rows)
	case tcell.KeyEnd:
		d.Move(len(d.Lines), rows)
	case tcell.KeyEnter:
		a.goToDiffLine()
	case tcell.KeyEscape:
		a.closeDiff()
	case tcell.KeyTab:
		a.cmdChangesList()
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'n', ']', 'j':
			d.StepBlock(1, rows)
		case 'p', '[', 'k':
			d.StepBlock(-1, rows)
		case 'q':
			a.closeDiff()
		}
	}
}

func (a *app) closeDiff() {
	a.ui.Diff.Close()
	a.ui.Sidebar.Changes.Active = ""
}

// goToDiffLine leaves the diff for the file, at the selected line as it is
// now. A removed line has no line of its own; it goes to where it was.
func (a *app) goToDiffLine() {
	d := &a.ui.Diff
	l, ok := d.Current()
	if !ok {
		return
	}
	path := d.Path
	target := l.New
	if l.Kind == '~' {
		// A gap row stands for the unchanged lines; go to the first of them.
		for i := d.Sel + 1; i < len(d.Lines) && target == 0; i++ {
			target = max(d.Lines[i].New-1, 1)
		}
	}
	if _, err := os.Stat(path); err != nil && a.viewOf(path) == nil {
		a.ui.Notify("%s was deleted; there is nothing to open", shortPath(a.root, path))
		return
	}
	a.closeDiff()
	a.ui.Sidebar.Focused = false
	if a.goTo(locationAt(path, max(target, 1))) {
		a.ui.Notify("%s:%d", shortPath(a.root, path), max(target, 1))
	}
}

// diffClick selects the row under a click; a double click opens it.
func (a *app) diffClick(x, y int) {
	d := &a.ui.Diff
	a.ui.Sidebar.Focused = false
	if idx := d.Top + y - a.ui.TextTop(); idx >= d.Top && idx < len(d.Lines) {
		d.Move(idx-d.Sel, a.ui.View.Height)
	}
	if a.click.register(x, y, a.now()) == 2 {
		a.goToDiffLine()
	}
}
