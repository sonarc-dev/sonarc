package main

import (
	"context"
	"os"
	"path/filepath"
	"sonarc/internal/buffer"
	"sonarc/internal/filetree"
	"sonarc/internal/index/builtin"
	"sonarc/internal/index/provider"
	"sonarc/internal/term"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// harness drives the real editor against a simulation screen, so these tests
// exercise the same code path as a live terminal without needing one. This is
// what makes end-to-end UI coverage possible in CI.
type harness struct {
	t   *testing.T
	app *app
	sim tcell.SimulationScreen
}

func newHarness(t *testing.T, content string) *harness {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatalf("init simulation screen: %v", err)
	}
	sim.SetSize(80, 24)
	t.Cleanup(sim.Fini)

	a := newApp(term.Wrap(sim), buffer.FromBytes([]byte(content)))
	return &harness{t: t, app: a, sim: sim}
}

// settle waits for any background query to finish and applies its result, as
// the event loop would. Tests that only care about the outcome call it (key and
// typeText do so automatically); tests about in-flight behaviour do not.
func (h *harness) settle() {
	h.t.Helper()
	if j := h.app.running; j != nil {
		select {
		case <-j.done:
		case <-time.After(10 * time.Second):
			h.t.Fatal("background query did not finish")
		}
	}
	h.app.pump()
}

// key sends a control or navigation key.
func (h *harness) key(k tcell.Key, mods ...tcell.ModMask) *harness {
	var m tcell.ModMask
	for _, x := range mods {
		m |= x
	}
	h.app.handle(tcell.NewEventKey(k, 0, m))
	h.settle()
	return h
}

