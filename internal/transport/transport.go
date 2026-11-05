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
func Dial(name string) (*Conn, error) {
	nc, err := Ensure(name)
	if err != nil {
		return nil, err
	}
	return &Conn{Conn: nc}, nil
}

// Ensure returns a live connection to the session server, starting it if
// needed.
func Ensure(name string) (net.Conn, error) {
	sock := paths.ServerSock(name)
	if c, err := net.DialTimeout("unix", sock, 300*time.Millisecond); err == nil {
		return c, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(paths.SessionDir(name), 0o700); err != nil {
		return nil, err
	}
	logPath := filepath.Join(paths.SessionDir(name), "server.log")
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	defer lf.Close()
	cmd := exec.Command(exe, "__server", "-s", name)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout, cmd.Stderr = lf, lf
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	_ = cmd.Process.Release()
	for i := 0; i < 100; i++ {
		if c, err := net.DialTimeout("unix", sock, 100*time.Millisecond); err == nil {
			return c, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, fmt.Errorf("server for %q did not start", name)
}

// RPC sends one request and returns the first response.
func RPC(name string, m wire.Msg) (wire.Msg, error) {
	c, err := Ensure(name)
	if err != nil {
		return wire.Msg{}, err
	}
	defer c.Close()
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
func ListSessions() ([]SessionInfo, error) {
	entries, err := os.ReadDir(filepath.Join(paths.StateDir(), "sessions"))
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
		if c, err := net.DialTimeout("unix", paths.ServerSock(name), 100*time.Millisecond); err == nil {
			c.Close()
			info.Live = true
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// KillSession stops the daemon for a session and removes its socket.
func KillSession(name string) error {
	c, err := Ensure(name)
	if err != nil {
		return err
	}
	c.Close()
	data, err := os.ReadFile(filepath.Join(paths.SessionDir(name), "server.pid"))
	if err != nil {
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return err
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := p.Signal(syscall.SIGTERM); err != nil {
		return err
	}
	_ = os.Remove(paths.ServerSock(name))
	return nil
}
