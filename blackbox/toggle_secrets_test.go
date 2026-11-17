package blackbox

// The secret redaction toggle end to end: a real pane, a real secret value
// registered through the secret path, the real keybinding through a real
// attach client, and the pane's screen as the server sees it. While the
// switch is off the pane echoes the raw value; after the switch returns on
// the same value scrubs to asterisks again.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// toggleSecret is the value the test registers and echoes. It clears
// MinSecretLen by a wide margin, so the scrubber keeps it.
const toggleSecret = "surface-token-7788"

// TestToggleSecretsKeybindingEndToEnd spawns a pane, registers a value
// through the secret path, drives the toggle keybinding end to end, and
// asserts the pane echoes the raw value while the switch is off and
// asterisks again after the switch returns on.
func TestToggleSecretsKeybindingEndToEnd(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// The secret path fetches through the registered tool. A pass stub on
	// PATH prints the value, so the test never needs a real store.
	bin := t.TempDir()
	script := "#!/bin/sh\necho " + toggleSecret + "\n"
	if err := os.WriteFile(filepath.Join(bin, "pass"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	for i, e := range h.env {
		if strings.HasPrefix(e, "PATH=") {
			h.env[i] = "PATH=" + bin + string(os.PathListSeparator) + strings.TrimPrefix(e, "PATH=")
			break
		}
	}

	h.run("new", "-s", "scrub")
	root := h.focus("scrub")
	h.run("secret", "register", "tok", "--tool", "pass", "--ref", "stub", "-s", "scrub")
	h.run("secret", "use", "tok", "-s", "scrub")

	a := startAttach(t, h, "scrub")
	a.waitContains(t, firstPaint)

	// With the switch on, the pane screen hides the value from everyone,
	// server side included.
	h.run("write-to", root, "-s", "scrub", "--msg", "echo "+toggleSecret, "--enter")
	h.waitFor("scrub", root, strings.Repeat("*", len(toggleSecret)))

	// The keybinding toggles the focused pane off. The toast names the pane
	// and the state it landed on, so an unprotected pane never reads as
	// protected.
	a.write(prefixKey + "\x07")
	a.waitContains(t, "redaction off: "+root)

	// With the switch off the pane echoes the raw value.
	a.write("echo " + toggleSecret + "-off\r")
	waitPaneRaw(t, h, "scrub", root, toggleSecret+"-off")

	// The same key returns the switch on, and the return repaints. The raw
	// echo the reveal left on the screen is rewritten to asterisks by the
	// pane itself: no clear, no new output and no scrollback to hide it.
	a.write(prefixKey + "\x07")
	a.waitContains(t, "redaction on: "+root)
	stars := strings.Repeat("*", len(toggleSecret))
	waitPanePlain(t, h, "scrub", root, stars+"-off", toggleSecret)
}

// waitPaneRaw polls the pane screen until the raw value shows, failing when
// the switch the keybinding flipped did not take.
func waitPaneRaw(t *testing.T, h *harness, session, pane, needle string) {
	t.Helper()
	start := time.Now()
	deadline := start.Add(waitBudget)
	var last string
	for time.Now().Before(deadline) {
		out, err := h.try("read", pane, "-s", session)
		last = out
		if err == nil && strings.Contains(out, needle) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("pane %s never echoed %q raw within the wait budget; last read:\n%s", pane, needle, last)
}

// waitPanePlain polls the pane screen until needle shows and gone is absent,
// failing when the switch the keybinding flipped did not repaint what the
// reveal painted.
func waitPanePlain(t *testing.T, h *harness, session, pane, needle, gone string) {
	t.Helper()
	deadline := time.Now().Add(waitBudget)
	var last string
	for time.Now().Before(deadline) {
		out, err := h.try("read", pane, "-s", session)
		last = out
		if err == nil && strings.Contains(out, needle) && !strings.Contains(out, gone) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("pane %s never repainted %q and dropped %q within the wait budget; last read:\n%s",
		pane, needle, gone, last)
}
