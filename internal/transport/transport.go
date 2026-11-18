// Package transport is the client side of the session socket: it dials the
// daemon, spawns it when it is missing, and speaks the wire envelope.
package transport

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"dm/internal/paths"
	"dm/internal/wire"
)

// Conn wraps the server socket with a write mutex.
type Conn struct {
	net.Conn
	mu sync.Mutex
}

// Send writes one message under the write mutex.
func (c *Conn) Send(m wire.Msg) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return wire.WriteMsg(c.Conn, m)
}

// Dial returns a live connection to the session server, starting it if needed.
func Dial(name string) (*Conn, error) { return DialAt(paths.StateDir(), name) }

// DialAt is Dial with an explicit state root instead of paths.StateDir().
func DialAt(root, name string) (*Conn, error) {
	nc, err := EnsureAt(root, name)
	if err != nil {
		return nil, err
	}
	return &Conn{Conn: nc}, nil
}

// Ensure returns a live connection to the session server, starting it if
// needed.
func Ensure(name string) (net.Conn, error) { return EnsureAt(paths.StateDir(), name) }

// EnsureAt is Ensure with an explicit state root instead of paths.StateDir().
func EnsureAt(root, name string) (net.Conn, error) {
	if !paths.ValidSessionName(name) {
		return nil, fmt.Errorf("invalid session name %q", name)
	}
	sock := paths.ServerSockAt(root, name)
	if c, err := net.DialTimeout("unix", sock, 300*time.Millisecond); err == nil {
		return c, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(paths.SessionDirAt(root, name), 0o700); err != nil {
		return nil, err
	}
	logPath := filepath.Join(paths.SessionDirAt(root, name), "server.log")
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lf.Close() }()
	cmd := exec.Command(exe, "__server", "-s", name)
	// The spawned server resolves its own paths from DM_STATE. Pin the
	// child's copy to root so an explicit root survives the spawn.
	env := make([]string, 0, len(os.Environ())+1)
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "DM_STATE=") {
			env = append(env, e)
		}
	}
	cmd.Env = append(env, "DM_STATE="+root)
	detachProcess(cmd)
	cmd.Stdout, cmd.Stderr = lf, lf
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// Wait reaps the child in the background and reports an early exit, so a
	// server that dies during startup fails the dial loop at once instead of
	// burning the whole retry budget on a process that is already gone. On a
	// successful dial the goroutine stays and reaps the server when it exits.
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	for i := 0; i < 100; i++ {
		if c, err := net.DialTimeout("unix", sock, 100*time.Millisecond); err == nil {
			return c, nil
		}
		select {
		case err := <-exited:
			return nil, fmt.Errorf("server for %q exited: %v%s", name, err, logTail(logPath))
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, fmt.Errorf("server for %q did not start%s", name, logTail(logPath))
}

// logTail returns the last lines of the server log for an error message. A
// server that fails during startup names the reason there, and a generic
// "did not start" is useless without it. A missing or empty log adds nothing.
func logTail(path string) string {
	const max = 2048
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return ""
	}
	start := int64(0)
	if info.Size() > max {
		start = info.Size() - max
	}
	b := make([]byte, info.Size()-start)
	if _, err := f.ReadAt(b, start); err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return ": server.log: " + strings.Join(lines, " | ")
}

// RPC sends one request and returns the first response.
func RPC(name string, m wire.Msg) (wire.Msg, error) { return RPCAt(paths.StateDir(), name, m) }

// RPCAt is RPC with an explicit state root instead of paths.StateDir().
func RPCAt(root, name string, m wire.Msg) (wire.Msg, error) {
	c, err := EnsureAt(root, name)
	if err != nil {
		return wire.Msg{}, err
	}
	defer func() { _ = c.Close() }()
	m.Type = "req"
	m.ID = 1
	if err := wire.WriteMsg(c, m); err != nil {
		return wire.Msg{}, err
	}
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		got, err := wire.ReadMsg(c)
		if err != nil {
			return wire.Msg{}, err
		}
		if got.Type == "resp" {
			return got, nil
		}
	}
}

// SessionInfo is one session on disk and whether its daemon answers.
type SessionInfo struct {
	Name string `json:"name"`
	Live bool   `json:"live"`
}

// ListSessions returns every session on disk, sorted by name.
func ListSessions() ([]SessionInfo, error) { return ListSessionsAt(paths.StateDir()) }

