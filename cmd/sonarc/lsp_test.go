package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/sonarc-dev/sonarc/internal/index/provider"
)

// stubServer answers position queries the way a language server does: one
// precise definition, however many things share the name.
type stubServer struct {
	def     provider.Location
	refs    []provider.Location
	hover   string
	handles string // the extension it covers
	asked   []provider.At
}

func (s *stubServer) Name() string    { return "stub-lsp" }
func (s *stubServer) Available() bool { return true }
func (s *stubServer) Close() error    { return nil }
func (s *stubServer) Definitions(context.Context, string) ([]provider.Symbol, error) {
	return nil, nil
}
func (s *stubServer) References(context.Context, string) ([]provider.Location, error) {
	return nil, nil
}
func (s *stubServer) Search(context.Context, string, int) ([]provider.Symbol, error) {
	return nil, nil
}
func (s *stubServer) Handles(path string) bool { return filepath.Ext(path) == s.handles }
func (s *stubServer) DefinitionAt(_ context.Context, at provider.At) ([]provider.Symbol, error) {
	s.asked = append(s.asked, at)
	return []provider.Symbol{{Loc: s.def}}, nil
}
func (s *stubServer) ReferencesAt(_ context.Context, at provider.At) ([]provider.Location, error) {
	s.asked = append(s.asked, at)
	return s.refs, nil
}
func (s *stubServer) HoverAt(_ context.Context, at provider.At) (string, error) {
	return s.hover, nil
}

// Two files define helper; the built-in index can only list both, while the
// server knows which one this call means.
func lspProject(t *testing.T) (*harness, *stubServer) {
	h := project(t, map[string]string{
		"a.go": "package p\n\nfunc helper() {}\n",
		"b.go": "package q\n\nfunc helper() {}\n\nfunc use() { helper() }\n",
	}, "b.go")
	s := &stubServer{handles: ".go", hover: "func helper() (from q)"}
	s.def = provider.Location{Path: filepath.Join(h.app.root, "b.go"), Line: 3, Col: 6}
	s.refs = []provider.Location{{Path: filepath.Join(h.app.root, "b.go"), Line: 5, Col: 14, Text: "func use() { helper() }"}}
	h.app.index.Set(s, h.app.builtin)
	h.putCursorOn(t, "helper() }") // the call, not the definition above it
	return h, s
}

func TestDefinitionPrefersTheServersAnswer(t *testing.T) {
	h, s := lspProject(t)
	h.key(tcell.KeyCtrlRightSq)
	if h.app.ui.Picker.Open {
		t.Fatal("a picker opened: the server's single answer should be used")
	}
	if len(s.asked) != 1 || s.asked[0].Line != 4 || !strings.Contains(string(s.asked[0].Doc.Text), "func use()") {
		t.Fatalf("the server was asked %+v", s.asked)
	}
	if v := h.app.v(); v.Head.Line != 2 || v.Head.Col != 5 {
		t.Errorf("cursor at %d:%d, want 3:6 exactly where the server said", v.Head.Line+1, v.Head.Col+1)
	}
}

// Unsaved text is what the server is asked about.
func TestServerIsAskedAboutUnsavedText(t *testing.T) {
	h, s := lspProject(t)
	h.typeText("x")
	h.key(tcell.KeyLeft)
	h.key(tcell.KeyCtrlRightSq)
	if len(s.asked) == 0 || !strings.Contains(string(s.asked[0].Doc.Text), "xhelper") {
		t.Errorf("the server did not see the unsaved edit")
	}
}

func TestPeekShowsTheServersDescription(t *testing.T) {
	h, _ := lspProject(t)
	h.key(tcell.KeyCtrlK)
	h.typeText("v")
	if !strings.Contains(h.app.ui.Msg, "func helper() (from q)") || !strings.Contains(h.app.ui.Msg, "stub-lsp") {
		t.Errorf("peek = %q", h.app.ui.Msg)
	}
}

func TestReferencesComeFromTheServer(t *testing.T) {
	h, s := lspProject(t)
	h.key(tcell.KeyF7)
	if len(s.asked) == 0 {
		t.Fatal("the server was not asked for references")
	}
	if !h.app.ui.Panel.Open || len(h.app.ui.Panel.Results) == 0 {
		t.Fatalf("no results panel: %q", h.app.ui.Msg)
	}
}

// A file the server does not cover goes to the indexes, by name, as before.
func TestOtherLanguagesUseTheIndexes(t *testing.T) {
	h, s := lspProject(t)
	s.handles = ".rs"
	h.key(tcell.KeyCtrlRightSq)
	if len(s.asked) != 0 {
		t.Error("the server was asked about a file it does not handle")
	}
	if !h.app.ui.Picker.Open {
		t.Errorf("the built-in index should offer both helpers: %q", h.app.ui.Msg)
	}
}
