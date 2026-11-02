package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
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

	"github.com/hinshun/vt10x"
)

type paneConn struct {
	id    string
	c     net.Conn
	mu    sync.Mutex
	st    Status
	alt   bool
	mouse bool
}

func (p *paneConn) send(m Msg) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return writeMsg(p.c, m)
}

type client struct {
	c       net.Conn
	mu      sync.Mutex
	cols    int
	rows    int
	paused  bool
	pending []Msg
}

func (c *client) send(m Msg) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return writeMsg(c.c, m)
}

// deliver queues pane output while the client is being caught up.
func (c *client) deliver(m Msg) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.paused {
		if len(c.pending) < 4096 {
			c.pending = append(c.pending, m)
		}
		return
	}
	_ = writeMsg(c.c, m)
}

func (c *client) resume() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, m := range c.pending {
		_ = writeMsg(c.c, m)
	}
	c.pending = nil
	c.paused = false
}

type server struct {
	mu      sync.Mutex
	st      *State
	panes   map[string]*paneConn
	clients map[*client]bool
	cols    int
	rows    int
	ln      net.Listener
	ending  bool            // session is shutting down; accept errors are expected
	adopt   map[string]bool // panes loaded from disk: a fresh spawn is a restart
}

func runServer(args []string) error {
	fs := flag.NewFlagSet("__server", flag.ContinueOnError)
	name := fs.String("s", "main", "session name")
	fs.Bool("headless", true, "unused, kept for clarity")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(sessionDir(*name), "panes"), 0o700); err != nil {
		return err
	}
	lockPath := filepath.Join(sessionDir(*name), "server.lock")
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("session %q already has a server", *name)
	}
	defer lf.Close()

	st, err := loadState(*name)
	fromDisk := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if st == nil {
		st = newState(*name)
	}
	adopt := map[string]bool{}
	if fromDisk {
		for id := range st.Panes {
			adopt[id] = true
		}
	}

	s := &server{st: st, panes: map[string]*paneConn{}, clients: map[*client]bool{},
		cols: st.Cols, rows: st.Rows, adopt: adopt}
	if s.cols < 20 {
		s.cols = 80
	}
	if s.rows < 5 {
		s.rows = 24
	}

	win := st.win()
	if win.Root == nil {
		id := win.newPaneID()
		win.Root = &Node{Pane: id}
		win.Focus = id
		s.addPaneState(id)
	}

	// Start or adopt every pane in the state, all workspaces and windows.
	var ids []string
	for _, ws := range st.Workspaces {
		for _, w := range ws.Windows {
			ids = append(ids, allPanes(w.Root)...)
		}
	}
	for _, id := range ids {
		if err := s.ensurePane(id); err != nil {
			log.Printf("pane %s: %v", id, err)
			continue
		}
		// A pane outlives the server: seed its status and modes from the pane
		// itself, or a restart would show every agent as unknown.
		rp := readPaneReplay(s.st.Name, id)
		if rp.ok {
			s.mu.Lock()
			if pc := s.panes[id]; pc != nil {
				pc.st, pc.alt, pc.mouse = rp.st, rp.alt, rp.mouse
			}
			s.mu.Unlock()
		}
		s.watchPane(id)
	}
	s.syncSizes()
	if err := st.save(); err != nil {
		return err
	}

	sock := serverSock(*name)
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	s.ln = ln
	_ = os.Chmod(sock, 0o600)
	writePID(filepath.Join(sessionDir(*name), "server.pid"))

	log.Printf("session %q listening on %s", *name, sock)
	for {
		c, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			ending := s.ending
			s.mu.Unlock()
			if ending {
				os.Exit(0)
			}
			return err
		}
		go s.serve(c)
	}
}

func (s *server) addPaneState(id string) {
	s.addPaneStateAt(id, "", "")
}

