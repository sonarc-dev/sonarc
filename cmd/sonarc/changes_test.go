package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// changedProject is a committed project with two files then changed: one
// edited on disk, one new.
func changedProject(t *testing.T) *harness {
	h := gitProject(t, map[string]string{
		"a.c":     "one\ntwo\nthree\n",
		"sub/b.c": "keep\n",
		"z.c":     "zz\n",
	}, "z.c")
	os.WriteFile(filepath.Join(h.app.root, "a.c"), []byte("one\nTWO\nthree\n"), 0o644)
	os.WriteFile(filepath.Join(h.app.root, "sub", "new.c"), []byte("fresh\n"), 0o644)
	h.app.refreshGit()
	h.waitGit()
	return h
}

func TestChangesSectionListsChangedFiles(t *testing.T) {
	h := changedProject(t)
	scr := h.screen()
	for _, want := range []string{"▾ CHANGES  2", "M a.c", "? new.c sub/"} {
		if !strings.Contains(scr, want) {
			t.Errorf("sidebar lacks %q:\n%s", want, scr)
		}
	}
	// The tree ends above the section; its rows and the section never overlap.
	head := h.rowWith("CHANGES")
	if got := h.app.ui.SidebarListRows(); got != head-1 {
		t.Errorf("tree has %d rows, want %d (up to the section header at %d)", got, head-1, head)
	}
}

func TestChangesSectionCollapsesAndRemembers(t *testing.T) {
	h := changedProject(t)
	path := saving(t, h)
	treeRows := h.app.ui.SidebarListRows()
	h.click(3, h.rowWith("CHANGES"))
	if !h.screenHas("▸ CHANGES  2") || h.screenHas("M a.c") {
		t.Fatalf("header click did not collapse:\n%s", h.screen())
	}
	if h.app.ui.SidebarListRows() <= treeRows {
		t.Error("collapsing did not give the rows back to the tree")
	}
	if !strings.Contains(readState(t, path), `"changes_collapsed":true`) {
		t.Errorf("collapse not saved: %s", readState(t, path))
	}
	h.click(3, h.rowWith("CHANGES"))
	if !h.screenHas("M a.c") {
		t.Error("second click did not expand it")
	}
}

func TestClickingAChangedFileShowsItsDiff(t *testing.T) {
	h := changedProject(t)
	h.click(5, h.rowWith("M a.c"))
	if !h.app.ui.Diff.Open {
		t.Fatalf("no diff open; msg %q", h.app.ui.Msg)
	}
	scr := h.screen()
	for _, want := range []string{"- two", "+ TWO", "diff  a.c", "+1 −1"} {
		if !strings.Contains(scr, want) {
			t.Errorf("diff view lacks %q:\n%s", want, scr)
		}
	}
	if h.curFile() != "z.c" {
		t.Error("showing a diff switched the open file")
	}

	// Typing while a diff shows must not edit the file underneath.
	h.app.ui.Sidebar.Focused = false
	h.typeText("xyz")
	h.key(tcell.KeyBackspace2)
	if h.app.v().Buf.Modified() {
		t.Error("keys reached the hidden text")
	}

	// Double-clicking a line opens the file there.
	y := h.rowWith("+ TWO")
	h.click(h.app.ui.TextX()+4, y)
	h.click(h.app.ui.TextX()+4, y)
	if h.app.ui.Diff.Open || h.curFile() != "a.c" || h.app.v().Head.Line != 1 {
		t.Errorf("double click: diff open=%v, file %s line %d", h.app.ui.Diff.Open, h.curFile(), h.app.v().Head.Line)
	}
	h.app.cmdJumpBack()
	if h.curFile() != "z.c" {
		t.Error("jump back after leaving a diff did not return")
	}
}

// Reviewing by keyboard: Ctrl+K m into the list, arrows show each diff in
// turn, Enter goes into the diff, Enter again to the line, Esc back out.
func TestReviewChangesByKeyboard(t *testing.T) {
	h := changedProject(t)
	h.app.ui.Sidebar.Focused = false
	h.key(tcell.KeyCtrlK)
	h.typeText("m")
	if !h.app.ui.Sidebar.Focused || !h.app.ui.Sidebar.InChanges {
		t.Fatal("Ctrl+K m did not focus the list")
	}
	h.key(tcell.KeyDown)
	if h.app.ui.Diff.Path != filepath.Join(h.app.root, "sub", "new.c") || !strings.Contains(h.app.ui.Diff.Title, "new file") {
		t.Errorf("Down showed %q %q", h.app.ui.Diff.Path, h.app.ui.Diff.Title)
	}
	h.key(tcell.KeyUp)
	if h.app.ui.Diff.Path != filepath.Join(h.app.root, "a.c") {
		t.Fatalf("Up showed %q", h.app.ui.Diff.Path)
	}
	h.key(tcell.KeyEnter)
	if h.app.ui.Sidebar.Focused {
		t.Fatal("Enter did not move into the diff")
	}
	if l, _ := h.app.ui.Diff.Current(); l.Kind != '-' {
		t.Errorf("diff starts on %q, want the first change", l.Kind)
	}
	h.key(tcell.KeyDown) // onto "+ TWO"
	h.key(tcell.KeyEnter)
	if h.curFile() != "a.c" || h.app.v().Head.Line != 1 {
		t.Errorf("Enter went to %s line %d", h.curFile(), h.app.v().Head.Line)
	}

	h.app.cmdShowChanges() // F8 on the file itself
	h.key(tcell.KeyEscape)
	if h.app.ui.Diff.Open || h.curFile() != "a.c" {
		t.Error("Esc did not return to the file")
	}
}

// Tree clicks still land on the right entry with the section below.
func TestTreeClicksWithChangesSection(t *testing.T) {
	h := changedProject(t)
	h.click(3, h.rowOf("sub"))
	if !h.screenHas("b.c") {
		t.Errorf("clicking sub did not expand it:\n%s", h.screen())
	}
}

// Opening a file from the tree while a diff shows puts the file on screen.
func TestOpeningAFileClosesTheDiff(t *testing.T) {
	h := changedProject(t)
	h.click(5, h.rowWith("M a.c"))
	if !h.app.ui.Diff.Open {
		t.Fatal("no diff")
	}
	h.click(3, h.rowOf("z.c"))
	if h.app.ui.Diff.Open || h.curFile() != "z.c" {
		t.Errorf("after opening z.c from the tree: diff open=%v, file %s", h.app.ui.Diff.Open, h.curFile())
	}
}
