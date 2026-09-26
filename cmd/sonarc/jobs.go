package main

import (
	"context"
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/sonarc-dev/sonarc/internal/index/provider"
)

// Slow work — reference lookups, project text search — must not run on the
// goroutine that reads keys and draws the screen. On a kernel tree a search can
// take tens of seconds, and while it ran the editor ignored every key,
// including Ctrl+Q.
//
// The rule that keeps this safe: work runs on its own goroutine and touches no
// editor state. It returns a closure, and the closure is applied on the UI
// goroutine by pump, at the top of the event loop. Nothing but pump mutates
// what the user is looking at, so no locks are needed around the UI itself.

// queryTimeout bounds a background query. It is generous because the UI stays
// usable while one runs and Esc cancels it; it is a variable so tests can make
// a search run out of time without needing a tree big enough to be slow.
var queryTimeout = 60 * time.Second

// queryContext bounds a query so a wedged index cannot run forever.
func queryContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), queryTimeout)
}

// job is one background query. Only the UI goroutine reads or writes a.running.
type job struct {
	label   string
	cancel  context.CancelFunc
	done    chan struct{} // closed once the work has finished and posted its result
	started time.Time
	keys    uint64 // a.keyCount when it started; see applyFunc
}

// applyFunc puts a finished query's result on screen. untouched is true if the
// user pressed no key since the query started; if they did, the result must not
// yank the cursor to a new file underneath them.
type applyFunc func(untouched bool)

// wake interrupts the event loop so it redraws or applies a result promptly.
func (a *app) wake() {
	_ = a.scr.PostEvent(tcell.NewEventInterrupt(nil))
}

// post queues fn to run on the UI goroutine. It is the only safe way for a
// background goroutine to change editor state.
func (a *app) post(fn func()) {
	a.inboxMu.Lock()
	a.inbox = append(a.inbox, fn)
	a.inboxMu.Unlock()
	a.wake()
}

// pump runs everything background goroutines have queued, then refreshes the
// progress line. Call it on the UI goroutine before drawing.
func (a *app) pump() {
	a.inboxMu.Lock()
	work := a.inbox
	a.inbox = nil
	a.inboxMu.Unlock()
	for _, fn := range work {
		fn()
	}
	a.ui.Busy = a.busyText()
}

// startQuery runs work off the UI goroutine. Starting a query cancels any
// earlier one still running: results for a question the user has moved past
// are worse than none.
func (a *app) startQuery(label string, work func(ctx context.Context) applyFunc) {
	a.cancelQuery("")
	ctx, cancel := queryContext()
	j := &job{
		label:   label,
		cancel:  cancel,
		done:    make(chan struct{}),
		started: time.Now(),
		keys:    a.keyCount,
	}
	a.running = j

	go func() {
		apply := a.runProtected(func() applyFunc { return work(ctx) })
		a.post(func() {
			if a.running != j {
				return // cancelled or superseded; drop the result
			}
			a.running = nil
			cancel()
			apply(a.keyCount == j.keys)
		})
		close(j.done)
	}()

	// Redraw while it runs so the elapsed time keeps moving.
	go func() {
		for {
			select {
			case <-j.done:
				return
			case <-time.After(200 * time.Millisecond):
				a.wake()
			}
		}
	}()

	a.pump()
}

// runProtected turns a panic in background work into an error message. The
// terminal-restoring Guard only covers the main goroutine, so an unrecovered
// panic here would exit with the terminal still in raw mode.
func (a *app) runProtected(work func() applyFunc) (apply applyFunc) {
	defer func() {
		if r := recover(); r != nil {
			apply = func(bool) { a.ui.Error("background query crashed: %v", r) }
		}
	}()
	return work()
}

// cancelQuery stops the running query, if any. A non-empty msg is shown.
func (a *app) cancelQuery(msg string) {
	j := a.running
	if j == nil {
		return
	}
	a.running = nil
	j.cancel()
	a.ui.Busy = ""
	if msg != "" {
		a.ui.Notify("%s", msg)
	}
}

// busyText is the progress line shown while a query runs.
func (a *app) busyText() string {
	rb := a.rebuildBusyText()
	j := a.running
	if j == nil {
		return rb
	}
	if rb != "" {
		return a.queryBusyText(j) + "  ·  " + rb
	}
	return a.queryBusyText(j)
}

// queryBusyText is the progress line for a running query.
func (a *app) queryBusyText(j *job) string {
	el := time.Since(j.started)
	frames := []string{"-", "\\", "|", "/"}
	spin := frames[int(el/(200*time.Millisecond))%len(frames)]
	return fmt.Sprintf("%s %s  %s — Esc cancels", spin, j.label, provider.FormatDuration(el))
}
