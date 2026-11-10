package client

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"dm/internal/boxview"
	"dm/internal/keymap"
	"dm/internal/keymapview"
	"dm/internal/layout"
	"dm/internal/panel"
	"dm/internal/rail"
	"dm/internal/switcher"
	"dm/internal/theme"
	"dm/internal/transport"
	"dm/internal/vt"
	"dm/internal/wire"

	"golang.org/x/term"
)

const (
	// histRows is how many rows of scrollback the tall terminal keeps.
	histRows = 2000

	// escTimeout bounds how long a lone escape or a partial escape sequence
	// waits for the rest of its bytes.
	escTimeout = 40 * time.Millisecond

	// pasteTimeout bounds how long a paste start marker that is already more
	// than its escape byte waits for the rest. A paste is written by a program,
	// not typed, and it can arrive in pieces with a long gap on a slow link, so
	// the window is the escape-time a conventional multiplexer allows. A lone
	// escape keeps escTimeout, because that is the dismiss key.
	pasteTimeout = 500 * time.Millisecond

	// termGrace is how long the client waits for the second colour reply
	// before sending the one it has, so the two coalesce into one request.
	termGrace = 300 * time.Millisecond

	// toastMax is how many toasts the stack holds, toastLifetime how long each
	// one lives, and toastDedupe how close a repeat must be to join the last
	// toast instead of raising a new one.
	toastMax      = 3
	toastLifetime = 3 * time.Second
	toastDedupe   = time.Second

	// idleFlush is how long a pane's output must be quiet before its pending
	// grapheme cluster is placed. The parser holds a cluster until the next
	// rune proves it complete, so a program that writes once and waits would
	// otherwise show one character short until it writes again.
	idleFlush = 16 * time.Millisecond
)

