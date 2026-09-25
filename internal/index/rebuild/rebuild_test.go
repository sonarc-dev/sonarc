package rebuild

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// kernelTree is the minimum Detect recognises as a kernel, with an existing
// x86-only index like the one `make cscope tags` produced on the real server.
func kernelTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "Makefile"), "# kernel\n")
	write(t, filepath.Join(root, "Kconfig"), "# kconfig\n")
	write(t, filepath.Join(root, "scripts", "tags.sh"), "#!/bin/sh\n")
	write(t, filepath.Join(root, "cscope.files"), "-k\n-q\narch/x86/kernel/a.c\nfs/read_write.c\n")
	write(t, filepath.Join(root, "cscope.out"), "OLD CSCOPE")
	write(t, filepath.Join(root, "tags"), "OLD TAGS")
	return root
}

// fakeMake behaves like the kernel's index targets: tags.sh deletes tags and
// writes a new one; cscope writes cscope.files and renames ncscope.out into
// place. FAKE_MODE makes a target fail halfway or hang.
const fakeMake = `#!/bin/sh
for a in "$@"; do case "$a" in ARCH=*) echo "$a" > "$ARGLOG";; esac; target="$a"; done
case "$FAKE_MODE:$target" in
  hang:*) sleep 30 & echo $! > "$CHILDPID"; wait ;;
  failtags:tags) rm -f tags; printf 'HALF' > tags; echo "ctags: out of memory" >&2; exit 2 ;;
  failcscope:cscope) printf 'x' > cscope.files; printf 'HALF' > ncscope.out; echo "cscope: disk full" >&2; exit 2 ;;
  *:tags) rm -f tags; printf 'NEW TAGS' > tags ;;
  *:cscope) printf -- '-k\n-q\nfs/x.c\n' > cscope.files; printf 'NEW CSCOPE' > ncscope.out; mv ncscope.out cscope.out
            if [ -n "$FAKE_Q" ]; then printf 'NEW IN' > ncscope.out.in; mv ncscope.out.in cscope.out.in; printf 'NEW PO' > ncscope.out.po; mv ncscope.out.po cscope.out.po; fi ;;
esac
`

func withFakeTools(t *testing.T, mode string) (argLog string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stubs are POSIX-only")
	}
	bin := t.TempDir()
	write(t, filepath.Join(bin, "make"), fakeMake)
	os.Chmod(filepath.Join(bin, "make"), 0o755)
	// Present so Detect finds the tools; the kernel path never runs them.
	write(t, filepath.Join(bin, "cscope"), "#!/bin/sh\nexit 0\n")
	os.Chmod(filepath.Join(bin, "cscope"), 0o755)
	write(t, filepath.Join(bin, "ctags"), "#!/bin/sh\necho 'Exuberant Ctags 5.9'\n")
	os.Chmod(filepath.Join(bin, "ctags"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_MODE", mode)
	argLog = filepath.Join(t.TempDir(), "args")
	t.Setenv("ARGLOG", argLog)
	return argLog
}

func TestDetectRecognisesAKernelTreeAndItsArch(t *testing.T) {
	withFakeTools(t, "")
	p := Detect(kernelTree(t))
	if !p.Kernel || p.Arch != "x86" {
		t.Errorf("Detect = %+v, want a kernel plan for ARCH=x86", p)
	}
	if d := p.Describe(); !strings.Contains(d, "make cscope tags") || !strings.Contains(d, "ARCH=x86") {
		t.Errorf("Describe() = %q", d)
	}
}

func TestArchIsLeftToMakeWhenAmbiguous(t *testing.T) {
	root := t.TempDir()
	arch := func(name, content string) string {
		write(t, filepath.Join(root, name), content)
		return archFromCscopeFiles(root, filepath.Join(root, name))
	}
	if a := arch("two.files", "arch/x86/a.c\narch/arm64/b.c\n"); a != "" {
		t.Errorf("arch = %q for a two-architecture list, want \"\"", a)
	}
	if a := arch("abs.files", root+"/arch/arm64/kernel/x.c\n"+root+"/fs/y.c\n"); a != "arm64" {
		t.Errorf("arch = %q from absolute paths under the root, want arm64", a)
	}
	if a := arch("odd.files", "src/myarch/x/a.c\n"); a != "" {
		t.Errorf("arch = %q, want none", a)
	}
}

// The real server's list: x86 at the top level, and arch/ directories of other
// architectures nested under tools/ and Documentation/. Seeing those as
// architectures made the rebuild fall back to "the host architecture".
func TestNestedArchDirectoriesDoNotCount(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "cscope.files"), `-k
-q
arch/x86/include/uapi/asm/auxvec.h
arch/x86/kernel/cpu/common.c
tools/arch/powerpc/include/uapi/asm/kvm.h
tools/arch/arm64/include/asm/cputype.h
Documentation/arch/loongarch/x.rst
./arch/x86/entry/entry_64.S
`)
	if a := archFromCscopeFiles(root, filepath.Join(root, "cscope.files")); a != "x86" {
		t.Errorf("arch = %q, want x86", a)
	}
}

