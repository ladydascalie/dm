package client

import (
	"bytes"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"dm/internal/layout"
	"dm/internal/testutil"
	"dm/internal/transport"
	"dm/internal/wire"
)

// The spike's proof, without a browser: one attached session over a connection
// the test owns, with no terminal, no raw mode, no os.Stdout and no SIGWINCH.
// A frame comes out, a key goes in, and a resize travels.

// syncBuffer is a writer the client goroutine and the test both touch.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Len()
}

// fakeSize is the page's role: a size the test changes, read by the client
// when the resize signal arrives.
type fakeSize struct {
	mu         sync.Mutex
	cols, rows int
}

func (s *fakeSize) get() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cols, s.rows
}

func (s *fakeSize) set(cols, rows int) {
	s.mu.Lock()
	s.cols, s.rows = cols, rows
	s.mu.Unlock()
}

// testSignal stands in for SIGWINCH, which has no js/wasm equivalent.
type testSignal struct{}

func (testSignal) String() string { return "resize" }
func (testSignal) Signal()        {}

func TestAttachConnRunsWithNoTerminal(t *testing.T) {
	// Attach loads the keymap from the config path, which writes a file when
	// it is missing. Keep the operator's own config out of it.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	out := &syncBuffer{}
	size := &fakeSize{cols: 80, rows: 24}
	keys := make(chan []byte, 1)
	winch := make(chan os.Signal, 1)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	frames := make(chan wire.Msg, 16)
	go func() {
		for {
			m, err := wire.ReadMsg(serverConn)
			if err != nil {
				close(frames)
				return
			}
			switch m.Type {
			case "attach", "resize":
				// Answer every size change with the picture a server sends.
				snap := testutil.TestSnapshot(&layout.Node{Pane: "a"}, m.Cols, m.Rows, nil)
				snap.Session, snap.Workspace, snap.Focus = "test", "main", "a"
				_ = wire.WriteMsg(serverConn, wire.Msg{Type: "snapshot", Snap: snap})
			}
			select {
			case frames <- m:
			default:
			}
		}
	}()

	var events []Event
	var evMu sync.Mutex
	go func() {
		onEvent := func(e Event) { evMu.Lock(); events = append(events, e); evMu.Unlock() }
		_, _ = attachConn(&transport.Conn{Conn: clientConn}, out, keys, winch, size.get, "test", &pacer{}, onEvent)
	}()

	waitFrame(t, frames, func(m wire.Msg) bool { return m.Type == "attach" })
	waitBytes(t, out, "the first frame")

	keys <- []byte("x")
	m := waitFrame(t, frames, func(m wire.Msg) bool { return m.Type == "keys" })
	if string(m.Keys) != "x" {
		t.Fatalf("keys = %q, want %q", m.Keys, "x")
	}

	// The tap reports the action a keybinding ran.
	keys <- []byte("\x02")
	keys <- []byte("|")
	deadline := time.After(5 * time.Second)
	for {
		evMu.Lock()
		found := false
		for _, e := range events {
			if e.Type == "action" && e.Name == "split-right" {
				found = true
			}
		}
		evMu.Unlock()
		if found {
			break
		}
		select {
		case <-deadline:
			t.Fatal("no split-right action reported")
		case <-time.After(5 * time.Millisecond):
		}
	}

	size.set(100, 30)
	winch <- testSignal{}
	m = waitFrame(t, frames, func(m wire.Msg) bool { return m.Type == "resize" })
	if m.Cols != 100 || m.Rows != 30 {
		t.Fatalf("resize = %dx%d, want 100x30", m.Cols, m.Rows)
	}
}

// waitFrame reads frames until want says yes.
func waitFrame(t *testing.T, frames <-chan wire.Msg, want func(wire.Msg) bool) wire.Msg {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m, ok := <-frames:
			if !ok {
				t.Fatal("the client closed the connection")
			}
			if want(m) {
				return m
			}
		case <-deadline:
			t.Fatal("no matching frame within 5s")
		}
	}
}

// waitBytes waits until the client has written something, which is the proof
// that it painted with no terminal under it.
func waitBytes(t *testing.T, out *syncBuffer, what string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for out.Len() == 0 {
		select {
		case <-deadline:
			t.Fatalf("%s never reached the writer", what)
		case <-time.After(5 * time.Millisecond):
		}
	}
}
