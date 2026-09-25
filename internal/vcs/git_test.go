package vcs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// repo makes a git repository with files committed.
func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	dir, _ = filepath.EvalSymlinks(dir) // macOS: /var is a link to /private/var
	run(t, dir, "init", "-q")
	for name, content := range files {
		write(t, filepath.Join(dir, name), content)
	}
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestChangesReportsEachKind(t *testing.T) {
	dir := repo(t, map[string]string{"mod.c": "1\n", "del.c": "2\n", "old.c": "3\n", "same.c": "4\n", "sub/deep.c": "5\n"})
	ctx := context.Background()
	write(t, filepath.Join(dir, "mod.c"), "changed\n")
	os.Remove(filepath.Join(dir, "del.c"))
	run(t, dir, "mv", "old.c", "renamed.c")
	write(t, filepath.Join(dir, "new.c"), "x\n")
	write(t, filepath.Join(dir, "staged.c"), "y\n")
	run(t, dir, "add", "staged.c")
	write(t, filepath.Join(dir, "sub", "deep.c"), "changed\n")

	r := Find(ctx, filepath.Join(dir, "sub"))
	if r == nil || r.Top != dir {
		t.Fatalf("Find from a subdirectory = %+v, want top %s", r, dir)
	}
	got, err := r.Changes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Status{
		"mod.c": 'M', "del.c": 'D', "renamed.c": 'R', "new.c": '?', "staged.c": 'A', "sub/deep.c": 'M',
	}
	for name, st := range want {
		if got[filepath.Join(dir, name)] != st {
			t.Errorf("%s: status %q, want %q", name, got[filepath.Join(dir, name)], st)
		}
	}
	if _, ok := got[filepath.Join(dir, "same.c")]; ok {
		t.Error("an unchanged file was reported")
	}
	if _, ok := got[filepath.Join(dir, "old.c")]; ok {
		t.Error("a rename's old name was reported as a file of its own")
	}
}

func TestCommittedReturnsTheLastCommittedVersion(t *testing.T) {
	dir := repo(t, map[string]string{"a.c": "committed\n"})
	ctx := context.Background()
	r := Find(ctx, dir)
	write(t, filepath.Join(dir, "a.c"), "edited\n")
	data, ok, err := r.Committed(ctx, filepath.Join(dir, "a.c"))
	if err != nil || !ok || string(data) != "committed\n" {
		t.Errorf("Committed = %q %v %v", data, ok, err)
	}
	write(t, filepath.Join(dir, "new.c"), "x\n")
	if _, ok, err := r.Committed(ctx, filepath.Join(dir, "new.c")); ok || err != nil {
		t.Errorf("a new file: ok=%v err=%v, want not committed and no error", ok, err)
	}
	if _, ok, _ := r.Committed(ctx, "/elsewhere/x.c"); ok {
		t.Error("a path outside the repository was found")
	}
}

func TestSignatureMovesOnCommit(t *testing.T) {
	dir := repo(t, map[string]string{"a.c": "1\n"})
	r := Find(context.Background(), dir)
	before := r.Signature()
	if before == "" {
		t.Fatal("empty signature")
	}
	time.Sleep(20 * time.Millisecond)
	write(t, filepath.Join(dir, "a.c"), "2\n")
	if r.Signature() != before {
		t.Error("editing a work-tree file moved the signature; only git's own state should")
	}
	run(t, dir, "commit", "-qam", "two")
	if r.Signature() == before {
		t.Error("a commit did not move the signature")
	}
}

func TestFindOutsideARepository(t *testing.T) {
	if r := Find(context.Background(), t.TempDir()); r != nil {
		t.Errorf("Find outside a repository = %+v", r)
	}
}
