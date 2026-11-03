package main

import (
	"context"
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

// outboundBytes bounds what one client may have queued, in bytes. A client
// past the bound is not reading: it gets a fresh picture instead of a backlog.
const outboundBytes = 1 << 20

type client struct {
	c    net.Conn
	mu   sync.Mutex
	cond *sync.Cond // wakes the writer; guards the queue with mu

	cols int
	rows int
	// paused holds the queue while the client is being caught up. A paused
	// client queues; the writer does not touch the socket.
	paused bool

	queue  []Msg
	qbytes int
	// stale records that the queue overflowed and queued frames were
	// dropped. The writer resyncs from a picture once the queue drains.
	stale bool
	// resync is the server's rebuild hook: one snapshot, then one screen per
	// pane. It is nil until the client attaches.
	resync func()
	closed bool
	writer sync.Once
}

func newClient(c net.Conn) *client {
	cl := &client{c: c}
	cl.cond = sync.NewCond(&cl.mu)
	return cl
}

// deliver queues a frame and returns. It never writes the socket and never
// waits on it: a stalled client must not block the pane read loop.
func (c *client) deliver(m Msg) {
	c.mu.Lock()
	c.enqueueLocked(m)
	c.mu.Unlock()
}

// enqueueLocked appends m to the queue under the byte bound. On overflow it
// drops the queued "bytes" frames, marks the client stale and stops queueing
// output, so the writer resyncs from the pane screens instead of replaying a
// backlog.
func (c *client) enqueueLocked(m Msg) {
	if c.cond == nil {
		c.cond = sync.NewCond(&c.mu)
	}
	if c.closed {
		return
	}
	if c.stale && m.Type == "bytes" {
		// The client already owes itself a picture. Queueing more output
		// would only rebuild the backlog the overflow threw away.
		return
	}
	n := msgBytes(m)
	if c.qbytes+n > outboundBytes {
		// Every queued output frame is a backlog, and the resync below sends
		// a screen per pane, so the backlog goes.
		c.dropBytesLocked()
		c.stale = true
		for c.qbytes+n > outboundBytes && len(c.queue) > 0 {
			c.popLocked()
		}
		if n > outboundBytes {
			// A single frame bigger than the whole bound never ships; the
			// resync rebuilds it piecemeal.
			return
		}
	}
	c.queue = append(c.queue, m)
	c.qbytes += n
	c.startWriter()
	c.cond.Broadcast()
}

// dropBytesLocked removes every queued "bytes" frame. The caller holds c.mu.
func (c *client) dropBytesLocked() {
	kept := c.queue[:0]
	for _, m := range c.queue {
		if m.Type == "bytes" {
			c.qbytes -= msgBytes(m)
			continue
		}
		kept = append(kept, m)
	}
	c.queue = kept
}

// popLocked removes the head frame and clears its slot, so a lagging client
// does not pin the buffers of frames it already sent. The caller holds c.mu.
func (c *client) popLocked() Msg {
	m := c.queue[0]
	c.queue[0] = Msg{}
	c.queue = c.queue[1:]
	c.qbytes -= msgBytes(m)
	return m
}

// startWriter runs the one goroutine that owns the socket. Every client frame,
// including replies, leaves through it.
func (c *client) startWriter() {
	c.writer.Do(func() { go c.writeLoop() })
}

func (c *client) writeLoop() {
	for {
		m, ok := c.next()
		if !ok {
			return
		}
		if err := writeMsg(c.c, m); err != nil {
			c.shutdown()
			return
		}
		if fn := c.takeResync(); fn != nil {
			fn()
		}
	}
}

// next waits for a frame the writer may send. A paused client holds its
// frames, so the writer waits instead of writing.
func (c *client) next() (Msg, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		if c.closed {
			return Msg{}, false
		}
		if !c.paused && len(c.queue) > 0 {
			return c.popLocked(), true
		}
		c.cond.Wait()
	}
}

// takeResync reports the server hook to run once a stale queue has drained.
// The caller runs the hook off c.mu, because it queues frames again.
func (c *client) takeResync() func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.stale || c.paused || len(c.queue) > 0 || c.resync == nil {
		return nil
	}
	c.stale = false
	return c.resync
}

// setResync installs the rebuild hook the writer runs after an overflow.
func (c *client) setResync(fn func()) {
	c.mu.Lock()
	c.resync = fn
	c.mu.Unlock()
}

