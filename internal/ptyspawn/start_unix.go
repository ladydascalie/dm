//go:build !js

package ptyspawn

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

// Start starts cmd on a new pseudo-terminal of the given size and returns the
// master side, which the pane reads and writes.
func Start(cmd *exec.Cmd, cols, rows int) (Pty, error) {
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	return master{f}, nil
}

// master is the kernel's pty master.
type master struct{ *os.File }

// Resize sets the terminal's size.
func (m master) Resize(cols, rows int) error {
	return pty.Setsize(m.File, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}
