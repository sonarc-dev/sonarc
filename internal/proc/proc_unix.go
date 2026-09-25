//go:build unix

// Package proc runs child processes that must not outlive the editor's
// interest in them.
package proc

import (
	"os/exec"
	"syscall"
)

// SetGroup puts the child in its own process group.
//
// cscope can leave descendants behind, and make runs ctags, sort and cscope
// underneath it; on a shared server an orphaned indexer holding a kernel-sized
// index in memory is a real nuisance. Owning the group lets the whole tree be
// signalled at once.
func SetGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// KillTree terminates the child and everything it spawned. Signalling the
// negative pid addresses the whole process group.
func KillTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		// The group may already be gone, or we may not own it; fall back to
		// the process itself.
		_ = cmd.Process.Kill()
	}
}
