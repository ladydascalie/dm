//go:build js

package ptyspawn

import (
	"errors"
	"os/exec"
)

// ErrNoPty says there is no kernel to give a program a terminal. The browser
// build will put an in-memory terminal here; until it does, a pane in the
// browser cannot start a program and says so.
var ErrNoPty = errors.New("ptyspawn: no pty in this build")

// Start has nowhere to start cmd in a browser.
func Start(cmd *exec.Cmd, cols, rows int) (Pty, error) {
	return nil, ErrNoPty
}
