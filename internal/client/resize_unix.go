//go:build !js

package client

import (
	"os"
	"os/signal"
	"syscall"
)

// notifyResize delivers the terminal's resize signal on ch.
func notifyResize(ch chan<- os.Signal) {
	signal.Notify(ch, syscall.SIGWINCH)
}
