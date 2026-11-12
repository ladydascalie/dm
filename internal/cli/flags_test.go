package cli

// The flag plumbing: reorder, the box cursor and sender identity, and the
// pid checks purge uses.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestReorderMovesFlagsBeforePositionals proves -s may follow a pane id: the
// reordered argv puts the flags first, so flag.Parse sees them.
func TestReorderMovesFlagsBeforePositionals(t *testing.T) {
	t.Parallel()
	got := reorder([]string{"w1p1", "-s", "other", "hello"})
	want := []string{"-s", "other", "w1p1", "hello"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("reorder = %v, want %v", got, want)
	}
}

// TestReorderKeepsAValuelessFlag proves a boolean flag stays alone and does
// not swallow the next argument.
func TestReorderKeepsAValuelessFlag(t *testing.T) {
	t.Parallel()
	got := reorder([]string{"--enter", "w1p1"})
	want := []string{"--enter", "w1p1"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("reorder = %v, want %v", got, want)
	}
}

// TestReorderKeepsADash proves a bare "-" stays positional: it is stdin,
// never a flag.
func TestReorderKeepsADash(t *testing.T) {
	t.Parallel()
	got := reorder([]string{"-", "w1p1"})
	want := []string{"-", "w1p1"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("reorder = %v, want %v", got, want)
	}
}

// TestSessionDefaultsToThePaneEnv proves a command from inside a pane targets
// that pane's session without -s, and stays main outside dm.
func TestSessionDefaultsToThePaneEnv(t *testing.T) {
	t.Setenv("DM_SESSION", "waiter")
	if got := newFlags("wait").session; got != "waiter" {
		t.Fatalf("session with DM_SESSION set = %q, want waiter", got)
	}
	os.Unsetenv("DM_SESSION")
	if got := newFlags("wait").session; got != "main" {
		t.Fatalf("session without DM_SESSION = %q, want main", got)
	}
}

// TestSessionFlagBeatsThePaneEnv proves -s still selects another session.
func TestSessionFlagBeatsThePaneEnv(t *testing.T) {
	t.Setenv("DM_SESSION", "waiter")
	c := newFlags("wait")
	c.parse([]string{"-s", "other"})
	if got := c.session; got != "other" {
		t.Fatalf("session with -s other = %q, want other", got)
	}
}

// TestBoxCursor proves -after turns into the request cursor, and that the
// default -1 sends no cursor at all.
func TestBoxCursor(t *testing.T) {
	t.Parallel()
	c := newFlags("msg")
	c.parse([]string{"wait", "--after", "7"})
	if got := c.boxCursor(); got == nil || *got != 7 {
		t.Fatalf("boxCursor = %v, want 7", got)
	}
	c = newFlags("msg")
	c.parse([]string{"wait"})
	if got := c.boxCursor(); got != nil {
		t.Fatalf("boxCursor = %v, want nil", got)
	}
}

// TestBoxFromNeedsTheSessionEnv proves the sender id only travels when the
// shell exports the session it is writing to.
// Stays serial: the test's subject is env behaviour, and t.Setenv cannot run
// in a parallel test.
func TestBoxFromNeedsTheSessionEnv(t *testing.T) {
	t.Setenv("DM_SESSION", "main")
	t.Setenv("DM_PANE", "w1p1")
	if got := boxFrom("main"); got != "w1p1" {
		t.Fatalf("boxFrom(main) = %q, want w1p1", got)
	}
	if got := boxFrom("other"); got != "" {
		t.Fatalf("boxFrom(other) = %q, want empty", got)
	}
}

// TestBoxTermWidthUsesColumns proves $COLUMNS caps the row width when stdout
// is not a terminal.
// Stays serial: the test's subject is env behaviour, and t.Setenv cannot run
// in a parallel test.
func TestBoxTermWidthUsesColumns(t *testing.T) {
	t.Setenv("COLUMNS", "80")
	if w := boxTermWidth(); w != 80 {
		t.Fatalf("boxTermWidth = %d, want 80", w)
	}
	t.Setenv("COLUMNS", "0")
	if w := boxTermWidth(); w != 0 {
		t.Fatalf("boxTermWidth = %d, want 0 for an unparsable COLUMNS", w)
	}
}

// TestPidAliveMissingAndGarbage proves a pid file that names nothing is not
// alive, so purge treats it as a dead session.
func TestPidAliveMissingAndGarbage(t *testing.T) {
	t.Parallel()
	if pidAlive(filepath.Join(t.TempDir(), "gone")) {
		t.Fatal("a missing pid file was alive")
	}
	garbage := filepath.Join(t.TempDir(), "server.pid")
	if err := os.WriteFile(garbage, []byte("not a pid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if pidAlive(garbage) {
		t.Fatal("a garbage pid file was alive")
	}
	zero := filepath.Join(t.TempDir(), "zero.pid")
	if err := os.WriteFile(zero, []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}
	if pidAlive(zero) {
		t.Fatal("pid 0 was alive")
	}
}

// TestPidAliveSelf proves a pid file naming a live process is alive, so purge
// skips the session.
func TestPidAliveSelf(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "server.pid")
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if !pidAlive(path) {
		t.Fatal("the test process was not alive")
	}
}

// TestPidAliveAny proves the pane scan looks only at pid files and stops at
// the first live one.
func TestPidAliveAny(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if pidAliveAny(dir) {
		t.Fatal("an empty pane directory was alive")
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if pidAliveAny(dir) {
		t.Fatal("a non-pid file counted as a pane")
	}
	if err := os.WriteFile(filepath.Join(dir, "w1p1.sock.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if !pidAliveAny(dir) {
		t.Fatal("a live pane pid file was not seen")
	}
}
