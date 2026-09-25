package main

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"sonarc/internal/buffer"
	"sonarc/internal/view"
)

// diskCheckEvery is how often open files are compared with the disk. A stat
// per open file is cheap enough to do often, and two seconds is quick enough
// that a `git checkout` or an agent's edit shows up before you look for it.
const diskCheckEvery = 2 * time.Second

// watchDisk checks open files for outside changes for as long as the editor
// runs. The goroutine only wakes the UI goroutine; all the work happens there.
func (a *app) watchDisk() {
	go func() {
		for range time.Tick(diskCheckEvery) {
			a.post(a.checkDisk)
		}
	}()
}

// checkDisk reacts to files another program has changed. An unedited file is
// reloaded, since there is nothing to lose. An edited one is left alone, with
// one warning: which version wins is the user's call, and the save guard makes
// sure it is a conscious one.
func (a *app) checkDisk() {
	defer a.saveSession()
	defer a.pollGit()
	// Files other programs create or delete show up in the tree too. Only
	// folders the user has opened are reread, which is a few directory
	// listings even on a kernel tree.
	if t := a.ui.Sidebar.Tree; t != nil && a.ui.Sidebar.Visible {
		defer t.Refresh()
	}
	for _, v := range a.views {
		state, now := v.Buf.OnDisk()
		if state == buffer.DiskSame {
			continue
		}
		// A deleted file's stamp is the zero value, so an absent entry must
		// not read as "already warned about this".
		if w, warned := a.warnedDisk[v]; warned && w == now {
			continue
		}
		name := displayName(v)
		switch {
		case state == buffer.DiskGone:
			a.warnedDisk[v] = now
			a.ui.Error("%s was deleted or moved on disk; Ctrl+S writes it back", name)
		case v.Buf.Modified():
			a.warnedDisk[v] = now
			a.ui.Error("%s changed on disk; your unsaved edits are kept. Ctrl+K ! reloads it and discards them", name)
		default:
			if err := v.Reload(); err != nil {
				a.ui.Error("%s changed on disk and could not be reread: %v", name, err)
				a.warnedDisk[v] = now
				continue
			}
			delete(a.warnedDisk, v)
			a.afterSave(v) // the file on disk moved on, as after a save
			a.ui.Notify("reloaded %s: it changed on disk", name)
		}
	}
}

// cmdReloadFile rereads the file on screen from disk, discarding unsaved
// edits after asking.
func (a *app) cmdReloadFile() {
	v := a.v()
	if v.Buf.Path() == "" {
		a.ui.Notify("this buffer has no file to reload")
		return
	}
	if v.Buf.Modified() {
		answer, ok := a.prompt("Discard your unsaved edits and reload "+displayName(v)+" from disk? [y/N]: ", "")
		if !ok || (answer != "y" && answer != "Y") {
			a.ui.Notify("cancelled")
			return
		}
	}
	if err := v.Reload(); err != nil {
		a.ui.Error("reload failed: %v", err)
		return
	}
	delete(a.warnedDisk, v)
	a.afterSave(v)
	a.ui.Notify("reloaded %s", displayName(v))
}

// writeView saves v, first asking before it overwrites a version of the file
// that another program wrote after sonarc read it. It reports success.
func (a *app) writeView(v *view.View) bool {
	switch state, now := v.Buf.OnDisk(); state {
	case buffer.DiskChanged:
		answer, ok := a.prompt(displayName(v)+" changed on disk since it was opened. Overwrite that version? [y/N]: ", "")
		if !ok || (answer != "y" && answer != "Y") {
			a.ui.Notify("not saved; Ctrl+K ! reloads the version on disk")
			return false
		}
		v.Buf.AcceptDisk(now)
	}
	if err := v.Buf.Save(); err != nil {
		a.ui.Error("save failed: %v", err)
		return false
	}
	delete(a.warnedDisk, v)
	a.afterSave(v)
	return true
}

// quitOnHangup ends the editor cleanly when its terminal goes away (an ssh
// connection dropping sends SIGHUP) or it is asked to stop, instead of dying
// on the spot, so the session is saved on the way out. It returns a function
// that stops listening.
func (a *app) quitOnHangup() func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP, syscall.SIGTERM)
	go func() {
		if _, ok := <-ch; ok {
			a.post(func() { a.quit = true })
		}
	}()
	return func() { signal.Stop(ch); close(ch) }
}
