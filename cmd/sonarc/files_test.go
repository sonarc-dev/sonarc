package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestCtrlWClosesTheFileAndShowsItsNeighbor(t *testing.T) {
	h := project(t, map[string]string{"a.c": "a\n", "b.c": "b\n", "c.c": "c\n"}, "a.c")
	h.openAll("b.c", "c.c")
	h.app.switchTo(1) // b.c

	h.key(tcell.KeyCtrlW)
	if got := strings.Join(h.openNames(), ","); got != "a.c,c.c" {
		t.Fatalf("open files after closing b.c: %s", got)
	}
	if h.curFile() != "c.c" {
		t.Errorf("showing %s after the close, want its neighbor c.c", h.curFile())
	}

	// Closing everything leaves an empty buffer, not a crash or an exit.
	h.key(tcell.KeyCtrlW)
	h.key(tcell.KeyCtrlW)
	if len(h.app.views) != 1 || h.app.v().Buf.Path() != "" || h.app.quit {
		t.Errorf("after closing the last file: %v, quit=%v", h.openNames(), h.app.quit)
	}
}

func TestClosingAnUnsavedFileAsksFirst(t *testing.T) {
	h := project(t, map[string]string{"a.c": "a\n", "b.c": "b\n"}, "a.c")
	h.openAll("b.c")
	h.typeText("X")

	h.answer("c")
	h.key(tcell.KeyCtrlW)
	if len(h.app.views) != 2 || !h.app.v().Buf.Modified() {
		t.Fatal("cancel closed the file or lost the edit")
	}

	h.answer("s")
	h.key(tcell.KeyCtrlW)
	if len(h.app.views) != 1 {
		t.Fatalf("save-and-close left %v open", h.openNames())
	}
	data, _ := os.ReadFile(filepath.Join(h.app.root, "b.c"))
	if string(data) != "Xb\n" {
		t.Errorf("b.c on disk = %q, want the edit saved", data)
	}

	h.openAll("b.c")
	h.typeText("Y")
	h.answer("d")
	h.key(tcell.KeyCtrlW)
	data, _ = os.ReadFile(filepath.Join(h.app.root, "b.c"))
	if len(h.app.views) != 1 || string(data) != "Xb\n" {
		t.Errorf("discard: open %v, disk %q", h.openNames(), data)
	}
}

func TestCloseOthersKeepsOnlyTheCurrentFile(t *testing.T) {
	h := project(t, map[string]string{"a.c": "a\n", "b.c": "b\n", "c.c": "c\n"}, "a.c")
	h.openAll("b.c", "c.c")
	h.app.switchTo(1)
	h.key(tcell.KeyCtrlK)
	h.typeText("z")
	if got := strings.Join(h.openNames(), ","); got != "b.c" || h.curFile() != "b.c" {
		t.Errorf("after close-others: %s showing %s", got, h.curFile())
	}
}

func TestOpenFilesListSwitchesFiles(t *testing.T) {
	h := project(t, map[string]string{"a.c": "a\n", "b.c": "b\n"}, "a.c")
	h.openAll("b.c")
	h.typeText("Z") // b.c now unsaved

	h.key(tcell.KeyCtrlK)
	h.typeText("u")
	if !h.app.ui.Picker.Open {
		t.Fatal("Ctrl+K u did not open the list")
	}
	if !h.screenHas("b.c ●") {
		t.Errorf("unsaved file not marked:\n%s", strings.Join(h.draw(), "\n"))
	}
	h.typeText("a.c")
	h.key(tcell.KeyEnter)
	if h.curFile() != "a.c" {
		t.Errorf("picked a.c, showing %s", h.curFile())
	}
}

// Opening the same file twice must reuse its buffer, or edits would be split
// across two copies and one set silently lost on save.
func TestOpenFileReusesBuffers(t *testing.T) {
	h := project(t, map[string]string{
		"main.c": mainC,
		"util.c": utilC,
	}, "main.c")

	util := filepath.Join(h.app.root, "util.c")
	if err := h.app.openFile(util); err != nil {
		t.Fatal(err)
	}
	n := len(h.app.views)
	if err := h.app.openFile(util); err != nil {
		t.Fatal(err)
	}
	if len(h.app.views) != n {
		t.Errorf("reopening a file created a second buffer: %d -> %d", n, len(h.app.views))
	}
}
