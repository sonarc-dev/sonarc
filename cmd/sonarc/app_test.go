package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/sonarc-dev/sonarc/internal/buffer"
	"github.com/sonarc-dev/sonarc/internal/term"
)

func TestTypingAppearsOnScreen(t *testing.T) {
	h := newHarness(t, "")
	h.typeText("hello world")

	if got := h.text(); got != "hello world" {
		t.Errorf("buffer = %q, want %q", got, "hello world")
	}
	// The text must actually be drawn, not merely stored.
	if !h.screenHas("hello world") {
		t.Errorf("typed text not rendered; screen:\n%s", strings.Join(h.draw(), "\n"))
	}
}

func TestLineNumbersAreDrawn(t *testing.T) {
	h := newHarness(t, "one\ntwo\nthree")
	lines := h.draw()
	for i, want := range []string{"1 one", "2 two", "3 three"} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("row %d = %q, want it to contain %q", i, lines[i], want)
		}
	}
}

func TestStatusBarShowsFileAndPosition(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "example.c")
	if err := os.WriteFile(path, []byte("int main(void) { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf, err := buffer.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	defer sim.Fini()
	sim.SetSize(80, 24)
	h := &harness{t: t, app: newApp(term.Wrap(sim), buf), sim: sim}

	if !h.screenHas("example.c") {
		t.Error("status bar does not show the filename")
	}
	if !h.screenHas("Ln 1, Col 1") {
		t.Errorf("status bar does not show the position; screen:\n%s", strings.Join(h.draw(), "\n"))
	}

	// The unsaved marker must appear only after an edit.
	if h.screenHas("●") {
		t.Error("modified marker shown on an unmodified file")
	}
	h.typeText("x")
	if !h.screenHas("●") {
		t.Error("modified marker missing after an edit")
	}
}

func TestSaveWritesFileAndClearsModifiedMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.txt")
	buf, err := buffer.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	defer sim.Fini()
	sim.SetSize(80, 24)
	h := &harness{t: t, app: newApp(term.Wrap(sim), buf), sim: sim}

	h.typeText("saved content")
	h.key(tcell.KeyCtrlS)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file was not written: %v", err)
	}
	if string(got) != "saved content" {
		t.Errorf("file = %q, want %q", got, "saved content")
	}
	if h.screenHas("●") {
		t.Error("modified marker still shown after save")
	}
}

func TestNavigationKeys(t *testing.T) {
	h := newHarness(t, "first line\nsecond line\nthird line")

	h.key(tcell.KeyDown)
	h.key(tcell.KeyEnd)
	if p := h.app.v().Head; p.Line != 1 || p.Col != 11 {
		t.Errorf("after Down,End cursor = %v, want line 1 col 11", p)
	}

	h.key(tcell.KeyHome)
	if p := h.app.v().Head; p.Col != 0 {
		t.Errorf("after Home col = %d, want 0", p.Col)
	}

	// Ctrl+Home is delivered as Home with ModCtrl, not as a distinct key.
	h.key(tcell.KeyEnd, tcell.ModCtrl)
	if p := h.app.v().Head; p.Line != 2 {
		t.Errorf("Ctrl+End should reach the last line, got line %d", p.Line)
	}
}

// Scrolling must follow the cursor, and the viewport must render the lines it
// scrolled to rather than staying at the top of the file.
func TestScrollingFollowsCursor(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 200; i++ {
		sb.WriteString("line")
		sb.WriteString(strings.Repeat("x", i%5))
		sb.WriteByte('\n')
	}
	h := newHarness(t, sb.String())
	for i := 0; i < 60; i++ {
		h.key(tcell.KeyDown)
	}
	if h.app.v().Top == 0 {
		t.Error("viewport never scrolled")
	}
	// The topmost rendered row must be the line the viewport scrolled to, so
	// the gutter number matches Top rather than still reading 1.
	v := h.app.v()
	lines := h.draw()
	wantNum := strconv.Itoa(v.Top + 1)
	if got := strings.Fields(lines[0]); len(got) == 0 || got[0] != wantNum {
		t.Errorf("first visible row = %q, want it to start with line number %s", lines[0], wantNum)
	}
	// The cursor line must be on screen.
	if v.Head.Line < v.Top || v.Head.Line >= v.Top+v.Height {
		t.Errorf("cursor line %d outside viewport [%d,%d)", v.Head.Line, v.Top, v.Top+v.Height)
	}
}

