package server

import (
	"strings"

	"dm/internal/notify"
)

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
	return s.st.ActiveWindow().Focus == id
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
	notify.SystemNotify(title, message)
}
