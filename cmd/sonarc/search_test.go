package main

import (
	"context"
	"github.com/sonarc-dev/sonarc/internal/index/builtin"
	"github.com/sonarc-dev/sonarc/internal/index/provider"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestRegexQuery(t *testing.T) {
	for _, tc := range []struct {
		q, pat   string
		ic, isRe bool
	}{
		{"plain", "", false, false},
		{"/a.*b/", "a.*b", false, true},
		{"/Foo/i", "Foo", true, true},
		{"/", "", false, false},
		{"//", "", false, false},
		{"/unterminated", "", false, false},
		{"a/b/", "", false, false},
	} {
		pat, ic, ok := regexQuery(tc.q)
		if pat != tc.pat || ic != tc.ic || ok != tc.isRe {
			t.Errorf("%q: got %q %v %v", tc.q, pat, ic, ok)
		}
	}
}

// A regex search finds what a literal search for the same text would not, in
// both the git and the built-in path.
func TestProjectRegexSearch(t *testing.T) {
	files := map[string]string{
		"a.c": "int read_one(void);\nint write_one(void);\nint other(void);\n",
		"b.c": "static int READ_two;\n",
	}
	for _, withGit := range []bool{false, true} {
		h := project(t, files, "a.c")
		if withGit {
			if _, err := exec.LookPath("git"); err != nil {
				continue
			}
			gitRun(t, h.app.root, "init", "-q")
		}
		h.replaceAnswer("/^int (read|write)_/")
		h.key(tcell.KeyCtrlK)
		h.typeText("f")
		h.settle()
		if got := len(h.app.ui.Panel.Results); got != 2 {
			t.Errorf("git=%v: %d results, want read_one and write_one; msg %q", withGit, got, h.app.ui.Msg)
		}
		h.replaceAnswer("/read_/i")
		h.key(tcell.KeyCtrlK)
		h.typeText("f")
		h.settle()
		if got := len(h.app.ui.Panel.Results); got != 2 {
			t.Errorf("git=%v: case-insensitive: %d results, want 2", withGit, got)
		}
	}
}

func TestBadRegexIsReported(t *testing.T) {
	h := project(t, map[string]string{"a.c": "x\n"}, "a.c")
	h.replaceAnswer("/a(b/")
	h.key(tcell.KeyCtrlK)
	h.typeText("f")
	if !strings.Contains(h.app.ui.Msg, "not a valid pattern") {
		t.Errorf("message %q", h.app.ui.Msg)
	}
}

// A search that runs out of time says so, instead of reporting "no matches"
// as though the whole tree had been read.
func TestProjectTextSearchSaysWhenItRanOutOfTime(t *testing.T) {
	files := map[string]string{"main.c": mainC}
	for i := 0; i < 30; i++ {
		files[filepath.Join("src", string(rune('a'+i%26))+strings.Repeat("x", i)+".c")] = "int v;\n"
	}
	h := project(t, files, "main.c")

	old := queryTimeout
	queryTimeout = time.Nanosecond
	defer func() { queryTimeout = old }()
	time.Sleep(time.Millisecond)

	h.putCursorOn(t, "compute_total")
	// The prompt is pre-filled with the word under the cursor; Enter accepts it.
	// It reads from the screen's event queue, so queue the key before asking.
	if err := h.sim.PostEvent(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)); err != nil {
		t.Fatal(err)
	}
	h.app.cmdFindText()
	h.settle()

	msg := h.app.ui.Msg
	if !strings.Contains(msg, "part searched") || !strings.Contains(msg, "timed out") {
		t.Errorf("message %q must say the search ran out of time and was not complete", msg)
	}
	if !h.app.ui.IsErr {
		t.Error("an incomplete search with no matches should be shown as a problem, not as information")
	}
}

func TestPartialScanNoteSaysHowMuchWasCovered(t *testing.T) {
	r := textResult{
		locs:    []provider.Location{{Path: "a.c", Line: 1}},
		source:  "text scan",
		partial: &provider.PartialError{Done: 21340, Total: 60000, Cause: context.DeadlineExceeded},
	}
	got := r.note(15 * time.Second)
	want := "text scan 15.0s partial (21340 of 60000 files, timed out)"
	if got != want {
		t.Errorf("note = %q, want %q", got, want)
	}
}

// slowText is a text-searching provider that runs until its deadline, like
// cscope's text-string query on a large tree.
type slowText struct{ stubIndex }

