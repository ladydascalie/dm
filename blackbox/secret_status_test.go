package blackbox

// A status report travels outside the byte stream: the pane daemon decodes it
// out of the OSC 7501 sequence, so the scrub stage never sees a byte of it.
// The daemon scrubs the decoded message and app name with the live value list
// before it stores or publishes the status, and the box, the desktop
// notification, the state file and every read verb then carry the redaction
// alone.

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// statusSecret is the stub value this test registers. It is not a live
// secret: nothing here leaves the test's own state directory.
const statusSecret = "status-token-7711"

// TestStatusReportNeverCarriesTheValue registers a value, then has the pane
// report an error whose message is the value and whose app name carries it
// too. The report must reach the box, the notification and the state file as
// asterisks, and the state itself must still settle: the wait machinery reads
// it.
func TestStatusReportNeverCarriesTheValue(t *testing.T) {
	t.Parallel()
	h, notifyLog := notifyHarness(t)
	// The fetch path runs the registered tool. The fake notifier's directory
	// is already on PATH, so the pass stub goes beside it.
	script := "#!/bin/sh\necho " + statusSecret + "\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(notifyLog), "pass"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	h.run("new", "-s", "status")
	pane := h.focus("status")
	h.run("secret", "register", "tok", "--tool", "pass", "--ref", "stub", "-s", "status")
	h.run("secret", "use", "tok", "-s", "status")

	redacted := strings.Repeat("*", len(statusSecret))
	msg := base64.StdEncoding.EncodeToString([]byte(statusSecret))
	report := `printf '\033]7501;state=error:app=` + statusSecret + `:msg=` + msg + `\007'`
	h.run("write-to", pane, "-s", "status", "--msg", report, "--enter")

	// dm status: the pane's report as the server stored it.
	status := waitContains(t, "status", redacted, func() string {
		return h.run("status", pane, "-s", "status")
	})
	if !strings.Contains(status, "error") {
		t.Fatalf("status lost the state: %q", status)
	}
	if strings.Contains(status, statusSecret) {
		t.Fatalf("dm status printed the value: %q", status)
	}

	// dm exec read: the same status through the read action.
	read := h.rpc("status", map[string]any{"action": "read", "target": pane})
	st, _ := read["status"].(map[string]any)
	if st["state"] != "error" || st["message"] != redacted {
		t.Fatalf("read status = %v, want error and the redaction", st)
	}

	// dm exec list: the same report in the session snapshot, message and app
	// name alike, which is what the rail and the palette read.
	list := h.rpc("status", map[string]any{"action": "list"})
	snap, _ := list["snapshot"].(map[string]any)
	found := false
	for _, raw := range snap["all"].([]any) {
		p, _ := raw.(map[string]any)
		if p["id"] != pane {
			continue
		}
		found = true
		if p["message"] != redacted || p["app"] != redacted {
			t.Fatalf("list pane = %v, want the redaction in the message and the app", p)
		}
	}
	if !found {
		t.Fatalf("list does not name the pane %q", pane)
	}

	// dm msg show: the box entry the state change posted, keyed by the pane.
	entry := waitContains(t, "box", redacted, func() string {
		return h.run("msg", "show", "--key", "notify/"+pane, "-s", "status")
	})
	if strings.Contains(entry, statusSecret) {
		t.Fatalf("the box holds the value: %s", entry)
	}

	// The desktop notification the same entry renders.
	for _, line := range waitNotify(t, notifyLog, 1) {
		if strings.Contains(line, statusSecret) {
			t.Fatalf("the notification holds the value: %q", line)
		}
		if !strings.Contains(line, redacted) {
			t.Fatalf("the notification = %q, want the redaction", line)
		}
	}

	// At rest: the state file holds the box entry and the app name, never the
	// value.
	path := filepath.Join(h.stateDir(), "sessions", "status", "state.json")
	state := waitContains(t, "state file", redacted, func() string {
		b, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		return string(b)
	})
	if strings.Contains(state, statusSecret) {
		t.Fatalf("the state file holds the value: %s", state)
	}
}

// TestStatusReportWithNoValueIsUnchanged proves the scrub does not rewrite an
// ordinary report: a message with no value in it reaches the box as written.
func TestStatusReportWithNoValueIsUnchanged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "plain")
	pane := h.focus("plain")
	h.run("secret", "register", "tok", "--tool", "pass", "--ref", "stub", "-s", "plain")

	h.run("write-to", pane, "-s", "plain", "--msg",
		`printf '\033]7501;state=working:app=pi\007'`, "--enter")
	waitContains(t, "status", "working", func() string {
		return h.run("status", pane, "-s", "plain")
	})
	status := h.run("status", pane, "-s", "plain")
	fields := strings.Split(strings.TrimRight(status, "\n"), "\t")
	if len(fields) != 3 || fields[1] != "working" || fields[2] != "" {
		t.Fatalf("status = %q, want working with no message", status)
	}
}

// waitContains polls one command until its output holds want, then returns
// that output.
func waitContains(t *testing.T, what, want string, run func() string) string {
	t.Helper()
	deadline := time.Now().Add(waitBudget)
	var last string
	for {
		last = run()
		if strings.Contains(last, want) {
			return last
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never held %q: %q", what, want, last)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
