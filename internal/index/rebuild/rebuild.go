// Package rebuild regenerates a project's cscope database and tags file.
//
// On a kernel tree both are over a gigabyte and take minutes to build, the
// editor is using the old ones the whole time, and a rebuild that dies halfway
// must not leave the user with nothing. Three rules follow:
//
//   - Never write over a live index in place. Either build under a temporary
//     name and rename it into place, or, where a project's own build writes in
//     place, hard-link the old files aside first and put them back on failure.
//     The editor holds the old files open, so it keeps answering from them
//     until it reloads.
//   - Build the index the project's way. A Linux kernel tree has its own
//     `make cscope` and `make tags`, which understand SYSCALL_DEFINE and the
//     rest of the kernel's macros and index one architecture; plain ctags -R
//     would silently lose sys_read and index every architecture instead.
//   - Everything is cancellable, and cancelling kills the whole process tree.
package rebuild

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sonarc-dev/sonarc/internal/index/builtin"
	"github.com/sonarc-dev/sonarc/internal/index/tags"
	"github.com/sonarc-dev/sonarc/internal/proc"
)

// Plan is how a rebuild will be done, decided before it starts so the user can
// be told what is about to run.
type Plan struct {
	Root string

	// Kernel means the tree's own make targets are used.
	Kernel bool
	// Arch is passed to the kernel's make as ARCH=, taken from the existing
	// cscope.files so a rebuild keeps indexing the architecture the user chose.
	// Empty means make's default, the host architecture.
	Arch string

	// ListFrom says where a generic rebuild takes its file list: the project's
	// own cscope.files when there is one, otherwise the editor's file list.
	ListFrom string

	Cscope bool // cscope is installed
	Ctags  bool // a ctags able to generate a usable file is installed
}

// Detect decides how to rebuild the index at root.
func Detect(root string) Plan {
	p := Plan{Root: root}
	_, err := exec.LookPath("cscope")
	p.Cscope = err == nil
	_, _, p.Ctags = tags.DetectCtags()

	if isKernel(root) {
		p.Kernel = true
		p.Arch = archFromCscopeFiles(root, filepath.Join(root, "cscope.files"))
		return p
	}
	if exists(filepath.Join(root, "cscope.files")) {
		p.ListFrom = "cscope.files"
	} else {
		p.ListFrom = "project file list"
	}
	return p
}

// Describe says in one line what Run will do, for the confirmation prompt.
func (p Plan) Describe() string {
	var tools []string
	if p.Cscope {
		tools = append(tools, "cscope")
	}
	if p.Ctags {
		tools = append(tools, "tags")
	}
	if len(tools) == 0 {
		return "neither cscope nor a usable ctags is installed"
	}
	what := strings.Join(tools, " and ")
	if p.Kernel {
		arch := "the host architecture"
		if p.Arch != "" {
			arch = "ARCH=" + p.Arch
		}
		return fmt.Sprintf("kernel tree: make %s, %s", strings.Join(tools, " "), arch)
	}
	return fmt.Sprintf("%s from %s", what, p.ListFrom)
}

// Step is the outcome of rebuilding one index.
type Step struct {
	Name    string // "cscope" or "tags"
	Elapsed time.Duration
	Err     error
	Skipped string // why it did not run, when it did not
}

