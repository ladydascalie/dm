package pane

import (
	"bytes"
	"encoding/base64"
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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"dm/internal/osc"
	"dm/internal/paths"
	"dm/internal/vt"
	"dm/internal/wire"

	"github.com/creack/pty"
)

// paneProc is one pane daemon: it owns the PTY, the VT screen, the status and
// the scroll ring. The session server comes and goes; this process does not.
type paneProc struct {
	id      string
	session string
	sock    string
	shell   string
	cwd     string // where the shell is now, not where it started; guarded by mu
	title   string // the last OSC window title reported; guarded by mu

	ptmx *os.File
	cmd  *exec.Cmd

	mu   sync.Mutex
	vt   *vt.Term
	ring *scrollRing
	st   wire.Status
	subs map[*subConn]bool
	dead bool
	// flushTimer places the parser's pending cluster once the pane's output
	// stops, and flushGen makes a timer that a later read superseded a no-op.
	// Both are guarded by mu.
	flushTimer *time.Timer
	flushGen   uint64
	mode       vt.Modes
	// fg and bg are the terminal colours this pane answers OSC 10 and OSC 11
	// with. The server sets them from the client's detection; guarded by mu.
	fg, bg string

	// produced counts the bytes the pty has produced, after the pane's filter.
	// anchor is the value of produced when the last Enter was written, so it is
	// a position in the same stream the ring holds. echo is the command text
	// that Enter carried, so a yank can drop the line the shell echoed back.
	// anchorSet is false until an Enter arrives. All are guarded by mu.
	produced  int64
	anchor    int64
	anchorSet bool
	echo      []byte

	// waiter is the pane's one in-flight wait action, nil when none runs. A
	// second wait request is refused until it ends. Guarded by mu.
	waiter *waitReq

	// scrub redacts secret values from the pane's output before it reaches
	// the screen, the ring or a subscriber. The server pushes the values
	// with set-secrets. carry belongs to the pty reader goroutine alone.
	scrub scrubber

	// children samples a pid's foreground children for the wait fallback; nil
	// uses the real /proc read. It is a field, not a package var, so a test's
	// stand-in lives on its own pane and parallel tests share no global.
	// Set once before the serve loop runs; read-only afterwards.
	children func(int) []int

	// Outbound pty writes queue on one dedicated writer goroutine, so a pty
	// nobody reads never blocks the serve loop and the control socket drains
	// at wire speed whatever the foreground job does. queueWrite creates
	// writes the first time it runs. The queue holds at most ptyQueueDepth
	// frames; a write that finds it full is dropped and counted in ptyDropped.
	// The frames a suspended job cannot take are exactly the frames worth
	// dropping: keystrokes pile up behind a job that will never read them,
	// and an unbounded backlog would grow the daemon's heap and delay every
	// later write. ptyDropped is atomic because the writer goroutine and every
	// serve loop touch it without the pane lock.
	writes     chan ptyWrite
	ptyDropped atomic.Int64
	writer     sync.Once

	// subEvicted counts the subscribers evicted for not reading: a queue
	// past its byte bound, or a send that failed or outlived its deadline.
	// Atomic because the subscriber writers and every serve loop touch it
	// without the pane lock.
	subEvicted atomic.Int64
}

// ptyWrite is one queued pty write: the payload bytes and, for a pane write
// action, whether the Enter key follows them as a carriage return.
type ptyWrite struct {
	b     []byte
	enter bool
}

// ptyQueueDepth bounds the pty writer's queue. Keystrokes arrive a frame at
// a time, so a queue this deep never drops on a live terminal; only a pty
// whose reader is gone or suspended can fill it, and there the drops are
// the honest answer.
const ptyQueueDepth = 256

