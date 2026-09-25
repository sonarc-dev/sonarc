package provider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fake is a scriptable provider used to test the registry's merging rules.
type fake struct {
	name      string
	available bool
	defs      []Symbol
	refs      []Location
	err       error
	calls     int
}

func (f *fake) Name() string    { return f.name }
func (f *fake) Available() bool { return f.available }
func (f *fake) Close() error    { return nil }

func (f *fake) Definitions(context.Context, string) ([]Symbol, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.defs, nil
}

func (f *fake) References(context.Context, string) ([]Location, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.refs, nil
}

func (f *fake) Search(_ context.Context, _ string, limit int) ([]Symbol, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if len(f.defs) > limit {
		return f.defs[:limit], nil
	}
	return f.defs, nil
}

// callGraphFake also answers caller/callee queries, as cscope does.
type callGraphFake struct {
	fake
	callers []Location
}

func (f *callGraphFake) Callers(context.Context, string) ([]Location, error) {
	return f.callers, nil
}
func (f *callGraphFake) Callees(context.Context, string) ([]Location, error) {
	return nil, nil
}

func sym(name, path string, line int) Symbol {
	return Symbol{Name: name, Loc: Location{Path: path, Line: line}}
}

// Results from a higher-priority provider must come first, because that order
// is what decides which definition the editor offers.
func TestRegistryPreservesProviderPriority(t *testing.T) {
	high := &fake{name: "cscope", available: true, defs: []Symbol{sym("f", "a.c", 1)}}
	low := &fake{name: "builtin", available: true, defs: []Symbol{sym("f", "b.c", 2)}}

	var r Registry
	r.Add(high)
	r.Add(low)

	got := r.Definitions(context.Background(), "f")
	if len(got) != 2 {
		t.Fatalf("got %d definitions, want 2", len(got))
	}
	if got[0].Source != "cscope" {
		t.Errorf("first result came from %q, want the highest-priority provider", got[0].Source)
	}
	if got[1].Source != "builtin" {
		t.Errorf("second result came from %q", got[1].Source)
	}
}

// Providers frequently report the same definition; showing it twice would make
// the picker confusing.
func TestRegistryDeduplicatesByLocation(t *testing.T) {
	a := &fake{name: "cscope", available: true, defs: []Symbol{sym("f", "a.c", 10)}}
	b := &fake{name: "ctags", available: true, defs: []Symbol{sym("f", "a.c", 10)}}

	var r Registry
	r.Add(a)
	r.Add(b)

	got := r.Definitions(context.Background(), "f")
	if len(got) != 1 {
		t.Fatalf("got %d definitions, want 1 after dedup: %+v", len(got), got)
	}
	if got[0].Source != "cscope" {
		t.Errorf("dedup kept the %q result, want the higher-priority one", got[0].Source)
	}
}

// A provider that is failing must not take navigation down with it: the others
// still have answers.
func TestBrokenProviderDoesNotBreakNavigation(t *testing.T) {
	broken := &fake{name: "cscope", available: true, err: errors.New("database corrupt")}
	working := &fake{name: "builtin", available: true, defs: []Symbol{sym("f", "b.c", 3)}}

	var r Registry
	r.Add(broken)
	r.Add(working)

	got := r.Definitions(context.Background(), "f")
	if len(got) != 1 {
		t.Fatalf("got %d definitions, want the one from the working provider", len(got))
	}
	if got[0].Loc.Path != "b.c" {
		t.Errorf("result came from %q", got[0].Loc.Path)
	}
}

func TestUnavailableProvidersAreNotQueried(t *testing.T) {
	off := &fake{name: "cscope", available: false, defs: []Symbol{sym("f", "a.c", 1)}}
	on := &fake{name: "builtin", available: true, defs: []Symbol{sym("f", "b.c", 1)}}

	var r Registry
	r.Add(off)
	r.Add(on)

	got := r.Definitions(context.Background(), "f")
	if off.calls != 0 {
		t.Errorf("an unavailable provider was queried %d times", off.calls)
	}
	if len(got) != 1 || got[0].Loc.Path != "b.c" {
		t.Errorf("got %+v, want only the available provider's result", got)
	}
}

