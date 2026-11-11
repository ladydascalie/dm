package transport

import (
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"dm/internal/paths"
	"dm/internal/wire"
)

// TestMain lets the test binary play the dm server and pane daemons: Ensure
// spawns os.Executable() with "__server" as its first argument, and the tests
// use that to exercise the real spawn path.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "__server":
			if hasFlag(os.Args, "--hard") {
				// A server that ignores SIGTERM forces stopServer down its
				// SIGKILL escalation.
				term := make(chan os.Signal, 1)
				signal.Notify(term, syscall.SIGTERM)
				go func() {
					for range term {
					}
				}()
			}
			fakeServer(flagValue(os.Args, "-s"))
		case "__pane":
			// Hold a child of our own so a reaper has a grandchild to take.
			child := exec.Command("sleep", "60")
			if err := child.Start(); err == nil {
				defer func() { _ = child.Process.Kill() }()
			}
			time.Sleep(time.Minute)
		}
	}
	os.Exit(m.Run())
}

// hasFlag reports whether name appears in args as a bare flag.
func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

// flagValue returns the value that follows name in args, or "".
func flagValue(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// fakeServer is the stand-in the spawned test binary runs: it listens on the
// session socket, writes the pid file the real server owns, echoes each
// request as a response, and exits when the socket or the process goes away.
func fakeServer(name string) {
	sock := paths.ServerSock(name)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		os.Exit(1)
	}
	_ = os.WriteFile(filepath.Join(paths.SessionDir(name), "server.pid"),
		[]byte(strconv.Itoa(os.Getpid())), 0o600)
	go func() {
		for {
			if _, err := os.Stat(sock); err != nil {
				_ = ln.Close()
				os.Exit(0)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			os.Exit(0)
		}
		go serve(c)
	}
}

// serve answers one connection: every req gets a status line and then the
// echoed response, so callers can prove a non-resp frame is skipped.
func serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	for {
		got, err := wire.ReadMsg(c)
		if err != nil {
			return
		}
		if got.Type != "req" {
			continue
		}
		if err := wire.WriteMsg(c, wire.Msg{Type: "status"}); err != nil {
			return
		}
		reply := got
		reply.Type = "resp"
		if err := wire.WriteMsg(c, reply); err != nil {
			return
		}
	}
}

// listenSession creates a live session socket under DM_STATE and returns the
// listener.
func listenSession(t *testing.T, name string) net.Listener {
	t.Helper()
	sock := paths.ServerSock(name)
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

// awaitSubcommand waits until pid's cmdline names the subcommand: a freshly
// started process may still be coming up when the test looks at it.
func awaitSubcommand(t *testing.T, pid int, subcommand string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !isDMProcess(pid, subcommand) {
		if !processAlive(pid) {
			t.Fatalf("pid %d exited before running %s", pid, subcommand)
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d never ran %s", pid, subcommand)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// dropSession removes a session's directory at cleanup, which is how the
// fake server notices it should exit: no socket, no daemon.
func dropSession(t *testing.T, name string) {
	t.Cleanup(func() { _ = os.RemoveAll(paths.SessionDir(name)) })
}

func TestSendReadsBackWhatThePeerFramed(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	ln := listenSession(t, "x")
	c, err := net.Dial("unix", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn := &Conn{Conn: c}
	server, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]wire.Msg{
		"empty":       {},
		"keys":        {Type: "keys", Keys: []byte{0x1b, 0x5b, 0x41}},
		"two-meg":     {Type: "bytes", Data: make([]byte, 2<<20)},
		"vtsnap-utf8": {Type: "bytes", Data: []byte("héllo → 世界"), Cols: 80, Rows: 24},
	}
	// The peer reads in its own goroutine: a payload larger than the socket
	// buffer blocks Send until someone drains the other end.
	type received struct {
		msg wire.Msg
		err error
	}
	frames := make(chan received, len(cases))
	go func() {
		for range cases {
			m, err := wire.ReadMsg(server)
			frames <- received{m, err}
		}
	}()
	for name, msg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := conn.Send(msg); err != nil {
				t.Fatalf("Send: %v", err)
			}
			r := <-frames
			got, err := r.msg, r.err
			if err != nil {
				t.Fatalf("ReadMsg: %v", err)
			}
			if string(got.Data) != string(msg.Data) || got.Type != msg.Type ||
				got.Cols != msg.Cols || got.Rows != msg.Rows ||
				string(got.Keys) != string(msg.Keys) {
				t.Fatalf("round trip changed the message: got %+v want %+v", got, msg)
			}
		})
	}
}

// TestSendSerialisesWriters proves the mutex keeps concurrent Send calls from
// interleaving their frames: every frame the peer reads must still parse.
func TestSendSerialisesWriters(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	ln := listenSession(t, "x")
	c, err := net.Dial("unix", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn := &Conn{Conn: c}
	server, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	const writers, each = 8, 25
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if err := conn.Send(wire.Msg{Type: "keys", Msg: strconv.Itoa(w)}); err != nil {
					t.Errorf("Send: %v", err)
					return
				}
			}
		}(w)
	}
	got := 0
	for got < writers*each {
		m, err := wire.ReadMsg(server)
		if err != nil {
			t.Fatalf("frame %d: %v", got, err)
		}
		if _, err := strconv.Atoi(m.Msg); err != nil {
			t.Fatalf("frame %d arrived interleaved: %q", got, m.Msg)
		}
		got++
	}
	wg.Wait()
}