// pause stops pane output from reaching the socket while the client is being
// caught up. It takes c.mu, like deliver, so the flag has one owner.
func (c *client) pause() {
	c.mu.Lock()
	c.paused = true
	c.mu.Unlock()
}

func (c *client) resume() {
	c.mu.Lock()
	c.paused = false
	if c.cond != nil {
		c.cond.Broadcast()
	}
	c.mu.Unlock()
}

// shutdown stops the writer. A frame already on the wire finishes or fails on
// the closed connection.
func (c *client) shutdown() {
	c.mu.Lock()
	c.closed = true
	c.queue = nil
	c.qbytes = 0
	if c.cond != nil {
		c.cond.Broadcast()
	}
	c.mu.Unlock()
}

// queuedBytes reports what the client still owes the wire.
func (c *client) queuedBytes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.qbytes
}

// queuedFrames reports how many frames are waiting on the writer.
func (c *client) queuedFrames() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.queue)
}

// msgBytes counts what a frame costs on the wire, near enough to bound it.
func msgBytes(m Msg) int {
	return 64 + len(m.Data) + len(m.Keys) + len(m.Msg) + len(m.Name) + len(m.Res)
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

	// Git branch labels for the rail, one entry per working directory. The
	// check runs off s.mu and the result arrives with a later snapshot.
	branchMu sync.Mutex
	branches map[string]*branchEntry
}

// branchTTL is how long a detected git label stays fresh. A stale entry is
// served while a background check runs, so a snapshot never waits on git.
const branchTTL = 10 * time.Second

type branchEntry struct {
	info    gitInfo
	at      time.Time
	loading bool
}

// gitInfo is what the rail shows for a working directory: the branch, for a
// linked worktree its own folder name, the repository root, and the
// uncommitted change counts.
type gitInfo struct {
	branch   string
	worktree string
	// root is the repository's toplevel as git reports it, cleaned. It is the
	// folder a linked worktree shares with its main checkout.
	root    string
	inRepo  bool
	dirty   bool
	added   int
	deleted int
}

// gitOutput runs one git command against cwd with the one second timeout the
// rail can afford.
func gitOutput(cwd string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, args...)...).Output()
}

// parseNumstat sums a git diff --numstat report. A binary file prints a dash
// instead of a count; that column adds nothing.
func parseNumstat(out []byte) (added, deleted int) {
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if n, err := strconv.Atoi(f[0]); err == nil {
			added += n
		}
		if n, err := strconv.Atoi(f[1]); err == nil {
			deleted += n
		}
	}
	return added, deleted
}

// detectGit is a variable so tests can stub git.
var detectGit = func(cwd string) gitInfo {
	out, err := gitOutput(cwd, "rev-parse", "--abbrev-ref", "HEAD",
		"--show-toplevel", "--git-dir", "--git-common-dir")
	if err != nil {
		return gitInfo{}
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) < 4 {
		return gitInfo{}
	}
	// A detached HEAD prints the literal HEAD. Leave the branch empty so the
	// worktree folder, or the window name, labels the rail line.
	info := gitInfo{inRepo: true}
	if b := lines[0]; b != "HEAD" {
		info.branch = b
	}
	root := filepath.Clean(lines[1])
	info.root = root
	gd, cd := lines[2], lines[3]
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(cwd, gd)
	}
	if !filepath.IsAbs(cd) {
		cd = filepath.Join(cwd, cd)
	}
	// A linked worktree has its own git dir under the main repo's worktrees
	// directory, while the common dir stays in the main checkout.
	if filepath.Clean(gd) != filepath.Clean(cd) {
		info.worktree = filepath.Base(root)
	}
	// Tracked changes only: an untracked file is not a change the rail counts.
	if st, err := gitOutput(cwd, "status", "--porcelain=v1", "-z",
		"--untracked-files=no"); err == nil {
		info.dirty = strings.Trim(string(st), "\x00") != ""
	}
	if ns, err := gitOutput(cwd, "diff", "--numstat", "HEAD"); err == nil {
		info.added, info.deleted = parseNumstat(ns)
	}
	return info
}

