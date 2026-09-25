package ignore

import (
	"os"
	"path/filepath"
	"testing"
)

func writeGitignore(t *testing.T, contents string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSkipDirCoversBuiltinAndDotDirs(t *testing.T) {
	ig := Load(t.TempDir())
	for _, name := range []string{"node_modules", "vendor", ".git", ".hidden"} {
		if !ig.SkipDir(name, name) {
			t.Errorf("SkipDir(%q) = false, want true", name)
		}
	}
	if ig.SkipDir("src", "src") {
		t.Error("SkipDir(\"src\") = true, want false")
	}
}

func TestGitignoreDirAndSuffixPatterns(t *testing.T) {
	root := writeGitignore(t, "build/\n*.gen.c\nsecret.c\n# comment\n\n")
	ig := Load(root)

	if !ig.SkipDir("build", "build") {
		t.Error("build/ from .gitignore should be skipped")
	}
	if !ig.SkipFile("thing.gen.c", "thing.gen.c") {
		t.Error("*.gen.c from .gitignore should be skipped")
	}
	if !ig.SkipFile("secret.c", "secret.c") {
		t.Error("secret.c from .gitignore should be skipped")
	}
	if ig.SkipFile("keep.c", "keep.c") {
		t.Error("keep.c should not be skipped")
	}
}

func TestGitignoreRootedPath(t *testing.T) {
	root := writeGitignore(t, "docs/generated\n")
	ig := Load(root)

	if !ig.SkipFile(filepath.Join("docs", "generated"), "generated") {
		t.Error("rooted path docs/generated should be skipped")
	}
	if ig.SkipFile(filepath.Join("other", "generated"), "generated") {
		t.Error("rooted pattern should not match a different directory")
	}
}

func TestLoadWithNoGitignoreIsPermissive(t *testing.T) {
	ig := Load(t.TempDir())
	if ig.SkipFile("main.go", "main.go") {
		t.Error("with no .gitignore, an ordinary file should not be skipped")
	}
}

// The kernel's .gitignore lists "/linux" for the top-level build artifact.
// Treating it as "any directory named linux" hid include/linux, which holds
// most of the headers a kernel developer navigates to. Found on a real tree.
func TestLeadingSlashAnchorsToRoot(t *testing.T) {
	ig := Load(writeGitignore(t, "/linux\n/vmlinux\n"))

	if !ig.SkipDir("linux", "linux") {
		t.Error("/linux should ignore the top-level linux")
	}
	if ig.SkipDir(filepath.Join("include", "linux"), "linux") {
		t.Error("/linux must not ignore include/linux")
	}
	if !ig.SkipFile("vmlinux", "vmlinux") {
		t.Error("/vmlinux should ignore the top-level file")
	}
	if ig.SkipFile(filepath.Join("tools", "vmlinux"), "vmlinux") {
		t.Error("/vmlinux must not ignore tools/vmlinux")
	}
}

func TestUnanchoredNameStillMatchesAtAnyDepth(t *testing.T) {
	ig := Load(writeGitignore(t, "linux\n"))
	if !ig.SkipDir(filepath.Join("include", "linux"), "linux") {
		t.Error("an unanchored name should match at any depth")
	}
}
