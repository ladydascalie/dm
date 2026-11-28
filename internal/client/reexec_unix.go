//go:build !js

package client

import (
	"fmt"
	"os"
	"syscall"
)

// reexecSelf replaces the client process with the binary on disk, attached to
// the session this client is on now, and resumed: the new process inherits the
// alternate screen rather than opening it again. A restart is where an upgrade
// lands, and the client is half of it, because a fresh server with a stale
// client is only half a deploy. exec keeps the terminal file descriptors and
// the terminal's own saved screen, so the handover shows no shell in between.
//
// The argv is built, not reused: a client may have switched sessions since it
// started, so the original arguments name the wrong one, and a layout file they
// carried was applied on the first start.
func reexecSelf(session string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("restart: find the binary: %w", err)
	}
	if err := syscall.Exec(exe, []string{os.Args[0], "__client", "-s", session}, os.Environ()); err != nil {
		return fmt.Errorf("restart: exec %s: %w", exe, err)
	}
	return nil
}