// gitFor returns the cached git labels for a working directory and starts a
// background check when the entry is missing or stale. It never runs git on
// the caller's goroutine, because snapshots hold s.mu.
func (s *server) gitFor(cwd string) gitInfo {
	if cwd == "" {
		return gitInfo{}
	}
	now := time.Now()
	s.branchMu.Lock()
	if s.branches == nil {
		s.branches = map[string]*branchEntry{}
	}
	e := s.branches[cwd]
	if e == nil {
		s.branches[cwd] = &branchEntry{at: now, loading: true}
		s.branchMu.Unlock()
		go s.loadGit(cwd)
		return gitInfo{}
	}
	info := e.info
	load := now.Sub(e.at) > branchTTL && !e.loading
	if load {
		e.loading = true
	}
	s.branchMu.Unlock()
	if load {
		go s.loadGit(cwd)
	}
	return info
}

func (s *server) loadGit(cwd string) {
	info := detectGit(cwd)
	s.branchMu.Lock()
	e := s.branches[cwd]
	if e == nil {
		e = &branchEntry{}
		s.branches[cwd] = e
	}
	changed := e.info != info
	e.info, e.at, e.loading = info, time.Now(), false
	s.branchMu.Unlock()
	if changed {
		s.pushSnapshot()
	}
}

// toggleFold flips a window's rail fold state and persists it.
func (s *server) toggleFold(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ws := range s.st.Workspaces {
		for _, w := range ws.Windows {
			if w.ID != id {
				continue
			}
			w.Unfolded = !w.Unfolded
			_ = s.st.save()
			return w.Unfolded, nil
		}
	}
	return false, fmt.Errorf("no window %q", id)
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
		cols: st.Cols, rows: st.Rows, adopt: adopt,
		branches: map[string]*branchEntry{}}
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
					from := pc.st.State
					pc.st = *m.St
					to, message := pc.st.State, pc.st.Message
					s.noteStatus(id, *m.St)
					s.pushSnapshot()
					if notifyStateChange(from, to) {
						s.notifyPaneState(id, to, message)
					}
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

// fitToClientsLocked takes the shared session size from the largest attached
// client. Every client draws the same layout, but a small one, such as a dm
// nested in one of this session's own panes, must not shrink the display of
// the others. The caller holds s.mu and reports whether the size changed.
func (s *server) fitToClientsLocked() bool {
	cols, rows := 0, 0
	for cl := range s.clients {
		if cl.cols > cols {
			cols = cl.cols
		}
		if cl.rows > rows {
			rows = cl.rows
		}
	}
	if cols < 20 || rows < 5 {
		return false
	}
	if cols == s.cols && rows == s.rows {
		return false
	}
	s.cols, s.rows = cols, rows
	return true
}

// requestedRailLocked is the stored width, floored at zero, before the screen
// clamp. The caller holds s.mu.
func (s *server) requestedRailLocked() int {
	if !s.st.RailSet {
		return defaultRail
	}
	if s.st.Rail < 0 {
		return 0
	}
	return s.st.Rail
}

// railLocked is the effective sidebar width for the current screen. The caller
// holds s.mu.
func (s *server) railLocked() int {
	return effectiveRail(s.cols, s.requestedRailLocked())
}

// setRail records the requested width and returns the effective one. A
// requested 0 hides the rail. The value is clamped when it is used, not when
// it is stored, so a wider terminal restores a wider rail.
func (s *server) setRail(requested int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Rail = requested
	s.st.RailSet = true
	rail := s.railLocked()
	_ = s.st.save()
	return rail
}

