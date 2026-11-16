package pane

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"dm/internal/vt"
	"dm/internal/wire"
)

// guardedPane is a pane serving one conn that completed the nonce
// handshake, the way the accept loop runs it: hello first, then serve. It
// returns the pane, the privileged conn, the frames that conn receives and
// the nonce it was presented.
func guardedPane(t *testing.T, cols, rows int) (*paneProc, net.Conn, chan wire.Msg, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})
	go func() { _, _ = io.Copy(io.Discard, r) }()
	p := &paneProc{
		id: "w1p1", vt: vt.NewTerm(cols, rows), ring: NewRing(ringDefault),
		subs: map[*subConn]bool{}, st: wire.Status{State: "idle"}, ptmx: w,
	}
	serve, cli := net.Pipe()
	t.Cleanup(func() { _ = cli.Close(); _ = serve.Close() })
	msgs := make(chan wire.Msg, 64)
	go func() {
		for {
			m, err := wire.ReadMsg(cli)
			if err != nil {
				close(msgs)
				return
			}
			msgs <- m
		}
	}()
	p.hello(serve)
	go p.serve(serve)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m, ok := <-msgs:
			if !ok {
				t.Fatal("socket closed before the nonce frame")
			}
			if m.Type != "nonce" {
				continue
			}
			var hello struct {
				Nonce string `json:"nonce"`
			}
			if err := json.Unmarshal(m.Res, &hello); err != nil {
				t.Fatalf("nonce frame: %v", err)
			}
			return p, cli, msgs, hello.Nonce
		case <-deadline:
			t.Fatal("no nonce frame")
		}
	}
}

// subscriberConn opens a second conn the handshake never privileged.
func subscriberConn(t *testing.T, p *paneProc) net.Conn {
	t.Helper()
	serve, cli := net.Pipe()
	t.Cleanup(func() { _ = cli.Close(); _ = serve.Close() })
	go p.serve(serve)
	return cli
}

// waitGuardFree waits for the privileged slot to clear after its conn goes
// away, so the next hello re-handshakes.
func waitGuardFree(t *testing.T, p *paneProc) {
	t.Helper()
	for i := 0; i < 200; i++ {
		p.guardMu.Lock()
		free := p.guardConn == nil
		p.guardMu.Unlock()
		if free {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the privileged slot never cleared")
}

// The privileged conn carrying the nonce flips the stage, and the reply
// carries the state it landed on: enabled false first, since a daemon
// starts enabled, then true.
func TestToggleSecretsActionReportsState(t *testing.T) {
	t.Parallel()
	p, cli, msgs, nonce := guardedPane(t, 20, 5)
	reply := paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "toggle-secrets", ID: 1, Nonce: nonce})
	if !reply.OK {
		t.Fatalf("toggle-secrets reply = %+v, want ok", reply)
	}
	if enabled, ok := replyEnabled(t, reply); ok && enabled {
		t.Fatalf("first toggle reported enabled %v, want false", enabled)
	}
	reply = paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "toggle-secrets", ID: 2, Nonce: nonce})
	if enabled, ok := replyEnabled(t, reply); !ok || !enabled {
		t.Fatalf("second toggle reported enabled %v ok %v, want true", enabled, ok)
	}
	_ = p
}

// The privileged conn without the nonce is refused, and the refused call
// must not have flipped the stage: the next authorized toggle still lands
// on enabled false.
func TestToggleSecretsRefusedWithoutNonce(t *testing.T) {
	t.Parallel()
	p, cli, msgs, nonce := guardedPane(t, 20, 5)
	reply := paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "toggle-secrets", ID: 1})
	if reply.OK {
		t.Fatal("toggle-secrets without the nonce succeeded, want a refusal")
	}
	if !strings.Contains(reply.Error, "nonce") {
		t.Fatalf("refusal = %q, want it to name the nonce", reply.Error)
	}
	reply = paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "toggle-secrets", ID: 2, Nonce: nonce})
	if enabled, ok := replyEnabled(t, reply); !ok || enabled {
		t.Fatalf("post-refusal toggle reported enabled %v ok %v, want false: the refused call must not flip", enabled, ok)
	}
	_ = p
}

