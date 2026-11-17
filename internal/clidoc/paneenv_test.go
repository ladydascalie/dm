package clidoc

import (
	"strings"
	"testing"
)

// TestPaneEnvNoteRendered proves the operator copy of the pane env rule is the
// text the man page and the HTML page render, so the prose and the code cannot
// disagree through a stale or escaped copy.
func TestPaneEnvNoteRendered(t *testing.T) {
	t.Parallel()
	if !strings.Contains(ManPage(), roffEscape(PaneEnvNote)) {
		t.Error("man page does not render PaneEnvNote")
	}
	if !strings.Contains(HTMLPage(), htmlEscape(PaneEnvNote)) {
		t.Error("HTML page does not render PaneEnvNote")
	}
}
