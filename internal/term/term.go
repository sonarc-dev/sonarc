// Package term wraps the terminal: bringing it up, tearing it down safely, and
// working out what it can actually do.
//
// sonarc's primary environment is ssh into tmux or screen, where several
// things a GUI editor takes for granted are simply unavailable. Rather than
// assume and degrade badly, capabilities are probed once at startup and the
// rest of the editor asks Caps what it is allowed to rely on.
package term

import (
	"fmt"
	"os"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// Multiplexer identifies the terminal multiplexer wrapping us, if any.
type Multiplexer int

const (
	MuxNone Multiplexer = iota
	MuxTmux
	MuxScreen
)

func (m Multiplexer) String() string {
	switch m {
	case MuxTmux:
		return "tmux"
	case MuxScreen:
		return "screen"
	}
	return "none"
}

// Tier describes how much keyboard fidelity the terminal offers. It decides
// which key bindings are advertised, so the help screen never shows a binding
// that cannot fire here.
type Tier int

const (
	// TierFull reports distinct events for Ctrl+Shift and similar combinations,
	// because the terminal negotiated an extended keyboard protocol.
	TierFull Tier = iota
	// TierBasic is the common ssh+tmux case: Ctrl and Alt work, Ctrl+Shift does
	// not. Chord bindings carry everything here.
	TierBasic
	// TierMinimal is a bare console: few colors, no mouse, chords only.
	TierMinimal
)

func (t Tier) String() string {
	switch t {
	case TierFull:
		return "full"
	case TierBasic:
		return "basic"
	}
	return "minimal"
}

// Caps is what this terminal can do.
type Caps struct {
	TERM      string
	Mux       Multiplexer
	Colors    int
	TrueColor bool
	Mouse     bool
	Tier      Tier

	// MouseBlocked says why clicks cannot arrive although the terminal
	// supports them, or is empty. Mouse reporting stays on regardless, so the
	// mouse starts working the moment the user fixes the cause.
	MouseBlocked string
}

// Screen is the terminal, plus what we know about it.
type Screen struct {
	tcell.Screen
	Caps  Caps
	Theme Theme
}

// detectCaps inspects the environment and the initialized screen.
func detectCaps(s tcell.Screen) Caps {
	c := Caps{
		TERM:   os.Getenv("TERM"),
		Colors: s.Colors(),
		Mouse:  s.HasMouse(),
	}

	switch {
	case os.Getenv("TMUX") != "":
		c.Mux = MuxTmux
	case os.Getenv("STY") != "":
		c.Mux = MuxScreen
	case strings.HasPrefix(c.TERM, "screen"):
		// TERM=screen without $STY usually still means a multiplexer; treat it
		// as one so the conservative key bindings are used.
		c.Mux = MuxScreen
	}

	// COLORTERM is the only broadly honored signal for 24-bit color. Inside a
	// multiplexer it is frequently absent even when the outer terminal can do
	// it, which is exactly why the theme targets 256 colors.
	switch os.Getenv("COLORTERM") {
	case "truecolor", "24bit":
		c.TrueColor = true
	}

	switch {
	case c.Colors < 16 || c.TERM == "" || c.TERM == "dumb":
		c.Tier = TierMinimal
	case c.Mux != MuxNone:
		// tmux forwards the extended key protocols only when extended-keys is
		// enabled, and screen never does. Assume the worst; chords cover it.
		c.Tier = TierBasic
	case c.TrueColor || strings.Contains(c.TERM, "kitty") || strings.Contains(c.TERM, "xterm"):
		c.Tier = TierFull
	default:
		c.Tier = TierBasic
	}
	return c
}

// Wrap adapts an already-initialized tcell.Screen, detecting its capabilities
// and choosing a theme. Init uses it for the real terminal; tests use it to
// drive the editor against a tcell SimulationScreen with no terminal at all.
func Wrap(ts tcell.Screen) *Screen {
	s := &Screen{Screen: ts}
	s.Caps = detectCaps(ts)
	s.Theme = ThemeFor(s.Caps)
	return s
}

// Init brings up the terminal and returns a Screen.
//
// The caller must arrange for Close to run on every exit path, including a
// panic; see Guard. Leaving a terminal in raw mode after a crash is the single
// rudest thing a TUI can do to someone on a remote shell.
func Init() (*Screen, error) {
	tty, err := tcell.NewDevTty()
	if err != nil {
		return nil, fmt.Errorf("cannot open terminal: %w", err)
	}
	ts, err := tcell.NewTerminfoScreenFromTty(newMouseInput(tty))
	if err != nil {
		return nil, fmt.Errorf("cannot open terminal: %w", err)
	}
	if err := ts.Init(); err != nil {
		return nil, fmt.Errorf("cannot initialize terminal: %w", err)
	}

	s := Wrap(ts)

	if s.Caps.Mouse {
		// Drag events are needed for click-and-drag selection.
		ts.EnableMouse(tcell.MouseButtonEvents | tcell.MouseDragEvents)
		if s.Caps.Mux == MuxTmux && TmuxMouseOff() {
			s.Caps.MouseBlocked = "tmux has mouse off, so clicks never reach sonarc. Turn it on: tmux set -g mouse on"
		}
	}
	if TraceInput != nil {
		fmt.Fprintf(TraceInput, "terminal: %s; mouse requested: %v %s\n", s.Caps.Describe(), s.Caps.Mouse, s.Caps.MouseBlocked)
	}
	// Bracketed paste keeps a pasted block from being treated as typing, which
	// would otherwise trigger auto-indent on every line of it.
	ts.EnablePaste()
	ts.SetStyle(s.Theme.Text)
	ts.Clear()
	return s, nil
}

// Close restores the terminal to how it was found. It is safe to call more than
// once, so the normal exit path and the panic guard can both call it.
func (s *Screen) Close() {
	if s == nil || s.Screen == nil {
		return
	}
	s.Screen.Fini()
	s.Screen = nil
}

// Guard restores the terminal and then re-panics, preserving the original
// stack. Use it as `defer scr.Guard()` in main so a crash still leaves a usable
// shell behind and the panic remains visible.
func (s *Screen) Guard() {
	if r := recover(); r != nil {
		s.Close()
		panic(r)
	}
}

// Describe renders a one-line summary of the detected terminal, for the status
// bar and for --doctor.
func (c Caps) Describe() string {
	term := c.TERM
	if term == "" {
		term = "(unset)"
	}
	s := fmt.Sprintf("%s, %d colors, keys=%s", term, c.Colors, c.Tier)
	if c.Mux != MuxNone {
		s += ", in " + c.Mux.String()
	}
	if c.TrueColor {
		s += ", truecolor"
	}
	switch {
	case !c.Mouse:
		s += ", no mouse"
	case c.MouseBlocked != "":
		s += ", mouse off in tmux"
	}
	return s
}