func (s *server) addPaneStateAt(id, cwd, shell string) {
	if s.st.Panes[id] != nil {
		return
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if shell == "" {
		shell = os.Getenv("SHELL")
	}
	s.st.Panes[id] = &PaneState{Shell: shell, Cwd: cwd, Cols: 80, Rows: 24}
}

// ensurePane dials the pane's socket and adopts it, or spawns a fresh daemon.
func (s *server) ensurePane(id string) error {
	sock := paneSock(s.st.Name, id)
	if c, err := net.DialTimeout("unix", sock, 300*time.Millisecond); err == nil {
		pc := &paneConn{id: id, c: c, st: Status{State: "unknown"}}
		s.mu.Lock()
		s.panes[id] = pc
		s.mu.Unlock()
		return nil
	}
	s.mu.Lock()
	ps := s.st.Panes[id]
	if ps == nil {
		s.addPaneState(id)
		ps = s.st.Panes[id]
	}
	if s.adopt[id] {
		ps.Restarted = true
		delete(s.adopt, id)
	}
	s.mu.Unlock()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "__pane", "--id", id, "--session", s.st.Name,
		"--cwd", ps.Cwd, "--shell", ps.Shell,
		"--cols", strconv.Itoa(ps.Cols), "--rows", strconv.Itoa(ps.Rows))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	logPath := filepath.Join(sessionDir(s.st.Name), "panes", id+".log")
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer lf.Close()
	cmd.Stdout, cmd.Stderr = lf, lf
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()

	for i := 0; i < 100; i++ {
		c, err := net.DialTimeout("unix", sock, 100*time.Millisecond)
		if err == nil {
			pc := &paneConn{id: id, c: c, st: Status{State: "unknown"}}
			s.mu.Lock()
			s.panes[id] = pc
			s.mu.Unlock()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("pane %s did not come up", id)
}

// watchPane fans pane output out to clients and tracks status.
func (s *server) watchPane(id string) {
	s.mu.Lock()
	pc := s.panes[id]
	s.mu.Unlock()
	if pc == nil {
		return
	}
	go func() {
		for {
			m, err := readMsg(pc.c)
			if err != nil {
				s.paneExited(id)
				return
			}
			switch m.Type {
			case "bytes":
				s.mu.Lock()
				s.broadcastLocked(Msg{Type: "bytes", Target: id, Data: m.Data})
				s.mu.Unlock()
			case "status":
				if m.St != nil {
					pc.st = *m.St
					s.pushSnapshot()
				}
			case "mode":
				var mi struct {
					Alt   bool `json:"alt"`
					Mouse bool `json:"mouse"`
				}
				_ = json.Unmarshal(m.Res, &mi)
				s.mu.Lock()
				pc.alt, pc.mouse = mi.Alt, mi.Mouse
				snap := s.snapshotLocked()
				s.broadcastLocked(Msg{Type: "snapshot", Snap: snap})
				s.mu.Unlock()
			case "exit":
				s.paneExited(id)
				return
			}
		}
	}()
}

func (s *server) broadcastLocked(m Msg) {
	for c := range s.clients {
		c.deliver(m)
	}
}

func (s *server) snapshotLocked() *Snapshot {
	win := s.st.win()
	rects := map[string]Rect{}
	layout(win.Root, Rect{0, 0, s.cols, s.rows - 1}, rects)
	snap := &Snapshot{
		Session:      s.st.Name,
		Workspace:    s.st.ws().Name,
		Windows:      []string{},
		ActiveWindow: s.st.ws().Active,
		Focus:        win.Focus,
		Cols:         s.cols,
		Rows:         s.rows,
		Root:         win.Root,
	}
	for _, w := range s.st.ws().Windows {
		snap.Windows = append(snap.Windows, w.Name)
	}
	for _, id := range allPanes(win.Root) {
		pc := s.panes[id]
		info := PaneInfo{ID: id, Rect: rects[id]}
		if pc != nil {
			info.Status = pc.st.State
			info.Message = pc.st.Message
			info.Alt = pc.alt
			info.Mouse = pc.mouse
		}
		if ps := s.st.Panes[id]; ps != nil {
			info.Name = ps.Name
		}
		ps := s.st.Panes[id]
		if ps != nil && ps.Restarted {
			info.Restart = true
		}
		snap.Panes = append(snap.Panes, info)
	}
	return snap
}

// paneExited removes a dead pane from the layout. A split collapses, focus
// moves to a live neighbour, an empty window closes. When the session has no
// panes left the server ends the session and every client detaches.
func (s *server) paneExited(id string) {
	s.mu.Lock()
	pc := s.panes[id]
	delete(s.panes, id)
	delete(s.st.Panes, id)

	found, emptyWindow := false, false
	for _, ws := range s.st.Workspaces {
		for _, w := range ws.Windows {
			if !findLeaf(w.Root, id) {
				continue
			}
			found = true
			ids := allPanes(w.Root)
			at := 0
			for i, pid := range ids {
				if pid == id {
					at = i
				}
			}
			if root, ok := pruneLeaf(w.Root, id); ok {
				w.Root = root
			}
			rest := allPanes(w.Root)
			if len(rest) > 0 {
				if w.Focus == id {
					if at >= len(rest) {
						at = len(rest) - 1
					}
					w.Focus = rest[at]
				}
				continue
			}
			w.Root = nil
			emptyWindow = true
		}
	}
	if !found && pc == nil {
		s.mu.Unlock()
		return
	}
	if emptyWindow {
		s.dropEmptyWindows()
	}
	if s.countPanes() == 0 {
		s.mu.Unlock()
		if pc != nil {
			_ = pc.c.Close()
		}
		s.endSession()
		return
	}
	s.mu.Unlock()

	if pc != nil {
		_ = pc.c.Close()
	}
	old := paneSock(s.st.Name, id)
	_ = os.Remove(old)
	_ = os.Remove(old + ".pid")

	s.syncSizes()
	s.mu.Lock()
	_ = s.st.save()
	s.broadcastLocked(Msg{Type: "exit", Target: id})
	s.broadcastLocked(Msg{Type: "snapshot", Snap: s.snapshotLocked()})
	ids := allPanes(s.st.win().Root)
	s.mu.Unlock()

	for _, pid := range ids {
		rp := readPaneReplay(s.st.Name, pid)
		if !rp.ok {
			continue
		}
		s.mu.Lock()
		if pc := s.panes[pid]; pc != nil {
			pc.alt, pc.mouse = rp.alt, rp.mouse
		}
		s.broadcastLocked(Msg{Type: "replay", Target: pid, Data: rp.data, St: &rp.st})
		s.mu.Unlock()
	}
}

// dropEmptyWindows removes windows with no panes and workspaces left with no
// windows, keeping the active window and workspace pointing at something real.
func (s *server) dropEmptyWindows() {
	var activeWin *Window
	if ws := s.st.ws(); ws.Active >= 0 && ws.Active < len(ws.Windows) {
		activeWin = ws.Windows[ws.Active]
	}
	var keep []*Workspace
	for _, ws := range s.st.Workspaces {
		var wins []*Window
		for _, w := range ws.Windows {
			if w.Root != nil && len(allPanes(w.Root)) > 0 {
				wins = append(wins, w)
			}
		}
		if len(wins) == 0 {
			continue
		}
		ws.Windows = wins
		ws.Active = 0
		for i, w := range wins {
			if w == activeWin {
				ws.Active = i
			}
		}
		keep = append(keep, ws)
	}
	if len(keep) == 0 {
		s.st.Workspaces = nil
		s.st.Active = 0
		return
	}
	s.st.Workspaces = keep
	if s.st.Active >= len(keep) {
		s.st.Active = len(keep) - 1
	}
}

func (s *server) countPanes() int {
	n := 0
	for _, ws := range s.st.Workspaces {
		for _, w := range ws.Windows {
			n += len(allPanes(w.Root))
		}
	}
	return n
}

// endSession closes every client, removes the session directory and exits. A
// session with no panes is gone, the same way tmux ends one.
func (s *server) endSession() {
	s.mu.Lock()
	s.ending = true
	clients := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.broadcastLocked(Msg{Type: "close"})
	s.mu.Unlock()

	// Remove the session before closing the listener: accepting goroutine wakes
	// on the closed listener and may exit the process immediately.
	name := s.st.Name
	_ = os.Remove(serverSock(name))
	_ = os.Remove(filepath.Join(sessionDir(name), "server.pid"))
	_ = os.RemoveAll(sessionDir(name))
	for _, c := range clients {
		_ = c.c.Close()
	}
	if s.ln != nil {
		_ = s.ln.Close()
	}
	os.Exit(0)
}

func (s *server) pushSnapshot() {
	s.mu.Lock()
	snap := s.snapshotLocked()
	s.broadcastLocked(Msg{Type: "snapshot", Snap: snap})
	s.mu.Unlock()
}

// syncSizes pushes each active-window pane's rectangle into its PTY and VT.
func (s *server) syncSizes() {
	s.mu.Lock()
	win := s.st.win()
	rects := map[string]Rect{}
	layout(win.Root, Rect{0, 0, s.cols, s.rows - 1}, rects)
	s.st.Cols, s.st.Rows = s.cols, s.rows
	var calls []func()
	for id, r := range rects {
		pc := s.panes[id]
		ps := s.st.Panes[id]
		if ps == nil {
			continue
		}
		if ps.Cols == r.W && ps.Rows == r.H {
			continue
		}
		ps.Cols, ps.Rows = r.W, r.H
		calls = append(calls, func() {
			if pc != nil {
				_ = pc.send(Msg{Action: "resize", Cols: r.W, Rows: r.H})
			}
		})
	}
	s.mu.Unlock()
	for _, f := range calls {
		f()
	}
	_ = s.st.save()
}

func (s *server) serve(c net.Conn) {
	cl := &client{c: c}
	registered := false
	defer func() {
		if registered {
			s.mu.Lock()
			delete(s.clients, cl)
			s.mu.Unlock()
		}
		c.Close()
	}()

	for {
		m, err := readMsg(c)
		if err != nil {
			return
		}
		switch m.Type {
		case "attach":
			registered = true
			s.mu.Lock()
			s.clients[cl] = true
			s.mu.Unlock()
			s.attach(cl, m)
		case "keys":
			s.mu.Lock()
			pc := s.panes[s.st.win().Focus]
			s.mu.Unlock()
			if pc != nil {
				_ = pc.send(Msg{Action: "write", Keys: m.Keys})
			}
		case "resize":
			s.mu.Lock()
			cl.cols, cl.rows = m.Cols, m.Rows
			if m.Cols > 20 && m.Rows > 5 {
				s.cols, s.rows = m.Cols, m.Rows
			}
			s.mu.Unlock()
			s.syncSizes()
			s.pushSnapshot()
		case "req":
			s.handleReq(cl, m)
		}
	}
}

func (s *server) attach(cl *client, m Msg) {
	s.mu.Lock()
	if m.Cols > 20 && m.Rows > 5 {
		s.cols, s.rows = m.Cols, m.Rows
	}
	cl.cols, cl.rows = s.cols, s.rows
	cl.paused = true
	s.mu.Unlock()
	s.syncSizes()
	s.mu.Lock()
	snap := s.snapshotLocked()
	ids := allPanes(s.st.win().Root)
	s.mu.Unlock()
	_ = cl.send(Msg{Type: "snapshot", Snap: snap})
	for _, id := range ids {
		rp := readPaneReplay(s.st.Name, id)
		if !rp.ok {
			continue
		}
		s.mu.Lock()
		if pc := s.panes[id]; pc != nil {
			pc.alt, pc.mouse = rp.alt, rp.mouse
		}
		s.mu.Unlock()
		_ = cl.send(Msg{Type: "replay", Target: id, Data: rp.data, St: &rp.st})
	}
	cl.resume()
}

type paneReplay struct {
	data  []byte
	st    Status
	alt   bool
	mouse bool
	ok    bool
}

func readPaneReplay(session, id string) paneReplay {
	var rp paneReplay
	c, err := net.DialTimeout("unix", paneSock(session, id), time.Second)
	if err != nil {
		return rp
	}
	defer c.Close()
	if err := writeMsg(c, Msg{Type: "req", Action: "replay", ID: 1}); err != nil {
		return rp
	}
	m, err := readMsg(c)
	if err != nil {
		return rp
	}
	if m.St != nil {
		rp.st = *m.St
	}
	var extra struct {
		Alt   bool `json:"alt"`
		Mouse bool `json:"mouse"`
	}
	_ = json.Unmarshal(m.Res, &extra)
	rp.data, rp.alt, rp.mouse, rp.ok = m.Data, extra.Alt, extra.Mouse, true
	return rp
}

func paneRequest(session, id string, m Msg) (Msg, error) {
	var out Msg
	c, err := net.DialTimeout("unix", paneSock(session, id), time.Second)
	if err != nil {
		return out, err
	}
	defer c.Close()
	m.ID = 1
	if err := writeMsg(c, m); err != nil {
		return out, err
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		got, err := readMsg(c)
		if err != nil {
			return out, err
		}
		if got.Type == "resp" || got.Type == "replay" {
			return got, nil
		}
	}
}

func (s *server) reply(cl *client, m Msg, res any, err error) {
	out := Msg{Type: "resp", ID: m.ID, Res: jsonRaw(res)}
	if err != nil {
		out.Error = err.Error()
	} else {
		out.OK = true
	}
	_ = cl.send(out)
}

func (s *server) handleReq(cl *client, m Msg) {
	switch m.Action {
	case "ping":
		s.reply(cl, m, map[string]any{"session": s.st.Name}, nil)

	case "list", "ls":
		s.mu.Lock()
		snap := s.snapshotLocked()
		win := s.st.win()
		tree := win.Root
		s.mu.Unlock()
		s.reply(cl, m, map[string]any{"snapshot": snap, "tree": tree}, nil)

	case "read":
		lines, st, err := s.paneLines(m.Target)
		if err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		s.reply(cl, m, map[string]any{"lines": lines, "status": st}, nil)

	case "history":
		lines, err := s.paneHistory(m.Target, m.Count)
		if err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		s.reply(cl, m, map[string]any{"lines": lines}, nil)

	case "status":
		s.mu.Lock()
		pc := s.panes[m.Target]
		s.mu.Unlock()
		if m.Target == "" {
			s.reply(cl, m, s.statuses(), nil)
			return
		}
		if pc == nil {
			s.reply(cl, m, nil, fmt.Errorf("no pane %q", m.Target))
			return
		}
		s.reply(cl, m, pc.st, nil)

	case "write-to", "exec":
		if m.Action == "exec" {
			m.Enter = true
		}
		if err := s.writeTo(m.Target, m.Msg, m.Enter); err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		s.reply(cl, m, map[string]any{"pane": m.Target}, nil)

	case "keys":
		s.mu.Lock()
		pc := s.panes[m.Target]
		s.mu.Unlock()
		if pc == nil {
			s.reply(cl, m, nil, fmt.Errorf("no pane %q", m.Target))
			return
		}
		_ = pc.send(Msg{Action: "write", Keys: m.Keys})
		s.reply(cl, m, nil, nil)

	case "split":
		var id string
		err := s.mutation(func() error {
			var e error
			id, e = s.split(m.Target, m.Dir, m.Cwd, m.Ratio, m.NoFocus)
			return e
		})
		s.afterMutation()
		s.reply(cl, m, map[string]any{"pane": id}, err)

	case "resize-split":
		err := s.mutation(func() error { return s.resizeSplit(m.Target, m.Other, m.Ratio) })
		if err == nil {
			s.syncSizes()
			s.pushSnapshot()
		}
		s.reply(cl, m, nil, err)

	case "focus":
		err := s.mutation(func() error { return s.setFocus(m.Target) })
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "focus-dir":
		err := s.mutation(func() error { return s.focusDir(m.Dir) })
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "next-pane", "prev-pane":
		step := 1
		if m.Action == "prev-pane" {
			step = -1
		}
		err := s.mutation(func() error { return s.cyclePane(step) })
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "close-pane":
		err := s.mutation(func() error { return s.closePane(m.Target) })
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "new-window":
		var wid, pid string
		err := s.mutation(func() error {
			var e error
			wid, pid, e = s.newWindow(m.Target, m.Name, m.Cwd, m.NoFocus)
			return e
		})
		s.afterMutation()
		s.reply(cl, m, map[string]any{"window": wid, "pane": pid}, err)

	case "next-window", "prev-window":
		step := 1
		if m.Action == "prev-window" {
			step = -1
		}
		err := s.mutation(func() error { return s.cycleWindow(step) })
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "select-window":
		err := s.mutation(func() error { return s.selectWindow(m.Index) })
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "new-workspace":
		var wsid, wid, pid string
		err := s.mutation(func() error {
			var e error
			wsid, wid, pid, e = s.newWorkspace(m.Name, m.Window, m.Cwd, m.NoFocus)
			return e
		})
		s.afterMutation()
		s.reply(cl, m, map[string]any{"workspace": wsid, "window": wid, "pane": pid}, err)

	case "next-workspace", "prev-workspace":
		step := 1
		if m.Action == "prev-workspace" {
			step = -1
		}
		err := s.mutation(func() error { return s.cycleWorkspace(step) })
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "list-workspaces":
		s.reply(cl, m, s.workspaceList(), nil)

	case "list-panes":
		s.reply(cl, m, map[string]any{"panes": s.paneList()}, nil)

	case "pane":
		ws, w, ok := s.findPane(m.Target)
		if !ok {
			s.reply(cl, m, nil, fmt.Errorf("no pane %q", m.Target))
			return
		}
		ps := s.st.Panes[m.Target]
		res := map[string]any{"id": m.Target, "workspace": ws.ID, "window": w.ID,
			"workspaceName": ws.Name, "windowName": w.Name}
		if ps != nil {
			res["name"] = ps.Name
			res["cwd"] = ps.Cwd
		}
		s.reply(cl, m, res, nil)

	case "set-pane-name":
		err := s.mutation(func() error {
			ps := s.st.Panes[m.Target]
			if ps == nil {
				return fmt.Errorf("no pane %q", m.Target)
			}
			ps.Name = m.Name
			return nil
		})
		s.afterMutation()
		s.reply(cl, m, map[string]any{"pane": m.Target, "name": m.Name}, err)

	case "focus-window":
		err := s.mutation(func() error { return s.focusWindow(m.Target) })
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "focus-workspace":
		err := s.mutation(func() error { return s.focusWorkspace(m.Target) })
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "close-workspace":
		err := s.mutation(func() error { return s.closeWorkspace(m.Target) })
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "rename-window":
		err := s.mutation(func() error {
			if m.Name == "" {
				return fmt.Errorf("name required")
			}
			s.st.win().Name = m.Name
			return nil
		})
		s.afterMutation()
		s.reply(cl, m, nil, err)

	default:
		s.reply(cl, m, nil, fmt.Errorf("unknown action %q", m.Action))
	}
}

func (s *server) statuses() map[string]Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]Status{}
	for id, pc := range s.panes {
		out[id] = pc.st
	}
	return out
}