type tui struct {
	session string
	conn    *transport.Conn
	// keymap is the loaded keybindings. A nil keymap means the built-in
	// defaults, so a test TUI runs without a config file.
	keymap *keymap.Config
	// th is the loaded theme. A nil theme means the built-in theme, so a test
	// TUI runs without a config file.
	th     *theme.Theme
	cols   int
	rows   int
	snap   *wire.Snapshot
	vts    map[string]*vt.Term
	armed  bool
	toasts []toast // oldest first, newest last
	// lastByte is when each pane last received output, so a quiet pane's
	// pending cluster can be placed without waiting for the next chunk.
	lastByte map[string]time.Time
	// glyphs is the resolved glyph set. The zero value is auto, so a test TUI
	// resolves it on first use.
	glyphs theme.GlyphSet
	// floats are arbitrary floating panes rendered over the main layout.
	floats []*panel.FloatingPane
	// hover is the URL under the pointer while ctrl is held, so the frame can
	// underline it. It is nil when no link is hovered.
	hover *linkHover
	// box is the message-box float, nil when closed.
	box *boxview.BoxView
	// keys is the keymap view float, nil when closed.
	keys  *keymapview.KeymapView
	dirty bool

	// out is the frame sink: a counting wrapper over os.Stdout in a session,
	// a buffer in a test.
	out io.Writer

	mode     string // "" or "sessions" or "scroll" or "menu" or "rename" or "box" or "help"
	sessions []transport.SessionInfo
	sel      int
	menu     *menu
	// menuPress is set when a right press opens a menu. The release of that
	// press belongs to nobody, so the menu swallows it instead of picking the
	// row under the pointer.
	menuPress bool
	// renameDraft is the text the rename prompt holds while mode is "rename".
	renameDraft string
	// renameID is the id of the workspace, window or pane the prompt renames.
	// The server looks the target up by id, so the request carries this, not
	// the draft.
	renameID string
	// renameKind is "workspace", "window" or "pane" while the prompt is open.
	// It picks the action the prompt sends and the float title it paints.
	renameKind string

	// hist is each pane's replay ring. It is not filled as output arrives:
	// the first scrollback entry asks the server for one replay and caches it
	// here. histMeta records the rect a ring was asked for and whether it has
	// landed, so a second entry asks for nothing and a ring wrapped for an old
	// layout is never shown.
	hist     map[string][]byte
	histMeta map[string]histCache
	scroll   string // pane id shown from scrollback
	offset   int
	maxOff   int
	scrollVT *vt.Term

	quit     bool
	switchTo string

	escAt   time.Time
	inbuf   []byte
	pending bool

	pasteDeadline time.Time // set while a paste start marker is still arriving
	pasteHeld     int       // buffer length when pasteDeadline was last extended
	pasteOff      bool      // one pass with paste detection off

	// termFG and termBG are the colours the outer terminal answered OSC 10
	// and OSC 11 with. termDeadline bounds a partial reply, termGrace the
	// wait for the second reply, and termSent* what the server already has.
	termFG, termBG string
	termDeadline   time.Time
	termGrace      time.Time
	termSentFG     string
	termSentBG     string
	termOff        bool // one pass with colour-reply detection off

	selText        *selection // text selected with the mouse, nil when none
	press          *heldPress // left press waiting for a drag or a click
	swallowRelease bool       // the menu took a click; its release belongs to nobody

	dragging   bool
	dragFirst  string
	dragSecond string
	dragAxis   string
	dragStart  int
	dragSpace  int
	dragAt     time.Time

	// railHover remembers the rail row the pointer last lit, so a motion
	// that moves it marks the frame dirty. The paint derives the row from
	// mouseX/mouseY, so this is a cache, never the source.
	railHover int
	// mouseSeen turns false until the first pointer event. A tui that never
	// saw the pointer lights no hover: the zero cell is a rail row, and a
	// highlight before the pointer moved would point at nothing.
	mouseSeen bool

	// padZone is the divider under the pointer and padAxis its orientation.
	// A zero zone draws no hover affordance. mouseX/mouseY remember the last
	// pointer cell, so a snapshot can re-resolve the hover.
	padZone layout.Rect
	padAxis string
	mouseX  int
	mouseY  int

	// railWant is the last non-zero rail width seen, so the toggle can restore
	// it. railPress marks a press that began in the rail, whose events belong
	// to no pane. railDragging marks a drag of the rail grip, with its own
	// clock and its own last width, so the rail never borrows the split drag's
	// state.
	railWant     int
	railPress    bool
	railDragging bool
	railDragAt   time.Time
	railWidth    int
	// railPressAt and railPressRow remember the last left press on a rail row,
	// so a second press on the same row within railDoubleClick opens the pane's
	// rename prompt instead of focusing again.
	railPressAt  time.Time
	railPressRow int

	// lastTabClick and lastTabClickPos track the last left press on a tab bar
	// tab, so a second press in the same cell within the double-click window
	// opens the window rename prompt instead of selecting the window.
	lastTabClick    time.Time
	lastTabClickPos struct{ X, Y int }

	// sw is the floating native switcher, nil when closed. swX and swY are
	// the query cursor cell its last paint asked for, swAt and swPane are the
	// last preview request, and swID numbers the next one.
	sw     *switcher.Switcher
	swX    int
	swY    int
	swAt   time.Time
	swPane string
	swID   int
	// swInput is true while the dashboard forwards every key to the selected
	// pane. Esc leaves it; the dashboard stays up.
	swInput bool

	// diff is the read-only diff pager, nil when closed. diffID numbers the
	// request and diffPane is the pane it named, so a late reply still opens
	// under the right worktree name.
	diff     *diffView
	diffID   int
	diffPane string

	// swVT holds the dashboard's own preview terminals, one per pane it has
	// asked the server about. A pane in the active window has a local terminal
	// in vts; every other pane has no bytes stream, so the dashboard keeps the
	// screen the server sent here. It is memory only and dies with the
	// dashboard. swScreen numbers those requests and swScreenAt is when the
	// last one went out, so the preview refreshes on a cadence.
	swVT       map[string]*vt.Term
	swScreen   int
	swScreenAt time.Time

	// yankID numbers the next copy-output request and yankPane is the pane it
	// names, so a reply writes the clipboard for the pane the operator asked
	// about even if focus moved before it landed.
	yankID   int
	yankPane string

	// rowsig holds one fingerprint per pane content row, so a steady frame
	// repaints only the rows that moved. laysig is the shape of the picture:
	// when it changes the next frame repaints whole. last is what the previous
	// frame painted, so each band is written only when its bytes change.
	rowsig map[string][]uint64
	laysig string
	last   lastFrame
}

