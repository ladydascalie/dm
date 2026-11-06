package server

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dm/internal/layout"
	"dm/internal/paths"
	"dm/internal/session"
	"dm/internal/wire"
)

// ringSize is what the fake panes report as history. It stands in for the
// 512 KiB ring a real pane carries, so the attach total is measurable against
// what the server no longer pushes.
const ringSize = 256 << 10

// ringFor is the fake pane's history ring: the pane id repeated to ringSize.
func ringFor(id string) []byte {
	unit := []byte(id + ",")
	return bytes.Repeat(unit, ringSize/len(unit))
}

// ringPane answers a pane socket with a small screen and a large ring. It
// reports the alternate screen and mouse modes, so the client's pane facts can
// be checked without a replay frame.
func ringPane(t *testing.T, session, id string) {
	t.Helper()
	sock := paths.PaneSock(session, id)
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
				defer c.Close()
				for {
					m, err := wire.ReadMsg(c)
					if err != nil {
						return
					}
					switch m.Action {
					case "screen":
						_ = wire.WriteMsg(c, wire.Msg{Type: "screen", ID: m.ID, Target: id,
							Data: []byte("\x1b[Hscreen " + id), Res: jsonRaw(struct {
								Alt   bool `json:"alt"`
								Mouse bool `json:"mouse"`
							}{Alt: true, Mouse: true})})
					case "replay":
						st := wire.Status{State: "idle", Message: "ring"}
						_ = wire.WriteMsg(c, wire.Msg{Type: "replay", ID: m.ID, Target: id,
							Data: ringFor(id), St: &st})
					default:
						_ = wire.WriteMsg(c, wire.Msg{Type: "resp", ID: m.ID, OK: true})
					}
				}
			}(c)
		}
	}()
}

// histServer builds a server with panes panes in one window, each backed by a
// ring pane socket and an adopted paneConn, so attach runs the real path.
func histServer(t *testing.T, name string, panes int) (*server, []string) {
	t.Helper()
	t.Setenv("DM_STATE", t.TempDir())
	st := session.NewState(name)
	w := st.ActiveWindow()
	ids := make([]string, 0, panes)
	kids := make([]*layout.Node, 0, panes)
	for i := 0; i < panes; i++ {
		id := w.NewPaneID()
		ids = append(ids, id)
		kids = append(kids, &layout.Node{Pane: id})
		st.Panes[id] = &session.PaneState{}
	}
	w.Root = &layout.Node{Dir: "row", Kids: kids}
	w.Focus = ids[0]
	if err := os.MkdirAll(paths.SessionDir(name), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		ringPane(t, name, id)
	}
	s := railServer(st)
	adoptPanes(t, s, ids)
	return s, ids
}

// adoptPanes gives the server a paneConn per pane, the way an adopted pane
// looks. The other end of the pipe is drained, so a resize write cannot block.
func adoptPanes(t *testing.T, s *server, ids []string) {
	t.Helper()
	for _, id := range ids {
		srv, cli := net.Pipe()
		s.mu.Lock()
		s.panes[id] = &paneConn{id: id, c: srv, st: wire.Status{State: "unknown"}}
		s.mu.Unlock()
		go func(c net.Conn) {
			for {
				if _, err := wire.ReadMsg(c); err != nil {
					return
				}
			}
		}(cli)
		t.Cleanup(func() {
			_ = srv.Close()
			_ = cli.Close()
		})
	}
}

// countingConn counts the bytes a client socket receives.
type countingConn struct {
	net.Conn
	read int
}

func (w *countingConn) Read(p []byte) (int, error) {
	n, err := w.Conn.Read(p)
	w.read += n
	return n, err
}

// dialTestServer starts the server's connection handler on a real unix socket and
// returns a client socket that counts what it reads.
func dialTestServer(t *testing.T, s *server) *countingConn {
	t.Helper()
	sock := paths.ServerSock(s.st.Name)
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
			go s.serve(c)
		}
	}()
	raw, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return &countingConn{Conn: raw}
}

// readUntil reads frames until match says yes, and fails on the deadline.
func readUntil(t *testing.T, c net.Conn, wait time.Duration, match func(wire.Msg) bool) (wire.Msg, int) {
	t.Helper()
	n := 0
	for {
		m, err := readFrame(c, wait)
		if err != nil {
			t.Fatalf("no matching frame after %d frames: %v", n, err)
		}
		n++
		if match(m) {
			return m, n
		}
	}
}

