package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// rewrite changes a file the way another program would, with a distinct
// modification time so the change is visible even on coarse filesystems.
func rewrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Duration(len(content)+1) * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

func TestUneditedFileReloadsWhenChangedOnDisk(t *testing.T) {
	h := project(t, map[string]string{"a.c": "one\ntwo\nthree\n"}, "a.c")
	path := filepath.Join(h.app.root, "a.c")
	h.app.v().Goto(3)

	rewrite(t, path, "one\ntwo\nthree\nfour\n")
	h.app.checkDisk()
	if h.text() != "one\ntwo\nthree\nfour" {
		t.Fatalf("buffer not reloaded: %q", h.text())
	}
	if h.app.v().Head.Line != 2 {
		t.Errorf("cursor moved to line %d on reload, want it kept on 2", h.app.v().Head.Line)
	}
	if !strings.Contains(h.app.ui.Msg, "reloaded") {
		t.Errorf("message %q", h.app.ui.Msg)
	}

	// Shrinking the file under the cursor keeps the cursor valid.
	rewrite(t, path, "x\n")
	h.app.checkDisk()
	if h.app.v().Head.Line > 1 {
		t.Errorf("cursor at line %d past the end of a 1-line file", h.app.v().Head.Line)
	}
	h.typeText("y") // and editing works on the reloaded text
	if h.app.v().Buf.Modified() == false {
		t.Error("typing after a reload did not register")
	}
}

// Edits are never thrown away by a background check: the user is told once,
// and saving asks before overwriting the other program's version.
func TestEditedFileIsNotReloadedAndSaveAsks(t *testing.T) {
	h := project(t, map[string]string{"a.c": "one\n"}, "a.c")
	path := filepath.Join(h.app.root, "a.c")
	h.typeText("MINE ")

	rewrite(t, path, "theirs\n")
	h.app.checkDisk()
	if !strings.HasPrefix(h.text(), "MINE ") {
		t.Fatalf("unsaved edits lost: %q", h.text())
	}
	if !strings.Contains(h.app.ui.Msg, "changed on disk") {
		t.Errorf("no warning: %q", h.app.ui.Msg)
	}
	h.app.ui.Msg = ""
	h.app.checkDisk()
	if h.app.ui.Msg != "" {
		t.Errorf("warned twice about the same change: %q", h.app.ui.Msg)
	}

	h.answer("n")
	h.key(tcell.KeyCtrlS)
	if data, _ := os.ReadFile(path); string(data) != "theirs\n" {
		t.Fatalf("declined overwrite still wrote: %q", data)
	}

	h.answer("y")
	h.key(tcell.KeyCtrlS)
	if data, _ := os.ReadFile(path); string(data) != "MINE one\n" {
		t.Errorf("confirmed overwrite wrote %q", data)
	}
	// Having saved, the next save needs no confirmation.
	h.typeText("!")
	h.key(tcell.KeyCtrlS)
	if data, _ := os.ReadFile(path); string(data) != "MINE !one\n" {
		t.Errorf("plain save after an overwrite wrote %q", data)
	}
}

func TestReloadCommandDiscardsEditsAfterAsking(t *testing.T) {
	h := project(t, map[string]string{"a.c": "disk\n"}, "a.c")
	h.typeText("edit ")
	h.answer("n")
	h.key(tcell.KeyCtrlK)
	h.typeText("!")
	if !strings.HasPrefix(h.text(), "edit ") {
		t.Fatal("declined reload discarded the edits")
	}
	h.answer("y")
	h.key(tcell.KeyCtrlK)
	h.typeText("!")
	if h.text() != "disk" || h.app.v().Buf.Modified() {
		t.Errorf("after reload: %q modified=%v", h.text(), h.app.v().Buf.Modified())
	}
}

func TestDeletedFileIsReportedNotEmptied(t *testing.T) {
	h := project(t, map[string]string{"a.c": "keep me\n"}, "a.c")
	if err := os.Remove(filepath.Join(h.app.root, "a.c")); err != nil {
		t.Fatal(err)
	}
	h.app.checkDisk()
	if h.text() != "keep me" {
		t.Errorf("buffer changed after the file was deleted: %q", h.text())
	}
	if !strings.Contains(h.app.ui.Msg, "deleted") {
		t.Errorf("message %q", h.app.ui.Msg)
	}
}
