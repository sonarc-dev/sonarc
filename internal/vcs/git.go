package vcs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Repo is a git work tree.
type Repo struct {
	Top    string // the work tree's top directory, absolute
	GitDir string // its .git directory, absolute (a worktree's lives elsewhere)
}

// Find returns the repository containing dir, or nil when dir is not in one
// or git is not installed.
func Find(ctx context.Context, dir string) *Repo {
	out, err := git(ctx, dir, "rev-parse", "--show-toplevel", "--absolute-git-dir")
	if err != nil {
		return nil
	}
	f := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(f) != 2 {
		return nil
	}
	return &Repo{Top: f[0], GitDir: f[1]}
}

// Status is a file's state in git, as a single letter: M modified, A added,
// D deleted, R renamed, U unmerged (a conflict), ? untracked.
type Status byte

// Changes lists every file that differs from the last commit, including
// untracked ones, keyed by absolute path. Ignored files are not included.
func (r *Repo) Changes(ctx context.Context) (map[string]Status, error) {
	out, err := git(ctx, r.Top, "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return nil, err
	}
	return parseStatus(r.Top, out), nil
}

// parseStatus reads `git status --porcelain=v1 -z`: records of "XY path",
// NUL-terminated; a rename or copy is followed by one more record, the
// original path.
func parseStatus(top string, out []byte) map[string]Status {
	m := map[string]Status{}
	recs := bytes.Split(out, []byte{0})
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if len(rec) < 4 {
			continue
		}
		x, y := rec[0], rec[1]
		path := filepath.Join(top, filepath.FromSlash(string(rec[3:])))
		var st Status
		switch {
		case x == '?':
			st = '?'
		case x == 'U' || y == 'U' || (x == 'A' && y == 'A') || (x == 'D' && y == 'D'):
			st = 'U'
		case x == 'R' || x == 'C':
			st = 'R'
			i++ // skip the original path
		case x == 'A':
			st = 'A'
		case x == 'D' || y == 'D':
			st = 'D'
		default:
			st = 'M'
		}
		m[path] = st
	}
	return m
}

// Committed returns the file at path as of the last commit. ok is false for a
// file the last commit does not have: new, untracked, or in a repository with
// no commits yet.
func (r *Repo) Committed(ctx context.Context, path string) (data []byte, ok bool, err error) {
	rel, err := filepath.Rel(r.Top, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil, false, nil
	}
	out, err := git(ctx, r.Top, "cat-file", "blob", "HEAD:"+filepath.ToSlash(rel))
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, false, nil // not in HEAD
		}
		return nil, false, err
	}
	return out, true, nil
}

// Signature changes whenever something that could alter Changes or Committed
// happens inside git itself: a commit, checkout, reset, stash, or staging. It
// costs two stats, so it can be polled often and git asked only when it moves.
func (r *Repo) Signature() string {
	var b strings.Builder
	for _, name := range []string{"index", "HEAD", filepath.Join("logs", "HEAD")} {
		if fi, err := os.Stat(filepath.Join(r.GitDir, name)); err == nil {
			b.WriteString(fi.ModTime().String())
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// timeout bounds each git command. A healthy repository answers well within
// it; one on a hung network filesystem must not leave work pending forever.
const timeout = 30 * time.Second

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	// Never stop to ask for credentials or open an editor: there is no one to
	// answer, and the editor's own terminal is in raw mode.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	return cmd.Output()
}
