package term

import (
	"io"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// translate runs input through a fresh mouseInput in one piece.
func translate(in string) string {
	m := newMouseInput(nil)
	m.feed([]byte(in))
	return string(m.out)
}

// x10 builds a legacy report: ESC [ M, then button, column and row, each as
// one byte offset by 32. Columns and rows are 1-based.
func x10(b, x, y int) string {
	return string([]byte{0x1b, '[', 'M', byte(32 + b), byte(32 + x), byte(32 + y)})
}

func TestX10ReportsBecomeSGR(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"press and release", x10(0, 41, 3) + x10(3, 41, 3), "\x1b[<0;41;3M\x1b[<0;41;3m"},
		{"right button release keeps the button", x10(2, 5, 6) + x10(3, 5, 6), "\x1b[<2;5;6M\x1b[<2;5;6m"},
		{"drag", x10(0, 1, 1) + x10(32, 2, 1), "\x1b[<0;1;1M\x1b[<32;2;1M"},
		{"wheel up", x10(64, 10, 10), "\x1b[<64;10;10M"},
		{"wheel down", x10(65, 10, 10), "\x1b[<65;10;10M"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := translate(tt.in); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// Everything that is not a mouse report must reach tcell unchanged.
func TestOtherInputPassesThroughUnchanged(t *testing.T) {
	for _, in := range []string{
		"hello, world",
		"\x1b",                // a lone Escape
		"\x1b[A\x1b[1;5C",     // arrows, Ctrl+Right
		"\x1bOP\x1b[15~",      // F1, F5
		"\x1b[200~x\x1b[201~", // bracketed paste
		"\x1b[<0;5;6M\x1b[<0;5;6m",
		"\x1b[<64;5;6M",
		"é漢\x00\x7f",
		"\x1b[<", "\x1b[<1;2", // incomplete: held, see below
	} {
		m := newMouseInput(nil)
		m.feed([]byte(in))
		got := string(m.out)
		if m.state != stateText {
			// Held only because the report is incomplete; the rest follows.
			continue
		}
		if got != in {
			t.Errorf("%q came out as %q", in, got)
		}
	}
}

// A lone Escape must go through at once, not wait for the next byte, or the
// Escape key would lag until the user typed something else.
func TestEscapeIsNotHeldBack(t *testing.T) {
	for _, in := range []string{"\x1b", "\x1b["} {
		if got := translate(in); got != in {
			t.Errorf("%q: passed on %q", in, got)
		}
	}
}

// Sequences that start like a report but are not one come out as they went in.
func TestNonReportsAreGivenBack(t *testing.T) {
	for _, in := range []string{
		"\x1b[M\x01x",     // control byte where a coordinate should be
		"\x1b[<1;2Mx",     // two numbers, not three
		"\x1b[<1;2;3;4Mx", // four numbers
		"\x1b[<ab",        // not a number
	} {
		if got := translate(in); got != in {
			t.Errorf("%q came out as %q", in, got)
		}
	}
}

// Over ssh a report can arrive split across reads at any byte.
func TestReportsSplitAnywhereTranslateTheSame(t *testing.T) {
	in := "ab" + x10(0, 41, 3) + x10(3, 41, 3) + "\x1b[<0;7;8M\x1b[<0;7;8m\x1b[Acd"
	want := translate(in)
	for cut := 1; cut < len(in); cut++ {
		m := newMouseInput(nil)
		m.feed([]byte(in[:cut]))
		m.feed([]byte(in[cut:]))
		if got := string(m.out); got != want {
			t.Errorf("split at %d: got %q, want %q", cut, got, want)
		}
	}
	m := newMouseInput(nil)
	for i := range len(in) {
		m.feed([]byte{in[i]})
	}
	if got := string(m.out); got != want {
		t.Errorf("byte at a time: got %q, want %q", got, want)
	}
}

// A press while a button is still down means its release went missing. The
// press must still start a new click, so a release is put in front of it.
func TestLostReleaseIsSupplied(t *testing.T) {
	got := translate("\x1b[<0;5;2M" + "\x1b[<0;5;3M\x1b[<0;5;3m")
	want := "\x1b[<0;5;2M" + "\x1b[<0;5;3m\x1b[<0;5;3M\x1b[<0;5;3m"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// Drag motion and the wheel are not new presses.
	for _, in := range []string{
		"\x1b[<0;5;2M\x1b[<32;6;2M\x1b[<32;7;2M\x1b[<0;7;2m",
		"\x1b[<0;5;2M\x1b[<64;5;2M\x1b[<0;5;2m",
	} {
		if got := translate(in); got != in {
			t.Errorf("%q came out as %q", in, got)
		}
	}
}

// fakeTty feeds bytes to tcell exactly as a terminal would.
type fakeTty struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func (f *fakeTty) Start() error        { return nil }
func (f *fakeTty) Stop() error         { return nil }
func (f *fakeTty) Drain() error        { return f.w.Close() } // wakes the reader, as a read deadline does
func (f *fakeTty) NotifyResize(func()) {}
func (f *fakeTty) WindowSize() (tcell.WindowSize, error) {
	return tcell.WindowSize{Width: 80, Height: 24}, nil
}
func (f *fakeTty) Read(b []byte) (int, error)  { return f.r.Read(b) }
func (f *fakeTty) Write(b []byte) (int, error) { return len(b), nil }
func (f *fakeTty) Close() error                { return f.r.Close() }

// The end-to-end check: an X10 click fed through the real tcell parser comes
// out as a click at the right cell, and types nothing into the file.
func TestX10ClickReachesTheEditorAsAClick(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	r, w := io.Pipe()
	s, err := tcell.NewTerminfoScreenFromTty(newMouseInput(&fakeTty{r: r, w: w}))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	defer s.Fini()
	s.EnableMouse(tcell.MouseButtonEvents | tcell.MouseDragEvents)

	go func() { _, _ = w.Write([]byte(x10(0, 41, 3) + x10(3, 41, 3) + "z")) }()

	type seen struct {
		btn  tcell.ButtonMask
		x, y int
	}
	var mice []seen
	for {
		ev := poll(t, s)
		switch ev := ev.(type) {
		case *tcell.EventMouse:
			x, y := ev.Position()
			mice = append(mice, seen{ev.Buttons(), x, y})
			continue
		case *tcell.EventKey:
			if ev.Rune() != 'z' {
				t.Errorf("click leaked a key: %q", ev.Rune())
				continue
			}
		default:
			continue
		}
		break
	}
	want := []seen{{tcell.Button1, 40, 2}, {tcell.ButtonNone, 40, 2}}
	if len(mice) != len(want) || mice[0] != want[0] || mice[1] != want[1] {
		t.Errorf("mouse events %+v, want %+v", mice, want)
	}
}

func poll(t *testing.T, s tcell.Screen) tcell.Event {
	t.Helper()
	ch := make(chan tcell.Event, 1)
	go func() { ch <- s.PollEvent() }()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no event within 5s")
		return nil
	}
}
