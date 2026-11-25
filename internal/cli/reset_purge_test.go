package cli

// The reset and purge maintenance verbs: what they refuse, what they remove,
// and what they leave alone.
//
// Every test here stays serial: reset and purge reach the session tree
// through transport (ResetSession and ListSessions), which resolves DM_STATE
// per call, and t.Setenv cannot run in a parallel test.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"dm/internal/paths"
	"dm/internal/testutil"
)

// TestResetWithoutForcePrintsThePlan proves a bare reset prints what it would
// discard and refuses, because the discard is final.
func TestResetWithoutForcePrintsThePlan(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	var out string
	err := func() error {
		var runErr error
		out = testutil.CaptureStdout(t, func() { runErr = Run([]string{"reset"}) })
		return runErr
	}()
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("dm reset without --force = %v, want the refusal", err)
	}
	if !strings.Contains(out, "would reset main") {
		t.Fatalf("dm reset printed %q, want the plan", out)
	}
}

// TestResetForceMissingSession proves a forced reset over a missing session
// reports the failure instead of pretending it reset.
func TestResetForceMissingSession(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"reset", "--force"}); err == nil || !strings.Contains(err.Error(), "not reset") {
			t.Fatalf("dm reset --force on a missing session = %v, want the failure count", err)
		}
	})
	if !strings.Contains(out, "no session") {
		t.Fatalf("dm reset --force printed %q, want the missing session report", out)
	}
}

// TestResetForceRemovesTheSession proves a forced reset removes the session
// directory and reports the reset.
func TestResetForceRemovesTheSession(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	dir := paths.SessionDir("main")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"reset", "--force"}); err != nil {
			t.Fatalf("dm reset --force: %v", err)
		}
	})
	if !strings.Contains(out, "reset main") {
		t.Fatalf("dm reset --force printed %q", out)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("reset left the session dir behind: %v", err)
	}
}

// TestResetAllWithNothingToReset proves --all over an empty state tree
// reports nothing and succeeds.
func TestResetAllWithNothingToReset(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"reset", "--all"}); err != nil {
			t.Fatalf("dm reset --all: %v", err)
		}
	})
	if strings.TrimSpace(out) != "nothing to reset" {
		t.Fatalf("dm reset --all printed %q", out)
	}
}

// TestPurgeNothing proves an empty state tree reports nothing to purge.
func TestPurgeNothing(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"purge"}); err != nil {
			t.Fatalf("dm purge: %v", err)
		}
	})
	if strings.TrimSpace(out) != "nothing to purge" {
		t.Fatalf("dm purge printed %q", out)
	}
}

// deadSession creates a session directory with no live server, panes or pid
// files: exactly what purge is for.
func deadSession(t *testing.T, name string) string {
	t.Helper()
	dir := paths.SessionDir(name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestPurgeRemovesADeadSession proves purge removes the directory of a
// session whose server and panes are gone.
func TestPurgeRemovesADeadSession(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	dir := deadSession(t, "gone")
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"purge"}); err != nil {
			t.Fatalf("dm purge: %v", err)
		}
	})
	if !strings.Contains(out, "purged gone") {
		t.Fatalf("dm purge printed %q", out)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("purged session dir still exists: %v", err)
	}
}

// TestPurgeDryRunKeepsTheSession proves --dry-run prints the plan and removes
// nothing.
func TestPurgeDryRunKeepsTheSession(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	dir := deadSession(t, "gone")
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"purge", "--dry-run"}); err != nil {
			t.Fatalf("dm purge --dry-run: %v", err)
		}
	})
	if !strings.Contains(out, "would purge gone") {
		t.Fatalf("dm purge --dry-run printed %q", out)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("dry run removed the session dir: %v", err)
	}
}

