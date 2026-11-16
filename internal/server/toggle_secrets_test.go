package server

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dm/internal/session"
	"dm/internal/wire"
)

// togglePaneDaemon answers toggle-secrets the way a real pane daemon does:
// the reply carries the state the pane landed on. The socket sits where the
// server's paneRequest dials.
func togglePaneDaemon(t *testing.T, st *session.State, id string, enabled bool) {
	t.Helper()
	sock := st.PaneSock(id)
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				for {
					m, err := wire.ReadMsg(c)
					if err != nil {
						return
					}
					_ = wire.WriteMsg(c, wire.Msg{Type: "resp", ID: m.ID, OK: true,
						Res: json.RawMessage(`{"enabled":` + map[bool]string{true: "true", false: "false"}[enabled] + `}`)})
				}
			}(c)
		}
	}()
}

// toggleServer is a histServer whose first pane answers the toggle, plus an
// attached client for the request path.
func toggleServer(t *testing.T, name string) (*server, *client, net.Conn, []string) {
	t.Helper()
	s, ids := histServer(t, name, 1)
	togglePaneDaemon(t, s.st, ids[0], false)
	cl, conn := boxConn(t)
	cl.attached = true
	return s, cl, conn, ids
}

// The toggle answers only to an attached client: a toggle a dm exec script
// could send would let an agent running inside dm disable its own redaction.
func TestToggleSecretsRefusesUnattachedConn(t *testing.T) {
	t.Parallel()
	s, ids := histServer(t, "toggleguard", 1)
	togglePaneDaemon(t, s.st, ids[0], false)
	cl, conn := boxConn(t)
	m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "toggle-secrets", Target: ids[0], ID: 1})
	wantErr(t, "toggle-secrets", "interactive client", m)
}

// The toggle rides to the named pane's daemon and the reply carries the pane
// and the state the daemon reports.
func TestToggleSecretsForwardsAndReports(t *testing.T) {
	t.Parallel()
	s, cl, conn, ids := toggleServer(t, "toggleride")
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
