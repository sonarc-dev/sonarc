package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/sonarc-dev/sonarc/internal/buffer"
	"github.com/sonarc-dev/sonarc/internal/search"
	"github.com/sonarc-dev/sonarc/internal/ui"
)

func splitProject(t *testing.T) *harness {
	t.Helper()
	h := project(t, map[string]string{
		"main.c":  "int main(void)\n{\n\treturn helper();\n}\n",
		"util.c":  "int helper(void)\n{\n\treturn 42;\n}\n",
		"notes.c": "one\ntwo\nthree\nfour\nfive\n",
	}, "main.c")
	h.sim.SetSize(120, 30)
	return h
}

func chord(h *harness, r rune) {
	h.key(tcell.KeyCtrlK)
	h.typeText(string(r))
}

// Ctrl+K 3 shows the file twice, side by side, with a title on each pane and
// the keyboard in the new one.
func TestSplitShowsTheFileTwice(t *testing.T) {
	h := splitProject(t)
	first := h.app.v()
	chord(h, '3')
	scr := h.draw()
	if h.app.ui.Panes() != 2 {
		t.Fatalf("panes = %d, want 2", h.app.ui.Panes())
	}
	if n := strings.Count(scr[0], "main.c"); n != 2 {
		t.Errorf("title row shows main.c %d times, want once per pane:\n%s", n, scr[0])
	}
	if strings.Count(h.screen(), "return helper();") != 2 {
		t.Errorf("the file is not shown in both panes:\n%s", h.screen())
	}
	if h.app.v() == first || h.app.v().Buf != first.Buf || h.app.ui.Other != first {
		t.Error("the new pane should be a second view of the same buffer, with the keyboard")
	}
}

// Typing in one pane shows in the other at once, and the other pane's cursor
// stays on the text it was on.
func TestEditsShowInBothPanes(t *testing.T) {
	h := splitProject(t)
	chord(h, '3')
	other := h.app.ui.Other
	other.SetCursor(buffer.Pos{Line: 2, Col: 1}) // on "return helper();"
	h.typeText("// top\n")
	scr := h.screen()
	if strings.Count(scr, "// top") != 2 {
		t.Errorf("the edit is not in both panes:\n%s", scr)
	}
	h.draw()
	if got := string(other.Buf.Line(other.Head.Line)); got != "\treturn helper();" {
		t.Errorf("the other pane's cursor moved off its text, onto %q", got)
	}
}

// F9 moves the keyboard; opening a file changes only the focused pane.
func TestEachPaneShowsItsOwnFile(t *testing.T) {
	h := splitProject(t)
	chord(h, '3')
	if err := h.app.openFile(filepath.Join(h.app.root, "util.c")); err != nil {
		t.Fatal(err)
	}
	scr := h.draw()
	if !strings.Contains(scr[0], "util.c") || !strings.Contains(scr[0], "main.c") {
		t.Fatalf("titles = %q, want main.c and util.c", scr[0])
	}
	h.key(tcell.KeyF9)
	if !strings.HasSuffix(h.app.v().Buf.Path(), "main.c") || !strings.HasSuffix(h.app.fileView().Buf.Path(), "main.c") {
		t.Errorf("F9 left the keyboard on %s", h.app.v().Buf.Path())
	}
	h.key(tcell.KeyF9)
	if !strings.HasSuffix(h.app.v().Buf.Path(), "util.c") {
		t.Errorf("F9 again should come back to util.c, not %s", h.app.v().Buf.Path())
	}
}

// Going to the file the other pane shows gives this pane its own view of it,
// so the two keep separate cursors; open files stay one entry per file.
func TestTheSameFileInBothPanesKeepsTwoCursors(t *testing.T) {
	h := splitProject(t)
	chord(h, '3')
	h.app.openFile(filepath.Join(h.app.root, "util.c"))
	h.key(tcell.KeyF9) // main.c's own view, on the left
	h.key(tcell.KeyF9) // back to util.c
	h.app.openFile(filepath.Join(h.app.root, "main.c"))
	if h.app.v() == h.app.ui.Other || h.app.v().Buf != h.app.ui.Other.Buf {
		t.Fatal("both panes should show main.c through different views")
	}
	if n := len(h.app.views); n != 2 {
		t.Errorf("open files = %d, want 2 (main.c and util.c)", n)
	}
}

// Ctrl+K 1 keeps the pane with the keyboard; a mirror's place carries over to
// the file's own view.
func TestOnlyPaneKeepsTheFocusedPlace(t *testing.T) {
	h := splitProject(t)
	chord(h, '3')
	h.app.v().SetCursor(buffer.Pos{Line: 3, Col: 0})
	chord(h, '1')
	h.draw()
	if h.app.ui.Other != nil || h.app.ui.Panes() != 1 {
		t.Fatalf("still split after Ctrl+K 1: %d panes", h.app.ui.Panes())
	}
	if !h.app.isFileView(h.app.v()) {
		t.Fatal("the remaining pane should be the file's own view")
	}
	if h.app.v().Head.Line != 3 {
		t.Errorf("cursor on line %d, want 4 where the mirror was", h.app.v().Head.Line+1)
	}
}

