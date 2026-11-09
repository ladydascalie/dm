package blackbox

import (
	"os"
	"path/filepath"
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

// TestResetSession proves reset discards a live session: the server and every
// pane process go, and the state directory is removed. Without --force it
// prints the plan and refuses.
func TestResetSession(t *testing.T) {
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
	if out := h.run("ls"); strings.Contains(out, "reset") {
		t.Fatalf("ls still lists the reset session: %q", out)
	}
}

// TestResetAll proves --all discards every session on disk.
func TestResetAll(t *testing.T) {
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
	h := newHarness(t)
	out, err := h.try("reset", "-s", "ghost", "--force")
	if err == nil {
		t.Fatal("reset of a missing session succeeded")
	}
	if !strings.Contains(out, `no session "ghost"`) {
		t.Fatalf("reset of a missing session printed %q", out)
	}
}
