//go:build !js

package client

import (
	"fmt"
	"os"
	"syscall"
)

// reexecSelf replaces the client process with the binary on disk, attached to
// the session this client is on now. A restart is where an upgrade lands, and
// the client is half of it: a fresh server with a stale client is only half a
// deploy. exec keeps the terminal file descriptors, so the new client draws
// into the same screen with no gap a detach would show.
//
// The argv is built, not reused: a client may have switched sessions since it
// started, so the original arguments name the wrong one, and a layout file they
// carried was applied on the first start.
func reexecSelf(session string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("restart: find the binary: %w", err)
	}
	if err := syscall.Exec(exe, []string{os.Args[0], "attach", "-s", session}, os.Environ()); err != nil {
		return fmt.Errorf("restart: exec %s: %w", exe, err)
	}
	return nil
}
