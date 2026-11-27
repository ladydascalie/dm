package client

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync"
	"time"

	"dm/internal/box"
	"dm/internal/boxview"
	"dm/internal/keymap"
	"dm/internal/keymapview"
	"dm/internal/layout"
	"dm/internal/panel"
	"dm/internal/paths"
	"dm/internal/rail"
	"dm/internal/switcher"
	"dm/internal/theme"
	"dm/internal/transport"
	"dm/internal/ui"
	"dm/internal/version"
	"dm/internal/vt"
	"dm/internal/wire"

	"golang.org/x/term"
)

const (
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
	// one lives, toastDedupe how close a repeat must be to join the last toast
	// instead of raising a new one, and toastDrip how long each cell of the
	// drain band stands still.
	toastMax      = 3
	toastLifetime = 3 * time.Second
	toastDedupe   = time.Second
	toastDrip     = 50 * time.Millisecond

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
	// keymapSeam is true when the harness supplied the keymap, so a reload has
	// no file to re-read and keeps what the harness gave.
	keymapSeam bool
	// th is the loaded theme. A nil theme means the built-in theme, so a test
	// TUI runs without a config file.
	th   *theme.Theme
	cols int
	rows int
	snap *wire.Snapshot
	vts  map[string]*vt.Term
	// sizing and sizerKnown are the server's verdict on this client. The
	// sizing client fits each pane's terminal to the layout rect; every other
	// client keeps the pane's own grid and draws it in a larger rect, which
	// the painter blanks around. A client that never receives a sizer frame
	// keeps fitting to the rect: that is what every plain TUI and the goldens
	// have always done.
	sizing     bool
	sizerKnown bool
	armed      bool
	toasts     []toast // oldest first, newest last
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
	keys *keymapview.KeymapView
	// shelf is the shelf float, nil when closed. It owns mode "shelf": the
	// strip row of shelved pane chips over the active member's screen.
	shelf *shelfView
	// lsw is the open layout palette, nil when closed. It lives in mode
	// "layout" and owns the keyboard while it is up.
	lsw *layoutSwitcher
	// git is the git facts float, nil when closed. It carries no mode: it
	// takes no keys but esc, so the desk keeps typing under it. It follows the
	// rail's git mark and closes when that mark leaves the rail.
	git *gitFloat
	// ask is the ask float, nil when closed. It owns mode "ask": the focused
	// pane's open question, one question at a time. askDeferred holds an ask
	// that arrived while another overlay had the keyboard, and askAnswerID
	// numbers the answer request whose reply the float reads.
	ask         *askView
	askDeferred bool
	askAnswerID int
	// askClosed is the id of an ask this client put away itself. An answered
	// ask stays listed until its owner closes it, and a float that could only
	// leave on a snapshot would trap the desk when the owner is gone.
	askClosed string
	// askHidden is the id of an ask the reader put out of the way with the
	// toggle key. The float is off the desk and its picks are kept, so the
	// reader can scroll or reread the pane before answering, and the same key
	// brings the form back.
	askHidden string
	// readOnly marks a client that may read the session and not answer the
	// questions in it: the ask float offers no send and no decline.
	readOnly bool
	// prefs is what the operator chose last run and wants back on the next
	// one, and prefsPath is the file it came from. An empty path means there
	// is no filesystem to read or write: a browser page supplies its own
	// keymap, so nothing is loaded and nothing is saved.
	prefs     prefs
	prefsPath string
	dirty     bool

	// out is the frame sink: a counting wrapper over os.Stdout in a session,
	// a buffer in a test.
	out io.Writer

	// onEvent reports what the operator did to a harness, nil in a session.
	onEvent   func(Event)
	lastState stateSummary
	// clipboard is the harness's clipboard, nil when the terminal owns it and
	// a copy rides OSC 52 or an external tool.
	clipboard func(string) bool

	mode     string // "" or "sessions" or "scroll" or "menu" or "rename" or "box" or "help" or "ask"
	sessions []transport.SessionInfo
	sel      int
	menu     *menu
	// menuPress is set when a right press opens a menu. The release of that
	// press belongs to nobody, so the menu swallows it instead of picking the
	// row under the pointer.
	menuPress bool
	// renameDraft is the text the rename prompt holds while mode is "rename".
	renameDraft ui.Input
	// renameID is the id of the workspace, window or pane the prompt renames.
	// The server looks the target up by id, so the request carries this, not
	// the draft.
	renameID string
	// renameKind is "workspace", "window" or "pane" while the prompt is open.
	// It picks the action the prompt sends and the float title it paints.
	renameKind string

	// histRows is the scrollback window in lines: the config's history-lines.
	// It sizes the tall terminal and the replay the client asks for. Zero means
	// the config default, which is what a bare tui in a test carries.
	histRows int
	// hist is each pane's replay ring. It is not filled as output arrives:
	// the first scrollback entry asks the server for one replay and caches it
	// here. histMeta records the rect a ring was asked for and whether it has
	// landed, so a second entry asks for nothing and a ring wrapped for an old
	// layout is never shown.
	hist     map[string][]byte
	histMeta map[string]histCache
	scroll   string // pane id shown from scrollback
	// search is the scrollback search: the query row's draft and the accepted
	// query. It is nil when no query is open and none is accepted, and leaving
	// scrollback drops it.
	search   *scrollSearch
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
	// railOff is the first rail item row painted, the rail's own scroll: a
	// list taller than the sidebar would otherwise clip its tail with no way
	// to reach it. Every read clamps it, so a fold that shortens the list
	// cannot leave a blank rail.
	railOff   int
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

	// winDrag tracks a drag of a window row in the rail: the window under the
	// press, its workspace, the row the press landed on, and where the drop
	// boundary now rests. nil when no drag is in flight; armed turns true at
	// the first row change, and the release sends the move.
	winDrag *winDragState
	// wsDrag tracks a drag of a workspace row, with the same shape and rules
	// as winDrag: the marker snaps to the project run's own boundaries.
	wsDrag *wsDragState

	// lastTabClick and lastTabClickPos track the last left press on a tab bar
	// tab, so a second press in the same cell within the double-click window
	// opens the window rename prompt instead of selecting the window.
	lastTabClick    time.Time
	lastTabClickPos struct{ X, Y int }

	// cp is the open command palette, nil when closed. cpX and cpY are the
	// query cursor cell its last paint asked for.
	cp  *commandPalette
	cpX int
	cpY int
	// sw is the floating native switcher, nil when closed. swX and swY are
	// the query cursor cell its last paint asked for, swAt and swPane are the
	// last preview request, and swID numbers the next one.
	sw     *switcher.Switcher
	swX    int
	swY    int
	swAt   time.Time
	swPane string
	swID   int
	// float is the floating command, nil when closed. floatID numbers the
	// request, so a reply to an older one opens nothing. floatGone is the id
	// of the float last closed, so its late frames are the child's last words
	// and never pane news.
	float     *floatView
	floatID   int
	floatGone string

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

	// toggleID numbers the next toggle-secrets request, so the reply toast
	// reports the pane and state this keypress produced and never one from
	// another window.
	toggleID int

	// reader is the open body reader overlay, nil when closed. The box view
	// owns its window and selection, so the client reads them off the view.
	reader *bodyReader

	// notifyRead is the seq below which every notify/ entry is read: the mark
	// the box view paints its rows from and the bar badge counts from, so the
	// two cannot disagree. notifyID numbers every id'd message request the
	// notify work sends, and notifyRoute remembers which read each outstanding
	// id belongs to, so a reply never lands in another handler.
	notifyRead  int
	notifyID    int
	notifyRoute map[int]string

	// notifyCursor is the highest notify/ seq the client has seen, and
	// notifySeen says it has caught up at least once since attach, so the
	// first catch-up toasts the newest notifyFetch entries instead of the
	// whole backlog. notifyPending says one is in flight, and notifyWant
	// holds a broadcast that landed while it was.
	notifyCursor  int
	notifySeen    bool
	notifyPending bool
	notifyWant    bool

	// notifyRecent is the newest notify/ entries the client knows, newest
	// first, capped at notifyRecentLimit. The catch-up reply feeds it and a
	// clear prunes it, so the bar badge counts the same rows the box view's
	// unread marks carry.
	notifyRecent []box.Entry

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

// histWindow is the scrollback window in lines. A tui that never loaded a
// config, like a test tui, reports the built-in default, so a zero field and a
// field set from a default config mean the same window.
func (t *tui) histWindow() int {
	if t.histRows > 0 {
		return t.histRows
	}
	return t.km().HistoryLines()
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
	prompt paint
	lsw    paint

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
// first row to show from it, the selection to mark, the hover to underline and
// the search highlight over its rows.
type panePaint struct {
	id    string
	rect  layout.Rect
	vt    *vt.Term
	row0  int
	sel   *selRange
	hover *selRange
	mark  *searchMark
}

// toast is one short message for the operator. A plain toast carries text
// alone; a notify toast also carries the entry's state and topic, so its box
// can name the state in the border and the topic above the message. Its time
// is when it was raised, so the stack can expire it, and left is the fraction of
// its life still to run, which sets how much of the drain band stays coloured.
// step is the drip the band was last painted at, so a tick inside the same drip
// leaves the frame alone.
type toast struct {
	text  string
	state string
	topic string
	at    time.Time
	left  float64
	step  int
}

// notify raises a plain toast: a client message with no entry behind it. The
// box carries the text alone.
func (t *tui) notify(text string) {
	t.pushToast(toast{text: text})
}

// configFailed announces a config load or reload failure in one row. The toast
// holds forty cells and dies in three seconds, so it names the verb that prints
// the whole line. The loader also keeps the text in <config>.error.
func (t *tui) configFailed() {
	t.notify("config error, see: dm config")
}

// announceUpdate raises the toast for a line the caller decided is worth
// saying, such as a newer release. It is the visible half of the boot notice:
// the stderr line survives in the scrollback, this one is in the operator's
// face for a few seconds. An empty line says nothing.
func (t *tui) announceUpdate(line string) {
	if line == "" {
		return
	}
	t.pushToast(toast{text: line})
}

// notifyEntry raises the toast a notify entry deserves: the state in the title
// bar, the topic above the message, and the message itself when the entry has
// one.
func (t *tui) notifyEntry(e box.Entry) {
	t.pushToast(toast{text: e.Msg, state: string(e.State), topic: e.Key})
}

// pushToast appends a toast to the stack. A repeat of the newest toast inside
// toastDedupe is the same toast, not a second one; an empty one is dropped. The
// stack keeps the last toastMax.
func (t *tui) pushToast(x toast) {
	if x.text == "" && x.topic == "" {
		return
	}
	x.at = time.Now()
	x.left = 1
	if n := len(t.toasts); n > 0 {
		if last := t.toasts[n-1]; last.same(x) && x.at.Sub(last.at) < toastDedupe {
			return
		}
	}
	t.toasts = append(t.toasts, x)
	if len(t.toasts) > toastMax {
		t.toasts = t.toasts[len(t.toasts)-toastMax:]
	}
	t.dirty = true
}

// same reports whether two toasts are the same announcement: the same state,
// topic and message, so a repeat about another pane or another state is a new
// toast.
func (x toast) same(y toast) bool {
	return x.text == y.text && x.state == y.state && x.topic == y.topic
}

// expireToasts drops the toasts past toastLifetime and advances the drain band
// of the ones still up. The band stands still for toastDrip per step, so a tick
// that lands inside the same step marks nothing dirty; the width is painted from
// left, so a ticker the idle decay slowed still lands on the right cell.
func (t *tui) expireToasts(now time.Time) {
	if len(t.toasts) == 0 {
		return
	}
	kept := t.toasts[:0]
	for _, x := range t.toasts {
		age := now.Sub(x.at)
		if age >= toastLifetime {
			t.dirty = true
			continue
		}
		if step := int(age / toastDrip); step != x.step {
			x.step = step
			t.dirty = true
		}
		x.left = 1 - float64(age)/float64(toastLifetime)
		kept = append(kept, x)
	}
	t.toasts = kept
}

// Run owns the terminal. A session switch returns to this loop with a new
// name, so raw mode and the stdin reader survive it.
func Run(name string) error { return RunWithLayout(name, "") }

// RunWithLayout is Run with a layout file for a session that is not running
// yet: the server this start spawns seeds its state from the file and the
// client then attaches the normal way. An empty path is Run. The layout
// applies to the first start only, so a session switch from inside the client
// attaches the next session as it stands.
func RunWithLayout(name, layoutPath string) error {
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
	// Any other attach from inside must find a live session: a name with no
	// server would be spawned, nesting a new session inside this pane.
	if in := os.Getenv("DM_SESSION"); in != "" {
		if in == name {
			return fmt.Errorf("already inside session %q; attach to another session with -s", name)
		}
		live := false
		if sessions, err := transport.ListSessions(); err == nil {
			for _, s := range sessions {
				if s.Name == name {
					live = s.Live
					break
				}
			}
		}
		if !live {
			return fmt.Errorf("session %q is not live; attaching it from inside %q would nest it; create it outside dm", name, in)
		}
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return fmt.Errorf("dm needs a terminal; use dm exec or dm read otherwise")
	}
	// The update notice is one line on stderr, before the alternate screen, so
	// it survives on exit. A stale cache leaves a detached refresh behind; the
	// attach never waits for it and a failed spawn is not a failure to attach.
	stateDir := paths.StateDir()
	notice := updateNotice(stateDir, version.Version, time.Now())
	if notice != "" {
		fmt.Fprintln(os.Stderr, notice)
	}
	if shouldCheck(stateDir, time.Now()) {
		spawnUpdateCheck()
	}
	// The chrome and the pane content both step down to what the outer
	// terminal can show. Set once, before the first frame.
	theme.SetDepth(theme.DetectDepth(os.Getenv))
	old, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	defer func() { _ = term.Restore(fd, old) }()

	// Ask the outer terminal for its colours once per attach, before the
	// alternate screen. The reply arrives on stdin and the detector consumes
	// it; a terminal that never answers leaves the pane defaults alone.
	_, _ = os.Stdout.WriteString("\x1b]10;?\x1b\\\x1b]11;?\x1b\\")
	// The clipboard probe rides the same reply machinery: a terminal that
	// honours OSC 52 answers the read, one that drops the sequence stays
	// silent and a write falls to an external carrier instead.
	_, _ = os.Stdout.WriteString(clipQuery())
	clipProbeAt = time.Now()

	keys := make(chan []byte, 64)
	go stdinReader(keys)
	winch := make(chan os.Signal, 1)
	notifyResize(winch)
	defer signal.Stop(winch)

	var out bytes.Buffer
	out.WriteString("\x1b[?1049h\x1b[?25l\x1b[2J\x1b[H\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1006h\x1b[?2004h")
	_, _ = os.Stdout.Write(out.Bytes())
	defer func() {
		_, _ = os.Stdout.WriteString("\x1b[?2004l\x1b[?1003l\x1b[?1006l\x1b[?1002l\x1b[?1000l\x1b[?25h\x1b[?1049l\x1b[0m")
	}()

	for {
		next, err := runSession(fd, name, layoutPath, notice, keys, winch, &pace)
		if err != nil {
			return err
		}
		if next == "" {
			return nil
		}
		name = next
		// The layout already did its work on the first start, and the update
		// notice was announced by the first frame.
		layoutPath, notice = "", ""
		_, _ = os.Stdout.WriteString("\x1b[0m\x1b[2J\x1b[H")
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
	t.expireOSCReply(now)
	t.expireSwitcher(now)
	t.expireCommandPalette(now)
	t.previewTick(now)
	t.flushBoxWatchers(now)
	t.expireArmed(now)
	t.expirePrefix(now)
	t.expireMode(now)
	// An ask the overlay held back opens once the overlay is gone.
	t.openDeferredAsk()
	if t.flushIdle(now) {
		t.dirty = true
		pace.reset()
	}
	if t.dirty {
		if pace.shouldDraw(now, true) {
			t.frame(now, pace, sink)
		}
	} else if len(t.toasts) == 0 {
		pace.decay(now)
	}
	if len(t.toasts) > 0 {
		// A live toast drains on its own clock, so the ticker stays at the
		// floor while one is up: a frame that carried no band change, or one
		// still owed, must not slow the band.
		pace.floor()
	}
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

// runSession dials the session and hands it to attachConn over the connection
// it owns. A layout path rides the dial, so the server it spawns seeds from the
// file; an already-running session ignores it.
func runSession(fd int, name, layoutPath, notice string, keys <-chan []byte, winch <-chan os.Signal, pace *pacer) (string, error) {
	c, err := transport.DialWithLayout(name, layoutPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = c.Close() }()
	size := func() (int, int) {
		cols, rows, _ := term.GetSize(fd)
		return cols, rows
	}
	return attachConn(c, Harness{Out: os.Stdout, Keys: keys, Winch: winch, Size: size, Session: name, Notice: notice}, pace)
}

// Event is one thing the operator did, reported to a harness that wants to
// check a step against it. The client is the only place that knows which
// action a key ran, so the report comes from here and not from the session.
type Event struct {
	// Type is "action" or "state".
	Type string
	// Name is the keybinding action, such as "split-right".
	Name string
	// Panes, Focus, Rail, Zoom and Sync describe the session on a "state"
	// event, which comes once per change and only when something changed.
	Panes int
	Focus string
	Rail  int
	Zoom  bool
	Sync  bool
}

// Harness is what a caller with no terminal supplies. Run owns a terminal: raw
// mode, the key reader, the colour probe, the resize signal, os.Stdout. A
// harness owns none of that, so it hands over the connection, where frames go,
// where keys and resizes come from, and where to report what happened.
type Harness struct {
	Conn    net.Conn
	Out     io.Writer
	Keys    <-chan []byte
	Winch   <-chan os.Signal
	Size    func() (int, int)
	Session string
	// OnEvent receives one Event per action the operator ran. Nil ignores them.
	OnEvent func(Event)
	// Clipboard receives text the operator copied, for a harness with no
	// terminal to write OSC 52 to. It reports whether the text landed; a
	// harness that returns false gets no "copied" toast. Nil writes through the
	// terminal, the way Run always has.
	Clipboard func(text string) bool
	// Depth is the colour depth the client emits. Nil detects it from the
	// environment, the way Run does. A harness with no environment to detect,
	// such as a browser page, names the depth its renderer supports instead.
	Depth *theme.Depth
	// Keymap is the keybindings the session runs. Nil loads the default config
	// file, the way Run does. A harness with no filesystem, such as a browser
	// page, passes the built-in defaults instead, so no path is read and no load
	// error is toasted.
	Keymap *keymap.Config
	// ReadOnly mounts the client on a session it may read and not answer. The
	// ask float offers no send and no decline there, so a viewer cannot answer
	// a question put to the operator. No shipped harness sets it yet.
	ReadOnly bool
	// Notice is one line the first frame announces as a toast, such as a newer
	// release. The caller owns the decision and its once-a-day gate; the client
	// only makes it visible where the operator is already looking. Empty says
	// nothing.
	Notice string
}

// Attach runs one attached session over a connection the caller made. It
// returns the session to switch to, empty when the client is done.
func Attach(h Harness) (string, error) {
	// A harness that knows its renderer names the depth. One that does not gets
	// the environment's answer, exactly as Run does.
	if h.Depth != nil {
		theme.SetDepth(*h.Depth)
	} else {
		theme.SetDepth(theme.DetectDepth(os.Getenv))
	}
	return attachConn(&transport.Conn{Conn: h.Conn}, h, &pacer{})
}

// lastState is what the harness heard last, so a snapshot that changes nothing
// it can see does not wake it.
type stateSummary struct {
	panes int
	focus string
	rail  int
	zoom  bool
	sync  bool
}

// reportState tells the harness what the session looks like now, when it is not
// what the last report said.
func (t *tui) reportState() {
	if t.snap == nil || t.onEvent == nil {
		return
	}
	now := stateSummary{
		panes: len(t.snap.Panes),
		focus: t.snap.Focus,
		rail:  t.snap.Rail,
		zoom:  t.snap.Zoom != "",
		sync:  t.snap.Sync,
	}
	if now == t.lastState {
		return
	}
	t.lastState = now
	t.emit(Event{
		Type: "state", Panes: now.panes, Focus: now.focus,
		Rail: now.rail, Zoom: now.zoom, Sync: now.sync,
	})
}

// emit reports one event to the harness, if it asked for any.
func (t *tui) emit(e Event) {
	if t.onEvent != nil {
		t.onEvent(e)
	}
}

// attachConn is one attached session over a connection already in hand.
func attachConn(c *transport.Conn, h Harness, pace *pacer) (string, error) {
	t := &tui{
		session: h.Session, conn: c, vts: map[string]*vt.Term{},
		hist: map[string][]byte{}, onEvent: h.OnEvent, clipboard: h.Clipboard,
	}
	// The sink counts a frame's bytes so the pacer can tell whether the
	// frame said anything.
	sink := &frameSink{w: h.Out}
	t.out = sink
	// The keymap is loaded once for the whole session. A harness that supplies
	// one owns it: a page has no filesystem, so there is no path to read and no
	// load error to toast. Otherwise a missing file is written with the defaults
	// first; a load error is toasted and the built-in defaults carry the
	// session.
	cfg := h.Keymap
	if cfg == nil {
		var err error
		cfg, err = keymap.LoadDefaultPath(true)
		if err != nil {
			t.configFailed()
		}
		// A client that reads the config also has the state tree, so the
		// choices the operator saved last run are read once, here, for the
		// whole session. A harness that supplies the keymap has no files to
		// read, so a page reads and writes nothing.
		t.prefsPath = paths.PrefsPath()
		t.prefs = loadPrefs(t.prefsPath)
	} else {
		// Nothing on disk owns the keymap, so a reload has nothing to re-read.
		t.keymapSeam = true
	}
	t.keymap = cfg
	t.announceUpdate(h.Notice)
	t.readOnly = h.ReadOnly
	t.histRows = cfg.HistoryLines()
	t.glyphs = rail.ResolveGlyphs(cfg.GlyphSet())
	th := cfg.Theme()
	t.th = &th
	t.cols, t.rows = h.Size()
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
		case b, ok := <-h.Keys:
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
		case <-h.Winch:
			cols, rows := h.Size()
			if cols != t.cols || rows != t.rows {
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

		// A snapshot may shorten the rail's list or the rail itself, so the
		// scroll offset must not point past the last page.
		t.railOff = rail.ClampOffset(t.snap, t.railOff)
		t.reportState()
		t.snap.WindowLabel = t.km().WindowLabel()
		t.updatePadHover(t.mouseX, t.mouseY)
		t.updateRailHover()
		// The list, the rail and the folds under the open facts box moved.
		t.syncGit()
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
				if c != p.Rect.W || r != p.Rect.H+t.histWindow() {
					t.exitScroll()
				}
			}
		}
		for _, p := range m.Snap.Panes {
			if p.Shelf {
				// A shelved pane has no rect, so there is no size to fit here:
				// the shelf float sizes its terminal when it summons it. Its
				// terminal, if it had one before it was shelved, stays as it
				// is and keeps taking the pane's bytes.
				continue
			}
			term := t.vts[p.ID]
			if term != nil {
				c, r := term.Grid().Size()
				if t.sizesPanes() && (c != p.Rect.W || r != p.Rect.H) {
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
			r := t.paneRect(p)
			if p == nil || r.W != t.selText.w || r.H != t.selText.h {
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
		if t.float != nil {
			t.syncFloat(m.Snap)
		}
		if t.shelf != nil {
			t.syncShelf(m.Snap)
		}
		if t.box != nil {
			t.box.Sync(m.Snap, t.theme())
			if t.reader != nil {
				t.reader.Sync(boxview.ReaderRect(t.snap, t.box.ListLen()), t.theme())
			}
			t.dirty = true
		}
		if t.keys != nil {
			t.keys.Sync(m.Snap)
			t.dirty = true
		}
		// A question the session lists is session state, so the float follows
		// every snapshot: it opens on the focused pane's ask and closes when
		// the ask is answered or gone.
		t.syncAsk()
		t.dirty = true
	case "sizer":
		// The server says whether this client sizes the panes. A change to
		// false stops the rect fit; a change to true starts it again, and the
		// next snapshot or screen lands at the rect.
		if m.Sizing != nil {
			t.sizerKnown = true
			t.sizing = *m.Sizing
			t.dirty = true
		}
	case "screen":
		if p := t.paneByID(m.Target); p != nil {
			if p.Shelf {
				// A shelved pane is drawn by the shelf float at that box's
				// size, not at a rect the snapshot does not carry.
				t.applyShelfScreen(m.Target, m.VTSnap)
			} else {
				t.applySnapshot(m.Target, p.Rect, m.VTSnap)
			}
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
	case "scrub":
		// The pane rewrote its ring and its screen after a redaction toggle
		// came back on: what a switch-off reveal drew is gone from the
		// pane, and the picture has to follow. The snapshot is the repaired
		// screen; the cached ring is dropped, so the next scrollback asks
		// the pane for the scrubbed copy instead of serving the revealed
		// one. A scrollback already open is rebuilt from that fresh ring.
		delete(t.hist, m.Target)
		delete(t.histMeta, m.Target)
		if t.scroll == m.Target {
			if p := t.paneByID(m.Target); p != nil {
				t.scrollVT = nil
				t.requestHistory(p)
			}
		}
		if p := t.paneByID(m.Target); p != nil {
			if p.Shelf {
				t.applyShelfScreen(m.Target, m.VTSnap)
			} else {
				t.applySnapshot(m.Target, p.Rect, m.VTSnap)
			}
		} else {
			t.applyPreviewScreen(m.Target, m.VTSnap)
		}
		t.dirty = true
	case "bytes":
		// A float's bytes belong to its own terminal, never to a pane's.
		if t.applyFloatBytes(m) {
			break
		}
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
		// A float's exit never toasts: the live float keeps its last screen
		// and says so, and a closed float is already off the screen.
		if t.float != nil && m.Target == t.float.id {
			t.floatExited()
			break
		}
		if m.Target == t.floatGone {
			break
		}
	case "resp":
		if m.Action == "yank" && m.ID == t.yankID {
			t.takeYank(m)
			break
		}
		// The toggle reply toasts the state the pane landed on, so an
		// unprotected pane never reads as protected.
		if m.Action == "toggle-secrets" && m.ID == t.toggleID {
			t.takeToggle(m)
			break
		}
		// The float-open reply shows the floating command. A failure toasts
		// and opens nothing.
		if m.Action == "float-open" && m.ID > 0 && m.ID == t.floatID {
			t.takeFloatOpen(m)
			break
		}
		// The answer's reply is the daemon's verdict on the float's send. A
		// refusal keeps the float open with the code on it.
		if m.Action == "ask-answer" && m.ID > 0 && m.ID == t.askAnswerID {
			t.takeAskAnswer(m)
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
			// A notify catch-up asks for the notify/ entries past the cursor.
			// The route map says which read the id belongs to, so a reply
			// never lands in another handler.
			if m.Action == "msg-list" && t.notifyRoute[m.ID] == notifyRouteCatchUp {
				t.takeNotifyList(m)
				delete(t.notifyRoute, m.ID)
			}
			// A clear from the view refetches the log, whatever the count was;
			// the reply itself carries only the number that went.
			if m.Action == "msg-clear" && t.notifyRoute[m.ID] == notifyRouteClear {
				delete(t.notifyRoute, m.ID)
				if t.box != nil {
					t.boxRequest()
				}
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
			if t.floatErrorSwallowed(m.Error) {
				break
			}
			t.notify(m.Error)
		}
	case "box":
		// A post or a clear landed. An open view asks for the log again; a
		// closed one needs nothing.
		if t.box != nil {
			t.boxRequest()
		}
		// Either way the notify branch catches up, so a toast finds the new
		// entries whether or not the log view is open.
		t.notifyCatchUp()
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
// snapshot's size and, for the sizing client, reflow to the rect, which is what
// the old ANSI replay did. Refusing a mismatch leaves the pane blank until the
// program redraws.
//
// A client that is not sizing the panes keeps the snapshot's own size: the
// sizing client claimed it, and a rect that is larger is just a larger box to
// paint the pane's grid into.
func (t *tui) applySnapshot(id string, r layout.Rect, snap *vt.Snapshot) bool {
	if r.W < 1 || r.H < 1 || snap == nil {
		return false
	}
	term := t.vts[id]
	if term == nil {
		term = vt.NewTerm(snap.Cols, snap.Rows)
	}
	if err := term.Restore(snap); err != nil {
		return false
	}
	if c, rows := term.Grid().Size(); t.sizesPanes() && (c != r.W || rows != r.H) {
		resizeTerm(term, r.W, r.H)
	}
	t.vts[id] = term
	return true
}

// sizesPanes reports whether this client fits each pane's terminal to its
// layout rect. It is true until the server says otherwise, so a wire peer that
// predates the sizer frame, and the golden scenes, keep the cell fit.
func (t *tui) sizesPanes() bool {
	return !t.sizerKnown || t.sizing
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

// flushIdle places each quiet terminal's pending grapheme cluster and reports
// whether it placed any. A terminal is quiet when a full tick has passed with
// no bytes, so a stream never flushes mid-cluster. A float's terminal is one of
// them: it is not in vts, so it is matched by its own id.
func (t *tui) flushIdle(now time.Time) bool {
	placed := false
	for id, at := range t.lastByte {
		if now.Sub(at) < idleFlush {
			continue
		}
		delete(t.lastByte, id)
		if t.float != nil && t.float.id == id {
			t.float.vt.Flush()
			placed = true
			continue
		}
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
