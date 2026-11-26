//go:build !js

package client

import (
	"os/exec"
	"syscall"
)

// detachUpdate puts the refresh in its own session, so the terminal that
// started it can close without taking it down and its signals stay its own.
func detachUpdate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
