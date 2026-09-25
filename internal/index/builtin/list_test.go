package builtin

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

// gitTree writes files into a fresh git repository and commits those listed in
// commit. Anything else is left untracked.
func gitTree(t *testing.T, files map[string]string, commit ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := tree(t, files)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if len(commit) > 0 {
		git(append([]string{"add", "--"}, commit...)...)
		git("commit", "-q", "-m", "init")
	}
	return root
}

func rels(t *testing.T, root string, paths []string) []string {
	t.Helper()
	var out []string
	for _, p := range paths {
		r, err := filepath.Rel(root, p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, filepath.ToSlash(r))
	}
	sort.Strings(out)
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestListUsesGitInAWorkTree(t *testing.T) {
	root := gitTree(t, map[string]string{
		".gitignore":   "gen_*.c\n", // a glob ignore.Load does not understand
		"main.c":       "int main;\n",
		"sub/util.c":   "int util;\n",
		"new.c":        "int fresh;\n", // untracked, not ignored
		"gen_table.c":  "int gen;\n",   // untracked and ignored by the glob
		"README":       "hi\n",         // not source
		"vendor/lib.c": "int v;\n",     // skip-listed directory
	}, "main.c", "sub/util.c", ".gitignore", "vendor/lib.c")

	l, err := listFiles(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if l.source != "git" {
		t.Fatalf("source = %q, want git", l.source)
	}
	got := rels(t, root, l.files)
	want := []string{"main.c", "new.c", "sub/util.c"}
	if !equal(got, want) {
		t.Errorf("files = %v, want %v", got, want)
	}
}

// A tracked file deleted from disk must not be offered for opening.
func TestListSkipsTrackedFilesDeletedFromDisk(t *testing.T) {
	root := gitTree(t, map[string]string{"a.c": "1\n", "b.c": "2\n"}, "a.c", "b.c")
	if err := os.Remove(filepath.Join(root, "b.c")); err != nil {
		t.Fatal(err)
	}
	l, _ := listFiles(root, 0)
	if got := rels(t, root, l.files); !equal(got, []string{"a.c"}) {
		t.Errorf("files = %v, want only a.c", got)
	}
}

// Opening a subdirectory of a repository lists only what is below it.
func TestListFromASubdirectoryOfARepo(t *testing.T) {
	root := gitTree(t, map[string]string{"top.c": "1\n", "fs/namei.c": "2\n"}, "top.c", "fs/namei.c")
	sub := filepath.Join(root, "fs")
	l, _ := listFiles(sub, 0)
	if l.source != "git" {
		t.Fatalf("source = %q, want git", l.source)
	}
	if got := rels(t, sub, l.files); !equal(got, []string{"namei.c"}) {
		t.Errorf("files = %v, want only namei.c", got)
	}
}

// Outside git the walk is used, and gives the same answer for the same tree.
func TestWalkAndGitAgreeOnTheSameTree(t *testing.T) {
	files := map[string]string{
		"main.c": "1\n", "sub/util.c": "2\n", "README": "x\n", "node_modules/m.js": "3\n",
	}
	plain := tree(t, files)
	walked, _ := listFiles(plain, 0)
	if walked.source != "walk" {
		t.Fatalf("source = %q, want walk outside a repository", walked.source)
	}
	repo := gitTree(t, files, "main.c", "sub/util.c", "README", "node_modules/m.js")
	gitted, _ := listFiles(repo, 0)

	a, b := rels(t, plain, walked.files), rels(t, repo, gitted.files)
	if !equal(a, b) {
		t.Errorf("walk found %v, git found %v", a, b)
	}
}

func TestListReportsTruncation(t *testing.T) {
	for _, useGit := range []bool{false, true} {
		files := map[string]string{"a.c": "1\n", "b.c": "2\n", "c.c": "3\n"}
		var root string
		if useGit {
			root = gitTree(t, files, "a.c", "b.c", "c.c")
		} else {
			root = tree(t, files)
		}
		l, _ := listFiles(root, 2)
		if len(l.files) != 2 || !l.truncated {
			t.Errorf("git=%v: got %d files, truncated=%v; want 2 and true", useGit, len(l.files), l.truncated)
		}
		l, _ = listFiles(root, 3)
		if l.truncated {
			t.Errorf("git=%v: a limit equal to the file count is not truncation", useGit)
		}
	}
}

// The symbol cap must not shrink the file list: on a kernel tree the single
// 60,000 cap hid files from fuzzy open.
func TestSymbolCapKeepsTheWholeFileList(t *testing.T) {
	files := map[string]string{}
	for _, n := range []string{"a", "b", "c", "d"} {
		files[n+".c"] = "int " + n + "_fn(void)\n{\n\treturn 0;\n}\n"
	}
	root := tree(t, files)
	ix := New(root)
	old := maxSymbolFilesForTest(2)
	defer maxSymbolFilesForTest(old)
	ix.Build(t.Context())

	nfiles, _ := ix.Stats()
	if nfiles != 4 {
		t.Errorf("file list has %d files, want all 4", nfiles)
	}
	if _, truncated := ix.Coverage(); !truncated {
		t.Error("hitting the symbol cap must be reported")
	}
}