// Closing the file the other pane shows closes that pane.
func TestClosingTheOtherPanesFileUnsplits(t *testing.T) {
	h := splitProject(t)
	chord(h, '3')
	h.app.openFile(filepath.Join(h.app.root, "util.c"))
	h.key(tcell.KeyF9)
	h.key(tcell.KeyCtrlW) // close main.c, which is also... only in this pane
	if h.app.ui.Other == nil {
		t.Fatal("closing this pane's file should keep the other pane")
	}
	h.key(tcell.KeyF9)
	chord(h, '3')
	// Both panes on util.c; closing it leaves one pane.
	h.key(tcell.KeyCtrlW)
	if h.app.ui.Other != nil {
		t.Error("the other pane still shows a closed file")
	}
}

// A reload from disk moves both views of the file to the new text.
func TestReloadMovesBothPanes(t *testing.T) {
	h := splitProject(t)
	chord(h, '3')
	path := h.app.v().Buf.Path()
	if err := os.WriteFile(path, []byte("rewritten\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.app.checkDisk()
	if h.app.v().Buf != h.app.ui.Other.Buf {
		t.Fatal("the panes no longer share a buffer after the reload")
	}
	if strings.Count(h.screen(), "rewritten") != 2 {
		t.Errorf("both panes should show the reloaded text:\n%s", h.screen())
	}
}

// A click in the other pane moves the keyboard there and puts the cursor
// where it landed; the wheel scrolls the pane under the pointer.
func TestMouseInTheOtherPane(t *testing.T) {
	h := splitProject(t)
	chord(h, '3')
	h.draw()
	h.click(10, 3) // left pane, second text row below the title
	if h.app.v() != h.app.fileView() || h.app.ui.SecondFocused {
		t.Fatal("the click did not move the keyboard to the left pane")
	}
	if h.app.v().Head.Line != 2 {
		t.Errorf("cursor on line %d, want 3", h.app.v().Head.Line+1)
	}
}

func TestSplitFallsBackOnSmallTerminals(t *testing.T) {
	h := splitProject(t)
	chord(h, '3')
	h.sim.SetSize(60, 30) // too narrow for two 30-column panes
	h.draw()
	if h.app.ui.Panes() != 2 || strings.Count(h.draw()[0], "main.c") != 1 {
		t.Errorf("a narrow split should stack the panes:\n%s", h.screen())
	}
	h.sim.SetSize(60, 8) // too short to stack
	h.draw()
	if h.app.ui.Panes() != 1 {
		t.Errorf("panes = %d on an 8-row terminal, want 1", h.app.ui.Panes())
	}
}

// Search highlights and the diff belong to the pane with the keyboard; the
// other pane keeps showing plain text.
func TestDiffAndSearchStayInTheFocusedPane(t *testing.T) {
	h := splitProject(t)
	chord(h, '2') // main.c above, its mirror below with the keyboard
	h.app.ui.Matches = []search.Match{{From: buffer.Pos{Line: 2, Col: 8}, To: buffer.Pos{Line: 2, Col: 14}}}
	scr := h.draw()
	cells, w, _ := h.sim.GetContents()
	matchStyle := h.app.scr.Theme.Match
	styled := func(y int) bool {
		x := strings.Index(scr[y], "helper")
		return x >= 0 && cells[y*w+x].Style == matchStyle
	}
	top, bottom := h.rowWith("return helper"), -1
	for y := top + 1; y < len(scr); y++ {
		if strings.Contains(scr[y], "return helper") {
			bottom = y
		}
	}
	if top < 0 || bottom < 0 {
		t.Fatalf("the line is not in both panes:\n%s", h.screen())
	}
	if styled(top) || !styled(bottom) {
		t.Errorf("search highlight: top pane %v, bottom pane %v; want only the focused bottom one", styled(top), styled(bottom))
	}

	h.app.ui.Matches = nil
	if h.app.ui.Split != ui.SplitBelow {
		t.Errorf("split = %v, want below", h.app.ui.Split)
	}
}

// Quitting with the keyboard in a mirror remembers where the mirror was.
func TestSessionKeepsTheMirrorsPlace(t *testing.T) {
	h := splitProject(t)
	saving(t, h)
	chord(h, '3')
	h.app.v().SetCursor(buffer.Pos{Line: 3, Col: 0})
	h.app.dropOther()
	h.app.saveSession()
	var s session
	if !readJSON(h.app.sessionPath(), &s) || len(s.Files) == 0 || s.Files[s.Active].Line != 3 {
		t.Errorf("session = %+v, want the active file at line 4", s)
	}
}
