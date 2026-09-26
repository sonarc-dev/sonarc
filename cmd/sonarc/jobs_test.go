package main

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/sonarc-dev/sonarc/internal/index/provider"
)

// gatedIndex answers a reference query only when told to, so a test can hold a
// query open for as long as it needs and observe the editor meanwhile.
type gatedIndex struct {
	stubIndex
	release chan struct{}
	started chan struct{}
	saw     atomic.Bool // set if the query's context was cancelled
}

func newGated(refs ...provider.Location) *gatedIndex {
	return &gatedIndex{
		stubIndex: stubIndex{refs: refs},
		release:   make(chan struct{}),
		started:   make(chan struct{}, 4),
	}
}

func (g *gatedIndex) References(ctx context.Context, _ string) ([]provider.Location, error) {
	g.started <- struct{}{}
	select {
	case <-g.release:
		return g.refs, nil
	case <-ctx.Done():
		g.saw.Store(true)
		return nil, ctx.Err()
	}
}

func (g *gatedIndex) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-g.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the query never started")
	}
}

func gatedProject(t *testing.T) (*harness, *gatedIndex) {
	t.Helper()
	h := project(t, map[string]string{"main.c": mainC, "util.c": utilC, "util.h": utilH}, "main.c")
	g := newGated(provider.Location{Path: filepath.Join(h.app.root, "util.c"), Line: 1, Text: "hit"})
	h.app.index.Add(g)
	return h, g
}

// The reason for this whole step: a running search must not freeze the editor.
func TestKeysStillWorkWhileAQueryRuns(t *testing.T) {
	h, g := gatedProject(t)
	h.putCursorOn(t, "compute_total")

	start := time.Now()
	h.app.cmdFindReferences() // returns at once; the work is in the background
	if time.Since(start) > time.Second {
		t.Fatal("cmdFindReferences blocked until the query finished")
	}
	g.waitStarted(t)
	if h.app.running == nil {
		t.Fatal("no query is recorded as running")
	}

	// Typing goes straight to the buffer while the query is still open.
	before := h.text()
	h.app.handle(tcell.NewEventKey(tcell.KeyRune, 'Z', tcell.ModNone))
	if h.text() == before {
		t.Error("a keystroke during the query did not reach the buffer")
	}
	if h.app.running == nil {
		t.Error("typing must not cancel the query")
	}

	close(g.release)
}

func TestProgressLineShowsWhileRunningAndClearsAfter(t *testing.T) {
	h, g := gatedProject(t)
	h.putCursorOn(t, "compute_total")
	h.app.cmdFindReferences()
	g.waitStarted(t)

	h.app.pump()
	if !strings.Contains(h.app.ui.Busy, "references to compute_total") || !strings.Contains(h.app.ui.Busy, "Esc cancels") {
		t.Errorf("Busy = %q, want the query and how to cancel it", h.app.ui.Busy)
	}
	if !h.screenHas("Esc cancels") {
		t.Errorf("progress not drawn; screen:\n%s", strings.Join(h.draw(), "\n"))
	}

	// Pressing a key clears the transient message but must not hide progress.
	h.app.handle(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	if !h.screenHas("Esc cancels") {
		t.Error("a keystroke hid the progress line")
	}

	close(g.release)
	h.settle()
	if h.app.ui.Busy != "" {
		t.Errorf("Busy = %q after the query finished", h.app.ui.Busy)
	}
}

func TestEscapeCancelsTheQueryAndItsContext(t *testing.T) {
	h, g := gatedProject(t)
	h.putCursorOn(t, "compute_total")
	h.app.cmdFindReferences()
	g.waitStarted(t)

	h.app.handle(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))

	if h.app.running != nil {
		t.Error("Esc did not stop the query")
	}
	if !strings.Contains(h.app.ui.Msg, "cancelled") {
		t.Errorf("message = %q, want it to say cancelled", h.app.ui.Msg)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !g.saw.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !g.saw.Load() {
		t.Error("the provider's context was never cancelled, so it would keep working")
	}
	h.app.pump()
	if h.app.ui.Panel.Open {
		t.Error("a cancelled query opened the results panel")
	}
}

// Esc does other jobs (close the panel, leave the tree); with nothing running it
// must still do them.
func TestEscapeStillClosesThePanelWhenNothingIsRunning(t *testing.T) {
	h := project(t, map[string]string{"main.c": mainC, "util.c": utilC, "util.h": utilH}, "main.c")
	h.putCursorOn(t, "compute_total")
	h.app.cmdFindReferences()
	h.settle()
	if !h.app.ui.Panel.Open {
		t.Fatal("precondition: panel open")
	}
	h.key(tcell.KeyEscape)
	if h.app.ui.Panel.Open {
		t.Error("Esc no longer closes the panel")
	}
}

// Quitting during a search must not wait for it.
func TestQuitDuringAQueryDoesNotWait(t *testing.T) {
	h, g := gatedProject(t)
	h.putCursorOn(t, "compute_total")
	h.app.cmdFindReferences()
	g.waitStarted(t)

	start := time.Now()
	h.app.handle(tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone))
	if !h.app.quit {
		t.Error("Ctrl+Q did not quit while a query was running")
	}
	if time.Since(start) > time.Second {
		t.Errorf("quit took %v", time.Since(start))
	}
	if h.app.running != nil {
		t.Error("the query was left running after quit")
	}
}