// TestAttachDoesNotPushHistory proves attach sends a picture, not the ring: no
// replay frame, a small byte total, and the pane facts still correct.
func TestAttachDoesNotPushHistory(t *testing.T) {
	s, ids := histServer(t, "lazy", 4)
	cl := dialTestServer(t, s)
	if err := wire.WriteMsg(cl, wire.Msg{Type: "attach", Cols: 100, Rows: 30}); err != nil {
		t.Fatal(err)
	}

	// Collect the attach burst. Nothing is read after it, so the window is
	// long enough for a pushed replay to have arrived.
	var frames []wire.Msg
	screens := map[string]bool{}
	deadline := time.Now().Add(750 * time.Millisecond)
	for time.Now().Before(deadline) {
		m, err := readFrame(cl, time.Until(deadline))
		if err != nil {
			break
		}
		frames = append(frames, m)
		if m.Type == "screen" {
			screens[m.Target] = true
		}
		if m.Type == "replay" {
			t.Fatalf("attach pushed history for %q, %d bytes", m.Target, len(m.Data))
		}
	}
	if len(frames) == 0 {
		t.Fatal("attach sent nothing")
	}
	if len(screens) != len(ids) {
		t.Fatalf("attach screens = %v, want one per pane %v", screens, ids)
	}

	// The whole attach must stay far below the rings it no longer pushes.
	rings := ringSize * len(ids)
	if cl.read > 64<<10 || cl.read > rings/8 {
		t.Fatalf("attach sent %d bytes; rings are %d bytes", cl.read, rings)
	}
	t.Logf("attach sent %d bytes for %d panes; their rings are %d bytes",
		cl.read, len(ids), rings)

	// Alternate screen and mouse came back with the screens and the refreshed
	// snapshot, exactly as the replay loop used to provide them.
	facts := false
	for _, m := range frames {
		if m.Type != "snapshot" || m.Snap == nil {
			continue
		}
		all := len(m.Snap.Panes) == len(ids)
		for _, p := range m.Snap.Panes {
			if !p.Alt || !p.Mouse {
				all = false
			}
		}
		facts = facts || all
	}
	if !facts {
		t.Fatal("no attach snapshot carried alt and mouse for every pane")
	}
}

// TestReplayRequestReturnsTheRing proves history is on demand: the request
// answers with one replay frame carrying the ring and the pane status, ahead
// of its reply.
func TestReplayRequestReturnsTheRing(t *testing.T) {
	s, ids := histServer(t, "ask", 3)
	cl := dialTestServer(t, s)
	if err := wire.WriteMsg(cl, wire.Msg{Type: "attach", Cols: 100, Rows: 30}); err != nil {
		t.Fatal(err)
	}
	// Let the attach burst land first, so the assertions below see only the
	// answer to the request.
	for i := 0; i < len(ids)+2; i++ {
		if _, err := readFrame(cl, 5*time.Second); err != nil {
			t.Fatalf("attach frame %d: %v", i, err)
		}
	}

	want := ids[1]
	if err := wire.WriteMsg(cl, wire.Msg{Type: "req", Action: "replay", Target: want, ID: 9}); err != nil {
		t.Fatal(err)
	}
	replay, before := readUntil(t, cl, 5*time.Second, func(m wire.Msg) bool {
		return m.Type == "replay"
	})
	if before != 1 {
		t.Fatalf("replay arrived after %d frames, want the first", before)
	}
	if replay.Target != want {
		t.Fatalf("replay target = %q, want %q", replay.Target, want)
	}
	if !bytes.Equal(replay.Data, ringFor(want)) {
		t.Fatalf("replay data %d bytes, want the %d byte ring", len(replay.Data), ringSize)
	}
	if replay.St == nil || replay.St.State != "idle" || replay.St.Message != "ring" {
		t.Fatalf("replay status = %+v, want the pane's own status", replay.St)
	}

	resp, _ := readUntil(t, cl, 5*time.Second, func(m wire.Msg) bool {
		return m.Type == "resp" && m.ID == 9
	})
	if !resp.OK {
		t.Fatalf("replay reply = %+v, want ok", resp)
	}
}

// TestReplayRequestForMissingPaneFails proves a bad target is an error and not
// a panic: the server answers and keeps serving.
func TestReplayRequestForMissingPaneFails(t *testing.T) {
	s, ids := histServer(t, "miss", 2)
	cl := dialTestServer(t, s)
	if err := wire.WriteMsg(cl, wire.Msg{Type: "attach", Cols: 100, Rows: 30}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(ids)+2; i++ {
		if _, err := readFrame(cl, 5*time.Second); err != nil {
			t.Fatalf("attach frame %d: %v", i, err)
		}
	}

	if err := wire.WriteMsg(cl, wire.Msg{Type: "req", Action: "replay", Target: "nope", ID: 11}); err != nil {
		t.Fatal(err)
	}
	resp, _ := readUntil(t, cl, 5*time.Second, func(m wire.Msg) bool {
		return m.Type == "replay" || (m.Type == "resp" && m.ID == 11)
	})
	if resp.Type == "replay" {
		t.Fatalf("a missing pane produced a replay frame for %q", resp.Target)
	}
	if resp.OK || !strings.Contains(resp.Error, "no pane") {
		t.Fatalf("missing pane reply = %+v, want an error", resp)
	}

	// The connection survived: a ping still answers.
	if err := wire.WriteMsg(cl, wire.Msg{Type: "req", Action: "ping", ID: 12}); err != nil {
		t.Fatal(err)
	}
	pong, _ := readUntil(t, cl, 5*time.Second, func(m wire.Msg) bool {
		return m.Type == "resp" && m.ID == 12
	})
	if !pong.OK {
		t.Fatalf("ping after a bad replay = %+v, want ok", pong)
	}
}
