package cscope

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeCscope installs a stub cscope on PATH implementing the line protocol, and
// returns the directory holding a dummy database.
//
// The real cscope is not installed on every machine (it is not on the one this
// was written on), and requiring it would mean these paths went untested. The
// stub also makes it possible to exercise failure modes — a crash mid-session,
// a hang, garbage on stdout — that are awkward to provoke in the real thing.
func fakeCscope(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub is POSIX-only")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "cscope"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CSCOPE_DB", "")

	db := t.TempDir()
	if err := os.WriteFile(filepath.Join(db, "cscope.out"), []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	return db
}

// normalScript answers queries with canned results, and emits the ">>" prompt
// the real cscope prints so the header-scanning logic is exercised.
const normalScript = `#!/bin/sh
printf '>> '
while IFS= read -r line; do
  cmd=$(printf '%s' "$line" | cut -c1)
  case "$cmd" in
    q) exit 0 ;;
    1) printf 'cscope: 1 lines\n'
       printf 'src/main.c main 42 int main(void)\n' ;;
    3) printf 'cscope: 2 lines\n'
       printf 'a.c caller_one 10 foo();\n'
       printf 'sub/b.c caller_two 20 foo();\n' ;;
    0) printf 'cscope: 3 lines\n'
       printf 'x.c <global> 1 int foo;\n'
       printf 'y.c bar 5 foo();\n'
       printf 'z.c baz 9 return foo;\n' ;;
    *) printf 'cscope: 0 lines\n' ;;
  esac
  printf '>> '
done
`

