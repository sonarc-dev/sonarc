package builtin

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/sonarc-dev/sonarc/internal/index/provider"
)

// ErrNotGit means git cannot search this directory: git is not installed, or
// the directory is not inside a work tree. Callers fall back to another search.
var ErrNotGit = errors.New("not a git work tree")

// maxGitGrepResults stops a search for something ubiquitous ("int") from
// producing millions of lines. Hitting it is reported as a partial answer.
var maxGitGrepResults = 20_000

// GitGrep searches the project's source files for a literal string with git.
//
// On a kernel tree it answered in 3.3 s where cscope's text search took 7.1 s
// and the built-in scan over a minute: git grep runs on every core and reads
// file lists from its index instead of walking. It searches the same file types
// the built-in scan does, so switching between them does not change which
// files are covered, and untracked files are included so new work shows up.
//
// A search that completes without error is exhaustive over those files: no
// matches means there are none, and there is no point asking anything slower.
func GitGrep(ctx context.Context, root, pattern string) ([]provider.Location, error) {
	return gitGrep(ctx, root, pattern, "-F") // literal, like the built-in scan
}

// GitGrepRegexp is GitGrep for a regular expression, matched a line at a
// time. It asks for Perl-compatible syntax, the closest to Go's own, which the
// built-in scan uses; a git built without PCRE falls back to extended POSIX
// syntax, which agrees on everything but the backslash classes.
func GitGrepRegexp(ctx context.Context, root, pattern string, ignoreCase bool) ([]provider.Location, error) {
	flags := []string{"-P"}
	if ignoreCase {
		flags = append(flags, "-i")
	}
	locs, err := gitGrep(ctx, root, pattern, flags...)
	if errors.Is(err, errNoPCRE) {
		flags[0] = "-E"
		return gitGrep(ctx, root, pattern, flags...)
	}
	return locs, err
}

// errNoPCRE means this git was built without Perl-compatible regexes.
var errNoPCRE = errors.New("git grep: no PCRE support")

func gitGrep(ctx context.Context, root, pattern string, mode ...string) ([]provider.Location, error) {
	if pattern == "" {
		return nil, nil
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, ErrNotGit
	}
	args := []string{"-c", "core.quotepath=off", "grep",
		"-I", // skip binary files
		"-n", // line numbers
		"-z", // NUL after path and line number, so ':' in names is safe
	}
	args = append(args, mode...)
	args = append(args,
		"--untracked", // include new files not yet added
		"-e", pattern, "--")
	args = append(args, sourcePathspecs()...)

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrNotGit
	}

	var out []provider.Location
	tooMany := false
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), 4<<20) // tolerate minified one-line files
	for sc.Scan() {
		loc, ok := parseGitGrep(root, sc.Bytes())
		if !ok {
			continue
		}
		if len(out) >= maxGitGrepResults {
			tooMany = true
			_ = cmd.Process.Kill()
			break
		}
		out = append(out, loc)
	}
	// Drain so Wait does not block on a full pipe after an early stop.
	if tooMany {
		for sc.Scan() {
		}
	}
	werr := cmd.Wait()

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})

	switch {
	case ctx.Err() != nil:
		return out, &provider.PartialError{Cause: ctx.Err()}
	case tooMany:
		return out, &provider.PartialError{Cause: provider.ErrTooManyResults}
	case werr == nil:
		return out, nil
	}
	var ee *exec.ExitError
	if errors.As(werr, &ee) && ee.ExitCode() == 1 {
		return nil, nil // git grep's "no matches"
	}
	if strings.Contains(stderr.String(), "not a git repository") {
		return nil, ErrNotGit
	}
	if msg := stderr.String(); strings.Contains(msg, "Perl-compatible") || strings.Contains(msg, "PCRE") {
		return nil, errNoPCRE
	}
	return nil, errors.New("git grep: " + strings.TrimSpace(stderr.String()))
}

// parseGitGrep reads one "path\0line\0text" record.
func parseGitGrep(root string, rec []byte) (provider.Location, bool) {
	path, rest, ok := bytes.Cut(rec, []byte{0})
	if !ok {
		return provider.Location{}, false
	}
	num, text, ok := bytes.Cut(rest, []byte{0})
	if !ok {
		return provider.Location{}, false
	}
	n, err := strconv.Atoi(string(num))
	if err != nil {
		return provider.Location{}, false
	}
	return provider.Location{
		Path: filepath.Join(root, filepath.FromSlash(string(path))),
		Line: n,
		Text: strings.TrimSpace(string(text)),
	}, true
}

// sourcePathspecs limits git grep to the files the built-in scan would read:
// the source extensions and the extensionless build files it recognizes.
func sourcePathspecs() []string {
	var specs []string
	for ext := range extLanguage {
		specs = append(specs, "*"+ext)
	}
	for ext := range searchableExts {
		specs = append(specs, "*"+ext)
	}
	for _, name := range specialNames {
		specs = append(specs, ":(glob)**/"+name)
	}
	sort.Strings(specs)
	return specs
}