func (s *slowText) Grep(ctx context.Context, _ string) ([]provider.Location, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func src(name string, exhaustive bool, fn func(ctx context.Context) ([]provider.Location, error)) textSource {
	return textSource{name: name, exhaustive: exhaustive,
		grep: func(ctx context.Context, _ string) ([]provider.Location, error) { return fn(ctx) }}
}

// When a source eats the whole deadline, the next must not start on a dead
// context and report "0 files searched": that blames the wrong thing and hides
// which source was slow.
func TestTextSearchDoesNotFallBackAfterASourceTimesOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	fellBack := false
	r := searchText(ctx, []textSource{
		src("cscope text search", false, func(ctx context.Context) ([]provider.Location, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}),
		src("text scan", true, func(context.Context) ([]provider.Location, error) {
			fellBack = true
			return nil, nil
		}),
	}, "x")

	if fellBack {
		t.Error("the next source ran with no time left")
	}
	if !r.timedOut || r.source != "cscope text search" {
		t.Errorf("got %+v, want the slow source named as timed out", r)
	}
}

// A source that finds nothing but did not cover everything lets the next try.
func TestTextSearchFallsBackWhenANonExhaustiveSourceFindsNothing(t *testing.T) {
	want := []provider.Location{{Path: "a.md", Line: 1}}
	r := searchText(context.Background(), []textSource{
		src("cscope text search", false, func(context.Context) ([]provider.Location, error) { return nil, nil }),
		src("text scan", true, func(context.Context) ([]provider.Location, error) { return want, nil }),
	}, "x")
	if len(r.locs) != 1 || r.source != "text scan" {
		t.Errorf("got %+v, want the scan's answer", r)
	}
}

// git grep that completes with no matches has covered the same files as the
// slower sources: asking them would repeat a minute-long scan to learn nothing.
func TestExhaustiveSourceWithNoMatchesIsFinal(t *testing.T) {
	asked := false
	r := searchText(context.Background(), []textSource{
		src("git grep", true, func(context.Context) ([]provider.Location, error) { return nil, nil }),
		src("text scan", true, func(context.Context) ([]provider.Location, error) { asked = true; return nil, nil }),
	}, "x")
	if asked {
		t.Error("a slower source was asked after an exhaustive one found nothing")
	}
	if r.source != "git grep" || r.incomplete() {
		t.Errorf("got %+v, want a complete answer from git grep", r)
	}
}

// Outside a repository git grep cannot run; that must fall through quietly.
func TestUnavailableSourceIsSkipped(t *testing.T) {
	want := []provider.Location{{Path: "a.c", Line: 3}}
	r := searchText(context.Background(), []textSource{
		src("git grep", true, func(context.Context) ([]provider.Location, error) { return nil, builtin.ErrNotGit }),
		src("text scan", true, func(context.Context) ([]provider.Location, error) { return want, nil }),
	}, "x")
	if r.source != "text scan" || len(r.locs) != 1 {
		t.Errorf("got %+v, want the scan's answer", r)
	}
}

func TestTooManyMatchesIsPartialNotTimedOut(t *testing.T) {
	r := searchText(context.Background(), []textSource{
		src("git grep", true, func(context.Context) ([]provider.Location, error) {
			return []provider.Location{{Path: "a.c", Line: 1}},
				&provider.PartialError{Cause: provider.ErrTooManyResults}
		}),
	}, "int")
	if !r.incomplete() || r.timedOut {
		t.Errorf("got %+v, want partial but not timed out", r)
	}
	if n := r.note(time.Second); !strings.Contains(n, "more specific") {
		t.Errorf("note %q should say to narrow the search", n)
	}
}

// The message must name the provider that ran out of time.
func TestTextSearchMessageNamesTheSlowProvider(t *testing.T) {
	h := project(t, map[string]string{"main.c": mainC, "util.c": utilC, "util.h": utilH}, "main.c")
	h.app.index.Add(&slowText{})

	old := queryTimeout
	queryTimeout = 30 * time.Millisecond
	defer func() { queryTimeout = old }()

	h.putCursorOn(t, "compute_total")
	if err := h.sim.PostEvent(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)); err != nil {
		t.Fatal(err)
	}
	h.app.cmdFindText()
	h.settle()

	if msg := h.app.ui.Msg; !strings.Contains(msg, "cscope text search timed out") {
		t.Errorf("message %q should name the provider that ran out of time", msg)
	}
}
