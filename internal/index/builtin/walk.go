// Package builtin is the code-intelligence provider of last resort.
//
// cscope and universal-ctags are not installed everywhere, and on a fresh
// server they usually are not. Without this package the editor's navigation
// would simply be dead on such a machine. It walks the project itself,
// extracts definitions with per-language heuristics, and finds references with
// its own parallel scanner, so goto-definition and find-references work with no
// external tools at all.
//
// It is deliberately less precise than cscope. It is registered last, so its
// answers only surface when nothing better is available.
package builtin

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"sonarc/internal/ignore"
)

// Language identifies how a file should be parsed.
type Language int

const (
	LangUnknown Language = iota
	LangC
	LangGo
	LangPython
)

// extLanguage maps file extensions to a parser. C, Go and Python are the
// languages this editor targets first; everything else is still searchable for
// references, just not indexed for definitions.
var extLanguage = map[string]Language{
	".c": LangC, ".h": LangC, ".cc": LangC, ".cpp": LangC, ".cxx": LangC,
	".hpp": LangC, ".hh": LangC, ".hxx": LangC, ".c++": LangC, ".inl": LangC,
	".go":  LangGo,
	".py":  LangPython,
	".pyi": LangPython,
}

// searchableExts are additionally scanned for references even though no
// definition parser exists for them.
var searchableExts = map[string]bool{
	".rs": true, ".js": true, ".ts": true, ".jsx": true, ".tsx": true,
	".java": true, ".sh": true, ".bash": true, ".pl": true, ".rb": true,
	".mk": true, ".cmake": true, ".s": true, ".S": true, ".asm": true,
	".proto": true, ".md": true, ".txt": true, ".conf": true, ".cfg": true,
	".json": true, ".yaml": true, ".yml": true, ".toml": true, ".sql": true,
}

// languageOf returns the parser for a path, and whether the file is worth
// scanning at all.
func languageOf(path string) (Language, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	if l, ok := extLanguage[ext]; ok {
		return l, true
	}
	if searchableExts[ext] {
		return LangUnknown, true
	}
	// Extensionless files with a known name are usually worth reading.
	base := filepath.Base(path)
	for _, n := range specialNames {
		if base == n {
			return LangUnknown, true
		}
	}
	return LangUnknown, false
}

// specialNames are extensionless files worth reading. Shared with GitGrep's
// pathspecs so both searches cover the same files.
var specialNames = []string{"Makefile", "makefile", "GNUmakefile", "Kconfig", "Kbuild", "Dockerfile"}

// maxFileSize bounds what is read. Anything larger is generated, vendored, or
// not source, and reading it would stall the index for no benefit.
const maxFileSize = 4 << 20

// walkFiles collects the source files under root worth indexing, by walking the
// directory. It reports whether it stopped at limit.
func walkFiles(root string, limit int) ([]string, bool, error) {
	ig := ignore.Load(root)
	var out []string
	truncated := false

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable directories are skipped, not fatal
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		name := d.Name()

		if d.IsDir() {
			if path == root {
				return nil
			}
			if ig.SkipDir(rel, name) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // skip symlinks, devices, sockets
		}
		if !worthReading(ig, rel, path) {
			return nil
		}
		if limit > 0 && len(out) >= limit {
			truncated = true
			return filepath.SkipAll
		}
		out = append(out, path)
		return nil
	})
	return out, truncated, err
}

// worthReading applies the per-file filters: ignore rules, a source extension,
// and a size bound. Symlinks and special files are the caller's concern.
func worthReading(ig *ignore.Ignorer, rel, path string) bool {
	if ig.SkipFile(rel, filepath.Base(rel)) {
		return false
	}
	if _, ok := languageOf(path); !ok {
		return false
	}
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxFileSize {
		return false
	}
	return true
}

// listing is the result of finding a project's files.
type listing struct {
	files     []string
	source    string // "git", or "walk"
	truncated bool   // limit was reached; files is not the whole project
}

// gitTimeout bounds asking git for the file list. git is fast here (a quarter
// second on a kernel tree), so running long means something is wrong, such as
// a network filesystem, and walking is the better bet.
const gitTimeout = 10 * time.Second

// listFiles collects the project's source files, asking git when the project is
// a git work tree and walking the directory otherwise.
//
// git already knows the file list — tracked files from its index, untracked
// ones filtered by the project's ignore rules, which git applies in full rather
// than the subset ignore.Load understands. Filters that are about what is worth
// reading rather than what belongs to the project (skip-listed directories,
// source extensions, size) are applied to both sources alike, so the two give
// the same answer on the same tree.
func listFiles(root string, limit int) (listing, error) {
	if files, ok := gitFiles(root); ok {
		ig := ignore.Load(root)
		var l listing
		l.source = "git"
		for _, rel := range files {
			if skipByDir(ig, rel) {
				continue
			}
			path := filepath.Join(root, rel)
			if !worthReading(ig, rel, path) {
				continue
			}
			if limit > 0 && len(l.files) >= limit {
				l.truncated = true
				break
			}
			l.files = append(l.files, path)
		}
		return l, nil
	}
	files, truncated, err := walkFiles(root, limit)
	return listing{files: files, source: "walk", truncated: truncated}, err
}

// gitFiles asks git for the files under root, relative to it. It reports false
// when root is not in a git work tree or git is unavailable.
func gitFiles(root string) ([]string, bool) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	// -c core.quotepath=off and -z keep unusual filenames intact. Run from
	// root, git lists only what is below it, with paths relative to it.
	cmd := exec.CommandContext(ctx, "git", "-c", "core.quotepath=off",
		"ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	var files []string
	seen := make(map[string]struct{})
	for _, f := range bytes.Split(out, []byte{0}) {
		if len(f) == 0 {
			continue
		}
		rel := filepath.FromSlash(string(f))
		// A file with unresolved merge conflicts is listed once per stage.
		if _, dup := seen[rel]; dup {
			continue
		}
		seen[rel] = struct{}{}
		files = append(files, rel)
	}
	return files, true
}

// skipByDir reports whether any directory on the way to rel is one a walk would
// not descend into.
func skipByDir(ig *ignore.Ignorer, rel string) bool {
	dir := filepath.Dir(rel)
	if dir == "." {
		return false
	}
	parts := strings.Split(dir, string(filepath.Separator))
	for i, name := range parts {
		if ig.SkipDir(filepath.Join(parts[:i+1]...), name) {
			return true
		}
	}
	return false
}

// ProjectFiles lists every source file of the project at root, as absolute
// paths: git's list in a work tree, a walk otherwise. It is the list the
// built-in index and the index rebuild both use, so they cover the same files.
func ProjectFiles(root string) ([]string, error) {
	l, err := listFiles(root, maxListFiles)
	return l.files, err
}