// Run rebuilds each index the plan covers, reporting each step's name to
// progress as it starts. A failed or cancelled step leaves that index exactly
// as it was. Steps are independent: tags still rebuild if cscope fails.
func Run(ctx context.Context, p Plan, progress func(step string)) []Step {
	if progress == nil {
		progress = func(string) {}
	}
	var out []Step
	step := func(name string, available bool, missing string, fn func(context.Context) error) {
		if !available {
			out = append(out, Step{Name: name, Skipped: missing})
			return
		}
		if ctx.Err() != nil {
			out = append(out, Step{Name: name, Err: ctx.Err()})
			return
		}
		progress(name)
		start := time.Now()
		err := fn(ctx)
		out = append(out, Step{Name: name, Elapsed: time.Since(start), Err: err})
	}

	if p.Kernel {
		step("cscope", p.Cscope, "cscope not installed", func(ctx context.Context) error {
			return kernelTarget(ctx, p, "cscope", cscopeFiles...)
		})
		step("tags", p.Ctags, "no usable ctags", func(ctx context.Context) error {
			return kernelTarget(ctx, p, "tags", "tags")
		})
		return out
	}

	// Temporary files from a rebuild that was killed outright.
	for _, f := range Leftovers(p.Root) {
		os.Remove(f)
	}
	lists, cleanup, err := fileLists(p)
	if err != nil {
		for _, n := range []string{"cscope", "tags"} {
			out = append(out, Step{Name: n, Err: err})
		}
		return out
	}
	defer cleanup()
	step("cscope", p.Cscope, "cscope not installed", func(ctx context.Context) error {
		return genericCscope(ctx, p.Root, lists.cscope)
	})
	step("tags", p.Ctags, "no usable ctags", func(ctx context.Context) error {
		return genericTags(ctx, p.Root, lists.ctags)
	})
	return out
}

// cscopeFiles are what a cscope build writes.
//
// The inverted index exists only when built with -q, and has two naming
// schemes: a build with an explicit -f cscope.out, which is what the kernel's
// target runs, writes cscope.out.in and cscope.out.po; a build without -f
// writes cscope.in.out and cscope.po.out. The editor runs cscope -d -f
// cscope.out, which reads the .out.in pair if present and falls back to the
// other, so a stale .out.in pair shadows a good index, and the error it causes
// is an interactive "Press the RETURN key" prompt that desynchronises the line
// protocol. Both pairs are therefore guarded, and whichever the build did not
// rewrite is removed.
var cscopeFiles = []string{"cscope.out", "cscope.files", "cscope.out.in", "cscope.out.po", "cscope.in.out", "cscope.po.out"}

// invertedIndex are the inverted-index names, in both schemes.
var invertedIndex = []string{"cscope.out.in", "cscope.out.po", "cscope.in.out", "cscope.po.out"}

// tmpPrefix names every temporary file a rebuild creates in the project root,
// so they are easy to recognise and clean up.
const tmpPrefix = ".sonarc-rebuild."

// kernelTarget runs one of the kernel's own index targets. They write in place
// (tags.sh does `rm -f tags` and regenerates it), so the old files are guarded
// first and restored if the target fails or is cancelled.
func kernelTarget(ctx context.Context, p Plan, target string, outputs ...string) error {
	g, err := protect(p.Root, outputs...)
	if err != nil {
		return err
	}
	args := []string{"-s"}
	if p.Arch != "" {
		args = append(args, "ARCH="+p.Arch)
	}
	args = append(args, target)
	if err := runTool(ctx, p.Root, "make", args...); err != nil {
		g.rollback()
		return err
	}
	g.commit()
	return nil
}

// lists are the file lists a generic rebuild feeds the two tools.
type lists struct {
	cscope string // a cscope name file: may carry option lines
	ctags  string // one bare name per line, for ctags -L
}

