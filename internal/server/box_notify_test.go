package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dm/internal/box"
)

// fakeNotifySend puts a notify-send on a fresh directory and returns that
// directory and the log the fake appends its arguments to. The PATH put in
// front of the real one makes SystemNotify see this binary.
func fakeNotifySend(t *testing.T) (dir, log string) {
	t.Helper()
	dir = t.TempDir()
	log = filepath.Join(dir, "log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "notify-send"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, log
}

// notifySendLines reads the fake's log. A missing or empty log is no lines.
func notifySendLines(t *testing.T, log string) []string {
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

// waitNotifySend waits until the log holds at least n lines, bounded.
func waitNotifySend(t *testing.T, log string, n int) []string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		lines := notifySendLines(t, log)
		if len(lines) >= n {
			return lines
		}
		if time.Now().After(deadline) {
			t.Fatalf("want %d dispatches, got %q", n, lines)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// noNotifySend fails when the log does not hold exactly want lines after the
// wait, so a missing and a repeated dispatch are both caught.
func noNotifySend(t *testing.T, log string, want int) {
	t.Helper()
	time.Sleep(500 * time.Millisecond)
	if lines := notifySendLines(t, log); len(lines) != want {
		t.Fatalf("dispatches = %q, want %d", lines, want)
	}
}

// postQuiet posts one entry straight through the server and fails the test on
// an error, so the tests drive boxPost without attaching a client.
func postQuiet(t *testing.T, s *server, key, state, msg string) {
	t.Helper()
	_, err := s.boxPost(&box.PostReq{Key: key, State: box.State(state), Msg: msg})
	if err != nil {
		t.Fatalf("post %s: %v", key, err)
	}
}

// A notify/ entry with no attached client pops: the server renders the topic
// and state as the title and the message as the body. The bare prefix names no
// pane and pops too.
func TestBoxNotifyPostDispatches(t *testing.T) {
	dir, log := fakeNotifySend(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DM_NOTIFY", "")
	s := boxServer(t)
	focus := s.st.ActiveWindow().Focus

	postQuiet(t, s, "notify/"+focus, "blocked", "needs review")
	postQuiet(t, s, "notify/", "error", "free post")
	// The two renders race onto the log, so the test matches the pair, not
	// the order.
	lines := waitNotifySend(t, log, 2)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"notify/" + focus + " blocked", "needs review", "notify/ error", "free post",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("dispatches = %q, want %q in them", joined, want)
		}
	}
}

// A notify/ topic that names the pane the operator is looking at pops nothing;
// the same post for a pane nobody focuses still pops. The entry is logged
// either way.
func TestBoxNotifyPostSuppressedWhenFocused(t *testing.T) {
	dir, log := fakeNotifySend(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DM_NOTIFY", "")
	s := boxServer(t)
	focus := s.st.ActiveWindow().Focus
	cl, _ := boxConn(t)
	t.Cleanup(cl.shutdown)
	s.mu.Lock()
	s.clients[cl] = true
	s.mu.Unlock()

	postQuiet(t, s, "notify/"+focus, "blocked", "needs review")
	noNotifySend(t, log, 0)
	postQuiet(t, s, "notify/ghost", "error", "finished")
	waitNotifySend(t, log, 1)

	entries, _, err := s.boxList(&box.ListReq{KeyPrefix: "notify/", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("logged = %d entries, want 2", len(entries))
	}
}

// A normal topic never renders, whatever the attachment.
func TestBoxNotifyPostNormalTopicSilent(t *testing.T) {
	dir, log := fakeNotifySend(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DM_NOTIFY", "")
	s := boxServer(t)

	postQuiet(t, s, "lane/a", "done", "gate green")
	noNotifySend(t, log, 0)
}

// DM_NOTIFY=0 gates the renderer: a notify/ entry logs and never pops.
func TestBoxNotifyPostGatedByEnv(t *testing.T) {
	dir, log := fakeNotifySend(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DM_NOTIFY", "0")
	s := boxServer(t)
	focus := s.st.ActiveWindow().Focus

	postQuiet(t, s, "notify/"+focus, "blocked", "needs review")
	noNotifySend(t, log, 0)
}

// Urgency is the second gate: a done notify/ entry logs and pops nothing even
// for a pane nobody is looking at, while an error on the same topic pops. The
// log is never gated.
func TestBoxNotifyPostGatedByUrgency(t *testing.T) {
	dir, log := fakeNotifySend(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DM_NOTIFY", "")
	s := boxServer(t)

	postQuiet(t, s, "notify/ghost", "done", "built ok")
	noNotifySend(t, log, 0)
	postQuiet(t, s, "notify/ghost", "error", "build failed")
	lines := waitNotifySend(t, log, 1)
	if !strings.Contains(lines[0], "notify/ghost error") || !strings.Contains(lines[0], "build failed") {
		t.Fatalf("dispatch = %q, want the topic, state and msg", lines[0])
	}

	entries, _, err := s.boxList(&box.ListReq{KeyPrefix: "notify/", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("logged = %d entries, want both the done and the error", len(entries))
	}
}
