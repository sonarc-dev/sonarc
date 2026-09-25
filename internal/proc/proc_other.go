//go:build !unix

// Package proc runs child processes that must not outlive the editor's
// interest in them.
package proc

import "os/exec"

// SetGroup is a no-op where process groups are unavailable.
func SetGroup(*exec.Cmd) {}

// KillTree terminates just the child.
func KillTree(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