// resolveShell picks the shell a pane runs: the caller's explicit value when
// it gave one, else $SHELL, else /bin/sh. The explicit argument keeps tests
// off $SHELL, which t.Setenv would force serial.
func resolveShell(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

// waitReq is one wait action: the connection the frames stream to, the
// request id, the stream position the wait's output starts at and whether the
// shell has integration marks, which decides between the mark and the
// foreground-children completion. done closes when the wait ends or the
// waiter disconnects, which stops the children poll. childSeen and looseStart
// belong to the poll goroutine: the spawner sets them before it starts, and
// childSeen then records that a sample found a foreground child. looseStart
// marks a wait whose mark path a reflow took away, where an empty child
// sample alone ends the wait.
type waitReq struct {
	sc         *subConn
	id         int
	anchor     int64
	mark       bool
	childSeen  bool
	looseStart bool
	done       chan struct{}
}

// echoMax is the longest command text a pane keeps to drop its echo from a
// yank. A longer write is a pasted block, not a command line.
const echoMax = 4096

// modeInfo reports the modes the client must respect: alternate screen (no
// terminal scrollback), mouse reporting (the wheel belongs to the app) and
// bracketed paste (a paste arrives framed by the 200~ and 201~ markers).
func modeInfo(m vt.Modes) (alt, mouse, paste bool) {
	return m.AltScreen, m.Mouse != vt.MouseOff, m.BracketedPaste
}

// screenText returns the visible rows joined by a newline, the shape the read
// action has always sent. Trailing blank rows are kept; splitLines trims them.
func screenText(t *vt.Term) string {
	g := t.Grid()
	_, rows := g.Size()
	lines := make([]string, rows)
	for y := 0; y < rows; y++ {
		lines[y] = g.Text(y)
	}
	return strings.Join(lines, "\n")
}

func modeRes(alt, mouse, paste bool) json.RawMessage {
	return jsonRaw(map[string]any{"alt": alt, "mouse": mouse, "paste": paste})
}

// subSendTimeout bounds one write to a subscriber. A subscriber that stops
// reading must not hold a frame, or the broadcasts behind it, forever; the
// bound matches the server's own paneSendTimeout, its client-side twin.
const subSendTimeout = 5 * time.Second

// subQueueBytes bounds what one subscriber may have queued, in bytes. A
// subscriber past the bound is not reading: it is evicted instead of handed a
// backlog, the same bound and answer the server gives its own clients.
const subQueueBytes = 1 << 20

// subConn is one connection the pane streams to. Frames queue on a bounded
// per-connection queue and one writer goroutine owns the socket, so a
// subscriber that stops reading never blocks the serve loop or the pane lock:
// the queue takes the frames until its byte bound, and past the bound, or on
// a write that outlives its deadline, the subscriber is evicted and its conn
// closes. It may reconnect and starts fresh.
type subConn struct {
	p *paneProc
	c net.Conn

	mu     sync.Mutex
	cond   *sync.Cond // wakes the writer; guards the queue with mu
	q      []wire.Msg
	qbytes int
	closed bool
	writer sync.Once

	// sendTimeout bounds one write to the socket; zero uses subSendTimeout.
	// A field, not a package var, so a test's stand-in lives on its own conn.
	sendTimeout time.Duration
}

// send queues one frame and reports whether it went into the queue. A frame
// for a closed or evicted conn returns false. A frame that would push a
// non-empty queue past its byte bound evicts the subscriber instead: the conn
// closes, the writer stops and the caller moves on. A single frame bigger
// than the whole bound still ships on an empty queue, so a full ring replay
// reaches a fresh subscriber under the write deadline.
func (s *subConn) send(m wire.Msg) bool {
	n := msgBytes(m)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	if s.qbytes+n > subQueueBytes && len(s.q) > 0 {
		s.mu.Unlock()
		s.evict()
		return false
	}
	if s.cond == nil {
		s.cond = sync.NewCond(&s.mu)
	}
	s.q = append(s.q, m)
	s.qbytes += n
	s.writer.Do(func() { go s.writeLoop() })
	s.cond.Broadcast()
	s.mu.Unlock()
	return true
}

// popLocked removes the head frame and clears its slot, so a lagging
// subscriber does not pin the buffers of frames it already sent. The caller
// holds s.mu.
func (s *subConn) popLocked() wire.Msg {
	m := s.q[0]
	s.q[0] = wire.Msg{}
	s.q = s.q[1:]
	s.qbytes -= msgBytes(m)
	return m
}

// writeLoop is the one goroutine that owns a subscriber's socket. It takes
// queued frames in order and writes each under a deadline; a write that fails
// or outlives the deadline evicts the subscriber.
func (s *subConn) writeLoop() {
	for {
		s.mu.Lock()
		for len(s.q) == 0 && !s.closed {
			if s.cond == nil {
				s.cond = sync.NewCond(&s.mu)
			}
			s.cond.Wait()
		}
		closed := s.closed
		var m wire.Msg
		if !closed {
			m = s.popLocked()
		}
		s.mu.Unlock()
		if closed {
			return
		}
		timeout := s.sendTimeout
		if timeout == 0 {
			timeout = subSendTimeout
		}
		_ = s.c.SetWriteDeadline(time.Now().Add(timeout))
		if err := wire.WriteMsg(s.c, m); err != nil {
			s.mu.Lock()
			closed = s.closed
			s.mu.Unlock()
			// The conn closing is also how a shutdown or an eviction from
			// another path ends this write; only a failure on a live conn
			// is an eviction.
			if !closed {
				s.evict()
			}
			return
		}
	}
}

// evict closes the conn and drops the subscriber from the pane. Idempotent:
// the first caller wins. The pane bookkeeping runs off the caller's locks, so
// a broadcast that evicts under the pane lock does not take it again.
func (s *subConn) evict() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	if s.cond != nil {
		s.cond.Broadcast()
	}
	s.q = nil
	s.qbytes = 0
	s.mu.Unlock()
	_ = s.c.Close()
	if s.p == nil {
		return
	}
	go s.p.evictSub(s)
}

// shutdown marks a subscriber gone after its serve loop ended: the writer
// wakes, drops its queue and returns. A disconnect is not an eviction, so it
// counts nothing.
func (s *subConn) shutdown() {
	s.mu.Lock()
	s.closed = true
	if s.cond != nil {
		s.cond.Broadcast()
	}
	s.q = nil
	s.qbytes = 0
	s.mu.Unlock()
	_ = s.c.Close()
}

// msgBytes counts what a frame costs on the wire, near enough to bound it. A
// screen frame carries its payload in VTSnap, so it is marshalled here to
// price it: an unpriced snapshot would queue at 64 bytes against the bound no
// matter how big it sends as.
func msgBytes(m wire.Msg) int {
	n := 64 + len(m.Data) + len(m.Keys) + len(m.Msg) + len(m.Res)
	for _, l := range m.Lines {
		n += len(l) + 1
	}
	if m.VTSnap != nil {
		if b, err := json.Marshal(m.VTSnap); err == nil {
			n += len(b)
		}
	}
	return n
}

// The ring budget bounds. ringDefault is the 512 KiB scrollback a pane has
// always carried; the config may ask for anything from ringMin to ringMax.
const (
	ringDefault = 512 << 10
	ringMin     = 64 << 10
	ringMax     = 8 << 20
)

// memHeadroom is the heap a pane may use above its ring: one screen, the VT
// parser and a shell's output.
const memHeadroom = 32 << 20

// flushIdle is how long a pane's output must be quiet before the parser's
// pending grapheme cluster is placed. The parser holds a cluster until the
// next rune proves it complete, so a program that writes once and waits would
// otherwise leave the last character off the grid until it writes again.
const flushIdle = 16 * time.Millisecond

// scrollRing is the pane's history: a circular buffer over a fixed budget.
// Writing wraps in place, so a burst never grows the daemon's heap and no trim
// leaves a larger backing array behind. A resize copies the newest bytes into a
// fresh buffer, so the ring never holds more than its budget.
//
// Locking is the caller's: paneProc.mu guards the ring.
type scrollRing struct {
	buf []byte
	n   int // bytes held, at most len(buf)
	w   int // next byte to overwrite
}

