//go:build js

package client

import "os"

// notifyResize does nothing: a browser has no SIGWINCH. The harness that owns
// the viewport sends into ch itself when the size changes.
func notifyResize(ch chan<- os.Signal) { _ = ch }
