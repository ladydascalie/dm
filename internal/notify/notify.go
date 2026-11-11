// Package notify holds the desktop notification policy: which pane state
// changes are worth interrupting the operator for, and how a title and body
// reach the notifier.
package notify

import (
	"context"
	"os"
	"os/exec"
	"time"
)

// notifyTimeout bounds one run of the desktop notifier. A notifier that hangs
// must not leave a process behind forever.
const notifyTimeout = 5 * time.Second

// StateChange reports whether a pane transition to state is worth
// interrupting the operator for: blocked, done or error, and only from a
// different state, so a repeated report notifies once.
func StateChange(from, to string) bool {
	if from == to {
		return false
	}
	switch to {
	case "blocked", "done", "error":
		return true
	}
	return false
}

// Urgent reports whether a notify entry's state deserves a desktop
// notification: blocked and error are urgent, everything else is not.
// StateChange stays the transition filter; urgency is a second, independent
// gate the renderers apply, so an entry that passes one can still fail the
// other.
func Urgent(state string) bool {
	switch state {
	case "blocked", "error":
		return true
	}
	return false
}

// Config is the explicit notifier configuration: the on/off switch and the
// path to the notify-send binary.
type Config struct {
	Enabled bool
	BinPath string
}

// Send hands a title and a body to the desktop notifier under explicit
// configuration. It is a no-op when cfg.Enabled is false, cfg.BinPath is
// empty or the title is empty. The process is started here and reaped in the
// background under notifyTimeout, so a caller never waits for the notifier.
func Send(cfg Config, title, body string) {
	if !cfg.Enabled || cfg.BinPath == "" || title == "" {
		return
	}
	// The fork happens in the goroutine: the caller is the pane watch loop,
	// and a loaded host must not stall status handling.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, cfg.BinPath, "-a", "dm", "-t", "10000", "-u", "normal", title, body)
		_ = cmd.Run()
	}()
}

// SystemNotify hands a title and a body to the desktop notifier. It is the
// env-defaults wrapper around Send: it is a no-op when DM_NOTIFY is "0" and
// resolves notify-send from PATH. The lookup and the fork happen in the
// goroutine, so a caller never waits for the notifier.
func SystemNotify(title, body string) {
	if title == "" || os.Getenv("DM_NOTIFY") == "0" {
		return
	}
	// The lookup and the fork happen in the goroutine: the caller is the pane
	// watch loop, and a loaded host must not stall status handling.
	go func() {
		path, err := exec.LookPath("notify-send")
		if err != nil {
			return
		}
		Send(Config{Enabled: true, BinPath: path}, title, body)
	}()
}
