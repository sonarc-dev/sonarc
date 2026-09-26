package main

import (
	"fmt"
	"github.com/sonarc-dev/sonarc/internal/ui"
	"github.com/sonarc-dev/sonarc/internal/vcs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func splitLines(s string) [][]byte {
	if s == "" {
		return nil
	}
	var out [][]byte
	for _, l := range strings.Split(strings.TrimSuffix(s, "\n"), "\n") {
		out = append(out, []byte(l))
	}
	return out
}

// render writes diff rows compactly: kind, old number, new number, text.
func render(lines []ui.DiffLine) string {
	var b strings.Builder
	for _, l := range lines {
		if l.Kind == '~' {
			b.WriteString("~\n")
			continue
		}
		fmt.Fprintf(&b, "%c %d %d %s\n", l.Kind, l.Old, l.New, l.Text)
	}
	return b.String()
}

func TestBuildDiffShowsContextAndGaps(t *testing.T) {
	var old []string
	for i := 1; i <= 20; i++ {
		old = append(old, fmt.Sprint("l", i))
	}
	cur := append([]string(nil), old...)
	cur[9] = "CHANGED"                                              // line 10
	cur = append(cur[:15], append([]string{"NEW"}, cur[15:]...)...) // after line 15
	o, c := splitLines(strings.Join(old, "\n")), splitLines(strings.Join(cur, "\n"))
	got, added, removed := buildDiff(o, c, vcs.Lines(o, c), 3)
	want := `~
  7 7 l7
  8 8 l8
  9 9 l9
- 10 10 l10
+ 0 10 CHANGED
  11 11 l11
  12 12 l12
  13 13 l13
  14 14 l14
  15 15 l15
+ 0 16 NEW
  16 17 l16
  17 18 l17
  18 19 l18
~
`
	if render(got) != want {
		t.Errorf("diff\n%s\nwant\n%s", render(got), want)
	}
	if added != 2 || removed != 1 {
		t.Errorf("+%d -%d, want +2 -1", added, removed)
	}
}

func TestBuildDiffNewAndDeletedFiles(t *testing.T) {
	c := splitLines("a\nb\n")
	got, _, _ := buildDiff(nil, c, vcs.Lines(nil, c), 3)
	if render(got) != "+ 0 1 a\n+ 0 2 b\n" {
		t.Errorf("new file:\n%s", render(got))
	}
	got, _, _ = buildDiff(c, nil, vcs.Lines(c, nil), 3)
	if render(got) != "- 1 1 a\n- 2 1 b\n" {
		t.Errorf("deleted file:\n%s", render(got))
	}
}

// F8 diffs the text in the editor, unsaved edits included, and says so.
func TestF8DiffsUnsavedEdits(t *testing.T) {
	h := gitProject(t, map[string]string{"m.c": "alpha\nbeta\n"}, "m.c")
	h.app.v().Goto(2)
	h.typeText("NEW ")
	h.key(tcell.KeyF8)
	if !h.app.ui.Diff.Open || !strings.Contains(h.app.ui.Diff.Title, "unsaved") {
		t.Fatalf("F8: open=%v title %q", h.app.ui.Diff.Open, h.app.ui.Diff.Title)
	}
	if !h.screenHas("+ NEW beta") {
		t.Errorf("diff lacks the unsaved line:\n%s", h.screen())
	}
}

func TestDeletedFileDiff(t *testing.T) {
	h := gitProject(t, map[string]string{"gone.c": "x\ny\n", "m.c": "m\n"}, "m.c")
	os.Remove(filepath.Join(h.app.root, "gone.c"))
	h.app.refreshGit()
	h.waitGit()
	h.click(5, h.rowWith("D gone.c"))
	if !strings.Contains(h.app.ui.Diff.Title, "deleted") || !h.screenHas("- x") {
		t.Fatalf("deleted diff: %q\n%s", h.app.ui.Diff.Title, h.screen())
	}
	h.app.ui.Sidebar.Focused = false
	h.key(tcell.KeyEnter)
	if !strings.Contains(h.app.ui.Msg, "deleted") || h.curFile() != "m.c" {
		t.Errorf("Enter on a deleted file: msg %q, file %s", h.app.ui.Msg, h.curFile())
	}
}

// Two blocks closer than twice the context share the lines between them:
// each shown once, with no gap row between.
func TestBuildDiffCloseBlocksShareContext(t *testing.T) {
	old := splitLines("a\nb\nc\nd\ne\nf\ng\nh\n")
	cur := splitLines("A\nb\nc\nd\ne\nF\ng\nh\n")
	got, _, _ := buildDiff(old, cur, vcs.Lines(old, cur), 3)
	want := "- 1 1 a\n+ 0 1 A\n  2 2 b\n  3 3 c\n  4 4 d\n  5 5 e\n- 6 6 f\n+ 0 6 F\n  7 7 g\n  8 8 h\n"
	if render(got) != want {
		t.Errorf("diff\n%s\nwant\n%s", render(got), want)
	}
}
