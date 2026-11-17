package blackbox

// The status state field, end to end. Every verb prints the state unscrubbed,
// so a program that reports its value as the state must not publish it: the
// parser validates the field against the protocol's set and reads anything
// else as unknown.

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStatusStateCannotCarryTheValue registers a value, reports it as the
// status state, and checks the verbs. The message in the same report is the
// landing marker: the report was accepted, and the state is still unknown.
func TestStatusStateCannotCarryTheValue(t *testing.T) {
	t.Parallel()
	h, notifyLog := notifyHarness(t)
	// The secret path fetches through the registered tool, so the value is a
	// live redaction value and not a literal the test made up.
	script := "#!/bin/sh\necho " + statusSecret + "\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(notifyLog), "pass"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	h.run("new", "-s", "state")
	pane := h.focus("state")
	h.run("secret", "register", "tok", "--tool", "pass", "--ref", "stub", "-s", "state")
	h.run("secret", "use", "tok", "-s", "state")

	msg := base64.StdEncoding.EncodeToString([]byte("reason"))
	report := `printf '\033]7501;state=` + statusSecret + `:app=pi:msg=` + msg + `\007'`
	h.run("write-to", pane, "-s", "state", "--msg", report, "--enter")

	// The report is in: its message reached the read action.
	read := func() map[string]any {
		return h.rpc("state", map[string]any{"action": "read", "target": pane})
	}
	waitContains(t, "the read message", "reason", func() string {
		st, _ := read()["status"].(map[string]any)
		s, _ := st["message"].(string)
		return s
	})

	// dm status prints the state raw: it must be a protocol word.
	status := h.run("status", pane, "-s", "state")
	if strings.Contains(status, statusSecret) {
		t.Fatalf("dm status printed the value: %q", status)
	}
	fields := strings.Split(strings.TrimRight(status, "\n"), "\t")
	if len(fields) != 3 || fields[1] != "unknown" {
		t.Fatalf("dm status = %q, want unknown as the state", status)
	}

	// dm exec read and dm exec list carry the same state.
	got := read()
	st, _ := got["status"].(map[string]any)
	if st["state"] != "unknown" {
		t.Fatalf("read state = %v, want unknown", st["state"])
	}
	if strings.Contains(jsonText(t, got), statusSecret) {
		t.Fatalf("the read reply holds the value: %v", got)
	}
	list := h.rpc("state", map[string]any{"action": "list"})
	snap, _ := list["snapshot"].(map[string]any)
	found := false
	for _, raw := range snap["all"].([]any) {
		p, _ := raw.(map[string]any)
		if p["id"] != pane {
			continue
		}
		found = true
		if p["status"] != "unknown" {
			t.Fatalf("list status = %v, want unknown", p["status"])
		}
		if strings.Contains(jsonText(t, p), statusSecret) {
			t.Fatalf("the list row holds the value: %v", p)
		}
	}
	if !found {
		t.Fatalf("list does not name the pane %q", pane)
	}
}

// jsonText renders a reply for a leak check.
func jsonText(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