// paneLines reads the pane VT through a short-lived socket connection.
func (s *server) paneLines(id string) ([]string, Status, error) {
	m, err := paneRequest(s.st.Name, id, Msg{Type: "req", Action: "read"})
	if err != nil {
		return nil, Status{}, err
	}
	if !m.OK {
		return nil, Status{}, fmt.Errorf("%s", m.Error)
	}
	st := Status{}
	if m.St != nil {
		st = *m.St
	}
	if m.Lines != nil {
		return m.Lines, st, nil
	}
	// A pane started by the short-lived build that moved rows into Res.
	var res struct {
		Lines []string `json:"lines"`
	}
	_ = json.Unmarshal(m.Res, &res)
	return res.Lines, st, nil
}

// paneHistory replays the pane's ring into a tall terminal and returns the
// rows immediately above the live screen, oldest first.
func (s *server) paneHistory(id string, n int) ([]string, error) {
	if n <= 0 {
		return nil, fmt.Errorf("history needs lines > 0")
	}
	r, ok := s.paneRect(id)
	if !ok || r.W <= 0 || r.H <= 0 {
		return nil, fmt.Errorf("no pane %q", id)
	}
	rp := readPaneReplay(s.st.Name, id)
	if !rp.ok {
		return nil, fmt.Errorf("no pane %q", id)
	}
	if rp.alt {
		return nil, fmt.Errorf("pane %q is on the alternate screen", id)
	}
	vt := vt10x.New(vt10x.WithSize(r.W, r.H+2000))
	if _, err := vt.Write(rp.data); err != nil {
		return nil, err
	}
	top, bottom := contentRows(vt)
	if bottom == 0 {
		return []string{}, nil
	}
	end := bottom - r.H
	if end < 0 {
		end = 0
	}
	start := end - n
	if start < top {
		start = top
	}
	if start > end {
		start = end
	}
	out := make([]string, 0, end-start)
	for y := start; y < end; y++ {
		out = append(out, historyRow(vt, y, r.W))
	}
	return out, nil
}

