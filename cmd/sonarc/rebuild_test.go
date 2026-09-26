package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/sonarc-dev/sonarc/internal/index/rebuild"
)

// fakeKernelMake stands in for a kernel's `make cscope` / `make tags`. With
// FAKE_HOLD set it waits until that file appears, so a test can observe the
// editor while a rebuild is running.
const fakeKernelMake = `#!/bin/sh
for a in "$@"; do target="$a"; done
if [ -n "$FAKE_HOLD" ]; then
  while [ ! -e "$FAKE_HOLD" ]; do sleep 0.05; done
fi
case "$target" in
  tags) rm -f tags; printf 'NEW TAGS' > tags ;;
  cscope) printf 'fs/x.c\n' > cscope.files; printf 'NEW CSCOPE' > ncscope.out; mv ncscope.out cscope.out ;;
esac
`

// rebuildProject is a project harness whose root looks like a kernel tree and
// whose PATH has fake make, cscope and ctags. hold is the file that releases a
// held build.
func rebuildProject(t *testing.T) (h *harness, hold string) {
	t.Helper()
	h = project(t, map[string]string{
		"main.c":          mainC,
		"Makefile":        "# kernel\n",
		"Kconfig":         "# k\n",
		"scripts/tags.sh": "#!/bin/sh\n",
		"tags":            "OLD TAGS",
	}, "main.c")
	bin := t.TempDir()
	for name, body := range map[string]string{
		"make":   fakeKernelMake,
		"cscope": "#!/bin/sh\nexit 0\n",
		"ctags":  "#!/bin/sh\necho 'Exuberant Ctags 5.9'\n",
	} {
		p := filepath.Join(bin, name)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	hold = filepath.Join(t.TempDir(), "go")
	t.Setenv("FAKE_HOLD", hold)
	return h, hold
}

func release(t *testing.T, hold string) {
	t.Helper()
	if err := os.WriteFile(hold, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// waitRebuild waits for the rebuild to finish and applies its result.
func (h *harness) waitRebuild() {
	h.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for h.app.rebuilding != nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		h.app.pump()
	}
	if h.app.rebuilding != nil {
		h.t.Fatal("rebuild did not finish")
	}
}

func TestRebuildRunsInTheBackgroundAndReportsEachStep(t *testing.T) {
	h, hold := rebuildProject(t)
	h.answer("y")
	h.app.cmdRebuildIndex()

	if h.app.rebuilding == nil {
		t.Fatal("no rebuild is running after confirming")
	}
	// The editor keeps working while it runs.
	before := h.text()
	h.app.handle(tcell.NewEventKey(tcell.KeyRune, 'Q', tcell.ModNone))
	if h.text() == before {
		t.Error("typing did not work during the rebuild")
	}
	h.app.pump()
	if !strings.Contains(h.app.ui.Busy, "rebuilding index") || !strings.Contains(h.app.ui.Busy, "Ctrl+K x to cancel") {
		t.Errorf("Busy = %q, want rebuild progress and how to cancel", h.app.ui.Busy)
	}

	release(t, hold)
	h.waitRebuild()

	msg := h.app.ui.Msg
	for _, want := range []string{"cscope rebuilt in", "tags rebuilt in"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q should report %q", msg, want)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(h.app.root, "tags")); string(got) != "NEW TAGS" {
		t.Errorf("tags = %q", got)
	}
	if h.app.ui.Busy != "" {
		t.Errorf("progress line still shown after the rebuild: %q", h.app.ui.Busy)
	}
}

// Esc is how a query is cancelled. A minutes-long rebuild must not die to a
// habitual Esc.
func TestEscapeDoesNotCancelARebuild(t *testing.T) {
	h, hold := rebuildProject(t)
	h.answer("y")
	h.app.cmdRebuildIndex()
	h.app.handle(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	if h.app.rebuilding == nil {
		t.Fatal("Esc cancelled the rebuild")
	}
	release(t, hold)
	h.waitRebuild()
	if !strings.Contains(h.app.ui.Msg, "rebuilt") {
		t.Errorf("message = %q", h.app.ui.Msg)
	}
}

func TestCtrlKXAgainOffersToCancelAndKeepsTheOldIndex(t *testing.T) {
	h, hold := rebuildProject(t)
	defer release(t, hold)
	h.answer("y")
	h.app.cmdRebuildIndex()

	h.answer("y") // "Cancel it and keep the old index?"
	h.app.cmdRebuildIndex()
	h.waitRebuild()

	if !strings.Contains(h.app.ui.Msg, "cancelled") || !strings.Contains(h.app.ui.Msg, "unchanged") {
		t.Errorf("message = %q, want it to say the old index is kept", h.app.ui.Msg)
	}
	if got, _ := os.ReadFile(filepath.Join(h.app.root, "tags")); string(got) != "OLD TAGS" {
		t.Errorf("tags = %q after cancelling, want the old file", got)
	}
}

func TestCtrlKXAgainCanDeclineToCancel(t *testing.T) {
	h, hold := rebuildProject(t)
	h.answer("y")
	h.app.cmdRebuildIndex()
	h.answer("n")
	h.app.cmdRebuildIndex()
	if h.app.rebuilding == nil {
		t.Fatal("answering no cancelled the rebuild")
	}
	release(t, hold)
	h.waitRebuild()
}

func TestQuitDuringARebuildAsksThenRollsBack(t *testing.T) {
	h, hold := rebuildProject(t)
	defer release(t, hold)
	h.answer("y")
	h.app.cmdRebuildIndex()

	h.answer("n")
	h.app.cmdQuit()
	if h.app.quit {
		t.Fatal("quit went ahead after answering no")
	}

	h.answer("y")
	h.app.cmdQuit()
	if !h.app.quit {
		t.Fatal("quit did not happen after answering yes")
	}
	if got, _ := os.ReadFile(filepath.Join(h.app.root, "tags")); string(got) != "OLD TAGS" {
		t.Errorf("tags = %q after quitting mid-rebuild, want the old file", got)
	}
	if left, _ := filepath.Glob(filepath.Join(h.app.root, "*.sonarc-prev")); len(left) > 0 {
		t.Errorf("preserved copies left behind: %v", left)
	}
}

func TestDecliningTheRebuildDoesNothing(t *testing.T) {
	h, _ := rebuildProject(t)
	h.answer("n")
	h.app.cmdRebuildIndex()
	if h.app.rebuilding != nil {
		t.Error("a rebuild started after answering no")
	}
}

// A rebuild that creates indexes the project did not have must put them ahead
// of the built-in indexer, not behind it where Add would.
func TestNewIndexesAreRegisteredAheadOfTheBuiltin(t *testing.T) {
	h, hold := rebuildProject(t)
	os.Remove(filepath.Join(h.app.root, "tags"))
	h.answer("y")
	h.app.cmdRebuildIndex()
	release(t, hold)
	h.waitRebuild()

	var names []string
	for _, p := range h.app.index.Providers() {
		names = append(names, p.Name())
	}
	if got := strings.Join(names, ","); got != "cscope,ctags,builtin" {
		t.Errorf("provider order = %s, want cscope,ctags,builtin", got)
	}
}

func TestPromptSaysWhatWillRun(t *testing.T) {
	h, _ := rebuildProject(t)
	if d := rebuild.Detect(h.app.root).Describe(); !strings.Contains(d, "make cscope tags") {
		t.Errorf("Describe() = %q", d)
	}
}
