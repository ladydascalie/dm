package main

import (
	"io"
	"net"
	"os"
	"testing"
	"time"

	"dm/internal/client"
	"dm/internal/lesson"
	"dm/internal/websession"
)

// TestFastForwardKeysReplayTheSteps is the resume's proof without a browser:
// the keys a fast forward presses for n steps, pressed through the real client
// into the fake session, leave the lesson exactly n steps in. The fake session is
// deterministic, so a reload on step n ends on step n with the session in the
// shape steps 1 to n-1 built.
func TestFastForwardKeysReplayTheSteps(t *testing.T) {
	// The client loads the operator's keymap from disk. Give it a home of its
	// own, so a rebound key cannot decide whether a resume works.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)

	for _, tr := range lesson.Tracks {
		for n := 1; n < len(tr.Steps); n++ {
			t.Run(tr.ID+"/step "+itoa(n), func(t *testing.T) {
				if got := replayPrefix(t, tr, n); got != n {
					t.Fatalf("the keys for %d steps left the lesson at step %d, want %d", n, got, n)
				}
			})
		}
	}
}

// replayPrefix presses the keys a fast forward presses for n steps and reports
// the step the lesson ended on. It waits a moment after the lesson reaches n, so
// a key that drove it past the saved step is caught rather than hidden.
func replayPrefix(t *testing.T, tr lesson.Track, n int) int {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	size := func() (int, int) { return 100, 30 }
	l := lesson.Start(tr)
	sess := websession.New(size, func(ev map[string]any) {
		// The pretend shell reporting a line it ran. A session event carries no
		// step and is not fed, as cmd/dm-wasm does.
		if kind, _ := ev["type"].(string); kind != "command" {
			return
		}
		line, _ := ev["line"].(string)
		l.Feed(lesson.Event{Type: "command", Line: line})
	}, serverConn)
	go sess.Serve()

	keys := make(chan []byte, 64)
	winch := make(chan os.Signal, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = client.Attach(client.Harness{
			Conn: clientConn, Out: io.Discard, Keys: keys, Winch: winch,
			Size: size, Session: "resume",
			OnEvent: func(e client.Event) {
				l.Feed(lesson.Event{Type: e.Type, Name: e.Name, Panes: e.Panes})
			},
		})
	}()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
		<-done
	})

	for _, k := range fastForwardKeys(tr, n) {
		select {
		case keys <- []byte(k):
		case <-time.After(time.Second):
			t.Fatalf("the client took no key %q", k)
		}
		// One read at a time, as the fast forward presses them, so the prefix
		// arms before the key after it arrives.
		time.Sleep(20 * time.Millisecond)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if at, _, _ := l.Current(); at >= n {
			break
		}
		if time.Now().After(deadline) {
			at, step, _ := l.Current()
			t.Fatalf("the keys for %d steps stopped at step %d %q", n, at, step.Title)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Give an extra key time to overshoot before the step is believed.
	time.Sleep(300 * time.Millisecond)
	at, _, _ := l.Current()
	return at
}

// itoa is strconv.Itoa without importing strconv for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [4]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
