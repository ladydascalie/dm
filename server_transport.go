package main

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
	"syscall"
	"time"

	"dm/internal/paths"
	"dm/internal/wire"
)

// ensureServer returns a live connection to the session server, starting it if
// needed. The returned bool reports whether a new daemon was spawned.
func ensureServer(name string) (net.Conn, error) {
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

func rpc(name string, m wire.Msg) (wire.Msg, error) {
	c, err := ensureServer(name)
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

type SessionInfo struct {
	Name string `json:"name"`
	Live bool   `json:"live"`
}

func listSessionInfo() ([]SessionInfo, error) {
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

func killSession(name string) error {
	c, err := ensureServer(name)
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
