package cli

// The fake server the verb tests share: a real unix socket at the session's
// path, so transport dials the test instead of spawning a server.

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"sync"
	"testing"

	"dm/internal/paths"
	"dm/internal/wire"
)

// fakeHandler answers one request on one connection. It returns the response
// to write and whether to keep the connection open. A zero response with
// keep=false means the handler wrote its own frames.
type fakeHandler func(c net.Conn, req wire.Msg) (wire.Msg, bool)

// startFakeServer listens on the session's socket and serves every connection
// with the handler. DM_STATE moves the whole state tree into a temp directory,
// so a test never touches a real session.
func startFakeServer(t *testing.T, name string, h fakeHandler) {
	t.Helper()
	t.Setenv("DM_STATE", t.TempDir())
	if err := os.MkdirAll(paths.SessionDir(name), 0o700); err != nil {
		t.Fatal(err)
	}
	sock := paths.ServerSock(name)
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
			go serveFake(c, h)
		}
	}()
}

// serveFake reads requests and hands each to the handler until the handler or
// the peer ends the connection.
func serveFake(c net.Conn, h fakeHandler) {
	defer func() { _ = c.Close() }()
	for {
		req, err := wire.ReadMsg(c)
		if err != nil {
			return
		}
		resp, keep := h(c, req)
		if !keep {
			return
		}
		resp.Type = "resp"
		resp.ID = req.ID
		if err := wire.WriteMsg(c, resp); err != nil {
			return
		}
	}
}

// okHandler replies OK with a JSON body to every request.
func okHandler(res string) fakeHandler {
	return func(_ net.Conn, _ wire.Msg) (wire.Msg, bool) {
		return wire.Msg{OK: true, Res: json.RawMessage(res)}, true
	}
}

// refuseHandler answers every request with the error text, so a test can walk
// the !OK path a verb carries.
func refuseHandler(errText string) fakeHandler {
	return func(_ net.Conn, _ wire.Msg) (wire.Msg, bool) {
		return wire.Msg{OK: false, Error: errText}, true
	}
}

// recorder keeps the requests the fake server received, so a test asserts on
// the wire message a verb sent.
type recorder struct {
	mu  sync.Mutex
	got []wire.Msg
}

func (r *recorder) handler() fakeHandler {
	return func(_ net.Conn, req wire.Msg) (wire.Msg, bool) {
		r.mu.Lock()
		r.got = append(r.got, req)
		r.mu.Unlock()
		return wire.Msg{OK: true, Res: json.RawMessage(`{}`)}, true
	}
}

func (r *recorder) last() wire.Msg {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.got) == 0 {
		return wire.Msg{}
	}
	return r.got[len(r.got)-1]
}

// captureStderr runs fn with os.Stderr on a pipe and returns what it wrote.
// Stdout stays untouched, so a test wraps this in testutil.CaptureStdout.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	fn()
	os.Stderr = old
	_ = w.Close()
	defer func() { _ = r.Close() }()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// TestMain keeps a spawned server child from running the test suite. Ensure
// starts os.Executable with __server when no daemon answers, and in a test
// binary that would run the tests again and spawn again. The child exits at
// once, so the caller's dial retries fail into the did-not-start error the
// no-server test asserts.
func TestMain(m *testing.M) {
	for _, a := range os.Args[1:] {
		if a == "__server" {
			os.Exit(3)
		}
	}
	os.Exit(m.Run())
}
