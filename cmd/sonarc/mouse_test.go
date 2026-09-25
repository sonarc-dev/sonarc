package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"sonarc/internal/index/provider"
	"sonarc/internal/ui"
)

// The core regression risk of adding a left column: a click must still land on
// the byte under the pointer once the text is shifted right.
func TestClickLandsOnCorrectByteWithSidebar(t *testing.T) {
	h := sidebarHarness(t, map[string]string{
		"main.c": "first line\nsecond line\nthird\n",
	}, "main.c")

	tx, g := h.app.ui.TextX(), h.app.ui.GutterWidth()
	if tx == 0 {
		t.Fatal("sidebar should be showing")
	}
	h.click(tx+g+3, 1)

	if p := h.app.v().Head; p.Line != 1 || p.Col != 3 {
		t.Errorf("cursor = %+v, want line 1 col 3", p)
	}
}

func TestClickingSidebarFileOpensItWithoutMovingTextCursor(t *testing.T) {
	h := sidebarHarness(t, map[string]string{
		"main.c":  "first line\nsecond line\n",
		"other.c": "int other;\n",
	}, "main.c")

	// Put a cursor somewhere recognisable in main.c first.
	tx, g := h.app.ui.TextX(), h.app.ui.GutterWidth()
	h.click(tx+g+4, 1)
	mainView := h.app.v()
	want := mainView.Head

	h.click(2, h.rowOf("other.c"))

	if filepath.Base(h.app.v().Buf.Path()) != "other.c" {
		t.Fatalf("open buffer = %q, want other.c", h.app.v().Buf.Path())
	}
	if mainView.Head != want {
		t.Errorf("main.c cursor moved to %+v, want it left at %+v", mainView.Head, want)
	}
	if h.app.ui.Sidebar.Focused {
		t.Error("opening a file should hand keyboard focus to the text")
	}
}

func TestClickingSidebarDirectoryExpandsAndFocuses(t *testing.T) {
	h := sidebarHarness(t, map[string]string{
		"sub/inner.c": "int inner;\n",
		"main.c":      "int main;\n",
	}, "main.c")

	if h.screenHas("inner.c") {
		t.Fatal("subdirectory should start collapsed")
	}
	h.click(2, h.rowOf("sub"))

	if !h.screenHas("inner.c") {
		t.Errorf("click did not expand 'sub'; screen:\n%s", strings.Join(h.draw(), "\n"))
	}
	if !h.app.ui.Sidebar.Focused {
		t.Error("clicking the tree should give it keyboard focus")
	}
}

func TestClickingTextReturnsFocusFromSidebar(t *testing.T) {
	h := sidebarHarness(t, map[string]string{"main.c": "hello\n"}, "main.c")
	h.key(tcell.KeyCtrlE)
	if !h.app.ui.Sidebar.Focused {
		t.Fatal("precondition: sidebar focused")
	}
	tx, g := h.app.ui.TextX(), h.app.ui.GutterWidth()
	h.click(tx+g+1, 0)
	if h.app.ui.Sidebar.Focused {
		t.Error("clicking the text should move keyboard focus back to it")
	}
}

func TestSidebarWheelScrollsTreeNotText(t *testing.T) {
	files := map[string]string{"main.c": strings.Repeat("a line\n", 100)}
	for i := 0; i < 60; i++ {
		files[fmt.Sprintf("f%02d.c", i)] = "x\n"
	}
	h := sidebarHarness(t, files, "main.c")

	tree := h.app.ui.Sidebar.Tree
	topBefore, textTop := tree.Top, h.app.v().Top
	h.mouse(3, 5, tcell.WheelDown)
	h.draw()

	if tree.Top <= topBefore {
		t.Errorf("tree Top = %d, want it to have scrolled past %d", tree.Top, topBefore)
	}
	if h.app.v().Top != textTop {
		t.Error("wheel over the sidebar scrolled the text")
	}

	// Scrolling away from the selection must survive a redraw rather than
	// snapping back to it.
	scrolled := tree.Top
	h.draw()
	if tree.Top != scrolled {
		t.Errorf("redraw moved Top from %d to %d", scrolled, tree.Top)
	}
}

