package term

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2/terminfo"

	// Registers the bundled terminfo database so colors can be reported
	// without initializing a screen and disturbing what is on it.
	_ "github.com/gdamore/tcell/v2/terminfo/extended"
)

// finding is one diagnosed setting.
type finding struct {
	name    string
	value   string
	ok      bool
	fix     string // the config line that fixes it
	because string // why it matters, in terms of what the user would notice
}

// Doctor reports what this terminal can do and what to change to get the rest.
//
// It exists because the four settings below fail silently: nothing errors, the
// editor just quietly feels worse, and there is no way to tell from inside it
// whether Ctrl+C reached your laptop or vanished into tmux.
func Doctor(w io.Writer) {
	caps := envCaps()

	fmt.Fprintf(w, "sonarc doctor\n\n")
	fmt.Fprintf(w, "Terminal\n")
	row := func(k, v string) { fmt.Fprintf(w, "  %-16s %s\n", k, v) }
	row("TERM", orUnset(caps.TERM))
	row("COLORTERM", orUnset(os.Getenv("COLORTERM")))
	row("Colors", strconv.Itoa(caps.Colors))
	row("Multiplexer", muxDescription(caps.Mux))
	row("Key fidelity", tierExplanation(caps.Tier))

	var findings []finding
	switch caps.Mux {
	case MuxTmux:
		if !tmuxAvailable() {
			fmt.Fprintf(w, "\ntmux\n")
			fmt.Fprintf(w, "  $TMUX is set but the tmux binary is not on PATH, so its settings\n")
			fmt.Fprintf(w, "  could not be read. Run sonarc -doctor from inside the tmux session.\n")
			break
		}
		findings = tmuxFindings()
	case MuxScreen:
		fmt.Fprintf(w, "\nscreen\n")
		fmt.Fprintf(w, "  screen cannot forward extended keys or the system clipboard.\n")
		fmt.Fprintf(w, "  Everything in sonarc still works; Ctrl+Shift accelerators do not,\n")
		fmt.Fprintf(w, "  and copy goes to an internal register. tmux is a better host.\n")
	}

	if len(findings) > 0 {
		fmt.Fprintf(w, "\ntmux settings\n")
		var fixes []string
		for _, f := range findings {
			mark := "OK  "
			if !f.ok {
				mark = "FIX "
			}
			fmt.Fprintf(w, "  %s%-16s %s\n", mark, f.name, f.value)
			if !f.ok {
				fmt.Fprintf(w, "      %s\n", f.because)
				fixes = append(fixes, f.fix)
			}
		}
		if len(fixes) > 0 {
			fmt.Fprintf(w, "\nAdd to ~/.tmux.conf, then run: tmux source-file ~/.tmux.conf\n")
			for _, f := range fixes {
				fmt.Fprintf(w, "  %s\n", f)
			}
		} else {
			fmt.Fprintf(w, "\n  Nothing to fix.\n")
		}
	}

	fmt.Fprintf(w, "\nsonarc works without any of these; they only add polish.\n")
}

func orUnset(s string) string {
	if s == "" {
		return "(unset)"
	}
	return s
}

func muxDescription(m Multiplexer) string {
	switch m {
	case MuxTmux:
		if v := tmuxVersion(); v != "" {
			return "tmux " + v
		}
		return "tmux"
	case MuxScreen:
		return "screen"
	}
	return "none"
}

func tierExplanation(t Tier) string {
	switch t {
	case TierFull:
		return "full (Ctrl+Shift combinations are reported)"
	case TierBasic:
		return "basic (no Ctrl+Shift; every command has a chord binding)"
	}
	return "minimal (limited colors, no mouse)"
}

// envCaps determines capabilities without opening a screen, so running the
// doctor does not clear what the user was looking at.
func envCaps() Caps {
	c := Caps{TERM: os.Getenv("TERM")}
	switch {
	case os.Getenv("TMUX") != "":
		c.Mux = MuxTmux
	case os.Getenv("STY") != "":
		c.Mux = MuxScreen
	case strings.HasPrefix(c.TERM, "screen"):
		c.Mux = MuxScreen
	}
	switch os.Getenv("COLORTERM") {
	case "truecolor", "24bit":
		c.TrueColor = true
	}

	// Prefer the real terminfo entry; fall back to the name when the terminal
	// is not in the bundled database.
	if ti, err := terminfo.LookupTerminfo(c.TERM); err == nil {
		c.Colors = ti.Colors
	} else {
		switch {
		case strings.Contains(c.TERM, "256color"):
			c.Colors = 256
		case c.TERM == "" || c.TERM == "dumb":
			c.Colors = 0
		default:
			c.Colors = 8
		}
	}

	switch {
	case c.Colors < 16 || c.TERM == "" || c.TERM == "dumb":
		c.Tier = TierMinimal
	case c.Mux != MuxNone:
		c.Tier = TierBasic
	case c.TrueColor || strings.Contains(c.TERM, "kitty") || strings.Contains(c.TERM, "xterm"):
		c.Tier = TierFull
	default:
		c.Tier = TierBasic
	}
	return c
}

