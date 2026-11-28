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

// TestRestartLandsAnUpgrade is the flow the operator asked for: dm upgrade, dm
// restart, and the session runs the new build with no detach. The install step
// of dm upgrade is a rename over the running binary, so the proofs read the
// inode each process is really executing instead of trusting the word.
func TestRestartLandsAnUpgrade(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "ru")
	root := h.focus("ru")

	a := startAttach(t, h, "ru")
	a.waitContains(t, firstPaint)
	h.run("write-to", root, "-s", "ru", "--msg", "echo BEFORE-UPGRADE", "--enter")
	a.waitContains(t, "BEFORE-UPGRADE")

	serverBefore := readPIDFile(t, h, "ru", "server.pid")
	paneBefore := readPIDFile(t, h, "ru", filepath.Join("panes", root+".sock.pid"))
	clientPID := a.cmd.Process.Pid

	// What dm upgrade does to the install: the new file takes the path the
	// running processes were started from. Their /proc entries keep the old
	// inode, which is exactly the state the restart has to climb out of.
	stamped := buildStamped(t, "9.9.9")
	if err := os.Rename(stamped, h.bin); err != nil {
		t.Fatalf("install the new binary: %v", err)
	}
	if sameFile(t, fmt.Sprintf("/proc/%d/exe", clientPID), h.bin) {
		t.Fatal("the running client already executes the new file: nothing to prove")
	}

	a.write(prefixKey + "R")
	waitPIDChange(t, h, "ru", "server.pid", serverBefore)
	if got := a.count(altScreenEnter); got != 1 {
		t.Fatalf("the alternate screen was entered %d times, want 1: the restart left the screen and came back", got)
	}

	// The client is the same process, and it now executes the installed file.
	if pid := a.cmd.Process.Pid; pid != clientPID {
		t.Fatalf("client pid = %d, want %d: a restart must exec, not respawn", pid, clientPID)
	}
	deadline := time.Now().Add(15 * time.Second)
	for !sameFile(t, fmt.Sprintf("/proc/%d/exe", clientPID), h.bin) {
		if time.Now().After(deadline) {
			t.Fatal("the client still executes the old binary after the restart")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// The successor server came from the same install, so both sides moved.
	serverPID := readPIDFile(t, h, "ru", "server.pid")
	if !sameFile(t, fmt.Sprintf("/proc/%d/exe", serverPID), h.bin) {
		t.Fatal("the server the restart started executes an older binary than the install")
	}
	// The panes are untouched, and the session still works through them.
	if got := readPIDFile(t, h, "ru", filepath.Join("panes", root+".sock.pid")); got != paneBefore {
		t.Fatalf("pane daemon pid = %d, want %d: an upgrade must not respawn a pane", got, paneBefore)
	}
	h.run("write-to", root, "-s", "ru", "--msg", "echo AFTER-UPGRADE", "--enter")
	a.waitContains(t, "AFTER-UPGRADE")

	a.write(prefixKey + "d")
	a.waitExit(t, 15*time.Second)

	// The restarted client exits cleanly: it inherited the alternate screen, so
	// its own exit has to be the one that gives it back, once.
	if a.readErr != nil && !strings.Contains(a.readErr.Error(), "input/output error") {
		t.Fatalf("the client left the pty with %v", a.readErr)
	}
	if got := a.count(altScreenLeave); got != 1 {
		t.Fatalf("the alternate screen was left %d times, want 1: the session did not hand the screen back once", got)
	}
}

// sameFile reports whether two paths reach the same inode. A path through
// /proc/<pid>/exe resolves to the file that process is executing.
func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		t.Fatalf("stat %s: %v", b, err)
	}
	return os.SameFile(fa, fb)
}

// altScreenEnter and altScreenLeave are the sequences that open and close the
// alternate screen. The count of each is how these tests tell a handover from a
// detach: a seamless restart writes the enter sequence once and the leave
// sequence once, at the very end.
const (
	altScreenEnter = "\x1b[?1049h"
	altScreenLeave = "\x1b[?1049l"
)
