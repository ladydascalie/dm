//go:build js

package client

import "os/exec"

// detachUpdate does nothing in a browser tab: there is no second process to
// leave behind, and the spawn fails and is ignored on its own.
func detachUpdate(*exec.Cmd) {}