func TestKernelRebuildUsesMakeWithTheSameArch(t *testing.T) {
	argLog := withFakeTools(t, "")
	root := kernelTree(t)

	var seen []string
	steps := Run(context.Background(), Detect(root), func(s string) { seen = append(seen, s) })

	for _, s := range steps {
		if s.Err != nil || s.Skipped != "" {
			t.Errorf("step %s: err=%v skipped=%q", s.Name, s.Err, s.Skipped)
		}
	}
	if got := read(t, filepath.Join(root, "tags")); got != "NEW TAGS" {
		t.Errorf("tags = %q", got)
	}
	if got := read(t, filepath.Join(root, "cscope.out")); got != "NEW CSCOPE" {
		t.Errorf("cscope.out = %q", got)
	}
	if got := strings.TrimSpace(read(t, argLog)); got != "ARCH=x86" {
		t.Errorf("make was run with %q, want ARCH=x86", got)
	}
	if strings.Join(seen, ",") != "cscope,tags" {
		t.Errorf("progress = %v", seen)
	}
	assertNoLeftovers(t, root)
}

// The rule the whole package exists for: a build that fails halfway leaves the
// old index exactly as it was, not half-written and not missing.
func TestFailedKernelTagsBuildRestoresTheOldFile(t *testing.T) {
	withFakeTools(t, "failtags")
	root := kernelTree(t)
	steps := Run(context.Background(), Detect(root), nil)

	tagsStep := steps[1]
	if tagsStep.Err == nil || !strings.Contains(tagsStep.Err.Error(), "out of memory") {
		t.Errorf("tags step error = %v, want the tool's own message", tagsStep.Err)
	}
	if got := read(t, filepath.Join(root, "tags")); got != "OLD TAGS" {
		t.Errorf("tags = %q after a failed build, want the old file back", got)
	}
	// The steps are independent: cscope still succeeded.
	if got := read(t, filepath.Join(root, "cscope.out")); got != "NEW CSCOPE" {
		t.Errorf("cscope.out = %q, want the successful rebuild kept", got)
	}
	assertNoLeftovers(t, root)
}

func TestFailedKernelCscopeBuildRestoresEveryFile(t *testing.T) {
	withFakeTools(t, "failcscope")
	root := kernelTree(t)
	oldList := read(t, filepath.Join(root, "cscope.files"))
	Run(context.Background(), Detect(root), nil)

	if got := read(t, filepath.Join(root, "cscope.out")); got != "OLD CSCOPE" {
		t.Errorf("cscope.out = %q", got)
	}
	if got := read(t, filepath.Join(root, "cscope.files")); got != oldList {
		t.Errorf("cscope.files was not restored: %q", got)
	}
}