// ListSessionsAt is ListSessions with an explicit state root instead of
// paths.StateDir().
func ListSessionsAt(root string) ([]SessionInfo, error) {
	entries, err := os.ReadDir(filepath.Join(root, "sessions"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []SessionInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		info := SessionInfo{Name: name}
		if c, err := net.DialTimeout("unix", paths.ServerSockAt(root, name), 100*time.Millisecond); err == nil {
			_ = c.Close()
			info.Live = true
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// KillSession stops the daemon for a session and removes its socket.
func KillSession(name string) error { return KillSessionAt(paths.StateDir(), name) }

// KillSessionAt is KillSession with an explicit state root instead of
// paths.StateDir(). It never spawns a server: killing a stopped session must
// not boot it, so the pid file decides and a missing or foreign pid fails.
func KillSessionAt(root, name string) error {
	if !paths.ValidSessionName(name) {
		return fmt.Errorf("invalid session name %q", name)
	}
	pid, ok := readPID(filepath.Join(paths.SessionDirAt(root, name), "server.pid"))
	if !ok || !isDMProcess(pid, "__server") {
		return fmt.Errorf("session %q has no server pid", name)
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := p.Signal(syscall.SIGTERM); err != nil {
		return err
	}
	_ = os.Remove(paths.ServerSockAt(root, name))
	return nil
}

// ResetSession discards a session for a full restart: it stops the server,
// reaps every pane and its shell, and removes the session directory. Kill
// leaves panes alive and purge skips them, so neither can do this.
func ResetSession(name string) error { return ResetSessionAt(paths.StateDir(), name) }

// ResetSessionAt is ResetSession with an explicit state root instead of
// paths.StateDir().
func ResetSessionAt(root, name string) error {
	if !paths.ValidSessionName(name) {
		return fmt.Errorf("invalid session name %q", name)
	}
	dir := paths.SessionDirAt(root, name)
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no session %q", name)
		}
		return err
	}
	stopServer(dir)
	reapPanes(dir)
	return os.RemoveAll(dir)
}

// stopServer ends the session's server and waits for the process to go.
func stopServer(dir string) {
	pid, ok := readPID(filepath.Join(dir, "server.pid"))
	if !ok {
		return
	}
	if !isDMProcess(pid, "__server") {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	if waitGone(pid, 2*time.Second) {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	waitGone(pid, time.Second)
}

// reapPanes ends every pane daemon and the shell it owns. The shell is a
// session leader, so killing its process group takes its children too. The
// daemon is killed last, after its children are read, because a dead daemon
// cannot name them. This works on a daemon built before reset existed: it
// needs no cooperation from the pane.
func reapPanes(dir string) {
	entries, err := os.ReadDir(filepath.Join(dir, "panes"))
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sock.pid") {
			continue
		}
		pid, ok := readPID(filepath.Join(dir, "panes", e.Name()))
		if !ok {
			continue
		}
		if !isDMProcess(pid, "__pane") {
			continue
		}
		for _, child := range childPIDs(pid) {
			_ = syscall.Kill(-child, syscall.SIGKILL)
			_ = syscall.Kill(child, syscall.SIGKILL)
		}
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

// isDMProcess reports whether pid still runs the named dm subcommand. It
// reads procfs argv and matches argv[1] exactly, plus argv[0] against the
// running dm binary, so a stale pid file naming a reused pid, or a process
// whose arguments merely mention the subcommand, never matches.
func isDMProcess(pid int, subcommand string) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return false
	}
	args := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
	if len(args) < 2 || args[1] != subcommand {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return filepath.Base(args[0]) == filepath.Base(exe)
}

// childPIDs returns the direct children of pid from procfs. A missing file
// means the process is gone or has no children.
func childPIDs(pid int) []int {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
	if err != nil {
		return nil
	}
	var out []int
	for _, f := range strings.Fields(string(b)) {
		if n, err := strconv.Atoi(f); err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}

// readPID reads a pid file. A missing or malformed file is not a pid.
func readPID(path string) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// processAlive reports whether pid names a running process. A process owned by
// another user answers EPERM, which still means it is alive. A zombie has
// exited and only waits to be reaped, so it is not alive.
func processAlive(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	// The state is the field after the parenthesised command name.
	if i := strings.LastIndexByte(string(b), ')'); i >= 0 && i+2 < len(b) {
		return b[i+2] != 'Z'
	}
	return true
}

// waitGone waits up to d for pid to exit and reports whether it did.
func waitGone(pid int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if !processAlive(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}
