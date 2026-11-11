package blackbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// waitDead waits for a pid to stop answering signal 0.
func waitDead(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid %d is still alive", pid)
}

// paneShellPIDs returns the shells a pane daemon owns, read from procfs.
func paneShellPIDs(t *testing.T, panePID int) []int {
	t.Helper()
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", panePID, panePID))
	if err != nil {
		t.Fatalf("children of %d: %v", panePID, err)
	}
	var out []int
	for _, f := range strings.Fields(string(b)) {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("child pid %q: %v", f, err)
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		t.Fatalf("pane %d has no shell child", panePID)
	}
	return out
}

// TestResetSession proves reset discards a live session: the server, every
// pane daemon and every shell go, and the state directory is removed. Without
// --force it prints the plan and refuses.
func TestResetSession(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "reset")
	root := h.focus("reset")
	p2 := h.split("reset", root, "right")

	dir := filepath.Join(h.stateDir(), "sessions", "reset")
	serverPID := readPID(t, filepath.Join(dir, "server.pid"))
	panePIDs := []int{
		readPID(t, filepath.Join(dir, "panes", root+".sock.pid")),
		readPID(t, filepath.Join(dir, "panes", p2+".sock.pid")),
	}
	var shellPIDs []int
	for _, pid := range panePIDs {
		shellPIDs = append(shellPIDs, paneShellPIDs(t, pid)...)
	}

	out, err := h.try("reset", "-s", "reset")
	if err == nil {
		t.Fatal("reset without --force succeeded")
	}
	if !strings.Contains(out, "would reset reset") {
		t.Fatalf("reset without --force printed %q", out)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("refused reset removed the session: %v", err)
	}

	out = h.run("reset", "-s", "reset", "--force")
	if !strings.Contains(out, "reset reset") {
		t.Fatalf("reset printed %q", out)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("session directory survived reset: %v", err)
	}
	waitDead(t, serverPID)
	for _, pid := range panePIDs {
		waitDead(t, pid)
	}
	for _, pid := range shellPIDs {
		waitDead(t, pid)
	}
	if out := h.run("ls"); strings.Contains(out, "reset") {
		t.Fatalf("ls still lists the reset session: %q", out)
	}
}

// TestResetManyPanesIsFast proves reset does not wait on each pane in turn.
// The old path gave every pane a six second grace period, so six panes took
// half a minute.
func TestResetManyPanesIsFast(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "many")
	root := h.focus("many")
	for i := 0; i < 5; i++ {
		h.split("many", root, "right")
	}
	start := time.Now()
	h.run("reset", "-s", "many", "--force")
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("reset of six panes took %s", d)
	}
}

// TestResetAll proves --all discards every session on disk.
func TestResetAll(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "one")
	h.run("new", "-s", "two")

	out := h.run("reset", "--all", "--force")
	for _, name := range []string{"one", "two"} {
		if !strings.Contains(out, "reset "+name) {
			t.Fatalf("reset --all printed %q, want %q", out, "reset "+name)
		}
		if _, err := os.Stat(filepath.Join(h.stateDir(), "sessions", name)); !os.IsNotExist(err) {
			t.Fatalf("session %s survived reset: %v", name, err)
		}
	}
	if out := h.run("ls"); !strings.Contains(out, "no sessions") {
		t.Fatalf("ls after reset --all = %q", out)
	}
}

// TestResetMissingSession proves reset names a session that is not there.
func TestResetMissingSession(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	out, err := h.try("reset", "-s", "ghost", "--force")
	if err == nil {
		t.Fatal("reset of a missing session succeeded")
	}
	if !strings.Contains(out, `no session "ghost"`) {
		t.Fatalf("reset of a missing session printed %q", out)
	}
}
