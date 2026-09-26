package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sonarc-dev/sonarc/internal/index/cscope"
	"github.com/sonarc-dev/sonarc/internal/index/provider"
	"github.com/sonarc-dev/sonarc/internal/index/rebuild"
	"github.com/sonarc-dev/sonarc/internal/index/tags"
)

// rebuildJob is an index rebuild in progress. Only the UI goroutine touches it.
//
// It is separate from the query machinery on purpose. A query is seconds long
// and disposable, so Esc and the next query cancel it. A rebuild runs for
// minutes and is expensive to repeat; it must survive the user pressing Esc or
// looking something up meanwhile, and is only cancelled on request.
type rebuildJob struct {
	started time.Time
	step    string // what is running now, for the progress line
	cancel  context.CancelFunc
	done    chan struct{} // closed once the build has stopped and rolled back if it failed
}

// rebuildQuitWait bounds how long quitting waits for a cancelled rebuild to
// roll back. Rollback is a few renames; the wait is for make to die.
var rebuildQuitWait = 15 * time.Second

// cmdRebuildIndex regenerates the cscope database and tags file in the
// background, the way the project builds them; see package rebuild. Asked
// again while one is running, it offers to cancel it.
func (a *app) cmdRebuildIndex() {
	if j := a.rebuilding; j != nil {
		answer, ok := a.prompt(fmt.Sprintf("Index rebuild running (%s, %s). Cancel it and keep the old index? [y/N]: ",
			j.step, provider.FormatDuration(time.Since(j.started))), "")
		if ok && (answer == "y" || answer == "Y") {
			j.cancel()
			a.ui.Notify("cancelling the rebuild; the old index stays in place")
		}
		return
	}

	plan := rebuild.Detect(a.root)
	if !plan.Cscope && !plan.Ctags {
		a.ui.Error("cannot rebuild: %s", plan.Describe())
		return
	}
	answer, ok := a.prompt("Rebuild index ("+plan.Describe()+")? Minutes on a large tree; you can keep working. [y/N]: ", "")
	if !ok || (answer != "y" && answer != "Y") {
		a.ui.Notify("cancelled")
		return
	}
	a.startRebuild(plan)
}

// startRebuild runs plan in the background. The goroutine only computes and
// talks to the index providers, which lock for themselves; every change to
// editor state is posted back to the UI goroutine.
func (a *app) startRebuild(plan rebuild.Plan) {
	ctx, cancel := context.WithCancel(context.Background())
	j := &rebuildJob{started: time.Now(), step: "starting", cancel: cancel, done: make(chan struct{})}
	a.rebuilding = j
	cp, tp, bi := a.cscope, a.tags, a.builtin
	root := a.root

	go func() {
		defer cancel()
		steps := rebuild.Run(ctx, plan, func(step string) {
			a.post(func() { j.step = step })
		})
		// Decided now: the deferred cancel runs before the posted closure, so
		// asking ctx later would report every rebuild as cancelled.
		cancelled := ctx.Err() != nil
		close(j.done) // the indexes on disk are final: rebuilt, or rolled back

		// Point the providers at the new files. Reloading here, off the UI
		// goroutine, keeps a gigabyte file's first read from stalling the screen.
		var newCscope *cscope.Provider
		var newTags *tags.Provider
		for i := range steps {
			s := &steps[i]
			if s.Err != nil || s.Skipped != "" {
				continue
			}
			switch s.Name {
			case "cscope":
				if cp != nil {
					if err := cp.Reload(); err != nil {
						s.Err = fmt.Errorf("built, but reopening it failed: %w", err)
					}
				} else {
					newCscope = cscope.Discover(root)
				}
			case "tags":
				if tp != nil {
					if err := tp.Reload(); err != nil {
						s.Err = fmt.Errorf("built, but reopening it failed: %w", err)
					}
				} else {
					newTags = tags.Discover(root)
				}
			}
		}
		// The file list can change between builds; it is cheap to refresh. With
		// a real index now present, the built-in one no longer needs symbols.
		if bi != nil && !cancelled {
			if cp != nil || tp != nil || newCscope != nil || newTags != nil {
				bi.SkipSymbols()
			}
			_ = bi.Build(ctx)
		}

		a.post(func() {
			if a.rebuilding == j {
				a.rebuilding = nil
			}
			if newCscope != nil {
				a.cscope = newCscope
			}
			if newTags != nil {
				a.tags = newTags
			}
			if newCscope != nil || newTags != nil {
				a.index.Set(nilIfNone(a.cscope), nilIfNone(a.tags), a.builtin)
			}
			a.reportRebuild(steps, time.Since(j.started), cancelled)
		})
	}()

	// Keep the elapsed time on the progress line moving.
	go func() {
		for {
			select {
			case <-j.done:
				return
			case <-time.After(time.Second):
				a.wake()
			}
		}
	}()
	a.pump()
}

// nilIfNone turns a typed nil provider into an untyped nil, so Registry.Set
// drops it instead of registering a nil pointer that would panic when asked.
func nilIfNone[T interface {
	*cscope.Provider | *tags.Provider
	provider.CodeIntel
}](p T) provider.CodeIntel {
	if p == nil {
		return nil
	}
	return p
}

// reportRebuild says what each step did, in one line.
func (a *app) reportRebuild(steps []rebuild.Step, total time.Duration, cancelled bool) {
	if cancelled {
		a.ui.Notify("index rebuild cancelled after %s; the old index is unchanged", provider.FormatDuration(total))
		return
	}
	var parts []string
	failed := false
	for _, s := range steps {
		switch {
		case s.Skipped != "":
			parts = append(parts, s.Name+" skipped: "+s.Skipped)
		case s.Err != nil:
			failed = true
			parts = append(parts, fmt.Sprintf("%s FAILED (old %s kept): %v", s.Name, s.Name, s.Err))
		default:
			parts = append(parts, fmt.Sprintf("%s rebuilt in %s", s.Name, provider.FormatDuration(s.Elapsed)))
		}
	}
	msg := "index: " + strings.Join(parts, "; ")
	if failed {
		a.ui.Error("%s", msg)
	} else {
		a.ui.Notify("%s", msg)
	}
}

// rebuildBusyText is the progress line for a running rebuild.
func (a *app) rebuildBusyText() string {
	j := a.rebuilding
	if j == nil {
		return ""
	}
	return fmt.Sprintf("rebuilding index: %s  %s — Ctrl+K x to cancel",
		j.step, provider.FormatDuration(time.Since(j.started)))
}

// confirmQuitDuringRebuild asks before quitting in the middle of a rebuild,
// and if the answer is yes, cancels it and waits for the old index to be put
// back. It reports whether to go ahead and quit.
func (a *app) confirmQuitDuringRebuild() bool {
	j := a.rebuilding
	if j == nil {
		return true
	}
	answer, ok := a.prompt("An index rebuild is running. Quit and keep the old index? [y/N]: ", "")
	if !ok || (answer != "y" && answer != "Y") {
		return false
	}
	j.cancel()
	a.ui.Notify("stopping the index rebuild...")
	a.ui.Draw()
	a.scr.Show()
	select {
	case <-j.done:
	case <-time.After(rebuildQuitWait):
		// Leaves NAME.sonarc-prev behind; the next rebuild recovers from it.
	}
	a.rebuilding = nil
	return true
}