// historyRow renders one terminal row as plain text.
func historyRow(v vt10x.View, y, cols int) string {
	var b strings.Builder
	for x := 0; x < cols; x++ {
		g := v.Cell(x, y)
		if g.Char == 0 || g.Char < 0x20 {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(g.Char)
	}
	return strings.TrimRight(b.String(), " \x00")
}

// paneRect finds a pane's current rectangle in any window.
func (s *server) paneRect(id string) (Rect, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ws := range s.st.Workspaces {
		for _, w := range ws.Windows {
			if !findLeaf(w.Root, id) {
				continue
			}
			rects := map[string]Rect{}
			layout(w.Root, Rect{0, 0, s.cols, s.rows - 1}, rects)
			r, ok := rects[id]
			return r, ok
		}
	}
	return Rect{}, false
}

func (s *server) writeTo(id, text string, enter bool) error {
	s.mu.Lock()
	pc := s.panes[id]
	known := false
	for _, ws := range s.st.Workspaces {
		for _, w := range ws.Windows {
			if findLeaf(w.Root, id) {
				known = true
			}
		}
	}
	s.mu.Unlock()
	if !known {
		return fmt.Errorf("no pane %q", id)
	}
	if pc == nil {
		// A pane can exist in the layout but not be adopted yet.
		if err := s.ensurePane(id); err != nil {
			return err
		}
		s.watchPane(id)
		s.mu.Lock()
		pc = s.panes[id]
		s.mu.Unlock()
	}
	if pc == nil {
		return fmt.Errorf("no pane %q", id)
	}
	return pc.send(Msg{Action: "write", Msg: text, Enter: enter})
}

// mutation runs fn and saves state. fn takes the lock itself, because it may
// need to spawn a process, which must not happen under the lock.
func (s *server) mutation(fn func() error) error {
	if err := fn(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.save()
}

// afterMutation resizes panes, broadcasts the new layout and re-sends replays.
func (s *server) afterMutation() {
	s.syncSizes()
	s.mu.Lock()
	snap := s.snapshotLocked()
	s.broadcastLocked(Msg{Type: "snapshot", Snap: snap})
	s.mu.Unlock()
	// Clients rebuild their VT after a layout change; replay the rings.
	s.mu.Lock()
	ids := allPanes(s.st.win().Root)
	s.mu.Unlock()
	for _, id := range ids {
		rp := readPaneReplay(s.st.Name, id)
		if !rp.ok {
			continue
		}
		s.mu.Lock()
		if pc := s.panes[id]; pc != nil {
			pc.alt, pc.mouse = rp.alt, rp.mouse
		}
		s.broadcastLocked(Msg{Type: "replay", Target: id, Data: rp.data, St: &rp.st})
		s.mu.Unlock()
	}
}

type paneListInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Status    string `json:"status,omitempty"`
	Message   string `json:"message,omitempty"`
	Workspace string `json:"workspace"`
	Window    string `json:"window"`
}

// paneList reports every pane in the session, including inactive workspaces
// and windows. Agents must see the whole fleet, not just the visible window.
func (s *server) paneList() []paneListInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []paneListInfo
	for _, ws := range s.st.Workspaces {
		for _, w := range ws.Windows {
			for _, id := range allPanes(w.Root) {
				info := paneListInfo{ID: id, Workspace: ws.ID, Window: w.ID}
				if ps := s.st.Panes[id]; ps != nil {
					info.Name = ps.Name
				}
				if pc := s.panes[id]; pc != nil {
					info.Status = pc.st.State
					info.Message = pc.st.Message
				}
				out = append(out, info)
			}
		}
	}
	return out
}

type winInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

type wsInfo struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Active  bool      `json:"active"`
	Windows []winInfo `json:"windows"`
}

func (s *server) workspaceList() []wsInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]wsInfo, 0, len(s.st.Workspaces))
	for i, ws := range s.st.Workspaces {
		info := wsInfo{ID: ws.ID, Name: ws.Name, Active: i == s.st.Active}
		for j, w := range ws.Windows {
			info.Windows = append(info.Windows, winInfo{ID: w.ID, Name: w.Name,
				Active: i == s.st.Active && j == ws.Active})
		}
		out = append(out, info)
	}
	return out
}

// findPane locates a pane anywhere in the session. Panes outside the active
// window are still addressable, which is what a background layout needs.
func (s *server) findPane(id string) (*Workspace, *Window, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ws := range s.st.Workspaces {
		for _, w := range ws.Windows {
			if findLeaf(w.Root, id) {
				return ws, w, true
			}
		}
	}
	return nil, nil, false
}

func (s *server) focusWindow(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, ws := range s.st.Workspaces {
		for j, w := range ws.Windows {
			if w.ID != id {
				continue
			}
			s.st.Active = i
			ws.Active = j
			if !findLeaf(w.Root, w.Focus) {
				if ids := allPanes(w.Root); len(ids) > 0 {
					w.Focus = ids[0]
				}
			}
			return nil
		}
	}
	return fmt.Errorf("no window %q", id)
}

