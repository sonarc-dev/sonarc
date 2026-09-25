package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"sonarc/internal/buffer"
	"sonarc/internal/filetree"
	"sonarc/internal/term"
)

// restart makes a second editor on the same project and state directory, as
// running `sonarc <project>` again would: an empty buffer and the tree.
func restart(t *testing.T, h *harness) *harness {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(100, 30)
	t.Cleanup(sim.Fini)
	a := newApp(term.Wrap(sim), buffer.New())
	a.root = h.app.root
	a.ui.PanelRoot = a.root
	a.ui.Sidebar.Tree = filetree.New(a.root)
	a.statePath = h.app.statePath
	return &harness{t: t, app: a, sim: sim}
}

// time1 moves a file's timestamp back far enough to tell a rewrite apart.
const time1 = time.Hour

func TestSessionReopensTheProjectAsItWasLeft(t *testing.T) {
	src := "l0\nl1\nl2\nl3\nl4\nl5\n"
	h := sidebarHarness(t, map[string]string{"a.c": src, "sub/b.c": src, "c.c": src}, "a.c")
	saving(t, h)
	h.openAll("sub/b.c", "c.c")
	h.app.v().Goto(5) // c.c line 4
	h.app.switchTo(1) // b.c showing
	h.app.v().Goto(3) // line 2
	h.app.saveSession()

	h2 := restart(t, h)
	h2.app.restoreSession()
	if got := h2.openNames(); len(got) != 3 || got[0] != "a.c" || got[1] != "b.c" || got[2] != "c.c" {
		t.Fatalf("reopened %v, want a.c b.c c.c", got)
	}
	if h2.curFile() != "b.c" || h2.app.v().Head.Line != 2 {
		t.Errorf("showing %s line %d, want b.c line 2", h2.curFile(), h2.app.v().Head.Line)
	}
	if v := h2.app.views[2]; v.Head.Line != 4 {
		t.Errorf("c.c cursor on line %d, want 4", v.Head.Line)
	}
	if h2.app.ui.Sidebar.Focused {
		t.Error("with files restored, focus belongs in the text")
	}
	if !h2.screenHas("sub") || !h2.screenHas("b.c") {
		t.Error("the tree was not reopened down to b.c")
	}
}

func TestSessionSkipsFilesThatAreGone(t *testing.T) {
	h := sidebarHarness(t, map[string]string{"a.c": "a\n", "b.c": "b\n"}, "a.c")
	saving(t, h)
	h.openAll("b.c")
	h.app.saveSession()
	os.Remove(filepath.Join(h.app.root, "b.c"))

	h2 := restart(t, h)
	h2.app.restoreSession()
	if got := h2.openNames(); len(got) != 1 || got[0] != "a.c" {
		t.Errorf("reopened %v, want just a.c", got)
	}
}

// Any file opened again, in any project, starts where it was left.
func TestReopenedFileReturnsToItsLastPosition(t *testing.T) {
	src := "l0\nl1\nl2\nl3\nl4\nl5\n"
	h := sidebarHarness(t, map[string]string{"a.c": "", "b.c": src}, "a.c")
	saving(t, h)
	h.openAll("b.c")
	h.app.v().Goto(5)
	h.key(tcell.KeyCtrlW) // closing remembers the place

	h.openAll("b.c")
	if h.app.v().Head.Line != 4 {
		t.Errorf("reopened b.c on line %d, want 4", h.app.v().Head.Line)
	}
}

// The session file is rewritten only when something in it changed, since it
// is saved every few seconds.
func TestSessionIsNotRewrittenWhenUnchanged(t *testing.T) {
	h := sidebarHarness(t, map[string]string{"a.c": "a\nb\n"}, "a.c")
	saving(t, h)
	h.app.saveSession()
	path := h.app.sessionPath()
	fi1, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	os.Chtimes(path, fi1.ModTime().Add(-time1), fi1.ModTime().Add(-time1))
	fi1, _ = os.Stat(path)
	h.app.saveSession()
	if fi2, _ := os.Stat(path); !fi2.ModTime().Equal(fi1.ModTime()) {
		t.Error("an unchanged session was rewritten")
	}
	h.app.v().Goto(2)
	h.app.saveSession()
	if fi3, _ := os.Stat(path); fi3.ModTime().Equal(fi1.ModTime()) {
		t.Error("a moved cursor was not saved")
	}
}