// TestSendOnClosedConn reports the write instead of swallowing it.
func TestSendOnClosedConn(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	conn := &Conn{Conn: a}
	_ = b.Close()
	if err := conn.Send(wire.Msg{Type: "keys"}); err == nil {
		t.Fatal("Send on a closed conn returned nil")
	}
}

func TestDialConnectsToALiveSocket(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	listenSession(t, "live")
	conn, err := Dial("live")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.Send(wire.Msg{Type: "keys", Msg: "hi"}); err != nil {
		t.Fatalf("Send over the dialed conn: %v", err)
	}
}

// TestEnsureSpawnsAndReuses covers the full cold start: no socket, so Ensure
// spawns the executable, and the second call reuses the socket it created.
func TestEnsureSpawnsAndReuses(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	dropSession(t, "cold")
	c1, err := Ensure("cold")
	if err != nil {
		t.Fatalf("Ensure cold start: %v", err)
	}
	defer func() { _ = c1.Close() }()
	if _, err := os.Stat(paths.ServerSock("cold")); err != nil {
		t.Fatalf("spawn did not create the socket: %v", err)
	}
	c2, err := Ensure("cold")
	if err != nil {
		t.Fatalf("Ensure warm: %v", err)
	}
	defer func() { _ = c2.Close() }()
}

func TestRPCSkipsInterleavedFrames(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	dropSession(t, "rpc")
	got, err := RPC("rpc", wire.Msg{Action: "ping"})
	if err != nil {
		t.Fatalf("RPC: %v", err)
	}
	if got.Type != "resp" || got.Action != "ping" {
		t.Fatalf("RPC returned %+v, want the echoed resp", got)
	}
}

// TestRPCFailsWhenThePeerNeverReads pins the write-failure return: a server
// that accepts and then closes without reading makes the 4 MiB request block
// in the socket buffer until the close turns it into EPIPE.
func TestRPCFailsWhenThePeerNeverReads(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	ln := listenSession(t, "stalled")
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				time.Sleep(100 * time.Millisecond)
				_ = c.Close()
			}()
		}
	}()
	if _, err := RPC("stalled", wire.Msg{Data: make([]byte, 4<<20)}); err == nil {
		t.Fatal("RPC returned a response from a server that never read")
	}
}