func (s *server) focusWorkspace(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, ws := range s.st.Workspaces {
		if ws.ID == id {
			s.st.Active = i
			return nil
		}
	}
	return fmt.Errorf("no workspace %q", id)
}

func (s *server) closeWorkspace(id string) error {
	s.mu.Lock()
	idx := -1
	for i, ws := range s.st.Workspaces {
		if ws.ID == id {
			idx = i
		}
	}
	if idx < 0 {
		s.mu.Unlock()
		return fmt.Errorf("no workspace %q", id)
	}
	if len(s.st.Workspaces) == 1 {
		s.mu.Unlock()
		return fmt.Errorf("cannot close the last workspace")
	}
	ws := s.st.Workspaces[idx]
	var ids []string
	for _, w := range ws.Windows {
		ids = append(ids, allPanes(w.Root)...)
	}
	s.st.Workspaces = append(s.st.Workspaces[:idx], s.st.Workspaces[idx+1:]...)
	if s.st.Active >= len(s.st.Workspaces) {
		s.st.Active = len(s.st.Workspaces) - 1
	}
	for _, pid := range ids {
		if pc := s.panes[pid]; pc != nil {
			_ = pc.send(Msg{Action: "kill"})
			_ = pc.c.Close()
			delete(s.panes, pid)
		}
		delete(s.st.Panes, pid)
	}
	s.mu.Unlock()
	return nil
}

