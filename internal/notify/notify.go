// Package notify holds the desktop notification policy: which pane state
// changes are worth interrupting the operator for, and how a title and body
// reach the notifier.
package notify

import (
	"context"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// notifyTimeout bounds one run of the desktop notifier. A notifier that hangs
// must not leave a process behind forever.
const notifyTimeout = 5 * time.Second

// StateChange reports whether a pane transition to state is worth an entry on
// the pane's notify topic. Blocked and error are, from a different state.
// Done is, only when the report carries a message: a bare done ends every
// agent turn, so an entry per turn would spend the box's shared cap on rows
// that say nothing, and the rail and the bar already show the pane finished.
func StateChange(from, to, message string) bool {
	if from == to {
		return false
	}
	switch to {
	case "blocked", "error":
		return true
	case "done":
		return strings.TrimSpace(message) != ""
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

// EnvTag names the marker a caller puts in front of every notification title
// and body. The test suite sets it, so a popup one of its scenarios triggers
// arrives named as an artifact of the suite: a desktop notification is the one
// surface the suite cannot take back, and without the tag a popup a test
// provokes looks exactly like the live alert the test is asserting on.
const EnvTag = "DM_NOTIFY_TAG"

// Tagged puts the marker from EnvTag in front of a title and a body. An unset
// or empty marker returns both untouched, which is every path outside a test
// suite.
func Tagged(title, body string) (string, string) {
	tag := os.Getenv(EnvTag)
	if tag == "" {
		return title, body
	}
	if body == "" {
		return tag + " " + title, ""
	}
	return tag + " " + title, tag + ": " + body
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
	title, body = Tagged(title, body)
	// The fork happens in the goroutine: the caller is the pane watch loop,
	// and a loaded host must not stall status handling.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, cfg.BinPath, "-a", "dm", "-t", "10000", "-u", "normal", title, body)
		if err := cmd.Run(); err != nil {
			// A dead daemon or a missing binary must leave a trace: stderr
			// lands in the server or pane log, and the caller never blocks.
			log.Printf("notify: %s: %v", cfg.BinPath, err)
		}
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
			log.Printf("notify: notify-send not found: %v", err)
			return
		}
		Send(Config{Enabled: true, BinPath: path}, title, body)
	}()
}
