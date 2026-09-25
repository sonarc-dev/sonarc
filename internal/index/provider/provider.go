// Package provider defines how sonarc answers "where is this symbol defined"
// and "who uses it", independently of what is available to answer it.
//
// Three implementations exist: cscope (richest, needs a cscope database), ctags
// (definitions and completion, needs a tags file), and a built-in indexer that
// needs nothing at all. A Registry queries them in priority order and merges
// the results, so the editor behaves the same whether or not the machine has
// the external tools installed.
//
// This indirection also exists so a language server can be added later as a
// fourth provider without touching anything that calls into here.
package provider

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Kind classifies a symbol. It mirrors the ctags kind letters, which cscope and
// the built-in indexer are mapped onto.
type Kind uint8

const (
	KindUnknown Kind = iota
	KindFunction
	KindVariable
	KindMacro
	KindStruct
	KindUnion
	KindEnum
	KindEnumerator
	KindTypedef
	KindMember
	KindClass
	KindInterface
	KindPackage
	KindFile
)

// String returns a short label suitable for a picker column.
func (k Kind) String() string {
	switch k {
	case KindFunction:
		return "func"
	case KindVariable:
		return "var"
	case KindMacro:
		return "macro"
	case KindStruct:
		return "struct"
	case KindUnion:
		return "union"
	case KindEnum:
		return "enum"
	case KindEnumerator:
		return "enumerator"
	case KindTypedef:
		return "typedef"
	case KindMember:
		return "member"
	case KindClass:
		return "class"
	case KindInterface:
		return "interface"
	case KindPackage:
		return "package"
	case KindFile:
		return "file"
	}
	return ""
}

// KindFromCtags maps a ctags kind letter to a Kind. The letters are shared
// across most languages ctags supports.
func KindFromCtags(s string) Kind {
	if s == "" {
		return KindUnknown
	}
	switch s[0] {
	case 'f':
		return KindFunction
	case 'v':
		return KindVariable
	case 'd':
		return KindMacro
	case 's':
		return KindStruct
	case 'u':
		return KindUnion
	case 'g':
		return KindEnum
	case 'e':
		return KindEnumerator
	case 't':
		return KindTypedef
	case 'm':
		return KindMember
	case 'c':
		return KindClass
	case 'i':
		return KindInterface
	case 'p':
		// 'p' is a function prototype in C and a package in Go; both are
		// reasonably described as a declaration of a callable or a package.
		return KindFunction
	case 'F':
		return KindFile
	}
	return KindUnknown
}

// Location is a place in the codebase.
type Location struct {
	Path string // absolute where possible
	Line int    // 1-based; 0 when unknown
	Col  int    // 1-based; 0 when unknown
	Text string // the source line, for preview in a results list
}

// Symbol is a definition found by a provider.
type Symbol struct {
	Name      string
	Kind      Kind
	Loc       Location
	Signature string // e.g. the argument list, when the provider supplies one
	Scope     string // enclosing struct, class, or namespace
	Source    string // which provider produced this, shown in the picker
}

// CodeIntel answers navigation queries. Every method must respect ctx: these
// run against databases large enough that a query can take seconds, and the
// editor cancels them when the user moves on.
type CodeIntel interface {
	// Name identifies the provider in the UI and in error messages.
	Name() string

	// Available reports whether this provider can currently answer queries. A
	// provider whose database is missing returns false rather than erroring on
	// every call.
	Available() bool

	// Definitions finds where sym is defined.
	Definitions(ctx context.Context, sym string) ([]Symbol, error)

	// References finds where sym is used.
	References(ctx context.Context, sym string) ([]Location, error)

	// Search finds symbols matching a prefix, for the symbol picker. limit
	// bounds the result count; a codebase can contain millions of tags.
	Search(ctx context.Context, prefix string, limit int) ([]Symbol, error)

	// Close releases any resources, such as a subprocess.
	Close() error
}

// CallGraph is implemented by providers that can walk a call graph. Only cscope
// can do this today, so callers type-assert for it rather than assuming.
type CallGraph interface {
	Callers(ctx context.Context, fn string) ([]Location, error)
	Callees(ctx context.Context, fn string) ([]Location, error)
}