// builtinKeymap parses the default keymap once. Every TUI without a loaded
// config shares it.
var (
	builtinOnce sync.Once
	builtinCfg  *keymap.Config
)

func builtinKeymap() *keymap.Config {
	builtinOnce.Do(func() { builtinCfg = keymap.LoadDefault() })
	return builtinCfg
}

// km returns the client's keymap, or the built-in defaults when it has none.
func (t *tui) km() *keymap.Config {
	if t.keymap != nil {
		return t.keymap
	}
	return builtinKeymap()
}

// theme is the client's theme. A client with no config file has none, and gets
// the built-in theme.
func (t *tui) theme() theme.Theme {
	if t == nil || t.th == nil {
		return theme.DefaultTheme().For(t.glyphSet())
	}
	return t.th.For(t.glyphSet())
}

// paint is the bytes one band of the screen painted last frame and, for an
// overlay, the screen rows those bytes cover.
type paint struct {
	buf  []byte
	rows []int
}

// lastFrame is what the previous frame painted. The bands are kept apart
// because each one changes on its own: a rail label must not cost a pane
// repaint.
type lastFrame struct {
	box    paint
	grip   paint
	div    paint
	rail   paint
	tabbar paint
	bar    paint
	toasts paint
	pal    paint
	help   paint
	sess   paint
	menu   paint
	rename paint

	// floats holds each float's last bytes, keyed by float ID. The widget
	// keeps no frame state of its own.
	floats map[string]*paint

	divRect layout.Rect // divider zone the last frame painted
	cur     string      // cursor block the last frame wrote
}

// floatPaint returns the last bytes of the float with this ID, creating the
// entry on first sight.
func (l *lastFrame) floatPaint(id string) *paint {
	if l.floats == nil {
		l.floats = make(map[string]*paint)
	}
	p := l.floats[id]
	if p == nil {
		p = &paint{}
		l.floats[id] = p
	}
	return p
}

// histCache is one pane's replay bookkeeping: the rect the ring was asked for
// and whether the reply has landed. The ring itself lives in tui.hist, which
// applyReplay owns.
type histCache struct {
	at  layout.Rect
	got bool
}

// linkHover is the URL under a ctrl-held pointer: the pane or float that owns
// it and its cell range in that pane's screen-local cells. w is the width the
// range was measured against, so a resized pane drops the range rather than
// underlining the wrong cells. A nil hover draws nothing.
type linkHover struct {
	pane   string
	w      int
	region selRange
}

// panePaint is one pane's content for one frame: the terminal to read, the
// first row to show from it, the selection to mark and the hover to underline.
type panePaint struct {
	id    string
	rect  layout.Rect
	vt    *vt.Term
	row0  int
	sel   *selRange
	hover *selRange
}

// toast is one short message for the operator. Its time is when it was raised,
// so the stack can expire it.
type toast struct {
	text string
	at   time.Time
}

// notify raises a toast. A repeat of the newest text inside toastDedupe is the
// same toast, not a second one. The stack keeps the last toastMax.
func (t *tui) notify(text string) {
	if text == "" {
		return
	}
	now := time.Now()
	if n := len(t.toasts); n > 0 {
		if last := t.toasts[n-1]; last.text == text && now.Sub(last.at) < toastDedupe {
			return
		}
	}
	t.toasts = append(t.toasts, toast{text: text, at: now})
	if len(t.toasts) > toastMax {
		t.toasts = t.toasts[len(t.toasts)-toastMax:]
	}
	t.dirty = true
}

// expireToasts drops the toasts past toastLifetime.
func (t *tui) expireToasts(now time.Time) {
	if len(t.toasts) == 0 {
		return
	}
	kept := t.toasts[:0]
	for _, x := range t.toasts {
		if now.Sub(x.at) < toastLifetime {
			kept = append(kept, x)
		}
	}
	if len(kept) == len(t.toasts) {
		return
	}
	t.toasts = kept
	t.dirty = true
}

