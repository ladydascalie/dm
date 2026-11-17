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
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"dm/internal/box"
	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/notify"
	"dm/internal/paths"
	"dm/internal/proxy"
	"dm/internal/secrets"
	"dm/internal/session"
	"dm/internal/wire"
)

type paneConn struct {
	id string
	c  net.Conn
	mu sync.Mutex
	// stm guards st. The pane reader goroutine writes it with no other lock
	// held, so every read goes through status and every write through
	// setStatus; a torn string read would hand a reply a mismatched header.
	stm   sync.Mutex
	st    wire.Status
	title string
	alt   bool
	mouse bool
	paste bool

	// sendTimeout bounds one control-socket write; zero means the package
	// default. A test shortens it to prove a stalled pane fails fast.
	sendTimeout time.Duration

	// degraded marks a conn whose framing tore: a send timed out mid-frame,
	// and a stream socket has no boundary to recover to. Guarded by mu.
	// Permanent for this conn: every send refuses at once, the conn is
	// closed, and the watcher replaces it with a fresh dial to the same
	// daemon.
	degraded bool

	// installedAt is when the watcher adopted this conn, so a redial can
	// tell a conn that never stabilised from one that lived.
	installedAt time.Time
}

// paneStableWindow is how long a conn must live for its redial to count as
// recovered. A conn that dies younger failed to stabilise: a daemon that
// wedges or drops every conn keeps the count climbing, a pane that ran fine
// since the last redial resets it.
const paneStableWindow = 2 * time.Second

// paneWedgeLimit is how many consecutive redials that failed to stabilise a
// pane's watcher tolerates before it declares the daemon wedged and stops.
const paneWedgeLimit = 3

// paneWatch is one pane's watcher bookkeeping, guarded by s.mu.
type paneWatch struct {
	// failed counts the consecutive redials that replaced a conn which was
	// poisoned or died younger than the stable window.
	failed int
	// wedged says the watcher gave up: the daemon keeps the socket but
	// every conn dies or poisons. The pane stays registered; a later
	// secret action or write re-arms the watcher.
	wedged bool
	// watching says a watcher loop is running for this pane, so a re-arm
	// never stacks a second one.
	watching bool
}

// errPaneWedged says the watcher gave up on a wedged daemon: the pane stays
// registered and untouched, the wedge is announced, and an explicit later
// action re-arms the watcher.
var errPaneWedged = errors.New("pane daemon wedged: the watcher stands down")

// errPaneDegraded is the failure every push reports for a poisoned conn, one
// whose framing tore on a timed-out send and which the watcher will replace.
// It names no pane and no value; the caller wraps the pane id around it.
var errPaneDegraded = errors.New("conn degraded: the last send tore the framing")

// paneSendTimeout bounds one write to a pane's control socket. A pane that
// stops reading must not hold a push, or every secret action and pane
// adoption serialised behind it, forever.
const paneSendTimeout = 5 * time.Second

// send writes one frame to the pane's control socket under a write deadline.
// A timeout poisons the conn: a stream socket has no message boundary, so a
// frame torn mid-write cannot be recovered to, and a server-side write
// success after the tear would mean nothing. The conn is degraded for good —
// every later send refuses at once, from any pusher — and the conn closes, so
// the watcher re-dials the same daemon and hands the pane a fresh conn and a
// fresh secret set. The pane stays registered and alive: a transient stall, a
// suspended job backpressuring the pty for one, costs a re-adoption, never
// the pane.
func (p *paneConn) send(m wire.Msg) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.degraded {
		return errPaneDegraded
	}
	timeout := p.sendTimeout
	if timeout == 0 {
		timeout = paneSendTimeout
	}
	if err := p.c.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	err := wire.WriteMsg(p.c, m)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		p.degraded = true
		_ = p.c.Close()
		return err
	}
	return err
}

// isDegraded reports whether the last send timed out.
func (p *paneConn) isDegraded() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.degraded
}

// status returns a copy of the pane's last reported status.
func (p *paneConn) status() wire.Status {
	p.stm.Lock()
	defer p.stm.Unlock()
	return p.st
}

