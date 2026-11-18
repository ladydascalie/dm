package pane

import "io"

// testPty is a pty that cannot be resized, for tests that only read or write
// through one. The real master resizes the kernel's window; a pipe has none.
type testPty struct{ io.ReadWriteCloser }

// Resize does nothing: a pipe has no terminal behind it.
func (testPty) Resize(cols, rows int) error { return nil }