// NewRing returns a ring that holds at most budget bytes.
func NewRing(budget int) *scrollRing {
	return &scrollRing{buf: make([]byte, budget)}
}

// Budget is the number of bytes the ring may hold.
func (r *scrollRing) Budget() int { return len(r.buf) }

// Resize gives the ring a new budget. The newest min(held, budget) bytes survive
// in a fresh buffer, oldest first: a shrink keeps the tail, a grow keeps
// everything. A budget of zero or less is refused and leaves the ring alone.
func (r *scrollRing) Resize(budget int) {
	if budget <= 0 {
		return
	}
	keep := min(r.n, budget)
	next := make([]byte, budget)
	if keep > 0 {
		size := len(r.buf)
		start := (r.w - r.n + size) % size
		start = (start + r.n - keep) % size
		end := start + keep
		if end <= size {
			copy(next, r.buf[start:end])
		} else {
			n := copy(next, r.buf[start:])
			copy(next[n:], r.buf[:end-size])
		}
	}
	r.buf, r.n, r.w = next, keep, keep%budget
}

// Write appends b, dropping the oldest bytes once the ring is full.
func (r *scrollRing) Write(b []byte) {
	size := len(r.buf)
	if size == 0 {
		return
	}
	if len(b) >= size {
		copy(r.buf, b[len(b)-size:])
		r.n, r.w = size, 0
		return
	}
	n := copy(r.buf[r.w:], b)
	r.w = (r.w + n) % size
	if rest := b[n:]; len(rest) > 0 {
		n = copy(r.buf[r.w:], rest)
		r.w = (r.w + n) % size
	}
	if r.n += len(b); r.n > size {
		r.n = size
	}
}

// Len is the number of bytes held.
func (r *scrollRing) Len() int { return r.n }

// Bytes copies the ring out, oldest first. The reply owns the copy; the ring
// stays fixed.
func (r *scrollRing) Bytes() []byte {
	return r.appendTo(nil)
}

// appendTo appends the ring to dst, oldest first. A caller that keeps a
// scratch slice reuses it instead of allocating the ring again.
func (r *scrollRing) appendTo(dst []byte) []byte {
	dst = dst[:0]
	if r.n == 0 {
		return dst
	}
	size := len(r.buf)
	start := (r.w - r.n + size) % size
	end := start + r.n
	if end <= size {
		return append(dst, r.buf[start:end]...)
	}
	dst = append(dst, r.buf[start:]...)
	return append(dst, r.buf[:end-size]...)
}

// at returns the byte at logical index i, where 0 is the oldest byte held.
func (r *scrollRing) at(i int) byte {
	size := len(r.buf)
	return r.buf[(r.w-r.n+i+size)%size]
}

// indexByte returns the logical index of the first c at or after from, or -1.
func (r *scrollRing) indexByte(c byte, from int) int {
	for i := from; i < r.n; i++ {
		if r.at(i) == c {
			return i
		}
	}
	return -1
}

// slice copies logical bytes [from, to) into a new slice, oldest first.
func (r *scrollRing) slice(from, to int) []byte {
	if from < 0 {
		from = 0
	}
	if to > r.n {
		to = r.n
	}
	if from >= to {
		return []byte{}
	}
	size := len(r.buf)
	start := (r.w - r.n + from + size) % size
	out := make([]byte, to-from)
	n := copy(out, r.buf[start:])
	if n < len(out) {
		copy(out[n:], r.buf[:])
	}
	return out
}

// paneOpts is the parsed command line of a pane daemon.
type paneOpts struct {
	id, session, cwd, shell, fg, bg string
	cols, rows, history             int
	// argv is the parsed --argv program and arguments, nil when the pane runs
	// a shell. argvRaw is the comma separated flag it came from.
	argvRaw string
	argv    []string
}

// historyBudget is the one rule the pane flag and the set-history action share:
// the config may ask for a ring between ringMin and ringMax, and nothing else.
func historyBudget(budget int) error {
	if budget < ringMin || budget > ringMax {
		return fmt.Errorf("history budget %d bytes is outside %d..%d", budget, ringMin, ringMax)
	}
	return nil
}

// splitArgv parses a pane's --argv value: a comma separated program and
// arguments. Every element must be non-empty and free of NUL bytes, the two
// values exec and the state round trip cannot carry. An empty value means no
// program: the pane runs its shell.
func splitArgv(s string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for i, p := range parts {
		if p == "" {
			return nil, fmt.Errorf("--argv element %d is empty", i+1)
		}
		if strings.ContainsRune(p, '\x00') {
			return nil, fmt.Errorf("--argv element %d contains a NUL byte", i+1)
		}
		out = append(out, p)
	}
	return out, nil
}

// paneCmd builds the command a pane runs: the --argv program when one was
// given, else the shell. Either way the pane is told its own session through
// DM_PANE and DM_SESSION, so a program started direct still posts to the pane
// and the session it belongs to.
func paneCmd(o *paneOpts) *exec.Cmd {
	cmd := exec.Command(o.shell, "-l")
	if len(o.argv) > 0 {
		cmd = exec.Command(o.argv[0], o.argv[1:]...)
	}
	cmd.Dir = o.cwd
	// The env is built by rule, not inherited whole: the daemon holds the
	// server's environment and none of its credentials may reach the pane.
	// paneEnv documents the strip rule.
	cmd.Env = paneEnv(os.Environ(), o.id, o.session)
	return cmd
}

