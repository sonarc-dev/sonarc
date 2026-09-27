package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
)

// Ctrl+] is what a long-time vi user will press for goto-definition, and Ctrl+T
// to come back. Both must be wired to the real commands.
func TestViStyleNavigationKeys(t *testing.T) {
	h := project(t, map[string]string{
		"main.c": mainC,
		"util.c": utilC,
		"util.h": utilH,
	}, "main.c")

	h.putCursorOn(t, "compute_total")
	h.key(tcell.KeyCtrlRightSq) // Ctrl+]
	if got := h.curFile(); got != "util.c" {
		t.Fatalf("Ctrl+] jumped to %s, want util.c", got)
	}
	h.key(tcell.KeyCtrlT)
	if got := h.curFile(); got != "main.c" {
		t.Errorf("Ctrl+T returned to %s, want main.c", got)
	}
}

// Chords are the primary binding scheme in tmux, so they must reach the same
// commands as the direct keys.
func TestChordDispatch(t *testing.T) {
	h := project(t, map[string]string{
		"main.c": mainC,
		"util.c": utilC,
		"util.h": utilH,
	}, "main.c")

	h.putCursorOn(t, "compute_total")

	// Ctrl+K then g is goto-definition.
	h.key(tcell.KeyCtrlK)
	if h.app.chord == 0 {
		t.Fatal("Ctrl+K did not start a chord")
	}
	if h.app.ui.Msg == "" {
		t.Error("a pending chord should hint at what comes next")
	}
	h.app.handle(tcell.NewEventKey(tcell.KeyRune, 'g', tcell.ModNone))
	h.settle()
	if h.app.chord != 0 {
		t.Error("chord was not cleared after dispatch")
	}
	if got := h.curFile(); got != "util.c" {
		t.Errorf("Ctrl+K g jumped to %s, want util.c", got)
	}
}

func TestChordIsCaseInsensitive(t *testing.T) {
	h := project(t, map[string]string{
		"main.c": mainC,
		"util.c": utilC,
		"util.h": utilH,
	}, "main.c")

	h.putCursorOn(t, "compute_total")
	h.key(tcell.KeyCtrlK)
	h.app.handle(tcell.NewEventKey(tcell.KeyRune, 'R', tcell.ModNone)) // uppercase
	h.settle()
	if !h.app.ui.Panel.Open {
		t.Error("Ctrl+K R did not run find-references")
	}
}

func TestUnknownChordReportsItself(t *testing.T) {
	h := project(t, map[string]string{"main.c": mainC}, "main.c")
	h.key(tcell.KeyCtrlK)
	h.app.handle(tcell.NewEventKey(tcell.KeyRune, 'Y', tcell.ModNone))
	if !strings.Contains(h.app.ui.Msg, "unknown chord") {
		t.Errorf("message = %q, want it to report an unknown chord", h.app.ui.Msg)
	}
	if h.app.chord != 0 {
		t.Error("chord state was not reset after an unknown key")
	}
}

// A chord prefix must not be typed into the buffer while it is pending.
func TestChordPrefixDoesNotInsertText(t *testing.T) {
	h := project(t, map[string]string{"main.c": "x\n"}, "main.c")
	before := h.text()
	h.key(tcell.KeyCtrlK)
	h.app.handle(tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone)) // close-panel
	if got := h.text(); got != before {
		t.Errorf("chord keys were inserted into the buffer: %q -> %q", before, got)
	}
}

// Every chord and key binding must name a command that exists.
func TestAllBindingsResolveToRealCommands(t *testing.T) {
	for spec, name := range keymap {
		if _, ok := commands[name]; !ok {
			t.Errorf("key %v is bound to unknown command %q", spec, name)
		}
	}
	for r, name := range chords {
		if _, ok := commands[name]; !ok {
			t.Errorf("chord Ctrl+K %c is bound to unknown command %q", r, name)
		}
	}
}

// Some Ctrl combinations can never reach the application: the terminal sends
// the same byte as a named key, and tcell reports that named key instead.
// Binding one produces a shortcut that silently does nothing, which is exactly
// the failure mode this editor exists to avoid.
func TestNoBindingUsesAnUnreachableKey(t *testing.T) {
	// tcell's input loop intercepts these bytes before the control-key branch
	// (input.go: case '\t'; case '\b', '\x7F'; case '\r'), so the Ctrl+letter
	// constants for them are unreachable from a real terminal.
	unreachable := map[tcell.Key]string{
		tcell.KeyCtrlH: "Ctrl+H (terminal sends 0x08, reported as Backspace)",
		tcell.KeyCtrlI: "Ctrl+I (terminal sends 0x09, reported as Tab)",
		tcell.KeyCtrlM: "Ctrl+M (terminal sends 0x0D, reported as Enter)",
	}
	for spec, name := range keymap {
		if why, bad := unreachable[spec.key]; bad {
			t.Errorf("%q is bound to %s, which can never be delivered", name, why)
		}
	}
}

// The message line advertises specific keys; each must reach a real command.
func TestAdvertisedBindingsExist(t *testing.T) {
	for _, tc := range []struct {
		key  tcell.Key
		want string
	}{
		{tcell.KeyCtrlF, "find"},
		{tcell.KeyCtrlS, "save"},
		{tcell.KeyCtrlO, "open"},
		{tcell.KeyCtrlQ, "quit"},
		{tcell.KeyCtrlRightSq, "goto-definition"},
		{tcell.KeyCtrlE, "toggle-sidebar"},
	} {
		if got := keymap[keySpec{key: tc.key}]; got != tc.want {
			t.Errorf("key %v is bound to %q, want %q", tc.key, got, tc.want)
		}
	}
	if chords['r'] != "find-references" {
		t.Errorf("Ctrl+K r is bound to %q, want find-references", chords['r'])
	}
}

// Every shortcut the docs' key tables document must be bound to something.
// Ctrl+D once dropped out of the key map while the docs went on promising
// it, and nothing noticed; this is what notices now.
func TestDocumentedKeysAreBound(t *testing.T) {
	// Every docs page with key tables; vim.md's first column is vim's keys.
	pages, err := filepath.Glob("../../docs/*.md")
	if err != nil || len(pages) == 0 {
		t.Fatalf("no docs pages found: %v", err)
	}
	var data []byte
	for _, p := range pages {
		if filepath.Base(p) == "vim.md" {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, b...)
	}
	bound := map[string]bool{}
	for spec := range keymap {
		bound[keyName(spec)] = true
	}
	for r := range chords {
		bound["Ctrl+K "+string(r)] = true
	}
	arrows := strings.NewReplacer("←", "Left", "→", "Right", "↑", "Up", "↓", "Down")
	shortcut := regexp.MustCompile(`^(Ctrl\+K .|Ctrl\+.|Alt\+.+|Shift\+F\d+|F\d+)$`)
	tick := regexp.MustCompile("`([^`]+)`")

	checked := 0
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		first := strings.Split(line, "|")[1] // the key column
		for _, m := range tick.FindAllStringSubmatch(first, -1) {
			k := arrows.Replace(m[1])
			if !shortcut.MatchString(k) {
				continue // tree-only keys, plain arrows, Esc and the like
			}
			if strings.HasPrefix(k, "Ctrl+") && utf8.RuneCountInString(k) == 6 {
				k = "Ctrl+" + strings.ToUpper(k[5:])
			}
			checked++
			if !bound[k] {
				t.Errorf("the docs document %s, but nothing is bound to it", m[1])
			}
		}
	}
	if checked < 30 {
		t.Errorf("only %d shortcuts found in the docs tables; is the parser still reading them?", checked)
	}
}
