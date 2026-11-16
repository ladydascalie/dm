package server

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"dm/internal/wire"
)

// togglePane plays the privileged end of one pane conn over a pipe: it
// records the frames the server sends and answers toggle-secrets the way
// the guarded daemon does, nonce check included.
type togglePane struct {
	pane net.Conn
	seen chan wire.Msg
}

// newTogglePane registers the conn as the pane's adopted conn, carrying the
// nonce an adopt read from the daemon's handshake, and starts the watcher
// that reads the conn's replies.
func newTogglePane(t *testing.T, s *server, id, nonce string) *togglePane {
	t.Helper()
	srv, pane := net.Pipe()
	s.mu.Lock()
	s.panes[id] = &paneConn{
		id: id, c: srv, st: wire.Status{State: "unknown"},
		sendTimeout: paneSendTimeout, installedAt: time.Now(), nonce: nonce,
	}
	s.mu.Unlock()
	s.watchPane(id)
	t.Cleanup(func() { _ = srv.Close(); _ = pane.Close() })
	tp := &togglePane{pane: pane, seen: make(chan wire.Msg, 16)}
	go func() {
		for {
			m, err := wire.ReadMsg(pane)
			if err != nil {
				return
			}
			select {
			case tp.seen <- m:
			default:
			}
			if m.Action == "toggle-secrets" {
				if nonce == "" || m.Nonce != nonce {
					_ = wire.WriteMsg(pane, wire.Msg{Type: "resp", ID: m.ID, OK: false,
						Error: "toggle-secrets needs the pane's nonce"})
					continue
				}
				_ = wire.WriteMsg(pane, wire.Msg{Type: "resp", ID: m.ID, OK: true,
					Res: json.RawMessage(`{"enabled":false}`)})
			}
		}
	}()
	return tp
}

// The toggle answers only to an attached client: a toggle a dm exec script
// could send would let an agent running inside dm disable its own redaction.
func TestToggleSecretsRefusesUnattachedConn(t *testing.T) {
	t.Parallel()
	s, ids := histServer(t, "toggleguard", 2)
	newTogglePane(t, s, ids[0], "handshake-nonce")
	cl, conn := boxConn(t)
	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "toggle-secrets", Target: ids[0], ID: 1})
	wantErr(t, "toggle-secrets", "interactive client", m)
}

// The toggle rides the adopted conn carrying the nonce, and the reply
// carries the pane and the state the daemon reports.
func TestToggleSecretsForwardsAndReports(t *testing.T) {
	t.Parallel()
	s, ids := histServer(t, "toggleride", 2)
	tp := newTogglePane(t, s, ids[0], "handshake-nonce")
	cl, conn := boxConn(t)
	cl.attached = true
	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "toggle-secrets", Target: ids[0], ID: 1})
	if !m.OK {
		t.Fatalf("toggle-secrets failed: %s", m.Error)
	}
	var res struct {
		Pane    string `json:"pane"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.Unmarshal(m.Res, &res); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if res.Pane != ids[0] || res.Enabled {
		t.Fatalf("reply = %+v, want pane %q enabled false", res, ids[0])
	}
	select {
	case got := <-tp.seen:
		if got.Action != "toggle-secrets" || got.Nonce != "handshake-nonce" {
			t.Fatalf("the pane received %+v, want toggle-secrets carrying the nonce", got)
		}
	default:
		t.Fatal("the pane received no toggle")
	}
}

// A conn adopted without the handshake sends an empty nonce and the daemon
// refuses; the refusal reaches the client as the reply's error.
func TestToggleSecretsRefusedWithoutNonce(t *testing.T) {
	t.Parallel()
	s, ids := histServer(t, "togglenonce", 2)
	newTogglePane(t, s, ids[0], "")
	cl, conn := boxConn(t)
	cl.attached = true
	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "toggle-secrets", Target: ids[0], ID: 1})
	if m.OK {
		t.Fatal("the toggle succeeded without a nonce, want the daemon's refusal")
	}
	if !strings.Contains(m.Error, "nonce") {
		t.Fatalf("error = %q, want it to name the nonce", m.Error)
	}
}

// An empty target resolves the focused pane of the active window, the pane
// the client's key press acts on.
func TestToggleSecretsTargetsFocus(t *testing.T) {
	t.Parallel()
	s, cl, conn, ids := toggleServer(t, "togglefocus")
	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "toggle-secrets", ID: 1})
	if !m.OK {
		t.Fatalf("toggle-secrets failed: %s", m.Error)
	}
	if !strings.Contains(string(m.Res), ids[0]) {
		t.Fatalf("reply %s does not name the focused pane %q", m.Res, ids[0])
	}
}

// A pane that is not there fails with the error every pane action answers.
func TestToggleSecretsMissingPaneFails(t *testing.T) {
	t.Parallel()
	s, cl, conn, _ := toggleServer(t, "togglegone")
	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "toggle-secrets", Target: "ghost", ID: 1})
	wantErr(t, "toggle-secrets", "no pane", m)
}

// toggleServer is a histServer whose first pane speaks the guarded toggle,
// plus an attached client for the request path.
func toggleServer(t *testing.T, name string) (*server, *client, net.Conn, []string) {
	t.Helper()
	s, ids := histServer(t, name, 2)
	newTogglePane(t, s, ids[0], "handshake-nonce")
	cl, conn := boxConn(t)
	cl.attached = true
	return s, cl, conn, ids
}