func TestWheelOverTextStillScrollsText(t *testing.T) {
	h := sidebarHarness(t, map[string]string{"main.c": strings.Repeat("a line\n", 100)}, "main.c")
	tx := h.app.ui.TextX()
	h.mouse(tx+10, 5, tcell.WheelDown)
	if h.app.v().Top == 0 {
		t.Error("wheel over the text did not scroll it")
	}
}

func TestClickingPanelResultJumpsAndKeepsPanelOpen(t *testing.T) {
	h := sidebarHarness(t, map[string]string{
		"main.c":  "one\ntwo\nthree\n",
		"other.c": "alpha\nbeta\ngamma\ndelta\n",
	}, "main.c")

	other := filepath.Join(h.app.root, "other.c")
	h.app.ui.Panel.Show("refs", []provider.Location{
		{Path: filepath.Join(h.app.root, "main.c"), Line: 1, Text: "one"},
		{Path: other, Line: 3, Text: "gamma"},
	})
	h.draw()

	// Header is the first panel row; results follow.
	top := h.app.v().Height
	tx := h.app.ui.TextX()
	h.click(tx+5, top+2) // second result

	if h.app.v().Buf.Path() != other {
		t.Fatalf("open buffer = %q, want %q", h.app.v().Buf.Path(), other)
	}
	if got := h.app.v().Head.Line; got != 2 {
		t.Errorf("cursor line = %d, want 2 (0-based line of result 3)", got)
	}
	if !h.app.ui.Panel.Open {
		t.Error("clicking a result should leave the panel open")
	}
	if h.app.ui.Panel.Sel != 1 {
		t.Errorf("panel selection = %d, want 1", h.app.ui.Panel.Sel)
	}
}

// Before the fix a click on a result moved the text cursor underneath the
// panel instead of jumping.
func TestClickingPanelDoesNotMoveTextCursorInPlace(t *testing.T) {
	h := sidebarHarness(t, map[string]string{"main.c": "one\ntwo\nthree\nfour\n"}, "main.c")
	path := filepath.Join(h.app.root, "main.c")
	h.app.ui.Panel.Show("refs", []provider.Location{{Path: path, Line: 4, Text: "four"}})
	h.draw()

	tx := h.app.ui.TextX()
	h.click(tx+5, h.app.v().Height+1)

	if got := h.app.v().Head.Line; got != 3 {
		t.Errorf("cursor line = %d, want 3 (jumped to the result)", got)
	}
}

func TestClickingPanelHeaderDoesNothing(t *testing.T) {
	h := sidebarHarness(t, map[string]string{"main.c": "one\ntwo\n"}, "main.c")
	path := filepath.Join(h.app.root, "main.c")
	h.app.ui.Panel.Show("refs", []provider.Location{{Path: path, Line: 2, Text: "two"}})
	h.draw()
	before := h.app.v().Head

	tx := h.app.ui.TextX()
	h.click(tx+5, h.app.v().Height)

	if h.app.v().Head != before {
		t.Errorf("clicking the panel header moved the cursor to %+v", h.app.v().Head)
	}
}

func TestDragInTextExtendsSelection(t *testing.T) {
	h := sidebarHarness(t, map[string]string{"main.c": "hello world\n"}, "main.c")
	tx, g := h.app.ui.TextX(), h.app.ui.GutterWidth()

	h.mouse(tx+g+0, 0, tcell.Button1)
	h.mouse(tx+g+5, 0, tcell.Button1)
	h.mouse(tx+g+5, 0, tcell.ButtonNone)

	if got := string(h.app.v().SelectedText()); got != "hello" {
		t.Errorf("selection = %q, want %q", got, "hello")
	}
}

func TestDragPastLeftEdgeClampsToColumnZero(t *testing.T) {
	h := sidebarHarness(t, map[string]string{"main.c": "hello world\n"}, "main.c")
	tx, g := h.app.ui.TextX(), h.app.ui.GutterWidth()

	h.mouse(tx+g+5, 0, tcell.Button1)
	h.mouse(2, 0, tcell.Button1) // over the sidebar
	h.mouse(2, 0, tcell.ButtonNone)

	if got := string(h.app.v().SelectedText()); got != "hello" {
		t.Errorf("selection = %q, want %q", got, "hello")
	}
}

