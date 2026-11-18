//go:build js

package pane

import "os/exec"

// hangupShell has no signal to send: a browser has no process to hang up.
func hangupShell(cmd *exec.Cmd) {}
