package builtin

import (
	"bytes"
	"context"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sonarc-dev/sonarc/internal/index/provider"
)

// maxSymbolFiles bounds how many files have symbols extracted. Symbols are what
// cost memory — a kernel tree yields millions of them and gigabytes of strings —
// so this cap guards the machine, and hitting it is reported, not silent.
var maxSymbolFiles = 60_000

// maxListFiles bounds the file list alone, which is a path per file: a million
// paths is tens of megabytes. It exists only so pointing the editor at / cannot
// exhaust memory; real projects stay far below it. The old single 60,000 cap
// applied here too and hid files from fuzzy open on a kernel tree.
const maxListFiles = 1_000_000

// Index is a self-contained code index over a directory tree.
//
// Building happens in the background: the editor stays responsive and simply
// has no answers from this provider until the walk finishes.
type Index struct {
	root        string
	skipSymbols bool

	mu        sync.RWMutex
	syms      map[string][]provider.Symbol
	files     []string
	source    string // where the file list came from: "git" or "walk"
	truncated bool   // the file list or the symbol set stopped at a cap
	built     bool
	err       error

	buildOnce sync.Once
	done      chan struct{}
}

// New returns an index rooted at dir. Nothing is read until Build runs.
func New(root string) *Index {
	return &Index{root: root, done: make(chan struct{})}
}

// SkipSymbols limits the build to the file list, skipping symbol extraction.
//
// A tree the size of a kernel yields ~2.4M symbols and retains over 2 GB of
// strings, which is worth paying only when this index is the one answering
// definition queries. When cscope or a tags file is present they answer
// instead, and the file list alone still powers fuzzy open and project-wide
// text search. Must be called before Start.
func (ix *Index) SkipSymbols() { ix.skipSymbols = true }

// Name identifies the provider.
func (ix *Index) Name() string { return "builtin" }

// Available reports whether the index has finished building.
func (ix *Index) Available() bool {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.built
}

// Root returns the indexed directory.
func (ix *Index) Root() string { return ix.root }

// Files returns the indexed file paths. The walk already honors .gitignore and
// skips build output, so this is the project's source files rather than
// everything on disk.
func (ix *Index) Files() []string {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	out := make([]string, len(ix.files))
	copy(out, ix.files)
	return out
}

// Coverage reports where the file list came from and whether a cap cut the
// index short, so the status screen can say the index is incomplete.
func (ix *Index) Coverage() (source string, truncated bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.source, ix.truncated
}

// Stats reports how much was indexed, for the status line.
func (ix *Index) Stats() (files, symbols int) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.files), len(ix.syms)
}

// Close releases the index.
func (ix *Index) Close() error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.syms, ix.files, ix.built = nil, nil, false
	return nil
}

// Start builds the index in the background, at most once.
func (ix *Index) Start() {
	ix.buildOnce.Do(func() {
		go func() {
			defer close(ix.done)
			_ = ix.Build(context.Background())
		}()
	})
}

// Wait blocks until the background build finishes. Intended for tests.
func (ix *Index) Wait() { <-ix.done }

