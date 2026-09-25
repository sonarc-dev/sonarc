//go:build !windows

package cscope

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// slowFirstScript hangs on the first query of each cscope *run* only if no
// marker exists yet, then answers normally. Restarted instances find the marker
// and answer at once, which is how a test tells "waited for the old process"
// from "started a fresh one".
const slowFirstScript = `#!/bin/sh
echo $$ > "$PIDFILE"
printf '>> '
while IFS= read -r line; do
  if [ ! -e "$MARK" ]; then
    : > "$MARK"
    sleep 30
  fi
  printf 'cscope: 1 lines\n'
  printf 'a.c fn 7 ok\n'
  printf '>> '
done
`

func processAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func readPid(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the stub never reported its pid")
	return 0
}

func openSlowFirst(t *testing.T) (db *DB, pidfile string) {
	t.Helper()
	tmp := t.TempDir()
	pidfile = filepath.Join(tmp, "pid")
	t.Setenv("PIDFILE", pidfile)
	t.Setenv("MARK", filepath.Join(tmp, "mark"))
	dir := fakeCscope(t, slowFirstScript)
	db, err := Open(filepath.Join(dir, "cscope.out"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, pidfile
}

// Cancelling a search must stop cscope, not leave it scanning the tree at full
// CPU. On a kernel checkout an abandoned text search kept a core busy for 35 s.
func TestCancelledQueryKillsTheSubprocess(t *testing.T) {
	db, pidfile := openSlowFirst(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := db.Query(ctx, FindText, "anything"); done <- err }()

	pid := readPid(t, pidfile)
	time.Sleep(100 * time.Millisecond) // let the query reach the process
	cancel()
	if err := <-done; err == nil {
		t.Fatal("a cancelled query should return an error")
	}

	deadline := time.Now().Add(3 * time.Second)
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if processAlive(pid) {
		syscall.Kill(pid, syscall.SIGKILL)
		t.Error("cscope was still running 3s after its query was cancelled")
	}
}

// cscope answers one request at a time. If an abandoned query is left to drain,
// the next one waits behind it.
func TestNextQueryAfterCancelDoesNotWaitBehindTheAbandonedOne(t *testing.T) {
	db, _ := openSlowFirst(t)

	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := db.Query(ctx, FindText, "slow"); first <- err }()
	time.Sleep(150 * time.Millisecond)
	cancel()
	<-first

	ctx2, cancel2 := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel2()
	start := time.Now()
	got, err := db.Query(ctx2, FindDefinition, "fast")
	if err != nil {
		t.Fatalf("the query after a cancel failed after %v: %v", time.Since(start), err)
	}
	if len(got) != 1 || got[0].Line != 7 {
		t.Errorf("got %+v, want the stub's single result", got)
	}
}

// A hard timeout is a cancellation too: it must not leave the process running.
func TestTimedOutQueryKillsTheSubprocess(t *testing.T) {
	db, pidfile := openSlowFirst(t)
	db.Timeout = 300 * time.Millisecond

	go func() { db.Query(context.Background(), FindText, "anything") }()
	pid := readPid(t, pidfile)
	time.Sleep(700 * time.Millisecond)

	deadline := time.Now().Add(3 * time.Second)
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if processAlive(pid) {
		syscall.Kill(pid, syscall.SIGKILL)
		t.Error("cscope survived its query's timeout")
	}
}
