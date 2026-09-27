package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/sonarc-dev/sonarc/internal/buffer"
	"github.com/sonarc-dev/sonarc/internal/index/builtin"
	"github.com/sonarc-dev/sonarc/internal/index/cscope"
	"github.com/sonarc-dev/sonarc/internal/index/provider"
	"github.com/sonarc-dev/sonarc/internal/index/tags"
	"github.com/sonarc-dev/sonarc/internal/search"
	"github.com/sonarc-dev/sonarc/internal/term"
	"github.com/sonarc-dev/sonarc/internal/ui"
	"github.com/sonarc-dev/sonarc/internal/update"
	"github.com/sonarc-dev/sonarc/internal/vcs"
	"github.com/sonarc-dev/sonarc/internal/view"
)

// app is the running editor.
type app struct {
	scr  *term.Screen
	ui   *ui.UI
	quit bool

	// views holds every open buffer; cur indexes the visible one.
	views []*view.View
	cur   int

	// root is the project directory that indexes and result paths are
	// relative to.
	root string

	// index answers navigation queries from cscope, ctags, and the built-in
	// indexer, in that order of preference.
	index   *provider.Registry
	cscope  *cscope.Provider
	tags    *tags.Provider
	builtin *builtin.Index
	// jumps is where navigation came from, most recent last; forward is where
	// jump-back left from, so the step can be retraced.
	jumps   []jump
	forward []jump

	// clip is an internal clipboard, used when the terminal cannot give us the
	// system one. Copy always writes here as well as attempting OSC 52, so
	// copy/paste works even on a terminal that supports nothing.
	clip []byte

	// pasting accumulates bracketed-paste content so it lands as one edit
	// rather than as a stream of keystrokes that would trigger auto-indent.
	pasting  bool
	pasteBuf []byte

	// pressed is set between mouse press and release, and pressRegion is where
	// the press landed. tcell reports Button1 for both the press and every
	// motion event after it, so the origin is what tells a drag that began in
	// the text (extend the selection) from a press elsewhere that merely
	// moved over the text on its way past.
	pressed     bool
	pressRegion ui.Region

	// resized is set while a drag of the sidebar edge has changed the width,
	// so the new width is saved once, on release, rather than on every step.
	resized bool

	// statePath is where settings that outlive a session are kept; see
	// state.go. Empty means nothing is saved, which is how tests run.
	statePath string
	// themeName is the theme the user picked, empty for the default.
	themeName string

	// keys is the key map in use: the defaults with keys.conf applied.
	keys *bindings

	// updates is where new releases are looked for, and updateChecked and
	// updateLatest what was last found there; see checkForUpdate.
	updates       update.Source
	updateChecked int64
	updateLatest  string
	// savedSession is the session file as last written, to skip rewriting
	// it when nothing has changed.
	savedSession string

	// warnedDisk is the on-disk version each view was last warned about, so
	// an outside change is reported once rather than every check.
	warnedDisk map[*view.View]buffer.Stamp

	// git is what git says about the project; see git.go.
	git gitState

	// click remembers the last press in the text so a repeat on the same cell
	// within multiClickWindow counts as a double or triple click. tcell
	// reports no click count of its own. now is a seam for tests.
	click clickState
	now   func() time.Time

	// chord holds the prefix key of a pending chord, zero when none is active.
	chord tcell.Key

	// pickAction is what Enter does in the current picker. Nil means the
	// default: jump to the selected location. The fuzzy opener and the command
	// palette reuse the same picker with a different action.
	pickAction func(ui.PickerItem)

	// running is the background query in flight, if any. Only the UI goroutine
	// touches it. inbox holds closures background goroutines have queued for
	// the UI goroutine to run; see post and pump. keyCount counts key presses so
	// a finished query can tell whether the user has moved on.
	running *job
	// rebuilding is the index rebuild in flight, if any; see rebuild.go.
	rebuilding *rebuildJob
	inbox      []func()
	inboxMu    sync.Mutex
	keyCount   uint64

	// matcher is the accepted search, kept so F3 can step through it after the
	// find prompt closes. lastSearch seeds the prompt next time.
	matcher    *search.Matcher
	lastSearch string
	searchOpts search.Options
}

// newApp assembles an editor around a screen and a buffer.
func newApp(scr *term.Screen, buf *buffer.Buffer) *app {
	v := view.New(buf)
	a := &app{scr: scr, views: []*view.View{v}, now: time.Now, warnedDisk: map[*view.View]buffer.Stamp{}, keys: defaultBindings()}
	a.ui = ui.New(scr, v)
	a.ui.Changes = func(v *view.View) []vcs.Hunk { return a.hunksFor(v) }
	return a
}

