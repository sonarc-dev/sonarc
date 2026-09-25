package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestSidebarVisibleByDefaultWhenWide(t *testing.T) {
	h := withSidebar(project(t, map[string]string{
		"main.c":  "int main(void) { return 0; }\n",
		"other.c": "int other(void) { return 0; }\n",
	}, "main.c"))

	if !h.screenHas("other.c") {
		t.Errorf("sidebar entries not rendered by default; screen:\n%s", strings.Join(h.draw(), "\n"))
	}
	if h.app.ui.Sidebar.Focused {
		t.Error("sidebar must not steal focus from the text by default")
	}
}

// From the default state (visible, unfocused) one press must reach the tree;
// a second hides it; a third brings it back focused.
func TestCtrlECyclesFocusHideShow(t *testing.T) {
	h := withSidebar(project(t, map[string]string{
		"main.c":  "int main(void) { return 0; }\n",
		"other.c": "int other(void) { return 0; }\n",
	}, "main.c"))
	h.draw()

	h.key(tcell.KeyCtrlE)
	h.draw()
	if !h.app.ui.Sidebar.Focused || h.app.ui.TextX() == 0 {
		t.Fatalf("1st Ctrl+E: want shown+focused, got Focused=%v TextX=%d", h.app.ui.Sidebar.Focused, h.app.ui.TextX())
	}

	h.key(tcell.KeyCtrlE)
	h.draw()
	if h.app.ui.Sidebar.Focused || h.app.ui.TextX() != 0 {
		t.Fatalf("2nd Ctrl+E: want hidden, got Focused=%v TextX=%d", h.app.ui.Sidebar.Focused, h.app.ui.TextX())
	}

	h.key(tcell.KeyCtrlE)
	h.draw()
	if !h.app.ui.Sidebar.Focused || h.app.ui.TextX() == 0 {
		t.Fatalf("3rd Ctrl+E: want shown+focused again, got Focused=%v TextX=%d", h.app.ui.Sidebar.Focused, h.app.ui.TextX())
	}
}

func TestSidebarArrowsAndEnterOpenAFile(t *testing.T) {
	h := withSidebar(project(t, map[string]string{
		"main.c":  "int main(void) { return 0; }\n",
		"other.c": "int other(void) { return 0; }\n",
	}, "main.c"))

	h.key(tcell.KeyCtrlE) // show + focus

	// Tree lists directories first, then files lexically: main.c, other.c.
	tree := h.app.ui.Sidebar.Tree
	before := tree.Sel
	h.key(tcell.KeyDown)
	if tree.Sel == before {
		t.Fatal("Down did not move the sidebar selection")
	}

	node, ok := tree.Selected()
	if !ok {
		t.Fatal("no sidebar selection after moving down")
	}
	want := node.Path

	h.key(tcell.KeyEnter)

	if h.app.v().Buf.Path() != want {
		t.Errorf("open buffer = %q, want %q", h.app.v().Buf.Path(), want)
	}
	if h.app.ui.Sidebar.Focused {
		t.Error("opening a file from the sidebar should return focus to the text")
	}
}

func TestSidebarEscapeReturnsFocusWithoutClosing(t *testing.T) {
	h := withSidebar(project(t, map[string]string{
		"main.c": "int main(void) { return 0; }\n",
	}, "main.c"))

	h.key(tcell.KeyCtrlE)
	if !h.app.ui.Sidebar.Focused {
		t.Fatal("sidebar should be focused after Ctrl+E")
	}

	h.key(tcell.KeyEscape)
	h.draw()

	if h.app.ui.Sidebar.Focused {
		t.Error("Escape should clear sidebar focus")
	}
	if h.app.ui.TextX() == 0 {
		t.Error("Escape should not close the sidebar, only unfocus it")
	}
}

func TestSidebarDirectoryTogglesWithRightAndLeft(t *testing.T) {
	h := withSidebar(project(t, map[string]string{
		"sub/inner.c": "int inner(void) { return 0; }\n",
		"main.c":      "int main(void) { return 0; }\n",
	}, "main.c"))

	h.key(tcell.KeyCtrlE)
	tree := h.app.ui.Sidebar.Tree
	// Directories sort first: "sub" should be the initial selection.
	node, ok := tree.Selected()
	if !ok || !node.IsDir || node.Name != "sub" {
		t.Fatalf("initial selection = %+v, want directory 'sub'", node)
	}

	h.key(tcell.KeyRight)
	if !h.screenHas("inner.c") {
		t.Errorf("expanding 'sub' with Right did not reveal inner.c; screen:\n%s", strings.Join(h.draw(), "\n"))
	}

	h.key(tcell.KeyLeft)
	if h.screenHas("inner.c") {
		t.Error("Left on an expanded directory should collapse it")
	}
}

func TestSidebarDefaultsOffInExistingHarness(t *testing.T) {
	// project() and newHarness() never set a.ui.Sidebar.Tree, so the sidebar
	// must stay off and existing layout-sensitive tests keep their assertions
	// valid without any special-casing.
	h := project(t, map[string]string{"main.c": "int main(void){return 0;}\n"}, "main.c")
	if h.app.ui.TextX() != 0 {
		t.Error("sidebar should be off by default when a harness does not wire a tree")
	}
}

func TestSidebarRevealsFileOnGotoDefinition(t *testing.T) {
	h := withSidebar(project(t, map[string]string{
		"main.c":     "int main(void) { return helper(); }\n",
		"deep/a/b.c": "int helper(void)\n{\n\treturn 1;\n}\n",
	}, "main.c"))

	target := filepath.Join(h.app.root, "deep", "a", "b.c")
	if err := h.app.openFile(target); err != nil {
		t.Fatal(err)
	}

	node, ok := h.app.ui.Sidebar.Tree.Selected()
	if !ok || node.Path != target {
		t.Fatalf("selected = %+v, want %q revealed and selected", node, target)
	}
	if !h.screenHas("b.c") {
		t.Errorf("ancestors not expanded; screen:\n%s", strings.Join(h.draw(), "\n"))
	}
}

func TestSidebarHighlightsActiveFile(t *testing.T) {
	h := withSidebar(project(t, map[string]string{
		"a.c": "int a;\n",
		"b.c": "int b;\n",
	}, "a.c"))
	// Start-up reveal is run() → setProject's job; harnesses skip run().
	h.app.ui.Sidebar.Tree.Reveal(filepath.Join(h.app.root, "a.c"))
	h.draw()

	th := h.app.scr.Theme
	_, wantBg, _ := th.Selection.Decompose()
	bgAt := func(name string) tcell.Color {
		y := h.rowOf(name)
		_, _, style, _ := h.sim.GetContent(2, y)
		_, bg, _ := style.Decompose()
		return bg
	}
	if bgAt("a.c") != wantBg {
		t.Error("the active file's row is not highlighted")
	}
	if bgAt("b.c") == wantBg {
		t.Error("an inactive file's row is highlighted")
	}
}

func TestSidebarFollowsBufferCycling(t *testing.T) {
	h := withSidebar(project(t, map[string]string{
		"a.c": "int a;\n",
		"b.c": "int b;\n",
	}, "a.c"))
	if err := h.app.openFile(filepath.Join(h.app.root, "b.c")); err != nil {
		t.Fatal(err)
	}
	h.app.nextBuffer(1)

	node, _ := h.app.ui.Sidebar.Tree.Selected()
	if node == nil || node.Name != "a.c" {
		t.Errorf("after cycling to a.c the tree selects %+v", node)
	}
}
