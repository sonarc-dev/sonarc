package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/sonarc-dev/sonarc/internal/index/builtin"
	"github.com/sonarc-dev/sonarc/internal/index/provider"
	"regexp"
	"strings"
	"time"
)

// cmdFindText searches the project for a literal string.
//
// This is the whole-tree scan that reference lookup now skips when an index has
// answered, so it says plainly when the deadline cut it short.
func (a *app) cmdFindText() {
	q, ok := a.prompt("Search project (/regex/ or /regex/i for a pattern): ", a.symbolUnderCursor())
	if !ok || q == "" {
		return
	}
	sources := a.textSources()
	if pattern, ignoreCase, isRegex := regexQuery(q); isRegex {
		expr := pattern
		if ignoreCase {
			expr = "(?i)" + expr
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			a.ui.Error("not a valid pattern: %v", err)
			return
		}
		sources = a.regexSources(pattern, re, ignoreCase)
	}
	a.startQuery("searching text "+fmt.Sprintf("%q", q), func(ctx context.Context) applyFunc {
		start := time.Now()
		r := searchText(ctx, sources, q)
		locs := r.locs
		note := r.note(time.Since(start))
		incomplete := r.incomplete()

		return func(untouched bool) {
			if len(locs) == 0 {
				if incomplete {
					a.ui.Error("no matches for %q in the part searched — %s", q, note)
				} else {
					a.ui.Notify("no matches for %q  (%s)", q, note)
				}
				return
			}
			title := fmt.Sprintf("text %q", q)
			if incomplete {
				title += " (partial)"
			}
			a.showResults(title, locs, note, untouched)
		}
	})
}

// textSource is one way of searching the project's text, in the order tried.
type textSource struct {
	name string
	grep func(ctx context.Context, q string) ([]provider.Location, error)
	// exhaustive means a search that completes has covered every file the
	// slower sources would read, so "no matches" is final.
	exhaustive bool
}

// textSources lists the ways to search the project, fastest first: git grep
// (every core, git's own file list), then any provider's text search (cscope's
// reads every file on one core), then the built-in scan.
func (a *app) textSources() []textSource {
	var out []textSource
	root := a.root
	out = append(out, textSource{
		name: "git grep",
		grep: func(ctx context.Context, q string) ([]provider.Location, error) {
			return builtin.GitGrep(ctx, root, q)
		},
		exhaustive: true,
	})
	for _, p := range a.index.Available() {
		if ts, ok := p.(provider.TextSearcher); ok {
			out = append(out, textSource{name: p.Name() + " text search", grep: ts.Grep})
		}
	}
	if ix := a.builtin; ix != nil {
		out = append(out, textSource{
			name: "text scan",
			grep: func(ctx context.Context, q string) ([]provider.Location, error) {
				return ix.Grep(ctx, q, false)
			},
			exhaustive: true,
		})
	}
	return out
}

// regexQuery recognizes a search written as /pattern/ or /pattern/i, which is
// matched as a regular expression; anything else is literal text.
func regexQuery(q string) (pattern string, ignoreCase, ok bool) {
	if len(q) < 3 || q[0] != '/' {
		return "", false, false
	}
	switch {
	case strings.HasSuffix(q, "/i") && len(q) > 3:
		return q[1 : len(q)-2], true, true
	case strings.HasSuffix(q, "/"):
		return q[1 : len(q)-1], false, true
	}
	return "", false, false
}

// regexSources are the searches that understand a regular expression: git
// grep, then the built-in scan. cscope's pattern dialect is its own, so it
// sits this one out rather than answer a subtly different question.
func (a *app) regexSources(pattern string, re *regexp.Regexp, ignoreCase bool) []textSource {
	root := a.root
	out := []textSource{{
		name: "git grep",
		grep: func(ctx context.Context, _ string) ([]provider.Location, error) {
			return builtin.GitGrepRegexp(ctx, root, pattern, ignoreCase)
		},
		exhaustive: true,
	}}
	if ix := a.builtin; ix != nil {
		out = append(out, textSource{
			name: "text scan",
			grep: func(ctx context.Context, _ string) ([]provider.Location, error) {
				return ix.GrepRegexp(ctx, re)
			},
			exhaustive: true,
		})
	}
	return out
}

// textResult is what a project text search found and how.
type textResult struct {
	locs     []provider.Location
	source   string                 // the source that answered, or ran out of time
	partial  *provider.PartialError // set when the answer is incomplete
	timedOut bool                   // source ran out of time without an answer
}

func (r textResult) incomplete() bool { return r.partial != nil || r.timedOut }

// note describes the search for the message line: which source answered, how
// long it took, and how much is missing if anything is.
func (r textResult) note(elapsed time.Duration) string {
	d := provider.FormatDuration(elapsed)
	src := r.source
	if src == "" {
		src = "text search"
	}
	switch {
	case r.partial != nil:
		return fmt.Sprintf("%s %s partial (%s)", src, d, coverage(r.partial, len(r.locs)))
	case r.timedOut:
		return fmt.Sprintf("%s timed out after %s", src, d)
	}
	return fmt.Sprintf("%s %s", src, d)
}

// searchText tries each source in turn and returns the first real answer.
//
// A source that cannot run here (git grep outside a repository) or fails is
// skipped. One that runs out of time ends the search: the next source would
// start with no time left, report "0 files searched", and hide which one was
// slow. Partial results are returned as they are.
func searchText(ctx context.Context, sources []textSource, q string) textResult {
	for _, s := range sources {
		found, err := s.grep(ctx, q)
		var pe *provider.PartialError
		switch {
		case errors.Is(err, builtin.ErrNotGit):
			continue // cannot run here at all, which is not the same as slow
		case err == nil && (len(found) > 0 || s.exhaustive):
			return textResult{locs: found, source: s.name}
		case errors.As(err, &pe):
			return textResult{locs: found, source: s.name, partial: pe}
		case err != nil && ctx.Err() != nil:
			return textResult{source: s.name, timedOut: true}
		}
		// Not applicable here, failed, or found nothing without being
		// exhaustive: ask the next one.
	}
	return textResult{}
}

// coverage words how much of the project an incomplete search covered.
func coverage(pe *provider.PartialError, found int) string {
	switch {
	case errors.Is(pe, provider.ErrTooManyResults):
		return fmt.Sprintf("stopped at %d matches; search for something more specific", found)
	case pe.Total > 0:
		return fmt.Sprintf("%d of %d files, timed out", pe.Done, pe.Total)
	}
	return "timed out"
}