func (s *server) activeIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return allPanes(s.st.win().Root)
}

func (s *server) split(target, dir, cwd string, ratio float64, noFocus bool) (string, error) {
	switch dir {
	case "left", "right", "up", "down":
	default:
		return "", fmt.Errorf("dir must be left, right, up or down")
	}
	s.mu.Lock()
	if target == "" {
		target = s.st.win().Focus
	}
	var win *Window
	var ws *Workspace
	for _, cand := range s.st.Workspaces {
		for _, w := range cand.Windows {
			if findLeaf(w.Root, target) {
				ws, win = cand, w
			}
		}
	}
	if win == nil {
		s.mu.Unlock()
		return "", fmt.Errorf("no pane %q", target)
	}
	id := win.newPaneID()
	splitLeaf(win.Root, target, dir, id, ratio)
	s.addPaneStateAt(id, cwd, "")
	if !noFocus {
		for i, cand := range s.st.Workspaces {
			if cand == ws {
				s.st.Active = i
			}
		}
		for i, cand := range ws.Windows {
			if cand == win {
				ws.Active = i
			}
		}
		win.Focus = id
	}
	s.mu.Unlock()

	if err := s.ensurePane(id); err != nil {
		return id, err
	}
	s.watchPane(id)
	return id, nil
}

// resizeSplit sets the weights of the split between two adjacent panes. Ratio
// is the share the first child keeps, clamped so both sides keep three cells.
func (s *server) resizeSplit(first, second string, ratio float64) error {
	if math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return fmt.Errorf("bad ratio")
	}
	if first == "" || second == "" || first == second {
		return fmt.Errorf("resize-split needs two panes")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ws := range s.st.Workspaces {
		for _, win := range ws.Windows {
			if !findLeaf(win.Root, first) || !findLeaf(win.Root, second) {
				continue
			}
			pa := ancestorPath(win.Root, first, nil)
			pb := ancestorPath(win.Root, second, nil)
			split := lowestCommonAncestor(pa, pb)
			if split == nil || split.Pane != "" {
				return fmt.Errorf("panes %q and %q share no split", first, second)
			}
			if len(split.Kids) != 2 {
				return fmt.Errorf("split has %d panes, want 2", len(split.Kids))
			}
			r, ok := nodeRect(win.Root, split, Rect{0, 0, s.cols, s.rows - 1})
			if !ok {
				return fmt.Errorf("no rect for split of %q", first)
			}
			horizontal := split.Dir == "row"
			space := r.W
			if !horizontal {
				space = r.H
			}
			space--
			if space < 6 {
				return fmt.Errorf("split between %q and %q is too small", first, second)
			}
			if space == 6 {
				split.Sizes = []float64{0.5, 0.5}
				return nil
			}
			const eps = 1e-9
			lo := 3.0/float64(space) + eps
			hi := 1 - 3.0/float64(space) + eps
			if ratio < lo {
				ratio = lo
			}
			if ratio > hi {
				ratio = hi
			}
			split.Sizes = []float64{ratio, 1 - ratio}
			return nil
		}
	}
	return fmt.Errorf("no pane %q", first)
}

func (s *server) setFocus(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, ws := range s.st.Workspaces {
		for j, w := range ws.Windows {
			if findLeaf(w.Root, id) {
				s.st.Active = i
				ws.Active = j
				w.Focus = id
				return nil
			}
		}
	}
	return fmt.Errorf("no pane %q", id)
}

func (s *server) focusDir(dir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	win := s.st.win()
	rects := map[string]Rect{}
	layout(win.Root, Rect{0, 0, s.cols, s.rows - 1}, rects)
	n := neighbor(rects, win.Focus, dir)
	if n == "" {
		return fmt.Errorf("no pane %s of %s", dir, win.Focus)
	}
	win.Focus = n
	return nil
}

func (s *server) cyclePane(step int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	win := s.st.win()
	ids := allPanes(win.Root)
	if len(ids) == 0 {
		return fmt.Errorf("no panes")
	}
	at := 0
	for i, id := range ids {
		if id == win.Focus {
			at = i
		}
	}
	win.Focus = ids[(at+step+len(ids))%len(ids)]
	return nil
}

