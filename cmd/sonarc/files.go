package main

import (
	"fmt"
	"path/filepath"
	"strconv"

	"sonarc/internal/buffer"
	"sonarc/internal/index/provider"
	"sonarc/internal/ui"
	"sonarc/internal/view"
)

// cmdCloseFile closes the file on screen, asking first if it has unsaved
// changes. Closing the last file leaves an empty buffer rather than quitting:
// Ctrl+W is "done with this file", and Ctrl+Q is how to leave.
func (a *app) cmdCloseFile() {
	v := a.v()
	if !a.settleUnsaved([]*view.View{v}) {
		return
	}
	name := displayName(v)
	a.removeView(a.cur)
	a.ui.Notify("closed %s", name)
}

// cmdCloseOthers closes every file except the one on screen.
func (a *app) cmdCloseOthers() {
	if len(a.views) < 2 {
		a.ui.Notify("no other files are open")
		return
	}
	keep := a.v()
	var others []*view.View
	for _, v := range a.views {
		if v != keep {
			others = append(others, v)
		}
	}
	if !a.settleUnsaved(others) {
		return
	}
	a.rememberPlaces(others...)
	for _, v := range others {
		delete(a.warnedDisk, v)
		a.forgetView(v)
	}
	a.views = []*view.View{keep}
	a.switchTo(0)
	a.ui.Notify("closed %d other files", len(others))
}

// settleUnsaved asks what to do with any of vs that have unsaved changes, and
// saves them if told to. It reports whether closing them may go ahead.
func (a *app) settleUnsaved(vs []*view.View) bool {
	var dirty []*view.View
	for _, v := range vs {
		if v.Buf.Modified() {
			dirty = append(dirty, v)
		}
	}
	if len(dirty) == 0 {
		return true
	}
	q := fmt.Sprintf("%s has unsaved changes. [s]ave, [d]iscard, [c]ancel: ", displayName(dirty[0]))
	if len(dirty) > 1 {
		q = fmt.Sprintf("%d files have unsaved changes. [s]ave all, [d]iscard, [c]ancel: ", len(dirty))
	}
	answer, ok := a.prompt(q, "")
	if !ok {
		a.ui.Notify("cancelled")
		return false
	}
	switch answer {
	case "s", "S":
		for _, v := range dirty {
			if v.Buf.Path() == "" {
				a.switchTo(indexOf(a.views, v))
				a.cmdSaveAs()
			} else if !a.writeView(v) {
				return false
			}
		}
		for _, v := range dirty {
			if v.Buf.Modified() {
				return false // a save failed or was cancelled; keep the work
			}
		}
		return true
	case "d", "D":
		return true
	}
	a.ui.Notify("cancelled")
	return false
}

// removeView drops view i, showing its neighbor instead. There is always a
// view on screen, so the last one is replaced by an empty buffer.
func (a *app) removeView(i int) {
	a.rememberPlaces(a.views[i])
	delete(a.warnedDisk, a.views[i])
	a.forgetView(a.views[i])
	a.views = append(a.views[:i], a.views[i+1:]...)
	if len(a.views) == 0 {
		a.views = []*view.View{view.New(buffer.New())}
	}
	a.switchTo(min(i, len(a.views)-1))
}

// cmdOpenFiles lists the open files to switch between, like editor tabs.
func (a *app) cmdOpenFiles() {
	items := make([]ui.PickerItem, 0, len(a.views))
	for i, v := range a.views {
		label := displayName(v)
		if v.Buf.Modified() {
			label += " ●"
		}
		detail := "(not saved to a file)"
		if p := v.Buf.Path(); p != "" {
			detail = shortPath(a.root, p)
		}
		if i == a.cur {
			detail += "  (showing)"
		}
		items = append(items, ui.PickerItem{
			Label:  label,
			Detail: detail,
			Loc:    provider.Location{Text: strconv.Itoa(i)},
		})
	}
	a.ui.ShowPicker(fmt.Sprintf("open files  (%d)", len(a.views)), items)
	a.pickAction = func(it ui.PickerItem) {
		if i, err := strconv.Atoi(it.Loc.Text); err == nil {
			a.switchTo(i)
		}
	}
}

// displayName is how a view's file is named in messages and lists.
func displayName(v *view.View) string {
	if p := v.Buf.Path(); p != "" {
		return filepath.Base(p)
	}
	return "[No Name]"
}

