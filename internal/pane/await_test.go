package pane

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"dm/internal/paths"
	"dm/internal/wire"
)

// The spawn wait. The daemon cannot tell a slow adopter from an absent server
// by time alone, so it tells them apart by the connection: no connection ever
// accepted means the grace starts the shell, and a connection means the shell
// waits for the set that connection carries. These tests pin both edges.

// brokenShell cannot start. Run reports the spawn failure the moment the wait
// ends, which is how an in-process test sees that the shell was reached: the
// daemon starts no pty and leaves no goroutine behind, so nothing exits the
// test binary.
const brokenShell = "/nonexistent-dm-await-shell"

// TestPaneDaemonHelper is the child of runPaneDaemon: it runs the daemon's own
// process loop for the flags it was handed. It skips when there is no work, so
// the suite never runs it twice.
func TestPaneDaemonHelper(t *testing.T) {
	raw := os.Getenv("DM_PANE_DAEMON_ARGS")
	if raw == "" {
		t.Skip("helper: only the daemon's child process runs it")
	}
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		t.Fatalf("DM_PANE_DAEMON_ARGS: %v", err)
	}
	if err := Run(args); err != nil {
		t.Fatalf("run: %v", err)
	}
}

// runPaneDaemon starts one pane daemon in a child process and returns its
// socket. A test that starts a real shell cannot call Run in the test binary:
// the daemon exits its process when the shell ends, and a cleanup that kills
// the shell would take the test binary with it.
func runPaneDaemon(t *testing.T, root string, args ...string) string {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestPaneDaemonHelper$")
	cmd.Env = append(withoutEnv(os.Environ(), "DM_STATE"),
		"DM_STATE="+root, "DM_PANE_DAEMON_ARGS="+string(raw))
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the pane daemon: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		// The daemon's own handler closes the pty, which hangs the shell up.
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		if t.Failed() {
			t.Logf("pane daemon output:\n%s", out.String())
		}
	})
	return paths.PaneSockAt(root, "s1", "p1")
}

// withoutEnv drops one key from an environment, so a child sees only the value
// the test gives it.
func withoutEnv(env []string, key string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, key+"=") {
			continue
		}
		out = append(out, e)
	}
	return out
}

// TestRunWaitsForASetDelayedPastTheGrace proves a live connection holds the
// shell past the grace. The pane's shell echoes a registered value the instant
// it runs and the test holds the set for twice the grace, so a 250ms wait
// would publish the value raw before the set lands. The shell starts only
// after the set, so the read surface draws the redaction and never the value.
func TestRunWaitsForASetDelayedPastTheGrace(t *testing.T) {
	t.Parallel()
	const value = "poc-await-1234"
	root := t.TempDir()
	sock := runPaneDaemon(t, root,
		"--id", "p1", "--session", "s1",
		"--argv", "sh,-c,echo "+value+"; sleep 30")
	c := dialPaneSock(t, sock)
	t.Cleanup(func() { _ = c.Close() })
	msgs := pump(c)

	time.Sleep(2 * spawnGrace)
	body := jsonRaw(map[string]any{"valuesB64": [][]byte{[]byte(value)}})
	reply := paneRequest(t, c, msgs, wire.Msg{Type: "req", Action: "set-secrets", ID: 1, Res: body})
	if !reply.OK {
		t.Fatalf("set-secrets: %s", reply.Error)
	}

	// The shell starts after the set, so its output is scrubbed from the
	// first byte. Wait for the redaction to reach the screen, then prove the
	// value never did.
	redacted := strings.Repeat("*", len(value))
	deadline := time.Now().Add(5 * time.Second)
	var drawn string
	for {
		read := paneRequest(t, c, msgs, wire.Msg{Type: "req", Action: "read", ID: 2})
		drawn = strings.Join(read.Lines, "\n")
		if strings.Contains(drawn, redacted) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the read surface never drew the redaction, drew %q", drawn)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if strings.Contains(drawn, value) {
		t.Fatalf("the shell published the value raw before its set: %q", drawn)
	}
}

