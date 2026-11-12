package blackbox

// The session default end to end: a verb typed inside a pane (DM_SESSION set,
// no -s) targets that session; a dead one falls back to main with one stderr
// line; an explicit -s wins over the env.

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// tryEnv runs the binary with extra env appended to the harness env and keeps
// stdout and stderr apart, so a test can assert on the fallback line.
func (h *harness) tryEnv(extra []string, args ...string) (string, string, error) {
	h.t.Helper()
	cmd := exec.Command(h.bin, args...)
	cmd.Env = append(append([]string{}, h.env...), extra...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// seedBox posts one entry with a known message, so a list can name the session
// the verb really hit.
func (h *harness) seedBox(session, msg string) {
	h.t.Helper()
	h.run("msg", "post", "-s", session, "--state", "note", "--msg", msg)
}

// TestVerbInsidePaneTargetsItsSession proves a verb run with DM_SESSION set
// and no -s lands on that session: its box answers, main's never opens, and
// nothing is written to stderr.
func TestVerbInsidePaneTargetsItsSession(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "probe")
	h.seedBox("probe", "probe-entry")

	stdout, stderr, err := h.tryEnv([]string{"DM_SESSION=probe"}, "msg", "list")
	if err != nil {
		t.Fatalf("msg list: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "probe-entry") {
		t.Fatalf("msg list on the pane session missed the entry: %q", stdout)
	}
}

// TestDeadPaneSessionFallsBackToMain proves a verb run inside a dead pane
// still answers: it targets main and says so on stderr, one line naming both
// sessions.
func TestDeadPaneSessionFallsBackToMain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "main")
	h.run("new", "-s", "doomed")
	h.seedBox("main", "main-entry")
	h.run("kill", "doomed")

	stdout, stderr, err := h.tryEnv([]string{"DM_SESSION=doomed"}, "msg", "list")
	if err != nil {
		t.Fatalf("msg list after fallback: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if strings.Count(stderr, "\n") != 1 {
		t.Fatalf("stderr lines = %d, want 1: %q", strings.Count(stderr, "\n"), stderr)
	}
	for _, want := range []string{"doomed", "main"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr %q misses %q", stderr, want)
		}
	}
	if !strings.Contains(stdout, "main-entry") {
		t.Fatalf("msg list after fallback missed main's entry: %q", stdout)
	}
}

// TestSessionFlagBeatsThePaneEnv proves -s passed explicitly wins over
// DM_SESSION, even when the env session is live.
func TestSessionFlagBeatsThePaneEnv(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "main")
	h.run("new", "-s", "other")
	h.seedBox("main", "main-entry")

	stdout, stderr, err := h.tryEnv([]string{"DM_SESSION=other"}, "msg", "list", "-s", "main")
	if err != nil {
		t.Fatalf("msg list -s main: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "main-entry") {
		t.Fatalf("msg list -s main missed main's entry: %q", stdout)
	}
}