// A newer question replaces an older one; the old answer must never surface.
func TestNewQueryCancelsTheOldOneAndDropsItsResult(t *testing.T) {
	h, g := gatedProject(t)
	h.putCursorOn(t, "compute_total")
	h.app.cmdFindReferences()
	g.waitStarted(t)
	first := h.app.running

	h.app.cmdFindReferences() // ask again
	g.waitStarted(t)
	if h.app.running == first {
		t.Fatal("the second query did not replace the first")
	}
	<-first.done // the first one ends once its context is cancelled

	h.app.pump()
	if h.app.ui.Panel.Open {
		t.Error("the superseded query's result reached the screen")
	}
	close(g.release)
	h.settle()
	if !h.app.ui.Panel.Open {
		t.Error("the second query's result never arrived")
	}
}

// If the user kept typing, the panel opens but the cursor stays where they are.
func TestResultsDoNotYankTheCursorAfterTyping(t *testing.T) {
	h, g := gatedProject(t)
	h.putCursorOn(t, "compute_total")
	h.app.cmdFindReferences()
	g.waitStarted(t)

	h.app.handle(tcell.NewEventKey(tcell.KeyRune, 'Z', tcell.ModNone))
	file := h.curFile()
	close(g.release)
	h.settle()

	if !h.app.ui.Panel.Open {
		t.Fatal("results panel did not open")
	}
	if got := h.curFile(); got != file {
		t.Errorf("the search jumped from %s to %s while the user was typing", file, got)
	}
}

// With no keys in between, the first result is shown as before.
func TestResultsJumpWhenTheUserHasNotMoved(t *testing.T) {
	h, g := gatedProject(t)
	h.putCursorOn(t, "compute_total")
	h.app.cmdFindReferences()
	g.waitStarted(t)
	close(g.release)
	h.settle()

	if got := h.curFile(); got != "util.c" {
		t.Errorf("open file = %s, want util.c (the first result)", got)
	}
}

// A provider that panics must produce a message, not leave the terminal in raw
// mode: the terminal-restoring guard only covers the main goroutine.
type panicIndex struct{ stubIndex }

func (*panicIndex) References(context.Context, string) ([]provider.Location, error) {
	panic("boom")
}

func TestPanicInABackgroundQueryBecomesAMessage(t *testing.T) {
	h := project(t, map[string]string{"main.c": mainC, "util.c": utilC, "util.h": utilH}, "main.c")
	h.app.index.Add(&panicIndex{})
	h.putCursorOn(t, "compute_total")

	h.app.cmdFindReferences()
	h.settle()

	if !strings.Contains(h.app.ui.Msg, "crashed") || !strings.Contains(h.app.ui.Msg, "boom") {
		t.Errorf("message = %q, want the crash reported", h.app.ui.Msg)
	}
}

