package blackbox

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeNotify puts a notify-send on a fresh directory and returns that directory
// and the log it appends its arguments to. The PATH put in front of the real
// one makes the server and every pane see this binary.
func fakeNotify(t *testing.T) (dir, log string) {
	t.Helper()
	dir = t.TempDir()
	log = filepath.Join(dir, "notify.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "notify-send"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, log
}

// notifyHarness builds a harness whose children find the fake notify-send on
// PATH. extra entries are added to the harness environment.
func notifyHarness(t *testing.T, extra ...string) (*harness, string) {
	t.Helper()
	dir, log := fakeNotify(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	h := newHarness(t)
	h.env = append(h.env, extra...)
	return h, log
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

// notifyOSC is the shell command that reports a state through OSC 7501.
func notifyOSC(state, message string) string {
	return `printf '\033]7501;{"state":"` + state + `","message":"` + message + `"}\007'`
}

// writeStatus reports a state from the pane and waits until the server records
// it, so a following assertion is not racing the watch loop.
func writeStatus(t *testing.T, h *harness, session, pane, state, message string) {
	t.Helper()
	h.run("write-to", pane, "-s", session, "--msg", notifyOSC(state, message), "--enter")
	deadline := time.Now().Add(10 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		last = h.run("status", pane, "-s", session)
		if strings.Contains(last, state) && strings.Contains(last, message) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("pane %s never reported %s/%s: %q", pane, state, message, last)
}

// attachClient sends a real attach message, so the server believes an operator
// is watching. It returns once the server answered.
func attachClient(t *testing.T, h *harness, session string) {
	t.Helper()
	sock := filepath.Join(h.stateDir(), "sessions", session, "server.sock")
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := writePaneMsg(c, paneMsg{Type: "attach", Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	answered := make(chan struct{}, 1)
	go func() {
		first := true
		for {
			if _, err := readPaneMsg(c); err != nil {
				return
			}
			if first {
				first = false
				answered <- struct{}{}
			}
		}
	}()
	select {
	case <-answered:
	case <-time.After(10 * time.Second):
		t.Fatal("server never answered the attach")
	}
}

// notifyEntry fails unless the box log holds an entry under key with the
// state and the message, so a test asserts the history the renderers consume,
// not only the popup.
func notifyEntry(t *testing.T, h *harness, session, key, state, msg string) {
	t.Helper()
	res := h.rpc(session, map[string]any{"action": "msg-list", "keyPrefix": "notify/", "limit": 100})
	entries, _ := res["entries"].([]any)
	for _, e := range entries {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if m["key"] == key && m["state"] == state && m["msg"] == msg {
			return
		}
	}
	t.Fatalf("box log = %v, want an entry under %s with state %s and msg %q", entries, key, state, msg)
}

// waitNotifyEntry polls the box log until an entry under key with the state
// and the message lands, bounded. A path with no desktop pop has no log line
// to wait on, so the box itself says when the post went through.
func waitNotifyEntry(t *testing.T, h *harness, session, key, state, msg string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		res := h.rpc(session, map[string]any{"action": "msg-list", "keyPrefix": "notify/", "limit": 100})
		entries, _ := res["entries"].([]any)
		for _, e := range entries {
			m, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if m["key"] == key && m["state"] == state && strings.Contains(m["msg"].(string), msg) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("box log = %v, never held an entry under %s with state %s and msg %q", entries, key, state, msg)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A blocked pane the operator is not looking at lands in the box under its
// notify topic with the entered state, and the headless renderer pops the
// topic, the state and the message.
func TestNotifyBlockedInBackgroundPane(t *testing.T) {
	h, log := notifyHarness(t)
	h.run("new", "-s", "n")
	root := h.focus("n")
	p2 := h.split("n", root, "right")
	h.run("focus", root, "-s", "n")
	h.run("exec", `{"action":"set-pane-name","target":"`+p2+`","name":"pi"}`, "-s", "n")

	writeStatus(t, h, "n", p2, "blocked", "needs review")
	lines := waitNotify(t, log, 1)
	if len(lines) != 1 {
		t.Fatalf("notifications = %q, want one", lines)
	}
	line := lines[0]
	for _, want := range []string{"notify/" + p2 + " blocked", "needs review"} {
		if !strings.Contains(line, want) {
			t.Fatalf("notification %q missing %q", line, want)
		}
	}
	stillNotify(t, log, 1, 400*time.Millisecond)
	notifyEntry(t, h, "n", "notify/"+p2, "blocked", "needs review")
}

// The same state twice is one notification: a second report for a pane already
// blocked does not interrupt again.
func TestNotifyOncePerState(t *testing.T) {
	h, log := notifyHarness(t)
	h.run("new", "-s", "n")
	root := h.focus("n")
	p2 := h.split("n", root, "right")
	h.run("focus", root, "-s", "n")

	writeStatus(t, h, "n", p2, "blocked", "first")
	waitNotify(t, log, 1)
	writeStatus(t, h, "n", p2, "blocked", "second")
	stillNotify(t, log, 1, 400*time.Millisecond)
	lines := notifyLines(t, log)
	if !strings.Contains(lines[0], "first") {
		t.Fatalf("notification %q carries the wrong message", lines[0])
	}
}

// A focused pane with a client attached is the operator's business, so it does
// not notify; the same pane in the background does.
func TestNotifySuppressedForAttachedFocus(t *testing.T) {
	h, log := notifyHarness(t)
	h.run("new", "-s", "n")
	root := h.focus("n")
	p2 := h.split("n", root, "right") // the split takes the focus
	attachClient(t, h, "n")

	writeStatus(t, h, "n", p2, "blocked", "hidden")
	stillNotify(t, log, 0, 400*time.Millisecond)
	notifyEntry(t, h, "n", "notify/"+p2, "blocked", "hidden")

	// The same pane in the background, entering an urgent state, does notify:
	// the silence above was the focus suppression, not urgency.
	h.run("focus", root, "-s", "n")
	writeStatus(t, h, "n", p2, "error", "seen")
	lines := waitNotify(t, log, 1)
	if len(lines) != 1 || !strings.Contains(lines[0], "seen") {
		t.Fatalf("notifications = %q, want one carrying seen", lines)
	}
	notifyEntry(t, h, "n", "notify/"+p2, "error", "seen")
}

// DM_NOTIFY=0 turns the desktop notifications off, and the box log still
// carries the event.
func TestNotifyDisabledByEnv(t *testing.T) {
	h, log := notifyHarness(t, "DM_NOTIFY=0")
	h.run("new", "-s", "n")
	root := h.focus("n")
	p2 := h.split("n", root, "right")
	h.run("focus", root, "-s", "n")

	writeStatus(t, h, "n", p2, "blocked", "needs review")
	stillNotify(t, log, 0, 400*time.Millisecond)
	notifyEntry(t, h, "n", "notify/"+p2, "blocked", "needs review")
}

// A pane exit lands in the box under the pane's notify topic, rides the note
// state with the exit in its message, and pops nothing: note is not urgent,
// whatever the focus. The entry is logged either way.
func TestNotifyPaneExit(t *testing.T) {
	h, log := notifyHarness(t)
	h.run("new", "-s", "n")
	root := h.focus("n")
	p2 := h.split("n", root, "right")
	h.waitPanes("n", 2)
	h.run("focus", root, "-s", "n")

	h.run("write-to", p2, "-s", "n", "--msg", "exit", "--enter")
	waitNotifyEntry(t, h, "n", "notify/"+p2, "note", "exited")
	stillNotify(t, log, 0, 400*time.Millisecond)
	notifyEntry(t, h, "n", "notify/"+p2, "note", "exited")
}

// Urgency is the desktop gate: a done entry in a background pane produces no
// notification, error and blocked still do, and every entry lands in the box
// whatever its urgency.
func TestNotifyUrgencyGatesDesktopPops(t *testing.T) {
	h, log := notifyHarness(t)
	h.run("new", "-s", "n")
	root := h.focus("n")
	p2 := h.split("n", root, "right")
	h.run("focus", root, "-s", "n")

	writeStatus(t, h, "n", p2, "done", "built ok")
	stillNotify(t, log, 0, 400*time.Millisecond)
	waitNotifyEntry(t, h, "n", "notify/"+p2, "done", "built ok")

	writeStatus(t, h, "n", p2, "error", "build failed")
	lines := waitNotify(t, log, 1)
	if !strings.Contains(lines[0], "notify/"+p2+" error") || !strings.Contains(lines[0], "build failed") {
		t.Fatalf("notification %q missing the error pop", lines[0])
	}
	waitNotifyEntry(t, h, "n", "notify/"+p2, "error", "build failed")

	writeStatus(t, h, "n", p2, "blocked", "needs review")
	waitNotify(t, log, 2)
	waitNotifyEntry(t, h, "n", "notify/"+p2, "blocked", "needs review")

	h.run("write-to", p2, "-s", "n", "--msg", "exit", "--enter")
	waitNotifyEntry(t, h, "n", "notify/"+p2, "note", "exited")
	stillNotify(t, log, 2, 400*time.Millisecond)
}

// A last-pane exit rides note too: no desktop notification. The session ends
// with the exit and takes its state file with it, so the entry's landing in
// the box is proven by TestLastPaneExitPostsEntry in internal/server.
func TestNotifyLastPaneExitDoesNotPop(t *testing.T) {
	h, log := notifyHarness(t)
	h.run("new", "-s", "n")
	only := h.focus("n")

	h.run("write-to", only, "-s", "n", "--msg", "exit", "--enter")
	stillNotify(t, log, 0, 1*time.Second)
	if _, err := os.Stat(filepath.Join(h.stateDir(), "sessions", "n", "state.json")); !os.IsNotExist(err) {
		t.Fatalf("the last pane's exit left the session state behind: %v", err)
	}
}
