package server

import (
	"dm/internal/rail"
	"dm/internal/theme"
)

// refreshProjectAccentsLocked pins a rail accent slot on every live project
// name the state has not placed yet and releases every placed name whose
// last workspace closed. A new name takes its preferred slot, or the next
// free one when the preference is held, and a placed name never moves, so a
// project keeps its hue while neighbours come and go. Released slots refill
// by the same walk over new names only, so nothing else reshuffles. The
// caller holds s.mu. It reports whether the map moved, so a caller can
// decide to save.
func (s *server) refreshProjectAccentsLocked() bool {
	live := map[string]bool{}
	for _, ws := range s.st.Workspaces {
		if ws.Project != "" {
			live[ws.Project] = true
		}
	}
	changed := false
	for name := range s.st.ProjectAccent {
		if !live[name] {
			delete(s.st.ProjectAccent, name)
			changed = true
		}
	}
	for _, ws := range s.st.Workspaces {
		name := ws.Project
		if name == "" {
			continue
		}
		if _, ok := s.st.ProjectAccent[name]; ok {
			continue
		}
		if s.st.ProjectAccent == nil {
			s.st.ProjectAccent = map[string]int{}
		}
		s.st.ProjectAccent[name] = pickAccentSlot(name, s.st.ProjectAccent)
		changed = true
	}
	return changed
}

// pickAccentSlot walks from a name's preferred slot to the first one no
// placed project holds. It is the walk the rail's live allocator makes, so a
// session that never persisted a map still colours the way the rail drew.
func pickAccentSlot(name string, placed map[string]int) int {
	slots := len(theme.DefaultTheme().Accents)
	used := make(map[int]bool, len(placed))
	for _, slot := range placed {
		used[slot] = true
	}
	pref := rail.RailAccentSlot(name)
	for i := 0; i < slots; i++ {
		slot := (pref + i) % slots
		if !used[slot] {
			return slot
		}
	}
	return pref
}
