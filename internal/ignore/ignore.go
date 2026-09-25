// Package ignore decides which files and directories a project walk should
// skip. It is shared by the built-in indexer and the file-tree sidebar, so a
// directory ignored by .gitignore — or a vendor tree nobody wants indexed —
// disappears from both at once instead of being defined twice and drifting.
package ignore

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// SkipDirs are never descended into. These are the directories that make a
// naive project walk take minutes instead of seconds, or that nobody wants to
// browse: VCS metadata, dependency trees, and build output.
var SkipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, ".bzr": true,
	"node_modules": true, "vendor": true, "target": true,
	".venv": true, "venv": true, "__pycache__": true,
	".mypy_cache": true, ".pytest_cache": true, ".tox": true,
	"dist": true, "build": true, ".idea": true, ".vscode": true,
	".cache": true, ".next": true, ".gradle": true,
}

// Ignorer decides which paths a walk skips, combining the built-in SkipDirs
// with a project's own .gitignore.
type Ignorer struct {
	// dirNames are directory names ignored anywhere in the tree.
	dirNames map[string]bool
	// suffixes are file suffixes to ignore, from patterns like *.o.
	suffixes []string
	// rooted are paths anchored at the project root.
	rooted map[string]bool
	// names are exact file or directory names ignored anywhere.
	names map[string]bool
}

// Load reads .gitignore at root.
//
// This implements the common subset of gitignore syntax — directory patterns,
// *.ext suffixes, anchored paths and bare names — rather than the full
// specification. Getting those four right avoids walking build output and
// dependency trees, which is what actually matters for indexing speed; the
// remaining cases only cost a few extra files.
func Load(root string) *Ignorer {
	ig := &Ignorer{
		dirNames: map[string]bool{},
		rooted:   map[string]bool{},
		names:    map[string]bool{},
	}
	f, err := os.Open(filepath.Join(root, ".gitignore"))
	if err != nil {
		return ig
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue // comments and negations are not handled
		}
		// A leading slash anchors the pattern to the project root: "/linux"
		// is the top-level build artifact, not every directory named linux.
		// Losing that distinction hid include/linux on a kernel tree.
		anchored := strings.HasPrefix(line, "/")
		isDir := strings.HasSuffix(line, "/")
		line = strings.Trim(line, "/")
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "*."):
			ig.suffixes = append(ig.suffixes, line[1:])
		case strings.ContainsAny(line, "*?["):
			// Other globs are skipped rather than half-implemented.
		case anchored || strings.Contains(line, "/"):
			ig.rooted[line] = true
		case isDir:
			ig.dirNames[line] = true
		default:
			ig.names[line] = true
			ig.dirNames[line] = true
		}
	}
	return ig
}

// SkipDir reports whether a directory should not be descended into. rel is
// the path relative to the project root.
func (ig *Ignorer) SkipDir(rel, name string) bool {
	if SkipDirs[name] || strings.HasPrefix(name, ".") && name != "." {
		return true
	}
	return ig.dirNames[name] || ig.rooted[rel]
}

// SkipFile reports whether a file should not be scanned.
func (ig *Ignorer) SkipFile(rel, name string) bool {
	if ig.names[name] || ig.rooted[rel] {
		return true
	}
	for _, s := range ig.suffixes {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}