// parsePaneArgs parses and validates the pane's command line before Run touches
// a pty or a socket.
func parsePaneArgs(args []string) (*paneOpts, error) {
	fs := flag.NewFlagSet("__pane", flag.ContinueOnError)
	o := &paneOpts{}
	fs.StringVar(&o.id, "id", "", "pane id")
	fs.StringVar(&o.session, "session", "", "session name")
	fs.StringVar(&o.cwd, "cwd", "", "working directory")
	fs.StringVar(&o.shell, "shell", "", "shell")
	fs.StringVar(&o.argvRaw, "argv", "", "program and arguments, comma separated")
	fs.IntVar(&o.cols, "cols", 80, "columns")
	fs.IntVar(&o.rows, "rows", 24, "rows")
	fs.StringVar(&o.fg, "fg", osc.TermFG, "terminal foreground colour")
	fs.StringVar(&o.bg, "bg", osc.TermBG, "terminal background colour")
	fs.IntVar(&o.history, "history", ringDefault, "scrollback ring budget in bytes")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if o.id == "" || o.session == "" {
		return nil, fmt.Errorf("pane needs --id and --session")
	}
	if o.fg == "" || o.bg == "" {
		return nil, fmt.Errorf("pane needs a non-empty --fg and --bg")
	}
	if err := historyBudget(o.history); err != nil {
		return nil, err
	}
	argv, err := splitArgv(o.argvRaw)
	if err != nil {
		return nil, err
	}
	o.argv = argv
	return o, nil
}

func Run(args []string) error {
	o, err := parsePaneArgs(args)
	if err != nil {
		return err
	}
	shell := resolveShell(o.shell)
	if o.cwd == "" {
		o.cwd, _ = os.Getwd()
	}

	// A pane holds one screen, its ring and a shell's output. The budget's
	// headroom makes the GC return a burst's pages instead of keeping them.
	// GOGC stays at its default: lowering it raised the idle figure.
	debug.SetMemoryLimit(int64(o.history) + memHeadroom)

	p := &paneProc{
		id: o.id, session: o.session, sock: paths.PaneSock(o.session, o.id),
		shell: shell, cwd: o.cwd,
		vt:   vt.NewTerm(o.cols, o.rows),
		ring: NewRing(o.history),
		subs: map[*subConn]bool{},
		st:   wire.Status{State: "unknown"},
		fg:   o.fg, bg: o.bg,
	}

	cmd := paneCmd(o)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(o.cols), Rows: uint16(o.rows)})
	if err != nil {
		return fmt.Errorf("start shell: %w", err)
	}
	p.ptmx, p.cmd = ptmx, cmd

	if err := os.MkdirAll(paths.SessionDir(o.session)+"/panes", 0o700); err != nil {
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
	go p.watchShell()
	go func() {
		_ = cmd.Wait()
		p.shutdown()
		time.Sleep(2 * time.Second) // let attached clients read the exit
		_ = os.Remove(p.sock)
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
	var clean, scrubbed bytes.Buffer
	filter := p.newFilter()
	for {
		n, err := p.ptmx.Read(buf)
		if n > 0 {
			p.syncFilter(filter)
			clean.Reset()
			scrubbed.Reset()
			filter.Feed(buf[:n], &clean)
			// The scrub stage is the last filter: everything downstream, the
			// screen, the ring, the broadcasts and every read-back, sees
			// redacted bytes and no secret lands in scrollback.
			p.scrub.feed(clean.Bytes(), &scrubbed)
			b := append([]byte(nil), scrubbed.Bytes()...)
			if len(b) > 0 {
				p.mu.Lock()
				p.publishLocked(b)
				p.mu.Unlock()
			}
		}
		if err != nil {
			// The stream is over, so the scrub stage can let go of the bytes
			// it held back for a straddled match.
			scrubbed.Reset()
			p.scrub.flush(&scrubbed)
			if b := append([]byte(nil), scrubbed.Bytes()...); len(b) > 0 {
				p.mu.Lock()
				p.publishLocked(b)
				p.mu.Unlock()
			}
			p.mu.Lock()
			p.dead = true
			if sc, reply, ok := p.waitEndLocked(); ok {
				_ = sc.send(reply)
			}
			p.broadcastLocked(wire.Msg{Type: "exit"})
			p.mu.Unlock()
			return
		}
	}
}

// newFilter builds the OSC filter with the pane's current colour pair.
func (p *paneProc) newFilter() *osc.Filter {
	p.mu.Lock()
	defer p.mu.Unlock()
	return &osc.Filter{
		Status: p.setStatus,
		Query:  p.replyQuery,
		Reply:  p.replyTerminal,
		Cursor: p.cursorPos,
		FG:     p.fg,
		BG:     p.bg,
	}
}

// syncFilter refreshes the filter's colour pair from the guarded fields, so a
// term-colours action reaches the next read and a long-lived filter never
// answers with a stale pair.
func (p *paneProc) syncFilter(f *osc.Filter) {
	p.mu.Lock()
	f.FG, f.BG = p.fg, p.bg
	p.mu.Unlock()
}

// publishLocked feeds one clean chunk to the screen, the ring and the
// subscribers, and arms the quiet flush. The caller holds p.mu.
func (p *paneProc) publishLocked(b []byte) {
	oldTransitions := p.vt.Grid().AltTransitions()
	p.vt.Write(b)
	p.ring.Write(b)
	p.produced += int64(len(b))
	p.broadcastLocked(wire.Msg{Type: "bytes", Data: b})
	p.armFlushLocked()
	m := p.vt.Grid().Modes()
	if p.vt.Grid().AltTransitions() != oldTransitions {
		// An alternate-screen excursion swaps the picture and may scroll rows
		// that are not the shell's history. Drop the anchor on enter and on
		// exit: the next command's Enter records a fresh one, and a copy before
		// that falls back to the screen instead of spanning the excursion. A
		// counter notices an excursion that entered and left inside one chunk;
		// the chunk-boundary mode comparison would miss it.
		p.anchor, p.anchorSet, p.echo = 0, false, nil
	}
	if m != p.mode {
		p.mode = m
		alt, mouse, paste := modeInfo(m)
		p.broadcastLocked(wire.Msg{Type: "mode", Res: modeRes(alt, mouse, paste)})
	}
	// A publish that completed the wait sends its final reply here, under the
	// pane lock like every broadcast, so it lands after the bytes this
	// publish delivered and before anything produced later.
	if sc, reply, ok := p.waitEndLocked(); ok {
		_ = sc.send(reply)
	}
}

// armFlushLocked restarts the quiet timer. The caller holds p.mu. A timer that
// a later read superseded sees a newer generation and places nothing.
func (p *paneProc) armFlushLocked() {
	if p.flushTimer != nil {
		p.flushTimer.Stop()
	}
	p.flushGen++
	gen := p.flushGen
	p.flushTimer = time.AfterFunc(flushIdle, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.flushGen != gen {
			return
		}
		p.vt.Flush()
	})
}

