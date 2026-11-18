//go:build !js

package pane

import (
	"os/exec"
	"syscall"
)

// hangupShell tells the shell its terminal is gone, which is what closing a
// pty master does on a machine.
func hangupShell(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGHUP)
	}
}