func TestReferencesAreSortedAndDeduplicated(t *testing.T) {
	a := &fake{name: "a", available: true, refs: []Location{
		{Path: "z.c", Line: 5}, {Path: "a.c", Line: 10},
	}}
	b := &fake{name: "b", available: true, refs: []Location{
		{Path: "a.c", Line: 10}, // duplicate
		{Path: "a.c", Line: 2},
	}}

	var r Registry
	r.Add(a)
	r.Add(b)

	got := r.References(context.Background(), "f")
	if len(got) != 3 {
		t.Fatalf("got %d references, want 3 after dedup: %+v", len(got), got)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Path > got[i].Path ||
			(got[i-1].Path == got[i].Path && got[i-1].Line > got[i].Line) {
			t.Errorf("references are not sorted: %+v", got)
			break
		}
	}
}

func TestSearchHonorsLimit(t *testing.T) {
	many := make([]Symbol, 20)
	for i := range many {
		many[i] = sym("f", "a.c", i+1)
	}
	var r Registry
	r.Add(&fake{name: "x", available: true, defs: many})

	got := r.Search(context.Background(), "f", 5)
	if len(got) != 5 {
		t.Errorf("got %d results, want 5", len(got))
	}
}

// Only cscope can walk a call graph, so the registry has to find it by
// interface rather than assuming every provider can answer.
func TestCallGraphFindsTheCapableProvider(t *testing.T) {
	plain := &fake{name: "ctags", available: true}
	graph := &callGraphFake{
		fake:    fake{name: "cscope", available: true},
		callers: []Location{{Path: "a.c", Line: 7}},
	}

	var r Registry
	r.Add(plain)
	r.Add(graph)

	locs, ok := r.Callers(context.Background(), "f")
	if !ok {
		t.Fatal("Callers reported no capable provider")
	}
	if len(locs) != 1 || locs[0].Line != 7 {
		t.Errorf("got %+v, want the call-graph provider's result", locs)
	}
}

// With no cscope present, the caller must be told the capability is missing
// rather than being shown an empty result that looks like "no callers".
func TestCallGraphReportsWhenUnavailable(t *testing.T) {
	var r Registry
	r.Add(&fake{name: "ctags", available: true})

	if _, ok := r.Callers(context.Background(), "f"); ok {
		t.Error("Callers should report false when no provider can walk a call graph")
	}
	if _, ok := r.Callees(context.Background(), "f"); ok {
		t.Error("Callees should report false when no provider can walk a call graph")
	}
}

func TestEmptyRegistryIsSafe(t *testing.T) {
	var r Registry
	if got := r.Definitions(context.Background(), "f"); len(got) != 0 {
		t.Errorf("got %+v, want nothing", got)
	}
	if got := r.References(context.Background(), "f"); len(got) != 0 {
		t.Errorf("got %+v, want nothing", got)
	}
	if err := r.Close(); err != nil {
		t.Errorf("Close on an empty registry returned %v", err)
	}
}

func TestSymbolAt(t *testing.T) {
	tests := []struct {
		name string
		line string
		col  int
		want string
	}{
		{"start of identifier", "compute_total(x)", 0, "compute_total"},
		{"middle of identifier", "compute_total(x)", 5, "compute_total"},
		{"last character", "compute_total(x)", 12, "compute_total"},
		// A cursor just past a word should still resolve it: that is where the
		// cursor lands after double-clicking or arrowing to the end of a name.
		{"just past the end", "compute_total(x)", 13, "compute_total"},
		{"on punctuation between words", "a.b", 1, "a"},
		{"second identifier", "int value;", 4, "value"},
		{"underscore and digits", "buf_2_len", 4, "buf_2_len"},
		{"empty line", "", 0, ""},
		{"whitespace only", "    ", 2, ""},
		{"bare number is not a symbol", "x = 12345;", 5, ""},
		{"identifier with digits is", "utf8_len", 2, "utf8_len"},
		{"out of range clamps", "abc", 99, ""},
		{"negative clamps", "abc", -1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SymbolAt([]byte(tt.line), tt.col); got != tt.want {
				t.Errorf("SymbolAt(%q, %d) = %q, want %q", tt.line, tt.col, got, tt.want)
			}
		})
	}
}