func TestCancelKillsTheBuildAndRestores(t *testing.T) {
	withFakeTools(t, "hang")
	childPid := filepath.Join(t.TempDir(), "child")
	t.Setenv("CHILDPID", childPid)
	root := kernelTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(300 * time.Millisecond); cancel() }()

	start := time.Now()
	steps := Run(ctx, Detect(root), nil)
	if el := time.Since(start); el > 10*time.Second {
		t.Fatalf("cancel took %v; the build was not killed", el)
	}
	for _, s := range steps {
		if !errors.Is(s.Err, context.Canceled) {
			t.Errorf("step %s: err = %v, want context.Canceled", s.Name, s.Err)
		}
	}
	if got := read(t, filepath.Join(root, "tags")); got != "OLD TAGS" {
		t.Errorf("tags = %q after cancel", got)
	}
	// make's own child must die with it: the whole process group is killed.
	pid := strings.TrimSpace(read(t, childPid))
	deadline := time.Now().Add(3 * time.Second)
	for exec.Command("kill", "-0", pid).Run() == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if exec.Command("kill", "-0", pid).Run() == nil {
		exec.Command("kill", "-9", pid).Run()
		t.Errorf("make's child %s survived the cancel", pid)
	}
}

// If the editor itself is killed mid-build, the next run restores the old
// file from the preserved link before doing anything else.
func TestRecoverAfterACrashMidBuild(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "tags")
	write(t, live+prevSuffix, "OLD TAGS") // the link the dead build left
	// tags.sh had already deleted tags when the editor was killed.
	Recover(root, "tags")
	if got := read(t, live); got != "OLD TAGS" {
		t.Errorf("tags = %q, want the preserved copy restored", got)
	}
	if exists(live + prevSuffix) {
		t.Error("the preserved copy should be gone once restored")
	}

	// If the build had finished, the new file wins and the link is dropped.
	write(t, live, "NEW TAGS")
	write(t, live+prevSuffix, "OLD TAGS")
	Recover(root, "tags")
	if got := read(t, live); got != "NEW TAGS" || exists(live+prevSuffix) {
		t.Errorf("tags = %q, prev exists = %v", got, exists(live+prevSuffix))
	}
}

// An inverted index the kernel's build did not rewrite describes the old
// database and must not survive next to the new one.
func TestStaleInvertedIndexIsRemovedAfterKernelCscope(t *testing.T) {
	withFakeTools(t, "")
	root := kernelTree(t)
	write(t, filepath.Join(root, "cscope.in.out"), "OLD IN")
	write(t, filepath.Join(root, "cscope.po.out"), "OLD PO")
	Run(context.Background(), Detect(root), nil)

	for _, n := range []string{"cscope.in.out", "cscope.po.out"} {
		if exists(filepath.Join(root, n)) {
			t.Errorf("%s from the old database survived the rebuild", n)
		}
	}
}

func TestGuardLeavesNothingIfItCannotPreserve(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "tags"), "OLD")
	// A directory where the link must go makes os.Link fail.
	os.Mkdir(filepath.Join(root, "tags"+prevSuffix), 0o755)
	os.WriteFile(filepath.Join(root, "tags"+prevSuffix, "x"), nil, 0o644)

	if _, err := protect(root, "tags"); err == nil {
		t.Fatal("protect should refuse to start when it cannot keep a copy")
	}
	if got := read(t, filepath.Join(root, "tags")); got != "OLD" {
		t.Errorf("tags = %q", got)
	}
}

func TestSkippedWhenToolsAreMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	root := t.TempDir()
	write(t, filepath.Join(root, "a.c"), "int a;\n")
	steps := Run(context.Background(), Detect(root), nil)
	for _, s := range steps {
		if s.Skipped == "" || s.Err != nil {
			t.Errorf("step %s = %+v, want skipped", s.Name, s)
		}
	}
}

func assertNoLeftovers(t *testing.T, root string) {
	t.Helper()
	if l := Leftovers(root); len(l) > 0 {
		t.Errorf("temporary files left behind: %v", l)
	}
	links, _ := filepath.Glob(filepath.Join(root, "*"+prevSuffix))
	if len(links) > 0 {
		t.Errorf("preserved copies left behind: %v", links)
	}
}

// The gigabyte files are kept by hard link, not copied. Run the failure case
// down that path too.
func TestFailedBuildRestoresLinkedLargeFiles(t *testing.T) {
	old := linkAbove
	linkAbove = 0 // treat every file as large
	defer func() { linkAbove = old }()

	withFakeTools(t, "failtags")
	root := kernelTree(t)
	Run(context.Background(), Detect(root), nil)

	if got := read(t, filepath.Join(root, "tags")); got != "OLD TAGS" {
		t.Errorf("tags = %q after a failed build, want the linked original back", got)
	}
	assertNoLeftovers(t, root)
}

