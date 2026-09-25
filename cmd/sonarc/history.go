package main

import (
	"path/filepath"
	"sonarc/internal/buffer"
)

// jump records where the cursor was before a navigation command, so going to a
// definition can be undone. This is the tag stack, reachable by both Ctrl+T
// (what vi users reach for) and Alt+Left (what everyone else does).
type jump struct {
	path string
	pos  buffer.Pos
}

// maxJumps bounds each history stack. One that grew without bound would keep
// every visited buffer reachable; a few hundred entries is far more history
// than anyone uses.
const maxJumps = 256

// here is the current position, as a history entry.
func (a *app) here() jump {
	v := a.v()
	return jump{path: v.Buf.Path(), pos: v.Head}
}

func pushBounded(stack []jump, j jump) []jump {
	stack = append(stack, j)
	if len(stack) > maxJumps {
		stack = stack[len(stack)-maxJumps:]
	}
	return stack
}

// pushJump records the current position so a navigation can be undone. A new
// navigation starts a new branch of history, so the way forward is dropped,
// as in a browser.
func (a *app) pushJump() {
	a.jumps = pushBounded(a.jumps, a.here())
	a.forward = a.forward[:0]
}

// cmdJumpBack pops the tag stack, remembering where it left from so Alt+Right
// can return there.
func (a *app) cmdJumpBack() {
	if len(a.jumps) == 0 {
		a.ui.Notify("no earlier position to return to")
		return
	}
	j := a.jumps[len(a.jumps)-1]
	from := a.here()
	if a.visit(j) {
		a.jumps = a.jumps[:len(a.jumps)-1]
		a.forward = pushBounded(a.forward, from)
	}
}

// cmdJumpForward retraces a step undone by jump-back.
func (a *app) cmdJumpForward() {
	if len(a.forward) == 0 {
		a.ui.Notify("no later position to go forward to")
		return
	}
	j := a.forward[len(a.forward)-1]
	from := a.here()
	if a.visit(j) {
		a.forward = a.forward[:len(a.forward)-1]
		a.jumps = pushBounded(a.jumps, from)
	}
}

// visit goes to a history entry, reporting whether it could.
func (a *app) visit(j jump) bool {
	if j.path != "" && j.path != a.v().Buf.Path() {
		if err := a.openFile(j.path); err != nil {
			a.ui.Error("cannot reopen %s: %v", filepath.Base(j.path), err)
			return false
		}
	}
	a.v().SetCursor(a.v().Buf.Clamp(j.pos))
	return true
}