// openFile shows path in the editor, reusing an already-open buffer for it.
func (a *app) openFile(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	for i, v := range a.views {
		if v.Buf.Path() == abs {
			a.switchTo(i)
			return nil
		}
	}
	buf, err := buffer.Open(abs)
	if err != nil {
		return err
	}
	v := view.New(buf)
	a.recallPlace(v)
	a.views = append(a.views, v)
	a.switchTo(len(a.views) - 1)
	return nil
}

// switchTo makes view i visible.
func (a *app) switchTo(i int) {
	if i < 0 || i >= len(a.views) {
		return
	}
	a.cur = i
	a.ui.View = a.views[i]
	// Going to a file means looking at it, not at a diff.
	a.closeDiff()
	// Every route to a buffer — goto-definition, fuzzy open, jump-back, a
	// clicked result — passes through here, so the tree follows the file
	// without each caller having to remember to ask.
	if a.ui.Sidebar.Tree != nil {
		a.ui.Sidebar.Tree.Reveal(a.views[i].Buf.Path())
	}
}

// nextBuffer cycles through open buffers.
func (a *app) nextBuffer(delta int) {
	if len(a.views) < 2 {
		a.ui.Notify("only one file is open")
		return
	}
	n := len(a.views)
	a.switchTo(((a.cur+delta)%n + n) % n)
	a.ui.Notify("%s", a.v().Buf.Path())
}

func (a *app) cmdSave() {
	if a.v().Buf.Path() == "" {
		a.cmdSaveAs()
		return
	}
	if !a.writeView(a.v()) {
		return
	}
	a.ui.Notify("saved %s", a.v().Buf.Path())
}

// afterSave brings everything that depends on a file's contents on disk up to
// date once v has been written, or reread after another program wrote it.
func (a *app) afterSave(v *view.View) {
	a.refreshGit() // the file may now differ from the commit, or no longer
}

func (a *app) cmdSaveAs() {
	name, ok := a.prompt("Save as: ", a.v().Buf.Path())
	if !ok || name == "" {
		a.ui.Notify("cancelled")
		return
	}
	if err := a.v().Buf.SaveAs(name); err != nil {
		a.ui.Error("save failed: %v", err)
		return
	}
	a.afterSave(a.v())
	a.ui.Notify("saved %s", name)
}

// modifiedViews returns the open buffers with unsaved changes.
func (a *app) modifiedViews() []*view.View {
	var out []*view.View
	for _, v := range a.views {
		if v.Buf.Modified() {
			out = append(out, v)
		}
	}
	return out
}

func (a *app) cmdQuit() {
	if !a.confirmQuitDuringRebuild() {
		return
	}
	dirty := a.modifiedViews()
	if len(dirty) > 0 {
		q := "Unsaved changes. [s]ave, [d]iscard, [c]ancel: "
		if len(dirty) > 1 {
			q = fmt.Sprintf("%d files have unsaved changes. [s]ave all, [d]iscard, [c]ancel: ", len(dirty))
		}
		answer, ok := a.prompt(q, "")
		if !ok {
			return
		}
		switch answer {
		case "s":
			for _, v := range dirty {
				if v.Buf.Path() == "" {
					a.switchTo(indexOf(a.views, v))
					a.cmdSaveAs()
				} else if !a.writeView(v) {
					return
				}
			}
			if len(a.modifiedViews()) > 0 {
				return // a save failed; stay open rather than lose the work
			}
		case "d":
			// fall through and quit
		default:
			a.ui.Notify("cancelled")
			return
		}
	}
	a.cancelQuery("")
	a.quit = true
}

func indexOf(vs []*view.View, want *view.View) int {
	for i, v := range vs {
		if v == want {
			return i
		}
	}
	return 0
}

func (a *app) cmdOpenFile() {
	name, ok := a.prompt("Open: ", "")
	if !ok || name == "" {
		return
	}
	if err := a.openFile(name); err != nil {
		a.ui.Error("cannot open: %v", err)
	}
}

// viewOf is the open view of path, if any.
func (a *app) viewOf(path string) *view.View {
	for _, v := range a.views {
		if v.Buf.Path() == path {
			return v
		}
	}
	return nil
}

// shortPath renders a path relative to root where possible.
func shortPath(root, path string) string {
	if root != "" {
		if rel, err := filepath.Rel(root, path); err == nil && len(rel) < len(path) &&
			!filepath.IsAbs(rel) && rel[0] != '.' {
			return rel
		}
	}
	return path
}
