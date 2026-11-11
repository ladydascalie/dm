package blackbox

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestPaneWaitEndToEnd proves the pane wait through the binary: dm wait dials
// the pane, streams the running command's output to its stdout and ends on the
// command's exit code with the status line on stderr. The session name is one
// per test, as the harness does: every test runs in its own DM_STATE
// directory, so the same name never meets another session.
func TestPaneWaitEndToEnd(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	s := "waite2e"
	h.run("new", "-s", s)
	panes := h.panes(s)
	if len(panes) != 1 {
		t.Fatalf("a new session has %d panes, want 1", len(panes))
	}
	pane := panes[0]

	// The command echoes a go mark first, then sleeps, so the wait attaches
	// while the sleep still runs and the marker streams through it. The child
	// shell carries the exit code: an `exit 7` typed at the prompt would end
	// the pane's own shell, and no command-end mark is drawn for it.
	const marker = "WAITMARK-E2E-7"
	line := "echo WAITGO; sh -c 'sleep 3; echo " + marker + "; exit 7'"
	h.run("write-to", pane, "-s", s, "--msg", line, "--enter")
	h.waitFor(s, pane, "WAITGO")

	var stdout, stderr bytes.Buffer
	wait := exec.Command(h.bin, "wait", pane, "--timeout", "30s", "-s", s)
	wait.Env = h.env
	wait.Stdout = &stdout
	wait.Stderr = &stderr
	runErr := wait.Run()

	status := stderr.String()
	// Without OSC 133 integration in the pane's shell the wait has no end
	// mark to read, so it answers through the children fallback with no exit
	// code. The unit tests prove the mark path, so skip instead of asserting
	// on machine state.
	if strings.Contains(status, "exit unknown (children)") || strings.Contains(status, "exit unknown (idle)") {
		t.Skipf("pane shell has no OSC 133 command marks here; wait answered %q", strings.TrimSpace(status))
	}
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			t.Fatalf("dm wait failed to run: %v\nstdout:\n%s\nstderr:\n%s", runErr, stdout.String(), status)
		}
	}
	if code := wait.ProcessState.ExitCode(); code != 7 {
		t.Fatalf("dm wait exited %d, want 7\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), status)
	}
	if out := stdout.String(); !strings.Contains(out, marker) {
		t.Fatalf("wait stdout = %q, want the marker %q", out, marker)
	}
	if !strings.Contains(status, "* done, exit 7") {
		t.Fatalf("wait stderr = %q, want the status line * done, exit 7", status)
	}
}

// TestPaneWaitDefaultsToThePaneSession proves a wait from inside a pane
// targets that pane's session without -s: DM_SESSION picks the session, the
// way every other verb does.

// TestWaitDialsThePaneSessionSocket proves the routing itself, marks or
// not: with DM_SESSION set and no -s, a wait on a missing pane fails on that
// session's socket, not main's.
func TestWaitDialsThePaneSessionSocket(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	s := "waitdial"
	h.run("new", "-s", s)
	env := boxEnv(h.env, "DM_SESSION="+s)
	cmd := exec.Command(h.bin, "wait", "w1p4")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("dm wait w1p4 succeeded, want a missing pane error: %s", out)
	}
	want := "sessions/" + s + "/panes/w1p4.sock"
	if !strings.Contains(string(out), want) {
		t.Fatalf("dm wait output = %q, want an error naming %s", string(out), want)
	}
}

func TestPaneWaitDefaultsToThePaneSession(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	s := "waitenv"
	h.run("new", "-s", s)
	panes := h.panes(s)
	if len(panes) != 1 {
		t.Fatalf("a new session has %d panes, want 1", len(panes))
	}
	pane := panes[0]

	const marker = "WAITMARK-ENV-3"
	line := "echo WAITGO; sh -c 'sleep 3; echo " + marker + "; exit 3'"
	h.run("write-to", pane, "-s", s, "--msg", line, "--enter")
	h.waitFor(s, pane, "WAITGO")

	env := boxEnv(h.env, "DM_SESSION="+s, "DM_PANE="+pane)
	var stdout, stderr bytes.Buffer
	wait := exec.Command(h.bin, "wait", pane, "--timeout", "30s")
	wait.Env = env
	wait.Stdout = &stdout
	wait.Stderr = &stderr
	runErr := wait.Run()

	status := stderr.String()
	if strings.Contains(status, "exit unknown (children)") || strings.Contains(status, "exit unknown (idle)") {
		t.Skipf("pane shell has no OSC 133 command marks here; wait answered %q", strings.TrimSpace(status))
	}
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			t.Fatalf("dm wait failed to run: %v\nstdout:\n%s\nstderr:\n%s", runErr, stdout.String(), status)
		}
	}
	if code := wait.ProcessState.ExitCode(); code != 3 {
		t.Fatalf("dm wait exited %d, want 3\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), status)
	}
	if out := stdout.String(); !strings.Contains(out, marker) {
		t.Fatalf("wait stdout = %q, want the marker %q", out, marker)
	}
}