// A conn the handshake never privileged is refused outright, nonce or not,
// for both privileged actions.
func TestToggleSecretsRefusesSubscriberConn(t *testing.T) {
	t.Parallel()
	p, cli, msgs, nonce := guardedPane(t, 20, 5)
	sub := subscriberConn(t, p)
	subMsgs := make(chan wire.Msg, 64)
	go func() {
		for {
			m, err := wire.ReadMsg(sub)
			if err != nil {
				close(subMsgs)
				return
			}
			subMsgs <- m
		}
	}()
	for _, tc := range []struct {
		name  string
		nonce string
	}{
		{"no nonce", ""},
		{"wrong nonce", "not-the-nonce"},
		{"the real nonce", nonce},
	} {
		reply := paneRequest(t, sub, subMsgs, wire.Msg{Type: "req", Action: "toggle-secrets", ID: 7, Nonce: tc.nonce})
		if reply.OK {
			t.Fatalf("subscriber conn with %s toggled, want a refusal", tc.name)
		}
		reply = paneRequest(t, sub, subMsgs, wire.Msg{Type: "req", Action: "set-secrets", ID: 8, Nonce: tc.nonce, Res: json.RawMessage(`{"values":["x"]}`)})
		if reply.OK {
			t.Fatalf("subscriber conn with %s pushed set-secrets, want a refusal", tc.name)
		}
	}
	// The privileged conn still works after the subscriber's attempts.
	reply := paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "toggle-secrets", ID: 9, Nonce: nonce})
	if enabled, ok := replyEnabled(t, reply); !ok || enabled {
		t.Fatalf("post-subscriber toggle reported enabled %v ok %v, want false", enabled, ok)
	}
}

// A set-secrets push without the nonce is refused and leaves the list
// alone: the authorized push after it still redacts the value it names.
func TestSetSecretsRefusedWithoutNonce(t *testing.T) {
	t.Parallel()
	p, cli, msgs, nonce := guardedPane(t, 20, 5)
	reply := paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "set-secrets", ID: 1, Res: json.RawMessage(`{"values":["token1234"]}`)})
	if reply.OK {
		t.Fatal("set-secrets without the nonce succeeded, want a refusal")
	}
	got := runScrubFeed(t, &p.scrub, "see token1234 here")
	if got != "see token1234 here" {
		t.Fatalf("refused push still set the values: %q", got)
	}
	reply = paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "set-secrets", ID: 2, Nonce: nonce, Res: json.RawMessage(`{"values":["token1234"]}`)})
	if !reply.OK {
		t.Fatalf("authorized set-secrets failed: %s", reply.Error)
	}
	got = runScrubFeed(t, &p.scrub, "see token1234 here")
	if want := "see ********* here"; got != want {
		t.Fatalf("authorized push emitted %q, want %q", got, want)
	}
}

// A re-adopt replaces the nonce: the reconnecting conn gets a fresh one and
// the old one stops working.
func TestPaneRehandshakesReplacesNonce(t *testing.T) {
	t.Parallel()
	p, cli, _, nonce1 := guardedPane(t, 20, 5)
	_ = cli.Close()
	waitGuardFree(t, p)

	serve2, cli2 := net.Pipe()
	t.Cleanup(func() { _ = cli2.Close(); _ = serve2.Close() })
	msgs2 := make(chan wire.Msg, 64)
	go func() {
		for {
			m, err := wire.ReadMsg(cli2)
			if err != nil {
				close(msgs2)
				return
			}
			msgs2 <- m
		}
	}()
	p.hello(serve2)
	go p.serve(serve2)
	var nonce2 string
	deadline := time.After(5 * time.Second)
	for nonce2 == "" {
		select {
		case m, ok := <-msgs2:
			if !ok {
				t.Fatal("socket closed before the fresh nonce frame")
			}
			if m.Type == "nonce" {
				var hello struct {
					Nonce string `json:"nonce"`
				}
				if err := json.Unmarshal(m.Res, &hello); err != nil {
					t.Fatalf("nonce frame: %v", err)
				}
				nonce2 = hello.Nonce
			}
		case <-deadline:
			t.Fatal("no fresh nonce frame after the reconnect")
		}
	}
	if nonce2 == nonce1 {
		t.Fatal("the re-handshake presented the same nonce, want a fresh one")
	}
	// The old nonce stops working even on the conn the fresh one belongs to.
	reply := paneRequest(t, cli2, msgs2, wire.Msg{Type: "req", Action: "toggle-secrets", ID: 1, Nonce: nonce1})
	if reply.OK {
		t.Fatal("the old nonce toggled after the re-handshake, want a refusal")
	}
	reply = paneRequest(t, cli2, msgs2, wire.Msg{Type: "req", Action: "toggle-secrets", ID: 2, Nonce: nonce2})
	if enabled, ok := replyEnabled(t, reply); !ok || enabled {
		t.Fatalf("fresh-nonce toggle reported enabled %v ok %v, want false", enabled, ok)
	}
}

// replyEnabled reads the toggle-secrets reply's {"enabled":bool} body.
func replyEnabled(t *testing.T, m wire.Msg) (bool, bool) {
	t.Helper()
	var res struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(m.Res, &res); err != nil {
		t.Fatalf("toggle-secrets reply body %s: %v", m.Res, err)
	}
	return res.Enabled, len(m.Res) > 0
}