func TestKindFromCtags(t *testing.T) {
	tests := []struct {
		letter string
		want   Kind
	}{
		{"f", KindFunction},
		{"d", KindMacro},
		{"s", KindStruct},
		{"u", KindUnion},
		{"g", KindEnum},
		{"t", KindTypedef},
		{"m", KindMember},
		{"c", KindClass},
		{"v", KindVariable},
		{"", KindUnknown},
		{"?", KindUnknown},
		// Universal ctags emits full names when --fields=+K is used.
		{"function", KindFunction},
		{"struct", KindStruct},
	}
	for _, tt := range tests {
		if got := KindFromCtags(tt.letter); got != tt.want {
			t.Errorf("KindFromCtags(%q) = %v, want %v", tt.letter, got, tt.want)
		}
	}
}

func TestKindStrings(t *testing.T) {
	if KindFunction.String() != "func" {
		t.Errorf("KindFunction renders as %q", KindFunction.String())
	}
	if KindUnknown.String() != "" {
		t.Errorf("KindUnknown should render as empty, got %q", KindUnknown.String())
	}
}

// scanFake stands in for a provider whose reference search reads the whole
// tree, like the built-in indexer.
type scanFake struct{ fake }

func (s *scanFake) ScansForReferences() bool { return true }

// partialScan returns results together with an error, as a scan cut short does.
type partialScan struct {
	scanFake
	err error
}

func (p *partialScan) References(context.Context, string) ([]Location, error) {
	p.calls++
	return p.refs, p.err
}

func loc(path string, line int) Location { return Location{Path: path, Line: line} }

// On a kernel tree cscope answers in under half a second and a whole-tree scan
// takes over a minute. Asking both made every lookup wait out the scan.
func TestReferencesSkipTheScanWhenAnIndexAnswered(t *testing.T) {
	index := &fake{name: "cscope", available: true, refs: []Location{loc("a.c", 1)}}
	scan := &scanFake{fake{name: "builtin", available: true, refs: []Location{loc("a.c", 1), loc("b.c", 9)}}}
	var r Registry
	r.Add(index)
	r.Add(scan)

	got, rep := r.ReferencesReport(context.Background(), "f")

	if scan.calls != 0 {
		t.Errorf("the scanning provider ran %d time(s); an index had already answered", scan.calls)
	}
	if len(got) != 1 {
		t.Errorf("got %v, want only the index's answer", got)
	}
	if !rep.SkippedScan() {
		t.Error("the report should say a scan was left out, so the UI can offer it")
	}
}

// The order providers were registered in must not matter: a scanner registered
// first still waits behind the indexed providers.
func TestScannerRegisteredFirstStillRunsLast(t *testing.T) {
	scan := &scanFake{fake{name: "builtin", available: true, refs: []Location{loc("b.c", 9)}}}
	index := &fake{name: "cscope", available: true, refs: []Location{loc("a.c", 1)}}
	var r Registry
	r.Add(scan)
	r.Add(index)

	r.ReferencesReport(context.Background(), "f")
	if scan.calls != 0 {
		t.Error("scanner ran despite the index answering")
	}
}

func TestReferencesFallBackToTheScanWhenNothingIndexedAnswers(t *testing.T) {
	index := &fake{name: "cscope", available: true}
	scan := &scanFake{fake{name: "builtin", available: true, refs: []Location{loc("b.c", 9)}}}
	var r Registry
	r.Add(index)
	r.Add(scan)

	got, rep := r.ReferencesReport(context.Background(), "f")
	if scan.calls != 1 || len(got) != 1 {
		t.Errorf("scan calls = %d, results = %v; want the scan to supply the answer", scan.calls, got)
	}
	if rep.SkippedScan() {
		t.Error("nothing was skipped")
	}
}

func TestUnavailableScannerIsNotAsked(t *testing.T) {
	scan := &scanFake{fake{name: "builtin", available: false, refs: []Location{loc("b.c", 9)}}}
	var r Registry
	r.Add(scan)
	if got, _ := r.ReferencesReport(context.Background(), "f"); len(got) != 0 || scan.calls != 0 {
		t.Errorf("unavailable provider was used: calls=%d results=%v", scan.calls, got)
	}
}

