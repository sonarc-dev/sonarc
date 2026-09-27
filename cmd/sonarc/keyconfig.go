package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
)

// bindings is the key map one editor uses: the defaults in keys.go, with the
// user's keys.conf applied on top.
type bindings struct {
	keys   map[keySpec]string
	chords map[rune]string
}

func defaultBindings() *bindings {
	b := &bindings{keys: map[keySpec]string{}, chords: map[rune]string{}}
	for k, v := range keymap {
		b.keys[k] = v
	}
	for k, v := range chords {
		b.chords[k] = v
	}
	return b
}

// forCommand lists every key bound to name, direct keys first, each the way a
// person would say it.
func (b *bindings) forCommand(name string) []string {
	var direct, chord []string
	for spec, n := range b.keys {
		if n == name {
			direct = append(direct, keyName(spec))
		}
	}
	for r, n := range b.chords {
		if n == name {
			chord = append(chord, "Ctrl+K "+string(r))
		}
	}
	sort.Strings(direct)
	sort.Strings(chord)
	return append(direct, chord...)
}

// chordFor is the chord letter bound to name, if any.
func (b *bindings) chordFor(name string) (rune, bool) {
	var best rune
	for r, n := range b.chords {
		if n == name && (best == 0 || r < best) {
			best = r
		}
	}
	return best, best != 0
}

// keysPath is keys.conf, beside state.json.
func (a *app) keysPath() string {
	if dir := a.stateDir(); dir != "" {
		return filepath.Join(dir, "keys.conf")
	}
	return ""
}

// lineError is a mistake on one line of keys.conf.
type lineError struct {
	line int
	msg  string
}

func (e lineError) Error() string { return fmt.Sprintf("line %d: %s", e.line, e.msg) }

// apply reads keys.conf lines and applies each one it understands, returning
// an error for each it does not. One bad line never stops the others.
//
//	bind Ctrl+B toggle-sidebar
//	bind Ctrl+K y find-callers
//	unbind F12
//	unbind Ctrl+K y
func (b *bindings) apply(r io.Reader) []error {
	var errs []error
	sc := bufio.NewScanner(r)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue // comments are whole lines, so # can still be a chord key
		}
		if err := b.applyLine(strings.Fields(line)); err != nil {
			errs = append(errs, lineError{n, err.Error()})
		}
	}
	if err := sc.Err(); err != nil {
		errs = append(errs, err)
	}
	return errs
}

func (b *bindings) applyLine(f []string) error {
	verb := strings.ToLower(f[0])
	args := f[1:]
	chord := len(args) > 0 && strings.EqualFold(args[0], "Ctrl+K")
	switch {
	case verb == "bind" && chord && len(args) == 3:
		r, err := chordKey(args[1])
		if err != nil {
			return err
		}
		if _, ok := commands[args[2]]; !ok {
			return unknownCommand(args[2])
		}
		b.chords[r] = args[2]
	case verb == "bind" && !chord && len(args) == 2:
		spec, err := parseKey(args[0])
		if err != nil {
			return err
		}
		if _, ok := commands[args[1]]; !ok {
			return unknownCommand(args[1])
		}
		b.keys[spec] = args[1]
	case verb == "unbind" && chord && len(args) == 2:
		r, err := chordKey(args[1])
		if err != nil {
			return err
		}
		delete(b.chords, r)
	case verb == "unbind" && !chord && len(args) == 1:
		spec, err := parseKey(args[0])
		if err != nil {
			return err
		}
		delete(b.keys, spec)
	case verb == "bind" || verb == "unbind":
		return fmt.Errorf("expected %q", map[string]string{
			"bind":   "bind KEY COMMAND, or bind Ctrl+K LETTER COMMAND",
			"unbind": "unbind KEY, or unbind Ctrl+K LETTER",
		}[verb])
	default:
		return fmt.Errorf("unknown instruction %q; lines start with bind or unbind", f[0])
	}
	return nil
}