// TextSearcher is implemented by providers that can search file contents
// through their own index, which is far faster than walking the tree.
type TextSearcher interface {
	Grep(ctx context.Context, pattern string) ([]Location, error)
}

// ReferenceScanner is implemented by a provider whose reference search reads
// files across the whole project instead of consulting an index, so its cost
// grows with the size of the tree rather than with the answer. On a kernel
// checkout that is the difference between 0.4 s and over a minute, so the
// Registry only asks such a provider when an indexed one had nothing to say.
type ReferenceScanner interface {
	ScansForReferences() bool
}

// PartialError accompanies an incomplete answer: a scan that ran out of time
// after covering only part of the tree. The results returned alongside it are
// real but not exhaustive, and the caller must say so rather than present them
// as everything there is.
type PartialError struct {
	Done, Total int   // units covered out of units in the whole (files, for a scan)
	Cause       error // why it stopped, usually a deadline
}

// ErrTooManyResults is the Cause of a PartialError from a search that stopped
// because it had found more than anyone could read, rather than running out of
// time.
var ErrTooManyResults = errors.New("too many matches")

func (e *PartialError) Error() string {
	if e.Total == 0 {
		return fmt.Sprintf("incomplete: %v", e.Cause)
	}
	return fmt.Sprintf("incomplete: %d of %d files searched: %v", e.Done, e.Total, e.Cause)
}

func (e *PartialError) Unwrap() error { return e.Cause }

// Timing records what one provider did for one query.
type Timing struct {
	Provider string
	Elapsed  time.Duration
	Count    int
	Err      error         // the provider failed outright
	Skipped  bool          // deliberately not asked; see ReferenceScanner
	Partial  *PartialError // the provider stopped early; Count is what it found
}

// Report explains how a query was answered: who was asked, how long each took,
// and whether anything is missing. It exists so a slow or incomplete answer is
// visible in the UI instead of something to be inferred.
type Report struct {
	Timings []Timing
}

// Partial reports whether any provider's answer is known to be incomplete.
func (r Report) Partial() bool {
	for _, t := range r.Timings {
		if t.Partial != nil {
			return true
		}
	}
	return false
}

// SkippedScan reports whether a whole-tree scan was left out because an index
// had already answered.
func (r Report) SkippedScan() bool {
	for _, t := range r.Timings {
		if t.Skipped {
			return true
		}
	}
	return false
}

// Summary renders the report as one short line, e.g.
// "cscope 0.4s · scan 15.0s partial (21,340 of 60,000 files)".
func (r Report) Summary() string {
	var parts []string
	for _, t := range r.Timings {
		switch {
		case t.Skipped:
			continue // reported by SkippedScan; the caller words the hint
		case t.Partial != nil:
			parts = append(parts, fmt.Sprintf("%s %s partial (%s of %s files)",
				t.Provider, FormatDuration(t.Elapsed), commas(t.Partial.Done), commas(t.Partial.Total)))
		case t.Err != nil:
			parts = append(parts, fmt.Sprintf("%s failed: %v", t.Provider, shortErr(t.Err)))
		default:
			parts = append(parts, fmt.Sprintf("%s %s", t.Provider, FormatDuration(t.Elapsed)))
		}
	}
	return strings.Join(parts, " · ")
}

// FormatDuration renders a duration compactly: milliseconds under a second,
// tenths of a second under a minute, and minutes and seconds above that — an
// index rebuild's "25m3s" reads at a glance where "1503.0s" does not.
func FormatDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	d = d.Truncate(time.Second)
	return fmt.Sprintf("%dm%ds", int(d/time.Minute), int(d%time.Minute/time.Second))
}