func (s *server) closePane(target string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	win := s.st.win()
	if target == "" {
		target = win.Focus
	}
	ids := allPanes(win.Root)
	if len(ids) <= 1 {
		return fmt.Errorf("cannot close the last pane")
	}
	at := 0
	for i, id := range ids {
		if id == target {
			at = i
		}
	}
	root, ok := pruneLeaf(win.Root, target)
	if !ok {
		return fmt.Errorf("no pane %q", target)
	}
	win.Root = root
	if win.Focus == target {
		rest := allPanes(root)
		if len(rest) > 0 {
			if at >= len(rest) {
				at = len(rest) - 1
			}
			win.Focus = rest[at]
		}
	}
	if pc := s.panes[target]; pc != nil {
		_ = pc.send(Msg{Action: "kill"})
		_ = pc.c.Close()
		delete(s.panes, target)
	}
	delete(s.st.Panes, target)
	return nil
}

func (s *server) newWindow(wsID, label, cwd string, noFocus bool) (string, string, error) {
	s.mu.Lock()
	ws := s.st.ws()
	if wsID != "" {
		ws = nil
		for _, cand := range s.st.Workspaces {
			if cand.ID == wsID {
				ws = cand
			}
		}
		if ws == nil {
			s.mu.Unlock()
			return "", "", fmt.Errorf("no workspace %q", wsID)
		}
	}
	wid := s.st.newWindowID()
	w := &Window{ID: wid, Name: label}
	id := w.newPaneID()
	w.Root = &Node{Pane: id}
	w.Focus = id
	if label == "" {
		label = strconv.Itoa(len(ws.Windows) + 1)
		w.Name = label
	}
	ws.Windows = append(ws.Windows, w)
	s.addPaneStateAt(id, cwd, "")
	if !noFocus {
		for i, cand := range s.st.Workspaces {
			if cand == ws {
				s.st.Active = i
			}
		}
		ws.Active = len(ws.Windows) - 1
	}
	s.mu.Unlock()
	if err := s.ensurePane(id); err != nil {
		return wid, id, err
	}
	s.watchPane(id)
	return wid, id, nil
}

func (s *server) cycleWindow(step int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.st.ws()
	n := len(ws.Windows)
	if n == 0 {
		return fmt.Errorf("no windows")
	}
	ws.Active = (ws.Active + step + n) % n
	return nil
}

func (s *server) selectWindow(i int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.st.ws()
	if i < 0 {
		i = 0
	}
	if i >= len(ws.Windows) {
		return fmt.Errorf("window %d does not exist", i)
	}
	ws.Active = i
	return nil
}

func (s *server) newWorkspace(name, tabLabel, cwd string, noFocus bool) (string, string, string, error) {
	s.mu.Lock()
	if name == "" {
		name = "ws" + strconv.Itoa(len(s.st.Workspaces)+1)
	}
	if tabLabel == "" {
		tabLabel = "1"
	}
	wsid := s.st.newWorkspaceID()
	wid := s.st.newWindowID()
	win := &Window{ID: wid, Name: tabLabel}
	id := win.newPaneID()
	win.Root = &Node{Pane: id}
	win.Focus = id
	ws := &Workspace{ID: wsid, Name: name, Windows: []*Window{win}}
	s.st.Workspaces = append(s.st.Workspaces, ws)
	if !noFocus {
		s.st.Active = len(s.st.Workspaces) - 1
	}
	s.addPaneStateAt(id, cwd, "")
	s.mu.Unlock()
	if err := s.ensurePane(id); err != nil {
		return wsid, wid, id, err
	}
	s.watchPane(id)
	return wsid, wid, id, nil
}

func (s *server) cycleWorkspace(step int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.st.Workspaces)
	if n == 0 {
		return fmt.Errorf("no workspaces")
	}
	s.st.Active = (s.st.Active + step + n) % n
	return nil
}

// ---- client side helpers ----

// ensureServer returns a live connection to the session server, starting it if
// needed. The returned bool reports whether a new daemon was spawned.
func ensureServer(name string) (net.Conn, error) {
	sock := serverSock(name)
	if c, err := net.DialTimeout("unix", sock, 300*time.Millisecond); err == nil {
		return c, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(sessionDir(name), 0o700); err != nil {
		return nil, err
	}
	logPath := filepath.Join(sessionDir(name), "server.log")
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

func rpc(name string, m Msg) (Msg, error) {
	c, err := ensureServer(name)
	if err != nil {
		return Msg{}, err
	}
	defer c.Close()
	m.Type = "req"
	m.ID = 1
	if err := writeMsg(c, m); err != nil {
		return Msg{}, err
	}
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		got, err := readMsg(c)
		if err != nil {
			return Msg{}, err
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
	entries, err := os.ReadDir(filepath.Join(stateDir(), "sessions"))
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
		if c, err := net.DialTimeout("unix", serverSock(name), 100*time.Millisecond); err == nil {
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
	data, err := os.ReadFile(filepath.Join(sessionDir(name), "server.pid"))
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
	_ = os.Remove(serverSock(name))
	return nil
}
