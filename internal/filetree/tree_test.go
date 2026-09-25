package filetree

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeTree writes files (a path -> content map; directories are created
// implicitly) under a temp dir and returns its path.
func makeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func names(rows []Row) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Node.Name)
	}
	return out
}

func TestRootStartsExpandedShowingTopLevel(t *testing.T) {
	root := makeTree(t, map[string]string{
		"main.go":  "package main\n",
		"sub/a.go": "package sub\n",
	})
	tr := New(root)
	rows := tr.Rows()
	if got := names(rows); len(got) != 2 {
		t.Fatalf("Rows() = %v, want 2 top-level entries", got)
	}
	// Directories sort before files.
	if !rows[0].Node.IsDir || rows[0].Node.Name != "sub" {
		t.Errorf("first row = %+v, want dir 'sub' first", rows[0].Node)
	}
	if rows[1].Node.Name != "main.go" {
		t.Errorf("second row = %+v, want 'main.go'", rows[1].Node)
	}
}

func TestSubdirNotReadUntilExpanded(t *testing.T) {
	root := makeTree(t, map[string]string{
		"sub/a.go": "package sub\n",
	})
	tr := New(root)
	rows := tr.Rows()
	if len(rows) != 1 {
		t.Fatalf("Rows() = %v, want just 'sub'", names(rows))
	}
	sub := rows[0].Node
	if sub.loaded {
		t.Error("subdirectory was read before being expanded")
	}

	tr.Toggle(0)
	if !sub.loaded {
		t.Error("Toggle did not load the subdirectory")
	}
	rows = tr.Rows()
	if len(rows) != 2 || rows[1].Node.Name != "a.go" {
		t.Errorf("after expand, Rows() = %v, want ['sub', 'a.go']", names(rows))
	}

	// Collapsing hides children without discarding what was read.
	tr.Toggle(0)
	rows = tr.Rows()
	if len(rows) != 1 {
		t.Errorf("after collapse, Rows() = %v, want just 'sub'", names(rows))
	}
}