// TestRunStartsTheShellWithoutAServer proves the grace still starts the shell
// when nothing has ever dialed: a pane with no server works on its own.
func TestRunStartsTheShellWithoutAServer(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DM_STATE", root)
	t.Setenv("SHELL", brokenShell)
	start := time.Now()
	err := runInProcess(t, "--id", "p1", "--session", "s1")
	if err == nil || !strings.Contains(err.Error(), "start shell") {
		t.Fatalf("Run = %v, want the start shell failure", err)
	}
	if took := time.Since(start); took < spawnGrace {
		t.Fatalf("the shell started after %s, before the grace", took)
	}
}

// TestRunStartsAfterTheConnectionCloses proves a connection that leaves before
// its set is not an adopter: the pane falls back to the grace and starts.
func TestRunStartsAfterTheConnectionCloses(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DM_STATE", root)
	t.Setenv("SHELL", brokenShell)
	errc := make(chan error, 1)
	go func() { errc <- Run([]string{"--id", "p1", "--session", "s1"}) }()
	c := dialPaneSock(t, paths.PaneSockAt(root, "s1", "p1"))
	_ = c.Close()
	select {
	case err := <-errc:
		if err == nil || !strings.Contains(err.Error(), "start shell") {
			t.Fatalf("Run = %v, want the start shell failure", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the pane never started its shell after the connection closed")
	}
}

// TestRunWaitsWhileTheConnectionIsLive proves a live connection holds the
// shell past the grace, because the set is what the shell waits for. Closing
// the connection falls back to the grace, so the wait is not open-ended.
func TestRunWaitsWhileTheConnectionIsLive(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DM_STATE", root)
	t.Setenv("SHELL", brokenShell)
	errc := make(chan error, 1)
	go func() { errc <- Run([]string{"--id", "p1", "--session", "s1"}) }()
	c := dialPaneSock(t, paths.PaneSockAt(root, "s1", "p1"))
	defer func() { _ = c.Close() }()
	select {
	case err := <-errc:
		t.Fatalf("the shell started with a live connection and no set: %v", err)
	case <-time.After(2 * spawnGrace):
	}
	_ = c.Close()
	select {
	case err := <-errc:
		if err == nil || !strings.Contains(err.Error(), "start shell") {
			t.Fatalf("Run = %v, want the start shell failure", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the pane never started its shell after the connection closed")
	}
}

// TestAwaitAdoptBoundsASilentConnection proves the wait for a live connection
// has a deadline: a connection that says nothing and never closes cannot hold
// the shell for ever.
func TestAwaitAdoptBoundsASilentConnection(t *testing.T) {
	t.Parallel()
	p := &paneProc{
		adopted:  make(chan struct{}),
		conned:   make(chan struct{}),
		connDone: make(chan struct{}),
	}
	close(p.conned)
	const deadline = 40 * time.Millisecond
	start := time.Now()
	p.awaitAdopt(deadline)
	took := time.Since(start)
	if took < deadline {
		t.Fatalf("awaitAdopt returned after %s, before the deadline %s", took, deadline)
	}
	if took > 2*time.Second {
		t.Fatalf("awaitAdopt took %s, want about the deadline", took)
	}
}

// TestAwaitAdoptResolvesOnTheSet proves a delayed set ends the wait at once,
// past the grace, and that the grace alone never ends it while the connection
// is up.
func TestAwaitAdoptResolvesOnTheSet(t *testing.T) {
	t.Parallel()
	p := &paneProc{
		adopted:  make(chan struct{}),
		conned:   make(chan struct{}),
		connDone: make(chan struct{}),
	}
	close(p.conned)
	done := make(chan struct{})
	go func() {
		p.awaitAdopt(10 * time.Second)
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("the wait ended without a set")
	case <-time.After(2 * spawnGrace):
	}
	close(p.adopted)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the set did not end the wait")
	}
}

// runInProcess runs one daemon in this process and returns its error.
func runInProcess(t *testing.T, args ...string) error {
	t.Helper()
	errc := make(chan error, 1)
	go func() { errc <- Run(args) }()
	select {
	case err := <-errc:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("the pane never reached its shell")
		return nil
	}
}