// tmuxAvailable reports whether the tmux binary can be run, so the doctor can
// say "could not check" rather than reporting every option as broken.
func tmuxAvailable() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// tmuxShow reads one tmux option, trying session then server scope. An unset
// option returns the empty string; the caller substitutes tmux's own default,
// because that is the value actually in effect.
func tmuxShow(name string) string {
	for _, scope := range []string{"-gqv", "-sqv"} {
		out, err := exec.Command("tmux", "show", scope, name).Output()
		if err != nil {
			continue
		}
		if s := strings.TrimSpace(string(out)); s != "" {
			return s
		}
	}
	return ""
}

// tmuxDefaults are the values tmux uses when an option has not been set. An
// unset option is not "unknown" — it is this value, and it is what the user is
// actually living with.
var tmuxDefaults = map[string]string{
	"escape-time":      "500",
	"set-clipboard":    "external",
	"extended-keys":    "off",
	"default-terminal": "screen",
	"mouse":            "off",
}

// tmuxOption returns the effective value of an option and whether it came from
// tmux's default rather than the user's configuration.
func tmuxOption(name string) (value string, isDefault bool) {
	if v := tmuxShow(name); v != "" {
		return v, false
	}
	return tmuxDefaults[name], true
}

// shown renders a value for display, marking defaults as such.
func shown(v string, isDefault bool) string {
	if v == "" {
		return "(unknown)"
	}
	if isDefault {
		return v + "  (tmux default)"
	}
	return v
}

func tmuxVersion() string {
	out, err := exec.Command("tmux", "-V").Output()
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "tmux ")
}

// tmuxMouse returns the mouse option in effect for sonarc's own pane. tmux
// keeps it per session, so asking for the global value (as tmuxOption does)
// misses a "tmux set mouse on" typed in this session.
func tmuxMouse() (value string, isDefault bool) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	args := []string{"display", "-p"}
	if pane := os.Getenv("TMUX_PANE"); pane != "" {
		args = append(args, "-t", pane)
	}
	out, err := exec.CommandContext(ctx, "tmux", append(args, "#{mouse}")...).Output()
	if err == nil {
		switch strings.TrimSpace(string(out)) {
		case "1", "on":
			return "on", false
		case "0", "off":
			return "off", false
		}
	}
	return tmuxOption("mouse")
}

// TmuxMouseOff reports whether tmux is keeping mouse events from sonarc. With
// mouse off, tmux neither asks the outer terminal for them nor passes on what
// arrives, so no click ever reaches a program running inside it.
func TmuxMouseOff() bool {
	v, _ := tmuxMouse()
	return v == "off"
}

// tmuxFindings checks the settings that silently degrade the editor.
func tmuxFindings() []finding {
	var out []finding

	mouse, mouseDef := tmuxMouse()
	out = append(out, finding{
		name: "mouse", value: shown(mouse, mouseDef), ok: mouse == "on",
		fix:     "set -g mouse on",
		because: "Clicks, drags and the wheel never reach sonarc; tmux keeps them.",
	})

	// tmux waits this long after Escape to decide whether a key sequence
	// followed. At the 500ms default, Alt- bindings feel broken.
	esc, escDef := tmuxOption("escape-time")
	escOK := false
	if n, err := strconv.Atoi(esc); err == nil && n <= 50 {
		escOK = true
	}
	out = append(out, finding{
		name: "escape-time", value: shown(esc+"ms", escDef), ok: escOK,
		fix:     "set -sg escape-time 10",
		because: "Alt- keys and Escape lag by up to half a second.",
	})

	// Only "on" makes tmux forward an application's OSC 52 outward. The
	// "external" default lets tmux set the clipboard but drops ours.
	clip, clipDef := tmuxOption("set-clipboard")
	out = append(out, finding{
		name: "set-clipboard", value: shown(clip, clipDef), ok: clip == "on",
		fix:     "set -g set-clipboard on",
		because: "Copy stays inside tmux instead of reaching your local clipboard.",
	})

	ext, extDef := tmuxOption("extended-keys")
	out = append(out, finding{
		name: "extended-keys", value: shown(ext, extDef), ok: ext == "on" || ext == "always",
		fix:     "set -g extended-keys on",
		because: "Ctrl+Shift accelerators are unavailable. Chords still cover every command.",
	})

	term, termDef := tmuxOption("default-terminal")
	out = append(out, finding{
		name: "default-terminal", value: shown(term, termDef),
		ok:      strings.Contains(term, "256color"),
		fix:     `set -g default-terminal "tmux-256color"`,
		because: "Fewer than 256 colors reach the editor, so highlighting is coarse.",
	})

	return out
}
