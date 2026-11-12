package blackbox

import (
	"strings"
	"testing"
)

// TestTestEnvOwnsTheRoots proves the harness environment carries one value per
// dm key and the test's value wins. A duplicate would let exec read the tests
// value while stateDir read the operator's, which is the bug this guards.
func TestTestEnvOwnsTheRoots(t *testing.T) {
	base := []string{
		"PATH=/bin",
		"SHELL=/bin/zsh",
		"DM_STATE=/home/someone/.local/state/dm",
		"DM_SESSION=main",
		"DM_PANE=w1p1",
		"DM_NOTIFY=0",
		"DM_NERD=1",
		"DM_STATS=1",
		"DM_COLOR=16",
		"XDG_STATE_HOME=/home/someone/.local/state",
		"XDG_CONFIG_HOME=/home/someone/.config",
	}

	env := testEnv(base, "DM_STATE=/tmp/state", "XDG_CONFIG_HOME=/tmp/cfg")

	got := map[string][]string{}
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			t.Fatalf("entry %q has no value", e)
		}
		got[k] = append(got[k], v)
	}
	want := map[string]string{
		"PATH":            "/bin",
		"SHELL":           "/bin/zsh",
		"DM_STATE":        "/tmp/state",
		"XDG_CONFIG_HOME": "/tmp/cfg",
	}
	for k, v := range want {
		if len(got[k]) != 1 || got[k][0] != v {
			t.Fatalf("%s = %v, want exactly %q", k, got[k], v)
		}
	}
	for _, k := range []string{"DM_SESSION", "DM_PANE", "DM_NOTIFY", "DM_NERD", "DM_STATS", "DM_COLOR", "XDG_STATE_HOME"} {
		if len(got[k]) != 0 {
			t.Fatalf("%s leaked from the parent: %v", k, got[k])
		}
	}
}