// TestEnsureSurfacesSetupErrors pins the two early exits before the spawn:
// a session path that cannot be created, and a log file that cannot be
// opened.
func TestEnsureSurfacesSetupErrors(t *testing.T) {
	cases := map[string]func(t *testing.T) string{
		"session path is a file": func(t *testing.T) string {
			if err := os.MkdirAll(filepath.Dir(paths.SessionDir("busy")), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(paths.SessionDir("busy"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			return "busy"
		},
		"server log is a directory": func(t *testing.T) string {
			if err := os.MkdirAll(filepath.Join(paths.SessionDir("badlog"), "server.log"), 0o700); err != nil {
				t.Fatal(err)
			}
			return "badlog"
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("DM_STATE", t.TempDir())
			session := setup(t)
			if _, err := Ensure(session); err == nil {
				t.Fatal("Ensure succeeded where setup made it impossible")
			}
			if _, err := Dial(session); err == nil {
				t.Fatal("Dial succeeded where setup made it impossible")
			}
			if _, err := RPC(session, wire.Msg{Action: "ping"}); err == nil {
				t.Fatal("RPC succeeded where setup made it impossible")
			}
			if err := KillSession(session); err == nil {
				t.Fatal("KillSession succeeded where setup made it impossible")
			}
		})
	}
}

func TestListSessions(t *testing.T) {
	cases := []struct {
		name string
		live []string
		want []SessionInfo
	}{
		{"no state dir", nil, nil},
		{"sorted with a live and a dead session", []string{"a"}, []SessionInfo{
			{Name: "a", Live: true}, {Name: "b", Live: false},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DM_STATE", t.TempDir())
			if tc.want != nil {
				for _, n := range append(tc.live, "b") {
					if err := os.MkdirAll(paths.SessionDir(n), 0o700); err != nil {
						t.Fatal(err)
					}
				}
				for _, n := range tc.live {
					listenSession(t, n)
				}
				if err := os.WriteFile(filepath.Join(paths.StateDir(), "sessions", "z"),
					[]byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := ListSessions()
			if err != nil {
				t.Fatalf("ListSessions: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("ListSessions = %+v, want %+v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("ListSessions[%d] = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestKillSessionStopsTheServer(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	if _, err := Ensure("k"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	pid, ok := readPID(filepath.Join(paths.SessionDir("k"), "server.pid"))
	if !ok {
		t.Fatal("spawned server left no pid file")
	}
	dropSession(t, "k")
	if err := KillSession("k"); err != nil {
		t.Fatalf("KillSession: %v", err)
	}
	if _, err := os.Stat(paths.ServerSock("k")); !os.IsNotExist(err) {
		t.Fatalf("socket survived the kill: %v", err)
	}
	if !waitGone(pid, 2*time.Second) {
		t.Fatal("server survived SIGTERM")
	}
}

// TestKillSessionWithoutPidFile covers a live socket whose server never
// wrote its pid: the kill must fail loudly, not silently do nothing.
func TestKillSessionWithoutPidFile(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	listenSession(t, "npid")
	if err := KillSession("npid"); err == nil || !strings.Contains(err.Error(), "no server pid") {
		t.Fatalf("KillSession = %v, want a no-server-pid error", err)
	}
}

func TestResetSessionStopsEverythingAndRemovesTheDir(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	dropSession(t, "reset")
	if _, err := Ensure("reset"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pane := exec.Command(exe, "__pane")
	pane.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := pane.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pane.Process.Kill() })
	awaitSubcommand(t, pane.Process.Pid, "__pane")
	panes := filepath.Join(paths.SessionDir("reset"), "panes")
	if err := os.MkdirAll(panes, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(panes, "w1p1.sock.pid"),
		[]byte(strconv.Itoa(pane.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	var grandchildren []int
	deadline := time.Now().Add(2 * time.Second)
	for len(grandchildren) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the pane spawned no child to reap")
		}
		grandchildren = childPIDs(pane.Process.Pid)
		time.Sleep(10 * time.Millisecond)
	}
	if err := ResetSession("reset"); err != nil {
		t.Fatalf("ResetSession: %v", err)
	}
	if _, err := os.Stat(paths.SessionDir("reset")); !os.IsNotExist(err) {
		t.Fatalf("session directory survived the reset: %v", err)
	}
	if pid, ok := readPID(filepath.Join(paths.SessionDir("reset"), "server.pid")); ok && processAlive(pid) {
		t.Fatal("the server survived the reset")
	}
	if !waitGone(pane.Process.Pid, time.Second) {
		t.Fatal("the pane survived the reset")
	}
	for _, grand := range grandchildren {
		if !waitGone(grand, time.Second) {
			t.Fatalf("the pane's child %d survived the reset", grand)
		}
	}
	if err := ResetSession("nosuch"); err == nil || !strings.Contains(err.Error(), "no session") {
		t.Fatalf("ResetSession on a missing session = %v, want a no-session error", err)
	}
}

func TestStopServerKillsItsOwnAndSparesForeignPids(t *testing.T) {
	sleep := exec.Command("sleep", "30")
	if err := sleep.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sleep.Process.Kill() })

	dir := filepath.Join(t.TempDir(), "s")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A stale or reused pid must never be signalled.
	if err := os.WriteFile(filepath.Join(dir, "server.pid"),
		[]byte(strconv.Itoa(sleep.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	stopServer(dir)
	if !processAlive(sleep.Process.Pid) {
		t.Fatal("stopServer killed a process that is not a dm server")
	}

	// The real thing: a pid whose cmdline runs the server subcommand.
	t.Setenv("DM_STATE", t.TempDir())
	if _, err := Ensure("stop"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	dropSession(t, "stop")
	stopServer(paths.SessionDir("stop"))
	pid, ok := readPID(filepath.Join(paths.SessionDir("stop"), "server.pid"))
	if !ok {
		t.Fatal("no pid file to check")
	}
	if processAlive(pid) {
		t.Fatal("stopServer left the dm server running")
	}
}

// TestStopServerEscalatesToSigkill pins the hard path: a server that
// ignores SIGTERM is killed with SIGKILL within the escalation window.
func TestStopServerEscalatesToSigkill(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	dropSession(t, "hard")
	if err := os.MkdirAll(paths.SessionDir("hard"), 0o700); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	hard := exec.Command(exe, "__server", "-s", "hard", "--hard")
	hard.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := hard.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hard.Process.Kill() })
	// The pid file appears after Listen, and the child registers its SIGTERM
	// handler before Listen, so a pid file means SIGTERM will be ignored.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := readPID(filepath.Join(paths.SessionDir("hard"), "server.pid")); ok {
			break
		}
		if !processAlive(hard.Process.Pid) {
			t.Fatal("the hard server exited before it could listen")
		}
		if time.Now().After(deadline) {
			t.Fatal("the hard server never wrote its pid file")
		}
		time.Sleep(10 * time.Millisecond)
	}
	stopServer(paths.SessionDir("hard"))
	_ = hard.Wait()
	if processAlive(hard.Process.Pid) {
		t.Fatal("stopServer left a server that ignored SIGTERM")
	}
}

func TestReapPanesSparesForeignProcesses(t *testing.T) {
	sleep := exec.Command("sleep", "30")
	if err := sleep.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sleep.Process.Kill() })

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pane := exec.Command(exe, "__pane")
	pane.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := pane.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pane.Process.Kill() })
	awaitSubcommand(t, pane.Process.Pid, "__pane")

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "panes"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, pid := range map[string]int{
		"p1.sock.pid": sleep.Process.Pid,
		"p2.sock.pid": pane.Process.Pid,
	} {
		content := strconv.Itoa(pid)
		if pid == 0 {
			content = "not-a-pid"
		}
		if err := os.WriteFile(filepath.Join(dir, "panes", name),
			[]byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A file without the .sock.pid suffix is not a pane record, even when it
	// carries a dm pane's pid.
	if err := os.WriteFile(filepath.Join(dir, "panes", "p4.sock"),
		[]byte(strconv.Itoa(pane.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	reapPanes(t.TempDir())
	reapPanes(dir)
	if !waitGone(pane.Process.Pid, time.Second) {
		t.Fatal("reapPanes left the dm pane running")
	}
	// The pane was SIGKILLed; reap it so the pid cannot be reused under us.
	_ = pane.Wait()
	if !processAlive(sleep.Process.Pid) {
		t.Fatal("reapPanes killed a process that is not a dm pane")
	}
}

// TestReadMsgRejectsBrokenFrames pins what a client sees when the peer sends
// a truncated or malformed frame. wire owns ReadMsg; these are the errors a
// transport caller meets on the session socket. If internal/wire grows its
// own frame tests, move this table there.
func TestReadMsgRejectsBrokenFrames(t *testing.T) {
	cases := map[string]struct {
		write func(w net.Conn)
		want  string
	}{
		"truncated header": {func(w net.Conn) {
			_, _ = w.Write([]byte{0, 0})
		}, "unexpected EOF"},
		"zero length": {func(w net.Conn) {
			_, _ = w.Write([]byte{0, 0, 0, 0})
		}, "bad frame length 0"},
		"over max frame": {func(w net.Conn) {
			_, _ = w.Write([]byte{0xff, 0xff, 0xff, 0xff})
		}, "bad frame length 4294967295"},
		"truncated payload": {func(w net.Conn) {
			_, _ = w.Write([]byte{0, 0, 0, 8, 'x'})
		}, "unexpected EOF"},
		"not json": {func(w net.Conn) {
			_, _ = w.Write([]byte{0, 0, 0, 4, 'n', 'o', 'p', 'e'})
		}, "bad json"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			client, server := net.Pipe()
			go func() {
				tc.write(server)
				_ = server.Close()
			}()
			_, err := wire.ReadMsg(client)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ReadMsg error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestReadPID(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]struct {
		content string
		pid     int
		ok      bool
	}{
		"missing file":  {content: "", pid: 0, ok: false},
		"plain number":  {content: "4242", pid: 4242, ok: true},
		"padded number": {content: " 4242\n", pid: 4242, ok: true},
		"not a number":  {content: "abc", pid: 0, ok: false},
		"zero pid":      {content: "0", pid: 0, ok: false},
		"negative pid":  {content: "-7", pid: 0, ok: false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name)
			if tc.content != "" {
				if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			pid, ok := readPID(path)
			if pid != tc.pid || ok != tc.ok {
				t.Fatalf("readPID(%q) = %d, %v, want %d, %v", tc.content, pid, ok, tc.pid, tc.ok)
			}
		})
	}
}

func TestProcessAliveAndWaitGone(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("the test process reads as dead")
	}
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	if processAlive(dead.Process.Pid) {
		t.Fatal("a reaped pid reads as alive")
	}
	// A killed but unreaped child is a zombie: it answers kill(0) and its
	// stat says Z, so it is not alive.
	zombie := exec.Command("sleep", "30")
	if err := zombie.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = zombie.Process.Kill() })
	if err := zombie.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if !waitGone(zombie.Process.Pid, time.Second) {
		t.Fatal("the child never became a zombie")
	}
	if processAlive(zombie.Process.Pid) {
		t.Fatal("a zombie reads as alive")
	}
	_ = zombie.Wait()
	if !waitGone(dead.Process.Pid, time.Second) {
		t.Fatal("waitGone could not confirm an already dead pid")
	}
	if waitGone(os.Getpid(), 30*time.Millisecond) {
		t.Fatal("waitGone reported the live test process as gone")
	}
}

func TestIsDMProcess(t *testing.T) {
	if !isDMProcess(os.Getpid(), "transport.test") {
		t.Fatal("the test binary's own pid was not recognised")
	}
	if isDMProcess(os.Getpid(), "__server") {
		t.Fatal("the plain test binary matched the server subcommand")
	}
	if isDMProcess(0, "anything") {
		t.Fatal("a pid with no procfs entry matched")
	}
}
