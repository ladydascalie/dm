package blackbox

// The restart surface end to end: a real server, real pane daemons and a real
// attach client. A restart stops the server, starts the one that follows, and
// leaves every pane where it was. Neither verb kills a pane, and the key
// version keeps the client on screen through the whole move.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// readPIDFile reads one pid file under a session directory. A missing file or
// a body that is not a number fails the test: the restart proofs read what the
// processes wrote, not what the CLI claims.
func readPIDFile(t *testing.T, h *harness, session, rel string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.stateDir(), "sessions", session, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	var pid int
	if _, err := fmt.Sscanf(string(b), "%d", &pid); err != nil {
		t.Fatalf("%s holds %q, not a pid: %v", rel, b, err)
	}
	return pid
}

// waitPIDChange waits for a pid file to name a process other than before, and
// returns the new pid.
func waitPIDChange(t *testing.T, h *harness, session, rel string, before int) int {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(filepath.Join(h.stateDir(), "sessions", session, rel))
		if err == nil {
			var pid int
			if _, err := fmt.Sscanf(string(b), "%d", &pid); err == nil && pid != 0 && pid != before {
				return pid
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s never named a new pid after the restart (was %d)", rel, before)
	return 0
}

// TestRestartVerbKeepsPanes proves dm restart is not dm reset: the server pid
// changes, every pane daemon keeps its own pid and therefore its screen, and
// the layout is the one that was there.
func TestRestartVerbKeepsPanes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "rv")
	root := h.focus("rv")
	h.run("write-to", root, "-s", "rv", "--msg", "echo BEFORE-RESTART", "--enter")
	h.waitFor("rv", root, "BEFORE-RESTART")

	serverBefore := readPIDFile(t, h, "rv", "server.pid")
	paneBefore := readPIDFile(t, h, "rv", filepath.Join("panes", root+".sock.pid"))

	h.run("restart", "-s", "rv")

	if serverAfter := waitPIDChange(t, h, "rv", "server.pid", serverBefore); serverAfter == 0 {
		t.Fatal("the restart left no server")
	}
	if got := readPIDFile(t, h, "rv", filepath.Join("panes", root+".sock.pid")); got != paneBefore {
		t.Fatalf("pane daemon pid = %d, want %d: a restart must never respawn a pane", got, paneBefore)
	}
	if ids := h.panes("rv"); len(ids) != 1 || ids[0] != root {
		t.Fatalf("panes after restart = %v, want [%s]", ids, root)
	}
	// The screen lives in the pane daemon, so the successor adopts it whole.
	h.waitFor("rv", root, "BEFORE-RESTART")
}

// TestRestartKeyKeepsTheClientAttached proves the keybinding is not a detach:
// the client leaves the screen and comes straight back, the server pid changes
// under it, and the keys still reach the same pane daemon afterwards.
func TestRestartKeyKeepsTheClientAttached(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "rk")
	root := h.focus("rk")

	a := startAttach(t, h, "rk")
	a.waitContains(t, firstPaint)

	h.run("write-to", root, "-s", "rk", "--msg", "echo BEFORE-RESTART", "--enter")
	a.waitContains(t, "BEFORE-RESTART")

	serverBefore := readPIDFile(t, h, "rk", "server.pid")
	paneBefore := readPIDFile(t, h, "rk", filepath.Join("panes", root+".sock.pid"))

	queriesBefore := a.count(termColourQuery)

	a.write(prefixKey + "R")
	waitPIDChange(t, h, "rk", "server.pid", serverBefore)

	// The same client process is still on the pty: an exec keeps the pid and
	// the terminal. Output written after the restart proves the client
	// reattached to the successor, and the pane daemon is the one that was
	// there before.
	if got := a.count(termColourQuery); got <= queriesBefore {
		t.Fatalf("the terminal query went out %d times, want more than %d: the client did not start again", got, queriesBefore)
	}
	h.run("write-to", root, "-s", "rk", "--msg", "echo AFTER-RESTART", "--enter")
	a.waitContains(t, "AFTER-RESTART")
	if got := readPIDFile(t, h, "rk", filepath.Join("panes", root+".sock.pid")); got != paneBefore {
		t.Fatalf("pane daemon pid = %d, want %d: the restart respawned the pane", got, paneBefore)
	}
	if cmd := a.cmd.Process; cmd == nil {
		t.Fatal("the attach client has no process")
	}

	a.write(prefixKey + "d")
	a.waitExit(t, 15*time.Second)
}

// termColourQuery is what every client start asks the outer terminal, before
// it enters the alternate screen. A second one is the proof that the client
// went back through Run, which is what an exec does.
const termColourQuery = "\x1b]10;?\x1b\\"

// count reports how many times the client has written needle so far.
func (a *attachTerm) count(needle string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.Count(a.out.String(), needle)
}
