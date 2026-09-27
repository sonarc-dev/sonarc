package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sonarc-dev/sonarc/internal/update"
)

// fakeReleases serves a latest release and its files, counting how often the
// latest release is asked for.
func fakeReleases(t *testing.T, tag string, binary []byte) (update.Source, *atomic.Int32) {
	t.Helper()
	sum := sha256.Sum256(binary)
	var asked atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+update.Repo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		w.Write([]byte(`{"tag_name":"` + tag + `"}`))
	})
	mux.HandleFunc("/dl/"+tag+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(hex.EncodeToString(sum[:]) + "  " + update.Asset() + "\n"))
	})
	mux.HandleFunc("/dl/"+tag+"/"+update.Asset(), func(w http.ResponseWriter, r *http.Request) {
		w.Write(binary)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return update.Source{API: srv.URL, Download: srv.URL + "/dl"}, &asked
}

func withVersion(t *testing.T, v string) {
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

func TestUpdateInstallsANewerRelease(t *testing.T) {
	withVersion(t, "v0.1.0")
	src, _ := fakeReleases(t, "v0.2.0", []byte("V2"))
	exe := filepath.Join(t.TempDir(), "sonarc")
	os.WriteFile(exe, []byte("V1"), 0o755)

	var out strings.Builder
	if code := runUpdate(&out, src, exe); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if data, _ := os.ReadFile(exe); string(data) != "V2" {
		t.Errorf("binary = %q", data)
	}
	if !strings.Contains(out.String(), "updating sonarc v0.1.0 to v0.2.0") {
		t.Errorf("output: %s", out.String())
	}
}

func TestUpdateLeavesTheLatestAlone(t *testing.T) {
	withVersion(t, "v0.2.0")
	src, _ := fakeReleases(t, "v0.2.0", []byte("V2"))
	exe := filepath.Join(t.TempDir(), "sonarc")
	os.WriteFile(exe, []byte("MINE"), 0o755)

	var out strings.Builder
	if code := runUpdate(&out, src, exe); code != 0 || !strings.Contains(out.String(), "is the latest version") {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if data, _ := os.ReadFile(exe); string(data) != "MINE" {
		t.Errorf("binary replaced: %q", data)
	}
}

// The editor mentions a newer release, remembers the answer, and does not
// ask again the same day.
func TestEditorAnnouncesANewerReleaseOncePerDay(t *testing.T) {
	withVersion(t, "v0.1.0")
	src, asked := fakeReleases(t, "v0.2.0", nil)
	h := newHarness(t, "")
	path := saving(t, h)
	h.app.updates = src
	h.app.checkForUpdate()
	waitFor(t, h, func() bool { return strings.Contains(h.app.ui.Msg, "v0.2.0 is available") })
	if !strings.Contains(readState(t, path), `"update_latest":"v0.2.0"`) {
		t.Errorf("state = %s", readState(t, path))
	}

	// A new editor the same day announces from the saved answer, without asking.
	h2 := newHarness(t, "")
	h2.app.statePath = path
	h2.app.loadState()
	h2.app.updates = src
	h2.app.checkForUpdate()
	h2.app.pump()
	if !strings.Contains(h2.app.ui.Msg, "v0.2.0 is available") {
		t.Errorf("message = %q", h2.app.ui.Msg)
	}
	time.Sleep(50 * time.Millisecond)
	if n := asked.Load(); n != 1 {
		t.Errorf("GitHub was asked %d times, want 1", n)
	}
}

func TestUpdateCheckCanBeTurnedOff(t *testing.T) {
	withVersion(t, "v0.1.0")
	t.Setenv("SONARC_NO_UPDATE_CHECK", "1")
	src, asked := fakeReleases(t, "v0.2.0", nil)
	h := newHarness(t, "")
	saving(t, h)
	h.app.updates = src
	h.app.checkForUpdate()
	time.Sleep(100 * time.Millisecond)
	h.app.pump()
	if n := asked.Load(); n != 0 || h.app.ui.Msg != "" {
		t.Errorf("asked %d times, message %q", n, h.app.ui.Msg)
	}
}

// A build from a checkout is not a release, so it never nags.
func TestDevelopmentBuildsDoNotCheck(t *testing.T) {
	withVersion(t, "dev")
	src, asked := fakeReleases(t, "v0.2.0", nil)
	h := newHarness(t, "")
	saving(t, h)
	h.app.updates = src
	h.app.checkForUpdate()
	time.Sleep(100 * time.Millisecond)
	if n := asked.Load(); n != 0 {
		t.Errorf("asked %d times", n)
	}
}

func waitFor(t *testing.T, h *harness, ok func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		h.app.pump()
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}
