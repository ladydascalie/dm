package client

import (
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"dm/internal/layout"
	"dm/internal/testutil"
	"dm/internal/transport"
	"dm/internal/wire"
)

// restartConn starts one attached client over a pipe the test owns and returns
// the server end plus the channel the attach result lands on. It is the attach
// without a terminal, so the close frame reaches the client through the same
// reader goroutine a real session uses.
func restartConn(t *testing.T) (net.Conn, <-chan error) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DM_STATE", t.TempDir())
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	done := make(chan error, 1)
	go func() {
		_, err := attachConn(&transport.Conn{Conn: clientConn}, Harness{
			Out: &syncBuffer{}, Keys: make(chan []byte), Winch: make(chan os.Signal, 1),
			Size: func() (int, int) { return 80, 24 }, Session: "test",
		}, &pacer{})
		done <- err
	}()

	// Answer the attach with one snapshot, so the client is drawing when the
	// close frame arrives, the way it is in a live session.
	m, err := wire.ReadMsg(serverConn)
	if err != nil {
		t.Fatalf("read attach: %v", err)
	}
	if m.Type != "attach" {
		t.Fatalf("first frame = %q, want attach", m.Type)
	}
	snap := testutil.TestSnapshot(&layout.Node{Pane: "a"}, m.Cols, m.Rows, nil)
	snap.Session, snap.Workspace, snap.Focus = "test", "main", "a"
	if err := wire.WriteMsg(serverConn, wire.Msg{Type: "snapshot", Snap: snap}); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	return serverConn, done
}

// waitAttachEnd waits for the attach to return and reports what it returned.
func waitAttachEnd(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the attach did not return within 5s")
		return nil
	}
}

// TestCloseWithRestartAsksForReconnect proves the two close frames are told
// apart: the restart reason ends the attach with errServerRestart, so Run knows
// to exec the binary on disk and dial again, and a plain close still means the
// client is done.
func TestCloseWithRestartAsksForReconnect(t *testing.T) {
	conn, done := restartConn(t)
	if err := wire.WriteMsg(conn, wire.Msg{Type: "close", Msg: wire.CloseRestart}); err != nil {
		t.Fatalf("write close: %v", err)
	}
	if err := waitAttachEnd(t, done); !errors.Is(err, errServerRestart) {
		t.Fatalf("attach returned %v, want errServerRestart", err)
	}
}

// TestPlainCloseEndsTheClient proves a close with no reason is still a detach:
// the attach returns cleanly and nothing execs.
func TestPlainCloseEndsTheClient(t *testing.T) {
	conn, done := restartConn(t)
	if err := wire.WriteMsg(conn, wire.Msg{Type: "close"}); err != nil {
		t.Fatalf("write close: %v", err)
	}
	if err := waitAttachEnd(t, done); err != nil {
		t.Fatalf("attach returned %v, want a clean end", err)
	}
}

// TestRestartActionAsksTheServer proves the keybinding sends the request the
// server answers by leaving: the action queues the restart and lets the close
// frame end the attach, so it can never mistake a request for a result.
func TestRestartActionAsksTheServer(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	tui := &tui{conn: &transport.Conn{Conn: clientConn}}

	go tui.runAction("restart-server")
	m, err := wire.ReadMsg(serverConn)
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	if m.Type != "req" || m.Action != "restart" {
		t.Fatalf("frame = %+v, want req restart", m)
	}
}
