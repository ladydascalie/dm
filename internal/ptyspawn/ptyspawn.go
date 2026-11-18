// Package ptyspawn gives a pane its terminal. On a machine that is the
// kernel's pseudo-terminal; a build with no kernel puts something else behind
// the same two calls, so the code above it never learns the difference.
package ptyspawn

import "io"

// Pty is the master side of a pane's terminal: the bytes the guest writes come
// out of Read, the keys written to it reach the guest, and Resize tells the
// guest the window changed the way SIGWINCH does.
type Pty interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
}