// Work queued from another goroutine runs on the UI goroutine, in order.
func TestPostRunsOnPumpInOrder(t *testing.T) {
	h := project(t, map[string]string{"main.c": mainC}, "main.c")
	var got []int
	done := make(chan struct{})
	go func() {
		for i := 0; i < 5; i++ {
			i := i
			h.app.post(func() { got = append(got, i) })
		}
		close(done)
	}()
	<-done
	if len(got) != 0 {
		t.Fatal("posted work ran before pump, i.e. off the UI goroutine")
	}
	h.app.pump()
	if len(got) != 5 || got[0] != 0 || got[4] != 4 {
		t.Errorf("got %v, want 0..4 in order", got)
	}
}

// gatedDefs holds a definition lookup open until released.
type gatedDefs struct {
	stubIndex
	defs    []provider.Symbol
	release chan struct{}
	started chan struct{}
}

func (g *gatedDefs) Definitions(ctx context.Context, _ string) ([]provider.Symbol, error) {
	g.started <- struct{}{}
	select {
	case <-g.release:
		return g.defs, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func gatedDefProject(t *testing.T, n int) (*harness, *gatedDefs) {
	t.Helper()
	h := project(t, map[string]string{"main.c": mainC, "util.c": utilC, "util.h": utilH}, "main.c")
	g := &gatedDefs{release: make(chan struct{}), started: make(chan struct{}, 2)}
	for i := 0; i < n; i++ {
		g.defs = append(g.defs, provider.Symbol{
			Name: "compute_total", Source: "cscope",
			Loc: provider.Location{Path: filepath.Join(h.app.root, "util.c"), Line: 1 + i},
		})
	}
	// Replace the registry so only the gated provider answers.
	h.app.index = &provider.Registry{}
	h.app.index.Add(g)
	return h, g
}

func TestGotoDefinitionDoesNotBlockTheEditor(t *testing.T) {
	h, g := gatedDefProject(t, 1)
	h.putCursorOn(t, "compute_total")

	start := time.Now()
	h.app.cmdGotoDefinition()
	if time.Since(start) > time.Second {
		t.Fatal("goto-definition blocked until the lookup finished")
	}
	<-g.started
	if !strings.Contains(h.app.ui.Busy, "definition of compute_total") {
		t.Errorf("Busy = %q, want progress for the lookup", h.app.ui.Busy)
	}
	close(g.release)
	h.settle()
	if got := h.curFile(); got != "util.c" {
		t.Errorf("jumped to %s, want util.c", got)
	}
}

// Typing during a lookup: no jump, no modal picker eating the next keys.
func TestGotoDefinitionAfterTypingReportsInsteadOfJumping(t *testing.T) {
	for _, n := range []int{1, 2} {
		h, g := gatedDefProject(t, n)
		h.putCursorOn(t, "compute_total")
		h.app.cmdGotoDefinition()
		<-g.started

		h.app.handle(tcell.NewEventKey(tcell.KeyRune, 'Z', tcell.ModNone))
		close(g.release)
		h.settle()

		if got := h.curFile(); got != "main.c" {
			t.Errorf("n=%d: jumped to %s while the user was typing", n, got)
		}
		if h.app.ui.Picker.Open {
			t.Errorf("n=%d: a modal picker opened while the user was typing", n)
		}
		if !strings.Contains(h.app.ui.Msg, "Ctrl+]") {
			t.Errorf("n=%d: message %q should say how to act on the answer", n, h.app.ui.Msg)
		}
	}
}

func TestGotoDefinitionCanBeCancelled(t *testing.T) {
	h, g := gatedDefProject(t, 1)
	h.putCursorOn(t, "compute_total")
	h.app.cmdGotoDefinition()
	<-g.started
	h.app.handle(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	h.app.pump()
	if h.app.running != nil || h.curFile() != "main.c" {
		t.Error("Esc did not cancel the definition lookup")
	}
}
