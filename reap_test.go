package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"testing"
	"time"
)

// reapWindow bounds how long a lost pane may take to stop: the daemon checks
// its socket every 5 seconds.
const reapWindow = 15 * time.Second

// gone reports whether pid has exited. The server never waits for a pane
// daemon, so an exited daemon lingers as a zombie and signal 0 still succeeds;
// the state field in /proc tells a zombie from a live process. Without /proc,
// only a vanished pid counts.
func gone(pid int) bool {
	if pid <= 0 {
		return true
	}
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		if i := bytes.LastIndexByte(b, ')'); i >= 0 && i+2 < len(b) {
			return b[i+2] == 'Z' || b[i+2] == 'X'
		}
	}
	return syscall.Kill(pid, 0) == syscall.ESRCH
}

// shellPID asks a pane's shell for its pid. The daemon must give the shell a
// process group per pane, so the shell pid is also the group id it signals.
func shellPID(t *testing.T, h *harness, session, pane string) int {
	t.Helper()
	pidRE := regexp.MustCompile(`REAP-SHELL=(\d+)`)
	h.run("write-to", pane, "-s", session, "--msg", "echo REAP-SHELL=$$", "--enter")
	// The typed command is echoed too, so only a numbered line counts.
	deadline := time.Now().Add(waitBudget)
	var out string
	for {
		out, _ = h.try("read", pane, "-s", session)
		if m := pidRE.FindStringSubmatch(out); m != nil {
			var pid int
			if _, err := fmt.Sscanf(m[1], "%d", &pid); err != nil {
				t.Fatalf("pane %s shell pid %q: %v", pane, m[1], err)
			}
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane %s did not report its shell pid:\n%s", pane, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func panePID(t *testing.T, h *harness, session, pane string) int {
	t.Helper()
	return readPID(t, filepath.Join(h.stateDir(), "sessions", session, "panes", pane+".sock.pid"))
}

// TestReapOrphans unlinks one pane's socket. That daemon can never be reached
// again, so it and its shell must go, while the pane whose socket is intact
// outlives the reaper pass: an unattached path is the normal state.
func TestReapOrphans(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "reap")
	orphan := h.focus("reap")
	keep := h.split("reap", orphan, "right")

	orphanShell := shellPID(t, h, "reap", orphan)
	keepShell := shellPID(t, h, "reap", keep)
	// keep answered the pid probe, so its daemon is listening and its own
	// reaper timer is running. Its first pass lands within sockEvery of this
	// moment; the check below waits for that pass, not just the orphan's.
	keepUp := time.Now()
	orphanDaemon := panePID(t, h, "reap", orphan)
	keepDaemon := panePID(t, h, "reap", keep)
	if orphanDaemon == keepDaemon || orphanShell == keepShell {
		t.Fatalf("panes share a process: daemons %d/%d shells %d/%d",
			orphanDaemon, keepDaemon, orphanShell, keepShell)
	}

	start := time.Now()
	deadline := start.Add(reapWindow)
	orphanSock := filepath.Join(h.stateDir(), "sessions", "reap", "panes", orphan+".sock")
	if err := os.Remove(orphanSock); err != nil {
		t.Fatalf("unlink pane socket: %v", err)
	}

	for !gone(orphanDaemon) || !gone(orphanShell) {
		if time.Now().After(deadline) {
			t.Fatalf("orphan survived %s without its socket: daemon %d alive=%v, shell %d alive=%v",
				time.Since(start).Round(time.Millisecond), orphanDaemon, !gone(orphanDaemon),
				orphanShell, !gone(orphanShell))
		}
		time.Sleep(100 * time.Millisecond)
	}

	logPath := filepath.Join(h.stateDir(), "sessions", "reap", "panes", orphan+".log")
	if b, err := os.ReadFile(logPath); err != nil {
		t.Fatalf("read pane log: %v", err)
	} else if !regexp.MustCompile(`pane ` + orphan + `: socket .*, stopping`).Match(b) {
		t.Fatalf("pane log does not name the reason: %s", b)
	}

	// The orphan's death only proves the orphan's reaper pass ran. Each pane
	// runs its own timer, so wait for keep's first pass too, then look once.
	if wait := time.Until(keepUp.Add(sockEvery + 300*time.Millisecond)); wait > 0 {
		time.Sleep(wait)
	}
	if gone(keepDaemon) || gone(keepShell) {
		t.Fatalf("live pane reaped with the orphan after %s: daemon %d alive=%v, shell %d alive=%v",
			time.Since(start).Round(time.Millisecond), keepDaemon, !gone(keepDaemon),
			keepShell, !gone(keepShell))
	}
	if out := h.waitFor("reap", keep, "REAP-SHELL="); out == "" {
		t.Fatalf("live pane stopped answering")
	}
}