func TestQueryParsesResults(t *testing.T) {
	dir := fakeCscope(t, normalScript)
	db, err := Open(filepath.Join(dir, "cscope.out"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	got, err := db.Query(context.Background(), FindDefinition, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1", len(got))
	}
	want := Result{
		File:     filepath.Join(dir, "src/main.c"),
		Function: "main",
		Line:     42,
		Text:     "int main(void)",
	}
	if got[0] != want {
		t.Errorf("result =\n %+v\nwant %+v", got[0], want)
	}
}

func TestQueryResolvesRelativePaths(t *testing.T) {
	dir := fakeCscope(t, normalScript)
	db, err := Open(filepath.Join(dir, "cscope.out"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	got, err := db.Query(context.Background(), FindCallers, "foo")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	for _, r := range got {
		if !filepath.IsAbs(r.File) {
			t.Errorf("path %q was not resolved to absolute", r.File)
		}
	}
	if got[1].File != filepath.Join(dir, "sub/b.c") {
		t.Errorf("nested path = %q", got[1].File)
	}
}

func TestZeroResultsIsNotAnError(t *testing.T) {
	dir := fakeCscope(t, normalScript)
	db, err := Open(filepath.Join(dir, "cscope.out"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	got, err := db.Query(context.Background(), FindText, "nothing_matches_this")
	if err != nil {
		t.Errorf("a query with no matches should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d results, want 0", len(got))
	}
}

// Several queries must reuse the same subprocess. That reuse is the entire
// reason this package exists, so verify the stream stays in step across them.
func TestConsecutiveQueriesReuseTheProcess(t *testing.T) {
	dir := fakeCscope(t, normalScript)
	db, err := Open(filepath.Join(dir, "cscope.out"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	for i := 0; i < 20; i++ {
		if got, err := db.Query(ctx, FindDefinition, "main"); err != nil || len(got) != 1 {
			t.Fatalf("query %d: got %d results, err %v", i, len(got), err)
		}
		if got, err := db.Query(ctx, FindSymbol, "foo"); err != nil || len(got) != 3 {
			t.Fatalf("query %d: got %d results, err %v", i, len(got), err)
		}
	}
	db.mu.Lock()
	pid := db.cmd.Process.Pid
	db.mu.Unlock()
	if pid <= 0 {
		t.Error("no live subprocess after repeated queries")
	}
}

func TestConcurrentQueriesAreSerialized(t *testing.T) {
	dir := fakeCscope(t, normalScript)
	db, err := Open(filepath.Join(dir, "cscope.out"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Interleaved writes would desynchronize the protocol and produce results
	// belonging to a different query.
	const n = 16
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			got, err := db.Query(context.Background(), FindDefinition, "main")
			if err != nil {
				errs <- err
				return
			}
			if len(got) != 1 || got[0].Line != 42 {
				errs <- errMismatch(got)
				return
			}
			errs <- nil
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent query failed: %v", err)
		}
	}
}

type resultErr struct{ got int }

func (e resultErr) Error() string { return "unexpected results: " + itoa(e.got) }
func errMismatch(g []Result) error {
	return resultErr{len(g)}
}
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// crashScript answers one query then dies, standing in for cscope segfaulting
// or being killed part-way through a session.
const crashScript = `#!/bin/sh
read -r line
printf 'cscope: 1 lines\n'
printf 'a.c f 1 hit\n'
exit 1
`

// A dead cscope must degrade navigation, not take the editor down, and the next
// query must transparently start a fresh process.
func TestRestartsAfterCrash(t *testing.T) {
	dir := fakeCscope(t, crashScript)
	db, err := Open(filepath.Join(dir, "cscope.out"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	if got, err := db.Query(ctx, FindDefinition, "x"); err != nil || len(got) != 1 {
		t.Fatalf("first query: %d results, err %v", len(got), err)
	}

	// The process is gone; this must report an error rather than hang or panic.
	if _, err := db.Query(ctx, FindDefinition, "y"); err == nil {
		t.Log("second query succeeded (a fresh process answered it)")
	}

	// And the driver must recover for subsequent queries.
	if got, err := db.Query(ctx, FindDefinition, "z"); err != nil {
		t.Logf("third query returned %v", err)
	} else if len(got) != 1 {
		t.Errorf("after restart got %d results, want 1", len(got))
	}
}

// hangScript accepts queries and never answers, standing in for a database so
// large or corrupt that cscope stalls.
const hangScript = `#!/bin/sh
while IFS= read -r line; do
  sleep 60
done
`

// A stalled query must time out and return control to the editor. Without this
// the UI would freeze with no way back.
func TestQueryTimesOut(t *testing.T) {
	dir := fakeCscope(t, hangScript)
	db, err := Open(filepath.Join(dir, "cscope.out"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.Timeout = 300 * time.Millisecond

	start := time.Now()
	_, err = db.Query(context.Background(), FindDefinition, "anything")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a hung query should have failed")
	}
	if elapsed > 5*time.Second {
		t.Errorf("query took %v to give up", elapsed)
	}
}

// Cancelling must return promptly, so a keystroke can abandon a slow search.
func TestQueryRespectsContextCancellation(t *testing.T) {
	dir := fakeCscope(t, hangScript)
	db, err := Open(filepath.Join(dir, "cscope.out"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	if _, err := db.Query(ctx, FindDefinition, "anything"); err == nil {
		t.Fatal("cancelled query should have failed")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("cancellation took %v to take effect", elapsed)
	}
}

// garbageScript emits output that does not follow the protocol.
const garbageScript = `#!/bin/sh
while IFS= read -r line; do
  printf 'this is not a cscope response\n'
  printf 'cscope: 2 lines\n'
  printf 'malformed line with no number\n'
  printf 'good.c fn 7 ok\n'
  printf '>> '
done
`

// Unparseable lines must be skipped rather than aborting the whole result set.
func TestMalformedOutputIsToleratedNotFatal(t *testing.T) {
	dir := fakeCscope(t, garbageScript)
	db, err := Open(filepath.Join(dir, "cscope.out"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	got, err := db.Query(context.Background(), FindDefinition, "x")
	if err != nil {
		t.Fatalf("malformed output should degrade, not error: %v", err)
	}
	if len(got) != 1 || got[0].Line != 7 {
		t.Errorf("got %+v, want the one parseable result", got)
	}
}

func TestParseResult(t *testing.T) {
	tests := []struct {
		line string
		want Result
		ok   bool
	}{
		{"a.c main 42 int main(void)", Result{"a.c", "main", 42, "int main(void)"}, true},
		{"a.c <global> 1 int x;", Result{"a.c", "<global>", 1, "int x;"}, true},
		{"a.c fn 5 ", Result{"a.c", "fn", 5, ""}, true},
		{"a.c fn 5", Result{"a.c", "fn", 5, ""}, true},
		{"a.c fn notanumber text", Result{}, false},
		{"incomplete", Result{}, false},
		{"", Result{}, false},
		// Source text containing spaces must survive intact.
		{"a.c fn 3 if (a == b) { return; }", Result{"a.c", "fn", 3, "if (a == b) { return; }"}, true},
	}
	for _, tt := range tests {
		got, ok := parseResult(tt.line)
		if ok != tt.ok {
			t.Errorf("parseResult(%q) ok = %v, want %v", tt.line, ok, tt.ok)
			continue
		}
		if ok && got != tt.want {
			t.Errorf("parseResult(%q) = %+v, want %+v", tt.line, got, tt.want)
		}
	}
}

func TestParseCount(t *testing.T) {
	tests := []struct {
		line string
		n    int
		ok   bool
	}{
		{"cscope: 5 lines", 5, true},
		{"cscope: 0 lines", 0, true},
		{"cscope: 1 lines", 1, true},
		{">> ", 0, false},
		{"cscope: cannot open file", 0, false},
		{"random output", 0, false},
		{"", 0, false},
	}
	for _, tt := range tests {
		n, ok := parseCount(tt.line)
		if n != tt.n || ok != tt.ok {
			t.Errorf("parseCount(%q) = %d, %v; want %d, %v", tt.line, n, ok, tt.n, tt.ok)
		}
	}
}

// A newline in the search term would be read as a second command and
// desynchronize the protocol for every later query.
func TestNewlineInTermIsNeutralized(t *testing.T) {
	dir := fakeCscope(t, normalScript)
	db, err := Open(filepath.Join(dir, "cscope.out"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	if _, err := db.Query(ctx, FindDefinition, "evil\n1injected"); err != nil {
		t.Fatalf("query with an embedded newline failed: %v", err)
	}
	// The connection must still be usable and in step.
	if got, err := db.Query(ctx, FindDefinition, "main"); err != nil || len(got) != 1 {
		t.Errorf("protocol desynchronized: %d results, err %v", len(got), err)
	}
}

func TestEmptyTermRejected(t *testing.T) {
	dir := fakeCscope(t, normalScript)
	db, err := Open(filepath.Join(dir, "cscope.out"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Query(context.Background(), FindDefinition, "   "); err == nil {
		t.Error("an empty search term should be rejected")
	}
}

func TestFindLocatesDatabaseUpwards(t *testing.T) {
	t.Setenv("CSCOPE_DB", "")
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(root, "cscope.out")
	if err := os.WriteFile(dbPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := Find(deep)
	if !ok {
		t.Fatal("Find did not locate the database in a parent directory")
	}
	// Compare resolved paths: temp dirs are often behind a symlink.
	wantResolved, _ := filepath.EvalSymlinks(dbPath)
	gotResolved, _ := filepath.EvalSymlinks(got)
	if gotResolved != wantResolved {
		t.Errorf("Find = %q, want %q", gotResolved, wantResolved)
	}

	if _, ok := Find(t.TempDir()); ok {
		t.Error("Find reported a database where there is none")
	}
}

func TestFindHonorsEnvironmentOverride(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "custom.out")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CSCOPE_DB", p)
	got, ok := Find(t.TempDir())
	if !ok || got != p {
		t.Errorf("Find = %q, %v; want %q from $CSCOPE_DB", got, ok, p)
	}
}

func TestOpenReportsMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	p := filepath.Join(dir, "cscope.out")
	os.WriteFile(p, []byte("x"), 0o644)

	_, err := Open(p)
	if err == nil {
		t.Fatal("Open should fail when cscope is not installed")
	}
	if !strings.Contains(err.Error(), "not installed") {
		t.Errorf("error should say cscope is missing, got: %v", err)
	}
}

func TestClosedDatabaseRejectsQueries(t *testing.T) {
	dir := fakeCscope(t, normalScript)
	db, err := Open(filepath.Join(dir, "cscope.out"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Query(context.Background(), FindDefinition, "main"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Query(context.Background(), FindDefinition, "main"); err == nil {
		t.Error("queries after Close should fail cleanly")
	}
	if err := db.Close(); err != nil {
		t.Errorf("double Close returned %v", err)
	}
}

// missScript answers the way real cscope 15.9 does, transcribed from a kernel
// tree: a miss is ">> Unable to search database" with no count header, a bad
// regex is a message too, and a hit is the usual header and results.
const missScript = `#!/bin/sh
printf '>> '
while IFS= read -r line; do
  case "$line" in
    q) exit 0 ;;
    1hit) printf 'cscope: 1 lines\n'; printf 'a.c hit 7 int hit(void)\n' ;;
    6*) printf 'Egrep syntax error\n' ;;
    *) printf 'Unable to search database\n' ;;
  esac
  printf '>> '
done
`

// Every lookup of a name cscope did not know used to wait for a count header
// that never comes: 15 s per miss on a kernel tree.
func TestMissAnswersAtOnceAndKeepsTheProtocolInStep(t *testing.T) {
	dir := fakeCscope(t, missScript)
	db, err := Open(filepath.Join(dir, "cscope.out"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.Timeout = 5 * time.Second

	start := time.Now()
	got, err := db.Query(context.Background(), FindDefinition, "nosuch")
	if el := time.Since(start); el > time.Second {
		t.Errorf("a miss took %v; it must not wait for the timeout", el)
	}
	if err != nil || len(got) != 0 {
		t.Errorf("miss = %v, %v; want no results and no error", got, err)
	}

	// The next query must read its own answer, not a leftover.
	got, err = db.Query(context.Background(), FindDefinition, "hit")
	if err != nil || len(got) != 1 || got[0].Line != 7 {
		t.Errorf("hit after a miss = %+v, %v", got, err)
	}
}

func TestFailureMessageIsReportedAsAnError(t *testing.T) {
	dir := fakeCscope(t, missScript)
	db, err := Open(filepath.Join(dir, "cscope.out"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.Timeout = 5 * time.Second

	_, err = db.Query(context.Background(), FindEgrep, "(")
	if err == nil || !strings.Contains(err.Error(), "Egrep syntax error") {
		t.Errorf("err = %v, want cscope's own message", err)
	}
}

func TestStatusLine(t *testing.T) {
	for _, tc := range []struct {
		in   string
		msg  string
		done bool
	}{
		{">> Unable to search database", "Unable to search database", true},
		{">> >> Unable to search database", "Unable to search database", true},
		{">> ", "", false},
		{"cscope: warning: something", "", false}, // a notice, not an answer
		{"a.c fn 3 text", "", false},
	} {
		msg, done := statusLine(tc.in)
		if msg != tc.msg || done != tc.done {
			t.Errorf("statusLine(%q) = %q, %v; want %q, %v", tc.in, msg, done, tc.msg, tc.done)
		}
	}
}