// fileLists writes the lists a generic rebuild indexes, relative to the root.
//
// The project's own cscope.files is used as is when there is one, including
// the option lines cscope honours in it; ctags gets its names without them.
//
// Otherwise both come from the editor's file list, with one difference: cscope
// cannot store a path containing whitespace. Its name file accepts one quoted,
// but queries touching that entry then fail with "File does not have expected
// format", so such files are left out of cscope's list. ctags handles them, and
// text search covers them either way.
func fileLists(p Plan) (lists, func(), error) {
	var made []string
	cleanup := func() {
		for _, f := range made {
			os.Remove(f)
		}
	}
	create := func(kind string, names []string) (string, error) {
		f, err := os.CreateTemp(p.Root, tmpPrefix+kind+".*")
		if err != nil {
			return "", err
		}
		made = append(made, f.Name())
		w := bufio.NewWriter(f)
		for _, n := range names {
			fmt.Fprintln(w, n)
		}
		if err := w.Flush(); err != nil {
			f.Close()
			return "", err
		}
		return f.Name(), f.Close()
	}
	fail := func(err error) (lists, func(), error) {
		cleanup()
		return lists{}, func() {}, err
	}

	var l lists
	var err error
	if p.ListFrom == "cscope.files" {
		l.cscope = filepath.Join(p.Root, "cscope.files")
		names, err := namesFrom(l.cscope)
		if err != nil {
			return fail(err)
		}
		if l.ctags, err = create("ctags-list", names); err != nil {
			return fail(err)
		}
		return l, cleanup, nil
	}

	files, lerr := builtin.ProjectFiles(p.Root)
	if lerr != nil && len(files) == 0 {
		return fail(fmt.Errorf("listing project files: %w", lerr))
	}
	var all, noSpace []string
	for _, abs := range files {
		rel, err := filepath.Rel(p.Root, abs)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		all = append(all, rel)
		if !strings.ContainsAny(rel, " \t") {
			noSpace = append(noSpace, rel)
		}
	}
	if l.cscope, err = create("cscope-list", noSpace); err != nil {
		return fail(err)
	}
	if l.ctags, err = create("ctags-list", all); err != nil {
		return fail(err)
	}
	return l, cleanup, nil
}

// genericCscope builds a cscope database under a temporary name and renames it
// into place only once it is complete.
//
// With -f NAME cscope writes the inverted index to NAME.in and NAME.po; they are
// installed as cscope.out.in and cscope.out.po, the names cscope -d -f
// cscope.out reads first. An inverted index left from an earlier build under
// either scheme is then removed; see cscopeFiles.
func genericCscope(ctx context.Context, root, list string) error {
	tmp := filepath.Join(root, tmpPrefix+"cscope.out")
	temps := []string{tmp, tmp + ".in", tmp + ".po"}
	defer func() {
		for _, t := range temps {
			os.Remove(t) // no-op once renamed
		}
	}()
	if err := runTool(ctx, root, "cscope", "-b", "-q", "-k", "-i", list, "-f", tmp); err != nil {
		return err
	}
	moves := [][2]string{
		{tmp + ".in", filepath.Join(root, "cscope.out.in")},
		{tmp + ".po", filepath.Join(root, "cscope.out.po")},
		{tmp, filepath.Join(root, "cscope.out")}, // last: it is what readers look for
	}
	installed := map[string]bool{}
	for _, m := range moves {
		if !exists(m[0]) {
			continue
		}
		if err := os.Rename(m[0], m[1]); err != nil {
			return fmt.Errorf("installing %s: %w", filepath.Base(m[1]), err)
		}
		installed[filepath.Base(m[1])] = true
	}
	for _, n := range invertedIndex {
		if !installed[n] {
			os.Remove(filepath.Join(root, n)) // from an earlier build; describes the old database
		}
	}
	return nil
}

// genericTags writes a sorted tags file under a temporary name and renames it
// into place.
func genericTags(ctx context.Context, root, list string) error {
	exe, universal, ok := tags.DetectCtags()
	if !ok {
		return errors.New("no usable ctags")
	}
	extra := "--extra=+q" // Exuberant's spelling; Universal renamed it
	if universal {
		extra = "--extras=+q"
	}
	tmp := filepath.Join(root, tmpPrefix+"tags")
	defer os.Remove(tmp)
	if err := runTool(ctx, root, exe,
		"--fields=+iaSKmnl", // inheritance, access, signature, kind, scope, line, language
		extra,               // also emit qualified names, e.g. struct::member
		"--sort=yes",        // required for on-disk binary search
		"-L", list,
		"-f", tmp,
	); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(root, "tags"))
}