// Run owns the terminal. A session switch returns to this loop with a new
// name, so raw mode and the stdin reader survive it.
func Run(name string) error {
	// The pacer spans session switches so the stats describe the process.
	var pace pacer
	// DM_STATS asks for the render's cache and pacing tallies when the
	// client exits. It is a diagnostic, off by default, and it writes to
	// stderr so it never lands inside a frame.
	if os.Getenv("DM_STATS") != "" {
		defer func() {
			fmt.Fprintf(os.Stderr, "dm: style cache: %s\n", theme.SGRCache())
			fmt.Fprintf(os.Stderr, "dm: pacing: %s\n", pace.stats())
		}()
	}
	// A pane exports its own session name. Attaching that same session from a
	// pane makes a mirror that can also fight over the shared screen size.
	if in := os.Getenv("DM_SESSION"); in != "" && in == name {
		return fmt.Errorf("already inside session %q; attach to another session with -s", name)
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return fmt.Errorf("dm needs a terminal; use dm exec or dm read otherwise")
	}
	// The chrome and the pane content both step down to what the outer
	// terminal can show. Set once, before the first frame.
	theme.SetDepth(theme.DetectDepth(os.Getenv))
	old, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	defer term.Restore(fd, old)

	// Ask the outer terminal for its colours once per attach, before the
	// alternate screen. The reply arrives on stdin and the detector consumes
	// it; a terminal that never answers leaves the pane defaults alone.
	os.Stdout.WriteString("\x1b]10;?\x1b\\\x1b]11;?\x1b\\")

	keys := make(chan []byte, 64)
	go stdinReader(keys)
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)

	var out bytes.Buffer
	out.WriteString("\x1b[?1049h\x1b[?25l\x1b[2J\x1b[H\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1006h\x1b[?2004h")
	os.Stdout.Write(out.Bytes())
	defer os.Stdout.WriteString("\x1b[?2004l\x1b[?1003l\x1b[?1006l\x1b[?1002l\x1b[?1000l\x1b[?25h\x1b[?1049l\x1b[0m")

	for {
		next, err := runSession(fd, name, keys, winch, &pace)
		if err != nil {
			return err
		}
		if next == "" {
			return nil
		}
		name = next
		os.Stdout.WriteString("\x1b[0m\x1b[2J\x1b[H")
	}
}

func stdinReader(keys chan<- []byte) {
	buf := make([]byte, 4096)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			keys <- append([]byte(nil), buf[:n]...)
		}
		if err != nil {
			return
		}
	}
}

// frameSink counts the bytes of the frames written through it, so the pacer
// can tell a frame that said something from one that said nothing.
type frameSink struct {
	w io.Writer
	n int
}

// Write passes the frame through and counts what it wrote.
func (s *frameSink) Write(b []byte) (int, error) {
	n, err := s.w.Write(b)
	s.n += n
	return n, err
}

// reset forgets the bytes of the previous frame.
func (s *frameSink) reset() { s.n = 0 }

// wrote reports whether the last frame wrote any bytes.
func (s *frameSink) wrote() bool { return s.n > 0 }

// tick runs the per-tick work: the expiry timers, then at most one frame. A
// dirty frame inside the floor leaves the interval alone and waits for the next
// tick, so a burst of changes still coalesces into one frame; a clean tick is
// the idle tick and decays the interval.
func (t *tui) tick(now time.Time, pace *pacer, sink *frameSink) {
	t.expireToasts(now)
	t.expirePaste(now)
	t.expireTermColour(now)
	t.expireSwitcher(now)
	t.previewTick(now)
	t.flushBoxWatchers(now)
	t.expireArmed(now)
	t.expirePrefix(now)
	t.expireMode(now)
	if t.flushIdle(now) {
		t.dirty = true
		pace.reset()
	}
	if !t.dirty {
		pace.decay(now)
		return
	}
	if !pace.shouldDraw(now, true) {
		return
	}
	t.frame(now, pace, sink)
}

// wake runs after input, a message, a resize or a session switch: it floors the
// pacer and draws the frame now when the floor allows. Waiting for the next
// tick would put the first keystroke after a pause behind a ticker the idle
// decay had armed out at the ceiling.
func (t *tui) wake(now time.Time, pace *pacer, sink *frameSink) {
	pace.reset()
	if t.dirty && pace.shouldDraw(now, true) {
		t.frame(now, pace, sink)
	}
}