// Stale drag state: a press that begins outside the text must not turn later
// motion over the text into a selection.
func TestPressInSidebarThenMotionOverTextDoesNotSelect(t *testing.T) {
	h := sidebarHarness(t, map[string]string{
		"sub/inner.c": "x\n",
		"main.c":      "hello world\n",
	}, "main.c")
	tx, g := h.app.ui.TextX(), h.app.ui.GutterWidth()
	before := h.app.v().Head

	h.mouse(2, h.rowOf("sub"), tcell.Button1) // press begins in the sidebar
	h.mouse(tx+g+6, 0, tcell.Button1)         // ...and slides over the text
	h.mouse(tx+g+6, 0, tcell.ButtonNone)

	if h.app.v().HasSelection() {
		t.Error("a drag that began in the sidebar created a text selection")
	}
	if h.app.v().Head != before {
		t.Errorf("cursor moved to %+v", h.app.v().Head)
	}
}

func TestLostReleaseDoesNotLeakDragIntoNextPress(t *testing.T) {
	h := sidebarHarness(t, map[string]string{"main.c": "hello world\n"}, "main.c")
	tx, g := h.app.ui.TextX(), h.app.ui.GutterWidth()

	h.mouse(tx+g+2, 0, tcell.Button1) // press; release never arrives
	h.mouse(tx+g+2, 0, tcell.ButtonNone)
	h.click(tx+g+8, 0) // a fresh click must place, not extend

	if h.app.v().HasSelection() {
		t.Error("fresh click extended the previous selection")
	}
}

func TestPickerOwnsTheMouse(t *testing.T) {
	h := sidebarHarness(t, map[string]string{"main.c": "hello world\n"}, "main.c")
	before := h.app.v().Head
	h.app.ui.ShowPicker("pick", []ui.PickerItem{{Label: "a"}, {Label: "b"}})
	h.draw()

	tx, g := h.app.ui.TextX(), h.app.ui.GutterWidth()
	h.click(tx+g+5, 0)

	if h.app.v().Head != before {
		t.Error("a click while the picker was open moved the text cursor")
	}
	if !h.app.ui.Picker.Open {
		t.Error("a stray click should not dismiss the picker")
	}

	h.mouse(tx, 3, tcell.WheelDown)
	if h.app.ui.Picker.Sel != 1 {
		t.Errorf("wheel should move the picker selection; Sel = %d", h.app.ui.Picker.Sel)
	}
}

func TestClickOnStatusBarIsIgnored(t *testing.T) {
	h := sidebarHarness(t, map[string]string{"main.c": "hello\n"}, "main.c")
	before := h.app.v().Head
	_, hgt := h.app.scr.Size()
	h.click(30, hgt-2)
	h.click(30, hgt-1)
	if h.app.v().Head != before {
		t.Error("clicking the status or message line moved the cursor")
	}
}

func multiClickHarness(t *testing.T, content string) (*harness, *fakeClock, int, int) {
	t.Helper()
	h := sidebarHarness(t, map[string]string{"main.c": content}, "main.c")
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	h.app.now = clk.now
	return h, clk, h.app.ui.TextX() + h.app.ui.GutterWidth(), 0
}

func TestDoubleClickSelectsWord(t *testing.T) {
	h, clk, x0, _ := multiClickHarness(t, "alpha beta gamma\n")

	h.click(x0+7, 0)
	clk.advance(100 * time.Millisecond)
	h.click(x0+7, 0)

	if got := string(h.app.v().SelectedText()); got != "beta" {
		t.Errorf("double-click selected %q, want %q", got, "beta")
	}
}

func TestTripleClickSelectsLine(t *testing.T) {
	h, clk, x0, _ := multiClickHarness(t, "alpha beta\nsecond line\n")

	for i := 0; i < 3; i++ {
		h.click(x0+2, 0)
		clk.advance(100 * time.Millisecond)
	}
	if got := string(h.app.v().SelectedText()); got != "alpha beta\n" {
		t.Errorf("triple-click selected %q, want the whole first line", got)
	}
}

