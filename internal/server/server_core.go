package server

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"dm/internal/box"
	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/notify"
	"dm/internal/paths"
	"dm/internal/session"
	"dm/internal/wire"
)

type paneConn struct {
	id    string
	c     net.Conn
	mu    sync.Mutex
	st    wire.Status
	title string
	alt   bool
	mouse bool
	paste bool
}

func (p *paneConn) send(m wire.Msg) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return wire.WriteMsg(p.c, m)
}

// termColours is the terminal colour pair a client detected, in xterm form.
type termColours struct {
	fg, bg string
}

// outboundBytes bounds what one client may have queued, in bytes. A client
// past the bound is not reading: it gets a fresh picture instead of a backlog.
const outboundBytes = 1 << 20

// reflowSettle is the quiet period after the last width change before the
// server asks the affected panes to reflow. It is a var so a test can shorten
// it.
var reflowSettle = 150 * time.Millisecond

type client struct {
	c    net.Conn
	mu   sync.Mutex
	cond *sync.Cond // wakes the writer; guards the queue with mu

	cols int
	rows int
	// paused holds the queue while the client is being caught up. A paused
	// client queues; the writer does not touch the socket.
	paused bool

	queue  []wire.Msg
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

// deliver queues a frame and reports whether it went into the queue. A frame
// for a closed client, or one bigger than the whole outbound bound, is dropped,
// and a caller that owns a cursor must not advance past it. It never writes the
// socket and never waits on it: a stalled client must not block the pane read
// loop.
func (c *client) deliver(m wire.Msg) bool {
	c.mu.Lock()
	queued := c.enqueueLocked(m)
	c.mu.Unlock()
	return queued
}

// enqueueLocked appends m to the queue under the byte bound and reports whether
// it queued. On overflow it drops the queued "bytes" frames, marks the client
// stale and stops queueing output, so the writer resyncs from the pane screens
// instead of replaying a backlog.
func (c *client) enqueueLocked(m wire.Msg) bool {
	if c.cond == nil {
		c.cond = sync.NewCond(&c.mu)
	}
	if c.closed {
		return false
	}
	if c.stale && m.Type == "bytes" {
		// The client already owes itself a picture. Queueing more output
		// would only rebuild the backlog the overflow threw away.
		return false
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
			return false
		}
	}
	c.queue = append(c.queue, m)
	c.qbytes += n
	c.startWriter()
	c.cond.Broadcast()
	return true
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
func (c *client) popLocked() wire.Msg {
	m := c.queue[0]
	c.queue[0] = wire.Msg{}
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
		if err := wire.WriteMsg(c.c, m); err != nil {
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
func (c *client) next() (wire.Msg, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		if c.closed {
			return wire.Msg{}, false
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
func msgBytes(m wire.Msg) int {
	return 64 + len(m.Data) + len(m.Keys) + len(m.Msg) + len(m.Name) + len(m.Res)
}

type server struct {
	mu      sync.Mutex
	st      *session.State
	cfg     *keymap.Config
	panes   map[string]*paneConn
	clients map[*client]bool
	cols    int
	rows    int
	ln      net.Listener
	ending  bool            // session is shutting down; accept errors are expected
	adopt   map[string]bool // panes loaded from disk: a fresh spawn is a restart

	// floats are the floating command processes, keyed by the client chosen
	// id. They belong to one client and never reach the layout or the
	// snapshot. Guarded by mu.
	floats map[string]*floatProc

	// Git branch labels for the rail, one entry per working directory. The
	// check runs off s.mu and the result arrives with a later snapshot.
	branchMu sync.Mutex
	branches map[string]*branchEntry

	// boxWaiters holds the msg-wait calls blocked right now. Post and clear
	// wake every one; a waiter re-checks under s.mu, so the wakeup is a hint.
	boxWaiters map[*boxWaiter]bool

	// watches is the presence registry: the live reads, keyed by pane and
	// filter. It is memory only. A restart forgets it and the next reads
	// rebuild it, so it never reaches state.json.
	watches map[boxWatchKey]box.Watch

	// zoom is the zoomed pane id, empty when no pane is zoomed. Memory only:
	// a restart shows the splits, so it never reaches state.json.
	zoom string

	// termColours is the pair a client detected from its own terminal. It is
	// nil until the first client reports, so a server that never saw one
	// spawns panes with the compiled-in defaults.
	termColours *termColours

	// spawn starts a pane daemon. It is a field so a test can capture the
	// command line without starting a process; nil means exec's Start.
	spawn func(*exec.Cmd) error

	// statusChangedAt is when each pane's agent state last moved, in unix
	// seconds. It is memory only: a restart has no baseline, and a stale
	// stamp from disk would claim a change nobody saw.
	statusChangedAt map[string]int64

	// titlePushed, titleTimer and titleDirty cap the snapshot a title change
	// alone may push. An animated title names a frame, not news: one push per
	// titlePushEvery, with one trailing push so the last title lands. All three
	// are guarded by mu.
	titlePushed time.Time
	titleTimer  *time.Timer
	titleDirty  bool

	// reflowTimer and reflowWait hold the pending reflow: the timer that fires
	// once the resize goes quiet, and the panes whose width changed. Both are
	// guarded by mu. A drag sends one resize per pointer step, so the set grows
	// and the timer restarts; one settle asks each pane once at its final size.
	reflowTimer *time.Timer
	reflowWait  map[string]bool
}

func Run(args []string) error {
	fs := flag.NewFlagSet("__server", flag.ContinueOnError)
	name := fs.String("s", "main", "session name")
	fs.Bool("headless", true, "unused, kept for clarity")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(paths.SessionDir(*name), "panes"), 0o700); err != nil {
		return err
	}
	lockPath := filepath.Join(paths.SessionDir(*name), "server.lock")
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("session %q already has a server", *name)
	}
	defer func() { _ = lf.Close() }()

	st, err := session.LoadState(*name)
	fromDisk := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if st == nil {
		st = session.NewState(*name)
	}
	adopt := map[string]bool{}
	if fromDisk {
		for id := range st.Panes {
			adopt[id] = true
		}
	}

	cfg, cfgErr := keymap.LoadDefaultPath(false)
	if cfgErr != nil {
		log.Printf("keymap: %v", cfgErr)
	}
	s := &server{
		st: st, cfg: cfg, panes: map[string]*paneConn{}, clients: map[*client]bool{},
		floats: map[string]*floatProc{},
		cols:   st.Cols, rows: st.Rows, adopt: adopt,
		branches: map[string]*branchEntry{}, watches: map[boxWatchKey]box.Watch{},
		statusChangedAt: map[string]int64{},
	}
	// A pending settle must not outlive the accept loop. endSession exits the
	// process before this defer runs, so it stops the timer itself.
	defer s.stopReflow()
	if s.cols < 20 {
		s.cols = 80
	}
	if s.rows < 5 {
		s.rows = 24
	}

	applyDefaultFold(st, cfg)

	win := st.ActiveWindow()
	if win.Root == nil {
		id := win.NewPaneID()
		win.Root = &layout.Node{Pane: id}
		win.Focus = id
		s.addPaneState(id)
	}

	// Start or adopt every pane in the state, all workspaces and windows.
	var ids []string
	for _, ws := range st.Workspaces {
		for _, w := range ws.Windows {
			ids = append(ids, layout.AllPanes(w.Root)...)
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
				pc.st, pc.title, pc.alt, pc.mouse = rp.st, rp.title, rp.alt, rp.mouse
			}
			s.mu.Unlock()
		}
		s.watchPane(id)
	}
	s.syncSizes()
	if err := st.Save(); err != nil {
		return err
	}

	sock := paths.ServerSock(*name)
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	s.ln = ln
	_ = os.Chmod(sock, 0o600)
	writePID(filepath.Join(paths.SessionDir(*name), "server.pid"))

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

// reloadConfig re-reads the operator's config file and swaps it in when the
// read succeeds. The server reads the file because it owns the layout padding
// and the fold a new window gets. A failed read keeps the old config, so a bad
// file never changes the picture.
func (s *server) reloadConfig() error {
	cfg, err := keymap.LoadDefaultPath(false)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	return nil
}

// applyDefaultFold gives every restored window the config's fold state when the
// config carries an explicit default-fold node. An absent node leaves each
// window's saved fold state alone.
func applyDefaultFold(st *session.State, cfg *keymap.Config) {
	if !cfg.DefaultFoldSet() {
		return
	}
	unfolded := cfg.DefaultFold()
	for _, ws := range st.Workspaces {
		for _, w := range ws.Windows {
			w.Unfolded = unfolded
		}
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
	s.st.Panes[id] = &session.PaneState{Shell: shell, Cwd: cwd, Cols: 80, Rows: 24}
}

// paneCommand builds the __pane command for a fresh daemon. A known colour
// rides as --fg or --bg, so the pane answers OSC 10 and OSC 11 with the
// client's terminal instead of the compiled-in default. A half-known pair
// passes only the half it has and leaves the other at its default.
func paneCommand(exe, sess, id string, ps *session.PaneState, tc *termColours) *exec.Cmd {
	args := []string{"__pane", "--id", id, "--session", sess,
		"--cwd", ps.Cwd, "--shell", ps.Shell,
		"--cols", strconv.Itoa(ps.Cols), "--rows", strconv.Itoa(ps.Rows)}
	if tc != nil {
		if tc.fg != "" {
			args = append(args, "--fg", tc.fg)
		}
		if tc.bg != "" {
			args = append(args, "--bg", tc.bg)
		}
	}
	return exec.Command(exe, args...)
}

// ensurePane dials the pane's socket and adopts it, or spawns a fresh daemon.
func (s *server) ensurePane(id string) error {
	sock := paths.PaneSock(s.st.Name, id)
	if c, err := net.DialTimeout("unix", sock, 300*time.Millisecond); err == nil {
		pc := &paneConn{id: id, c: c, st: wire.Status{State: "unknown"}}
		s.mu.Lock()
		s.panes[id] = pc
		tc := s.termColours
		s.mu.Unlock()
		// A daemon that outlived a server keeps the colours it was born with,
		// so hand it the pair a client reported since.
		if tc != nil {
			if err := pc.send(wire.Msg{Action: "term-colours", FG: tc.fg, BG: tc.bg}); err != nil {
				log.Printf("pane %s: term-colours: %v", id, err)
			}
		}
		// A daemon that outlived a server has gone wherever its shell went, so
		// ask it where that is instead of trusting the state file.
		s.setPaneCwd(id, s.liveCwd(id))
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
	tc := s.termColours
	spawn := s.spawn
	s.mu.Unlock()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := paneCommand(exe, s.st.Name, id, ps, tc)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	logPath := filepath.Join(paths.SessionDir(s.st.Name), "panes", id+".log")
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = lf.Close() }()
	cmd.Stdout, cmd.Stderr = lf, lf
	if spawn == nil {
		spawn = func(c *exec.Cmd) error { return c.Start() }
	}
	if err := spawn(cmd); err != nil {
		return err
	}
	_ = cmd.Process.Release()

	for i := 0; i < 100; i++ {
		c, err := net.DialTimeout("unix", sock, 100*time.Millisecond)
		if err == nil {
			pc := &paneConn{id: id, c: c, st: wire.Status{State: "unknown"}}
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
			m, err := wire.ReadMsg(pc.c)
			if err != nil {
				s.paneExited(id)
				return
			}
			switch m.Type {
			case "bytes":
				s.mu.Lock()
				s.broadcastLocked(wire.Msg{Type: "bytes", Target: id, Data: m.Data})
				s.mu.Unlock()
			case "status":
				if m.St != nil {
					from := pc.st.State
					pc.st = *m.St
					to, message := pc.st.State, pc.st.Message
					s.noteStatus(id, *m.St)
					s.noteStatusChange(id, from, to)
					s.pushSnapshot()
					if notify.StateChange(from, to) {
						s.notifyPaneState(id, to, message)
					}
				}
			case "cwd":
				s.setPaneCwd(id, m.Cwd)
			case "title":
				var ti struct {
					Title string `json:"title"`
				}
				_ = json.Unmarshal(m.Res, &ti)
				s.setPaneTitle(id, ti.Title)
			case "mode":
				var mi struct {
					Alt   bool `json:"alt"`
					Mouse bool `json:"mouse"`
					Paste bool `json:"paste"`
				}
				_ = json.Unmarshal(m.Res, &mi)
				s.mu.Lock()
				pc.alt, pc.mouse, pc.paste = mi.Alt, mi.Mouse, mi.Paste
				snap := s.snapshotLocked()
				s.broadcastLocked(wire.Msg{Type: "snapshot", Snap: snap})
				s.mu.Unlock()
			case "exit":
				s.paneExited(id)
				return
			}
		}
	}()
}

func (s *server) broadcastLocked(m wire.Msg) {
	for c := range s.clients {
		c.deliver(m)
	}
}

// connGone reports whether the peer has closed its side of a unix socket. The
// peek leaves the frame stream untouched: a frame waiting to be read is not a
// close, and a socket that is not a unix socket never reports gone.
func connGone(c net.Conn) bool {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return true
	}
	gone := false
	err = raw.Control(func(fd uintptr) {
		var b [1]byte
		n, _, rerr := syscall.Recvfrom(int(fd), b[:], syscall.MSG_PEEK|syscall.MSG_DONTWAIT)
		switch {
		case rerr == syscall.EAGAIN || rerr == syscall.EWOULDBLOCK || rerr == syscall.EINTR:
			// Nothing to read and no hangup: keep waiting.
		case rerr != nil:
			gone = true
		default:
			gone = n == 0
		}
	})
	return gone || err != nil
}

func writePID(path string) {
	_ = os.WriteFile(path, fmt.Appendf(nil, "%d\n", os.Getpid()), 0o600)
}
