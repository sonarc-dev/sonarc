package term

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeTmux puts a stub tmux on PATH that reports the given options, so the
// diagnostics can be tested without tmux installed. opts maps option name to
// the value tmux should report; anything absent is reported as unset.
func fakeTmux(t *testing.T, opts map[string]string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub is POSIX-only")
	}
	dir := t.TempDir()

	var sb strings.Builder
	sb.WriteString("#!/bin/sh\n")
	sb.WriteString(`if [ "$1" = "-V" ]; then echo "tmux 3.4"; exit 0; fi` + "\n")
	// display -p [-t pane] '#{mouse}' reports the value in effect for the pane.
	sb.WriteString(`if [ "$1" = "display" ]; then echo '` + opts["#{mouse}"] + `'; exit 0; fi` + "\n")
	// Args arrive as: show <scope> <name>
	sb.WriteString(`name="$3"` + "\n")
	sb.WriteString("case \"$name\" in\n")
	for k, v := range opts {
		sb.WriteString("  " + k + ") echo '" + v + "' ;;\n")
	}
	sb.WriteString("  *) : ;;\nesac\nexit 0\n")

	path := filepath.Join(dir, "tmux")
	if err := os.WriteFile(path, []byte(sb.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestDoctorFlagsBadTmuxSettings(t *testing.T) {
	fakeTmux(t, map[string]string{
		"escape-time":      "500",
		"set-clipboard":    "external",
		"extended-keys":    "off",
		"default-terminal": "screen",
		"mouse":            "off",
	})

	findings := tmuxFindings()
	if len(findings) != 5 {
		t.Fatalf("got %d findings, want 5", len(findings))
	}
	for _, f := range findings {
		if f.ok {
			t.Errorf("%s reported OK at a known-bad value %q", f.name, f.value)
		}
		if f.fix == "" || f.because == "" {
			t.Errorf("%s has no fix or explanation", f.name)
		}
	}
}

func TestDoctorAcceptsGoodTmuxSettings(t *testing.T) {
	fakeTmux(t, map[string]string{
		"escape-time":      "10",
		"set-clipboard":    "on",
		"extended-keys":    "on",
		"default-terminal": "tmux-256color",
		"mouse":            "on",
	})

	for _, f := range tmuxFindings() {
		if !f.ok {
			t.Errorf("%s reported a problem at a known-good value %q", f.name, f.value)
		}
	}
}

// An unset option is not unknown: tmux is using its default, and that default
// is what the user is actually living with, so it must be judged on merit.
func TestUnsetOptionsAreJudgedByTmuxDefault(t *testing.T) {
	fakeTmux(t, map[string]string{}) // nothing configured

	findings := tmuxFindings()
	for _, f := range findings {
		if f.ok {
			t.Errorf("%s: tmux defaults are all suboptimal, but it reported OK", f.name)
		}
		if !strings.Contains(f.value, "tmux default") {
			t.Errorf("%s: value %q should be marked as a default", f.name, f.value)
		}
		if strings.Contains(f.value, "unset") {
			t.Errorf("%s: reported %q instead of the effective default", f.name, f.value)
		}
	}
}

// With $TMUX set but no tmux binary, the doctor must say it could not check
// rather than reporting every setting as broken.
func TestDoctorHandlesMissingTmuxBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("TMUX", "/tmp/fake,1,0")
	t.Setenv("TERM", "tmux-256color")

	var buf bytes.Buffer
	Doctor(&buf)
	out := buf.String()

	if !strings.Contains(out, "not on PATH") {
		t.Errorf("doctor should report that tmux could not be run:\n%s", out)
	}
	if strings.Contains(out, "FIX") {
		t.Errorf("doctor invented fixes without being able to read settings:\n%s", out)
	}
}

func TestDoctorReportsTerminalBasics(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("TMUX", "")
	t.Setenv("STY", "")

	var buf bytes.Buffer
	Doctor(&buf)
	out := buf.String()

	for _, want := range []string{"xterm-256color", "truecolor", "256"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output missing %q:\n%s", want, out)
		}
	}
}

func TestEnvCapsTierDetection(t *testing.T) {
	tests := []struct {
		name      string
		term      string
		colorterm string
		tmux      string
		want      Tier
	}{
		// tmux is the primary environment and never reports Ctrl+Shift by
		// default, so it must never be classified as full fidelity.
		{"tmux is basic", "tmux-256color", "", "/tmp/x,1,0", TierBasic},
		{"tmux stays basic even with truecolor", "tmux-256color", "truecolor", "/tmp/x,1,0", TierBasic},
		{"modern xterm is full", "xterm-256color", "truecolor", "", TierFull},
		{"screen is basic", "screen-256color", "", "", TierBasic},
		{"dumb is minimal", "dumb", "", "", TierMinimal},
		{"unset TERM is minimal", "", "", "", TierMinimal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TERM", tt.term)
			t.Setenv("COLORTERM", tt.colorterm)
			t.Setenv("TMUX", tt.tmux)
			t.Setenv("STY", "")

			if got := envCaps().Tier; got != tt.want {
				t.Errorf("tier = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEnvCapsDetectsMultiplexer(t *testing.T) {
	tests := []struct {
		name            string
		term, tmux, sty string
		want            Multiplexer
	}{
		{"tmux via TMUX", "xterm-256color", "/tmp/x,1,0", "", MuxTmux},
		{"screen via STY", "xterm-256color", "", "1234.pts-0", MuxScreen},
		{"screen via TERM", "screen-256color", "", "", MuxScreen},
		{"plain terminal", "xterm-256color", "", "", MuxNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TERM", tt.term)
			t.Setenv("TMUX", tt.tmux)
			t.Setenv("STY", tt.sty)
			if got := envCaps().Mux; got != tt.want {
				t.Errorf("mux = %v, want %v", got, tt.want)
			}
		})
	}
}

// A terminal reporting fewer than 256 colors must get the reduced theme, or
// every color collapses to the nearest of eight and the UI becomes unreadable.
func TestThemeDegradesWithColorCount(t *testing.T) {
	if ThemeFor(Caps{Colors: 256}) == ThemeFor(Caps{Colors: 8}) {
		t.Error("256-color and 8-color terminals got the same theme")
	}
}

// tmux keeps mouse per session. "tmux set mouse on" in this session leaves the
// global value off, so the pane's own value must win, whichever way it goes.
func TestTmuxMouseIsReadForThisPane(t *testing.T) {
	fakeTmux(t, map[string]string{"mouse": "off", "#{mouse}": "1"})
	if TmuxMouseOff() {
		t.Error("mouse is on for this pane but was reported off")
	}
	fakeTmux(t, map[string]string{"mouse": "on", "#{mouse}": "0"})
	if !TmuxMouseOff() {
		t.Error("mouse is off for this pane but was reported on")
	}
	// A tmux too old to answer display falls back to the option.
	fakeTmux(t, map[string]string{})
	if !TmuxMouseOff() {
		t.Error("unset mouse is tmux's default, off")
	}
}