// frame draws one frame and tells the pacer whether it said anything.
func (t *tui) frame(now time.Time, pace *pacer, sink *frameSink) {
	sink.reset()
	t.draw()
	t.dirty = false
	if sink.wrote() {
		pace.drew(now)
	} else {
		pace.decay(now)
	}
}

func runSession(fd int, name string, keys <-chan []byte, winch <-chan os.Signal, pace *pacer) (string, error) {
	c, err := transport.Dial(name)
	if err != nil {
		return "", err
	}
	defer c.Close()

	t := &tui{
		session: name, conn: c, vts: map[string]*vt.Term{},
		hist: map[string][]byte{},
	}
	// The sink counts a frame's bytes so the pacer can tell whether the
	// frame said anything.
	sink := &frameSink{w: os.Stdout}
	t.out = sink
	// The keymap is loaded once for the whole session. A missing file is
	// written with the defaults first; a load error is toasted and the
	// built-in defaults carry the session.
	cfg, err := keymap.LoadDefaultPath(true)
	t.keymap = cfg
	t.glyphs = rail.ResolveGlyphs(cfg.GlyphSet())
	th := cfg.Theme()
	t.th = &th
	if err != nil {
		t.notify(err.Error())
	}
	t.cols, t.rows, _ = term.GetSize(fd)
	defer closeSwitcher(t)

	done := make(chan struct{})
	defer close(done)
	msgs := make(chan wire.Msg, 256)
	go func() {
		for {
			m, err := wire.ReadMsg(c)
			if err != nil {
				close(msgs)
				return
			}
			select {
			case msgs <- m:
			case <-done:
				return
			}
		}
	}()

	if err := t.conn.Send(wire.Msg{Type: "attach", Cols: t.cols, Rows: t.rows}); err != nil {
		return "", err
	}

	ticker := time.NewTicker(pace.interval())
	defer ticker.Stop()
	tickEvery := pace.interval()

	for {
		select {
		case b, ok := <-keys:
			if !ok {
				return "", nil
			}
			t.inbuf = append(t.inbuf, b...)
			t.processInput()
			t.wake(time.Now(), pace, sink)
		case m, ok := <-msgs:
			if !ok {
				return "", nil
			}
			t.apply(m)
			t.wake(time.Now(), pace, sink)
		case <-winch:
			cols, rows, err := term.GetSize(fd)
			if err == nil && (cols != t.cols || rows != t.rows) {
				t.cols, t.rows = cols, rows
				_ = t.conn.Send(wire.Msg{Type: "resize", Cols: cols, Rows: rows})
				t.dirty = true
				t.wake(time.Now(), pace, sink)
			}
		case <-ticker.C:
			t.tick(time.Now(), pace, sink)
		}
		// Re-arm the ticker whenever the pacer's interval moved: the idle
		// decay lengthens it, a wake floors it. One re-arm per change, so a
		// message stream does not keep pushing the tick out.
		if every, ok := pace.rearm(tickEvery); ok {
			ticker.Reset(every)
			tickEvery = every
		}
		if t.quit {
			return "", nil
		}
		if t.switchTo != "" {
			return t.switchTo, nil
		}
	}
}

