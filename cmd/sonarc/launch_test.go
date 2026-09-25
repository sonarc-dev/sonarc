package main

import (
	"os"
	"path/filepath"
	"testing"

	"sonarc/internal/buffer"
)

// mkProject builds a directory tree with a .git marker at its top.
func mkProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range []string{
		".git/HEAD",
		"main.c",
		"fs/namei.c",
		"fs/Makefile", // a build file below the project root must not win
	} {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// t.TempDir may sit behind a symlink (macOS /var → /private/var);
	// filepath.Abs does not resolve those, so neither do we.
	return root
}

func TestParseLaunchSeparatesDirectoriesFromFiles(t *testing.T) {
	root := mkProject(t)
	l := parseLaunch([]string{filepath.Join(root, "main.c"), root, filepath.Join(root, "new.c")})

	if l.dir != root {
		t.Errorf("dir = %q, want %q", l.dir, root)
	}
	if len(l.files) != 2 {
		t.Fatalf("files = %v, want the two non-directory arguments", l.files)
	}
	// A path that does not exist yet is a file to create, not an error.
	if l.files[1] != filepath.Join(root, "new.c") {
		t.Errorf("files[1] = %q, want the not-yet-existing new.c", l.files[1])
	}
}

func TestParseLaunchResolvesDot(t *testing.T) {
	root := mkProject(t)
	old, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	l := parseLaunch([]string{"."})
	if !filepath.IsAbs(l.dir) {
		t.Errorf("dir = %q, want an absolute path", l.dir)
	}
	if len(l.files) != 0 {
		t.Errorf("files = %v, want none for a lone directory", l.files)
	}
}

func TestParseLaunchIgnoresExtraDirectories(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	if l := parseLaunch([]string{a, b}); l.dir != a {
		t.Errorf("dir = %q, want the first directory %q", l.dir, a)
	}
}

func TestParseLaunchWithNoArguments(t *testing.T) {
	l := parseLaunch(nil)
	if l.dir != "" || len(l.files) != 0 {
		t.Errorf("parseLaunch(nil) = %+v, want zero value", l)
	}
}

// The tree shows the directory that was named, while the project root — where
// indexes and search live — is found by walking up from it.
func TestSubdirectoryKeepsProjectRootButBrowsesNamedDir(t *testing.T) {
	root := mkProject(t)
	sub := filepath.Join(root, "fs")

	h := project(t, map[string]string{"x.c": "int x;\n"}, "x.c")
	h.app.setProject(parseLaunch([]string{sub}), buffer.New())

	if h.app.root != root {
		t.Errorf("root = %q, want project root %q (not the named subdirectory)", h.app.root, root)
	}
	if got := h.app.ui.Sidebar.Tree.Root(); got != sub {
		t.Errorf("tree root = %q, want the named directory %q", got, sub)
	}
}

func TestFolderOnlyStartsFocusedInPinnedSidebar(t *testing.T) {
	root := mkProject(t)
	h := project(t, map[string]string{"x.c": "int x;\n"}, "x.c")
	h.app.setProject(parseLaunch([]string{root}), buffer.New())

	sb := h.app.ui.Sidebar
	if !sb.Focused {
		t.Error("a folder with no file should start with the sidebar focused")
	}
	if !sb.Pinned {
		t.Error("focused sidebar must be pinned so a narrow terminal cannot hide it")
	}
}

func TestFileArgumentKeepsFocusInText(t *testing.T) {
	root := mkProject(t)
	file := filepath.Join(root, "main.c")
	buf, err := buffer.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	h := project(t, map[string]string{"x.c": "int x;\n"}, "x.c")
	h.app.setProject(parseLaunch([]string{file}), buf)

	if h.app.ui.Sidebar.Focused {
		t.Error("opening a file should leave focus in the text")
	}
	if h.app.root != root {
		t.Errorf("root = %q, want %q", h.app.root, root)
	}
	if got := h.app.ui.Sidebar.Tree.Root(); got != root {
		t.Errorf("tree root = %q, want the project root %q when a file is opened", got, root)
	}
}

// A file argument alongside a directory still opens as a buffer, and the
// focus stays in the text because there is something to edit.
func TestDirectoryPlusFileKeepsTextFocus(t *testing.T) {
	root := mkProject(t)
	file := filepath.Join(root, "main.c")
	buf, err := buffer.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	h := project(t, map[string]string{"x.c": "int x;\n"}, "x.c")
	h.app.setProject(parseLaunch([]string{root, file}), buf)

	if h.app.ui.Sidebar.Focused {
		t.Error("with a file open, focus should stay in the text")
	}
}