func TestGitignoreAndDotDirsExcluded(t *testing.T) {
	// .gitignore itself is a real tracked file, and the tree lists it like
	// any other file — only what .gitignore or the built-in skip list name
	// gets hidden.
	root := makeTree(t, map[string]string{
		".gitignore":        "ignored.go\nbuild/\n",
		"keep.go":           "package p\n",
		"ignored.go":        "package p\n",
		"build/out.go":      "package p\n",
		".git/HEAD":         "ref: refs/heads/main\n",
		"node_modules/x.js": "1\n",
	})
	tr := New(root)
	rows := tr.Rows()
	got := names(rows)
	want := map[string]bool{"keep.go": true, ".gitignore": true}
	for _, n := range got {
		if !want[n] {
			t.Errorf("unexpected entry %q in tree: %v", n, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("Rows() = %v, want exactly %v", got, want)
	}
}

func TestNonSourceFilesAreListed(t *testing.T) {
	root := makeTree(t, map[string]string{
		"README":    "hello\n",
		"style.css": "body {}\n",
		"LICENSE":   "MIT\n",
		"main.c":    "int main(void){return 0;}\n",
	})
	tr := New(root)
	got := names(tr.Rows())
	for _, want := range []string{"README", "style.css", "LICENSE", "main.c"} {
		found := false
		for _, g := range got {
			if g == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("filetree omitted non-source file %q; got %v", want, got)
		}
	}
}

func TestRevealExpandsAncestors(t *testing.T) {
	root := makeTree(t, map[string]string{
		"a/b/c.go":   "package c\n",
		"a/other.go": "package a\n",
	})
	tr := New(root)

	target := filepath.Join(root, "a", "b", "c.go")
	if !tr.Reveal(target) {
		t.Fatal("Reveal returned false for a path under the tree root")
	}

	node, ok := tr.Selected()
	if !ok || node.Path != target {
		t.Fatalf("Selected() = %+v, %v; want %q selected", node, ok, target)
	}

	// Ancestors should now be expanded so the row is actually visible.
	rows := tr.Rows()
	found := false
	for _, r := range rows {
		if r.Node.Path == target {
			found = true
		}
	}
	if !found {
		t.Errorf("revealed path is not present in Rows(): %v", names(rows))
	}
}

func TestRevealOutsideRootReturnsFalse(t *testing.T) {
	root := makeTree(t, map[string]string{"a.go": "package a\n"})
	other := t.TempDir()
	tr := New(root)
	if tr.Reveal(filepath.Join(other, "x.go")) {
		t.Error("Reveal should return false for a path outside the tree root")
	}
}

func TestMoveClampsToVisibleRows(t *testing.T) {
	root := makeTree(t, map[string]string{"a.go": "1\n", "b.go": "2\n"})
	tr := New(root)
	tr.Move(-5, 10)
	if tr.Sel != 0 {
		t.Errorf("Sel = %d, want 0 after moving above the top", tr.Sel)
	}
	tr.Move(50, 10)
	if tr.Sel != len(tr.Rows())-1 {
		t.Errorf("Sel = %d, want %d after moving past the bottom", tr.Sel, len(tr.Rows())-1)
	}
}

// Revealing something the tree does not show must not leave directories
// expanded as a side effect.
func TestRevealMissLeavesTreeUntouched(t *testing.T) {
	root := makeTree(t, map[string]string{
		".gitignore":        "hidden.go\n",
		"pkg/sub/keep.go":   "package sub\n",
		"pkg/sub/hidden.go": "package sub\n",
	})
	tr := New(root)
	before := names(tr.Rows())

	if tr.Reveal(filepath.Join(root, "pkg", "sub", "hidden.go")) {
		t.Fatal("Reveal found a gitignored file")
	}
	after := names(tr.Rows())
	if len(after) != len(before) {
		t.Errorf("failed Reveal changed the tree: before %v, after %v", before, after)
	}
}

func TestRevealEmptyPathIsANoOp(t *testing.T) {
	tr := New(makeTree(t, map[string]string{"a.go": "1\n"}))
	if tr.Reveal("") {
		t.Error("Reveal(\"\") should report false for an unnamed buffer")
	}
}

func TestScrollByClampsAndDoesNotFollowSelection(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 30; i++ {
		files[fmt.Sprintf("f%02d.go", i)] = "x\n"
	}
	tr := New(makeTree(t, files))

	tr.ScrollBy(1000, 10)
	tr.Fit(10)
	if want := 30 - 10; tr.Top != want {
		t.Errorf("Top = %d after scrolling past the end, want %d", tr.Top, want)
	}
	tr.ScrollBy(-1000, 10)
	tr.Fit(10)
	if tr.Top != 0 {
		t.Errorf("Top = %d after scrolling past the start, want 0", tr.Top)
	}
	// Selection is still row 0 and visible here, but scrolling down must not
	// be dragged back to it by Fit.
	tr.ScrollBy(5, 10)
	tr.Fit(10)
	if tr.Top != 5 {
		t.Errorf("Fit undid a deliberate scroll: Top = %d, want 5", tr.Top)
	}
}

func TestMoveThenFitKeepsSelectionVisible(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 30; i++ {
		files[fmt.Sprintf("f%02d.go", i)] = "x\n"
	}
	tr := New(makeTree(t, files))
	tr.Move(25, 10)
	tr.Fit(10)
	if tr.Sel < tr.Top || tr.Sel >= tr.Top+10 {
		t.Errorf("Sel %d not within viewport [%d, %d)", tr.Sel, tr.Top, tr.Top+10)
	}
}

func TestSelectRowRejectsOutOfRange(t *testing.T) {
	tr := New(makeTree(t, map[string]string{"a.go": "1\n", "b.go": "2\n"}))
	if tr.SelectRow(5) || tr.SelectRow(-1) {
		t.Error("SelectRow accepted an out-of-range index")
	}
	if !tr.SelectRow(1) || tr.Sel != 1 {
		t.Errorf("SelectRow(1) failed; Sel = %d", tr.Sel)
	}
}

func TestRefreshPicksUpOutsideChanges(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"a/x.c", "a/y.c", "b/z.c", "top.c"} {
		os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755)
		os.WriteFile(filepath.Join(root, p), nil, 0o644)
	}
	tr := New(root)
	tr.Toggle(0)    // expand a
	tr.SelectRow(2) // a/y.c
	if n, _ := tr.Selected(); n.Name != "y.c" {
		t.Fatalf("selected %s", n.Name)
	}

	os.WriteFile(filepath.Join(root, "a", "new.c"), nil, 0o644) // sorts before y.c
	os.Remove(filepath.Join(root, "top.c"))
	os.WriteFile(filepath.Join(root, "b", "unseen.c"), nil, 0o644)
	tr.Refresh()

	var names []string
	for _, r := range tr.Rows() {
		names = append(names, r.Node.Name)
	}
	if got := strings.Join(names, " "); got != "a new.c x.c y.c b" {
		t.Errorf("rows after refresh: %s", got)
	}
	if n, _ := tr.Selected(); n.Name != "y.c" {
		t.Errorf("selection moved to %s; it should stay on y.c", n.Name)
	}
	// b was never opened, so it was not read: opening it now shows its files.
	tr.Toggle(4)
	if rows := tr.Rows(); len(rows) != 7 {
		t.Errorf("opening b after refresh: %d rows", len(rows))
	}
}

func TestExpandedRoundTrips(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"a/b/c/x.c", "a/y.c", "d/z.c"} {
		os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755)
		os.WriteFile(filepath.Join(root, p), nil, 0o644)
	}
	tr := New(root)
	tr.Expand(filepath.Join(root, "a", "b", "c"))
	tr.Expand(filepath.Join(root, "gone")) // skipped quietly
	want := []string{filepath.Join(root, "a"), filepath.Join(root, "a", "b"), filepath.Join(root, "a", "b", "c")}
	if got := tr.Expanded(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Expanded = %v, want %v", got, want)
	}
	again := New(root)
	for _, p := range tr.Expanded() {
		again.Expand(p)
	}
	if len(again.Rows()) != len(tr.Rows()) {
		t.Errorf("restored tree has %d rows, want %d", len(again.Rows()), len(tr.Rows()))
	}
}