func (t *tui) apply(m wire.Msg) {
	// The history cache is filled on demand, and a replay pushed by a layout
	// change can arrive before the first request.
	if t.hist == nil {
		t.hist = map[string][]byte{}
	}
	switch m.Type {
	case "snapshot":
		t.snap = m.Snap
		t.snap.WindowLabel = t.km().WindowLabel()
		t.updatePadHover(t.mouseX, t.mouseY)
		t.updateRailHover()
		// railWant remembers the last expanded width only, so a collapse or a
		// hide does not replace it with the thin width or zero.
		switch {
		case m.Snap.RailWant > layout.CollapsedRail:
			t.railWant = m.Snap.RailWant
		case m.Snap.Rail > layout.CollapsedRail:
			t.railWant = m.Snap.Rail
		case t.railWant <= 0:
			t.railWant = layout.DefaultRail
		}
		if t.scroll != "" {
			p := t.paneByID(t.scroll)
			if p == nil {
				t.exitScroll()
			} else if t.scrollVT != nil {
				c, r := t.scrollVT.Grid().Size()
				if c != p.Rect.W || r != p.Rect.H+histRows {
					t.exitScroll()
				}
			}
		}
		for _, p := range m.Snap.Panes {
			term := t.vts[p.ID]
			if term != nil {
				c, r := term.Grid().Size()
				if c != p.Rect.W || r != p.Rect.H {
					resizeTerm(term, p.Rect.W, p.Rect.H)
				}
			} else {
				t.vts[p.ID] = vt.NewTerm(p.Rect.W, p.Rect.H)
			}
		}
		keep := map[string]bool{}
		for _, p := range m.Snap.Panes {
			keep[p.ID] = true
		}
		for id := range t.vts {
			if !keep[id] {
				delete(t.vts, id)
			}
		}
		// A pane that is gone, or whose id changed, takes its ring with it.
		for id := range t.histMeta {
			if !keep[id] {
				delete(t.hist, id)
				delete(t.histMeta, id)
			}
		}
		// A ring wrapped for the old rect would not line up with the pane.
		for _, p := range m.Snap.Panes {
			c, ok := t.histMeta[p.ID]
			if !ok || c.at == p.Rect {
				continue
			}
			delete(t.hist, p.ID)
			delete(t.histMeta, p.ID)
			if t.scroll == p.ID && t.scrollVT == nil {
				t.exitScroll()
			}
		}
		if t.selText != nil {
			p := t.paneByID(t.selText.pane)
			if p == nil || p.Rect.W != t.selText.w || p.Rect.H != t.selText.h {
				// The anchor and focus are in the old geometry.
				t.clearSelection()
			}
		}
		// A pane that left the session takes its preview terminal with it.
		if len(t.swVT) > 0 {
			live := make(map[string]bool, len(m.Snap.All))
			for _, p := range m.Snap.All {
				live[p.ID] = true
			}
			for id := range t.swVT {
				if !live[id] {
					delete(t.swVT, id)
				}
			}
		}
		// Folds, names and statuses move under an open float.
		if t.sw != nil {
			t.sw.Sync(m.Snap)
		}
		if t.diff != nil {
			t.diff.sync(m.Snap)
			t.dirty = true
		}
		if t.box != nil {
			t.box.Sync(m.Snap, t.theme())
			t.dirty = true
		}
		if t.keys != nil {
			t.keys.Sync(m.Snap)
			t.dirty = true
		}
		t.dirty = true
	case "screen":
		if p := t.paneByID(m.Target); p != nil {
			applySnapshot(t.vts, m.Target, p.Rect, m.VTSnap)
		} else {
			// A pane outside the active window has no local terminal. The
			// dashboard owns the one its preview paints from.
			t.applyPreviewScreen(m.Target, m.VTSnap)
		}
		t.dirty = true
	case "replay":
		// The ring is history. It never touches the live VT: the screen above
		// is the picture, and replaying raw bytes would double-apply them.
		applyReplay(t.hist, m.Target, m.Data)
		t.dirty = true
	case "bytes":
		if term := t.vts[m.Target]; term != nil {
			before := term.Grid().Scrolls()
			term.Write(m.Data)
			if term.Grid().Scrolls() != before {
				t.dropLiveSelection(m.Target)
			}
			t.noteBytes(m.Target, time.Now())
			t.dirty = true
		}
		// New output moves the ring. A cached replay would be stale, so drop
		// it: the next scrollback entry asks for a fresh snapshot.
		delete(t.hist, m.Target)
		delete(t.histMeta, m.Target)
	case "close":
		t.quit = true
	case "exit":
		t.notify("pane " + m.Target + " exited")
	case "resp":
		if m.Action == "yank" && m.ID == t.yankID {
			t.takeYank(m)
			break
		}
		// The diff reply opens the pager. A failure toasts and opens nothing.
		if m.Action == "diff" && m.ID > 0 && m.ID == t.diffID {
			if !m.OK {
				t.notify(m.Error)
				break
			}
			t.openDiffView(m.Lines)
			break
		}
		// A read reply carries the id its request named. Only the newest read
		// id fills the band: an older one is dropped and a failure stays
		// silent. A screen reply numbers its request in its own space, so it
		// never reaches the band even when the ids collide.
		if m.ID > 0 {
			if m.Action == "read" && t.sw != nil && t.sw.AcceptPreview(m.ID, previewLines(m)) {
				t.dirty = true
			}
			break
		}
		if m.Action == "msg-list" {
			t.takeBoxList(m)
			break
		}
		if m.Action == "msg-watchers" {
			t.takeBoxWatchers(m)
			break
		}
		if !m.OK {
			t.notify(m.Error)
		}
	case "box":
		// A post or a clear landed. An open view asks for the log again; a
		// closed one needs nothing.
		if t.box != nil {
			t.boxRequest()
		}
	case "watch":
		// A watch appeared, advanced, unparked or went. An open view asks for
		// the list again, at most once per boxview.WatchInterval.
		if t.box != nil {
			t.boxWatchers(time.Now())
		}
	}
	// A ring that just landed belongs to the scrollback wait it answers, if
	// there is one. The replay case itself only stores the ring.
	if m.Type == "replay" {
		t.noteHist(m.Target)
		t.histArrived(m.Target)
	}
}

