package server

import (
	"net"
	"strings"
	"testing"
	"time"

	"dm/internal/wire"
)

// screenReqServer is the queue fixture with its pane connections registered,
// so a screen request finds a pane and dials the fake socket behind it.
func screenReqServer(t *testing.T, name string) (*server, string, string) {
	t.Helper()
	s, a, b := queueServer(t, name)
	adoptPanes(t, s, []string{a, b})
	return s, a, b
}

// requestScreen asks for one pane's screen through handleReq and returns the
// frames the client received, in order.
func requestScreen(t *testing.T, s *server, target string, id int) []wire.Msg {
	t.Helper()
	srv, cli := net.Pipe()
	t.Cleanup(func() { _ = srv.Close(); _ = cli.Close() })
	cl := newClient(srv)
	s.mu.Lock()
	s.clients[cl] = true
	s.mu.Unlock()
	t.Cleanup(func() {
		s.mu.Lock()
		delete(s.clients, cl)
		s.mu.Unlock()
		cl.shutdown()
	})
	s.handleReq(cl, wire.Msg{Type: "req", Action: "screen", Target: target, ID: id})
	_ = cli.SetReadDeadline(time.Now().Add(2 * time.Second))
	var out []wire.Msg
	for {
		m, err := wire.ReadMsg(cli)
		if err != nil {
			return out
		}
		out = append(out, m)
		if m.Type == "resp" {
			return out
		}
	}
}

// TestScreenRequestReachesTheClient: a screen request for a live pane answers
// with a screen message carrying the pane's grid, then the request's reply.
func TestScreenRequestReachesTheClient(t *testing.T) {
	s, a, _ := screenReqServer(t, "screenreq")
	frames := requestScreen(t, s, a, 7)
	if len(frames) != 2 {
		t.Fatalf("frames = %+v, want a screen and a reply", frames)
	}
	screen, reply := frames[0], frames[1]
	if screen.Type != "screen" || screen.Target != a {
		t.Fatalf("first frame = %+v, want the screen of %s", screen, a)
	}
	if screen.VTSnap == nil {
		t.Fatal("the screen frame carries no snapshot")
	}
	if got, want := string(snapshotBytes(t, screen.VTSnap)), string(snapshotBytes(t, newScreenSnapshot(a))); got != want {
		t.Fatalf("screen grid = %q, want the pane's own %q", got, want)
	}
	if reply.Type != "resp" || !reply.OK || reply.Action != "screen" || reply.ID != 7 {
		t.Fatalf("reply = %+v, want a good screen reply for id 7", reply)
	}
	if !strings.Contains(string(reply.Res), a) {
		t.Fatalf("reply res = %s, want the pane id", reply.Res)
	}
}

// TestScreenRequestErrors: an unknown pane and a pane that does not answer both
// reply with an error and send no screen.
func TestScreenRequestErrors(t *testing.T) {
	s, _, _ := screenReqServer(t, "screenerr")

	// A pane the server does not know.
	frames := requestScreen(t, s, "nope", 3)
	if len(frames) != 1 || frames[0].Type != "resp" || frames[0].OK {
		t.Fatalf("unknown pane frames = %+v, want one failed reply", frames)
	}
	if !strings.Contains(frames[0].Error, "no pane") {
		t.Fatalf("unknown pane error = %q, want it to name the missing pane", frames[0].Error)
	}

	// A pane the server knows whose daemon does not answer: the connection is
	// registered but no socket stands behind it.
	adoptPanes(t, s, []string{"dead"})
	frames = requestScreen(t, s, "dead", 4)
	if len(frames) != 1 || frames[0].Type != "resp" || frames[0].OK {
		t.Fatalf("silent pane frames = %+v, want one failed reply", frames)
	}
	if frames[0].Error == "" {
		t.Fatal("the silent pane replied without an error")
	}
}
