package tags

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"sonarc/internal/index/provider"
)

// candidateNames are the file names searched for, in the order ctags and vim
// conventionally use.
var candidateNames = []string{"tags", ".tags", "TAGS"}

// Find locates a tags file at or above dir, the way vim's "tags=./tags;"
// setting does, so opening a file deep in a tree still finds the index at its
// root.
func Find(dir string) (string, bool) {
	d, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	for {
		for _, name := range candidateNames {
			p := filepath.Join(d, name)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Size() > 0 {
				return p, true
			}
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", false
		}
		d = parent
	}
}

// Provider adapts a tags file to the CodeIntel interface.
type Provider struct {
	mu      sync.RWMutex
	r       *Reader
	path    string
	modTime time.Time
}

// NewProvider opens the tags file at path.
func NewProvider(path string) (*Provider, error) {
	r, err := Open(path)
	if err != nil {
		return nil, err
	}
	p := &Provider{r: r, path: path}
	if fi, err := os.Stat(path); err == nil {
		p.modTime = fi.ModTime()
	}
	return p, nil
}

// Discover finds and opens a tags file for dir, returning nil if there is none.
func Discover(dir string) *Provider {
	path, ok := Find(dir)
	if !ok {
		return nil
	}
	p, err := NewProvider(path)
	if err != nil {
		return nil
	}
	return p
}

// Name identifies the provider.
func (p *Provider) Name() string { return "ctags" }

// Available reports whether a tags file is loaded.
func (p *Provider) Available() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.r != nil
}

// Path returns the tags file in use.
func (p *Provider) Path() string { return p.path }

// Stale reports whether the tags file has changed on disk since it was opened,
// so the editor can offer to reload rather than silently serving stale jumps.
func (p *Provider) Stale() bool {
	p.mu.RLock()
	mt := p.modTime
	p.mu.RUnlock()
	fi, err := os.Stat(p.path)
	if err != nil {
		return false
	}
	return fi.ModTime().After(mt)
}

// Reload reopens the tags file, picking up a regenerated index.
func (p *Provider) Reload() error {
	r, err := Open(p.path)
	if err != nil {
		return err
	}
	p.mu.Lock()
	old := p.r
	p.r = r
	if fi, err := os.Stat(p.path); err == nil {
		p.modTime = fi.ModTime()
	}
	p.mu.Unlock()
	if old != nil {
		old.Close()
	}
	return nil
}

// Close releases the tags file.
func (p *Provider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.r == nil {
		return nil
	}
	err := p.r.Close()
	p.r = nil
	return err
}

// Definitions returns every definition of sym.
func (p *Provider) Definitions(ctx context.Context, sym string) ([]provider.Symbol, error) {
	p.mu.RLock()
	r := p.r
	p.mu.RUnlock()
	if r == nil {
		return nil, nil
	}
	entries, err := r.Lookup(sym)
	if err != nil {
		return nil, err
	}
	return p.toSymbols(r, entries), nil
}

// References is not supported: a tags file records definitions only. Returning
// nothing lets the registry fall through to a provider that can answer.
func (p *Provider) References(ctx context.Context, sym string) ([]provider.Location, error) {
	return nil, nil
}

// Search returns symbols whose names begin with prefix.
func (p *Provider) Search(ctx context.Context, prefix string, limit int) ([]provider.Symbol, error) {
	p.mu.RLock()
	r := p.r
	p.mu.RUnlock()
	if r == nil || prefix == "" {
		return nil, nil
	}
	entries, err := r.Prefix(prefix, limit)
	if err != nil {
		return nil, err
	}
	return p.toSymbols(r, entries), nil
}

// toSymbols converts tag entries into symbols, resolving each address to a real
// line number.
func (p *Provider) toSymbols(r *Reader, entries []Entry) []provider.Symbol {
	out := make([]provider.Symbol, 0, len(entries))
	for _, e := range entries {
		path := r.Resolve(e)
		line, text := resolveAddress(path, e)
		out = append(out, provider.Symbol{
			Name:      e.Name,
			Kind:      provider.KindFromCtags(e.Kind),
			Signature: e.Signature,
			Scope:     e.Scope,
			Source:    "ctags",
			Loc: provider.Location{
				Path: path,
				Line: line,
				Text: text,
			},
		})
	}
	return out
}

// maxPatternScan bounds how much of a file is read looking for a tag pattern,
// so a stale tags file pointing at a huge generated file cannot stall a jump.
const maxPatternScan = 200_000

// resolveAddress turns a tag address into a concrete line number.
//
// ctags emits a search pattern rather than a line number precisely because
// files get edited: the pattern still finds the definition after lines have
// shifted. The recorded line is used as a hint and the search radiates outward
// from it, so the nearest match wins and a common function name does not jump
// to the wrong copy.
func resolveAddress(path string, e Entry) (int, string) {
	if e.Pattern == "" {
		return e.Line, ""
	}
	f, err := os.Open(path)
	if err != nil {
		return e.Line, "" // fall back to the recorded line
	}
	defer f.Close()

	want := strings.TrimSpace(e.Pattern)
	var (
		sc        = bufio.NewScanner(f)
		best      int
		bestText  string
		bestDelta = 1 << 30
		lineNo    int
	)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		lineNo++
		if lineNo > maxPatternScan {
			break
		}
		text := sc.Text()
		if strings.TrimSpace(text) != want {
			continue
		}
		delta := lineNo - e.Line
		if delta < 0 {
			delta = -delta
		}
		if delta < bestDelta {
			best, bestText, bestDelta = lineNo, text, delta
			if delta == 0 {
				break // exactly where the tags file said; stop looking
			}
		}
	}
	if best == 0 {
		// The definition moved or the pattern no longer matches. The recorded
		// line is the best remaining guess.
		return e.Line, ""
	}
	return best, bestText
}

// CtagsAvailable reports whether a usable ctags is installed.
//
// BSD ctags, which ships with macOS as /usr/bin/ctags, cannot produce a tags
// file sonarc can use: it has no --version, no recursion, and no extension
// fields. It is detected here so the editor can point at universal-ctags rather
// than generating something broken. Reading a tags file that BSD ctags produced
// still works; only generation is refused.
func CtagsAvailable() (exe string, ok bool) {
	exe, _, ok = DetectCtags()
	return exe, ok
}

// DetectCtags finds a ctags able to generate a usable file, and reports
// whether it is Universal Ctags. The two spell some options differently —
// Universal takes --extras, Exuberant only --extra — and Exuberant 5.9 is what
// ships on current Ubuntu LTS, so passing the wrong spelling fails the whole
// run.
func DetectCtags() (exe string, universal bool, ok bool) {
	for _, name := range []string{"ctags-universal", "uctags", "exctags", "ctags"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		out, err := exec.Command(path, "--version").Output()
		if err != nil {
			continue // BSD ctags exits non-zero on --version
		}
		v := string(out)
		switch {
		case strings.Contains(v, "Universal Ctags"):
			return path, true, true
		case strings.Contains(v, "Exuberant Ctags"):
			return path, false, true
		}
	}
	return "", false, false
}
