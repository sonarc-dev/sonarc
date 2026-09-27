package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/sonarc-dev/sonarc/internal/update"
)

// updateEvery is how often the editor asks GitHub whether a newer release
// exists. The answer is kept in state.json, so opening many editors in a day
// asks once.
const updateEvery = 24 * time.Hour

// executable is the path of the running binary, through any symlink, so an
// update replaces the file rather than the link to it.
func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return exe, nil
}

// runUpdate is `sonarc -update`: replace exe with the latest release. It
// returns the process exit code.
func runUpdate(out io.Writer, src update.Source, exe string) int {
	fail := func(err error) int {
		fmt.Fprintf(out, "sonarc: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	tag, err := src.Latest(ctx)
	if err != nil {
		return fail(err)
	}
	switch {
	case tag == version:
		fmt.Fprintf(out, "sonarc %s is the latest version\n", version)
		return 0
	case isRelease(version) && !update.Newer(tag, version):
		fmt.Fprintf(out, "sonarc %s is newer than the latest release, %s; nothing to do\n", version, tag)
		return 0
	}
	fmt.Fprintf(out, "updating sonarc %s to %s (%s)\n", version, tag, update.Asset())
	if err := src.Install(ctx, tag, exe); err != nil {
		return fail(err)
	}
	fmt.Fprintf(out, "installed %s at %s\n", tag, exe)
	return 0
}

// isRelease reports whether v is a release version rather than a build from
// a checkout.
func isRelease(v string) bool { return update.Newer("v999999.0.0", v) }

// checkForUpdate tells the user, once a day at most, when a newer release is
// out. It is quiet when offline, in development builds, and when
// SONARC_NO_UPDATE_CHECK is set, which also stops the request itself.
func (a *app) checkForUpdate() {
	if os.Getenv("SONARC_NO_UPDATE_CHECK") != "" || !isRelease(version) || a.statePath == "" {
		return
	}
	if update.Newer(a.updateLatest, version) {
		a.announceUpdate(a.updateLatest)
	}
	if a.now().Sub(time.Unix(a.updateChecked, 0)) < updateEvery {
		return
	}
	src := a.updates
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		tag, err := src.Latest(ctx)
		if err != nil {
			return // offline, blocked or rate limited: try again next start
		}
		a.post(func() {
			a.updateChecked, a.updateLatest = a.now().Unix(), tag
			a.saveState()
			if update.Newer(tag, version) {
				a.announceUpdate(tag)
			}
		})
	}()
}

func (a *app) announceUpdate(tag string) {
	a.ui.Notify("sonarc %s is available; run sonarc -update to install it", tag)
}