// v returns the view currently on screen.
func (a *app) v() *view.View { return a.ui.View }

// run opens the given files or folder and drives the editor until the user quits.
func run(args []string, lineNo int) error {
	l := parseLaunch(args)

	var first *buffer.Buffer
	var err error
	if len(l.files) > 0 {
		first, err = buffer.Open(l.files[0])
		if err != nil {
			return err
		}
	} else {
		first = buffer.New()
	}

	scr, err := term.Init()
	if err != nil {
		return err
	}
	a := newApp(scr, first)

	// Restore the terminal on every exit path. Guard runs first and re-panics,
	// so a crash still leaves a usable shell and the stack trace survives.
	defer scr.Close()
	defer scr.Guard()
	defer a.closeIndex()

	if len(l.files) > 1 {
		for i, extra := range l.files[1:] {
			if b, err := buffer.Open(extra); err == nil {
				v := view.New(b)
				a.recallPlace(v)
				place(v, l.at[i+1])
				a.views = append(a.views, v)
			}
		}
	}

	a.statePath = defaultStatePath()
	a.loadState()
	a.loadKeys()
	a.setProject(l, first)
	if len(l.files) == 0 {
		a.restoreSession()
	}
	a.startIndex()
	a.startGit()
	a.watchDisk()
	a.checkForUpdate()

	// -line only means something when a file is open to put the cursor in.
	if len(l.files) > 0 {
		if l.at[0].line == 0 {
			a.recallPlace(a.v())
		}
		place(a.v(), l.at[0])
		if lineNo > 0 {
			a.v().Goto(lineNo)
		}
	}
	switch {
	case first.Large():
		a.ui.Notify("large file: syntax highlighting is off")
	case scr.Caps.MouseBlocked != "":
		a.ui.Notify("%s", scr.Caps.MouseBlocked)
	}

	stopSignals := a.quitOnHangup()
	defer stopSignals()
	a.loop()
	// However the loop ended, a quit or a lost connection, keep where the
	// user was. Closing the second pane first hands a mirror's place to the
	// file's own view, which is the one remembered.
	a.dropOther()
	a.saveSession()
	a.rememberPlaces(a.views...)
	return nil
}

func (a *app) loop() {
	for !a.quit {
		a.pump()
		a.ui.Draw()
		a.scr.Show()

		ev := a.poll()
		if ev == nil {
			return // screen finished
		}
		a.handle(ev)
	}
}

// poll reads the next event. Every loop reads through it, the prompts and
// pickers included, so a mouse release they swallow still ends the press. A
// key also ends it: nobody types while holding a mouse button, and a press
// left open would make the next click look like a drag and do nothing.
func (a *app) poll() tcell.Event {
	ev := a.scr.PollEvent()
	switch ev := ev.(type) {
	case *tcell.EventMouse:
		if ev.Buttons() == tcell.ButtonNone {
			a.pressed = false
		}
	case *tcell.EventKey:
		a.pressed = false
	}
	return ev
}

// handle dispatches a single event. Keeping this separate from the polling loop
// is what lets the tests drive the editor against a simulation screen.
func (a *app) handle(ev tcell.Event) {
	if term.TraceInput != nil {
		traceEvent(ev)
	}
	switch ev := ev.(type) {
	case *tcell.EventKey:
		a.onKey(ev)
	case *tcell.EventMouse:
		a.onMouse(ev)
	case *tcell.EventPaste:
		a.onPaste(ev)
	case *tcell.EventResize:
		a.scr.Sync()
	case *tcell.EventError:
		a.ui.Error("%v", ev)
	}
}

func (a *app) reportIf(ok bool, msg string) {
	if !ok {
		a.ui.Notify("%s", msg)
	}
}

// traceEvent logs an event the loop is about to handle, beside the raw input
// the terminal layer logs; see -trace-input.
func traceEvent(ev tcell.Event) {
	w := term.TraceInput
	stamp := ev.When().Format("15:04:05.000")
	switch ev := ev.(type) {
	case *tcell.EventMouse:
		x, y := ev.Position()
		fmt.Fprintf(w, "%s event mouse buttons=%d x=%d y=%d mods=%d\n", stamp, ev.Buttons(), x, y, ev.Modifiers())
	case *tcell.EventKey:
		fmt.Fprintf(w, "%s event key %s\n", stamp, ev.Name())
	default:
		fmt.Fprintf(w, "%s event %T\n", stamp, ev)
	}
}
