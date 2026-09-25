//go:build unix

package main

import (
	"syscall"
	"testing"
	"time"
)

// A dropped ssh connection sends SIGHUP. The editor must leave through its
// normal exit, which saves the session, rather than die on the signal.
func TestHangupQuitsCleanly(t *testing.T) {
	h := newHarness(t, "x\n")
	stop := h.app.quitOnHangup()
	defer stop()
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !h.app.quit {
		if time.Now().After(deadline) {
			t.Fatal("SIGHUP did not ask the editor to quit")
		}
		time.Sleep(5 * time.Millisecond)
		h.app.pump()
	}
}
