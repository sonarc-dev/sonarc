package main

import (
	"github.com/sonarc-dev/sonarc/internal/buffer"
	"github.com/sonarc-dev/sonarc/internal/filetree"
	"github.com/sonarc-dev/sonarc/internal/view"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// launch is what the command line asked the editor to open.
type launch struct {
	// files are the non-directory arguments, in order. They need not exist:
	// opening a missing path starts an empty buffer that saves to it.
	files []string
	// at is where to put the cursor in each of files, from a "path:line" or
	// "path:line:col" argument; zero values mean the file's start.
	at []filePos
	// dir is the absolute path of the first directory argument, or "".
	// Further directory arguments are ignored; there is one project per window.
	dir string
}

// parseLaunch sorts command-line arguments into files and a project
// directory. Deciding this by stat rather than by spelling is what makes
// `sonarc .` and `sonarc ~/src/proj` work: buffer.Open would fail on either
// with "is a directory".
func parseLaunch(args []string) launch {
	var l launch
	for _, arg := range args {
		if fi, err := os.Stat(arg); err == nil && fi.IsDir() {
			if l.dir == "" {
				abs, err := filepath.Abs(arg)
				if err != nil {
					abs = arg
				}
				l.dir = abs
			}
			continue
		}
		path, at := splitPosition(arg)
		l.files = append(l.files, path)
		l.at = append(l.at, at)
	}
	return l
}

// filePos is a 1-based line and column from the command line.
type filePos struct{ line, col int }

// splitPosition reads the "file.c:42" and "file.c:42:7" forms that compilers,
// grep and stack traces print, so their output can be pasted straight onto the
// command line; a trailing colon, as grep leaves it, is allowed. A path that
// exists as written is taken literally, colon and all.
func splitPosition(arg string) (string, filePos) {
	if _, err := os.Stat(arg); err == nil {
		return arg, filePos{}
	}
	rest := strings.TrimSuffix(arg, ":")
	var nums []int
	for len(nums) < 2 {
		i := strings.LastIndexByte(rest, ':')
		if i <= 0 {
			break
		}
		n, err := strconv.Atoi(rest[i+1:])
		if err != nil || n <= 0 {
			break
		}
		nums = append(nums, n)
		rest = rest[:i]
	}
	switch len(nums) {
	case 1:
		return rest, filePos{line: nums[0]}
	case 2:
		return rest, filePos{line: nums[1], col: nums[0]}
	}
	return arg, filePos{}
}

// place puts the cursor where the command line asked, if it asked.
func place(v *view.View, at filePos) {
	if at.line <= 0 {
		return
	}
	v.Goto(at.line)
	if at.col > 0 {
		v.SetCursor(v.Buf.Clamp(buffer.Pos{Line: at.line - 1, Col: at.col - 1}))
	}
}

// setProject decides the two roots and builds the sidebar tree.
//
// They are deliberately distinct. root is the project — where indexes live and
// what searches cover — and is found by walking up from wherever the user
// pointed. The sidebar shows exactly the directory that was named. So
// `sonarc ~/Kernel/fs` browses fs/ but still searches and indexes the whole
// kernel; collapsing the two would narrow the index to one subdirectory, which
// is the failure the tiered project-root markers exist to prevent.
func (a *app) setProject(l launch, first *buffer.Buffer) {
	treeRoot := ""
	switch {
	case l.dir != "":
		a.root = projectRootFrom(l.dir)
		treeRoot = l.dir
	default:
		a.root = projectRoot(first.Path())
		treeRoot = a.root
	}
	a.ui.PanelRoot = a.root
	a.ui.Sidebar.Tree = filetree.New(treeRoot)
	a.ui.Sidebar.Tree.Reveal(first.Path()) // the file we start on is the one to highlight

	// A folder with no file open has nothing to edit yet: start in the tree,
	// pinned so a narrow terminal does not hide the only thing on screen.
	if l.dir != "" && len(l.files) == 0 {
		a.ui.Sidebar.Pinned = true
		a.ui.Sidebar.Focused = true
	}
}

// projectRoot finds the project containing a file. An empty path means an
// unnamed buffer, which belongs to wherever the editor was started.
func projectRoot(path string) string {
	if path == "" {
		if wd, err := os.Getwd(); err == nil {
			return wd
		}
		return "."
	}
	return projectRootFrom(filepath.Dir(path))
}

// projectRootFrom walks up from dir looking for a marker of a project root, so
// indexes and result paths are anchored somewhere meaningful rather than at
// whatever directory the editor happened to be started in.
//
// Markers are tiered, because a build file alone cannot mark a root: nearly
// every subdirectory of a C project carries a Makefile, so treating one as
// decisive anchored a kernel tree at fs/ rather than at its top, silently
// narrowing the built-in index and project search to a single subdirectory.
// A VCS directory, a module file, or an existing index is decisive; a build
// file is consulted only when the walk turns up nothing stronger.
func projectRootFrom(dir string) string {
	strong := []string{".git", ".hg", ".svn", "go.mod", "cscope.out", "tags"}
	weak := []string{"Makefile", "CMakeLists.txt"}

	fallback := ""
	for d := dir; ; {
		for _, m := range strong {
			if _, err := os.Stat(filepath.Join(d, m)); err == nil {
				return d
			}
		}
		// Remember only the nearest build file; it is used just as a last resort.
		if fallback == "" {
			for _, m := range weak {
				if _, err := os.Stat(filepath.Join(d, m)); err == nil {
					fallback = d
					break
				}
			}
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	if fallback != "" {
		return fallback
	}
	return dir
}