// setStatus records the pane's last reported status.
func (p *paneConn) setStatus(st wire.Status) {
	p.stm.Lock()
	p.st = st
	p.stm.Unlock()
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
	costs  []int // per-queue-frame price, parallel to queue
	qbytes int
	// stale records that the queue overflowed and queued frames were
	// dropped. The writer resyncs from a picture once the queue drains.
	stale bool
	// resync is the server's rebuild hook: one snapshot, then one screen per
	// pane. It is nil until the client attaches.
	resync func()
	// attached says the conn sent its attach frame. It keeps the toggle and
	// its like off the dm exec surface, so no script flips a switch meant
	// for the operator's hand. It is a convenience gate, not a boundary: an
	// agent attacking the dm socket can send the same frame, as it can
	// already type keys and read panes. The real boundary is sandboxing.
	attached bool
	closed   bool
	writer   sync.Once
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
	c.costs = append(c.costs, n)
	c.qbytes += n
	c.startWriter()
	c.cond.Broadcast()
	return true
}

// dropBytesLocked removes every queued "bytes" frame. The caller holds c.mu.
func (c *client) dropBytesLocked() {
	kept := c.queue[:0]
	keptCosts := c.costs[:0]
	for i, m := range c.queue {
		if m.Type == "bytes" {
			c.qbytes -= c.costs[i]
			continue
		}
		kept = append(kept, m)
		keptCosts = append(keptCosts, c.costs[i])
	}
	c.queue = kept
	c.costs = keptCosts
}

