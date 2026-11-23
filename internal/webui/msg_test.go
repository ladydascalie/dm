package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dm/internal/wire"
)

// msgServe starts the surface over a session socket that records what it is
// asked, and returns the requests it saw.
func msgServe(t *testing.T) (*httptest.Server, chan wire.Msg) {
	t.Helper()
	dir := root(t, "main")
	got := make(chan wire.Msg, 8)
	keySession(t, dir, "main", got)
	srv := httptest.NewServer(handler(Config{Session: "main", Root: dir, Token: "t"}))
	t.Cleanup(srv.Close)
	return srv, got
}

// postMsg posts one message body and returns the response.
func postMsg(t *testing.T, srv *httptest.Server, body map[string]any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.Post(srv.URL+"/api/msg?t=t", "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestMsgCarriesThePasteFlag proves the page's block reaches the daemon as a
// paste: the text goes whole, the flag rides the write-to frame and no enter
// follows it.
func TestMsgCarriesThePasteFlag(t *testing.T) {
	t.Parallel()
	srv, got := msgServe(t)
	res := postMsg(t, srv, map[string]any{"pane": "w1p1", "text": "one\ntwo", "paste": true})
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	select {
	case m := <-got:
		if m.Action != "write-to" || m.Target != "w1p1" {
			t.Fatalf("request = %s %q, want write-to w1p1", m.Action, m.Target)
		}
		if m.Msg != "one\ntwo" || !m.Paste {
			t.Fatalf("write-to = %q paste=%v, want the block marked as a paste", m.Msg, m.Paste)
		}
		if m.Enter {
			t.Fatal("the paste carried an enter")
		}
	default:
		t.Fatal("the daemon was never asked")
	}
}

// TestMsgKeepsTheTypedShape proves a single line is what it always was: the
// text and its enter, with no paste flag on the wire.
func TestMsgKeepsTheTypedShape(t *testing.T) {
	t.Parallel()
	srv, got := msgServe(t)
	res := postMsg(t, srv, map[string]any{"pane": "w1p1", "text": "ls", "enter": true})
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	select {
	case m := <-got:
		if m.Msg != "ls" || !m.Enter || m.Paste {
			t.Fatalf("write-to = %+v, want the typed line unchanged", m)
		}
	default:
		t.Fatal("the daemon was never asked")
	}
}

// TestMsgRefusesPasteWithEnter proves the endpoint refuses the contradictory
// request: a paste never submits, so it never carries an enter, and the daemon
// is never asked.
func TestMsgRefusesPasteWithEnter(t *testing.T) {
	t.Parallel()
	srv, got := msgServe(t)
	res := postMsg(t, srv, map[string]any{
		"pane": "w1p1", "text": "one\ntwo", "paste": true, "enter": true,
	})
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", res.StatusCode)
	}
	select {
	case m := <-got:
		t.Fatalf("the daemon was asked anyway: %+v", m)
	default:
	}
}