// evictSub counts one eviction and drops the subscriber the way its serve
// loop's cleanup would: out of the map, and out of a wait it may hold.
func (p *paneProc) evictSub(s *subConn) {
	p.subEvicted.Add(1)
	p.mu.Lock()
	delete(p.subs, s)
	if p.waiter != nil && p.waiter.sc == s {
		close(p.waiter.done)
		p.waiter = nil
	}
	p.mu.Unlock()
}

// subEvictions reports how many subscribers were evicted for not reading.
func (p *paneProc) subEvictions() int64 { return p.subEvicted.Load() }

func (p *paneProc) broadcastLocked(m wire.Msg) {
	for sc := range p.subs {
		_ = sc.send(m)
	}
}

// replyQuery answers the OSC 7501 feature probe with the pane's current
// status in the wire format ParseStatus accepts, written into the PTY, which
// is the terminal-to-application channel. The pane is the program's terminal,
// so the status it displays is the answer.
func (p *paneProc) replyQuery() {
	p.mu.Lock()
	st := p.st
	p.mu.Unlock()
	p.queueWrite([]byte(formatStatusQuery(st)), false)
}

// formatStatusQuery renders the answer to an OSC 7501 feature probe. The
// leading ? echoes the probe, which is the token a status-aware program
// matches to decide the terminal speaks the protocol; the pairs that follow
// are the pane's current status in the wire format ParseStatus accepts.
func formatStatusQuery(st wire.Status) string {
	s := formatStatus(st)
	return "\x1b]7501;?" + s[len("\x1b]7501;"):]
}

// formatStatus renders a status in the OSC 7501 wire format:
// state=working:app=pi:msg=<b64>. A stateless status reports unknown, the
// state a pane no program has named yet displays.
func formatStatus(st wire.Status) string {
	state := st.State
	if state == "" {
		state = "unknown"
	}
	s := "state=" + state
	if st.App != "" {
		s += ":app=" + st.App
	}
	if st.Message != "" {
		s += ":msg=" + base64.StdEncoding.EncodeToString([]byte(st.Message))
	}
	return "\x1b]7501;" + s + "\x1b\\"
}

// replyTerminal writes a terminal reply into the PTY, which is the app's input.
func (p *paneProc) replyTerminal(s string) {
	p.queueWrite([]byte(s), false)
}

// queueWrite hands one pty write to the writer goroutine and never blocks.
// The queue overflows only while the pty's reader is gone or suspended, and
// the frame it cannot take is exactly the frame worth dropping: a keystroke
// a suspended job will never read. The drop is counted so the cost of a
// stalled pty stays visible.
func (p *paneProc) queueWrite(b []byte, enter bool) {
	p.writer.Do(func() {
		p.writes = make(chan ptyWrite, ptyQueueDepth)
		go p.drainPtyWrites()
	})
	select {
	case p.writes <- ptyWrite{b: b, enter: enter}:
	default:
		p.ptyDropped.Add(1)
	}
}

// ptyDrops reports how many queued pty writes were dropped because the queue
// was full.
func (p *paneProc) ptyDrops() int64 { return p.ptyDropped.Load() }

// drainPtyWrites is the one goroutine that writes the pty. It takes queued
// frames in order and ignores write errors the way the serve loop always
// did: a dead pty shows up as readLoop's exit, and the process leaves with
// it, so the queue never needs a close.
func (p *paneProc) drainPtyWrites() {
	for w := range p.writes {
		_, _ = p.ptmx.Write(w.b)
		if w.enter {
			_, _ = p.ptmx.Write([]byte("\r"))
		}
	}
}

// cursorPos is the VT cursor, zero-based, for a cursor position report.
func (p *paneProc) cursorPos() (row, col int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := p.vt.Grid().Cursor()
	return c.Y, c.X
}

// cwdEvery is how often a pane samples its shell: where it stands and what
// its window title is. The cwd costs one readlink per pane per interval: a
// shell's own process carries the cwd, so a builtin cd moves it and /proc
// reports it without any shell integration.
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

// watchShell samples the shell every cwdEvery: where it stands and what its
// window title is. The poll is the coalescer, so a burst of cds costs one cwd
// report and an animated title costs one title report per sample.
func (p *paneProc) watchShell() {
	if p.cmd == nil || p.cmd.Process == nil {
		return
	}
	pid := p.cmd.Process.Pid
	for {
		time.Sleep(cwdEvery)
		p.sampleCwd(pid)
		p.sampleTitle()
	}
}

// sampleCwd reports the shell's directory when it moved.
func (p *paneProc) sampleCwd(pid int) {
	dir := shellCwd(pid)
	if dir == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if dir != p.cwd {
		p.cwd = dir
		p.broadcastLocked(wire.Msg{Type: "cwd", Cwd: dir})
	}
}

// sampleTitle reports the shell's OSC window title when it moved. A program
// that never sets one reports nothing, so the pane keeps an empty title.
func (p *paneProc) sampleTitle() {
	p.mu.Lock()
	defer p.mu.Unlock()
	title := p.vt.Title()
	if title == p.title {
		return
	}
	p.title = title
	p.broadcastLocked(wire.Msg{Type: "title", Res: jsonRaw(map[string]any{"title": title})})
}

