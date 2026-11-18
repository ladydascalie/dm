package websession

import "dm/internal/layout"

// reportSession pushes one session event: the shape of what the person built,
// counted from the session's own model. It rides with every snapshot, so the
// page draws the counts beside the screen they describe. It carries no step: the
// lesson never sees this event and the page holds no rule for it.
func (s *Session) reportSession() {
	if s.forward == nil {
		return
	}
	windows, panes := 0, 0
	for _, ws := range s.workspaces {
		windows += len(ws.Windows)
		for _, w := range ws.Windows {
			panes += len(layout.AllPanes(w.Root))
		}
	}
	focus := ""
	if w := s.activeWindow(); w != nil {
		focus = w.Focus
	}
	s.forward(map[string]any{
		"type":       "session",
		"workspaces": len(s.workspaces),
		"windows":    windows,
		"panes":      panes,
		"focus":      focus,
	})
}
