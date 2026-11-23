package pane

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"dm/internal/wire"
)

// sessionFrame renders the OSC 7502 report a program writes:
// session=<b64 path>;id=<id>.
func sessionFrame(file, id string) string {
	return "\x1b]7502;session=" + base64.StdEncoding.EncodeToString([]byte(file)) +
		";id=" + id + "\x1b\\"
}

// TestSessionFramePushed proves the pane decodes a session report out of the
// byte stream, publishes it to its subscribers, and keeps the last value so a
// reader that attaches later draws it from the replay reply.
func TestSessionFramePushed(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 60, 10)
	p.adopted = make(chan struct{})
	sub := subscriber(p)

	const file, id = "/home/me/.pi/sessions/9f2.jsonl", "9f2"
	filtered := feedStatus(t, p, sessionFrame(file, id), 4)
	if filtered != "" {
		t.Fatalf("the session frame leaked into the stream: %q", filtered)
	}

	var got struct {
		SessionFile string `json:"sessionFile"`
		SessionID   string `json:"sessionID"`
	}
	m := waitSessionFrame(t, sub)
	if err := json.Unmarshal(m.Res, &got); err != nil {
		t.Fatal(err)
	}
	if got.SessionFile != file || got.SessionID != id {
		t.Fatalf("broadcast session = %+v, want %q and %q", got, file, id)
	}

	// A repeat of the same report is not a second wake-up.
	feedStatus(t, p, sessionFrame(file, id), 1)
	readNothing(t, sub)

	// The stored value is what a reader that attaches later gets.
	paneSend(t, cli, wire.Msg{Type: "req", Action: "replay", ID: 7})
	reply := waitReplay(t, msgs)
	var extra struct {
		SessionFile string `json:"sessionFile"`
		SessionID   string `json:"sessionID"`
	}
	if err := json.Unmarshal(reply.Res, &extra); err != nil {
		t.Fatal(err)
	}
	if extra.SessionFile != file || extra.SessionID != id {
		t.Fatalf("replay session = %+v, want the kept %q and %q", extra, file, id)
	}
}

// TestSessionBadFrameIgnored proves the pane never stores a frame that names no
// session, so a reader sees nothing invented.
func TestSessionBadFrameIgnored(t *testing.T) {
	t.Parallel()
	p, _, _ := yankPane(t, 60, 10)
	p.adopted = make(chan struct{})
	sub := subscriber(p)

	feedStatus(t, p, "\x1b]7502;session=!!!not-base64!!!;id=9f2\x07", 1)
	readNothing(t, sub)

	p.mu.Lock()
	file, id := p.sessionFile, p.sessionID
	p.mu.Unlock()
	if file != "" || id != "" {
		t.Fatalf("bad frame stored session %q %q", file, id)
	}
}

// waitSessionFrame takes the next session frame from a subscriber.
func waitSessionFrame(t *testing.T, sub chan wire.Msg) wire.Msg {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m, ok := <-sub:
			if !ok {
				t.Fatal("the subscriber closed before the session frame")
			}
			if m.Type == "session" {
				return m
			}
		case <-deadline:
			t.Fatal("no session frame reached the subscriber")
		}
	}
}

// waitReplay takes the next replay frame from the pane socket.
func waitReplay(t *testing.T, msgs chan wire.Msg) wire.Msg {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m, ok := <-msgs:
			if !ok {
				t.Fatal("the socket closed before the replay reply")
			}
			if m.Type == "replay" {
				return m
			}
		case <-deadline:
			t.Fatal("no replay reply")
		}
	}
}
