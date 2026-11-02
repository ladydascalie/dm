package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

// paneProc is one pane daemon: it owns the PTY, the VT screen, the status and
// the scroll ring. The session server comes and goes; this process does not.
type paneProc struct {
	id      string
	session string
	sock    string
	shell   string
	cwd     string

	ptmx *os.File
	cmd  *exec.Cmd

	mu   sync.Mutex
	vt   vt10x.Terminal
	ring []byte
	st   Status
	subs map[*subConn]bool
	dead bool
	mode vt10x.ModeFlag
}

// modeInfo reports the two modes the client must respect: alternate screen
// (no terminal scrollback) and mouse reporting (the wheel belongs to the app).
func modeInfo(m vt10x.ModeFlag) (alt, mouse bool) {
	alt = m&vt10x.ModeAltScreen != 0
	mouse = m&(vt10x.ModeMouseMask|vt10x.ModeMouseSgr) != 0
	return alt, mouse
}

func modeRes(alt, mouse bool) json.RawMessage {
	return jsonRaw(map[string]any{"alt": alt, "mouse": mouse})
}

// subConn serialises frames: the read loop and request handlers both write.
type subConn struct {
	c  net.Conn
	mu sync.Mutex
}

func (s *subConn) send(m Msg) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeMsg(s.c, m)
}

const ringMax = 512 << 10

func runPane(args []string) error {
	fs := flag.NewFlagSet("__pane", flag.ContinueOnError)
	id := fs.String("id", "", "pane id")
	session := fs.String("session", "", "session name")
	cwd := fs.String("cwd", "", "working directory")
	shell := fs.String("shell", "", "shell")
	cols := fs.Int("cols", 80, "columns")
	rows := fs.Int("rows", 24, "rows")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" || *session == "" {
		return fmt.Errorf("pane needs --id and --session")
	}
	if *shell == "" {
		*shell = os.Getenv("SHELL")
		if *shell == "" {
			*shell = "/bin/sh"
		}
	}
	if *cwd == "" {
		*cwd, _ = os.Getwd()
	}

	p := &paneProc{
		id: *id, session: *session, sock: paneSock(*session, *id),
		shell: *shell, cwd: *cwd,
		vt:   vt10x.New(vt10x.WithSize(*cols, *rows)),
		subs: map[*subConn]bool{},
		st:   Status{State: "unknown"},
	}

	cmd := exec.Command(p.shell, "-l")
	cmd.Dir = *cwd
	cmd.Env = append(os.Environ(), "DM_PANE="+p.id, "DM_SESSION="+p.session)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(*cols), Rows: uint16(*rows)})
	if err != nil {
		return fmt.Errorf("start shell: %w", err)
	}
	p.ptmx, p.cmd = ptmx, cmd

	if err := os.MkdirAll(sessionDir(*session)+"/panes", 0o700); err != nil {
		return err
	}
	_ = os.Remove(p.sock)
	ln, err := net.Listen("unix", p.sock)
	if err != nil {
		return err
	}
	_ = os.Chmod(p.sock, 0o600)
	writePID(p.sock + ".pid")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM)
	go func() {
		<-sig
		p.shutdown()
		os.Exit(0)
	}()

	go p.readLoop()
	go func() {
		_ = cmd.Wait()
		p.shutdown()
		time.Sleep(2 * time.Second) // let attached clients read the exit
		os.Remove(p.sock)
		os.Exit(0)
	}()

	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go p.serve(c)
	}
}

func (p *paneProc) readLoop() {
	buf := make([]byte, 32<<10)
	var clean bytes.Buffer
	filter := &oscFilter{status: p.setStatus, query: p.replyQuery}
	for {
		n, err := p.ptmx.Read(buf)
		if n > 0 {
			clean.Reset()
			filter.feed(buf[:n], &clean)
			b := append([]byte(nil), clean.Bytes()...)
			if len(b) > 0 {
				p.mu.Lock()
				_, _ = p.vt.Write(b)
				p.ring = append(p.ring, b...)
				if len(p.ring) > ringMax {
					p.ring = p.ring[len(p.ring)-ringMax:]
				}
				p.broadcastLocked(Msg{Type: "bytes", Data: b})
				if m := p.vt.Mode(); m != p.mode {
					p.mode = m
					alt, mouse := modeInfo(m)
					p.broadcastLocked(Msg{Type: "mode", Res: modeRes(alt, mouse)})
				}
				p.mu.Unlock()
			}
		}
		if err != nil {
			p.mu.Lock()
			p.dead = true
			p.broadcastLocked(Msg{Type: "exit"})
			p.mu.Unlock()
			return
		}
	}
}