// TestPurgeSkipsALiveSession proves a session whose socket still answers is
// left alone and reported as nothing to purge.
func TestPurgeSkipsALiveSession(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{}`)) // a live socket, no sessions
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"purge"}); err != nil {
			t.Fatalf("dm purge: %v", err)
		}
	})
	if !strings.Contains(out, "nothing to purge") {
		t.Fatalf("dm purge on a live session printed %q", out)
	}
	if _, err := os.Stat(paths.SessionDir("main")); err != nil {
		t.Fatalf("purge removed a live session dir: %v", err)
	}
}

// TestPurgeSkipsAnAliveServerPid proves a dead-looking session with a live
// server pid file is skipped, not removed.
func TestPurgeSkipsAnAliveServerPid(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	dir := deadSession(t, "stuck")
	if err := os.WriteFile(filepath.Join(dir, "server.pid"),
		[]byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"purge"}); err != nil {
			t.Fatalf("dm purge: %v", err)
		}
	})
	if !strings.Contains(out, "skip stuck: server pid alive") {
		t.Fatalf("dm purge printed %q", out)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("purge removed a skipped session dir: %v", err)
	}
}

// TestPurgeSkipsLivePanes proves a session with a live pane daemon is skipped
// with its own reason.
func TestPurgeSkipsLivePanes(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	dir := deadSession(t, "stuck")
	panes := filepath.Join(dir, "panes")
	if err := os.MkdirAll(panes, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(panes, "w1p1.sock.pid"),
		[]byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"purge"}); err != nil {
			t.Fatalf("dm purge: %v", err)
		}
	})
	if !strings.Contains(out, "skip stuck: live panes") {
		t.Fatalf("dm purge printed %q", out)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("purge removed a skipped session dir: %v", err)
	}
}

// holdSessionLock takes an exclusive flock on a session's server.lock and
// returns the open file that holds it. flock belongs to the open file
// description, so a lock this process takes blocks another attempt in the same
// process: purge meets the holder exactly as it meets another process's.
func holdSessionLock(t *testing.T, dir string) *os.File {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, "server.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		t.Fatalf("take the session lock: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// TestPurgeRefusesAHeldLock proves a purge that cannot prove no server is live
// removes nothing: a session with no pid file and no live process, but a held
// lock, is skipped by both the real run and the dry run.
func TestPurgeRefusesAHeldLock(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	dir := deadSession(t, "held")
	holder := holdSessionLock(t, dir)
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"purge", "--dry-run"}); err != nil {
			t.Fatalf("dm purge --dry-run: %v", err)
		}
	})
	if !strings.Contains(out, "skip held: session lock held") {
		t.Fatalf("dm purge --dry-run printed %q", out)
	}
	out = testutil.CaptureStdout(t, func() {
		if err := Run([]string{"purge"}); err != nil {
			t.Fatalf("dm purge: %v", err)
		}
	})
	if !strings.Contains(out, "skip held: session lock held") {
		t.Fatalf("dm purge printed %q", out)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("purge removed a session whose lock is held: %v", err)
	}
	// The holder exits; the next purge removes the directory once.
	if err := holder.Close(); err != nil {
		t.Fatalf("release the lock: %v", err)
	}
	out = testutil.CaptureStdout(t, func() {
		if err := Run([]string{"purge"}); err != nil {
			t.Fatalf("dm purge: %v", err)
		}
	})
	if !strings.Contains(out, "purged held") {
		t.Fatalf("dm purge after the holder exited printed %q", out)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("purged session dir still exists: %v", err)
	}
}

// TestPurgeErrorKeepsTheSession proves a session purge that fails is reported
// and the directory is left for the next run.
func TestPurgeErrorKeepsTheSession(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	dir := deadSession(t, "gone")
	// A session directory with no read permission cannot be removed by the
	// test user... but the parent can. Remove the parent's write bit instead.
	stateDir := filepath.Dir(dir)
	if err := os.Chmod(stateDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(stateDir, 0o700) })
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"purge"}); err != nil {
			t.Fatalf("dm purge: %v", err)
		}
	})
	if !strings.Contains(out, "error purging gone") {
		t.Fatalf("dm purge printed %q, want the error report", out)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("purge removed a session it could not touch: %v", err)
	}
}