func (s *server) snapshotLocked() *Snapshot {
	win := s.st.win()
	rects := map[string]Rect{}
	layout(win.Root, paneAreaFor(s.cols, s.rows, s.railLocked()), rects)
	snap := &Snapshot{
		Session:      s.st.Name,
		Workspace:    s.st.ws().Name,
		Windows:      []string{},
		ActiveWindow: s.st.ws().Active,
		Focus:        win.Focus,
		Cols:         s.cols,
		Rows:         s.rows,
		Rail:         s.railLocked(),
		RailWant:     s.requestedRailLocked(),
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
	snap.All = s.allPanesLocked()
	return snap
}

// noteStatus persists the agent mark and the reporting app. Both keep the
// sidebar calling a pane an agent after state=clear, and both survive a server
// or pane restart because they live in the state file.
func (s *server) noteStatus(id string, st Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps := s.st.Panes[id]
	if ps == nil {
		return
	}
	changed := false
	if !ps.Reported {
		ps.Reported = true
		changed = true
	}
	if st.App != "" && ps.App != st.App {
		ps.App = st.App
		changed = true
	}
	if changed {
		_ = s.st.save()
	}
}

// allPanesLocked lists every pane in the session in workspace, window, tree
// order. A pane is an agent when it has a recorded name, reported the status
// protocol at least once, or has a status other than empty or unknown;
// otherwise it is a shell. The caller holds s.mu.
func (s *server) allPanesLocked() []AllPane {
	out := make([]AllPane, 0, len(s.st.Panes))
	for i, ws := range s.st.Workspaces {
		wsActive := i == s.st.Active
		for j, w := range ws.Windows {
			winActive := wsActive && j == ws.Active
			for _, id := range allPanes(w.Root) {
				info := AllPane{
					ID: id, Workspace: ws.ID, WorkspaceName: ws.Name,
					Window: w.ID, WindowName: w.Name,
					WorkspaceActive: wsActive, WindowActive: winActive,
					Focus: winActive && id == w.Focus,
				}
				info.WindowUnfolded = w.Unfolded
				ps := s.st.Panes[id]
				if ps != nil {
					info.Name = ps.Name
					info.App = ps.App
					gi := s.gitFor(ps.Cwd)
					info.Cwd = ps.Cwd
					info.Root = gi.root
					info.Branch = gi.branch
					info.Worktree = gi.worktree
					info.InRepo = gi.inRepo
					info.Dirty = gi.dirty
					info.Added = gi.added
					info.Deleted = gi.deleted
				}
				if pc := s.panes[id]; pc != nil {
					info.Status = pc.st.State
					info.Message = pc.st.Message
				}
				info.Agent = info.Name != "" || (ps != nil && ps.Reported) ||
					(info.Status != "" && info.Status != "unknown")
				out = append(out, info)
			}
		}
	}
	return out
}

// paneExited removes a dead pane from the layout. A split collapses, focus
// moves to a live neighbour, an empty window closes. When the session has no
// panes left the server ends the session and every client detaches.
func (s *server) paneExited(id string) {
	s.mu.Lock()
	pc := s.panes[id]
	// An exit interrupts the operator unless a client is attached and the pane
	// is the focus it is looking at. The title is built before the pane leaves
	// the state, because its recorded name goes with it.
	notifyExit := !s.operatorSeesLocked(id)
	exitTitle := ""
	if notifyExit {
		exitTitle = s.notifyTitleLocked(id, "exited")
	}
	exitBody := ""
	if pc != nil {
		exitBody = pc.st.Message
	}
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
		if found {
			systemNotify(exitTitle, exitBody)
		}
		if pc != nil {
			_ = pc.c.Close()
		}
		s.endSession()
		return
	}
	s.mu.Unlock()
	if found {
		systemNotify(exitTitle, exitBody)
	}

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

	s.broadcastScreens(ids)
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
	layout(win.Root, paneAreaFor(s.cols, s.rows, s.railLocked()), rects)
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
	cl := newClient(c)
	registered := false
	defer func() {
		if registered {
			s.mu.Lock()
			delete(s.clients, cl)
			resized := s.fitToClientsLocked()
			s.mu.Unlock()
			if resized {
				s.syncSizes()
				s.pushSnapshot()
			}
		}
		cl.shutdown()
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
			if m.Cols > 20 && m.Rows > 5 {
				cl.cols, cl.rows = m.Cols, m.Rows
			}
			s.fitToClientsLocked()
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
		cl.cols, cl.rows = m.Cols, m.Rows
	}
	s.fitToClientsLocked()
	s.mu.Unlock()
	// A client that overflows later resyncs from a picture instead of a
	// backlog: one snapshot, then one screen per pane.
	cl.setResync(func() { s.resyncClient(cl) })
	cl.pause()
	s.syncSizes()
	s.mu.Lock()
	snap := s.snapshotLocked()
	ids := allPanes(s.st.win().Root)
	s.mu.Unlock()
	cl.deliver(Msg{Type: "snapshot", Snap: snap})
	if s.sendScreens(ids, cl.deliver) {
		// The pane's alt or mouse mode changed since the snapshot was built.
		// Send a fresh one so the client's pane info is not one event stale.
		s.mu.Lock()
		snap = s.snapshotLocked()
		s.mu.Unlock()
		cl.deliver(Msg{Type: "snapshot", Snap: snap})
	}
	// History is not pushed at attach: the ring is 512 KiB per pane, and a
	// nested client would carry it through the outer pty. The client asks for
	// it with a "replay" request when it wants history. Its alternate-screen
	// and mouse facts came back with sendScreens above, and later mode
	// changes still arrive from the pane.
	cl.resume()
}

