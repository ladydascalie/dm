//go:build !js

package transport

import (
	"os/exec"
	"syscall"
)

// detachProcess puts the spawned server in its own session, so the terminal
// it was started from can close without taking it down.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