// popLocked removes the head frame and clears its slot, so a lagging client
// does not pin the buffers of frames it already sent. The caller holds c.mu.
func (c *client) popLocked() wire.Msg {
	m := c.queue[0]
	c.queue[0] = wire.Msg{}
	c.queue = c.queue[1:]
	c.qbytes -= c.costs[0]
	c.costs = c.costs[1:]
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
	c.costs = nil
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
// A snapshot or screen frame carries its payload in Snap or VTSnap, so both
// are marshalled here to price them: an unpriced snapshot would queue at 64
// bytes against the outbound bound no matter how big it sends as.
func msgBytes(m wire.Msg) int {
	n := 64 + len(m.Data) + len(m.Keys) + len(m.Msg) + len(m.Name) + len(m.Res)
	if m.Snap != nil {
		if b, err := json.Marshal(m.Snap); err == nil {
			n += len(b)
		}
	}
	if m.VTSnap != nil {
		if b, err := json.Marshal(m.VTSnap); err == nil {
			n += len(b)
		}
	}
	return n
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

	// paneSpawn serialises ensurePane per pane id, so two clients racing on
	// one missing pane spawn one daemon and adopt one socket. The map itself
	// is guarded by mu; each mutex is held across that pane's dial, spawn and
	// adopt.
	paneSpawn map[string]*sync.Mutex

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

	// secretPush serialises the redaction pushes: collecting the set and
	// sending it are one step, so a push can never interleave between
	// another push's collection and send and land a stale list on a pane.
	// Sends run off s.mu under it, so a slow pane write delays secret
	// pushes, never the server.
	secretPush sync.Mutex

	// secrets holds the fetched secret values, memory only: the registry
	// never reaches the state file, the logs or a reply. secretActive names
	// the secrets whose values are live, secretPrefixes maps a name to the
	// proxy prefix it serves under, and proxy is the loopback listener the
	// first secret use starts. All four are guarded by mu; nil until the
	// first secret action, so a server that never touches secrets pays
	// nothing. fetch is a field so a test can stub the tool call without
	// putting op on PATH; nil means secrets.Fetch.
	secrets        *secrets.Registry
	secretActive   map[string]bool
	secretPrefixes map[string]string
	proxy          *proxy.Proxy
	fetch          func(*session.Secret) (string, error)

	// secretFiles maps one ingested file's absolute path to the value names
	// its lines donated, in sorted order. A re-run of the same mapping
	// replaces exactly the names it donated before, so a line a mapping
	// dropped stops being redacted. Memory only, guarded by mu, nil until
	// the first sync.
	secretFiles map[string][]string

	// secretForeign holds the live values other sessions' servers asked
	// this one to scrub, by source session name. The source session owns
	// its values: it pushes every change and the withdrawal, and this
	// server folds the set into every push its own panes hear. Memory only,
	// like every secret value; guarded by mu, nil until the first cover.
	secretForeign map[string][]string

	// afterActivate runs inside a secret use, between the activation and
	// the reply, so a test can land a revoke in exactly that window. It is
	// a seam, nil in production.
	afterActivate func()

	// paneWatch holds the per-pane watcher bookkeeping: the consecutive
	// failed redials, the wedge mark and the running-watcher flag. Lazy,
	// guarded by mu.
	paneWatch map[string]*paneWatch

	// termColours is the pair a client detected from its own terminal. It is
	// nil until the first client reports, so a server that never saw one
	// spawns panes with the compiled-in defaults.
	termColours *termColours

	// spawn starts a pane daemon. It is a field so a test can capture the
	// command line without starting a process; nil means exec's Start.
	spawn func(*exec.Cmd) error

	// detect reports git labels for a cwd. It is a field so a test can stub
	// git without touching the package global; nil means detectGit.
	detect func(string) gitInfo

	// reflowSettle overrides the package settle when a test shortens it.
	// Zero keeps the default.
	reflowSettle time.Duration

	// exit ends the process. It is a field so a test can stub the shutdown
	// exit without touching the package global; nil means os.Exit.
	exit func(int)

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
	root := paths.StateDir()
	if err := os.MkdirAll(filepath.Join(paths.SessionDirAt(root, *name), "panes"), 0o700); err != nil {
		return err
	}
	lockPath := filepath.Join(paths.SessionDirAt(root, *name), "server.lock")
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("session %q already has a server", *name)
	}
	defer func() { _ = lf.Close() }()

	st, err := session.LoadStateAt(root, *name)
	fromDisk := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if st == nil {
		st = session.NewStateAt(root, *name)
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
		st: st, cfg: cfg, panes: map[string]*paneConn{}, paneSpawn: map[string]*sync.Mutex{}, clients: map[*client]bool{},
		floats: map[string]*floatProc{},
		cols:   st.Cols, rows: st.Rows, adopt: adopt,
		branches: map[string]*branchEntry{}, watches: map[boxWatchKey]box.Watch{},
		statusChangedAt: map[string]int64{},
	}
	// A pending settle must not outlive the accept loop. endSession exits the
	// process before this defer runs, so it stops the timer itself.
	defer s.stopReflow()

	// SIGTERM ends the server the way a crash would, minus the mess: the
	// listener and the clients close, the socket and the pid file go, and the
	// panes keep running with intact sockets for the next attach to adopt. A
	// signal that lands during startup waits for it, so the handler never
	// tears down a half-spawned session.
	started := make(chan struct{})
	sigTerm := make(chan os.Signal, 1)
	signal.Notify(sigTerm, syscall.SIGTERM)
	go func() {
		<-sigTerm
		select {
		case <-started:
		case <-time.After(10 * time.Second):
		}
		s.terminate()
	}()
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
		rp := readPaneReplay(s.st, id)
		if rp.ok {
			s.mu.Lock()
			if pc := s.panes[id]; pc != nil {
				pc.setStatus(rp.st)
				pc.title, pc.alt, pc.mouse = rp.title, rp.alt, rp.mouse
			}
			s.mu.Unlock()
		}
		s.watchPane(id)
	}
	s.syncSizes()
	// Pin the projects a loaded state already carries before the first save,
	// so a restart hands every client the hues the last run chose.
	s.mu.Lock()
	s.refreshProjectAccentsLocked()
	s.mu.Unlock()
	if err := st.Save(); err != nil {
		return err
	}

	sock := st.ServerSock()
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	s.ln = ln
	_ = os.Chmod(sock, 0o600)
	writePID(filepath.Join(st.SessionDir(), "server.pid"))
	close(started)

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
	ids := make([]string, 0, len(s.st.Panes))
	for id := range s.st.Panes {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	sort.Strings(ids)

	// Hand every pane the new ring budget. A pane that does not answer keeps
	// the budget it was born with and is logged: a dead or wedged pane never
	// fails the reload, so the rest of the config still takes effect.
	budget := cfg.HistoryBytes()
	for _, id := range ids {
		if _, err := paneRequest(s.st, id, wire.Msg{Type: "req", Action: "set-history", History: budget}); err != nil {
			log.Printf("pane %s: set-history: %v", id, err)
		}
	}
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
	s.addPaneStateAt(id, "", nil, nil)
}