func commas(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func shortErr(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return err.Error()
}

// Registry queries several providers and merges their answers.
//
// Order matters: providers are consulted in the order registered, and that
// order encodes quality. A definition from cscope outranks one from the
// built-in regex indexer, so it is offered first.
type Registry struct {
	mu        sync.RWMutex
	providers []CodeIntel
}

// Add registers a provider. Register the most authoritative first. It is safe
// to call while queries are running on other goroutines, which is what happens
// when an index rebuild finishes during a search.
func (r *Registry) Add(p CodeIntel) {
	if p != nil {
		r.mu.Lock()
		r.providers = append(r.providers, p)
		r.mu.Unlock()
	}
}

// Set replaces the providers, most authoritative first. An index rebuild uses
// it to put a newly created cscope database ahead of the built-in indexer
// rather than behind it, which is where Add would leave it.
func (r *Registry) Set(ps ...CodeIntel) {
	var keep []CodeIntel
	for _, p := range ps {
		if p != nil {
			keep = append(keep, p)
		}
	}
	r.mu.Lock()
	r.providers = keep
	r.mu.Unlock()
}

// list returns a snapshot of the providers, so a query iterates a stable slice
// even if Add runs meanwhile.
func (r *Registry) list() []CodeIntel {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]CodeIntel(nil), r.providers...)
}

// Providers returns the registered providers, for status display.
func (r *Registry) Providers() []CodeIntel { return r.list() }

// Available returns the providers that can currently answer queries.
func (r *Registry) Available() []CodeIntel {
	var out []CodeIntel
	for _, p := range r.list() {
		if p.Available() {
			out = append(out, p)
		}
	}
	return out
}