// paneScreen is the pane's visible screen, serialised as ANSI for a fresh
// terminal. It is the picture; the ring below is history.
type paneScreen struct {
	data  []byte
	alt   bool
	mouse bool
	ok    bool
}

func readPaneScreen(session, id string) paneScreen {
	var ps paneScreen
	c, err := net.DialTimeout("unix", paneSock(session, id), time.Second)
	if err != nil {
		return ps
	}
	defer c.Close()
	if err := writeMsg(c, Msg{Type: "req", Action: "screen", ID: 1}); err != nil {
		return ps
	}
	m, err := readMsg(c)
	if err != nil || m.Type != "screen" {
		return ps
	}
	var extra struct {
		Alt   bool `json:"alt"`
		Mouse bool `json:"mouse"`
	}
	_ = json.Unmarshal(m.Res, &extra)
	ps.data, ps.alt, ps.mouse, ps.ok = m.Data, extra.Alt, extra.Mouse, true
	return ps
}

// sendScreens asks each pane for its visible screen and hands it to send. It
// runs after a resize so the pane serialises at its new size.
func (s *server) sendScreens(ids []string, send func(Msg)) (changed bool) {
	for _, id := range ids {
		ps := readPaneScreen(s.st.Name, id)
		if !ps.ok {
			continue
		}
		s.mu.Lock()
		if pc := s.panes[id]; pc != nil {
			if pc.alt != ps.alt || pc.mouse != ps.mouse {
				changed = true
			}
			pc.alt, pc.mouse = ps.alt, ps.mouse
		}
		s.mu.Unlock()
		send(Msg{Type: "screen", Target: id, Data: ps.data})
	}
	return changed
}

func (s *server) broadcastScreens(ids []string) {
	s.sendScreens(ids, func(m Msg) {
		s.mu.Lock()
		s.broadcastLocked(m)
		s.mu.Unlock()
	})
}