func (s *server) addPaneStateAt(id, cwd string, argv, pluginEnv []string) {
	if s.st.Panes[id] != nil {
		return
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	s.st.Panes[id] = &session.PaneState{Shell: os.Getenv("SHELL"), Cwd: cwd, Cols: 80, Rows: 24, Argv: argv, PluginEnv: pluginEnv}
}

// paneCommand builds the __pane command for a fresh daemon. A known colour
// rides as --fg or --bg, so the pane answers OSC 10 and OSC 11 with the
// client's terminal instead of the compiled-in default. A half-known pair
// passes only the half it has and leaves the other at its default.
func paneCommand(exe, sess, id string, ps *session.PaneState, tc *termColours, history int) *exec.Cmd {
	args := []string{
		"__pane", "--id", id, "--session", sess,
		"--cwd", ps.Cwd, "--shell", ps.Shell,
		"--cols", strconv.Itoa(ps.Cols), "--rows", strconv.Itoa(ps.Rows),
		"--history", strconv.Itoa(history),
	}
	if len(ps.Argv) > 0 {
		// The pane flag is comma separated, the shape the state file holds.
		// The server validated every element before it stored it, so the join
		// is the exact program the restart must run.
		args = append(args, "--argv", strings.Join(ps.Argv, ","))
	}
	if len(ps.PluginEnv) > 0 {
		// The pairs are a JSON array, so one flag carries every name without
		// a separator the values could contain. The daemon appends them after
		// the allowlist.
		raw, _ := json.Marshal(ps.PluginEnv)
		args = append(args, "--plugin-env", string(raw))
	}
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
// Callers racing on the same missing pane serialise on the pane's mutex, so
// one daemon comes up and one socket is adopted; the loser's dial then finds
// the winner's registration and closes its own extra connection.
func (s *server) ensurePane(id string) error {
	s.mu.Lock()
	if s.paneSpawn == nil {
		s.paneSpawn = map[string]*sync.Mutex{}
	}
	pm := s.paneSpawn[id]
	if pm == nil {
		pm = &sync.Mutex{}
		s.paneSpawn[id] = pm
	}
	s.mu.Unlock()

	pm.Lock()
	defer pm.Unlock()

	sock := s.st.PaneSock(id)
	if c, err := net.DialTimeout("unix", sock, 300*time.Millisecond); err == nil {
		return s.adoptPane(id, c)
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
	history := s.cfg.HistoryBytes()
	spawn := s.spawn
	s.mu.Unlock()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := paneCommand(exe, s.st.Name, id, ps, tc, history)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	logPath := filepath.Join(s.st.SessionDir(), "panes", id+".log")
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
			return s.adoptPane(id, c)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("pane %s did not come up", id)
}

// adoptPane registers a live dial to the pane's daemon and hands it the
// session's colours and cwd. It never displaces a registered conn: a racing
// caller's fresh dial closes instead, so the one daemon keeps the one socket.
func (s *server) adoptPane(id string, c net.Conn) error {
	s.mu.Lock()
	if s.panes[id] != nil {
		s.mu.Unlock()
		_ = c.Close()
		return nil
	}
	pc := &paneConn{id: id, c: c, st: wire.Status{State: "unknown"}, sendTimeout: paneSendTimeout, installedAt: time.Now()}
	s.panes[id] = pc
	delete(s.paneWatch, id)
	tc := s.termColours
	s.mu.Unlock()
	// A daemon that outlived a server keeps the colours it was born with,
	// so hand it the pair a client reported since.
	if tc != nil {
		if err := pc.send(wire.Msg{Action: "term-colours", FG: tc.fg, BG: tc.bg}); err != nil {
			log.Printf("pane %s: term-colours: %v", id, err)
		}
	}
	// A pane that outlived a server, or one born while values were live,
	// starts with an empty redaction list. Hand it the live set now, so its
	// first published chunk is already scrubbed. The push runs after the
	// registration and under the same ordering lock as every other push, so
	// it can never regress the pane to a stale set.
	s.adoptSecretScrub(id)
	// A daemon that outlived a server has gone wherever its shell went, so
	// ask it where that is instead of trusting the state file.
	s.setPaneCwd(id, s.liveCwd(id))
	return nil
}

// watchPane fans pane output out to clients and tracks status.
func (s *server) watchPane(id string) {
	s.mu.Lock()
	pc := s.panes[id]
	if pc == nil {
		s.mu.Unlock()
		return
	}
	// One loop per pane: a second caller dedupes on the watching flag, so
	// two racing ensures cannot stack two watchers on one conn, double-
	// count toward the wedge cap or fire the wedge signal twice.
	ws := s.paneWatchEntryLocked(id)
	if ws.watching {
		s.mu.Unlock()
		return
	}
	ws.watching = true
	s.mu.Unlock()
	go s.watcherLoop(id, pc)
}

// watcherLoop is one pane's watcher: it reads the conn, and on a lost conn it
// re-dials the same daemon. A prune or an exit frame ends the pane; a wedge
// or a stand-down ends the loop and leaves the pane registered, so an
// explicit later action can re-arm it.
func (s *server) watcherLoop(id string, pc *paneConn) {
	for {
		exit := s.watchConn(id, pc)
		if exit {
			s.paneExited(id)
			s.watchStopped(id)
			return
		}
		// The conn died without an exit frame. The daemon survives the
		// loss of one conn: its serve loop is per conn, so a torn
		// stream or a dropped socket is a lost connection, not a pane
		// exit. While the daemon's socket file still exists, replace
		// the conn and keep watching; only a gone socket or a failed
		// replacement prunes the pane.
		next, err := s.redialPane(id, pc)
		if err != nil {
			if errors.Is(err, errPaneGone) {
				s.paneExited(id)
			}
			// A wedge stands the watcher down: the pane stays registered,
			// the signal went out, and an explicit later action re-arms.
			s.watchStopped(id)
			return
		}
		if next == nil {
			// Another conn owns the pane now; its watcher watches.
			s.watchStopped(id)
			return
		}
		pc = next
	}
}

// paneWatchEntryLocked returns one pane's watcher bookkeeping, building it on
// first use. The caller holds s.mu.
func (s *server) paneWatchEntryLocked(id string) *paneWatch {
	if s.paneWatch == nil {
		s.paneWatch = map[string]*paneWatch{}
	}
	ws := s.paneWatch[id]
	if ws == nil {
		ws = &paneWatch{}
		s.paneWatch[id] = ws
	}
	return ws
}

// watchStopped marks a pane's watcher loop as ended. The wedge mark, if any,
// stays until an explicit action re-arms.
func (s *server) watchStopped(id string) {
	s.mu.Lock()
	if ws := s.paneWatch[id]; ws != nil {
		ws.watching = false
	}
	s.mu.Unlock()
}

// rearmPaneWatcher restarts a wedged pane's capped watcher when an explicit
// action talks to the pane again: a secret action that reported the pane, a
// write, a resize. The loop is capped again, so a still-wedged daemon earns
// one more signal and stops; the pane's process is never touched either way.
func (s *server) rearmPaneWatcher(id string) {
	s.mu.Lock()
	pc := s.panes[id]
	ws := s.paneWatchEntryLocked(id)
	if !ws.wedged || ws.watching || pc == nil {
		s.mu.Unlock()
		return
	}
	ws.wedged = false
	ws.failed = 0
	ws.watching = true
	s.mu.Unlock()
	go s.watcherLoop(id, pc)
}

// watchConn reads one conn's frames until the peer goes away or the pane
// reports its own exit. It reports whether the pane exited.
func (s *server) watchConn(id string, pc *paneConn) (exit bool) {
	for {
		m, err := wire.ReadMsg(pc.c)
		if err != nil {
			return false
		}
		switch m.Type {
		case "bytes":
			s.mu.Lock()
			s.broadcastLocked(wire.Msg{Type: "bytes", Target: id, Data: m.Data})
			s.mu.Unlock()
		case "scrub":
			// The pane rewrote its ring and its picture after a redaction
			// toggle came back on. The snapshot is the repaired screen, so
			// every client repaints from it, and the frame is also the cue
			// to drop a cached scrollback ring: the pane's copy is the only
			// one that was scrubbed.
			s.mu.Lock()
			s.broadcastLocked(wire.Msg{Type: "scrub", Target: id, VTSnap: m.VTSnap})
			s.mu.Unlock()
		case "status":
			if m.St != nil {
				from := pc.status().State
				pc.setStatus(*m.St)
				to, message := m.St.State, m.St.Message
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
			return true
		}
	}
}

// errPaneGone says the daemon's socket is gone or refuses connections, so
// the pane behind it is dead and the caller prunes.
var errPaneGone = errors.New("pane socket is gone")

// redialPane replaces a failed conn with a fresh dial to the same daemon,
// carrying the old conn's view state across. It returns the new conn, or
// errPaneGone when the socket is gone or refuses connections, or a nil conn
// with no error when another conn already owns the pane and this watcher
// stands down.
func (s *server) redialPane(id string, old *paneConn) (*paneConn, error) {
	sock := s.st.PaneSock(id)
	if _, err := os.Stat(sock); err != nil {
		return nil, errPaneGone
	}
	// The ownership check wins first, under the lock, so two watchers on
	// one conn cannot both count the same redial toward the cap.
	s.mu.Lock()
	cur := s.panes[id]
	if cur != old {
		// The pane already left the map, or another conn owns it: its
		// handler ran or runs elsewhere, and this watcher stands down
		// without a second prune.
		s.mu.Unlock()
		_ = old.c.Close()
		return nil, nil
	}
	// A replaced conn that was poisoned or died younger than the stable
	// window failed to stabilise, and three in a row mean the daemon is
	// wedged. A conn that lived resets the count. The count is tentative
	// until the dial wins ownership; the winner commits it after the dial.
	oldFailed := old.isDegraded() || time.Since(old.installedAt) < paneStableWindow
	ws := s.paneWatchEntryLocked(id)
	failed := ws.failed
	if oldFailed {
		failed++
	} else {
		failed = 0
	}
	if failed >= paneWedgeLimit {
		// The watcher stands down: the pane keeps running and stays
		// registered with its poisoned conn, so every push reports it, and
		// the operator learns it once. No push timeout ever reaches
		// reaping, and the daemon's process is never touched.
		already := ws.wedged
		ws.wedged = true
		s.mu.Unlock()
		_ = old.c.Close()
		if !already {
			s.notifyPaneWedge(id)
		}
		return nil, errPaneWedged
	}
	s.mu.Unlock()
	_ = old.c.Close()
	var c net.Conn
	var err error
	for i := 0; i < 20; i++ {
		if i > 0 {
			time.Sleep(100 * time.Millisecond)
			if _, serr := os.Stat(sock); serr != nil {
				return nil, errPaneGone
			}
		}
		c, err = net.DialTimeout("unix", sock, time.Second)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, errPaneGone
	}
	s.mu.Lock()
	cur = s.panes[id]
	if cur != old {
		// The dial lost the race for ownership: the winner counts its own
		// redial, and this watcher stands down without a second prune.
		s.mu.Unlock()
		_ = c.Close()
		return nil, nil
	}
	// The count commits here, for the owner alone.
	s.paneWatchEntryLocked(id).failed = failed
	nc := &paneConn{
		id: id, c: c, st: old.status(), title: old.title,
		alt: old.alt, mouse: old.mouse, paste: old.paste,
		sendTimeout: paneSendTimeout, installedAt: time.Now(),
	}
	s.panes[id] = nc
	tc := s.termColours
	s.mu.Unlock()
	healthy := true
	if tc != nil {
		if err := nc.send(wire.Msg{Action: "term-colours", FG: tc.fg, BG: tc.bg}); err != nil {
			log.Printf("pane %s: term-colours: %v", id, err)
			// A conn poisoned during adoption is dead: skip the rest of
			// the hand-shake and let the loop re-dial, one count at a
			// time, until the cap.
			healthy = !errors.Is(err, errPaneDegraded)
		}
	}
	if healthy {
		// The fresh conn starts with an empty redaction list, like any
		// adopt.
		s.adoptSecretScrub(id)
		// A daemon that outlived a conn has gone wherever its shell went.
		s.setPaneCwd(id, s.liveCwd(id))
		// The conn gap may have dropped bytes: give every client the
		// pane's screen and ring again, so what they draw matches the
		// daemon.
		s.resync()
	}
	return nc, nil
}

// notifyPaneWedge emits the one operator-visible signal a wedge earns: the
// pane's notify box entry, the same path a pane's state change takes, which
// also drives the desktop notification. The message names the pane and the
// remedy, never a value.
func (s *server) notifyPaneWedge(id string) {
	s.notifyPaneState(id, "error", fmt.Sprintf(
		"pane %s stopped taking server pushes; the shell was running when the watcher stood down, and writing to the pane or using a secret again re-arms the watcher", id))
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