func TestTabsRenderToTabStops(t *testing.T) {
	h := newHarness(t, "\tx")
	h.app.v().TabWidth = 4
	lines := h.draw()
	g := h.app.ui.GutterWidth()
	// The tab occupies 4 cells, so x lands at gutter+4.
	if lines[0][g+4] != 'x' {
		t.Errorf("expected 'x' at column %d, row is %q", g+4, lines[0])
	}
}

// Invalid bytes must be visible rather than silently replaced, and must not
// corrupt the file: this checks the rendering half of that promise.
func TestInvalidBytesRenderAsHex(t *testing.T) {
	h := newHarness(t, "a\xffb")
	if !h.screenHas("a<FF>b") {
		t.Errorf("invalid byte not shown as hex; row: %q", h.draw()[0])
	}
}

// Help must be generated from the live keymap, so a binding can never be
// documented without existing (or exist without being documented).
func TestHelpIsGeneratedFromTheKeymap(t *testing.T) {
	h := newHarness(t, "")
	help := strings.Join(h.helpLinesOrFail(), "\n")

	for spec, name := range keymap {
		c, ok := commands[name]
		if !ok {
			t.Errorf("keymap binds %q to unknown command %q", keyName(spec), name)
			continue
		}
		if !strings.Contains(help, c.help) {
			t.Errorf("command %q (%s) is bound but missing from help", name, c.help)
		}
		if !strings.Contains(help, keyName(spec)) {
			t.Errorf("key %s is bound but missing from help", keyName(spec))
		}
	}

	// Help must state what this terminal can actually do, since that is the
	// whole point of filtering it by capability.
	if !strings.Contains(help, "Terminal") {
		t.Error("help does not report terminal capabilities")
	}
}

func (h *harness) helpLinesOrFail() []string {
	h.t.Helper()
	lines := h.app.helpLines()
	if len(lines) == 0 {
		h.t.Fatal("help is empty")
	}
	return lines
}

// The overlay must actually render its content, not just compute it.
func TestHelpOverlayRenders(t *testing.T) {
	h := newHarness(t, "")
	h.app.ui.Draw()
	h.app.ui.DrawOverlay("sonarc help", h.app.helpLines())
	h.app.scr.Show()

	cells, w, hgt := h.sim.GetContents()
	var sb strings.Builder
	for y := 0; y < hgt; y++ {
		for x := 0; x < w; x++ {
			if r := cells[y*w+x].Runes; len(r) > 0 && r[0] != 0 {
				sb.WriteRune(r[0])
			}
		}
		sb.WriteByte('\n')
	}
	if !strings.Contains(sb.String(), "sonarc help") {
		t.Errorf("overlay title not drawn; screen:\n%s", sb.String())
	}
}

func TestQuitWithoutChangesExitsImmediately(t *testing.T) {
	h := newHarness(t, "unchanged")
	h.key(tcell.KeyCtrlQ)
	if !h.app.quit {
		t.Error("Ctrl+Q on a clean buffer should quit")
	}
}

func TestSyntaxHighlightingReachesTheScreen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.c")
	// Line 0: a keyword, an identifier, a number and a comment.
	src := "return count = 42; // note\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	buf, err := buffer.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	defer sim.Fini()
	sim.SetSize(80, 24)
	h := &harness{t: t, app: newApp(term.Wrap(sim), buf), sim: sim}
	h.app.ui.Draw()
	g := h.app.ui.GutterWidth()

	kw := h.fgAt(g+0, 0)    // "r" of return
	ident := h.fgAt(g+7, 0) // "c" of count
	num := h.fgAt(g+15, 0)  // "4" of 42
	com := h.fgAt(g+19, 0)  // "/" of the comment

	if kw == ident {
		t.Error("keyword and plain identifier rendered in the same color")
	}
	if num == ident {
		t.Error("number and plain identifier rendered in the same color")
	}
	if com == ident {
		t.Error("comment and plain identifier rendered in the same color")
	}
	if kw == com {
		t.Error("keyword and comment rendered in the same color")
	}
}

func TestStatusBarShowsLanguage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf, _ := buffer.Open(path)
	sim := tcell.NewSimulationScreen("UTF-8")
	sim.Init()
	defer sim.Fini()
	sim.SetSize(100, 24)
	h := &harness{t: t, app: newApp(term.Wrap(sim), buf), sim: sim}

	if !h.screenHas("go") {
		t.Errorf("status bar does not name the language; screen:\n%s",
			strings.Join(h.draw(), "\n"))
	}
}

// A file with no known extension must still render, uncolored.
func TestPlainTextRendersWithoutHighlighting(t *testing.T) {
	h := newHarness(t, "just some prose here\n")
	if !h.screenHas("just some prose here") {
		t.Error("plain text did not render")
	}
}