func unknownCommand(name string) error {
	return fmt.Errorf("unknown command %q; sonarc -commands lists them", name)
}

// chordKey is the second key of a Ctrl+K chord: one printable character,
// matched without regard to case.
func chordKey(s string) (rune, error) {
	r, size := utf8.DecodeRuneInString(s)
	if size != len(s) || r == utf8.RuneError || r <= ' ' {
		return 0, fmt.Errorf("a chord's second key is one character, not %q", s)
	}
	if r >= 'A' && r <= 'Z' {
		r += 'a' - 'A'
	}
	return r, nil
}

var namedKeys = map[string]tcell.Key{
	"up": tcell.KeyUp, "down": tcell.KeyDown, "left": tcell.KeyLeft, "right": tcell.KeyRight,
	"home": tcell.KeyHome, "end": tcell.KeyEnd, "pgup": tcell.KeyPgUp, "pgdn": tcell.KeyPgDn,
	"pageup": tcell.KeyPgUp, "pagedown": tcell.KeyPgDn,
	"insert": tcell.KeyInsert, "delete": tcell.KeyDelete,
}

// parseKey reads a key the way the docs and F1 write it: Ctrl+B, Alt+X,
// Alt+Left, Shift+F3, F12. It refuses keys that cannot reach the editor from
// a terminal inside tmux, and keys that would stop you typing.
func parseKey(s string) (keySpec, error) {
	parts := strings.Split(s, "+")
	base := parts[len(parts)-1]
	if base == "" && len(parts) > 1 { // "Ctrl++" is not a thing, but "Alt++" might be
		base = "+"
		parts = parts[:len(parts)-1]
	}
	var ctrl, alt, shift bool
	for _, m := range parts[:len(parts)-1] {
		switch strings.ToLower(m) {
		case "ctrl":
			ctrl = true
		case "alt", "meta", "option":
			alt = true
		case "shift":
			shift = true
		default:
			return keySpec{}, fmt.Errorf("unknown modifier %q in %q; use Ctrl, Alt or Shift", m, s)
		}
	}
	if ctrl && shift {
		return keySpec{}, fmt.Errorf("%s: Ctrl+Shift keys do not reach programs inside tmux or screen; bind a Ctrl+K chord instead", s)
	}
	lower := strings.ToLower(base)

	var spec keySpec
	switch {
	case len(lower) >= 2 && lower[0] == 'f' && isDigits(lower[1:]):
		n, _ := strconv.Atoi(lower[1:])
		if n < 1 || n > 12 {
			return keySpec{}, fmt.Errorf("%s: function keys go from F1 to F12", s)
		}
		spec.key = tcell.KeyF1 + tcell.Key(n-1)
	case namedKeys[lower] != 0:
		spec.key = namedKeys[lower]
	case ctrl:
		k, err := ctrlKey(s, base)
		if err != nil {
			return keySpec{}, err
		}
		if alt {
			return keySpec{}, fmt.Errorf("%s: Ctrl+Alt keys are not supported", s)
		}
		return keySpec{key: k}, nil
	case utf8.RuneCountInString(base) == 1:
		r, _ := utf8.DecodeRuneInString(base)
		if !alt {
			return keySpec{}, fmt.Errorf("%s: binding a plain character would stop you typing it; add Alt, or use a Ctrl+K chord", s)
		}
		return keySpec{key: tcell.KeyRune, r: r, mod: tcell.ModAlt}, nil
	default:
		return keySpec{}, fmt.Errorf("unknown key %q", s)
	}
	// A named or function key.
	if ctrl {
		return keySpec{}, fmt.Errorf("%s: terminals report Ctrl with this key inconsistently; use Alt or Shift, or a Ctrl+K chord", s)
	}
	if alt {
		spec.mod |= tcell.ModAlt
	}
	if shift {
		spec.mod |= tcell.ModShift
	}
	return spec, nil
}

