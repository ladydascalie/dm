package pane

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"dm/internal/vt"
	"dm/internal/wire"
)

// titleOf reads the title out of the reply a read or replay carries.
func titleOf(t *testing.T, action string, m wire.Msg) string {
	t.Helper()
	var res struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(m.Res, &res); err != nil {
		t.Fatalf("%s reply: %v", action, err)
	}
	return res.Title
}

// TestSampleTitleRedactsALiveValue proves the title broadcast, the one the
// server's snapshot and the desk rail draw, carries the redaction and no part
// of the value. The pane holds the value through the same set the server
// pushes, so the rule under test is the pane's own.
func TestSampleTitleRedactsALiveValue(t *testing.T) {
	t.Parallel()
	const value = "poc-title-9f2c-secret"
	p := &paneProc{vt: vt.NewTerm(40, 5), ring: NewRing(ringDefault), subs: map[*subConn]bool{}}
	msgs := subscriber(p)

	p.setSecrets([]string{value})
	p.vt.Write([]byte("\x1b]2;vim " + value + "\x07"))
	p.vt.Flush()
	p.sampleTitle()

	m := readBroadcast(t, msgs)
	if m.Type != "title" {
		t.Fatalf("broadcast = %+v, want a title", m)
	}
	var res struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(m.Res, &res); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Title, value) {
		t.Fatalf("the title broadcast carries the value: %q", res.Title)
	}
	if want := "vim " + strings.Repeat("*", len(value)); res.Title != want {
		t.Fatalf("the title broadcast = %q, want %q", res.Title, want)
	}
}

// TestSampleTitleLeavesAShortValueAlone proves the title rule is the pane's
// rule: a value under MinSecretLen is never registered, so the title arrives
// exactly as the program set it.
func TestSampleTitleLeavesAShortValueAlone(t *testing.T) {
	t.Parallel()
	const value = "poc"
	if len(value) >= MinSecretLen {
		t.Fatalf("the fixture value is %d bytes, want under %d", len(value), MinSecretLen)
	}
	p := &paneProc{vt: vt.NewTerm(40, 5), ring: NewRing(ringDefault), subs: map[*subConn]bool{}}
	msgs := subscriber(p)

	p.setSecrets([]string{value})
	p.vt.Write([]byte("\x1b]2;at " + value + "\x07"))
	p.vt.Flush()
	p.sampleTitle()

	m := readBroadcast(t, msgs)
	var res struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(m.Res, &res); err != nil {
		t.Fatal(err)
	}
	if want := "at " + value; res.Title != want {
		t.Fatalf("the title broadcast = %q, want %q untouched", res.Title, want)
	}
}

// TestReadAndReplayTitlesAreScrubbed proves the two replies a client attaches
// with carry the same redaction as the broadcast: the server folds a replay's
// title into its pane state, so a raw one there would reach a snapshot.
func TestReadAndReplayTitlesAreScrubbed(t *testing.T) {
	t.Parallel()
	const value = "poc-reply-9f2c-secret"
	p, cli, msgs := yankPane(t, 40, 5)

	p.setSecrets([]string{value})
	p.mu.Lock()
	p.vt.Write([]byte("\x1b]2;build " + value + "\x07"))
	p.vt.Flush()
	p.mu.Unlock()

	want := "build " + strings.Repeat("*", len(value))
	for i, action := range []string{"read", "replay"} {
		paneSend(t, cli, wire.Msg{Type: "req", Action: action, ID: i + 1})
		reply := replyFrame(t, msgs, action, i+1)
		if got := titleOf(t, action, reply); got != want {
			t.Fatalf("the %s reply title = %q, want %q", action, got, want)
		}
	}
}

// replyFrame waits for the frame one action answers with: a resp for read, a
// replay frame for replay.
func replyFrame(t *testing.T, msgs chan wire.Msg, action string, id int) wire.Msg {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m, ok := <-msgs:
			if !ok {
				t.Fatalf("the socket closed before the %s reply", action)
			}
			// A replay frame carries no request id, so its type is the match.
			if m.Type == action || (m.Type == "resp" && m.ID == id) {
				return m
			}
		case <-deadline:
			t.Fatalf("no %s reply", action)
		}
	}
}