// namesFrom reads the file names from a cscope name file, skipping its option
// lines and unquoting quoted names.
func namesFrom(list string) ([]string, error) {
	f, err := os.Open(list)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var names []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "-") {
			continue
		}
		if len(line) >= 2 && line[0] == '"' && line[len(line)-1] == '"' {
			line = strings.ReplaceAll(line[1:len(line)-1], `\"`, `"`)
		}
		names = append(names, line)
	}
	return names, sc.Err()
}

// runTool runs a build tool in dir at low priority, killing its whole process
// tree if ctx ends. The indexers run for minutes on a large tree, usually on a
// machine someone is also using for real work, and the editor's own lookups
// must stay responsive meanwhile.
func runTool(ctx context.Context, dir, name string, args ...string) error {
	tool := filepath.Base(name)
	if nice, err := exec.LookPath("nice"); err == nil {
		args = append([]string{"-n", "10", name}, args...)
		name = nice
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	proc.SetGroup(cmd)
	cmd.Cancel = func() error { proc.KillTree(cmd); return nil }
	cmd.WaitDelay = 5 * time.Second
	var tail tailBuffer
	cmd.Stdout, cmd.Stderr = &tail, &tail

	err := cmd.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		if msg := tail.lastLine(); msg != "" {
			return fmt.Errorf("%s: %s", tool, msg)
		}
		return fmt.Errorf("%s: %w", tool, err)
	}
	return nil
}

// tailBuffer keeps the last few kilobytes of a tool's output, which is where
// the reason for a failure is.
type tailBuffer struct{ b []byte }

func (t *tailBuffer) Write(p []byte) (int, error) {
	const keep = 4 << 10
	t.b = append(t.b, p...)
	if len(t.b) > keep {
		t.b = t.b[len(t.b)-keep:]
	}
	return len(p), nil
}