// Build walks the tree and extracts definitions.
//
// Files are parsed in parallel across the available cores: a walk of a large
// tree is dominated by reading and scanning, both of which parallelize cleanly.
func (ix *Index) Build(ctx context.Context) error {
	l, err := listFiles(ix.root, maxListFiles)
	if err != nil && len(l.files) == 0 {
		ix.mu.Lock()
		ix.err = err
		ix.mu.Unlock()
		return err
	}
	files := l.files

	// The file list alone is enough for fuzzy open and project text search.
	if ix.skipSymbols {
		ix.mu.Lock()
		ix.syms = make(map[string][]provider.Symbol)
		ix.files = files
		ix.source, ix.truncated = l.source, l.truncated
		ix.built = true
		ix.err = nil
		ix.mu.Unlock()
		return nil
	}

	// Symbols are extracted from at most maxSymbolFiles files. The whole list is
	// kept regardless, so fuzzy open and text search still see every file.
	symFiles := files
	truncated := l.truncated
	if len(symFiles) > maxSymbolFiles {
		symFiles, truncated = symFiles[:maxSymbolFiles], true
	}

	type result struct {
		path string
		syms []found
	}
	workers := runtime.GOMAXPROCS(0)
	jobs := make(chan string, workers*4)
	results := make(chan result, workers*4)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				if ctx.Err() != nil {
					return
				}
				lang, ok := languageOf(path)
				if !ok || lang == LangUnknown {
					continue // searchable, but nothing to extract
				}
				data, err := os.ReadFile(path)
				if err != nil || isBinary(data) {
					continue
				}
				var syms []found
				for num, line := range strings.Split(string(data), "\n") {
					syms = append(syms, scanLine(lang, strings.TrimRight(line, "\r"), num+1)...)
				}
				if len(syms) > 0 {
					results <- result{path, syms}
				}
			}
		}()
	}

	go func() {
	feed:
		for _, f := range symFiles {
			select {
			case jobs <- f:
			case <-ctx.Done():
				break feed // a bare break would only leave the select
			}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	syms := make(map[string][]provider.Symbol)
	for r := range results {
		for _, f := range r.syms {
			syms[f.name] = append(syms[f.name], provider.Symbol{
				Name:   f.name,
				Kind:   f.kind,
				Source: "builtin",
				Loc: provider.Location{
					Path: r.path,
					Line: f.line,
					Text: strings.TrimSpace(f.text),
				},
			})
		}
	}

	ix.mu.Lock()
	ix.syms = syms
	ix.files = files
	ix.source, ix.truncated = l.source, truncated
	ix.built = true
	ix.err = nil
	ix.mu.Unlock()
	return nil
}

// isBinary reports whether data looks like a binary file. A NUL byte in the
// first block is the same cheap test grep uses.
func isBinary(data []byte) bool {
	if len(data) > 8000 {
		data = data[:8000]
	}
	return bytes.IndexByte(data, 0) >= 0
}

// Definitions returns the indexed definitions of sym.
func (ix *Index) Definitions(ctx context.Context, sym string) ([]provider.Symbol, error) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	if !ix.built {
		return nil, nil
	}
	out := make([]provider.Symbol, len(ix.syms[sym]))
	copy(out, ix.syms[sym])
	return out, nil
}