// typeText sends each rune as a keystroke, the way typing actually arrives.
func (h *harness) typeText(s string) *harness {
	for _, r := range s {
		h.app.handle(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
		h.settle()
	}
	return h
}

// draw renders a frame and returns the screen as lines of text.
func (h *harness) draw() []string {
	h.t.Helper()
	h.app.ui.Draw()
	h.app.scr.Show()

	cells, w, hgt := h.sim.GetContents()
	lines := make([]string, hgt)
	for y := 0; y < hgt; y++ {
		var sb strings.Builder
		for x := 0; x < w; x++ {
			runes := cells[y*w+x].Runes
			if len(runes) == 0 || runes[0] == 0 {
				sb.WriteByte(' ')
				continue
			}
			sb.WriteRune(runes[0])
		}
		lines[y] = strings.TrimRight(sb.String(), " ")
	}
	return lines
}

// text returns the buffer contents.
func (h *harness) text() string {
	b := h.app.v().Buf
	return string(b.Text(buffer.Pos{}, b.End()))
}

// screenHas reports whether any rendered line contains want.
func (h *harness) screenHas(want string) bool {
	for _, l := range h.draw() {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}

// styleAt returns the foreground color of a rendered cell, so tests can assert
// that syntax highlighting actually reaches the screen rather than merely
// producing tokens nobody draws.
func (h *harness) fgAt(x, y int) tcell.Color {
	h.app.ui.Draw()
	h.app.scr.Show()
	cells, w, _ := h.sim.GetContents()
	fg, _, _ := cells[y*w+x].Style.Decompose()
	return fg
}

func (h *harness) screen() string { return strings.Join(h.draw(), "\n") }

func (h *harness) rowWith(s string) int {
	h.t.Helper()
	for y, l := range h.draw() {
		if strings.Contains(l, s) {
			return y
		}
	}
	h.t.Fatalf("no row shows %q:\n%s", s, h.screen())
	return -1
}

// openAll opens each name under the harness project, leaving the last showing.
func (h *harness) openAll(names ...string) {
	h.t.Helper()
	for _, n := range names {
		if err := h.app.openFile(filepath.Join(h.app.root, n)); err != nil {
			h.t.Fatal(err)
		}
	}
}

func (h *harness) openNames() []string {
	var out []string
	for _, v := range h.app.views {
		out = append(out, displayName(v))
	}
	return out
}

// replaceAnswer answers the next prompt with s in place of whatever it offers
// (Ctrl+U clears it). Keys are fed as the prompt reads them, since the
// simulated screen queues only a few events at a time.
func (h *harness) replaceAnswer(s string) {
	evs := []tcell.Event{tcell.NewEventKey(tcell.KeyCtrlU, 0, tcell.ModNone)}
	for _, r := range s {
		evs = append(evs, tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	evs = append(evs, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	go func() {
		for _, ev := range evs {
			for h.sim.PostEvent(ev) != nil {
				time.Sleep(time.Millisecond)
			}
		}
	}()
}

// A real terminal always reports the release after a press. Tests that click
// more than once must send it too, or the second press reads as a drag.
func (h *harness) mouse(x, y int, b tcell.ButtonMask) {
	h.app.handle(tcell.NewEventMouse(x, y, b, tcell.ModNone))
}

func (h *harness) click(x, y int) {
	h.mouse(x, y, tcell.Button1)
	h.mouse(x, y, tcell.ButtonNone)
}

// sidebarHarness is a wide project with the sidebar wired and drawn once, so
// the geometry mouse events are routed by exists.
func sidebarHarness(t *testing.T, files map[string]string, open string) *harness {
	t.Helper()
	h := withSidebar(project(t, files, open))
	h.draw()
	return h
}

// rowOf finds the screen row of a tree entry by name.
func (h *harness) rowOf(name string) int {
	h.t.Helper()
	for y, l := range h.draw() {
		// Only look inside the sidebar columns.
		if w := h.app.ui.TextX(); len(l) >= w {
			if strings.Contains(l[:w], name) {
				return y
			}
		}
	}
	h.t.Fatalf("no sidebar row shows %q; screen:\n%s", name, strings.Join(h.draw(), "\n"))
	return -1
}

// fakeClock lets tests place presses exactly inside or outside the
// multi-click window without sleeping.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// read hands ev to the editor the way the terminal does, through poll, and
// returns it for the caller to handle or, like a prompt, drop.
func (h *harness) read(ev tcell.Event) tcell.Event {
	h.t.Helper()
	if err := h.sim.PostEvent(ev); err != nil {
		h.t.Fatal(err)
	}
	return h.app.poll()
}

// project writes a source tree, opens one of its files in a fully wired editor,
// and waits for the index to finish. This exercises the whole navigation stack:
// key dispatch, symbol extraction, the provider registry, buffer switching, and
// the results panel.
func project(t *testing.T, files map[string]string, open string) *harness {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(100, 30)
	t.Cleanup(sim.Fini)

	buf, err := buffer.Open(filepath.Join(root, open))
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(term.Wrap(sim), buf)
	a.root = root
	a.ui.PanelRoot = root

	// Build the index synchronously so the test is deterministic.
	a.index = &provider.Registry{}
	a.builtin = builtin.New(root)
	if err := a.builtin.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.index.Add(a.builtin)
	t.Cleanup(func() { a.closeIndex() })

	return &harness{t: t, app: a, sim: sim}
}

// putCursorOn places the cursor on the first occurrence of word.
func (h *harness) putCursorOn(t *testing.T, word string) {
	t.Helper()
	b := h.app.v().Buf
	for i := 0; i < b.NumLines(); i++ {
		if col := strings.Index(string(b.Line(i)), word); col >= 0 {
			h.app.v().SetCursor(buffer.Pos{Line: i, Col: col})
			return
		}
	}
	t.Fatalf("%q does not appear in the open buffer", word)
}

// curFile returns the base name of the file currently on screen.
func (h *harness) curFile() string {
	return filepath.Base(h.app.v().Buf.Path())
}

const (
	mainC = `#include "util.h"

int main(int argc, char **argv)
{
	int n = compute_total(argc);
	return n;
}
`
	utilC = `#include "util.h"

int compute_total(int base)
{
	return base * 2;
}

int unused_helper(void)
{
	return compute_total(1);
}
`
	utilH = "int compute_total(int base);\n"
)

// answer queues a reply to the next prompt, followed by Enter.
func (h *harness) answer(s string) {
	h.t.Helper()
	for _, r := range s {
		if err := h.sim.PostEvent(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone)); err != nil {
			h.t.Fatal(err)
		}
	}
	if err := h.sim.PostEvent(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)); err != nil {
		h.t.Fatal(err)
	}
}

// saving points the harness at a state file of its own and returns its path.
func saving(t *testing.T, h *harness) string {
	t.Helper()
	h.app.statePath = filepath.Join(t.TempDir(), "sonarc", "state.json")
	return h.app.statePath
}

func readState(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// withSidebar wires a file-tree sidebar onto a harness built by project(),
// mirroring what run() does but for a harness that otherwise bypasses it.
func withSidebar(h *harness) *harness {
	h.app.ui.Sidebar.Tree = filetree.New(h.app.root)
	return h
}
