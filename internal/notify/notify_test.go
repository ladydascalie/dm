package notify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeNotify puts a notify-send on a fresh directory and returns that directory
// and the log it appends its arguments to.
func fakeNotify(t *testing.T) (dir, log string) {
	t.Helper()
	dir = t.TempDir()
	log = filepath.Join(dir, "log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "notify-send"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, log
}

// notifyLines reads the fake's log. A missing or empty log is no lines.
func notifyLines(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	body := strings.TrimSpace(string(b))
	if body == "" {
		return nil
	}
	return strings.Split(body, "\n")
}

// waitNotify waits for at least n lines, bounded.
func waitNotify(t *testing.T, log string, n int) []string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		lines := notifyLines(t, log)
		if len(lines) >= n {
			return lines
		}
		if time.Now().After(deadline) {
			t.Fatalf("want %d notifications, got %q", n, lines)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// stillNotify fails when the log does not hold exactly want lines after the
// wait, so both a missing and a repeated notification are caught.
func stillNotify(t *testing.T, log string, want int, wait time.Duration) {
	t.Helper()
	time.Sleep(wait)
	if lines := notifyLines(t, log); len(lines) != want {
		t.Fatalf("notifications = %q, want %d", lines, want)
	}
}

// A transition notifies only when the new state is worth interrupting for,
// differs from the old one, and, for done, carries something to say.
func TestNotifyStateChange(t *testing.T) {
	for _, c := range []struct {
		from, to string
		message  string
		want     bool
	}{
		{"", "blocked", "", true},
		{"idle", "blocked", "needs review", true},
		{"working", "done", "built ok", true},
		{"working", "error", "wedge", true},
		{"working", "done", "", false},
		{"working", "done", "  \n\t ", false},
		{"blocked", "blocked", "again", false},
		{"done", "done", "again", false},
		{"blocked", "working", "", false},
		{"idle", "clear", "", false},
	} {
		if got := StateChange(c.from, c.to, c.message); got != c.want {
			t.Fatalf("StateChange(%q, %q, %q) = %v, want %v", c.from, c.to, c.message, got, c.want)
		}
	}
}

// Urgency is a state property, independent of the transition: blocked and
// error are urgent, done, note, working and unknown are not.
func TestNotifyUrgent(t *testing.T) {
	for _, c := range []struct {
		state string
		want  bool
	}{
		{"blocked", true},
		{"error", true},
		{"done", false},
		{"note", false},
		{"working", false},
		{"clear", false},
		{"", false},
	} {
		if got := Urgent(c.state); got != c.want {
			t.Fatalf("Urgent(%q) = %v, want %v", c.state, got, c.want)
		}
	}
}

// Send runs the configured binary with the pinned arguments, and every test
// here owns its own fake and log, so the suite runs in parallel.
func TestSendRunsNotifySend(t *testing.T) {
	t.Parallel()
	dir, log := fakeNotify(t)
	Send(Config{Enabled: true, BinPath: filepath.Join(dir, "notify-send")},
		"dm main w1p1 pi blocked", "needs review")
	lines := waitNotify(t, log, 1)
	line := lines[0]
	for _, want := range []string{"-a dm", "-t 10000", "-u normal",
		"dm main w1p1 pi blocked", "needs review"} {
		if !strings.Contains(line, want) {
			t.Fatalf("notify-send got %q, want %q in it", line, want)
		}
	}
}

// An off switch or an empty binary path is a silent no-op: nothing runs,
// nothing breaks.
func TestSendDisabled(t *testing.T) {
	t.Parallel()
	_, log := fakeNotify(t)
	Send(Config{}, "dm main w1p1 blocked", "needs review")
	Send(Config{Enabled: true}, "dm main w1p1 blocked", "needs review")
	stillNotify(t, log, 0, 400*time.Millisecond)
}

// A binary path that does not exist is a silent no-op: nothing runs, nothing
// breaks.
func TestSendWithoutBinary(t *testing.T) {
	t.Parallel()
	_, log := fakeNotify(t)
	Send(Config{Enabled: true, BinPath: filepath.Join(t.TempDir(), "notify-send")},
		"dm main w1p1 blocked", "needs review")
	stillNotify(t, log, 0, 400*time.Millisecond)
}

// TestSendTagsTheNotification proves the suite's marker reaches both the title
// and the body. A popup a test scenario triggers is the one surface the suite
// cannot take back, so it has to name the suite it came from.
func TestSendTagsTheNotification(t *testing.T) {
	// Serial: the marker is read from the environment, which t.Setenv owns.
	t.Setenv(EnvTag, "TESTSUITE")
	dir, log := fakeNotify(t)
	Send(Config{Enabled: true, BinPath: filepath.Join(dir, "notify-send")},
		"notify/w1p2 error", "pane w1p2 stopped taking server pushes")
	line := waitNotify(t, log, 1)[0]
	for _, want := range []string{"TESTSUITE notify/w1p2 error", "TESTSUITE: pane w1p2 stopped taking server pushes"} {
		if !strings.Contains(line, want) {
			t.Fatalf("notification = %q, want %q in it", line, want)
		}
	}
}

// Tagged is the whole prefixing rule: an empty marker leaves a title and a
// body exactly as the caller wrote them.
func TestTagged(t *testing.T) {
	t.Setenv(EnvTag, "")
	if title, body := Tagged("t", "b"); title != "t" || body != "b" {
		t.Fatalf("Tagged with no marker = %q %q, want the inputs", title, body)
	}
	t.Setenv(EnvTag, "TESTSUITE")
	if title, body := Tagged("t", "b"); title != "TESTSUITE t" || body != "TESTSUITE: b" {
		t.Fatalf("Tagged = %q %q, want the marker in front", title, body)
	}
	if title, body := Tagged("t", ""); title != "TESTSUITE t" || body != "" {
		t.Fatalf("Tagged without a body = %q %q, want an untagged empty body", title, body)
	}
}

// SystemNotify runs notify-send from PATH when DM_NOTIFY is not "0", and
// DM_NOTIFY=0 stops it. These tests keep t.Setenv and stay serial: t.Setenv
// forbids t.Parallel, and the env fallback behaviour of the exported wrapper
// cannot be exercised with explicit configuration.
func TestSystemNotifyEnvFallback(t *testing.T) {
	dir, log := fakeNotify(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DM_NOTIFY", "")

	SystemNotify("dm main w1p1 pi blocked", "needs review")
	lines := waitNotify(t, log, 1)
	line := lines[0]
	for _, want := range []string{"-a dm", "-t 10000", "-u normal",
		"dm main w1p1 pi blocked", "needs review"} {
		if !strings.Contains(line, want) {
			t.Fatalf("notify-send got %q, want %q in it", line, want)
		}
	}

	t.Setenv("DM_NOTIFY", "0")
	SystemNotify("dm main w1p1 pi done", "finished")
	stillNotify(t, log, 1, 400*time.Millisecond)
}

// A PATH without notify-send is a silent no-op for the env-defaults wrapper:
// nothing runs, nothing breaks. Same serial t.Setenv constraint as above.
func TestSystemNotifyEnvWithoutBinary(t *testing.T) {
	_, log := fakeNotify(t)
	t.Setenv("PATH", t.TempDir()) // a PATH that does not hold the fake
	t.Setenv("DM_NOTIFY", "")

	SystemNotify("dm main w1p1 blocked", "needs review")
	stillNotify(t, log, 0, 400*time.Millisecond)
}