// resyncClient rebuilds one client from a picture: a fresh snapshot, then one
// screen per pane through readPaneScreen. The client's writer calls it after an
// overflow drains, so a lagging client never replays a backlog.
func (s *server) resyncClient(cl *client) {
	s.mu.Lock()
	snap := s.snapshotLocked()
	ids := allPanes(s.st.win().Root)
	s.mu.Unlock()
	cl.deliver(Msg{Type: "snapshot", Snap: snap})
	s.sendScreens(ids, cl.deliver)
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
		if got.Type == "resp" || got.Type == "replay" || got.Type == "screen" {
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
	cl.deliver(out)
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

	case "replay":
		// The pane's history ring, on demand. Attach no longer pushes it.
		rp := readPaneReplay(s.st.Name, m.Target)
		if !rp.ok {
			s.reply(cl, m, nil, fmt.Errorf("no pane %q", m.Target))
			return
		}
		s.mu.Lock()
		if pc := s.panes[m.Target]; pc != nil {
			pc.alt, pc.mouse = rp.alt, rp.mouse
		}
		s.mu.Unlock()
		cl.deliver(Msg{Type: "replay", Target: m.Target, Data: rp.data, St: &rp.st})
		s.reply(cl, m, map[string]any{"pane": m.Target}, nil)

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

	case "set-rail":
		rail := s.setRail(m.Index)
		s.syncSizes()
		s.pushSnapshot()
		s.reply(cl, m, map[string]any{"rail": rail}, nil)

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

	case "toggle-fold":
		unfolded, err := s.toggleFold(m.Target)
		if err == nil {
			s.pushSnapshot()
		}
		s.reply(cl, m, map[string]any{"window": m.Target, "unfolded": unfolded}, err)

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

	case "rename-workspace":
		name := strings.TrimSpace(m.Name)
		err := s.mutation(func() error { return s.renameWorkspace(m.Target, name) })
		s.afterMutation()
		s.reply(cl, m, map[string]any{"workspace": m.Target, "name": name}, err)

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
	// The tall replay must use the pane's own PTY size, not its layout rect:
	// a pane in an inactive workspace keeps its 80x24 PTY until it is shown.
	s.mu.Lock()
	ps := s.st.Panes[id]
	s.mu.Unlock()
	if ps == nil || ps.Cols <= 0 || ps.Rows <= 0 {
		return nil, fmt.Errorf("no pane %q", id)
	}
	r := Rect{W: ps.Cols, H: ps.Rows}
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
	// Clients rebuild their VT after a layout change; send the screen, then
	// replay the rings as history.
	s.mu.Lock()
	ids := allPanes(s.st.win().Root)
	s.mu.Unlock()
	s.broadcastScreens(ids)
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
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	App           string `json:"app,omitempty"`
	Status        string `json:"status,omitempty"`
	Message       string `json:"message,omitempty"`
	Workspace     string `json:"workspace"`
	WorkspaceName string `json:"workspaceName"`
	Window        string `json:"window"`
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
				info := paneListInfo{ID: id, Workspace: ws.ID, WorkspaceName: ws.Name, Window: w.ID}
				if ps := s.st.Panes[id]; ps != nil {
					info.Name = ps.Name
					info.App = ps.App
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

// renameWorkspace sets the workspace name and nothing else: the id, the active
// index, the windows and the panes stay. A background workspace is fair game.
func (s *server) renameWorkspace(id, name string) error {
	if name == "" {
		return fmt.Errorf("name required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ws := range s.st.Workspaces {
		if ws.ID == id {
			ws.Name = name
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
			r, ok := nodeRect(win.Root, split,
				paneAreaFor(s.cols, s.rows, s.railLocked()))
			if !ok {
				return fmt.Errorf("no rect for split of %q", first)
			}
			horizontal := split.Dir == "row"
			space := r.W
			if !horizontal {
				space = r.H
			}
			// A pane box is border + content + border, so the smallest
			// content a drag may leave is 3 cells, a 5-cell box.
			const minBox = 5
			if space < 2 {
				return fmt.Errorf("split between %q and %q is too small", first, second)
			}
			if space < 2*minBox {
				split.Sizes = []float64{0.5, 0.5}
				return nil
			}
			const eps = 1e-9
			lo := float64(minBox)/float64(space) + eps
			hi := 1 - float64(minBox)/float64(space) + eps
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
	layout(win.Root, paneAreaFor(s.cols, s.rows, s.railLocked()), rects)
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
	win := s.st.win()
	if target == "" {
		target = win.Focus
	}
	ids := allPanes(win.Root)
	if len(ids) <= 1 {
		// Closing the last pane empties the window. Let the pane's exit drive
		// the normal path, which drops the window and ends the session when
		// nothing is left, exactly like a shell exiting.
		pc := s.panes[target]
		s.mu.Unlock()
		if pc == nil {
			return fmt.Errorf("no pane %q", target)
		}
		_ = pc.send(Msg{Action: "kill"})
		return nil
	}
	defer s.mu.Unlock()
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

// windowCwdLocked is the starting directory of a workspace's active pane, or
// the server's own directory when the workspace has none. The caller holds
// s.mu.
func (s *server) windowCwdLocked(ws *Workspace) string {
	if ws != nil && len(ws.Windows) > 0 {
		if ps := s.st.Panes[ws.Windows[ws.Active].Focus]; ps != nil {
			return ps.Cwd
		}
	}
	cwd, _ := os.Getwd()
	return cwd
}

func (s *server) newWindow(wsID, label, cwd string, noFocus bool) (string, string, error) {
	s.mu.Lock()
	ws := s.st.ws()
	src := ""
	if wsID != "" {
		ws = nil
		for _, cand := range s.st.Workspaces {
			if cand.ID == wsID {
				ws = cand
				break
			}
			for _, w := range cand.Windows {
				if findLeaf(w.Root, wsID) {
					ws, src = cand, wsID
					break
				}
			}
			if ws != nil {
				break
			}
		}
		if ws == nil {
			s.mu.Unlock()
			return "", "", fmt.Errorf("no workspace or pane %q", wsID)
		}
	}
	// The new pane opens where the source pane is, or where the active pane of
	// the target workspace is, so the window's folder name and its shell agree.
	if cwd == "" {
		if ps := s.st.Panes[src]; src != "" && ps != nil {
			cwd = ps.Cwd
		}
		if cwd == "" {
			cwd = s.windowCwdLocked(ws)
		}
	}
	wid := s.st.newWindowID()
	w := &Window{ID: wid, Name: label}
	id := w.newPaneID()
	w.Root = &Node{Pane: id}
	w.Focus = id
	if label == "" {
		label = folderName(cwd)
		if label == "" {
			label = strconv.Itoa(len(ws.Windows) + 1)
		}
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
	if cwd == "" {
		cwd = s.windowCwdLocked(s.st.ws())
	}
	if tabLabel == "" {
		tabLabel = folderName(cwd)
		if tabLabel == "" {
			tabLabel = "1"
		}
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
