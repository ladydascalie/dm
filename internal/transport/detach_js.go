//go:build js

package transport

import "os/exec"

// detachProcess has nothing to detach in a browser: there is no process tree,
// no session and no terminal to outlive. The browser harness hands the client
// a connection it made itself and never reaches this path, which exists only
// so the transport package compiles for js/wasm.
func detachProcess(cmd *exec.Cmd) {}
