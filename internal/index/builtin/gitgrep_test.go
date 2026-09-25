package builtin

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"sonarc/internal/index/provider"
)

func TestGitGrepFindsTrackedAndUntrackedSource(t *testing.T) {
	root := gitTree(t, map[string]string{
		"a.c":        "int x;\nvfs_read();\n",
		"sub/b.h":    "vfs_read\n",
		"new.c":      "call vfs_read\n", // untracked
		"notes.rst":  "vfs_read\n",      // not a source type; the scan skips it too
		"sub/Kbuild": "obj-y += vfs_read.o\n",
	}, "a.c", "sub/b.h", "notes.rst", "sub/Kbuild")

	got, err := GitGrep(context.Background(), root, "vfs_read")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"a.c": 2, "new.c": 1, "sub/Kbuild": 1, "sub/b.h": 1}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want hits in %v", got, want)
	}
	for _, l := range got {
		rel, _ := filepath.Rel(root, l.Path)
		if want[filepath.ToSlash(rel)] != l.Line {
			t.Errorf("unexpected hit %s:%d", rel, l.Line)
		}
	}
}

func TestGitGrepNoMatchIsAnEmptyAnswerNotAnError(t *testing.T) {
	root := gitTree(t, map[string]string{"a.c": "int x;\n"}, "a.c")
	got, err := GitGrep(context.Background(), root, "nothing_here")
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v; want no hits and no error", got, err)
	}
}

func TestGitGrepOutsideARepositoryIsNotGit(t *testing.T) {
	root := tree(t, map[string]string{"a.c": "vfs_read\n"})
	if _, err := GitGrep(context.Background(), root, "vfs_read"); !errors.Is(err, ErrNotGit) {
		t.Errorf("err = %v, want ErrNotGit", err)
	}
}

// ':' is legal in file names, and git grep's default output separates fields
// with it; -z keeps such names intact.
func TestGitGrepHandlesColonsInFileNames(t *testing.T) {
	root := gitTree(t, map[string]string{"we:ird.c": "vfs_read\n"}, "we:ird.c")
	got, err := GitGrep(context.Background(), root, "vfs_read")
	if err != nil || len(got) != 1 || filepath.Base(got[0].Path) != "we:ird.c" || got[0].Line != 1 {
		t.Errorf("got %+v, %v", got, err)
	}
}

// A literal search: regex metacharacters are not special.
func TestGitGrepIsLiteral(t *testing.T) {
	root := gitTree(t, map[string]string{"a.c": "a.b\naxb\n"}, "a.c")
	got, _ := GitGrep(context.Background(), root, "a.b")
	if len(got) != 1 || got[0].Line != 1 {
		t.Errorf("got %+v, want only the literal a.b on line 1", got)
	}
}

func TestGitGrepStopsAtTheResultCapAndSaysSo(t *testing.T) {
	root := gitTree(t, map[string]string{"a.c": "x\nx\nx\nx\nx\n"}, "a.c")
	old := maxGitGrepResults
	maxGitGrepResults = 3
	defer func() { maxGitGrepResults = old }()

	got, err := GitGrep(context.Background(), root, "x")
	var pe *provider.PartialError
	if !errors.As(err, &pe) || !errors.Is(err, provider.ErrTooManyResults) {
		t.Fatalf("err = %v, want a partial result for too many matches", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d hits, want the first 3", len(got))
	}
}

func TestGitGrepRespectsCancellation(t *testing.T) {
	root := gitTree(t, map[string]string{"a.c": "x\n"}, "a.c")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := GitGrep(ctx, root, "x"); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// Both searches must cover the same file types, or switching between them would
// silently change what "search the project" means.
func TestGitGrepAndScanCoverTheSameFiles(t *testing.T) {
	files := map[string]string{
		"a.c": "hit\n", "b.py": "hit\n", "c.md": "hit\n", "Makefile": "hit\n",
		"d.rst": "hit\n", "e.bin": "hit\n", "sub/Kconfig": "hit\n",
	}
	var tracked []string
	for f := range files {
		tracked = append(tracked, f)
	}
	root := gitTree(t, files, tracked...)

	viaGit, err := GitGrep(context.Background(), root, "hit")
	if err != nil {
		t.Fatal(err)
	}
	ix := New(root)
	ix.SkipSymbols()
	if err := ix.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	viaScan, _ := ix.Grep(context.Background(), "hit", false)

	a, b := locPaths(t, root, viaGit), locPaths(t, root, viaScan)
	if !equal(a, b) {
		t.Errorf("git grep covered %v, the scan covered %v", a, b)
	}
}

func locPaths(t *testing.T, root string, locs []provider.Location) []string {
	var paths []string
	for _, l := range locs {
		paths = append(paths, l.Path)
	}
	return rels(t, root, paths)
}