func TestListsKeepWhitespacePathsOutOfCscopeOnly(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.c"), "int a;\n")
	write(t, filepath.Join(root, "dir with space", "b.c"), "int b;\n")
	p := Plan{Root: root, ListFrom: "project file list"}

	l, cleanup, err := fileLists(p)
	if err != nil {
		t.Fatal(err)
	}
	cs, ct := read(t, l.cscope), read(t, l.ctags)
	cleanup()

	if strings.Contains(cs, "dir with space") {
		t.Errorf("cscope list includes a path cscope cannot store:\n%s", cs)
	}
	if !strings.Contains(ct, "dir with space/b.c") || !strings.Contains(ct, "a.c") {
		t.Errorf("ctags list = %q, want both files", ct)
	}
	if left := Leftovers(root); len(left) > 0 {
		t.Errorf("cleanup left %v", left)
	}
}

// A project's own cscope.files is cscope's list as is; ctags gets its names
// without the option lines or quotes.
func TestProjectCscopeFilesDrivesBothTools(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "cscope.files"), "-k\n-q\nsrc/a.c\n\"odd name.c\"\n")
	p := Plan{Root: root, ListFrom: "cscope.files"}
	l, cleanup, err := fileLists(p)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if l.cscope != filepath.Join(root, "cscope.files") {
		t.Errorf("cscope list = %s, want the project's own file", l.cscope)
	}
	if got := read(t, l.ctags); got != "src/a.c\nodd name.c\n" {
		t.Errorf("ctags list = %q", got)
	}
}

// The kernel's make cscope with -q in cscope.files writes cscope.out.in and
// cscope.out.po, as it did on the real server. Those are kept; the other
// scheme's files, left from some earlier build, are stale and removed.
func TestKernelCscopeKeepsItsOwnInvertedIndexAndDropsTheOtherScheme(t *testing.T) {
	withFakeTools(t, "")
	t.Setenv("FAKE_Q", "1")
	root := kernelTree(t)
	write(t, filepath.Join(root, "cscope.out.in"), "OLD IN")
	write(t, filepath.Join(root, "cscope.out.po"), "OLD PO")
	write(t, filepath.Join(root, "cscope.in.out"), "OLDER IN")
	Run(context.Background(), Detect(root), nil)

	if got := read(t, filepath.Join(root, "cscope.out.in")); got != "NEW IN" {
		t.Errorf("cscope.out.in = %q, want the rebuilt one", got)
	}
	if exists(filepath.Join(root, "cscope.in.out")) {
		t.Error("a stale cscope.in.out survived next to the new database")
	}
	assertNoLeftovers(t, root)
}

// The layout on the real server: an inverted index under the .out.in scheme. A
// failed cscope build must put back all of it, including the 1.1 GB .po file.
func TestFailedKernelCscopeRestoresTheInvertedIndex(t *testing.T) {
	withFakeTools(t, "failcscope")
	root := kernelTree(t)
	write(t, filepath.Join(root, "cscope.out.in"), "OLD IN")
	write(t, filepath.Join(root, "cscope.out.po"), "OLD PO")
	Run(context.Background(), Detect(root), nil)

	for n, want := range map[string]string{"cscope.out": "OLD CSCOPE", "cscope.out.in": "OLD IN", "cscope.out.po": "OLD PO"} {
		if got := read(t, filepath.Join(root, n)); got != want {
			t.Errorf("%s = %q, want %q", n, got, want)
		}
	}
	assertNoLeftovers(t, root)
}

// A copy left by a build killed before the editor was renamed is still the
// last good file and is restored.
func TestRecoverRestoresCopyFromBeforeTheRename(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "tags")
	write(t, live+legacyPrevSuffix, "OLD TAGS")
	Recover(root, "tags")
	if got := read(t, live); got != "OLD TAGS" || exists(live+legacyPrevSuffix) {
		t.Errorf("tags = %q, legacy copy exists = %v", got, exists(live+legacyPrevSuffix))
	}
}