// A scan that runs out of time has still found real hits. Discarding them made
// an incomplete answer look identical to no answer, and 7 hits became 5.
func TestPartialResultsAreKeptAndFlagged(t *testing.T) {
	scan := &partialScan{
		scanFake: scanFake{fake{name: "text", available: true, refs: []Location{loc("a.c", 3)}}},
		err:      &PartialError{Done: 21340, Total: 60000, Cause: context.DeadlineExceeded},
	}
	var r Registry
	r.Add(scan)

	got, rep := r.ReferencesReport(context.Background(), "f")
	if len(got) != 1 {
		t.Fatalf("partial results were dropped: %v", got)
	}
	if !rep.Partial() {
		t.Error("the report must flag the answer as incomplete")
	}
	if s := rep.Summary(); !strings.Contains(s, "partial (21,340 of 60,000 files)") {
		t.Errorf("summary %q does not say how much was covered", s)
	}
}

// cancelling runs a hook during the query, so a test can end the context while
// the provider is working, as a deadline does.
type cancelling struct {
	scanFake
	during func()
	err    error
}

func (c *cancelling) References(context.Context, string) ([]Location, error) {
	c.calls++
	c.during()
	return c.refs, c.err
}

// A provider that returns hits plus a bare context error was cut short too,
// even if it did not use PartialError to say so.
func TestHitsWithABareContextErrorAreMarkedPartial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &cancelling{
		scanFake: scanFake{fake{name: "text", available: true, refs: []Location{loc("a.c", 3)}}},
		during:   cancel,
		err:      context.Canceled,
	}
	var r Registry
	r.Add(p)

	got, rep := r.ReferencesReport(ctx, "f")
	if len(got) != 1 {
		t.Fatalf("hits found before the cancel were dropped: %v", got)
	}
	if !rep.Partial() {
		t.Error("an answer cut short by cancellation must be flagged partial")
	}
}

// No point starting a whole-tree scan with no time left to run it.
func TestScanIsNotStartedOnAnExpiredContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &scanFake{fake{name: "text", available: true, refs: []Location{loc("a.c", 3)}}}
	var r Registry
	r.Add(p)

	got, rep := r.ReferencesReport(ctx, "f")
	if p.calls != 0 || len(got) != 0 {
		t.Errorf("scan ran on a dead context: calls=%d results=%v", p.calls, got)
	}
	if rep.SkippedScan() {
		t.Error("it was not skipped because an index answered, so it must not say so")
	}
}

func TestFailedProviderIsReportedNotFatal(t *testing.T) {
	bad := &fake{name: "cscope", available: true, err: errors.New("boom")}
	good := &fake{name: "ctags", available: true, refs: []Location{loc("a.c", 1)}}
	var r Registry
	r.Add(bad)
	r.Add(good)

	got, rep := r.ReferencesReport(context.Background(), "f")
	if len(got) != 1 {
		t.Errorf("a failing provider broke navigation: %v", got)
	}
	if s := rep.Summary(); !strings.Contains(s, "cscope failed: boom") {
		t.Errorf("summary %q does not report the failure", s)
	}
}

func TestSummaryShowsEachProvidersTime(t *testing.T) {
	rep := Report{Timings: []Timing{
		{Provider: "cscope", Elapsed: 400 * time.Millisecond},
		{Provider: "text", Skipped: true},
	}}
	if got, want := rep.Summary(), "cscope 400ms"; got != want {
		t.Errorf("Summary() = %q, want %q (skipped providers are worded by the caller)", got, want)
	}
}

func TestFormatDuration(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{0, "0ms"},
		{190 * time.Millisecond, "190ms"},
		{999 * time.Millisecond, "999ms"},
		{time.Second, "1.0s"},
		{15250 * time.Millisecond, "15.2s"},
		{59900 * time.Millisecond, "59.9s"},
		{time.Minute, "1m0s"},
		{686600 * time.Millisecond, "11m26s"},
		{time.Hour + 5*time.Second, "60m5s"},
	} {
		if got := FormatDuration(tc.d); got != tc.want {
			t.Errorf("FormatDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestCommas(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 60000: "60,000", 1234567: "1,234,567"} {
		if got := commas(n); got != want {
			t.Errorf("commas(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestPartialErrorUnwrapsToItsCause(t *testing.T) {
	err := error(&PartialError{Done: 1, Total: 2, Cause: context.DeadlineExceeded})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Error("errors.Is should reach the cause, so existing deadline checks still work")
	}
}
