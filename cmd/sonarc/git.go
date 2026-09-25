package main

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"sonarc/internal/buffer"
	"sonarc/internal/vcs"
	"sonarc/internal/view"
)

// gitRefreshEvery bounds how stale the list of changed files can get when
// nothing announces a change: a file created by another program touches no
// git state. On a kernel tree a refresh costs about a third of a second.
const gitRefreshEvery = 30 * time.Second

// gitState is what the editor knows from git. Only the UI goroutine touches
// it; git runs in goroutines that post their results back.
type gitState struct {
	repo    *vcs.Repo
	changes map[string]vcs.Status

	signature   string    // repo.Signature() when changes were last read
	refreshed   time.Time // when changes were last asked for
	refreshing  bool
	refreshMore bool // something changed while a refresh ran; run another

	// bases holds each open file's last committed version, by path. gen
	// counts how often they have been thrown away, so a fetch that started
	// before a commit cannot install an out-of-date version after it.
	bases map[string]*base
	gen   int

	// hunks caches each view's diff against its base for one text version.
	hunks map[*view.View]cachedHunks
}

type base struct {
	lines   [][]byte // nil while loading, or when the file is not committed
	ok      bool     // the last commit has the file
	loading bool
}

type cachedHunks struct {
	version uint64
	gen     int
	base    *base
	hunks   []vcs.Hunk
}

// startGit finds the repository in the background and, if there is one,
// starts following it. Outside a repository nothing git-related appears.
func (a *app) startGit() {
	root := a.root
	go func() {
		r := vcs.Find(context.Background(), root)
		if r == nil {
			return
		}
		r.Top = logicalTop(root, r.Top)
		a.post(func() { a.setRepo(r) })
	}()
}

// setRepo starts following r: the Changes section appears and the first
// status and committed version are read.
func (a *app) setRepo(r *vcs.Repo) {
	a.git.repo = r
	a.ui.Sidebar.Changes.Enabled = true
	a.refreshGit()
	a.loadBase(a.v())
}

// logicalTop is the repository's top as a path under root rather than with
// symlinks resolved, which is how git reports it. Every other path in the
// editor is the logical one, the way the user typed it, and git's must match
// for a file's status to be found.
func logicalTop(root, realTop string) string {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return realTop
	}
	rel, err := filepath.Rel(realTop, realRoot)
	if err != nil || strings.HasPrefix(rel, "..") {
		return realTop
	}
	top := root
	if rel != "." {
		for range strings.Split(rel, string(filepath.Separator)) {
			top = filepath.Dir(top)
		}
	}
	return top
}

// refreshGit rereads which files have changed, in the background.
func (a *app) refreshGit() {
	g := &a.git
	if g.repo == nil {
		return
	}
	if g.refreshing {
		g.refreshMore = true
		return
	}
	g.refreshing, g.refreshed = true, a.now()
	repo := g.repo
	sig := repo.Signature()
	go func() {
		changes, err := repo.Changes(context.Background())
		a.post(func() {
			g.refreshing = false
			if err == nil {
				g.changes = changes
				g.signature = sig
				a.ui.Sidebar.SetGit(changes)
				a.updateChangesList()
			}
			if g.refreshMore {
				g.refreshMore = false
				a.refreshGit()
			}
		})
	}()
}

// pollGit runs with the disk check. When git's own state has moved — a
// commit, checkout or reset, perhaps in another terminal — the changed files
// and every committed version are reread; otherwise the list is refreshed
// now and then for files created outside git.
func (a *app) pollGit() {
	g := &a.git
	if g.repo == nil {
		return
	}
	if sig := g.repo.Signature(); sig != g.signature {
		g.signature = sig
		g.bases = nil
		g.gen++
		a.refreshGit()
		a.loadBase(a.v())
		return
	}
	if a.now().Sub(g.refreshed) >= gitRefreshEvery {
		a.refreshGit()
	}
}

// loadBase fetches v's committed version if it is not known yet.
func (a *app) loadBase(v *view.View) {
	g := &a.git
	path := v.Buf.Path()
	if g.repo == nil || path == "" || v.Buf.Large() {
		return
	}
	if g.bases == nil {
		g.bases = map[string]*base{}
	}
	if _, ok := g.bases[path]; ok {
		return
	}
	b := &base{loading: true}
	g.bases[path] = b
	repo, gen := g.repo, g.gen
	go func() {
		data, ok, err := repo.Committed(context.Background(), path)
		a.post(func() {
			if g.gen != gen || g.bases[path] != b {
				return // the repository moved on while this ran
			}
			b.loading = false
			if err != nil || !ok {
				return
			}
			old := buffer.FromBytes(data)
			b.ok = true
			b.lines = make([][]byte, old.NumLines())
			for i := range b.lines {
				b.lines[i] = old.Line(i)
			}
		})
	}()
}

// hunksFor is how v's text differs from its last commit, or nil when that is
// not known (no repository, not loaded yet, or a new file). It is recomputed
// only when the text or the committed version has changed.
func (a *app) hunksFor(v *view.View) []vcs.Hunk {
	g := &a.git
	b := g.bases[v.Buf.Path()]
	if b == nil {
		a.loadBase(v)
		return nil
	}
	if !b.ok {
		return nil
	}
	if c, ok := g.hunks[v]; ok && c.version == v.Buf.Version() && c.gen == g.gen && c.base == b {
		return c.hunks
	}
	cur := make([][]byte, v.Buf.NumLines())
	for i := range cur {
		cur[i] = v.Buf.Line(i)
	}
	h := vcs.Lines(b.lines, cur)
	if g.hunks == nil {
		g.hunks = map[*view.View]cachedHunks{}
	}
	g.hunks[v] = cachedHunks{version: v.Buf.Version(), gen: g.gen, base: b, hunks: h}
	return h
}

// forgetView drops what is cached for a view that has been closed.
func (a *app) forgetView(v *view.View) {
	delete(a.git.hunks, v)
}

// cmdNextChange and cmdPrevChange move to the next or previous changed block
// in the file, wrapping around.
func (a *app) cmdNextChange() { a.stepChange(1) }
func (a *app) cmdPrevChange() { a.stepChange(-1) }

func (a *app) stepChange(dir int) {
	if a.ui.Diff.Open {
		a.ui.Diff.StepBlock(dir, a.ui.View.Height)
		return
	}
	v := a.v()
	hunks := a.hunksFor(v)
	if len(hunks) == 0 {
		if a.git.repo == nil {
			a.ui.Notify("not in a git repository")
		} else {
			a.ui.Notify("no changes since the last commit")
		}
		return
	}
	// A pure deletion sits between lines; stop on the line after it.
	start := func(h vcs.Hunk) int { return min(h.NewStart, v.Buf.NumLines()-1) }
	cur := v.Head.Line
	target := -1
	if dir > 0 {
		for _, h := range hunks {
			if start(h) > cur {
				target = start(h)
				break
			}
		}
		if target < 0 {
			target = start(hunks[0])
		}
	} else {
		for i := len(hunks) - 1; i >= 0; i-- {
			if s := start(hunks[i]); s < cur {
				target = s
				break
			}
		}
		if target < 0 {
			target = start(hunks[len(hunks)-1])
		}
	}
	a.pushJump()
	v.Goto(target + 1)
}
