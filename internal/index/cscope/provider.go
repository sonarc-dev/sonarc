package cscope

import (
	"context"
	"sync"

	"github.com/sonarc-dev/sonarc/internal/index/provider"
)

// Provider adapts a cscope database to the CodeIntel interface.
//
// It also implements provider.CallGraph and provider.TextSearcher, which no
// other provider can: walking callers and callees is the reason cscope is worth
// the trouble on a large C codebase.
type Provider struct {
	mu sync.RWMutex
	db *DB
}

// Discover locates and opens a cscope database for dir, returning nil when
// there is none or cscope is not installed.
func Discover(dir string) *Provider {
	if !Available() {
		return nil
	}
	path, ok := Find(dir)
	if !ok {
		return nil
	}
	db, err := Open(path)
	if err != nil {
		return nil
	}
	return &Provider{db: db}
}

// Name identifies the provider.
func (p *Provider) Name() string { return "cscope" }

// Available reports whether a database is connected.
func (p *Provider) Available() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.db != nil
}

// Path returns the database file in use.
func (p *Provider) Path() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.db == nil {
		return ""
	}
	return p.db.Path()
}

// Dir returns the directory the database covers.
func (p *Provider) Dir() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.db == nil {
		return ""
	}
	return p.db.Dir()
}

// Close shuts down the database connection.
func (p *Provider) Close() error {
	p.mu.Lock()
	db := p.db
	p.db = nil
	p.mu.Unlock()
	if db == nil {
		return nil
	}
	return db.Close()
}

// Reload reconnects to the database, picking up a rebuilt index.
func (p *Provider) Reload() error {
	p.mu.RLock()
	old := p.db
	p.mu.RUnlock()
	if old == nil {
		return nil
	}
	db, err := Open(old.Path())
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.db = db
	p.mu.Unlock()
	old.Close()
	return nil
}

// query runs a cscope search, returning nothing when no database is connected.
func (p *Provider) query(ctx context.Context, q Query, term string) ([]Result, error) {
	p.mu.RLock()
	db := p.db
	p.mu.RUnlock()
	if db == nil {
		return nil, nil
	}
	return db.Query(ctx, q, term)
}

// Query exposes an arbitrary cscope search, for the query commands that have no
// equivalent in the generic interface.
func (p *Provider) Query(ctx context.Context, q Query, term string) ([]provider.Location, error) {
	res, err := p.query(ctx, q, term)
	if err != nil {
		return nil, err
	}
	return toLocations(res), nil
}

// Definitions finds where sym is defined.
func (p *Provider) Definitions(ctx context.Context, sym string) ([]provider.Symbol, error) {
	res, err := p.query(ctx, FindDefinition, sym)
	if err != nil {
		return nil, err
	}
	out := make([]provider.Symbol, 0, len(res))
	for _, r := range res {
		out = append(out, provider.Symbol{
			Name:   sym,
			Kind:   provider.KindUnknown, // cscope does not report a kind
			Scope:  scopeOf(r.Function),
			Source: "cscope",
			Loc:    toLocation(r),
		})
	}
	return out, nil
}

// References finds every use of sym.
func (p *Provider) References(ctx context.Context, sym string) ([]provider.Location, error) {
	res, err := p.query(ctx, FindSymbol, sym)
	if err != nil {
		return nil, err
	}
	return toLocations(res), nil
}

// Search finds symbols by name. cscope has no prefix search, so this matches
// the name exactly via its definition index.
func (p *Provider) Search(ctx context.Context, prefix string, limit int) ([]provider.Symbol, error) {
	if prefix == "" {
		return nil, nil
	}
	syms, err := p.Definitions(ctx, prefix)
	if err != nil {
		return nil, err
	}
	if len(syms) > limit {
		syms = syms[:limit]
	}
	return syms, nil
}

// Callers returns the functions that call fn.
func (p *Provider) Callers(ctx context.Context, fn string) ([]provider.Location, error) {
	res, err := p.query(ctx, FindCallers, fn)
	if err != nil {
		return nil, err
	}
	return toLocations(res), nil
}

// Callees returns the functions fn calls.
func (p *Provider) Callees(ctx context.Context, fn string) ([]provider.Location, error) {
	res, err := p.query(ctx, FindCallees, fn)
	if err != nil {
		return nil, err
	}
	return toLocations(res), nil
}

// Grep searches file contents with cscope's text-string query.
//
// Despite going through cscope this is not an indexed lookup: cscope reads the
// source files itself, single-threaded. On a kernel tree that took 35 s where
// a reference query took 0.4 s, so callers must give it a deadline and be ready
// for it to run out.
func (p *Provider) Grep(ctx context.Context, pattern string) ([]provider.Location, error) {
	res, err := p.query(ctx, FindText, pattern)
	if err != nil {
		return nil, err
	}
	return toLocations(res), nil
}

// scopeOf normalizes cscope's function column, which reads "<global>" for
// file-scope matches.
func scopeOf(fn string) string {
	if fn == "<global>" {
		return ""
	}
	return fn
}

func toLocation(r Result) provider.Location {
	return provider.Location{Path: r.File, Line: r.Line, Text: r.Text}
}

func toLocations(res []Result) []provider.Location {
	out := make([]provider.Location, 0, len(res))
	for _, r := range res {
		out = append(out, toLocation(r))
	}
	return out
}