// Search returns indexed symbols whose names start with prefix.
func (ix *Index) Search(ctx context.Context, prefix string, limit int) ([]provider.Symbol, error) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	if !ix.built || prefix == "" {
		return nil, nil
	}
	lower := strings.ToLower(prefix)
	var out []provider.Symbol
	for name, syms := range ix.syms {
		if !strings.HasPrefix(strings.ToLower(name), lower) {
			continue
		}
		out = append(out, syms...)
		if len(out) >= limit*4 {
			break // gather extra, then rank
		}
	}
	// Exact-prefix-case matches first, then shorter names: the symbol you meant
	// is far more often the short one.
	sort.SliceStable(out, func(i, j int) bool {
		ei := strings.HasPrefix(out[i].Name, prefix)
		ej := strings.HasPrefix(out[j].Name, prefix)
		if ei != ej {
			return ei
		}
		if len(out[i].Name) != len(out[j].Name) {
			return len(out[i].Name) < len(out[j].Name)
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// References finds every whole-word occurrence of sym across the tree.
func (ix *Index) References(ctx context.Context, sym string) ([]provider.Location, error) {
	return ix.Grep(ctx, sym, true)
}

// ScansForReferences marks References as a whole-tree scan, which the Registry
// leaves until last. It reads every indexed file for every query, and on a tree
// too big for the page cache that is bound by the disk, not the CPU.
func (ix *Index) ScansForReferences() bool { return true }

// Grep searches every indexed file for pattern.
//
// This is a plain literal scan rather than a regex engine: bytes.Index is
// vectorized by the runtime, and searching for an identifier is what navigation
// actually needs. Files are scanned in parallel across cores.
//
// If ctx ends before every file has been read, the hits found so far are
// returned together with a *provider.PartialError saying how much was covered,
// so the caller can tell an incomplete answer from a complete one.
func (ix *Index) Grep(ctx context.Context, pattern string, wholeWord bool) ([]provider.Location, error) {
	if pattern == "" {
		return nil, nil
	}
	needle := []byte(pattern)
	return ix.scan(ctx, func(path string, data []byte) []provider.Location {
		return scanFile(path, data, needle, wholeWord)
	})
}

// GrepRegexp is Grep for a regular expression, matched a line at a time as
// git grep does, so ^ and $ mean the start and end of a line.
func (ix *Index) GrepRegexp(ctx context.Context, re *regexp.Regexp) ([]provider.Location, error) {
	return ix.scan(ctx, func(path string, data []byte) []provider.Location {
		var out []provider.Location
		for n, rest := 1, data; len(rest) > 0; n++ {
			line := rest
			if i := bytes.IndexByte(rest, '\n'); i >= 0 {
				line, rest = rest[:i], rest[i+1:]
			} else {
				rest = nil
			}
			line = bytes.TrimSuffix(line, []byte{'\r'})
			if re.Match(line) {
				out = append(out, provider.Location{Path: path, Line: n, Text: strings.TrimSpace(string(line))})
			}
		}
		return out
	})
}

// scan runs match over every indexed file in parallel and gathers the hits in
// path and line order; see Grep for how a deadline is reported.
func (ix *Index) scan(ctx context.Context, match func(path string, data []byte) []provider.Location) ([]provider.Location, error) {
	ix.mu.RLock()
	files := ix.files
	built := ix.built
	ix.mu.RUnlock()
	if !built {
		return nil, nil
	}

	var scanned atomic.Int64
	workers := runtime.GOMAXPROCS(0)
	jobs := make(chan string, workers*4)
	out := make(chan []provider.Location, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var local []provider.Location
			for path := range jobs {
				if ctx.Err() != nil {
					break
				}
				data, err := os.ReadFile(path)
				scanned.Add(1)
				if err != nil || isBinary(data) {
					continue
				}
				local = append(local, match(path, data)...)
			}
			out <- local
		}()
	}
	go func() {
		for _, f := range files {
			select {
			case jobs <- f:
			case <-ctx.Done():
			}
		}
		close(jobs)
		wg.Wait()
		close(out)
	}()

	var all []provider.Location
	for chunk := range out {
		all = append(all, chunk...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Path != all[j].Path {
			return all[i].Path < all[j].Path
		}
		return all[i].Line < all[j].Line
	})
	if err := ctx.Err(); err != nil {
		done := int(scanned.Load())
		if done >= len(files) {
			return all, nil // it finished before the deadline was noticed
		}
		return all, &provider.PartialError{Done: done, Total: len(files), Cause: err}
	}
	return all, nil
}

// scanFile finds each line of data containing needle.
func scanFile(path string, data, needle []byte, wholeWord bool) []provider.Location {
	var out []provider.Location
	line := 1
	lineStart := 0
	pos := 0

	for {
		i := bytes.Index(data[pos:], needle)
		if i < 0 {
			break
		}
		abs := pos + i

		// Advance the line counter over everything skipped.
		for j := lineStart; j < abs; j++ {
			if data[j] == '\n' {
				line++
				lineStart = j + 1
			}
		}
		if !wholeWord || isWordBoundary(data, abs, len(needle)) {
			end := bytes.IndexByte(data[abs:], '\n')
			if end < 0 {
				end = len(data)
			} else {
				end += abs
			}
			text := strings.TrimSpace(string(data[lineStart:end]))
			// One hit per line is enough for a results list.
			if n := len(out); n == 0 || out[n-1].Line != line {
				out = append(out, provider.Location{Path: path, Line: line, Text: text})
			}
		}
		pos = abs + len(needle)
	}
	return out
}

// isWordBoundary reports whether the match at off is a complete identifier
// rather than part of a longer one, so searching for "read" does not report
// every "thread".
func isWordBoundary(data []byte, off, n int) bool {
	if off > 0 && isIdentChar(data[off-1]) {
		return false
	}
	if end := off + n; end < len(data) && isIdentChar(data[end]) {
		return false
	}
	return true
}
