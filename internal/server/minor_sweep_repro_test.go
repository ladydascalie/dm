package server

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"dm/internal/session"
	"dm/internal/testutil"
	"dm/internal/wire"
)

// TestNamesBoundedAndFiltered proves one request cannot plant a giant or
// control-character name in the state: pane, window, workspace and project
// names all reject them.
func TestNamesBoundedAndFiltered(t *testing.T) {
	s, cl, conn, a, _ := rpcServer(t, "namebounds")
	s.mu.Lock()
	wsID := s.st.ActiveWorkspace().ID
	winID := s.st.ActiveWindow().ID
	s.mu.Unlock()

	huge := make([]byte, 200)
	for i := range huge {
		huge[i] = 'x'
	}
	for _, name := range []string{string(huge), "bad\x01name"} {
		if m := rpcCall(t, s, cl, conn,
			wire.Msg{Type: "req", Action: "set-pane-name", Target: a, Name: name, ID: 1}); m.OK {
			t.Fatalf("set-pane-name %q accepted", name)
		}
		if err := s.renameWindow(winID, name); err == nil {
			t.Fatalf("renameWindow accepted %q", name)
		}
		if err := s.renameWorkspace(wsID, name); err == nil {
			t.Fatalf("renameWorkspace accepted %q", name)
		}
		if err := s.setProject(wsID, name); err == nil {
			t.Fatalf("setProject accepted %q", name)
		}
		if _, _, err := s.newWindow(wsID, name, "", false, nil, nil); err == nil {
			t.Fatalf("newWindow label accepted %q", name)
		}
		if _, _, _, err := s.newWorkspace(name, name, "", "", false, nil); err == nil {
			t.Fatalf("newWorkspace accepted %q", name)
		}
	}
	// A good name still lands.
	if m := rpcCall(t, s, cl, conn,
		wire.Msg{Type: "req", Action: "set-pane-name", Target: a, Name: "builder", ID: 2}); !m.OK {
		t.Fatalf("set-pane-name builder: %s", m.Error)
	}
}

// TestFloatSizeClampsToUint16 proves a size past the pty's uint16 clamps
// instead of wrapping through zero.
func TestFloatSizeClampsToUint16(t *testing.T) {
	dir := t.TempDir()
	s, pane := floatServer(t, "floatclamp", dir, "sleep 5")
	cl, _ := floatClient(t, s)

	s.handleReq(cl, wire.Msg{
		Type: "req", Action: "float-open", ID: 1, Target: pane, Float: "f1",
		Cols: 65536, Rows: 70000,
	})
	f := s.floatFor("f1")
	if f == nil {
		t.Fatal("float f1 did not start")
	}
	t.Cleanup(func() { s.closeFloat("f1") })
	f.mu.Lock()
	cols, rows := f.cols, f.rows
	f.mu.Unlock()
	if cols != 65535 || rows != 65535 {
		t.Fatalf("float size = %dx%d, want 65535x65535 after the clamp", cols, rows)
	}

	s.resizeFloat("f1", 131072, 131073)
	f.mu.Lock()
	cols, rows = f.cols, f.rows
	f.mu.Unlock()
	if cols != 65535 || rows != 65535 {
		t.Fatalf("resized float = %dx%d, want 65535x65535 after the clamp", cols, rows)
	}
}

// TestConcurrentEnsurePaneSpawnsOnce proves callers racing ensurePane on one
// missing pane spawn one daemon and register one socket, not one each.
func TestConcurrentEnsurePaneSpawnsOnce(t *testing.T) {
	st := session.NewStateAt(t.TempDir(), "racepane")
	id := testutil.RootPane(st)
	s := railServer(st)

	sock := st.PaneSock(id)
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	var spawns atomic.Int32
	var sleeper *os.Process
	s.spawn = func(c *exec.Cmd) error {
		spawns.Add(1)
		// Stand in for the daemon: bind the socket it would listen on.
		ln, err := net.Listen("unix", sock)
		if err != nil {
			return err
		}
		go serveFakePane(ln)
		// ensurePane releases the started process, so give it a real one that
		// harms nothing and dies with the test.
		c.Path = "/bin/sleep"
		c.Args = []string{"sleep", "30"}
		if err := c.Start(); err != nil {
			return err
		}
		sleeper = c.Process
		return nil
	}
	t.Cleanup(func() {
		if sleeper != nil {
			_ = syscall.Kill(-sleeper.Pid, syscall.SIGKILL)
			_, _ = sleeper.Wait()
		}
	})

	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = s.ensurePane(id)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("ensurePane %d: %v", i, err)
		}
	}
	if got := spawns.Load(); got != 1 {
		t.Fatalf("spawned %d daemons, want 1", got)
	}
	s.mu.Lock()
	registered := s.panes[id] != nil
	s.mu.Unlock()
	if !registered {
		t.Fatal("no pane conn registered")
	}
}

// serveFakePane answers the cwd probes an adopt sends and holds the conn.
func serveFakePane(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = c.Close() }()
			for {
				m, err := wire.ReadMsg(c)
				if err != nil {
					return
				}
				if m.Type == "req" {
					_ = wire.WriteMsg(c, wire.Msg{Type: "resp", ID: m.ID, OK: true, Cwd: "/tmp"})
				}
			}
		}()
	}
}

// TestClosePaneRemovesPidFile proves the close path does not leave the pane's
// pid file in the session dir: the daemon's own cleanup removes only the
// socket, so the exit handler must remove both even on the path where the
// layout already forgot the pane.
func TestClosePaneRemovesPidFile(t *testing.T) {
	s := boxServer(t)
	a, gone := twoPaneSession(t, s)
	_ = a
	// A live-looking pane conn whose reads are drained, so closePane's kill
	// write does not block on the pipe.
	pa, pb := net.Pipe()
	t.Cleanup(func() { _ = pa.Close(); _ = pb.Close() })
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		for {
			_ = pa.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			if _, err := wire.ReadMsg(pa); err != nil {
				return
			}
		}
	}()
	s.panes[gone] = &paneConn{id: gone, c: pb, st: wire.Status{State: "working"}}

	sock := s.st.PaneSock(gone)
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{sock, sock + ".pid"} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// close-pane forgets the pane in the layout and the maps, then kills the
	// daemon. The daemon's exit reaches paneExited with the pane already gone.
	if err := s.closePane(gone); err != nil {
		t.Fatalf("closePane: %v", err)
	}
	s.paneExited(gone)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sock + ".pid"); errors.Is(err, os.ErrNotExist) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pid file %s still exists after the close", sock+".pid")
}
