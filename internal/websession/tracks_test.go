package websession_test

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

// TestEveryTrackPlaysThroughTheRealClient is the tour's proof without a
// browser: the shipped client, the fake session, and the keys each step names,
// pressed one keystroke at a time. A track that names a key the client does not
// act on, a command the shell does not answer, or a step nothing can finish
// stops here.
func TestEveryTrackPlaysThroughTheRealClient(t *testing.T) {
	// The client loads the operator's keymap from disk. Give it a home of its
	// own, so a rebound key cannot decide whether a track is playable, and the
	// run leaves the real config alone.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	for _, track := range lesson.Tracks {
		t.Run(track.ID, func(t *testing.T) { playTrack(t, track) })
	}
}

// playTrack presses every step's keys through the client and waits for the
// lesson to move on. The lesson, not the test, decides what the keys did: an
// action arrives from the client, a command from the pretend shell, and the
// step advances only when its rule is met.
func playTrack(t *testing.T, track lesson.Track) {
	t.Helper()
	size := func() (int, int) { return 100, 30 }
	clientConn, serverConn := net.Pipe()
	l := lesson.Start(track)
	sess := websession.New(size, func(ev map[string]any) {
		// The pretend shell reporting a line it ran, the same callback
		// cmd/dm-wasm hands the lesson.
		if kind, _ := ev["type"].(string); kind == "command" {
			line, _ := ev["line"].(string)
			l.Feed(lesson.Event{Type: "command", Line: line})
		}
	}, serverConn)
	go sess.Serve()

	keys := make(chan []byte, 64)
	winch := make(chan os.Signal, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = client.Attach(client.Harness{
			Conn: clientConn, Out: io.Discard, Keys: keys, Winch: winch,
			Size: size, Session: "track",
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

	for {
		at, step, ok := l.Current()
		if !ok {
			break
		}
		for _, k := range step.Keys {
			select {
			case keys <- []byte(k.Bytes):
			case <-time.After(time.Second):
				t.Fatalf("step %d %q: the client took no key %q", at+1, step.Title, k.Label)
			}
			// One read at a time, as a person presses them, so the prefix
			// arms before the key after it arrives.
			time.Sleep(20 * time.Millisecond)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			now, _, more := l.Current()
			if !more || now > at {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("step %d %q did not finish on its own keys (%v)", at+1, step.Title, step.Keys)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !l.Finished() {
		at, step, _ := l.Current()
		t.Fatalf("the track stopped at step %d %q", at+1, step.Title)
	}
	if got, want := len(l.Results()), len(track.Steps); got != want {
		t.Fatalf("results = %d, want %d", got, want)
	}
}
