package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"dm/internal/paths"
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
	cwd     string // where the shell is now, not where it started; guarded by mu

	ptmx *os.File
	cmd  *exec.Cmd

	mu   sync.Mutex
	vt   vt10x.Terminal
	ring *scrollRing
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

// ringMax is the scrollback a pane keeps: a fixed 512 KiB, allocated once.
const ringMax = 512 << 10

// scrollRing is the pane's history: a circular buffer over one fixed array.
// Writing wraps in place, so a burst never grows the daemon's heap and no
// trim leaves a larger backing array behind.
//
// Locking is the caller's: paneProc.mu guards the ring.
type scrollRing struct {
	buf [ringMax]byte
	n   int // bytes held, at most ringMax
	w   int // next byte to overwrite
}

// Write appends b, dropping the oldest bytes once the ring is full.
func (r *scrollRing) Write(b []byte) {
	if len(b) >= ringMax {
		copy(r.buf[:], b[len(b)-ringMax:])
		r.n, r.w = ringMax, 0
		return
	}
	n := copy(r.buf[r.w:], b)
	r.w = (r.w + n) % ringMax
	if rest := b[n:]; len(rest) > 0 {
		n = copy(r.buf[r.w:], rest)
		r.w = (r.w + n) % ringMax
	}
	if r.n += len(b); r.n > ringMax {
		r.n = ringMax
	}
}

// Len is the number of bytes held.
func (r *scrollRing) Len() int { return r.n }

// Bytes copies the ring out, oldest first. The reply owns the copy; the ring
// stays fixed.
func (r *scrollRing) Bytes() []byte {
	out := make([]byte, 0, r.n)
	start := (r.w - r.n + ringMax) % ringMax
	end := start + r.n
	if end <= ringMax {
		return append(out, r.buf[start:end]...)
	}
	out = append(out, r.buf[start:]...)
	return append(out, r.buf[:end-ringMax]...)
}

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

	// A pane holds one screen, one 512 KiB ring and a shell's output. 32 MiB
	// is plenty and makes the GC return a burst's pages instead of keeping
	// them. GOGC stays at its default: lowering it raised the idle figure.
	debug.SetMemoryLimit(32 << 20)

	p := &paneProc{
		id: *id, session: *session, sock: paths.PaneSock(*session, *id),
		shell: *shell, cwd: *cwd,
		vt:   vt10x.New(vt10x.WithSize(*cols, *rows)),
		ring: &scrollRing{},
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

	if err := os.MkdirAll(paths.SessionDir(*session)+"/panes", 0o700); err != nil {
		return err
	}
	_ = os.Remove(p.sock)
	ln, err := net.Listen("unix", p.sock)
	if err != nil {
		return err
	}
	_ = os.Chmod(p.sock, 0o600)
	writePID(p.sock + ".pid")
	own, err := os.Stat(p.sock)
	if err != nil {
		return err
	}
	go p.reapOrphans(own)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM)
	go func() {
		<-sig
		p.shutdown()
		os.Exit(0)
	}()

	go p.readLoop()
	go p.watchCwd()
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

// sockEvery is how often a pane checks that its socket still names this
// listener. The server removes a pane's socket when it drops the pane; a
// daemon that outlives that is unreachable and its shell would leak.
const sockEvery = 5 * time.Second

// reapOrphans stops the daemon when its socket path is gone or names another
// file. A stat error other than ENOENT is transient and only retried: an
// intact path with no client attached is the normal state while the server
// restarts, and the pane must survive it.
func (p *paneProc) reapOrphans(own os.FileInfo) {
	for {
		time.Sleep(sockEvery)
		info, err := os.Stat(p.sock)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				p.reap("socket removed")
			}
			continue
		}
		if !os.SameFile(own, info) {
			p.reap("socket replaced")
		}
	}
}

// reap kills the shell's process group and exits. The shell is a session
// leader, so its process group id is its pid, and the group takes any child
// with it.
func (p *paneProc) reap(reason string) {
	log.Printf("pane %s: %s, stopping", p.id, reason)
	if p.cmd != nil && p.cmd.Process != nil {
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	}
	os.Exit(0)
}

func (p *paneProc) readLoop() {
	buf := make([]byte, 32<<10)
	var clean bytes.Buffer
	filter := &oscFilter{
		status: p.setStatus,
		query:  p.replyQuery,
		reply:  p.replyTerminal,
		cursor: p.cursorPos,
		fg:     termFG,
		bg:     termBG,
	}
	for {
		n, err := p.ptmx.Read(buf)
		if n > 0 {
			clean.Reset()
			filter.feed(buf[:n], &clean)
			b := append([]byte(nil), clean.Bytes()...)
			if len(b) > 0 {
				p.mu.Lock()
				_, _ = p.vt.Write(b)
				p.ring.Write(b)
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

// replyTerminal writes a terminal reply into the PTY, which is the app's input.
func (p *paneProc) replyTerminal(s string) {
	go func() {
		_, _ = p.ptmx.Write([]byte(s))
	}()
}

// cursorPos is the VT cursor, zero-based, for a cursor position report.
func (p *paneProc) cursorPos() (row, col int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := p.vt.Cursor()
	return c.Y, c.X
}

// cwdEvery is how often a pane asks the kernel where its shell is. One
// readlink per pane per interval: a shell's own process carries the cwd, so a
// builtin cd moves it and /proc reports it without any shell integration.
const cwdEvery = 500 * time.Millisecond

// shellCwd is the directory a process stands in, or empty when the kernel will
// not say, as when the directory is gone.
func shellCwd(pid int) string {
	dir, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil {
		return ""
	}
	return dir
}

// watchCwd follows the shell around the filesystem and reports each move. The
// poll is the coalescer: a burst of cds costs one report.
func (p *paneProc) watchCwd() {
	if p.cmd == nil || p.cmd.Process == nil {
		return
	}
	pid := p.cmd.Process.Pid
	for {
		time.Sleep(cwdEvery)
		dir := shellCwd(pid)
		if dir == "" {
			continue
		}
		p.mu.Lock()
		if dir != p.cwd {
			p.cwd = dir
			p.broadcastLocked(Msg{Type: "cwd", Cwd: dir})
		}
		p.mu.Unlock()
	}
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
		case "screen":
			p.mu.Lock()
			data := screenBytes(p.vt)
			alt, mouse := modeInfo(p.vt.Mode())
			p.mu.Unlock()
			_ = sc.send(Msg{Type: "screen", Data: data, Res: modeRes(alt, mouse)})
		case "replay":
			p.mu.Lock()
			data := p.ring.Bytes()
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
		case "cwd":
			p.mu.Lock()
			dir := p.cwd
			p.mu.Unlock()
			_ = sc.send(Msg{Type: "resp", ID: m.ID, OK: true, Cwd: dir})
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