// projectRoot must handle an unnamed buffer without panicking, and a path
// inside a project must resolve to the project's top.
func TestProjectRootFromFileAndFromDirAgree(t *testing.T) {
	root := mkProject(t)
	if got := projectRoot(filepath.Join(root, "fs", "namei.c")); got != root {
		t.Errorf("projectRoot(file) = %q, want %q", got, root)
	}
	if got := projectRootFrom(filepath.Join(root, "fs")); got != root {
		t.Errorf("projectRootFrom(dir) = %q, want %q", got, root)
	}
	if got := projectRoot(""); got == "" {
		t.Error("projectRoot(\"\") must fall back to the working directory")
	}
}

// "file:line" and "file:line:col", as compilers and grep print them, open the
// file there; a path that really contains the colon is still taken as written.
func TestFileLineArguments(t *testing.T) {
	root := t.TempDir()
	odd := filepath.Join(root, "odd:12")
	if err := os.WriteFile(odd, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(root, "main.c")
	for _, tc := range []struct {
		arg  string
		path string
		at   filePos
	}{
		{f, f, filePos{}},
		{f + ":42", f, filePos{line: 42}},
		{f + ":42:7", f, filePos{line: 42, col: 7}},
		{f + ":42:", f, filePos{line: 42}},
		{f + ":42:7:", f, filePos{line: 42, col: 7}},
		{odd, odd, filePos{}},
		{f + ":x", f + ":x", filePos{}},
		{f + ":0", f + ":0", filePos{}},
	} {
		l := parseLaunch([]string{tc.arg})
		if len(l.files) != 1 || l.files[0] != tc.path || l.at[0] != tc.at {
			t.Errorf("%q: got %v %v, want %q %v", tc.arg, l.files, l.at, tc.path, tc.at)
		}
	}
}

func TestPlaceGoesToLineAndColumn(t *testing.T) {
	h := newHarness(t, "one\ntwo\nthree three\n")
	place(h.app.v(), filePos{line: 3, col: 7})
	if p := h.app.v().Head; p.Line != 2 || p.Col != 6 {
		t.Errorf("cursor at %+v, want line 2 col 6", p)
	}
	place(h.app.v(), filePos{line: 99, col: 99})
	if p := h.app.v().Head; p.Line != 2 {
		t.Errorf("past the end: cursor at %+v, want the last line", p)
	}
}

func TestProjectRootDetection(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "src", "sub")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(deep, "a.c")
	if err := os.WriteFile(file, []byte("int x;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := projectRoot(file)
	wantResolved, _ := filepath.EvalSymlinks(root)
	gotResolved, _ := filepath.EvalSymlinks(got)
	if gotResolved != wantResolved {
		t.Errorf("projectRoot = %q, want %q", gotResolved, wantResolved)
	}
}

// A C project puts a Makefile in nearly every directory, so treating one as a
// root marker stopped the walk at fs/ in a real kernel tree and quietly
// narrowed the built-in index and project search to that one subdirectory.
// Found by running against a 6.8 kernel tree, not by this suite.
func TestProjectRootPrefersVCSOverSubdirectoryMakefile(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "fs")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{".git", "Makefile", "tags"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The subdirectory carries its own Makefile, exactly as the kernel does.
	if err := os.WriteFile(filepath.Join(sub, "Makefile"), []byte("obj-y :=\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(sub, "read_write.c")
	if err := os.WriteFile(file, []byte("int x;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, _ := filepath.EvalSymlinks(projectRoot(file))
	want, _ := filepath.EvalSymlinks(root)
	if got != want {
		t.Errorf("projectRoot = %q, want %q (stopped at the subdirectory Makefile)", got, want)
	}
}

// With nothing stronger anywhere above it, a build file is still the best
// signal available and should anchor the root rather than being discarded.
func TestProjectRootFallsBackToMakefile(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "Makefile"), []byte("all:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(sub, "a.c")
	if err := os.WriteFile(file, []byte("int x;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := filepath.EvalSymlinks(projectRoot(file))
	want, _ := filepath.EvalSymlinks(sub)
	if got != want {
		t.Errorf("projectRoot = %q, want %q", got, want)
	}
}