func (p *paneProc) broadcastLocked(m Msg) {
	for sc := range p.subs {
		_ = sc.send(m)
	}
}

// replyQuery answers the OSC 7501 feature probe by echoing the body into the
// PTY, which is the terminal-to-application channel.
func (p *paneProc) replyQuery() {
	go func() {
		_, _ = p.ptmx.Write([]byte("\x1b]7501;?\x1b\\"))
	}()
}

func (p *paneProc) setStatus(st Status) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.st == st {
		return
	}
	p.st = st
	p.broadcastLocked(Msg{Type: "status", St: &st})
}

func (p *paneProc) shutdown() {
	if p.ptmx != nil {
		_ = p.ptmx.Close()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Signal(syscall.SIGHUP)
	}
}

func (p *paneProc) serve(c net.Conn) {
	defer c.Close()
	sc := &subConn{c: c}
	p.mu.Lock()
	p.subs[sc] = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.subs, sc)
		p.mu.Unlock()
	}()

	for {
		m, err := readMsg(c)
		if err != nil {
			return
		}
		switch m.Action {
		case "write":
			p.mu.Lock()
			b := []byte(m.Msg)
			if m.Keys != nil {
				b = m.Keys
			}
			p.mu.Unlock()
			_, _ = p.ptmx.Write(b)
			if m.Enter {
				_, _ = p.ptmx.Write([]byte("\r"))
			}
		case "resize":
			if m.Cols < 2 || m.Rows < 2 {
				continue
			}
			p.mu.Lock()
			p.vt.Resize(m.Cols, m.Rows)
			p.mu.Unlock()
			_ = pty.Setsize(p.ptmx, &pty.Winsize{Cols: uint16(m.Cols), Rows: uint16(m.Rows)})
		case "read":
			p.mu.Lock()
			text := p.vt.String()
			st := p.st
			title := p.vt.Title()
			dead := p.dead
			p.mu.Unlock()
			lines := splitLines(text)
			_ = sc.send(Msg{Type: "resp", ID: m.ID, OK: true, St: &st, Lines: lines,
				Res: jsonRaw(map[string]any{"title": title, "dead": dead})})
		case "replay":
			p.mu.Lock()
			data := append([]byte(nil), p.ring...)
			st := p.st
			dead := p.dead
			title := p.vt.Title()
			alt, mouse := modeInfo(p.vt.Mode())
			p.mu.Unlock()
			_ = sc.send(Msg{Type: "replay", Data: data, St: &st,
				Res: jsonRaw(map[string]any{"title": title, "dead": dead, "alt": alt, "mouse": mouse})})
		case "status":
			p.mu.Lock()
			st := p.st
			p.mu.Unlock()
			_ = sc.send(Msg{Type: "resp", ID: m.ID, OK: true, St: &st})
		case "ping":
			_ = sc.send(Msg{Type: "resp", ID: m.ID, OK: true})
		case "kill":
			_ = sc.send(Msg{Type: "resp", ID: m.ID, OK: true})
			p.shutdown()
			go func() {
				time.Sleep(50 * time.Millisecond)
				os.Remove(p.sock)
				os.Exit(0)
			}()
		}
	}
}

func splitLines(s string) []string {
	lines := bytes.Split([]byte(s), []byte("\n"))
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, string(bytes.TrimRight(l, " \x00")))
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

func jsonRaw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func writePID(path string) {
	_ = os.WriteFile(path, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600)
}

func init() {
	log.SetFlags(log.Ltime)
}
