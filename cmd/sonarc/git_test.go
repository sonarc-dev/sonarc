package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/sonarc-dev/sonarc/internal/vcs"
)

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitProject is a project harness whose files are committed to a fresh
// repository, with the sidebar shown and git followed as run() would.
func gitProject(t *testing.T, files map[string]string, open string) *harness {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	h := sidebarHarness(t, files, open)
	gitRun(t, h.app.root, "init", "-q")
	gitRun(t, h.app.root, "add", "-A")
	gitRun(t, h.app.root, "commit", "-q", "-m", "init")
	r := vcs.Find(context.Background(), h.app.root)
	if r == nil {
		t.Fatal("no repository found")
	}
	r.Top = logicalTop(h.app.root, r.Top)
	h.app.setRepo(r)
	h.waitGit()
	return h
}

// waitGit lets background git work finish and applies its results, as the
// event loop would.
func (h *harness) waitGit() {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		h.app.pump()
		h.app.hunksFor(h.app.v()) // starts loading the base if needed
		h.app.pump()
		b := h.app.git.bases[h.app.v().Buf.Path()]
		if !h.app.git.refreshing && (b == nil || !b.loading) && h.app.git.changes != nil {
			if b != nil || h.app.v().Buf.Path() == "" {
				return
			}
		}
		if time.Now().After(deadline) {
			h.t.Fatal("git work did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// gutterMark returns the mark drawn beside a buffer line (0-based) and its
// style, or a space.
func (h *harness) gutterMark(line int) (rune, tcell.Style) {
	h.draw()
	x := h.app.ui.TextX() + h.app.ui.GutterWidth() - 1
	r, _, st, _ := h.sim.GetContent(x, line-h.app.v().Top)
	return r, st
}

func TestGutterMarksAddedChangedAndDeletedLines(t *testing.T) {
	h := gitProject(t, map[string]string{"m.c": "one\ntwo\nthree\nfour\nfive\n"}, "m.c")
	th := h.app.scr.Theme
	if r, _ := h.gutterMark(0); r != ' ' {
		t.Fatalf("an unchanged file has mark %q", r)
	}

	h.typeText("1") // line 0 changed
	h.app.v().Goto(3)
	h.key(tcell.KeyEnd)
	h.key(tcell.KeyEnter)
	h.typeText("added") // new line 3
	h.app.v().Goto(6)   // "five"
	h.app.v().DeleteLine()

	for _, c := range []struct {
		line  int
		r     rune
		style tcell.Style
	}{
		{0, '▎', th.GitModified},
		{3, '▎', th.GitAdded},
		{4, '▁', th.GitDeleted}, // "four", with "five" removed after it
		{1, ' ', tcell.StyleDefault},
	} {
		r, st := h.gutterMark(c.line)
		if r != c.r || (c.r != ' ' && st != c.style) {
			t.Errorf("line %d: mark %q, want %q", c.line, r, c.r)
		}
	}

	// Undoing everything clears the marks: they follow the text, not saves.
	for h.app.v().Buf.Modified() {
		h.key(tcell.KeyCtrlZ)
	}
	for l := 0; l < 5; l++ {
		if r, _ := h.gutterMark(l); r != ' ' {
			t.Errorf("after undo, line %d still marked %q", l, r)
		}
	}
}

func TestTreeBadgesChangedFiles(t *testing.T) {
	h := gitProject(t, map[string]string{"m.c": "1\n", "sub/s.c": "2\n", "same.c": "3\n"}, "m.c")
	os.WriteFile(filepath.Join(h.app.root, "m.c"), []byte("changed\n"), 0o644)
	os.WriteFile(filepath.Join(h.app.root, "new.c"), []byte("x\n"), 0o644)
	os.WriteFile(filepath.Join(h.app.root, "sub", "s.c"), []byte("changed\n"), 0o644)
	h.app.checkDisk() // as the two-second tick would: rereads the tree, polls git
	h.app.refreshGit()
	h.waitGit()

	w := h.app.ui.TextX()
	badge := func(name string) string {
		row := []rune(h.draw()[h.rowOf(name)]) // ▸ and • are several bytes
		return strings.TrimSpace(string(row[w-3 : w-1]))
	}
	for name, want := range map[string]string{"m.c": "M", "new.c": "?", "sub": "•", "same.c": ""} {
		if got := badge(name); got != want {
			t.Errorf("%s: badge %q, want %q", name, got, want)
		}
	}
}

func TestNextAndPrevChangeWrap(t *testing.T) {
	src := strings.Repeat("x\n", 30)
	h := gitProject(t, map[string]string{"m.c": src}, "m.c")
	for _, l := range []int{5, 20} {
		h.app.v().Goto(l + 1)
		h.typeText("!")
	}
	h.app.v().Goto(1)
	var seen []int
	for i := 0; i < 3; i++ {
		h.key(tcell.KeyDown, tcell.ModAlt)
		seen = append(seen, h.app.v().Head.Line)
	}
	if fmt.Sprint(seen) != "[5 20 5]" {
		t.Errorf("Alt+Down visited %v, want 5 20 then wrap to 5", seen)
	}
	h.key(tcell.KeyUp, tcell.ModAlt)
	if h.app.v().Head.Line != 20 {
		t.Errorf("Alt+Up from 5 went to %d, want to wrap to 20", h.app.v().Head.Line)
	}
}

// A commit made in another terminal is noticed: the marks and badges clear
// without the user doing anything.
func TestCommitElsewhereClearsTheMarks(t *testing.T) {
	h := gitProject(t, map[string]string{"m.c": "one\n"}, "m.c")
	h.typeText("X")
	h.key(tcell.KeyCtrlS)
	h.waitGit()
	if r, _ := h.gutterMark(0); r != '▎' {
		t.Fatalf("saved change not marked: %q", r)
	}
	time.Sleep(20 * time.Millisecond) // a distinct mtime for git's files
	gitRun(t, h.app.root, "commit", "-qam", "two")

	h.app.checkDisk()
	h.waitGit()
	if r, _ := h.gutterMark(0); r != ' ' {
		t.Errorf("after the commit, line 0 still marked %q", r)
	}
	if len(h.app.git.changes) != 0 {
		t.Errorf("changes after the commit: %v", h.app.git.changes)
	}
}

// Git reports paths with symlinks resolved; the editor's own are as typed.
func TestLogicalTopFollowsTheTypedPath(t *testing.T) {
	real := t.TempDir()
	real, _ = filepath.EvalSymlinks(real)
	os.MkdirAll(filepath.Join(real, "repo", "sub"), 0o755)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Join(real, "repo"), link); err != nil {
		t.Skip(err)
	}
	if got := logicalTop(filepath.Join(link, "sub"), filepath.Join(real, "repo")); got != link {
		t.Errorf("logicalTop = %s, want %s", got, link)
	}
	if got := logicalTop(link, filepath.Join(real, "repo")); got != link {
		t.Errorf("at the top: %s, want %s", got, link)
	}
}

// startGit, as run() calls it, finds the repository in the background and
// names its top the way the editor names the project, symlinks and all (the
// macOS temp directory is itself behind one).
func TestStartGitFindsTheRepositoryUnderTheTypedPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	h := sidebarHarness(t, map[string]string{"m.c": "1\n"}, "m.c")
	gitRun(t, h.app.root, "init", "-q")
	h.app.startGit()
	deadline := time.Now().Add(10 * time.Second)
	for h.app.git.repo == nil {
		if time.Now().After(deadline) {
			t.Fatal("repository not found")
		}
		time.Sleep(5 * time.Millisecond)
		h.app.pump()
	}
	if h.app.git.repo.Top != h.app.root {
		t.Errorf("repository top %s, want the project root as typed, %s", h.app.git.repo.Top, h.app.root)
	}
}
