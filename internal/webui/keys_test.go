package webui

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dm/internal/paths"
	"dm/internal/wire"
)

// keySession answers the session socket for one handler and hands every request
// it receives to got, so a test can see the bytes the page's key press became.
func keySession(t *testing.T, dir, session string, got chan<- wire.Msg) {
	t.Helper()
	listenUnix(t, paths.ServerSockAt(dir, session), func(c net.Conn) {
		for {
			m, err := wire.ReadMsg(c)
			if err != nil {
				return
			}
			select {
			case got <- m:
			default:
			}
			_ = wire.WriteMsg(c, wire.Msg{Type: "resp", Action: m.Action, ID: m.ID, OK: true})
		}
	})
}

// keysServe starts the surface over a session socket that records what it is
// asked, and returns the requests it saw.
func keysServe(t *testing.T, readOnly bool) (*httptest.Server, chan wire.Msg) {
	t.Helper()
	dir := root(t, "main")
	got := make(chan wire.Msg, 8)
	keySession(t, dir, "main", got)
	srv := httptest.NewServer(handler(Config{Session: "main", Root: dir, Token: "t", ReadOnly: readOnly}))
	t.Cleanup(srv.Close)
	return srv, got
}

// press posts one key press and returns the response.
func press(t *testing.T, srv *httptest.Server, body map[string]string) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.Post(srv.URL+"/api/keys?t=t", "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestKeysSendsEscape proves the stop control reaches the pane as one Esc byte,
// which is the whole way a phone interrupts a turn or declines a question.
func TestKeysSendsEscape(t *testing.T) {
	t.Parallel()
	srv, got := keysServe(t, false)
	res := press(t, srv, map[string]string{"pane": "w1p1", "key": "esc"})
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	select {
	case m := <-got:
		if m.Action != "keys" || m.Target != "w1p1" {
			t.Fatalf("request = %s %q, want keys to w1p1", m.Action, m.Target)
		}
		if len(m.Keys) != 1 || m.Keys[0] != 0x1b {
			t.Fatalf("keys = %v, want one escape byte", m.Keys)
		}
	default:
		t.Fatal("the daemon was never asked")
	}
}

// TestKeysSendsCtrlC proves the same path carries the other key a shell needs.
func TestKeysSendsCtrlC(t *testing.T) {
	t.Parallel()
	srv, got := keysServe(t, false)
	res := press(t, srv, map[string]string{"pane": "w2p3", "key": "ctrlc"})
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	select {
	case m := <-got:
		if len(m.Keys) != 1 || m.Keys[0] != 0x03 {
			t.Fatalf("keys = %v, want one ctrl-c byte", m.Keys)
		}
	default:
		t.Fatal("the daemon was never asked")
	}
}

// TestKeysRefuses proves the endpoint is not a keyboard driver: an unknown key,
// a pane id that could walk the filesystem, and a read only page all stop here.
func TestKeysRefuses(t *testing.T) {
	t.Parallel()
	srv, _ := keysServe(t, false)
	readOnly, _ := keysServe(t, true)
	cases := []struct {
		name string
		srv  *httptest.Server
		body map[string]string
		want int
	}{
		{"an unknown key", srv, map[string]string{"pane": "w1p1", "key": "ctrl-d"}, http.StatusBadRequest},
		{"a path as a pane", srv, map[string]string{"pane": "../../etc/passwd", "key": "esc"}, http.StatusBadRequest},
		{"a read only page", readOnly, map[string]string{"pane": "w1p1", "key": "esc"}, http.StatusForbidden},
	}
	for _, c := range cases {
		res := press(t, c.srv, c.body)
		_ = res.Body.Close()
		if res.StatusCode != c.want {
			t.Fatalf("%s: status %d, want %d", c.name, res.StatusCode, c.want)
		}
	}
}