func (p *paneProc) setStatus(st wire.Status) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.st == st {
		return
	}
	p.st = st
	p.broadcastLocked(wire.Msg{Type: "status", St: &st})
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
	defer func() { _ = c.Close() }()
	sc := &subConn{p: p, c: c}
	p.mu.Lock()
	p.subs[sc] = true
	p.mu.Unlock()
	defer func() {
		// The conn closes here and in shutdown; the writer wakes on either.
		sc.shutdown()
		p.mu.Lock()
		delete(p.subs, sc)
		// The waiter going away ends its wait and stops its children poll.
		if p.waiter != nil && p.waiter.sc == sc {
			close(p.waiter.done)
			p.waiter = nil
		}
		p.mu.Unlock()
	}()

	for {
		m, err := wire.ReadMsg(c)
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
			// An Enter records the stream position the output after it starts
			// on. The last Enter wins, and the anchor is taken before the
			// write so the output the Enter produces lands after it.
			if m.Enter || bytes.ContainsAny(b, "\r\n") {
				p.anchor = p.produced
				p.anchorSet = true
				// Keep the text to drop its echo from a later yank, but not a
				// whole pasted block: the pane would hold it until the next
				// Enter, and a bulky echo is not worth a command to match.
				if len(b) <= echoMax {
					p.echo = append(p.echo[:0], b...)
				} else {
					p.echo = p.echo[:0]
				}
			}
			p.mu.Unlock()
			p.queueWrite(b, m.Enter)
		case "resize":
			if m.Cols < 2 || m.Rows < 2 {
				continue
			}
			p.mu.Lock()
			// The anchor is a stream byte offset, so a geometry change only
			// changes how a later yank replays those bytes. It is rebased, not
			// dropped.
			//
			// A width change re-wraps the screen instead of clipping it: the
			// rows join into logical lines and wrap again at the new width.
			// Rewrap refuses the alternate screen and an application scroll
			// region, and those keep the plain resize. A height change alone
			// has nothing to re-wrap.
			oc, _ := p.vt.Grid().Size()
			if m.Cols != oc && !p.vt.Grid().Rewrap(m.Cols, m.Rows) {
				p.vt.Grid().Resize(m.Cols, m.Rows)
			}
			p.mu.Unlock()
			_ = pty.Setsize(p.ptmx, &pty.Winsize{Cols: uint16(m.Cols), Rows: uint16(m.Rows)})
		case "read":
			p.mu.Lock()
			text := screenText(p.vt)
			st := p.st
			title := p.vt.Title()
			dead := p.dead
			p.mu.Unlock()
			lines := splitLines(text)
			_ = sc.send(wire.Msg{
				Type: "resp", ID: m.ID, OK: true, St: &st, Lines: lines,
				Res: jsonRaw(map[string]any{"title": title, "dead": dead}),
			})
		case "screen":
			p.mu.Lock()
			snap := p.vt.Snapshot()
			alt, mouse, paste := modeInfo(p.vt.Grid().Modes())
			p.mu.Unlock()
			_ = sc.send(wire.Msg{Type: "screen", VTSnap: snap, Res: modeRes(alt, mouse, paste)})
		case "reflow":
			if m.Cols < 2 || m.Rows < 2 {
				continue
			}
			p.mu.Lock()
			// The rewrap already happened in the resize action, so this is the
			// picture handshake: the server asks once per drag gesture, after
			// the last width change, and broadcasts the answer to the clients.
			// No output is replayed, so the screen is what the app painted and
			// no scrollback ever returns to it.
			snap := p.vt.Snapshot()
			alt, mouse, paste := modeInfo(p.vt.Grid().Modes())
			// A reflow takes the in-flight wait's mark path away: the fallback
			// poll takes over, and there an empty child sample alone ends the
			// wait. Both goroutines hold p.mu around this field. A marked wait
			// started no poll, so the demotion starts one now.
			var demoted *waitReq
			if w := p.waiter; w != nil && !w.looseStart {
				w.looseStart = true
				demoted = w
			}
			p.mu.Unlock()
			if demoted != nil {
				go p.pollWaitChildren(demoted)
			}
			_ = sc.send(wire.Msg{Type: "screen", VTSnap: snap, Res: modeRes(alt, mouse, paste)})
		case "yank":
			p.mu.Lock()
			tail, cols, h, ok := p.yankTailLocked()
			p.mu.Unlock()
			// Replay outside the lock: the pane's pty reader must not wait on
			// a copy of every row in the command's output.
			var lines []string
			start, rows := 0, 0
			if ok {
				lines, start, rows = yankRows(tail, cols, h)
			}
			_ = sc.send(wire.Msg{Type: "resp", ID: m.ID, OK: true, Res: jsonRaw(map[string]any{
				"lines": lines, "start": start, "rows": rows, "anchor": ok,
			})})
		case "replay":
			p.mu.Lock()
			data := p.ring.Bytes()
			st := p.st
			dead := p.dead
			title := p.vt.Title()
			alt, mouse, paste := modeInfo(p.vt.Grid().Modes())
			p.mu.Unlock()
			_ = sc.send(wire.Msg{
				Type: "replay", Data: data, St: &st,
				Res: jsonRaw(map[string]any{"title": title, "dead": dead, "alt": alt, "mouse": mouse, "paste": paste}),
			})
		case "status":
			p.mu.Lock()
			st := p.st
			p.mu.Unlock()
			_ = sc.send(wire.Msg{Type: "resp", ID: m.ID, OK: true, St: &st})
		case "cwd":
			p.mu.Lock()
			dir := p.cwd
			p.mu.Unlock()
			_ = sc.send(wire.Msg{Type: "resp", ID: m.ID, OK: true, Cwd: dir})
		case "term-colours":
			// An empty value is a no-op: the server may fan out before it has
			// a pair, and a pane keeps the compiled-in default until then.
			p.mu.Lock()
			if m.FG != "" {
				p.fg = m.FG
			}
			if m.BG != "" {
				p.bg = m.BG
			}
			p.mu.Unlock()
			_ = sc.send(wire.Msg{Type: "resp", ID: m.ID, OK: true})
		case "set-history":
			// The server pushes the config's ring budget after a reload. A
			// budget the config could not have asked for is refused, so a bad
			// frame cannot grow the pane past its limit.
			if err := historyBudget(m.History); err != nil {
				_ = sc.send(wire.Msg{Type: "resp", ID: m.ID, OK: false, Error: err.Error()})
				continue
			}
			p.mu.Lock()
			p.ring.Resize(m.History)
			p.mu.Unlock()
			debug.SetMemoryLimit(int64(m.History) + memHeadroom)
			_ = sc.send(wire.Msg{Type: "resp", ID: m.ID, OK: true})
		case "set-secrets":
			// The server pushes the values this pane's output must never
			// echo. The stage redacts from the next chunk on; output already
			// published stays as it was, so the push should follow the spawn
			// before the first command runs.
			values, err := secretValues(m.Res)
			if err != nil {
				_ = sc.send(wire.Msg{Type: "resp", ID: m.ID, OK: false, Error: err.Error()})
				continue
			}
			p.scrub.set(values)
			_ = sc.send(wire.Msg{Type: "resp", ID: m.ID, OK: true})
		case "toggle-secrets":
			// The operator's redaction switch, a convenience for human
			// editing, served from any conn like every other control
			// action: the pane socket is not a boundary. An agent attacking
			// the dm socket can flip it, as it can already type keys and
			// read panes; the real boundary is the sandboxing work. The
			// stage flips between scrubbing and passing the output through;
			// the value list survives both ways, so a set-secrets push
			// while the switch is off updates the values without turning
			// the stage back on. The reply carries the resulting state, so
			// the server can show it.
			res := jsonRaw(map[string]any{"enabled": p.scrub.toggle()})
			_ = sc.send(wire.Msg{Type: "resp", ID: m.ID, OK: true, Res: res})
		case "ping":
			_ = sc.send(wire.Msg{Type: "resp", ID: m.ID, OK: true})
		case "wait":
			// The children sample runs before the lock: a /proc read must not
			// stall publishes, and it must be settled before the tail reply
			// goes out so the start decision lands with the registration it
			// belongs to. A marked pane never uses it.
			kids := p.childrenAt(p.shellPID())
			p.mu.Lock()
			if p.dead {
				p.mu.Unlock()
				_ = sc.send(waitReply(m.ID, nil, "dead"))
				continue
			}
			if p.waiter != nil {
				p.mu.Unlock()
				_ = sc.send(wire.Msg{
					Type: "resp", ID: m.ID, OK: false,
					Error: "a wait is already running",
				})
				continue
			}
			// A pane at a prompt has no command to report: its wait belongs to
			// the next command, so it streams from now and answers that
			// command's end. With no mark to read, an empty child sample says
			// the same.
			atPrompt := p.vt.AtPrompt()
			_, marked := p.vt.CommandEnd()
			idle := atPrompt || (!marked && len(kids) == 0)
			// A prompt mark proves shell integration, so the next command's
			// end mark can end the wait even before any D mark arrived.
			marked = marked || atPrompt
			// Otherwise the wait streams from the last command's start: the
			// Enter's anchor, or the stream's current end when none was
			// recorded.
			anchor := p.produced
			if p.anchorSet && !idle {
				anchor = p.anchor
			}
			w := &waitReq{
				sc: sc, id: m.ID, anchor: anchor, mark: marked,
				done: make(chan struct{}),
			}
			p.waiter = w
			tail, cols, h, tailOK := p.yankTailLocked()
			if idle {
				// The bytes between the last Enter and the prompt belong to the
				// command that already returned, not to this wait.
				tail, tailOK = nil, false
			}
			// The tail is rendered and sent under the pane lock, the way
			// broadcasts are: every bytes frame and the final reply on this
			// connection must land after it. Frames the waiter received
			// before it repeat what the tail renders, so a client treats the
			// tail as authoritative and discards those.
			var lines []string
			start, rows := 0, 0
			if tailOK {
				lines, start, rows = yankRows(tail, cols, h)
			}
			_ = sc.send(wire.Msg{
				Type: "resp", ID: m.ID, OK: true,
				Res: jsonRaw(map[string]any{
					"lines": lines, "start": start, "rows": rows, "anchor": tailOK,
				}),
			})
			// A command that already returned has its D mark behind the
			// request and no further publish may come: run the same check
			// every publish runs, right here.
			if s, reply, ok := p.waitEndLocked(); ok {
				p.mu.Unlock()
				_ = s.send(reply)
				continue
			}
			p.mu.Unlock()
			if !marked {
				// A markless pane sees a command only through the shell's
				// foreground children, so the poll runs either way: with a
				// child already there it ends when that child leaves, and
				// with none it waits for the command it was asked about to
				// start and then return. The waiter's quiet-idle timeout
				// bounds a pane where nothing ever starts.
				w.childSeen = len(kids) > 0
				go p.pollWaitChildren(w)
			}
		case "kill":
			_ = sc.send(wire.Msg{Type: "resp", ID: m.ID, OK: true})
			p.shutdown()
			go func() {
				time.Sleep(50 * time.Millisecond)
				_ = os.Remove(p.sock)
				os.Exit(0)
			}()
		}
	}
}