func (t *tailBuffer) lastLine() string {
	s := strings.TrimSpace(string(t.b))
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// isKernel recognises a Linux kernel source tree by the script its index
// targets run and the top-level Makefile that defines them.
func isKernel(root string) bool {
	return exists(filepath.Join(root, "scripts", "tags.sh")) &&
		exists(filepath.Join(root, "Makefile")) &&
		exists(filepath.Join(root, "Kconfig"))
}

// archFromCscopeFiles returns the one architecture an existing cscope.files
// indexes, or "" if it indexes none or several.
//
// Only a top-level arch/NAME/ counts. The kernel's list also carries paths such
// as tools/arch/powerpc/ and Documentation/arch/arm64/ — on the real server
// 136 lines across ten other names beside 1,371 for x86 — which say nothing
// about the architecture the index was built for. Paths may be relative, as
// `make cscope` writes them, or absolute under root, as `make O=dir` does.
func archFromCscopeFiles(root, path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	prefix := filepath.ToSlash(filepath.Clean(root)) + "/"
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.Trim(strings.TrimSpace(sc.Text()), `"`)
		line = strings.TrimPrefix(line, prefix)
		line = strings.TrimPrefix(line, "./")
		rest, ok := strings.CutPrefix(line, "arch/")
		if !ok {
			continue
		}
		if j := strings.IndexByte(rest, '/'); j > 0 {
			seen[rest[:j]] = true
		}
	}
	if len(seen) != 1 {
		return ""
	}
	for a := range seen {
		return a
	}
	return ""
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// guard preserves index files around a build that writes them in place.
//
// Each existing file is kept as NAME.sonarc-prev before the build. Rollback
// renames it back over whatever is there; commit deletes it.
//
// Large files are hard-linked rather than copied: no second gigabyte, and the
// old contents survive the build replacing NAME, which is what both big files
// see — tags.sh deletes tags before writing a new one, and cscope writes
// ncscope.out and renames it. A link does not survive a file being truncated
// and rewritten in place, though, and that is exactly how the kernel writes
// cscope.files; small files are therefore copied.
type guard struct {
	root    string
	names   []string
	existed map[string]os.FileInfo // name → the file as it was, for those that existed
}

const prevSuffix = ".sonarc-prev"

// legacyPrevSuffix is what the editor named these copies before it was
// renamed; Recover still restores them.
const legacyPrevSuffix = ".keditor-prev"

// linkAbove is the size from which a file is preserved by hard link instead of
// by copy. Index files are either a few megabytes (file lists) or a gigabyte.
var linkAbove int64 = 64 << 20

// Recover finishes the job of a rebuild that was killed before it could roll
// back or commit, such as when the editor itself was killed. A leftover
// NAME.sonarc-prev is the last good copy: it is restored if NAME is missing
// and discarded otherwise, since NAME is then a completed build. It is safe
// to call at any time, and Run's guard calls it before every in-place build.
func Recover(root string, names ...string) {
	for _, n := range names {
		live := filepath.Join(root, n)
		for _, prev := range []string{live + prevSuffix, live + legacyPrevSuffix} {
			if !exists(prev) {
				continue
			}
			if exists(live) {
				os.Remove(prev)
			} else {
				os.Rename(prev, live)
			}
		}
	}
}

// protect guards names under root. It fails, touching nothing, if a file
// cannot be preserved.
func protect(root string, names ...string) (*guard, error) {
	Recover(root, names...)
	g := &guard{root: root, existed: map[string]os.FileInfo{}}
	for _, n := range names {
		live := filepath.Join(root, n)
		fi, err := os.Stat(live)
		if err != nil {
			g.names = append(g.names, n) // absent now; remove if the build leaves one behind on failure
			continue
		}
		keep := copyFile
		if fi.Size() >= linkAbove {
			keep = os.Link
		}
		if err := keep(live, live+prevSuffix); err != nil {
			g.commit() // drop the copies already made
			return nil, fmt.Errorf("cannot preserve %s before rebuilding, so not starting: %w", n, err)
		}
		g.names = append(g.names, n)
		g.existed[n] = fi
	}
	return g, nil
}

// rollback puts every guarded file back as it was.
func (g *guard) rollback() {
	for _, n := range g.names {
		live := filepath.Join(g.root, n)
		if _, had := g.existed[n]; had {
			os.Rename(live+prevSuffix, live)
		} else {
			os.Remove(live) // the build created it; it did not exist before
		}
	}
}

// commit keeps the new files and drops the preserved ones.
//
// One case needs care. An inverted index is only rewritten by a build that
// uses -q, and only under one of its two naming schemes. One left untouched
// beside a new cscope.out describes the old database, so it is removed rather
// than left to give wrong answers or break the protocol; see cscopeFiles.
func (g *guard) commit() {
	outChanged := g.replaced("cscope.out")
	for _, n := range g.names {
		live := filepath.Join(g.root, n)
		if outChanged && isInverted(n) && !g.replaced(n) {
			os.Remove(live)
		}
		os.Remove(live + prevSuffix)
	}
}

func isInverted(n string) bool {
	for _, x := range invertedIndex {
		if n == x {
			return true
		}
	}
	return false
}

// replaced reports whether the build wrote n, either as a new file or by
// rewriting the old one in place, as opposed to leaving it untouched.
func (g *guard) replaced(n string) bool {
	old, had := g.existed[n]
	if !had {
		return exists(filepath.Join(g.root, n))
	}
	cur, err := os.Stat(filepath.Join(g.root, n))
	if err != nil {
		return false
	}
	return !os.SameFile(old, cur) || cur.Size() != old.Size() || !cur.ModTime().Equal(old.ModTime())
}

// copyFile copies src to a new file dst, preserving its mode.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}

// Leftovers lists temporary files a killed rebuild left in root, for cleanup.
func Leftovers(root string) []string {
	matches, _ := filepath.Glob(filepath.Join(root, tmpPrefix+"*"))
	sort.Strings(matches)
	return matches
}