func TestSlowSecondClickIsNotADoubleClick(t *testing.T) {
	h, clk, x0, _ := multiClickHarness(t, "alpha beta\n")

	h.click(x0+2, 0)
	clk.advance(multiClickWindow + time.Millisecond)
	h.click(x0+2, 0)

	if h.app.v().HasSelection() {
		t.Errorf("clicks %v apart selected %q", multiClickWindow+time.Millisecond, h.app.v().SelectedText())
	}
}

func TestClickOnADifferentCellIsNotADoubleClick(t *testing.T) {
	h, clk, x0, _ := multiClickHarness(t, "alpha beta\n")

	h.click(x0+2, 0)
	clk.advance(50 * time.Millisecond)
	h.click(x0+3, 0) // one cell over

	if h.app.v().HasSelection() {
		t.Errorf("clicks on different cells selected %q", h.app.v().SelectedText())
	}
}

func TestFourthClickStartsOver(t *testing.T) {
	h, clk, x0, _ := multiClickHarness(t, "alpha beta\n")

	for i := 0; i < 4; i++ {
		h.click(x0+2, 0)
		clk.advance(50 * time.Millisecond)
	}
	if h.app.v().HasSelection() {
		t.Errorf("fourth click should be a fresh single click, got selection %q", h.app.v().SelectedText())
	}
}

// Terminals may repeat a motion report for the cell the press landed on; that
// must not shrink the word a double-click just selected.
func TestMotionInPlaceKeepsMultiClickSelection(t *testing.T) {
	h, clk, x0, _ := multiClickHarness(t, "alpha beta gamma\n")

	h.click(x0+7, 0)
	clk.advance(100 * time.Millisecond)
	h.mouse(x0+7, 0, tcell.Button1) // second press
	h.mouse(x0+7, 0, tcell.Button1) // duplicate motion report
	h.mouse(x0+7, 0, tcell.ButtonNone)

	if got := string(h.app.v().SelectedText()); got != "beta" {
		t.Errorf("selection = %q after in-place motion, want %q", got, "beta")
	}
}

func TestDoubleClickInSidebarDoesNotSelectText(t *testing.T) {
	h, clk, _, _ := multiClickHarness(t, "alpha beta\n")

	row := h.rowOf("main.c")
	h.click(2, row)
	clk.advance(50 * time.Millisecond)
	h.click(2, row)

	if h.app.v().HasSelection() {
		t.Error("double-clicking a tree entry selected text")
	}
}

// A release that never reaches onMouse, because a prompt read and dropped it
// or because a key came first, must not leave the press open: the next click
// would pass for a drag and do nothing.
func TestClickAfterAnUnhandledReleaseStillWorks(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  tcell.Event
	}{
		{"release read by another loop", tcell.NewEventMouse(0, 0, tcell.ButtonNone, tcell.ModNone)},
		{"key instead of a release", tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := sidebarHarness(t, map[string]string{"a/x.c": "", "b/y.c": "", "m.c": ""}, "m.c")
			h.mouse(2, h.rowOf("a"), tcell.Button1) // press; the release goes elsewhere
			h.read(tc.end)                          // dropped, as a prompt drops it

			y := h.rowOf("b")
			h.click(2, y)
			if !strings.Contains(strings.Join(h.draw(), "\n"), "y.c") {
				t.Errorf("click on b did not expand it:\n%s", strings.Join(h.draw(), "\n"))
			}
		})
	}
}

func TestMouseClickPlacesCursor(t *testing.T) {
	h := newHarness(t, "one\ntwo\nthree")
	h.draw() // establish the gutter width
	g := h.app.ui.GutterWidth()
	h.app.handle(tcell.NewEventMouse(g+2, 1, tcell.Button1, tcell.ModNone))
	if p := h.app.v().Head; p.Line != 1 || p.Col != 2 {
		t.Errorf("click cursor = %v, want line 1 col 2", p)
	}
}

func TestMouseWheelScrollsWithoutMovingCursor(t *testing.T) {
	h := newHarness(t, strings.Repeat("a line\n", 100))
	before := h.app.v().Head
	h.app.handle(tcell.NewEventMouse(10, 5, tcell.WheelDown, tcell.ModNone))
	if h.app.v().Top == 0 {
		t.Error("wheel did not scroll")
	}
	if h.app.v().Head != before {
		t.Error("wheel moved the cursor")
	}
}