// yankTailLocked returns the stream bytes the last command produced, plus the
// pane's size. The copy starts at the command start mark when the shell sends
// one, because a prompt that repaints the line on Enter puts bytes between the
// anchor and the output that are not output. Without a mark it starts after the
// echoed command line: either the text dm wrote, or the line end the pty
// echoes at the anchor. The end is the command end mark, or the prompt that
// follows, so the trailing prompt is not copied. ok is false when no Enter has
// been recorded. The caller holds p.mu and owns the returned copy.
func (p *paneProc) yankTailLocked() (tail []byte, cols, h int, ok bool) {
	cols, h = p.vt.Grid().Size()
	if !p.anchorSet || p.ring.Len() == 0 {
		return nil, cols, h, false
	}
	ringStart := p.produced - int64(p.ring.Len())
	head, marked := p.anchor, false
	if start, has := p.vt.CommandStart(); has && start >= p.anchor {
		head, marked = start, true
	}
	tailEnd := p.produced
	ended := false
	if end, has := p.vt.CommandEnd(); has && end >= head {
		tailEnd, ended = end, true
	}
	from, to := int(head-ringStart), int(tailEnd-ringStart)
	if from < 0 {
		// The ring dropped the anchor's bytes. Start at the oldest row held.
		from = 0
	}
	if to > p.ring.Len() {
		to = p.ring.Len()
	}
	if from > to {
		from = to
	}
	if !marked {
		// Drop the echo of the command dm wrote, then the line end after it.
		if n := len(p.echo); n > 0 && to-from >= n && bytes.Equal(p.ring.slice(from, from+n), p.echo) {
			from += n
		}
		// The pty normally echoes an Enter as "\r\n" at the anchor, so the copy
		// starts after it. A pane whose Enter is not echoed puts its first
		// output row right at the anchor; skipping a newline that has text
		// before it would drop that row.
		if nl := p.ring.indexByte('\n', from); nl >= 0 && nl < to && onlyLineEnds(p.ring, from, nl) {
			from = nl + 1
		}
	}
	if !ended {
		if prompt, has := p.vt.PromptStart(); has {
			if cut := int(prompt - ringStart); cut >= from && cut <= to {
				to = cut
			}
		}
	}
	return p.ring.slice(from, to), cols, h, true
}

