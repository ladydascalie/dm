package blackbox

// A pane's first bytes are scrubbed. The shell starts only after the server
// adopts the pane and pushes the value list, so a command that prints a
// registered value the instant it runs never reaches the screen or the ring.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const spawnSecret = "spawn-token-4411"

// TestPaneSpawnScrubsItsFirstBytes registers a value, then splits a pane
// whose command echoes it immediately. The screen and the history must show
// asterisks and never the value: the daemon holds the shell until the
// adopted set is in place.
func TestPaneSpawnScrubsItsFirstBytes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// The fetch path runs the registered tool. A pass stub prints the value,
	// so the test needs no store.
	bin := t.TempDir()
	script := "#!/bin/sh\necho " + spawnSecret + "\n"
	if err := os.WriteFile(filepath.Join(bin, "pass"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	for i, e := range h.env {
		if strings.HasPrefix(e, "PATH=") {
			h.env[i] = "PATH=" + bin + string(os.PathListSeparator) + strings.TrimPrefix(e, "PATH=")
			break
		}
	}

	h.run("new", "-s", "spawn")
	root := h.focus("spawn")
	h.run("secret", "register", "tok", "--tool", "pass", "--ref", "stub", "-s", "spawn")
	h.run("secret", "use", "tok", "-s", "spawn")

	// The pane's first output is the value, with no delay to hide behind.
	res := h.rpc("spawn", map[string]any{
		"action": "split", "target": root, "dir": "down",
		"argv": []string{"sh", "-c", "echo " + spawnSecret + "; sleep 60"},
	})
	pane, _ := res["pane"].(string)
	if pane == "" {
		t.Fatalf("split returned no pane: %v", res)
	}

	screen := h.waitFor("spawn", pane, strings.Repeat("*", len(spawnSecret)))
	if strings.Contains(screen, spawnSecret) {
		t.Fatalf("pane %s showed its first value unscrubbed:\n%s", pane, screen)
	}
	for i, line := range h.history("spawn", pane, 50) {
		if strings.Contains(line, spawnSecret) {
			t.Fatalf("pane %s history line %d holds the raw value: %q", pane, i, line)
		}
	}
}
