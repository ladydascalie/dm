package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// notifyTimeout bounds one run of the desktop notifier. A notifier that hangs
// must not leave a process behind forever.
const notifyTimeout = 5 * time.Second

// notifyStateChange reports whether a pane transition to state is worth
// interrupting the operator for: blocked, done or error, and only from a
// different state, so a repeated report notifies once.
func notifyStateChange(from, to string) bool {
	if from == to {
		return false
	}
	switch to {
	case "blocked", "done", "error":
		return true
	}
	return false
}

// systemNotify hands a title and a body to the desktop notifier. It is a
// no-op when DM_NOTIFY is "0" or notify-send is not on PATH. The process is
// started here and reaped in the background under notifyTimeout, so a caller
// never waits for the notifier.
func systemNotify(title, body string) {
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
		ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, path, "-a", "dm", "-t", "10000", "-u", "normal", title, body)
		_ = cmd.Run()
	}()
}

// notifyTitleLocked names the session, the pane, its recorded agent name and
// the state. The name is left out when no one recorded one. Callers hold s.mu.
func (s *server) notifyTitleLocked(id, state string) string {
	parts := []string{"dm", s.st.Name, id}
	if ps := s.st.Panes[id]; ps != nil && ps.Name != "" {
		parts = append(parts, ps.Name)
	}
	return strings.Join(append(parts, state), " ")
}

// operatorSeesLocked reports whether a client is attached and looking at the
// pane. The guards keep a session that is being torn down from panicking on an
// empty layout. Callers hold s.mu.
func (s *server) operatorSeesLocked(id string) bool {
	if len(s.clients) == 0 || len(s.st.Workspaces) == 0 {
		return false
	}
	return s.st.win().Focus == id
}

// notifyPaneState raises a desktop notification for a state a pane entered,
// unless the operator is looking at it. It reads the state under the lock and
// returns before the notifier runs, so the watch loop never waits on it.
func (s *server) notifyPaneState(id, state, message string) {
	s.mu.Lock()
	if s.operatorSeesLocked(id) {
		s.mu.Unlock()
		return
	}
	title := s.notifyTitleLocked(id, state)
	s.mu.Unlock()
	systemNotify(title, message)
}
