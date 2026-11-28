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

// DialWithLayout is Dial with a layout file for a session that is not running
// yet. An empty path is Dial.
func DialWithLayout(name, layoutPath string) (*Conn, error) {
	return DialAtWithLayout(paths.StateDir(), name, layoutPath)
}

// DialAt is Dial with an explicit state root instead of paths.StateDir().
func DialAt(root, name string) (*Conn, error) {
	return DialAtWithLayout(root, name, "")
}

// DialAtWithLayout is DialAt with a layout file for a session that is not
// running yet.
func DialAtWithLayout(root, name, layoutPath string) (*Conn, error) {
	nc, err := EnsureAtWithLayout(root, name, layoutPath)
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
	return EnsureAtWithLayout(root, name, "")
}

// EnsureAtWithLayout is EnsureAt with a layout file. The file reaches the
// server only when this call spawns one: a session already answering on its
// socket attaches as it stands, and a layout never rearranges live state.
func EnsureAtWithLayout(root, name, layoutPath string) (net.Conn, error) {
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
	// A session whose server is leaving still holds its flock for a moment,
	// and its socket is already gone. Spawning into that window starts a
	// server that dies on the lock and reports a locked session instead of the
	// server that is coming up, so wait for the lock to free first: this is the
	// path a client takes when it reconnects across a restart.
	if err := waitUnlocked(root, name, spawnWait); err != nil {
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
	args := []string{"__server", "-s", name}
	if layoutPath != "" {
		args = append(args, "-layout", layoutPath)
	}
	cmd := exec.Command(exe, args...)
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

// spawnWait bounds how long Ensure waits for a session lock to come free. It
// covers a restart, where the successor and the client both race the server
// that is leaving, and a boot in progress, which a second client sees as a lock
// with no socket to dial. It matches the read deadline RPCAt gives a live but
// silent server, so an Ensure never buys more patience than a request does.
const spawnWait = 10 * time.Second

// waitUnlocked waits for the session lock to come free. A spawn that runs
// while another process holds it starts a server that exits on the lock, and
// the caller's error would name a locked session instead of the server that is
// coming up.
func waitUnlocked(root, name string, d time.Duration) error {
	deadline := time.Now().Add(d)
	for {
		held, err := paths.SessionLockHeld(root, name)
		if err != nil {
			return err
		}
		if !held {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("session %q is locked: a server is starting or stopping", name)
		}
		time.Sleep(25 * time.Millisecond)
	}
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

// RPCWithLayout is RPC with a layout file for a session that is not running
// yet. An empty path is RPC.
func RPCWithLayout(name, layoutPath string, m wire.Msg) (wire.Msg, error) {
	return RPCAtWithLayout(paths.StateDir(), name, layoutPath, m)
}

// RPCAt is RPC with an explicit state root instead of paths.StateDir().
func RPCAt(root, name string, m wire.Msg) (wire.Msg, error) {
	return RPCAtWithLayout(root, name, "", m)
}

// RPCAtWithLayout is RPCAt with a layout file for a session that is not
// running yet.
func RPCAtWithLayout(root, name, layoutPath string, m wire.Msg) (wire.Msg, error) {
	c, err := EnsureAtWithLayout(root, name, layoutPath)
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

// RestartSession stops a session's server in order and starts the one that
// follows. The panes never move, so the successor adopts them with their
// screens and their scrollback: a restart is where a new binary takes over a
// live session.
func RestartSession(name string) error { return RestartSessionAt(paths.StateDir(), name) }

// RestartSessionAt is RestartSession with an explicit state root instead of
// paths.StateDir(). A session with no server is restarted by the dial at the
// end: Ensure spawns it, and the first start of a dead session is a restart.
func RestartSessionAt(root, name string) error {
	if !paths.ValidSessionName(name) {
		return fmt.Errorf("invalid session name %q", name)
	}
	if c, err := net.DialTimeout("unix", paths.ServerSockAt(root, name), 300*time.Millisecond); err == nil {
		_ = c.Close()
		if _, err := RPCAt(root, name, wire.Msg{Action: "restart"}); err != nil {
			return fmt.Errorf("restart %q: %w", name, err)
		}
	}
	// Hold the caller until the successor answers, so a script that restarts
	// and then asks the session a question never races the server it asked for.
	nc, err := EnsureAt(root, name)
	if err != nil {
		return fmt.Errorf("restart %q: %w", name, err)
	}
	return nc.Close()
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
// paths.StateDir(). It holds server.lock across the reap and the removal, so no
// other process can start or purge the session while its directory is going.
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
	// The pid file is a hint, never the decision. A server that has not written
	// it yet, or whose file was removed, still holds the lock; stopServer cannot
	// see it and signals nothing.
	stopServer(dir)
	lock, err := lockSession(root, name, lockWait)
	if errors.Is(err, paths.ErrSessionLocked) {
		// The lock is still held after the stop attempt, so the server either
		// ignored SIGTERM or was never named by the pid file. Reset's contract is
		// to destroy the session, so it escalates: it finds the dm server by the
		// lock it holds and SIGKILLs it, and a second stop attempt covers a pid
		// file that names a server which started after the first. A holder that
		// is not this session's dm server answers to no signal here, and the
		// retry refuses below.
		stopLockHolders(dir, name)
		stopServer(dir)
		lock, err = lockSession(root, name, lockWait)
	}
	if err != nil {
		if errors.Is(err, paths.ErrSessionLocked) {
			return fmt.Errorf("session %q is locked by a live process; not reset", name)
		}
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no session %q", name)
		}
		return err
	}
	// The lock stays held across the reap and the removal. The lock file is
	// inside the directory, so the removal takes it in the same step: no spawn
	// can create a second lock inode while the first is still there, and the
	// Release that follows closes an fd on an inode nobody can reach.
	defer func() { _ = lock.Release() }()
	reapPanes(dir)
	return os.RemoveAll(dir)
}

// lockWait bounds how long reset waits for a session lock to come free after it
// has stopped the server. The wait exists for the kernel, not the server: a
// SIGKILLed multithreaded process can read as dead in procfs while one of its
// threads still holds the lock's open file description, and the lock is what
// decides.
const lockWait = 500 * time.Millisecond

// lockSession takes the session lock, polling up to d so a process that has
// just been killed has time to drop its last reference to the lock. A single
// non-blocking attempt would refuse a session reset has already ended.
func lockSession(root, name string, d time.Duration) (*paths.SessionLock, error) {
	deadline := time.Now().Add(d)
	for {
		lock, err := paths.LockSession(root, name)
		if err == nil || !errors.Is(err, paths.ErrSessionLocked) || time.Now().After(deadline) {
			return lock, err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// stopLockHolders SIGKILLs every dm server that holds this session's lock. It
// reaches the server the pid file cannot name: one that has not written its pid
// file yet, or whose file was removed. A holder that is not this session's dm
// server is left alone, so a foreign flock still stops reset cold at the lock.
func stopLockHolders(dir, name string) {
	for _, pid := range lockHolderPIDs(dir) {
		if !isDMProcess(pid, "__server") || !serverForSession(pid, name) {
			continue
		}
		_ = syscall.Kill(pid, syscall.SIGKILL)
		waitGone(pid, time.Second)
	}
}

// lockHolderPIDs returns the pids that have the session's lock file open. It
// reads procfs fd tables rather than /proc/locks so a holder whose lock file
// was already unlinked is still named: the fd link carries the path it was
// opened under, with a " (deleted)" suffix once the inode is gone.
func lockHolderPIDs(dir string) []int {
	lock, err := filepath.Abs(filepath.Join(dir, "server.lock"))
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		fds, err := os.ReadDir(filepath.Join("/proc", e.Name(), "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join("/proc", e.Name(), "fd", fd.Name()))
			if err != nil {
				continue
			}
			if target == lock || strings.HasPrefix(target, lock+" ") {
				out = append(out, pid)
				break
			}
		}
	}
	return out
}

// serverForSession reports whether pid's command line names this session. A dm
// server is "__server -s <name>", and the name is what keeps one session's reset
// from signalling another's server.
func serverForSession(pid int, name string) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return false
	}
	args := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-s" {
			return args[i+1] == name
		}
	}
	return false
}

// RemoveSessionAt removes a dead session's directory under the session lock. A
// purge that cannot prove no server is live removes nothing: it returns
// paths.ErrSessionLocked and leaves the directory untouched.
func RemoveSessionAt(root, name string) error {
	if !paths.ValidSessionName(name) {
		return fmt.Errorf("invalid session name %q", name)
	}
	lock, err := paths.LockSession(root, name)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()
	return os.RemoveAll(paths.SessionDirAt(root, name))
}

// stopServer ends the server the pid file names and waits for the process to go.
// A missing or foreign pid file is not proof that no server is live: a server
// that has not written its pid file yet, or whose file was removed, is invisible
// here and this returns without signalling. Callers decide liveness from the
// session lock, not from this.
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