// Close shuts down every provider.
func (r *Registry) Close() error {
	var firstErr error
	for _, p := range r.list() {
		if err := p.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Definitions asks each available provider in turn and returns the merged,
// deduplicated result.
//
// Every provider is asked rather than stopping at the first hit: ctags and
// cscope disagree often enough in real C codebases (macros, static functions,
// generated code) that showing the union and letting the user pick beats
// silently trusting one of them.
func (r *Registry) Definitions(ctx context.Context, sym string) []Symbol {
	var out []Symbol
	for _, p := range r.list() {
		if !p.Available() {
			continue
		}
		syms, err := p.Definitions(ctx, sym)
		if err != nil {
			continue // a broken provider must not break navigation
		}
		for i := range syms {
			if syms[i].Source == "" {
				syms[i].Source = p.Name()
			}
		}
		out = append(out, syms...)
		if ctx.Err() != nil {
			break
		}
	}
	return dedupeSymbols(out)
}

// References asks the providers and merges the results. See ReferencesReport.
func (r *Registry) References(ctx context.Context, sym string) []Location {
	locs, _ := r.ReferencesReport(ctx, sym)
	return locs
}

// ReferencesReport finds uses of sym and reports how the answer was obtained.
//
// Indexed providers (cscope) are asked first. A provider that has to scan the
// whole tree is asked only if they found nothing: on a kernel tree cscope
// answers in under half a second while a scan takes over a minute, and running
// both made every query wait out the scan to add lines the index had already
// judged irrelevant. The scan is still available on demand as a text search.
//
// A provider that stops early keeps what it found and is marked partial rather
// than having its results discarded; dropping them silently made an
// incomplete answer indistinguishable from a complete one.
func (r *Registry) ReferencesReport(ctx context.Context, sym string) ([]Location, Report) {
	var out []Location
	var rep Report

	ask := func(p CodeIntel) int {
		start := time.Now()
		locs, err := p.References(ctx, sym)
		t := Timing{Provider: p.Name(), Elapsed: time.Since(start)}

		var pe *PartialError
		switch {
		case err == nil:
		case errors.As(err, &pe):
			t.Partial = pe
		case len(locs) > 0 && ctx.Err() != nil:
			t.Partial = &PartialError{Cause: err}
		default:
			t.Err, locs = err, nil // a broken provider must not break navigation
		}
		t.Count = len(locs)
		rep.Timings = append(rep.Timings, t)
		out = append(out, locs...)
		return len(locs)
	}

	var scanners []CodeIntel
	found := 0
	for _, p := range r.list() {
		if !p.Available() {
			continue
		}
		if rs, ok := p.(ReferenceScanner); ok && rs.ScansForReferences() {
			scanners = append(scanners, p)
			continue
		}
		found += ask(p)
		if ctx.Err() != nil {
			break
		}
	}
	for _, p := range scanners {
		switch {
		case found > 0:
			rep.Timings = append(rep.Timings, Timing{Provider: p.Name(), Skipped: true})
		case ctx.Err() != nil:
			// Out of time before it could start; nothing useful to report.
		default:
			found += ask(p)
		}
	}
	return dedupeLocations(out), rep
}

// Search collects symbols matching a prefix across providers, stopping once
// limit results have been gathered.
func (r *Registry) Search(ctx context.Context, prefix string, limit int) []Symbol {
	var out []Symbol
	for _, p := range r.list() {
		if !p.Available() || len(out) >= limit {
			continue
		}
		syms, err := p.Search(ctx, prefix, limit-len(out))
		if err != nil {
			continue
		}
		for i := range syms {
			if syms[i].Source == "" {
				syms[i].Source = p.Name()
			}
		}
		out = append(out, syms...)
		if ctx.Err() != nil {
			break
		}
	}
	out = dedupeSymbols(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Callers returns functions calling fn, from the first provider that can walk a
// call graph.
func (r *Registry) Callers(ctx context.Context, fn string) ([]Location, bool) {
	for _, p := range r.list() {
		cg, ok := p.(CallGraph)
		if !ok || !p.Available() {
			continue
		}
		if locs, err := cg.Callers(ctx, fn); err == nil {
			return dedupeLocations(locs), true
		}
	}
	return nil, false
}

// Callees returns functions called by fn, from the first provider that can walk
// a call graph.
func (r *Registry) Callees(ctx context.Context, fn string) ([]Location, bool) {
	for _, p := range r.list() {
		cg, ok := p.(CallGraph)
		if !ok || !p.Available() {
			continue
		}
		if locs, err := cg.Callees(ctx, fn); err == nil {
			return dedupeLocations(locs), true
		}
	}
	return nil, false
}

// key identifies a location for deduplication.
type key struct {
	path string
	line int
}

func dedupeLocations(in []Location) []Location {
	seen := make(map[key]struct{}, len(in))
	out := in[:0]
	for _, l := range in {
		k := key{l.Path, l.Line}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, l)
	}
	sortLocations(out)
	return out
}

// dedupeSymbols removes duplicates by location, keeping the first occurrence,
// which is the one from the highest-priority provider.
func dedupeSymbols(in []Symbol) []Symbol {
	seen := make(map[key]struct{}, len(in))
	out := in[:0]
	for _, s := range in {
		k := key{s.Loc.Path, s.Loc.Line}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, s)
	}
	return out
}

func sortLocations(l []Location) {
	sort.SliceStable(l, func(i, j int) bool {
		if l[i].Path != l[j].Path {
			return l[i].Path < l[j].Path
		}
		return l[i].Line < l[j].Line
	})
}

// IsSymbolChar reports whether b can appear in an identifier. Used to pull the
// word under the cursor for a navigation query.
func IsSymbolChar(b byte) bool {
	return b == '_' ||
		b >= 'a' && b <= 'z' ||
		b >= 'A' && b <= 'Z' ||
		b >= '0' && b <= '9'
}

// SymbolAt extracts the identifier surrounding byte offset col in line. It
// returns an empty string when the cursor is not on an identifier.
func SymbolAt(line []byte, col int) string {
	if col < 0 || col > len(line) {
		return ""
	}
	// A cursor sitting just past the end of a word should still find it, which
	// is what happens when you double-click or arrow to the end of a name.
	if col == len(line) || !IsSymbolChar(line[col]) {
		if col == 0 || !IsSymbolChar(line[col-1]) {
			return ""
		}
		col--
	}
	start := col
	for start > 0 && IsSymbolChar(line[start-1]) {
		start--
	}
	end := col
	for end < len(line) && IsSymbolChar(line[end]) {
		end++
	}
	// A bare number is not a symbol worth looking up.
	if s := string(line[start:end]); !isAllDigits(s) {
		return s
	}
	return ""
}

func isAllDigits(s string) bool {
	if s == "" {
		return true
	}
	return strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' }) < 0
}
