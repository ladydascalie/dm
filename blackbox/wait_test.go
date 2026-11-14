package blackbox

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
	"time"
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

	// The test drives the OSC 133 marks itself, so the wait's mark path is
	// exercised on every machine, not only where the login shell ships real
	// shell integration. The setup line leaves a prompt mark A behind it: a
	// wait that arrives while the mark is A belongs to the next command.
	// Every marker is typed split, as READY""133, so the echoed command line
	// can never satisfy the wait on it: only the command's own output can.
	h.run("write-to", pane, "-s", s, "--msg",
		`printf '\033]133;A\007'; echo READY""133`, "--enter")
	h.waitFor(s, pane, "READY133")

	// The wait parks at the prompt and ends on the command-end mark D the
	// command leaves behind, with the exit code the mark carries. An attach
	// that lands after the command returned ends on the same mark at once.
	// The command exits 7 either way, so both ends answer 7.
	const marker = "WAITMARK-E2E-7"
	type waitRes struct {
		err    error
		stdout string
		stderr string
		code   int
	}
	done := make(chan waitRes, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		wait := exec.Command(h.bin, "wait", pane, "--timeout", "30s", "-s", s)
		wait.Env = h.env
		wait.Stdout = &stdout
		wait.Stderr = &stderr
		err := wait.Run()
		code := 0
		if wait.ProcessState != nil {
			code = wait.ProcessState.ExitCode()
		}
		done <- waitRes{err, stdout.String(), stderr.String(), code}
	}()

	line := `echo WAITGO; sh -c 'sleep 3; echo WAITMARK-""E2E-7; printf "\033]133;D;7\007"; exit 7'`
	h.run("write-to", pane, "-s", s, "--msg", line, "--enter")

	select {
	case r := <-done:
		if r.err != nil {
			if _, ok := r.err.(*exec.ExitError); !ok {
				t.Fatalf("dm wait failed to run: %v\nstdout:\n%s\nstderr:\n%s", r.err, r.stdout, r.stderr)
			}
		}
		if r.code != 7 {
			t.Fatalf("dm wait exited %d, want 7\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
		}
		if !strings.Contains(r.stdout, marker) {
			t.Fatalf("wait stdout = %q, want the marker %q", r.stdout, marker)
		}
		if !strings.Contains(r.stderr, "* done, exit 7") {
			t.Fatalf("wait stderr = %q, want the status line * done, exit 7", r.stderr)
		}
	case <-time.After(35 * time.Second):
		t.Fatal("dm wait never returned")
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

	// The marks are driven by the test, as in TestPaneWaitEndToEnd, and the
	// markers are typed split, so only the command's own output can match.
	h.run("write-to", pane, "-s", s, "--msg",
		`printf '\033]133;A\007'; echo READY""133`, "--enter")
	h.waitFor(s, pane, "READY133")

	const marker = "WAITMARK-ENV-3"
	type waitRes struct {
		err    error
		stdout string
		stderr string
		code   int
	}
	done := make(chan waitRes, 1)
	go func() {
		env := boxEnv(h.env, "DM_SESSION="+s, "DM_PANE="+pane)
		var stdout, stderr bytes.Buffer
		wait := exec.Command(h.bin, "wait", pane, "--timeout", "30s")
		wait.Env = env
		wait.Stdout = &stdout
		wait.Stderr = &stderr
		err := wait.Run()
		code := 0
		if wait.ProcessState != nil {
			code = wait.ProcessState.ExitCode()
		}
		done <- waitRes{err, stdout.String(), stderr.String(), code}
	}()

	line := `echo WAITGO; sh -c 'sleep 3; echo WAITMARK-""ENV-3; printf "\033]133;D;3\007"; exit 3'`
	h.run("write-to", pane, "-s", s, "--msg", line, "--enter")

	select {
	case r := <-done:
		if r.err != nil {
			if _, ok := r.err.(*exec.ExitError); !ok {
				t.Fatalf("dm wait failed to run: %v\nstdout:\n%s\nstderr:\n%s", r.err, r.stdout, r.stderr)
			}
		}
		if r.code != 3 {
			t.Fatalf("dm wait exited %d, want 3\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
		}
		if !strings.Contains(r.stdout, marker) {
			t.Fatalf("wait stdout = %q, want the marker %q", r.stdout, marker)
		}
	case <-time.After(35 * time.Second):
		t.Fatal("dm wait never returned")
	}
}
