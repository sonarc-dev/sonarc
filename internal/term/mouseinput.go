package term

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/gdamore/tcell/v2"
)

// mouseInput sits between the terminal and tcell's input parser and repairs
// the two ways mouse input goes wrong in practice.
//
// tcell understands only SGR mouse reports (ESC [ < b;x;y M). A terminal that
// ignores the request for them sends the legacy X10 form instead (ESC [ M and
// three raw bytes), which tcell does not recognize: the click does nothing and
// the three bytes are typed into the file. mouseInput rewrites X10 reports as
// SGR.
//
// A release can also go missing, for example when the button is let go
// outside the window. The editor then takes the next click for the tail of a
// drag and ignores it. A fresh press while a button is still down is therefore
// preceded by a synthetic release, so every press starts a new click.
//
// Everything else passes through byte for byte and without delay. Only bytes
// after ESC [ M or ESC [ < are ever held back, and no key produces those.
// TraceInput, when set before Init, receives every read from the terminal as
// it arrived and as it was passed on. It exists to answer "my clicks do
// nothing" from the far side of an ssh connection: the log shows whether the
// terminal sends anything at all, and in which form.
var TraceInput io.Writer

type mouseInput struct {
	tcell.Tty

	in  []byte // scratch for reads from the terminal
	out []byte // translated bytes not yet handed to tcell

	state   inputState
	csi     int    // how many bytes of ESC [ end the output so far
	seq     []byte // the mouse report collected in stateX10 or stateSGR
	down    bool   // a press has been passed on and its release has not
	downBtn int    // the button that is down, in SGR numbering
}

type inputState int

const (
	stateText inputState = iota // passing bytes through
	stateX10                    // after ESC [ M: collecting three raw bytes
	stateSGR                    // after ESC [ <: collecting b;x;y up to M or m
)

// maxSGR bounds a report's parameters; anything longer is not a mouse report.
const maxSGR = 24

func newMouseInput(tty tcell.Tty) *mouseInput {
	return &mouseInput{Tty: tty, in: make([]byte, 4096)}
}

// Start resets the translation, since input from before a suspend is gone.
func (m *mouseInput) Start() error {
	m.out, m.seq = m.out[:0], m.seq[:0]
	m.state, m.csi, m.down = stateText, 0, false
	return m.Tty.Start()
}

func (m *mouseInput) Read(b []byte) (int, error) {
	for len(m.out) == 0 {
		n, err := m.Tty.Read(m.in)
		before := len(m.out)
		m.feed(m.in[:n])
		if TraceInput != nil && n > 0 {
			fmt.Fprintf(TraceInput, "%s read %q -> %q\n",
				time.Now().Format("15:04:05.000"), m.in[:n], m.out[before:])
		}
		if err != nil && len(m.out) == 0 {
			return 0, err
		}
		if n == 0 {
			break // a drained or non-blocking read; tcell polls again
		}
	}
	n := copy(b, m.out)
	m.out = m.out[:copy(m.out, m.out[n:])]
	return n, nil
}

// feed translates p and appends the result to m.out.
func (m *mouseInput) feed(p []byte) {
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch m.state {
		case stateX10:
			if c < 32 { // not a report after all: give the bytes back
				m.abort()
				i--
				continue
			}
			m.seq = append(m.seq, c)
			if len(m.seq) == 3 {
				b, x, y := int(m.seq[0])-32, int(m.seq[1])-32, int(m.seq[2])-32
				release := b&3 == 3 && b&0x60 == 0
				if release {
					b = b&^3 | m.downBtn
				}
				m.report(b, max(x, 1), max(y, 1), release)
			}
			continue

		case stateSGR:
			if c == 'M' || c == 'm' {
				if b, x, y, ok := parseSGR(m.seq); ok {
					m.report(b, x, y, c == 'm')
					continue
				}
			} else if (c >= '0' && c <= '9' || c == ';') && len(m.seq) < maxSGR {
				m.seq = append(m.seq, c)
				continue
			}
			m.abort()
			i--
			continue
		}

		if m.csi == 2 && (c == 'M' || c == '<') {
			m.state, m.seq = stateX10, m.seq[:0]
			if c == '<' {
				m.state = stateSGR
			}
			continue
		}
		m.out = append(m.out, c)
		switch {
		case c == 0x1b:
			m.csi = 1
		case c == '[' && m.csi == 1:
			m.csi = 2
		default:
			m.csi = 0
		}
	}
}

// abort gives back a collected sequence that turned out not to be a report.
func (m *mouseInput) abort() {
	if m.state == stateX10 {
		m.out = append(m.out, 'M')
	} else {
		m.out = append(m.out, '<')
	}
	m.out = append(m.out, m.seq...)
	m.state, m.csi = stateText, 0
}

// report emits one mouse report as the remainder of an SGR sequence; the
// ESC [ that starts it has already been passed on.
func (m *mouseInput) report(b, x, y int, release bool) {
	motion, wheel := b&0x20 != 0, b&0x40 != 0
	if !release && !motion && !wheel && b&3 != 3 {
		if m.down {
			m.out = appendSGR(m.out, m.downBtn, x, y, 'm')
			m.out = append(m.out, 0x1b, '[')
		}
		m.down, m.downBtn = true, b&3
	}
	final := byte('M')
	if release {
		final = 'm'
		m.down = false
	}
	m.out = appendSGR(m.out, b, x, y, final)
	m.state, m.csi = stateText, 0
}

func appendSGR(out []byte, b, x, y int, final byte) []byte {
	out = append(out, '<')
	out = strconv.AppendInt(out, int64(b), 10)
	out = append(out, ';')
	out = strconv.AppendInt(out, int64(x), 10)
	out = append(out, ';')
	out = strconv.AppendInt(out, int64(y), 10)
	return append(out, final)
}

// parseSGR splits "b;x;y" into its three numbers.
func parseSGR(s []byte) (b, x, y int, ok bool) {
	var n [3]int
	k := 0
	for _, c := range s {
		if c == ';' {
			if k++; k > 2 {
				return 0, 0, 0, false
			}
			continue
		}
		n[k] = n[k]*10 + int(c-'0')
	}
	return n[0], n[1], n[2], k == 2
}
