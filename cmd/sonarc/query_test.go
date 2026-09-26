package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sonarc-dev/sonarc/internal/index/provider"
)

// stubIndex is a cheap indexed provider, standing in for cscope.
type stubIndex struct {
	refs  []provider.Location
	delay time.Duration
}

func (s *stubIndex) Name() string    { return "cscope" }
func (s *stubIndex) Available() bool { return true }
func (s *stubIndex) Close() error    { return nil }
func (s *stubIndex) Definitions(context.Context, string) ([]provider.Symbol, error) {
	return nil, nil
}
func (s *stubIndex) References(context.Context, string) ([]provider.Location, error) {
	time.Sleep(s.delay)
	return s.refs, nil
}
func (s *stubIndex) Search(context.Context, string, int) ([]provider.Symbol, error) {
	return nil, nil
}

// With an index answering, the whole-tree scan must not run, and the message
// must say how long it took and where to get the scan if it is wanted.
func TestReferencesSkipTheScanAndOfferItInstead(t *testing.T) {
	h := project(t, map[string]string{
		"main.c": mainC,
		"util.c": utilC,
		"util.h": utilH,
	}, "main.c")
	h.app.index.Add(&stubIndex{refs: []provider.Location{
		{Path: filepath.Join(h.app.root, "util.c"), Line: 1, Text: "the one the index knows"},
	}})

	h.putCursorOn(t, "compute_total")
	h.app.cmdFindReferences()
	h.settle()

	p := &h.app.ui.Panel
	if !p.Open || len(p.Results) != 1 {
		t.Fatalf("results = %+v, want only the index's single answer (the scan would find 4)", p.Results)
	}
	msg := h.app.ui.Msg
	for _, want := range []string{"cscope", "ms", "Ctrl+K f"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q should mention %q", msg, want)
		}
	}
}

// With nothing indexed the scan is the only source, and it still runs.
func TestReferencesStillScanWhenNoIndexAnswers(t *testing.T) {
	h := project(t, map[string]string{
		"main.c": mainC,
		"util.c": utilC,
		"util.h": utilH,
	}, "main.c")

	h.putCursorOn(t, "compute_total")
	h.app.cmdFindReferences()
	h.settle()

	if n := len(h.app.ui.Panel.Results); n != 4 {
		t.Errorf("got %d references, want 4 from the scan", n)
	}
	if !strings.Contains(h.app.ui.Msg, "builtin") {
		t.Errorf("message %q should name the provider and its time", h.app.ui.Msg)
	}
	if strings.Contains(h.app.ui.Msg, "Ctrl+K f") {
		t.Errorf("message %q offers a scan that just ran", h.app.ui.Msg)
	}
}