// applySnapshot restores a pane's visible state from the pane daemon's
// snapshot. The snapshot carries its own size, and it can be the size the pane
// had before the layout's resize reached it: syncSizes sends the resize and
// asks for the screen without waiting, so the two race. Restore at the
// snapshot's size and reflow to the rect, which is what the old ANSI replay
// did. Refusing a mismatch leaves the pane blank until the program redraws.
func applySnapshot(vts map[string]*vt.Term, id string, r layout.Rect, snap *vt.Snapshot) bool {
	if r.W < 1 || r.H < 1 || snap == nil {
		return false
	}
	term := vts[id]
	if term == nil {
		term = vt.NewTerm(snap.Cols, snap.Rows)
	}
	if err := term.Restore(snap); err != nil {
		return false
	}
	if c, rw := term.Grid().Size(); c != r.W || rw != r.H {
		resizeTerm(term, r.W, r.H)
	}
	vts[id] = term
	return true
}

// glyphSet is the glyph vocabulary the chrome draws with, resolved once. Auto
// picks Nerd Font glyphs when the font is present and Unicode otherwise.
func (t *tui) glyphSet() theme.GlyphSet {
	if t.glyphs == theme.GlyphAuto {
		t.glyphs = rail.ResolveGlyphs(theme.GlyphAuto)
	}
	return t.glyphs
}

// noteBytes records when a pane last received output, so flushIdle can tell a
// quiet pane from a busy one.
func (t *tui) noteBytes(id string, now time.Time) {
	if t.lastByte == nil {
		t.lastByte = map[string]time.Time{}
	}
	t.lastByte[id] = now
}

// flushIdle places each quiet pane's pending grapheme cluster and reports
// whether it placed any. A pane is quiet when a full tick has passed with no
// bytes, so a stream never flushes mid-cluster.
func (t *tui) flushIdle(now time.Time) bool {
	placed := false
	for id, at := range t.lastByte {
		if now.Sub(at) < idleFlush {
			continue
		}
		delete(t.lastByte, id)
		term := t.vts[id]
		if term == nil {
			continue
		}
		term.Flush()
		placed = true
	}
	return placed
}

// applyReplay replaces a pane's history ring only.
func applyReplay(hist map[string][]byte, id string, data []byte) {
	hist[id] = append([]byte(nil), data...)
}

func (t *tui) paneByID(id string) *wire.PaneInfo {
	if t.snap == nil {
		return nil
	}
	for i := range t.snap.Panes {
		if t.snap.Panes[i].ID == id {
			return &t.snap.Panes[i]
		}
	}
	return nil
}

// resizeTerm resizes a pane terminal. A full-screen scroll region moves with
// the grid: the VT's Resize clamps it to the old edge, which would leave a
// resized pane wrapping at its previous width.
func resizeTerm(term *vt.Term, cols, rows int) {
	g := term.Grid()
	oc, or := g.Size()
	r := g.ScrollRegion()
	g.Resize(cols, rows)
	if r.Top == 0 && r.Bottom == or-1 && r.Left == 0 && r.Right == oc-1 {
		g.SetScrollRegion(vt.ScrollRegion{Top: 0, Bottom: rows - 1, Left: 0, Right: cols - 1})
	}
}
