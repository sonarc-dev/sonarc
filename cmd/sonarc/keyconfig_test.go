package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestParseKey(t *testing.T) {
	good := map[string]keySpec{
		"Ctrl+B":   {key: tcell.KeyCtrlB},
		"ctrl+b":   {key: tcell.KeyCtrlB},
		"Ctrl+]":   {key: tcell.KeyCtrlRightSq},
		"Alt+x":    {key: tcell.KeyRune, r: 'x', mod: tcell.ModAlt},
		"Alt+Left": {key: tcell.KeyLeft, mod: tcell.ModAlt},
		"Shift+F3": {key: tcell.KeyF3, mod: tcell.ModShift},
		"F12":      {key: tcell.KeyF12},
		"PgDn":     {key: tcell.KeyPgDn},
	}
	for in, want := range good {
		got, err := parseKey(in)
		if err != nil || got != want {
			t.Errorf("parseKey(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	bad := map[string]string{
		"Ctrl+Shift+F": "do not reach programs inside tmux",
		"Ctrl+H":       "Backspace",
		"Ctrl+I":       "Tab",
		"Ctrl+K":       "starts a chord",
		"x":            "stop you typing",
		"Ctrl+Left":    "inconsistently",
		"F13":          "F1 to F12",
		"Hyper+X":      "unknown modifier",
		"Ctrl+Alt+x":   "not supported",
		"Banana":       "unknown key",
	}
	for in, why := range bad {
		if _, err := parseKey(in); err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("parseKey(%q) error = %v, want it to mention %q", in, err, why)
		}
	}
}

// Every key a default binding uses must be one keys.conf can name, and name
// back to the same key: F1 and the docs show keys the way keys.conf reads them.
func TestDefaultKeysRoundTripThroughTheirNames(t *testing.T) {
	for spec, name := range keymap {
		got, err := parseKey(keyName(spec))
		if err != nil || got != spec {
			t.Errorf("%s (%s): parseKey(%q) = %+v, %v", name, keyName(spec), keyName(spec), got, err)
		}
	}
}

func TestApplyKeepsGoodLinesAndReportsBadOnes(t *testing.T) {
	b := defaultBindings()
	errs := b.apply(strings.NewReader(`# a comment
bind Ctrl+B toggle-sidebar

bind Ctrl+K y find-callers
bind Ctrl+K # palette
unbind Ctrl+D
unbind Ctrl+K r
bind Ctrl+N no-such-command
bind Ctrl+H save
rebind Ctrl+N save
bind Ctrl+N
`))
	if b.keys[keySpec{key: tcell.KeyCtrlB}] != "toggle-sidebar" {
		t.Error("Ctrl+B was not bound")
	}
	if b.chords['y'] != "find-callers" || b.chords['#'] != "palette" {
		t.Errorf("chords y=%q #=%q", b.chords['y'], b.chords['#'])
	}
	if _, ok := b.keys[keySpec{key: tcell.KeyCtrlD}]; ok {
		t.Error("Ctrl+D is still bound")
	}
	if _, ok := b.chords['r']; ok {
		t.Error("Ctrl+K r is still bound")
	}
	var got []string
	for _, e := range errs {
		got = append(got, e.Error())
	}
	want := []string{"line 8: unknown command", "line 9: Ctrl+H: terminals send Ctrl+H as Backspace", "line 10: unknown instruction", "line 11: expected"}
	if len(got) != len(want) {
		t.Fatalf("errors = %q", got)
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("error %d = %q, want it to start %q", i, got[i], want[i])
		}
	}
	// The package defaults are untouched: every editor starts from them.
	if keymap[keySpec{key: tcell.KeyCtrlD}] != "delete-line" {
		t.Error("the default key map was modified")
	}
}

// keys.conf changes what keys do, what F1 says, and what the palette shows.
func TestKeysConfChangesTheEditor(t *testing.T) {
	h := newHarness(t, "one\ntwo\n")
	path := saving(t, h)
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(filepath.Join(filepath.Dir(path), "keys.conf"), []byte("bind Alt+d delete-line\nunbind Ctrl+D\nbind Ctrl+K y find-callers\n"), 0o644)
	h.app.loadKeys()

	h.key(tcell.KeyCtrlD)
	if h.text() != "one\ntwo" {
		t.Errorf("Ctrl+D still deletes a line: %q", h.text())
	}
	h.app.handle(tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModAlt))
	if h.text() != "two" {
		t.Errorf("Alt+D did not delete the line: %q", h.text())
	}
	help := strings.Join(h.app.helpLines(), "\n")
	if !strings.Contains(help, "Alt+d") || strings.Contains(help, "Ctrl+D ") {
		t.Errorf("help does not reflect keys.conf:\n%s", help)
	}
	if got := h.app.bindingsFor("find-callers"); !strings.Contains(got, "Ctrl+K y") {
		t.Errorf("palette shows %q for find-callers", got)
	}
	h.key(tcell.KeyCtrlK)
	if !strings.Contains(h.app.ui.Msg, "c callers") || !strings.Contains(h.app.ui.Msg, "r refs") {
		t.Errorf("chord hint = %q", h.app.ui.Msg)
	}
}

func TestBadKeysConfIsReportedAtStartup(t *testing.T) {
	h := newHarness(t, "")
	path := saving(t, h)
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(filepath.Join(filepath.Dir(path), "keys.conf"), []byte("bind Ctrl+Shift+P palette\nbind F2 nope\n"), 0o644)
	h.app.loadKeys()
	if !h.app.ui.IsErr || !strings.Contains(h.app.ui.Msg, "line 1") || !strings.Contains(h.app.ui.Msg, "and 1 more") {
		t.Errorf("message = %q", h.app.ui.Msg)
	}
}

// The palette command opens keys.conf from a template; saving it applies it.
func TestEditingKeysConfAppliesOnSave(t *testing.T) {
	h := newHarness(t, "one\ntwo\n")
	saving(t, h)
	h.app.cmdEditKeys()
	if !strings.HasSuffix(h.app.v().Buf.Path(), "keys.conf") || !strings.Contains(h.text(), "bind KEY COMMAND") {
		t.Fatalf("keys.conf not opened from the template: %q", h.text())
	}
	h.typeText("\nbind Alt+u undo")
	h.key(tcell.KeyCtrlS)
	if !strings.Contains(h.app.ui.Msg, "applied the key bindings") {
		t.Fatalf("message = %q", h.app.ui.Msg)
	}
	if h.app.keys.keys[keySpec{key: tcell.KeyRune, r: 'u', mod: tcell.ModAlt}] != "undo" {
		t.Error("Alt+U was not bound after saving")
	}
}

func TestPrintCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.conf")
	os.WriteFile(path, []byte("bind Alt+z undo\nbind Ctrl+H save\n"), 0o644)
	var out strings.Builder
	code := printCommands(&out, path)
	s := out.String()
	for _, want := range []string{"edit-keys", "undo", "Alt+z", "Ctrl+Z", "line 2: Ctrl+H"} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1 for a keys.conf with errors", code)
	}
}