// ctrlKey maps Ctrl+letter and the few Ctrl+punctuation keys terminals send.
func ctrlKey(s, base string) (tcell.Key, error) {
	switch strings.ToLower(base) {
	case "h":
		return 0, errors.New(s + ": terminals send Ctrl+H as Backspace")
	case "i":
		return 0, errors.New(s + ": terminals send Ctrl+I as Tab")
	case "m":
		return 0, errors.New(s + ": terminals send Ctrl+M as Enter")
	case "[":
		return 0, errors.New(s + ": terminals send Ctrl+[ as Esc")
	case "k":
		return 0, errors.New(s + ": Ctrl+K starts a chord; bind Ctrl+K LETTER instead")
	case "]":
		return tcell.KeyCtrlRightSq, nil
	case "\\":
		return tcell.KeyCtrlBackslash, nil
	}
	if len(base) == 1 {
		c := base[0] | 0x20 // lower case
		if c >= 'a' && c <= 'z' {
			return tcell.KeyCtrlA + tcell.Key(c-'a'), nil
		}
	}
	return 0, fmt.Errorf("%s: only Ctrl+letter, Ctrl+] and Ctrl+\\ can be bound", s)
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// loadKeys applies keys.conf, if there is one, and reports what it could not
// use on the message line.
func (a *app) loadKeys() {
	b, errs := loadBindings(a.keysPath())
	a.keys = b
	if len(errs) > 0 {
		more := ""
		if len(errs) > 1 {
			more = fmt.Sprintf(" (and %d more; sonarc -commands shows all)", len(errs)-1)
		}
		a.ui.Error("keys.conf %v%s", errs[0], more)
	}
}

// loadBindings is the defaults with the file at path applied. A missing file
// is not an error.
func loadBindings(path string) (*bindings, []error) {
	b := defaultBindings()
	if path == "" {
		return b, nil
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return b, nil
	}
	if err != nil {
		return b, []error{err}
	}
	defer f.Close()
	return b, b.apply(f)
}

const keysTemplate = `# sonarc key bindings. Saving this file applies it at once.
#
#   bind KEY COMMAND              bind Ctrl+B toggle-sidebar
#   bind Ctrl+K LETTER COMMAND    bind Ctrl+K y find-callers
#   unbind KEY                    unbind F12
#   unbind Ctrl+K LETTER          unbind Ctrl+K y
#
# Keys are written as F1 shows them: Ctrl+B, Alt+X, Alt+Left, Shift+F3, F12.
# A later line wins over an earlier one and over the built-in keys.
# sonarc -commands lists every command with the keys bound to it now.

`

// cmdEditKeys opens keys.conf, starting it from a commented template.
func (a *app) cmdEditKeys() {
	path := a.keysPath()
	if path == "" {
		a.ui.Error("no configuration directory to keep keys.conf in")
		return
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if !writeFile(path, []byte(strings.TrimSuffix(keysTemplate, "\n"))) {
			a.ui.Error("cannot create %s", path)
			return
		}
	}
	if err := a.openFile(path); err != nil {
		a.ui.Error("%v", err)
		return
	}
	a.v().MoveDocEnd(false)
	a.ui.Notify("key bindings: saving this file applies it")
}

// printCommands is sonarc -commands: every command, its keys with keys.conf
// applied, and anything keys.conf got wrong.
func printCommands(w io.Writer, path string) int {
	b, errs := loadBindings(path)
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		keys := strings.Join(b.forCommand(n), ", ")
		fmt.Fprintf(w, "%-22s %-28s %s\n", n, keys, commands[n].help)
	}
	if path != "" {
		fmt.Fprintf(w, "\nkey bindings file: %s\n", path)
	}
	for _, err := range errs {
		fmt.Fprintf(w, "keys.conf %v\n", err)
	}
	if len(errs) > 0 {
		return 1
	}
	return 0
}