// onlyLineEnds reports whether ring[from:to] holds only CR and LF, the bytes a
// pty echoes for an Enter.
func onlyLineEnds(r *scrollRing, from, to int) bool {
	for i := from; i < to; i++ {
		if c := r.at(i); c != '\r' && c != '\n' {
			return false
		}
	}
	return true
}

// yankRows turns a yank tail into the row space the client places. lines holds
// every replayed row, start is the first row with content, and rows is the
// total. The client slices lines at start and drops trailing blank rows, so
// leading blank rows after the prompt are not copied.
func yankRows(tail []byte, cols, h int) (lines []string, start, rows int) {
	all := vt.ReplayRows(tail, cols, h, 0)
	rows = len(all)
	start = 0
	for start < rows && vt.BlankRow(all[start]) {
		start++
	}
	lines = make([]string, rows)
	for y := range all {
		lines[y] = vt.PlainRow(all[y])
	}
	return lines, start, rows
}

// childrenAt samples a pid's foreground children through the pane's sampler:
// the test's stand-in when one is set, else the real /proc read.
func (p *paneProc) childrenAt(pid int) []int {
	if p.children != nil {
		return p.children(pid)
	}
	return shellChildren(pid)
}

// shellPID is the pane shell's pid, or 0 when the pane has no command.
func (p *paneProc) shellPID() int {
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// shellChildren reads the shell's direct child pids from /proc, the file
// watchShell's shell runs by.
func shellChildren(pid int) []int {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
	if err != nil {
		return nil
	}
	var kids []int
	for _, f := range strings.Fields(string(b)) {
		if n, err := strconv.Atoi(f); err == nil {
			kids = append(kids, n)
		}
	}
	return kids
}

// waitReply is a wait's final frame: the exit code when one was observed and
// the reason the wait ended.
func waitReply(id int, exit *int, reason string) wire.Msg {
	return wire.Msg{Type: "resp", ID: id, OK: true, Exit: exit, WaitReason: reason}
}

// waitEndLocked ends the pane's wait when it is finished: the shell died, or
// a command end mark at or after the wait's start anchor arrived. The caller
// holds p.mu. ok is false while the wait keeps running; otherwise the wait is
// cleared and the returned reply goes to the waiting connection.
func (p *paneProc) waitEndLocked() (sc *subConn, reply wire.Msg, ok bool) {
	w := p.waiter
	if w == nil {
		return nil, wire.Msg{}, false
	}
	if p.dead {
		p.waiter = nil
		close(w.done)
		return w.sc, waitReply(w.id, nil, "dead"), true
	}
	if w.mark {
		if end, marked := p.vt.CommandEnd(); marked && end >= w.anchor {
			p.waiter = nil
			close(w.done)
			// The exit comes from the most recent coded D mark at or after
			// the anchor, not from the latest D mark: one read of the stream
			// can hold the command's coded mark and the prompt hook's bare D
			// behind it, and the wait that ends on that read must still
			// answer the code the command reported.
			var exit *int
			if code, coded := p.vt.CommandExitFrom(w.anchor); coded {
				exit = &code
			}
			return w.sc, waitReply(w.id, exit, "mark"), true
		}
	}
	return nil, wire.Msg{}, false
}

// waitPollEvery is how often the children fallback samples the shell, the
// same cadence the shell watcher runs on.
const waitPollEvery = cwdEvery

// pollWaitChildren runs the wait's no-marks fallback: it samples the shell's
// foreground children until one sample finds some and the next finds none,
// which is the command returning with an unobservable exit code. It returns
// when the wait ends for another reason or the waiter disconnects.
func (p *paneProc) pollWaitChildren(w *waitReq) {
	pid := p.shellPID()
	ticker := time.NewTicker(waitPollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
		}
		kids := p.childrenAt(pid)
		p.mu.Lock()
		if p.waiter != w {
			p.mu.Unlock()
			return
		}
		if sc, reply, ok := p.waitEndLocked(); ok {
			p.mu.Unlock()
			_ = sc.send(reply)
			return
		}
		if len(kids) > 0 {
			w.childSeen = true
		} else if w.childSeen || w.looseStart {
			// The child set emptied after holding children, or the wait lost
			// its marks to a reflow and no child is running: the command came
			// back and nothing observed its exit code.
			// The child set emptied after holding children: the command came
			// back and nothing observed its exit code.
			p.waiter = nil
			p.mu.Unlock()
			_ = w.sc.send(waitReply(w.id, nil, "children"))
			return
		}
		p.mu.Unlock()
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

func init() {
	log.SetFlags(log.Ltime)
}

func writePID(path string) {
	_ = os.WriteFile(path, fmt.Appendf(nil, "%d\n", os.Getpid()), 0o600)
}
