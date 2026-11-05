package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hinshun/vt10x"
	"golang.org/x/term"
)

const (
	// histRows is how many rows of scrollback the tall terminal keeps.
	histRows = 2000

	// escTimeout bounds how long a lone escape or a partial escape sequence
	// waits for the rest of its bytes.
	escTimeout = 40 * time.Millisecond

	// toastMax is how many toasts the stack holds, toastLifetime how long each
	// one lives, and toastDedupe how close a repeat must be to join the last
	// toast instead of raising a new one.
	toastMax      = 3
	toastLifetime = 3 * time.Second
	toastDedupe   = time.Second
)

type tui struct {
	session string
	conn    *conn
	// keymap is the loaded keybindings. A nil keymap means the built-in
	// defaults, so a test TUI runs without a config file.
	keymap *Config
	cols   int
	rows   int
	snap   *Snapshot
	vts    map[string]vt10x.Terminal
	armed  bool
	toasts []toast // oldest first, newest last
	// floats are arbitrary floating panes rendered over the main layout.
	floats []*FloatingPane
	// hover is the URL under the pointer while ctrl is held, so the frame can
	// underline it. It is nil when no link is hovered.
	hover *linkHover
	// box is the message-box float, nil when closed.
	box *boxView
	// keys is the keymap view float, nil when closed.
	keys  *keymapView
	dirty bool

	// out is the frame sink: os.Stdout in a session, a buffer in a test.
	out io.Writer

	mode     string // "" or "sessions" or "scroll" or "menu" or "rename" or "box" or "help"
	sessions []SessionInfo
	sel      int
	menu     *menu
	// menuPress is set when a right press opens a menu. The release of that
	// press belongs to nobody, so the menu swallows it instead of picking the
	// row under the pointer.
	menuPress bool
	// renameDraft is the text the rename prompt holds while mode is "rename".
	renameDraft string
	// renameID is the id of the workspace or window the prompt renames. The
	// server looks the target up by id, so the request carries this, not the
	// draft.
	renameID string
	// renameKind is "workspace" or "window" while the prompt is open. It picks
	// the action the prompt sends and the label it paints.
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
	scrollVT vt10x.Terminal

	quit     bool
	switchTo string

	escAt   time.Time
	inbuf   []byte
	pending bool

	pasteDeadline time.Time // set while a paste start marker is still arriving
	pasteOff      bool      // one pass with paste detection off

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

	// padZone is the divider under the pointer and padAxis its orientation.
	// A zero zone draws no hover affordance. mouseX/mouseY remember the last
	// pointer cell, so a snapshot can re-resolve the hover.
	padZone Rect
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

	// sw is the floating native switcher, nil when closed. swX and swY are
	// the query cursor cell its last paint asked for, swAt and swPane are the
	// last preview request, and swID numbers the next one.
	sw     *switcher
	swX    int
	swY    int
	swAt   time.Time
	swPane string
	swID   int

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
	builtinCfg  *Config
)

func builtinKeymap() *Config {
	builtinOnce.Do(func() { builtinCfg = LoadDefault() })
	return builtinCfg
}

// km returns the client's keymap, or the built-in defaults when it has none.
func (t *tui) km() *Config {
	if t.keymap != nil {
		return t.keymap
	}
	return builtinKeymap()
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
	bar    paint
	toasts paint
	pal    paint
	help   paint
	sess   paint
	menu   paint
	rename paint

	divRect Rect   // divider zone the last frame painted
	cur     string // cursor block the last frame wrote
}

// frameOpen and frameClose bracket a frame so the terminal swaps it in whole.
// A terminal that does not know the sequence ignores it.
const (
	frameOpen  = "\x1b[?2026h"
	frameClose = "\x1b[?2026l"
)

// histCache is one pane's replay bookkeeping: the rect the ring was asked for
// and whether the reply has landed. The ring itself lives in tui.hist, which
// applyReplay owns.
type histCache struct {
	at  Rect
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
	rect  Rect
	vt    vt10x.View
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

// AddFloatingPane appends a floating pane to the client's render stack.
func (t *tui) AddFloatingPane(f *FloatingPane) {
	t.floats = append(t.floats, f)
	t.dirty = true
}

// RemoveFloatingPane removes a floating pane by ID.
func (t *tui) RemoveFloatingPane(id string) {
	for i, f := range t.floats {
		if f.ID == id {
			t.floats = append(t.floats[:i], t.floats[i+1:]...)
			t.dirty = true
			return
		}
	}
}

// runClient owns the terminal. A session switch returns to this loop with a new
// name, so raw mode and the stdin reader survive it.
func runClient(name string) error {
	// A pane exports its own session name. Attaching that same session from a
	// pane makes a mirror that can also fight over the shared screen size.
	if in := os.Getenv("DM_SESSION"); in != "" && in == name {
		return fmt.Errorf("already inside session %q; attach to another session with -s", name)
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return fmt.Errorf("dm needs a terminal; use dm exec or dm read otherwise")
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	defer term.Restore(fd, old)

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
		next, err := runSession(fd, name, keys, winch)
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

func runSession(fd int, name string, keys <-chan []byte, winch <-chan os.Signal) (string, error) {
	c, err := dialServer(name)
	if err != nil {
		return "", err
	}
	defer c.Close()

	t := &tui{
		session: name, conn: c, vts: map[string]vt10x.Terminal{},
		hist: map[string][]byte{},
	}
	// The keymap is loaded once for the whole session. A missing file is
	// written with the defaults first; a load error is toasted and the
	// built-in defaults carry the session.
	cfg, err := LoadDefaultPath(true)
	t.keymap = cfg
	applyTheme(cfg.Theme())
	if err != nil {
		t.notify(err.Error())
	}
	t.cols, t.rows, _ = term.GetSize(fd)
	defer closeSwitcher(t)

	done := make(chan struct{})
	defer close(done)
	msgs := make(chan Msg, 256)
	go func() {
		for {
			m, err := readMsg(c)
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

	if err := t.conn.send(Msg{Type: "attach", Cols: t.cols, Rows: t.rows}); err != nil {
		return "", err
	}

	ticker := time.NewTicker(16 * time.Millisecond)
	defer ticker.Stop()
	lastDraw := time.Time{}

	for {
		select {
		case b, ok := <-keys:
			if !ok {
				return "", nil
			}
			t.inbuf = append(t.inbuf, b...)
			t.processInput()
		case m, ok := <-msgs:
			if !ok {
				return "", nil
			}
			t.apply(m)
		case <-winch:
			cols, rows, err := term.GetSize(fd)
			if err == nil && (cols != t.cols || rows != t.rows) {
				t.cols, t.rows = cols, rows
				_ = t.conn.send(Msg{Type: "resize", Cols: cols, Rows: rows})
				t.dirty = true
			}
		case <-ticker.C:
			now := time.Now()
			t.expireToasts(now)
			t.expirePaste(now)
			t.expireSwitcher(now)
			t.flushBoxWatchers(now)
			t.expireArmed(now)
			t.expirePrefix(now)
			t.expireMode(now)
			if t.dirty && now.Sub(lastDraw) > 15*time.Millisecond {
				t.draw()
				lastDraw = now
				t.dirty = false
			}
		}
		if t.quit {
			return "", nil
		}
		if t.switchTo != "" {
			return t.switchTo, nil
		}
	}
}

func (t *tui) apply(m Msg) {
	// The history cache is filled on demand, and a replay pushed by a layout
	// change can arrive before the first request.
	if t.hist == nil {
		t.hist = map[string][]byte{}
	}
	switch m.Type {
	case "snapshot":
		t.snap = m.Snap
		t.updatePadHover(t.mouseX, t.mouseY)
		// railWant remembers the last expanded width only, so a collapse or a
		// hide does not replace it with the thin width or zero.
		switch {
		case m.Snap.RailWant > collapsedRail:
			t.railWant = m.Snap.RailWant
		case m.Snap.Rail > collapsedRail:
			t.railWant = m.Snap.Rail
		case t.railWant <= 0:
			t.railWant = defaultRail
		}
		if t.scroll != "" {
			p := t.paneByID(t.scroll)
			if p == nil {
				t.exitScroll()
			} else if t.scrollVT != nil {
				c, r := t.scrollVT.Size()
				if c != p.Rect.W || r != p.Rect.H+histRows {
					t.exitScroll()
				}
			}
		}
		for _, p := range m.Snap.Panes {
			vt := t.vts[p.ID]
			if vt != nil {
				c, r := vt.Size()
				if c != p.Rect.W || r != p.Rect.H {
					vt.Resize(p.Rect.W, p.Rect.H)
				}
			} else {
				t.vts[p.ID] = vt10x.New(vt10x.WithSize(p.Rect.W, p.Rect.H))
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
		// Folds, names and statuses move under an open float.
		if t.sw != nil {
			t.sw.sync(m.Snap)
		}
		if t.box != nil {
			t.box.sync(m.Snap)
			t.dirty = true
		}
		if t.keys != nil {
			t.keys.sync(m.Snap)
			t.dirty = true
		}
		t.dirty = true
	case "screen":
		if p := t.paneByID(m.Target); p != nil {
			applyScreen(t.vts, m.Target, p.Rect, m.Data)
		}
		t.dirty = true
	case "replay":
		// The ring is history. It never touches the live VT: the screen above
		// is the picture, and replaying raw bytes would double-apply them.
		applyReplay(t.hist, m.Target, m.Data)
		t.dirty = true
	case "bytes":
		if vt := t.vts[m.Target]; vt != nil {
			_, _ = vt.Write(m.Data)
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
		// A preview reply carries the id its request named. Only the newest id
		// fills the band: an older one is dropped and a failure stays silent.
		if m.ID > 0 {
			if t.sw != nil && t.sw.acceptPreview(m.ID, previewLines(m)) {
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
		// the list again, at most once per boxWatchInterval.
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

// applyScreen replaces a pane's live VT with a fresh one built from the pane
// daemon's snapshot. The daemon serialised it at the pane's rect, so the size
// matches what the layout will paint.
func applyScreen(vts map[string]vt10x.Terminal, id string, r Rect, data []byte) {
	if r.W < 1 || r.H < 1 {
		return
	}
	vt := vt10x.New(vt10x.WithSize(r.W, r.H))
	_, _ = vt.Write(data)
	vts[id] = vt
}

// applyReplay replaces a pane's history ring only.
func applyReplay(hist map[string][]byte, id string, data []byte) {
	hist[id] = append([]byte(nil), data...)
}

func (t *tui) paneByID(id string) *PaneInfo {
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

// menu is one open context menu: where it sits, its title, its items and the
// selected row. pane is the pane the right click landed on.
type menu struct {
	x, y  int
	w     int
	title string
	pane  string
	items []menuItem
	sel   int
}

type menuItem struct {
	label  string
	key    string
	action string
	run    func()
}

// openMenu builds the pane menu at a zero-based terminal point, clamped to the
// screen, and takes over input.
func (t *tui) openMenu(p *PaneInfo, x, y int) {
	if t.snap == nil {
		return
	}
	title := p.ID
	if p.Name != "" {
		title += " " + p.Name
	}
	items := []menuItem{
		{label: "Split right", action: "split-right", run: func() {
			t.actionOn(p.ID, "split", func(m *Msg) { m.Dir = "right" })
		}},
		{label: "Split down", action: "split-down", run: func() {
			t.actionOn(p.ID, "split", func(m *Msg) { m.Dir = "down" })
		}},
		{label: "Split left", action: "split-left", run: func() {
			t.actionOn(p.ID, "split", func(m *Msg) { m.Dir = "left" })
		}},
		{label: "Split up", action: "split-up", run: func() {
			t.actionOn(p.ID, "split", func(m *Msg) { m.Dir = "up" })
		}},
		{label: "Focus", run: func() { t.actionOn(p.ID, "focus", nil) }},
		{label: "Close pane", action: "close-pane", run: func() {
			t.actionOn(p.ID, "close-pane", nil)
		}},
		{label: "Scrollback", action: "scrollback", run: func() { t.startScroll(p) }},
		{label: "New window", action: "new-window", run: func() { t.action("new-window", nil) }},
		{label: "New workspace", action: "new-workspace", run: func() {
			t.action("new-workspace", nil)
		}},
		{label: "Rename workspace", action: "rename-workspace", run: func() {
			t.closeMenu()
			t.openRenamePrompt()
		}},
		{label: "Switch session", action: "picker", run: func() { t.openPicker() }},
		{label: "Detach", action: "detach", run: func() { t.quit = true }},
	}
	// Each row's key label is the hint the configured main keymap gives it. An
	// unbound action shows no key.
	km := t.km()
	for i := range items {
		if items[i].action != "" {
			items[i].key = km.Key("main", items[i].action)
		}
	}
	w := 34
	if w > t.snap.Cols-2 {
		w = t.snap.Cols - 2
	}
	h := len(items) + 2
	x = clampInt(x, 0, t.snap.Cols-w)
	y = clampInt(y, 0, t.snap.Rows-h)
	t.menu = &menu{x: x, y: y, w: w, title: title, pane: p.ID, items: items}
	t.mode = "menu"
	t.armed = false
	t.dirty = true
}

// openRailMenu builds the sidebar menu for the row under a right press. The
// items depend on the row kind, and every item carries the main keymap's hint
// for its action. The menu is the pane menu's type, anchored at the click and
// clamped to the screen.
func (t *tui) openRailMenu(it railItem, x, y int) {
	if t.snap == nil {
		return
	}
	var items []menuItem
	switch it.kind {
	case railWorkspace:
		items = []menuItem{
			{label: "Rename workspace", action: "rename-workspace", run: func() {
				t.openRenamePromptFor("workspace", it.workspace, it.text)
			}},
			{label: "Close workspace", action: "close-workspace", run: func() {
				t.actionOn(it.workspace, "close-workspace", nil)
			}},
			{label: "New window", action: "new-window", run: func() {
				t.actionOn(it.workspace, "new-window", nil)
			}},
			{label: "New workspace", action: "new-workspace", run: func() {
				t.action("new-workspace", nil)
			}},
		}
	case railWindow, railWindowNote:
		items = []menuItem{
			{label: "Rename window", action: "rename-window", run: func() {
				t.openRenamePromptFor("window", it.window, it.windowName)
			}},
			{label: "Close window", action: "close-window", run: func() {
				t.actionOn(it.window, "close-window", nil)
			}},
			{label: "New window", action: "new-window", run: func() {
				t.actionOn(it.workspace, "new-window", func(m *Msg) { m.Cwd = it.cwd })
			}},
			{label: "Fold/unfold", action: "toggle-fold", run: func() {
				t.actionOn(it.window, "toggle-fold", nil)
			}},
		}
	case railPane:
		items = []menuItem{
			{label: "Focus", action: "focus", run: func() {
				t.actionOn(it.pane, "focus", nil)
			}},
			{label: "Rename window", action: "rename-window", run: func() {
				t.openRenamePromptFor("window", it.window, it.windowName)
			}},
			{label: "Close window", action: "close-window", run: func() {
				t.actionOn(it.window, "close-window", nil)
			}},
			{label: "Fold/unfold", action: "toggle-fold", run: func() {
				t.actionOn(it.window, "toggle-fold", nil)
			}},
		}
	default:
		return
	}
	// Each row's key label is the hint the configured main keymap gives it. An
	// unbound action shows no key.
	km := t.km()
	for i := range items {
		if items[i].action != "" {
			items[i].key = km.Key("main", items[i].action)
		}
	}
	w := 34
	if w > t.snap.Cols-2 {
		w = t.snap.Cols - 2
	}
	h := len(items) + 2
	x = clampInt(x, 0, t.snap.Cols-w)
	y = clampInt(y, 0, t.snap.Rows-h)
	t.menu = &menu{x: x, y: y, w: w, title: it.text, items: items}
	t.mode = "menu"
	t.menuPress = true
	t.armed = false
	t.dirty = true
}

func clampInt(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (t *tui) closeMenu() {
	t.menu = nil
	t.mode = ""
	t.inbuf = nil
	t.dirty = true
}

// menuKey consumes one key while the menu is open. The menu's own bindings are
// the menu mode's; its item keys are only hints. It returns false when it needs
// more bytes for an escape sequence.
func (t *tui) menuKey() bool {
	if t.menu == nil {
		t.closeMenu()
		return true
	}
	if len(t.inbuf) == 0 {
		return true
	}
	action, n, state := t.km().Match("menu", t.inbuf)
	switch state {
	case Partial:
		if !t.pending {
			t.pending = true
			t.escAt = time.Now()
		}
		return false
	case Match:
		t.inbuf = append([]byte(nil), t.inbuf[n:]...)
		t.pending = false
	default:
		// An unbound key closes the menu, as it always has.
		t.inbuf = t.inbuf[1:]
		t.pending = false
		t.closeMenu()
		return true
	}
	switch action {
	case "down", "down-arrow":
		t.menuMove(1)
	case "up", "up-arrow":
		t.menuMove(-1)
	case "pick":
		t.menuPick(t.menu.sel)
	case "close", "close-esc":
		t.closeMenu()
	default:
		if i, ok := pickIndex(action); ok && i < len(t.menu.items) {
			t.menuPick(i)
			return true
		}
		t.closeMenu()
	}
	return true
}

// pickIndex reads a "pick-<n>" action's zero-based row. It reports false for
// any other action.
func pickIndex(action string) (int, bool) {
	rest := strings.TrimPrefix(action, "pick-")
	if rest == action || rest == "" {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 1 {
		return 0, false
	}
	return n - 1, true
}

func (t *tui) menuMove(step int) {
	if t.menu == nil || len(t.menu.items) == 0 {
		return
	}
	t.menu.sel = (t.menu.sel + step + len(t.menu.items)) % len(t.menu.items)
	t.dirty = true
}

func (t *tui) menuPick(i int) {
	if t.menu == nil || i < 0 || i >= len(t.menu.items) {
		t.closeMenu()
		return
	}
	item := t.menu.items[i]
	t.closeMenu()
	item.run()
}

// menuRow returns the item index at a zero-based point, or -1.
func (m *menu) itemAt(x, y int) int {
	if m == nil {
		return -1
	}
	if x < m.x+1 || x >= m.x+m.w-1 {
		return -1
	}
	i := y - (m.y + 1)
	if i < 0 || i >= len(m.items) {
		return -1
	}
	return i
}

var (
	pasteStart = []byte("\x1b[200~")
	pasteEnd   = []byte("\x1b[201~")
)

// takePaste reports whether the buffer starts with a bracketed paste. When the
// whole sequence is present it returns it and its length. When the start marker
// has only partly arrived it returns n == 0 so the caller keeps buffering. A
// lone escape counts as a partial marker: a read can split before the paste
// arrives, and the deadline decides whether it was a real escape.
func takePaste(buf []byte) (seq []byte, n int, started bool) {
	if len(buf) < len(pasteStart) {
		return nil, 0, bytes.HasPrefix(pasteStart, buf)
	}
	if !bytes.HasPrefix(buf, pasteStart) {
		return nil, 0, false
	}
	i := bytes.Index(buf[len(pasteStart):], pasteEnd)
	if i < 0 {
		return nil, 0, true
	}
	n = len(pasteStart) + i + len(pasteEnd)
	return buf[:n], n, true
}

func (t *tui) takePaste() (seq []byte, n int, started bool) {
	if t.pasteOff {
		return nil, 0, false
	}
	return takePaste(t.inbuf)
}

// expirePaste flushes a start marker that never completed once its deadline
// passes. A real lone escape reaches the pane this way.
func (t *tui) expirePaste(now time.Time) bool {
	if t.pasteDeadline.IsZero() || now.Sub(t.pasteDeadline) <= escTimeout {
		return false
	}
	t.staleInput()
	return true
}

// staleInput handles bytes that looked like the start of a paste and then
// stalled. They take the normal input path once.
func (t *tui) staleInput() {
	t.pasteDeadline = time.Time{}
	t.pasteOff = true
	t.processInput()
	t.pasteOff = false
}

// forwardPaste sends a bracketed paste to the focused pane as one keys message.
// A paste closes any open menu, picker, prompt or scrollback first and disarms
// the prefix: its bytes are text for the pane, never dm commands. A paste into
// the rename prompt closes it and the bytes go nowhere: a workspace label is
// typed, not dropped in.
func (t *tui) forwardPaste(seq []byte) {
	t.pasteDeadline = time.Time{}
	t.pending = false
	t.disarm()
	switch t.mode {
	case "rename":
		// A pasted block is not a workspace name: close the prompt and drop
		// its bytes rather than feed them to the draft or the pane.
		t.closeRename()
		return
	case "menu":
		t.closeMenu()
	case "sessions":
		t.closePicker()
	case "scroll":
		t.exitScroll()
	default:
		t.mode = ""
	}
	_ = t.conn.send(Msg{Type: "keys", Keys: seq})
}

func (t *tui) processInput() {
	for len(t.inbuf) > 0 {
		if t.sw != nil {
			// While the float is up every key belongs to it: nothing reaches a
			// pane. A mouse event and a paste are swallowed whole. A lone esc is
			// the close key, so it skips the paste marker check: a bare esc is
			// not the start of one.
			if len(t.inbuf) == 1 && t.inbuf[0] == 0x1b {
				if !t.switcherKey() {
					return
				}
				continue
			}
			if ev, n, ok := parseMouse(t.inbuf); ok {
				t.inbuf = t.inbuf[n:]
				_ = ev
				continue
			} else if n == 0 {
				return // partial sequence, wait for the rest
			}
			if seq, n, started := t.takePaste(); started {
				if n == 0 {
					if t.pasteDeadline.IsZero() {
						t.pasteDeadline = time.Now()
					}
					return
				}
				t.inbuf = append([]byte(nil), t.inbuf[n:]...)
				_ = seq
				continue
			}
			if !t.switcherKey() {
				return
			}
			continue
		}
		if t.mode == "help" {
			// While the view is up every key belongs to it: nothing reaches a
			// pane. A lone esc is the close key, so it skips the paste marker
			// check: a bare esc is not the start of one. A mouse event scrolls
			// and a paste is swallowed whole.
			if len(t.inbuf) == 1 && t.inbuf[0] == 0x1b {
				if !t.keymapKey() {
					return
				}
				continue
			}
			if ev, n, ok := parseMouse(t.inbuf); ok {
				t.inbuf = t.inbuf[n:]
				t.keymapWheel(ev)
				continue
			} else if n == 0 {
				return // partial sequence, wait for the rest
			}
			if seq, n, started := t.takePaste(); started {
				if n == 0 {
					if t.pasteDeadline.IsZero() {
						t.pasteDeadline = time.Now()
					}
					return
				}
				// A pasted block is text for nobody: the view is read-only.
				t.inbuf = append([]byte(nil), t.inbuf[n:]...)
				_ = seq
				continue
			}
			if !t.keymapKey() {
				return
			}
			continue
		}
		if t.mode == "box" {
			// While the view is up every key belongs to it: nothing reaches a
			// pane. A lone esc is the close key, so it skips the paste marker
			// check: a bare esc is not the start of one. A mouse event scrolls
			// and a paste is swallowed whole.
			if len(t.inbuf) == 1 && t.inbuf[0] == 0x1b {
				if !t.boxKey() {
					return
				}
				continue
			}
			if ev, n, ok := parseMouse(t.inbuf); ok {
				t.inbuf = t.inbuf[n:]
				t.boxWheel(ev)
				continue
			} else if n == 0 {
				return // partial sequence, wait for the rest
			}
			if seq, n, started := t.takePaste(); started {
				if n == 0 {
					if t.pasteDeadline.IsZero() {
						t.pasteDeadline = time.Now()
					}
					return
				}
				// A pasted block is text for nobody: the view is read-only.
				t.inbuf = append([]byte(nil), t.inbuf[n:]...)
				_ = seq
				continue
			}
			if !t.boxKey() {
				return
			}
			continue
		}
		if seq, n, started := t.takePaste(); started {
			if n == 0 {
				// The marker is still arriving. Keep buffering; the ticker
				// sends these bytes down the normal path if they stall.
				if t.pasteDeadline.IsZero() {
					t.pasteDeadline = time.Now()
				}
				return
			}
			rest := append([]byte(nil), t.inbuf[n:]...)
			t.forwardPaste(seq)
			t.inbuf = rest
			continue
		}
		// These bytes cannot start a paste, so any earlier partial marker is
		// disproved and must not be flushed early.
		t.pasteDeadline = time.Time{}
		if ev, n, ok := parseMouse(t.inbuf); ok {
			t.inbuf = t.inbuf[n:]
			// The rename prompt owns the keyboard, not the pointer: a stray
			// click must not move the draft or the focus out from under it.
			if t.mode != "rename" {
				t.handleMouse(ev)
			}
			continue
		} else if n == 0 {
			return // partial sequence, wait for the rest
		}
		if t.mode == "rename" {
			if !t.renameKey() {
				return
			}
			continue
		}
		if t.mode == "sessions" {
			if !t.pickerKey() {
				return
			}
			continue
		}
		if t.mode == "menu" {
			if !t.menuKey() {
				return
			}
			continue
		}
		if t.mode == "scroll" {
			if !t.scrollKey() {
				return
			}
			continue
		}
		if !t.armed {
			prefix := t.km().PrefixBytes()
			pi := -1
			if !t.pasteOff {
				pi = bytes.Index(t.inbuf, pasteStart)
			}
			ci := -1
			if len(prefix) > 0 {
				ci = bytes.Index(t.inbuf, prefix)
			}
			if ci >= 0 && (pi < 0 || ci < pi) {
				// The prefix arms. Bytes before it belong to the pane.
				if ci > 0 {
					_ = t.conn.send(Msg{Type: "keys", Keys: t.inbuf[:ci]})
				}
				t.inbuf = append([]byte(nil), t.inbuf[ci+len(prefix):]...)
				t.arm()
				continue
			}
			if pi > 0 {
				// Plain bytes before a paste are keys; the paste itself is
				// handled at the head of the buffer on the next pass.
				_ = t.conn.send(Msg{Type: "keys", Keys: t.inbuf[:pi]})
				t.inbuf = append([]byte(nil), t.inbuf[pi:]...)
				continue
			}
			if pi < 0 && partialPrefix(t.inbuf, prefix) {
				// The buffer ends with the head of the prefix key. Wait for the
				// rest; the ticker forwards the bytes if they stall.
				if !t.pending {
					t.pending = true
					t.escAt = time.Now()
				}
				return
			}
			_ = t.conn.send(Msg{Type: "keys", Keys: t.inbuf})
			t.inbuf = nil
			return
		}
		if !t.armedKey() {
			return
		}
	}
}

// partialPrefix reports whether buf ends with the head of the prefix key, so the
// prefix is still arriving. A one-byte prefix has no head and never waits.
func partialPrefix(buf, prefix []byte) bool {
	for k := 1; k < len(prefix) && k <= len(buf); k++ {
		if bytes.HasSuffix(buf, prefix[:k]) {
			return true
		}
	}
	return false
}

// armedKey consumes the head of the input buffer as one main-mode binding. A
// partial sequence waits for the rest of its bytes. An unbound key notifies and
// disarms. It returns false when it needs more bytes.
func (t *tui) armedKey() bool {
	if len(t.inbuf) == 0 {
		return true
	}
	action, n, state := t.km().Match("main", t.inbuf)
	if state == Match && escapeRun(t.inbuf) > n {
		// The match covered only the first byte of an escape sequence. The
		// sequence as a whole has no binding, so it is one unknown key: the
		// rest of an F1 or a Home must not reach a pane.
		state = NoMatch
	}
	switch state {
	case Partial:
		if !t.pending {
			t.pending = true
			t.escAt = time.Now()
		}
		return false
	case Match:
		t.inbuf = append([]byte(nil), t.inbuf[n:]...)
		t.pending = false
		t.runAction(action)
		return true
	}
	b := t.inbuf[0]
	// An unbound escape sequence is one key too: its tail is not input.
	t.inbuf = append([]byte(nil), t.inbuf[escapeRun(t.inbuf):]...)
	t.pending = false
	t.notify(fmt.Sprintf("no binding for %q", string(b)))
	t.disarm()
	return true
}

// escapeRun returns how many leading bytes belong to one escape sequence. A
// sequence that has not finished yet runs to the end of the buffer.
func escapeRun(buf []byte) int {
	if len(buf) == 0 {
		return 0
	}
	if buf[0] != 0x1b {
		return 1
	}
	i := 1
	if i < len(buf) && (buf[i] == '[' || buf[i] == 'O') {
		i++
		for i < len(buf) {
			c := buf[i]
			i++
			if c >= 0x40 && c <= 0x7e {
				break
			}
		}
	}
	return i
}

// expireArmed resolves an armed sequence that never finished arriving. A lone
// esc is the dismiss key, so a stalled sequence dismisses.
func (t *tui) expireArmed(now time.Time) bool {
	if !t.armed || !t.pending || now.Sub(t.escAt) <= escTimeout {
		return false
	}
	t.pending = false
	t.inbuf = t.inbuf[:0]
	t.disarm()
	return true
}

// expireMode closes an overlay whose lone esc never finished arriving. A bare
// esc is the close key in every overlay, so a stalled one means close.
func (t *tui) expireMode(now time.Time) bool {
	if t.mode == "" || !t.pending || now.Sub(t.escAt) <= escTimeout {
		return false
	}
	t.pending = false
	// The lone esc was consumed by the mode's key handler but left in the
	// buffer. Drop it with the mode, so the next keypress does not forward a
	// stray escape to a pane.
	t.inbuf = t.inbuf[:0]
	switch t.mode {
	case "menu":
		t.closeMenu()
	case "sessions":
		t.closePicker()
	case "scroll":
		t.exitScroll()
	case "box":
		closeBoxView(t)
	case "help":
		closeKeymapView(t)
	case "rename":
		t.closeRename()
	}
	return true
}

// expirePrefix sends a prefix key that never finished arriving to the pane as
// plain input.
func (t *tui) expirePrefix(now time.Time) bool {
	if t.armed || t.mode != "" || t.sw != nil || !t.pending || now.Sub(t.escAt) <= escTimeout {
		return false
	}
	t.pending = false
	if len(t.inbuf) > 0 && t.conn != nil {
		_ = t.conn.send(Msg{Type: "keys", Keys: append([]byte(nil), t.inbuf...)})
	}
	t.inbuf = t.inbuf[:0]
	return true
}

// pickerKey consumes one key while the session picker is open. It returns false
// when it needs more bytes for an escape sequence.
func (t *tui) pickerKey() bool {
	if len(t.inbuf) == 0 {
		return true
	}
	action, n, state := t.km().Match("picker", t.inbuf)
	switch state {
	case Partial:
		if !t.pending {
			t.pending = true
			t.escAt = time.Now()
		}
		return false
	case Match:
		t.inbuf = append([]byte(nil), t.inbuf[n:]...)
		t.pending = false
	default:
		// An unbound key closes the picker.
		t.inbuf = t.inbuf[1:]
		t.pending = false
		t.closePicker()
		return true
	}
	switch action {
	case "down", "down-arrow":
		t.moveSel(1)
	case "up", "up-arrow":
		t.moveSel(-1)
	case "pick":
		t.pickSession()
	default:
		if i, ok := pickIndex(action); ok && i < len(t.sessions) {
			t.sel = i
			t.pickSession()
			return true
		}
		t.closePicker()
	}
	return true
}

// switcherKey consumes one key while the native switcher is open. Its own
// bindings are the switcher mode's; a printable key that matches none of them
// is text for the query. It returns false when it needs more bytes for an
// escape sequence.
func (t *tui) switcherKey() bool {
	if len(t.inbuf) == 0 {
		return true
	}
	if p := t.km().PrefixBytes(); len(p) > 0 && bytes.HasPrefix(t.inbuf, p) {
		// The prefix key pressed again closes the float.
		t.inbuf = append([]byte(nil), t.inbuf[len(p):]...)
		t.pending = false
		closeSwitcher(t)
		return true
	}
	action, n, state := t.km().Match("switcher", t.inbuf)
	if state == Match && escapeRun(t.inbuf) > n {
		// The match covered only the head of an escape sequence. The sequence
		// as a whole is unbound, and an unbound escape is not text.
		state = NoMatch
	}
	switch state {
	case Partial:
		if !t.pending {
			t.pending = true
			t.escAt = time.Now()
		}
		return false
	case Match:
		t.inbuf = append([]byte(nil), t.inbuf[n:]...)
		t.pending = false
	default:
		b := t.inbuf[0]
		n := escapeRun(t.inbuf)
		t.inbuf = append([]byte(nil), t.inbuf[n:]...)
		t.pending = false
		// An escape sequence with no binding is not text: left, right and
		// shift-tab have no meaning in a list, and neither does F1.
		if n == 1 && b >= 0x20 && b < 0x7f {
			t.sw.appendText(rune(b))
			t.dirty = true
		}
		return true
	}
	switch action {
	case "close", "close-ctrl-c", "close-esc":
		closeSwitcher(t)
	case "pick":
		t.pickSwitcher()
	case "clear-query":
		t.sw.clearQuery()
		t.dirty = true
	case "backspace":
		t.sw.backspace()
		t.dirty = true
	case "down", "down-arrow", "down-ctrl-n":
		t.sw.move(1)
		t.dirty = true
	case "up", "up-arrow", "up-ctrl-p":
		t.sw.move(-1)
		t.dirty = true
	}
	return true
}

// pickSwitcher closes the float, then goes to its row. A workspace goes to
// focus-workspace, a window to focus-window and a pane to focus. A gap row or
// an empty selection closes silently.
func (t *tui) pickSwitcher() {
	target := ""
	if it, ok := t.sw.selected(); ok && it.kind != railGap {
		target = t.sw.target()
	}
	closeSwitcher(t)
	if target != "" {
		t.dispatchPick(target)
	}
}

// expireSwitcher closes a float whose lone esc never finished arriving. A bare
// esc is the close key, so a stalled one means close, the way a pending escape
// in the other modes resolves to dismiss.
func (t *tui) expireSwitcher(now time.Time) {
	if t.sw == nil || !t.pending {
		return
	}
	if now.Sub(t.escAt) <= escTimeout {
		return
	}
	t.pending = false
	t.inbuf = t.inbuf[:0]
	closeSwitcher(t)
}

// askPreview sends a read request for the selected pane when the float shows a
// preview the client does not already have. It asks at most once per pane per
// 120 ms and never for a pane in the active window, whose screen is in t.vts.
func (t *tui) askPreview() {
	if t.sw == nil || t.snap == nil || !t.sw.previewDue() {
		return
	}
	it, ok := t.sw.selected()
	if !ok || it.kind != railPane || it.pane == "" {
		return
	}
	if t.vts[it.pane] != nil {
		return
	}
	if it.pane == t.swPane && time.Since(t.swAt) < 120*time.Millisecond {
		return
	}
	t.swID++
	t.swAt = time.Now()
	t.swPane = it.pane
	t.sw.setPreview(t.swID, it.pane)
	_ = t.conn.send(Msg{Type: "req", Action: "read", Target: it.pane, ID: t.swID})
}

// previewLines pulls the lines out of a read reply. The reply carries them in
// Res as an object with a lines array, so a malformed one yields no lines and
// the band stays blank.
func previewLines(m Msg) []string {
	if len(m.Res) == 0 {
		return nil
	}
	var res struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(m.Res, &res); err != nil {
		return nil
	}
	return res.Lines
}

// scrollKey consumes one key in scrollback mode. It returns false when it
// needs more bytes for an escape sequence.
func (t *tui) scrollKey() bool {
	if len(t.inbuf) == 0 {
		return true
	}
	action, n, state := t.km().Match("scroll", t.inbuf)
	switch state {
	case Partial:
		if !t.pending {
			t.pending = true
			t.escAt = time.Now()
		}
		return false
	case Match:
		t.inbuf = append([]byte(nil), t.inbuf[n:]...)
		t.pending = false
	default:
		// An unbound key leaves scrollback.
		t.inbuf = t.inbuf[1:]
		t.pending = false
		t.exitScroll()
		return true
	}
	switch action {
	case "up", "up-arrow":
		t.scrollBy(1)
	case "down", "down-arrow":
		t.scrollBy(-1)
	case "page-up":
		t.scrollBy(t.panHeight())
	case "page-down":
		t.scrollBy(-t.panHeight())
	case "top":
		t.scrollBy(t.maxOff)
	default:
		t.exitScroll()
	}
	return true
}

func (t *tui) panHeight() int {
	if p := t.paneByID(t.scroll); p != nil {
		return p.Rect.H
	}
	return 10
}

func (t *tui) scrollBy(n int) {
	if t.scroll == "" {
		t.exitScroll()
		return
	}
	if t.scrollVT == nil {
		// The ring is still on its way; the wheel waits for it.
		return
	}
	t.offset += n
	if t.offset <= 0 {
		t.exitScroll()
		return
	}
	if t.offset > t.maxOff {
		t.offset = t.maxOff
	}
	t.dirty = true
}

func (t *tui) exitScroll() {
	t.scroll = ""
	t.scrollVT = nil
	t.offset = 0
	t.maxOff = 0
	t.mode = ""
	t.inbuf = nil
	t.dirty = true
}

// startScroll puts a pane in scrollback. The client keeps no history until the
// operator asks for it, so the first entry requests one replay and shows the
// pane's live screen until the ring lands. A second entry asks for nothing.
func (t *tui) startScroll(p *PaneInfo) {
	if p.Alt {
		t.notify("app is on the alternate screen; use its own scroll")
		return
	}
	t.mode = "scroll"
	t.scroll = p.ID
	t.scrollVT = nil
	t.offset = 0
	t.maxOff = 0
	t.armed = false
	t.dirty = true
	if m, ok := t.histMeta[p.ID]; ok {
		if m.got && m.at == p.Rect {
			t.buildScroll(p)
		}
		return
	}
	t.requestHistory(p)
}

// requestHistory asks the server for one pane's replay ring. histMeta is the
// cache key: it goes in before the request, so nothing asks twice.
func (t *tui) requestHistory(p *PaneInfo) {
	if t.hist == nil {
		t.hist = map[string][]byte{}
	}
	if t.histMeta == nil {
		t.histMeta = map[string]histCache{}
	}
	t.histMeta[p.ID] = histCache{at: p.Rect}
	if t.conn != nil {
		_ = t.conn.send(Msg{Type: "req", Action: "replay", Target: p.ID})
	}
}

// buildScroll builds the tall terminal a scrollback pane is shown from, over
// the ring the server sent for the pane's current rect. The live screen keeps
// updating behind it; the tall one is a frozen snapshot.
func (t *tui) buildScroll(p *PaneInfo) {
	rows := replayRows(t.hist[p.ID], p.Rect.W, p.Rect.H, histRows+p.Rect.H)
	t.scrollVT = buildTall(rows, p.Rect.W)
	_, t.maxOff = scrollStart(t.scrollVT, p.Rect.H, 0)
}

// noteHist records a ring that just landed. A ring wrapped for a rect the pane
// no longer has is dropped, and so is one for a pane that is gone.
func (t *tui) noteHist(id string) {
	if t.histMeta == nil {
		t.histMeta = map[string]histCache{}
	}
	p := t.paneByID(id)
	if p == nil {
		delete(t.hist, id)
		delete(t.histMeta, id)
		return
	}
	if m, ok := t.histMeta[id]; ok && m.at != p.Rect {
		delete(t.hist, id)
		delete(t.histMeta, id)
		return
	}
	t.histMeta[id] = histCache{at: p.Rect, got: true}
}

// histArrived fills the scrollback of a pane that was waiting for its ring.
func (t *tui) histArrived(id string) {
	if t.scroll != id || t.scrollVT != nil {
		return
	}
	if m, ok := t.histMeta[id]; !ok || !m.got {
		return
	}
	if p := t.paneByID(id); p != nil {
		t.buildScroll(p)
	}
	t.scrollTop()
}

// scrollTop puts scrollback one row above the live screen. A pane whose ring
// is still on its way waits: histArrived calls this again when it lands.
func (t *tui) scrollTop() {
	if t.scroll == "" {
		return
	}
	if t.scrollVT == nil {
		t.dirty = true
		return
	}
	if t.maxOff == 0 {
		t.exitScroll()
		t.notify("no scrollback yet")
		return
	}
	t.offset = 1
	t.dirty = true
}

func (t *tui) startScrollOnFocus() {
	if t.snap == nil {
		return
	}
	p := t.paneByID(t.snap.Focus)
	if p == nil {
		return
	}
	if p.Alt {
		t.disarm()
		t.notify("app is on the alternate screen; use its own scroll")
		return
	}
	t.startScroll(p)
	if t.scroll != p.ID {
		return
	}
	t.scrollTop()
}

// focusForClick is the pane a left press must focus, or "" when focus is
// already there or the event is not a plain left press.
func focusForClick(p *PaneInfo, cur string, ev mouseEvent) string {
	if ev.btn&3 != 0 || ev.btn&32 != 0 || ev.btn&64 != 0 || ev.release {
		return ""
	}
	if cur == p.ID {
		return ""
	}
	return p.ID
}

// zoomedPane returns the pane the snapshot marks as zoomed, or nil.
func (s *Snapshot) zoomedPane() *PaneInfo {
	if s == nil || s.Zoom == "" {
		return nil
	}
	for i := range s.Panes {
		if s.Panes[i].ID == s.Zoom {
			return &s.Panes[i]
		}
	}
	return nil
}

// zoomedHit returns the zoomed pane when the point lies in the pane area, the
// box that pane fills while zoomed. Every hit test while zoomed goes through
// it, so a click on a hidden sibling's old rectangle lands on the zoomed pane.
func (s *Snapshot) zoomedHit(x, y int) *PaneInfo {
	p := s.zoomedPane()
	if p == nil {
		return nil
	}
	a := paneAreaFor(s.Cols, s.Rows, s.Rail)
	if x < a.X || y < a.Y || x >= a.X+a.W || y >= a.Y+a.H {
		return nil
	}
	return p
}

// visiblePanes is the panes a frame draws: all of them, or only the zoomed one
// while a zoom is set. The snapshot keeps every rect, so the rail, the switcher
// and the pane list keep their geometry.
func visiblePanes(win *Snapshot) []PaneInfo {
	if win == nil {
		return nil
	}
	if z := win.zoomedPane(); z != nil {
		return []PaneInfo{*z}
	}
	return win.Panes
}

// paneAtBox returns the pane whose whole box contains the point, border
// included. A click on a pane's frame belongs to that pane.
func (t *tui) paneAtBox(x, y int) *PaneInfo {
	if t.snap == nil {
		return nil
	}
	if p := t.snap.zoomedHit(x, y); p != nil {
		return p
	}
	for i := range t.snap.Panes {
		r := t.snap.Panes[i].Rect
		b := Rect{X: r.X - 1, Y: r.Y - 1, W: r.W + 2, H: r.H + 2}
		if x >= b.X && x < b.X+b.W && y >= b.Y && y < b.Y+b.H {
			return &t.snap.Panes[i]
		}
	}
	return nil
}

func (t *tui) paneAt(x, y int) *PaneInfo {
	if t.snap == nil {
		return nil
	}
	if p := t.snap.zoomedHit(x, y); p != nil {
		return p
	}
	for i := range t.snap.Panes {
		r := t.snap.Panes[i].Rect
		if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
			return &t.snap.Panes[i]
		}
	}
	return nil
}

// cell is a pane content coordinate: column and row counted from the pane's
// rect top-left.
type cell struct{ x, y int }

// selection is the text dm selected with the mouse. Both cells lie inside the
// content rect of the pane the drag started in.
type selection struct {
	pane   string
	anchor cell
	focus  cell

	// w and h are the pane's content size when the press landed. A snapshot
	// that changes the geometry drops the selection.
	w, h int

	// moved is set once the pointer passed the drag threshold, so a jitter on
	// a click stays a click.
	moved bool
}

// heldPress is a left press on a pane that has not been forwarded yet. A drag
// turns it into a selection, a release in the same cell forwards a click.
type heldPress struct {
	pane  string
	mouse bool
	raws  [][]byte
}

// cellAt maps a screen point to a cell inside p's content rect.
func cellAt(p *PaneInfo, x, y int) (cell, bool) {
	r := p.Rect
	if x < r.X || y < r.Y || x >= r.X+r.W || y >= r.Y+r.H {
		return cell{}, false
	}
	return cell{x: x - r.X, y: y - r.Y}, true
}

// selFocus moves the selection's focus to the point, clamped to the content
// rect of the pane the drag started in.
func (t *tui) selFocus(x, y int) {
	if t.selText == nil {
		return
	}
	p := t.paneByID(t.selText.pane)
	if p == nil || p.Rect.W < 1 || p.Rect.H < 1 {
		return
	}
	c := cell{x: x - p.Rect.X, y: y - p.Rect.Y}
	if c.x < 0 {
		c.x = 0
	}
	if c.y < 0 {
		c.y = 0
	}
	if c.x >= p.Rect.W {
		c.x = p.Rect.W - 1
	}
	if c.y >= p.Rect.H {
		c.y = p.Rect.H - 1
	}
	s := t.selText
	if !s.moved {
		dx, dy := c.x-s.anchor.x, c.y-s.anchor.y
		if dx < 0 {
			dx = -dx
		}
		if dy < 0 {
			dy = -dy
		}
		if dx < 2 && dy < 2 {
			return // a one-cell jitter is still a click
		}
		s.moved = true
	}
	if c != s.focus {
		s.focus = c
		t.dirty = true
	}
}

// rangeIn returns the selection as a row-major cell range for a pane w wide.
func (s *selection) rangeIn(w int) *selRange {
	if s == nil || w < 1 {
		return nil
	}
	lo := s.anchor.y*w + s.anchor.x
	hi := s.focus.y*w + s.focus.x
	if lo > hi {
		lo, hi = hi, lo
	}
	return &selRange{lo: lo, hi: hi}
}

// clearSelection drops the selection and the press held for it. A press that
// outlives its selection would forward a phantom click to the app on release.
func (t *tui) clearSelection() {
	if t.selText != nil || t.press != nil {
		t.selText = nil
		t.press = nil
		t.dirty = true
	}
}

// finishPress ends a held left press. A release in another cell copies the
// selection; the same cell is a click, forwarded to an app that claims the
// mouse.
func (t *tui) finishPress(x, y int, raw []byte) {
	pr := t.press
	t.press = nil
	if pr == nil {
		return
	}
	t.selFocus(x, y)
	if t.selText != nil && t.selText.pane == pr.pane && t.selText.focus != t.selText.anchor {
		t.copySelection()
		return
	}
	t.selText = nil
	t.dirty = true
	if !pr.mouse || t.conn == nil {
		return
	}
	var keys []byte
	for _, r := range pr.raws {
		keys = append(keys, r...)
	}
	keys = append(keys, raw...)
	if len(keys) > 0 {
		_ = t.conn.send(Msg{Type: "keys", Keys: keys})
	}
}

// copySelection reads the selected text out of the pane's VT and sends it to
// the terminal's clipboard.
func (t *tui) copySelection() {
	s := t.selText
	t.selText = nil
	t.dirty = true
	if s == nil {
		return
	}
	p := t.paneByID(s.pane)
	vt := t.vts[s.pane]
	if p == nil || vt == nil {
		return
	}
	text := extractSelection(vt, p.Rect.W, s.anchor, s.focus)
	os.Stdout.WriteString(osc52(text))
	t.notify(fmt.Sprintf("copied %d chars", len(text)))
}

// osc52 is the clipboard sequence for text. Under tmux it must ride the
// passthrough DCS with doubled escapes, or tmux swallows it.
func osc52(text string) string {
	seq := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x1b\\"
	if os.Getenv("TMUX") != "" {
		return "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
	}
	return seq
}

// urlPattern matches an http or https URL on one row. It stops at whitespace
// or a character that commonly delimits a URL in prose.
var urlPattern = regexp.MustCompile(`https?://[^\s"'<>()\[\]{}]+`)

// openURL launches the system's default browser for url. It returns an error
// when no opener is available; the caller reports it as a toast.
func openURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// rowText reads one row of vt as text. Trailing blanks go. It takes the vt
// lock, so the caller must not hold it.
func rowText(vt vt10x.View, w, row int) string {
	if vt == nil || w < 1 {
		return ""
	}
	cols, rows := vt.Size()
	vt.Lock()
	defer vt.Unlock()
	if row < 0 || row >= rows {
		return ""
	}
	var b strings.Builder
	for x := 0; x < w && x < cols; x++ {
		g := normCell(vt.Cell(x, row))
		if g.Char < 0x20 {
			b.WriteByte(' ')
		} else {
			b.WriteRune(g.Char)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// urlAtRow returns the URL in row of vt whose cell range covers x. The row is
// read as text and matched against urlPattern. Cells are treated one per rune,
// which holds for the ASCII URLs this matches.
func urlAtRow(vt vt10x.View, w, row, x int) string {
	text := rowText(vt, w, row)
	for _, loc := range urlPattern.FindAllStringIndex(text, -1) {
		start := len([]rune(text[:loc[0]]))
		end := start + len([]rune(text[loc[0]:loc[1]])) - 1
		if x >= start && x <= end {
			return strings.TrimRight(text[loc[0]:loc[1]], ".,;:!?")
		}
	}
	return ""
}

// urlMatchAtRow returns the URL in row of vt whose cell range covers x and the
// cell range itself. The row is read as text and matched against urlPattern.
// Cells are treated one per rune, which holds for the ASCII URLs this matches.
func urlMatchAtRow(vt vt10x.View, w, row, x int) (string, *selRange) {
	text := rowText(vt, w, row)
	for _, loc := range urlPattern.FindAllStringIndex(text, -1) {
		start := len([]rune(text[:loc[0]]))
		url := strings.TrimRight(text[loc[0]:loc[1]], ".,;:!?")
		end := start + len([]rune(url)) - 1
		if x >= start && x <= end {
			return url, &selRange{lo: start, hi: end}
		}
	}
	return "", nil
}

// paneHoverAt resolves the point in pane p to the URL range under it, or nil.
// The range is row-major in the pane's screen-local cells. The live VT is read,
// or the scrollback view when the pane is scrolled.
func (t *tui) paneHoverAt(p *PaneInfo, x, y int) *linkHover {
	if p == nil {
		return nil
	}
	c, ok := cellAt(p, x, y)
	if !ok {
		return nil
	}
	vt := t.vts[p.ID]
	row := c.y
	if t.scroll == p.ID && t.scrollVT != nil {
		vt = t.scrollVT
		row0, _ := scrollStart(t.scrollVT, p.Rect.H, t.offset)
		row = row0 + c.y
	}
	if vt == nil {
		return nil
	}
	_, rng := urlMatchAtRow(vt, p.Rect.W, row, c.x)
	if rng == nil {
		return nil
	}
	return &linkHover{
		pane: p.ID, w: p.Rect.W,
		region: selRange{lo: c.y*p.Rect.W + rng.lo, hi: c.y*p.Rect.W + rng.hi},
	}
}

// boxHoverAt resolves the point to the URL range under it in the message box
// float, or nil. The float's VT is the box's interior, so the row is local.
func (t *tui) boxHoverAt(x, y int) *linkHover {
	b := t.box
	if b == nil || b.float == nil || b.float.VT == nil {
		return nil
	}
	r := b.float.Rect
	if x < r.X || x >= r.X+r.W || y < r.Y || y >= r.Y+r.H {
		return nil
	}
	cx, cy := x-r.X, y-r.Y
	_, rng := urlMatchAtRow(b.float.VT, r.W, cy, cx)
	if rng == nil {
		return nil
	}
	return &linkHover{
		pane: "box", w: r.W,
		region: selRange{lo: cy*r.W + rng.lo, hi: cy*r.W + rng.hi},
	}
}

// updateLinkHover tracks the URL under the pointer while ctrl is held, so the
// frame can underline it. A pointer off every URL, or a released ctrl, clears
// it. The terminal reports modifiers with mouse events only, so a stationary
// pointer keeps its underline until the next event moves it.
func (t *tui) updateLinkHover(x, y int, ctrl bool) {
	var h *linkHover
	if ctrl {
		switch t.mode {
		case "box":
			h = t.boxHoverAt(x, y)
		case "":
			if p := t.paneAtBox(x, y); p != nil {
				h = t.paneHoverAt(p, x, y)
			}
		}
	}
	if h == nil && t.hover == nil {
		return
	}
	if h != nil && t.hover != nil && *h == *t.hover {
		return
	}
	t.hover = h
	t.dirty = true
}

// hoverFor returns the hover range for the pane or float of id and width w, or
// nil when the hover belongs elsewhere or was measured against another width.
func (t *tui) hoverFor(id string, w int) *selRange {
	h := t.hover
	if h == nil || h.pane != id || h.w != w {
		return nil
	}
	r := h.region
	return &r
}

// urlAt returns the URL under the point in pane p, or "" when none. The live VT
// is read, or the scrollback view when the pane is scrolled. A URL is a run of
// non-space characters starting with http:// or https://; the click must land
// inside that run.
func (t *tui) urlAt(p *PaneInfo, x, y int) string {
	if p == nil {
		return ""
	}
	c, ok := cellAt(p, x, y)
	if !ok {
		return ""
	}
	vt := t.vts[p.ID]
	row := c.y
	if t.scroll == p.ID && t.scrollVT != nil {
		vt = t.scrollVT
		row0, _ := scrollStart(t.scrollVT, p.Rect.H, t.offset)
		row = row0 + c.y
	}
	if vt == nil {
		return ""
	}
	return urlAtRow(vt, p.Rect.W, row, c.x)
}

func (t *tui) handleMouse(ev mouseEvent) {
	if t.sw != nil {
		return // the float owns the pointer while it is open
	}
	if t.snap == nil {
		return
	}
	if ev.release && ev.btn&3 == 0 && t.swallowRelease {
		// The menu already ran this click; its release is not the app's.
		t.swallowRelease = false
		return
	}
	if !ev.release && ev.btn&32 == 0 {
		t.swallowRelease = false
	}
	x, y := ev.x-1, ev.y-1
	t.mouseX, t.mouseY = x, y
	t.updateLinkHover(x, y, ev.btn&16 != 0)
	t.updatePadHover(x, y)
	if t.mode == "menu" {
		if t.menu == nil {
			t.closeMenu()
			return
		}
		if t.menuPress {
			if ev.release {
				t.menuPress = false
				return
			}
			if ev.btn&32 == 0 {
				t.menuPress = false
			}
		}
		m := t.menu
		inside := x >= m.x && x < m.x+m.w && y >= m.y && y < m.y+len(m.items)+2
		row := m.itemAt(x, y)
		motion := ev.btn&32 != 0
		if ev.btn&64 != 0 {
			return // a wheel over the menu is not a click
		}
		if motion {
			// Hover or drag: light up the row under the pointer.
			if row >= 0 && row != m.sel {
				m.sel = row
				t.dirty = true
			}
			return
		}
		if ev.btn&3 == 0 && ev.btn&64 == 0 && !ev.release {
			// Left press: a row runs, an outside point closes the menu and
			// keeps going, so one click both dismisses and focuses.
			if row >= 0 {
				t.menuPick(row)
				t.swallowRelease = true
				return
			}
			if inside {
				return
			}
			t.closeMenu()
		} else {
			switch ev.btn & 3 {
			case 0:
				if row >= 0 {
					t.menuPick(row)
				}
			case 2: // right: release picks, press elsewhere dismisses
				if ev.release {
					if row >= 0 {
						t.menuPick(row)
					} else if !inside {
						t.closeMenu()
					}
				} else if !inside {
					t.closeMenu()
				}
			}
			return
		}
	}
	// The rail grip is the rail's last column. A drag of it adjusts the rail
	// width and swallows every event, motion and release alike, so nothing
	// reaches a pane even when the pointer leaves the rail.
	if t.railDragging {
		if ev.release {
			t.railDragTo(x, true)
			t.railDragging = false
			t.updatePadHover(x, y)
			t.dirty = true
			return
		}
		if ev.btn&32 == 0 {
			t.railDragging = false
			t.dirty = true
			return
		}
		t.railDragTo(x, false)
		return
	}
	// A left press on the grip starts that drag and sends nothing, so a stray
	// click on the last column does nothing.
	if t.press == nil && !t.dragging && ev.btn&3 == 0 && ev.btn&32 == 0 &&
		ev.btn&64 == 0 && !ev.release && railGripAt(t.snap, x, y) {
		t.startRailDrag()
		return
	}
	// The rail owns every event inside its columns. A left press on a pane row
	// focuses that pane; headers and empty space do nothing. A press that began
	// in the rail keeps owning its drag and release, so nothing leaks to a pane.
	if t.press == nil && !t.dragging && t.railClick(ev, x, y) {
		return
	}
	// Ctrl+click on a URL opens it. The click is consumed and its release is
	// swallowed, so the app never sees a press without its release. A ctrl+click
	// elsewhere falls through to the usual press.
	if ev.btn&16 != 0 && ev.btn&3 == 0 && ev.btn&32 == 0 && ev.btn&64 == 0 && !ev.release {
		if p := t.paneAtBox(x, y); p != nil {
			if _, ok := cellAt(p, x, y); ok {
				if u := t.urlAt(p, x, y); u != "" {
					if err := openURL(u); err != nil {
						t.notify("open: " + err.Error())
					} else {
						t.notify("opened " + u)
					}
					t.swallowRelease = true
					return
				}
			}
		}
	}
	// Right press is the mux's context menu, even over an app that owns the
	// mouse. It is a window-manager gesture, not the app's.
	if ev.btn&3 == 2 && ev.btn&32 == 0 && !ev.release {
		p := t.paneAtBox(x, y)
		if p == nil {
			p = t.paneByID(t.snap.Focus)
		}
		if p != nil {
			t.clearSelection()
			t.openMenu(p, x, y)
		}
		return
	}
	left := ev.btn&3 == 0 && ev.btn&32 == 0 && ev.btn&64 == 0 && !ev.release
	if t.dragging {
		if ev.release {
			t.dragTo(x, y, true)
			t.dragging = false
			t.updatePadHover(x, y)
			t.dirty = true
			return
		}
		if ev.btn&32 == 0 {
			t.dragging = false
			t.dirty = true
			return
		}
		t.dragTo(x, y, false)
		return
	}
	// A held left press is a selection drag until the button comes up.
	if t.press != nil {
		if ev.release && ev.btn&3 == 0 {
			t.finishPress(x, y, ev.raw)
			return
		}
		if ev.btn&32 != 0 && ev.btn&3 == 0 {
			t.press.raws = append(t.press.raws, ev.raw)
			t.selFocus(x, y)
			return
		}
		t.clearSelection()
	}
	// A divider belongs to the mux, unless its app claims the mouse.
	if left {
		if ds, ok := dragPress(t.snap, x, y); ok {
			t.clearSelection()
			t.startDrag(ds)
			return
		}
	}
	p := t.paneAtBox(x, y)
	if p == nil {
		return
	}
	if left {
		// A press on a pane starts a selection. The press is held: a drag
		// copies on release, a release in the same cell forwards a click.
		t.clearSelection()
		if focus := focusForClick(p, t.snap.Focus, ev); focus != "" && t.conn != nil {
			_ = t.conn.send(Msg{Type: "req", Action: "focus", Target: focus})
		}
		if c, ok := cellAt(p, x, y); ok {
			t.press = &heldPress{
				pane: p.ID, mouse: p.Mouse,
				raws: [][]byte{ev.raw},
			}
			t.selText = &selection{
				pane: p.ID, anchor: c, focus: c,
				w: p.Rect.W, h: p.Rect.H,
			}
			t.dirty = true
			return
		}
		// A press on the frame is not a selection: the app gets it as before.
		if p.Mouse && t.conn != nil {
			_ = t.conn.send(Msg{Type: "keys", Keys: ev.raw})
		}
		return
	}
	if ev.btn&32 != 0 && ev.btn&3 == 3 {
		// Pointer motion with no button: dm uses it for hover affordances and
		// keeps it from apps that asked only for button motion.
		return
	}
	if p.Mouse {
		// The pane daemon owns the mode; the client's VT may have missed the
		// enable sequence if it scrolled out of the replay ring.
		if t.conn != nil {
			_ = t.conn.send(Msg{Type: "keys", Keys: ev.raw})
		}
		return
	}
	if ev.btn&64 == 0 {
		return
	}
	if t.scroll != p.ID {
		t.startScroll(p)
	}
	if ev.btn&1 == 0 {
		t.scrollBy(3)
	} else {
		t.scrollBy(-3)
	}
}

// railPadAxis is the padAxis of a hover over the rail grip. It marks the zone
// as the rail's own, so the frame shows the hint without a split divider.
const railPadAxis = "rail"

// railGripAt reports whether x,y is the rail's grab strip: its last column,
// above the row the rail keeps free for its bottom control.
func railGripAt(snap *Snapshot, x, y int) bool {
	if snap == nil || snap.Rail <= 0 {
		return false
	}
	return x == snap.Rail-1 && y >= 0 && y < snap.Rows-2
}

// dragStart is the split a mouse press resolved to.
type dragStart struct {
	first  string
	second string
	axis   string
	rect   Rect
	zone   Rect
}

// dragPress decides whether a left press at x,y starts a divider drag. The
// split's own rect is used for the ratio, so nested splits keep their full
// span. The divider always belongs to dm, even when a pane in the subtree
// claims the mouse: the app's PTY covers its content, not the border cells.
func dragPress(snap *Snapshot, x, y int) (dragStart, bool) {
	if snap == nil || snap.Root == nil || snap.Zoom != "" {
		return dragStart{}, false
	}
	_, first, second, axis, r, zone, ok := padHit(snap.Root,
		paneAreaFor(snap.Cols, snap.Rows, snap.Rail), x, y)
	if !ok {
		return dragStart{}, false
	}
	return dragStart{
		first: first, second: second, axis: axis, rect: r,
		zone: zone,
	}, true
}

// barInfo is the transient right side of the status bar: a scroll position, or
// the divider hint while the pointer rests on one.
func (t *tui) barInfo() string {
	if t.scroll != "" {
		return fmt.Sprintf("SCROLL %s +%d/%d", t.scroll, t.offset, t.maxOff)
	}
	if t.padZone != (Rect{}) {
		return "drag to resize"
	}
	return ""
}

// updatePadHover tracks the divider under the pointer so the frame can show a
// grab affordance. It uses the same padHit path as the press, so hover and
// drag agree on the hit area.
func (t *tui) updatePadHover(x, y int) {
	var zone Rect
	axis := ""
	if t.snap != nil && !t.dragging && !t.railDragging && t.mode == "" {
		if ds, ok := dragPress(t.snap, x, y); ok {
			zone, axis = ds.zone, ds.axis
		} else if railGripAt(t.snap, x, y) {
			// The grip is a grab strip too: same hint, no divider painted.
			zone, axis = Rect{t.snap.Rail - 1, 0, 1, t.snap.Rows - 2}, railPadAxis
		}
	}
	if zone == t.padZone && axis == t.padAxis {
		return
	}
	t.padZone, t.padAxis = zone, axis
	t.dirty = true
}

func (t *tui) startDrag(d dragStart) {
	t.dragging = true
	t.padZone = Rect{}
	t.dragFirst = d.first
	t.dragSecond = d.second
	t.dragAxis = d.axis
	t.dragStart = d.rect.X
	space := d.rect.W
	if d.axis == "col" {
		t.dragStart = d.rect.Y
		space = d.rect.H
	}
	t.dragSpace = space
	t.dragAt = time.Time{}
	t.dirty = true
}

// dragTo sends the split ratio the pointer asks for. Unless forced, it sends
// at most once every 30 ms; release forces the final position through.
func (t *tui) dragTo(x, y int, force bool) {
	// Two boxes of at least five cells need ten.
	if t.dragSpace < 10 {
		return
	}
	pos := x
	if t.dragAxis == "col" {
		pos = y
	}
	off := pos - t.dragStart
	if off < 5 {
		off = 5
	}
	if off > t.dragSpace-5 {
		off = t.dragSpace - 5
	}
	if !force && !t.dragAt.IsZero() && time.Since(t.dragAt) < 30*time.Millisecond {
		return
	}
	t.dragAt = time.Now()
	ratio := float64(off) / float64(t.dragSpace)
	_ = t.conn.send(Msg{
		Type: "req", Action: "resize-split",
		Target: t.dragFirst, Other: t.dragSecond, Ratio: ratio,
	})
}

// startRailDrag begins a drag of the rail grip. It sends nothing: a click that
// never moves must not resize the rail.
func (t *tui) startRailDrag() {
	t.railDragging = true
	t.railDragAt = time.Time{}
	if t.snap != nil {
		t.railWidth = t.snap.Rail
	}
	t.padZone = Rect{}
	t.padAxis = ""
	t.dirty = true
}

// railDragTo sends the rail width the pointer asks for. The thin rail is the
// narrowest a drag goes: hiding is the toggle's job, not a drag's. A width the
// drag already asked for is not sent again, so a press that never moved sends
// nothing. Unless forced, it sends at most once every 30 ms; a release forces
// the final width through.
func (t *tui) railDragTo(x int, force bool) {
	if t.snap == nil || t.conn == nil {
		return
	}
	w := x + 1
	if w < collapsedRail {
		w = collapsedRail
	}
	if m := maxRail(t.snap.Cols); w > m {
		w = m
	}
	if w == t.railWidth {
		return
	}
	if !force && !t.railDragAt.IsZero() && time.Since(t.railDragAt) < 30*time.Millisecond {
		return
	}
	t.railDragAt = time.Now()
	t.railWidth = w
	_ = t.conn.send(Msg{Type: "req", Action: "set-rail", Index: w})
}

type mouseEvent struct {
	btn     int
	x, y    int
	release bool
	raw     []byte
}

// parseMouse reads an SGR mouse sequence: ESC [ < b ; x ; y M|m. n is the bytes
// consumed; n == 0 means the sequence is still incomplete.
func parseMouse(b []byte) (ev mouseEvent, n int, ok bool) {
	if len(b) < 3 || b[0] != 0x1b || b[1] != '[' || b[2] != '<' {
		return ev, -1, false
	}
	end := -1
	for i := 3; i < len(b) && i < 48; i++ {
		if b[i] == 'M' || b[i] == 'm' {
			end = i
			break
		}
		if (b[i] < '0' || b[i] > '9') && b[i] != ';' {
			return ev, -1, false
		}
	}
	if end < 0 {
		return ev, 0, false
	}
	parts := strings.Split(string(b[3:end]), ";")
	if len(parts) != 3 {
		return ev, end + 1, false
	}
	btn, err1 := strconv.Atoi(parts[0])
	x, err2 := strconv.Atoi(parts[1])
	y, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return ev, end + 1, false
	}
	ev = mouseEvent{btn: btn, x: x, y: y, release: b[end] == 'm', raw: append([]byte(nil), b[:end+1]...)}
	return ev, end + 1, true
}

func (t *tui) moveSel(step int) {
	if len(t.sessions) == 0 {
		return
	}
	t.sel = (t.sel + step + len(t.sessions)) % len(t.sessions)
	t.dirty = true
}

func (t *tui) pickSession() {
	if len(t.sessions) == 0 {
		t.closePicker()
		return
	}
	name := t.sessions[t.sel].Name
	t.closePicker()
	if name == t.session {
		return
	}
	t.switchTo = name
}

func (t *tui) openPicker() {
	sessions, err := listSessionInfo()
	if err != nil {
		t.notify(err.Error())
		t.disarm()
		return
	}
	if len(sessions) == 0 {
		t.notify("no sessions")
		t.disarm()
		return
	}
	t.sessions = sessions
	t.sel = 0
	for i, s := range sessions {
		if s.Name == t.session {
			t.sel = i
		}
	}
	t.mode = "sessions"
	t.armed = false
	t.dirty = true
}

func (t *tui) closePicker() {
	t.mode = ""
	t.inbuf = nil
	t.dirty = true
}

// openRenamePrompt takes over input for the active workspace's label. The
// draft starts as the current name from the snapshot, and the prompt resolves
// the active workspace's id from the rail entries so the request targets the
// id. A workspace the snapshot does not list opens an empty draft; the prompt
// still opens.
func (t *tui) openRenamePrompt() {
	if t.snap == nil {
		t.disarm()
		return
	}
	id, current := "", ""
	for _, p := range t.snap.All {
		if p.WorkspaceName == t.snap.Workspace {
			id, current = p.Workspace, p.WorkspaceName
			break
		}
	}
	if id == "" {
		// A snapshot whose Workspace field carries the id still resolves it.
		for _, p := range t.snap.All {
			if p.Workspace == t.snap.Workspace {
				id, current = p.Workspace, p.WorkspaceName
				break
			}
		}
	}
	t.openRenamePromptFor("workspace", id, current)
}

// openRenamePromptFor takes over input for the named workspace or window. The
// kind picks the action the prompt sends and the label it paints; the draft
// starts as the current name.
func (t *tui) openRenamePromptFor(kind, id, current string) {
	t.renameKind = kind
	t.renameID = id
	t.renameDraft = current
	t.mode = "rename"
	t.armed = false
	t.pending = false
	t.dirty = true
}

// closeRename drops the prompt and its draft, whatever it held.
func (t *tui) closeRename() {
	t.mode = ""
	t.renameDraft = ""
	t.renameID = ""
	t.renameKind = ""
	t.inbuf = nil
	t.pending = false
	t.dirty = true
}

// submitRename sends the trimmed draft to the server and closes the prompt. An
// empty result cancels: a workspace label is never blank. The prompt closes
// first, so a refusal toast lands on the live picture.
func (t *tui) submitRename() {
	if t.snap == nil {
		t.closeRename()
		return
	}
	name := strings.TrimSpace(t.renameDraft)
	target := t.renameID
	action := "rename-workspace"
	if t.renameKind == "window" {
		action = "rename-window"
	}
	t.closeRename()
	if name == "" {
		return
	}
	if t.conn != nil {
		_ = t.conn.send(Msg{Type: "req", Action: action, Target: target, Name: name})
	}
}

// renameKey consumes one key while the rename prompt is open. No byte leaves
// the client: every key edits or ends the draft. Esc cancels at once, so the
// prompt never waits for the rest of an escape sequence. It returns false when
// it needs more bytes for an escape sequence.
func (t *tui) renameKey() bool {
	if len(t.inbuf) == 0 {
		return true
	}
	action, n, state := t.km().Match("rename", t.inbuf)
	if state == Partial {
		if t.inbuf[0] == 0x1b {
			// A lone esc cancels. An arrow key cancels too: its remaining bytes
			// are dropped rather than typed into the draft.
			t.inbuf = t.inbuf[1:]
			t.pending = false
			t.closeRename()
			return true
		}
		if !t.pending {
			t.pending = true
			t.escAt = time.Now()
		}
		return false
	}
	if state == Match {
		t.inbuf = append([]byte(nil), t.inbuf[n:]...)
		t.pending = false
		switch action {
		case "cancel":
			t.closeRename()
		case "submit":
			t.submitRename()
		case "backspace":
			if r := []rune(t.renameDraft); len(r) > 0 {
				t.renameDraft = string(r[:len(r)-1])
			}
			t.dirty = true
		case "clear":
			t.renameDraft = ""
			t.dirty = true
		default:
			// The arrows cancel as they always have.
			t.closeRename()
		}
		return true
	}
	b := t.inbuf[0]
	t.inbuf = t.inbuf[1:]
	t.pending = false
	if b >= 0x20 && b <= 0x7e {
		t.renameDraft += string(rune(b))
		t.dirty = true
	}
	return true
}

// key runs one single-byte key through the main keymap. Tests call it directly;
// processInput goes through armedKey, which also handles longer sequences.
func (t *tui) key(b byte) {
	t.inbuf = append(t.inbuf, b)
	t.pending = false
	t.armedKey()
}

// reload re-reads the default config file and its named theme. On a read, parse
// or theme error it returns the error and leaves the running keymap and theme
// untouched. On success it swaps both in place, marks the frame dirty and
// toasts.
func (t *tui) reload() error {
	cfg, err := LoadDefaultPath(false)
	if err != nil {
		return err
	}
	t.keymap = cfg
	applyTheme(cfg.Theme())
	t.dirty = true
	t.notify("config reloaded")
	return nil
}

// runAction runs one action from the main keymap.
func (t *tui) runAction(name string) {
	switch name {
	case "split-right":
		t.action("split", func(m *Msg) { m.Dir = "right" })
	case "split-down", "split-down-alt":
		t.action("split", func(m *Msg) { m.Dir = "down" })
	case "split-left":
		t.action("split", func(m *Msg) { m.Dir = "left" })
	case "split-up":
		t.action("split", func(m *Msg) { m.Dir = "up" })
	case "scrollback":
		t.startScrollOnFocus()
	case "focus-left", "focus-left-arrow":
		t.action("focus-dir", func(m *Msg) { m.Dir = "left" })
	case "focus-down", "focus-down-arrow":
		t.action("focus-dir", func(m *Msg) { m.Dir = "down" })
	case "focus-up", "focus-up-arrow":
		t.action("focus-dir", func(m *Msg) { m.Dir = "up" })
	case "focus-right", "focus-right-arrow":
		t.action("focus-dir", func(m *Msg) { m.Dir = "right" })
	case "next-pane":
		t.action("next-pane", nil)
	case "prev-pane":
		t.action("prev-pane", nil)
	case "close-pane":
		t.action("close-pane", nil)
	case "close-window":
		pane := ""
		if t.snap != nil {
			pane = t.snap.Focus
		}
		wid := t.windowForPane(pane)
		if wid == "" {
			t.notify("no window for " + pane)
			t.disarm()
			return
		}
		t.actionOn(wid, "close-window", nil)
	case "zoom-pane":
		t.action("zoom-pane", nil)
	case "new-window":
		t.action("new-window", nil)
	case "next-window":
		t.action("next-window", nil)
	case "prev-window":
		t.action("prev-window", nil)
	case "new-workspace":
		t.action("new-workspace", nil)
	case "next-workspace":
		t.action("next-workspace", nil)
	case "switcher":
		openSwitcher(t)
	case "picker":
		t.openPicker()
	case "rename-workspace":
		t.openRenamePrompt()
	case "toggle-rail":
		t.toggleRail()
	case "message-box":
		openBoxView(t)
	case "help":
		openKeymapView(t)
		t.disarm()
	case "detach", "detach-alt":
		t.quit = true
	case "dismiss", "dismiss-esc":
		t.disarm()
	case "send-prefix":
		if p := t.km().PrefixBytes(); len(p) > 0 && t.conn != nil {
			_ = t.conn.send(Msg{Type: "keys", Keys: append([]byte(nil), p...)})
		}
		t.disarm()
	case "reload":
		if err := t.reload(); err != nil {
			t.notify(err.Error())
		}
		t.disarm()
	default:
		if strings.HasPrefix(name, "window-") {
			if n, err := strconv.Atoi(strings.TrimPrefix(name, "window-")); err == nil &&
				n >= 1 && n <= 10 {
				i := n - 1
				t.action("select-window", func(m *Msg) { m.Index = i })
				return
			}
		}
		t.notify(fmt.Sprintf("no binding for %q", name))
		t.disarm()
	}
}

// toggleRail cycles the rail: expanded, thin, hidden. A hidden rail comes back
// at the last expanded width, or the default when none was seen.
func (t *tui) toggleRail() {
	want := t.railToggleWidth(true)
	t.action("set-rail", func(m *Msg) { m.Index = want })
}

// toggleRailControl is a click on the rail's bottom row: full <-> thin. Hiding
// stays on `prefix b`, so a thin rail expands here instead of going hidden.
func (t *tui) toggleRailControl() {
	want := t.railToggleWidth(false)
	t.action("set-rail", func(m *Msg) { m.Index = want })
}

// railToggleWidth is the width a rail toggle asks for. The key and the bottom
// control both take their width from here, so the two routes cannot drift. A
// hidden or thin rail comes back at the last expanded width, or the default
// when none was seen. hide lets `prefix b` reach the hidden state; the control
// passes false and expands instead.
func (t *tui) railToggleWidth(hide bool) int {
	rail := 0
	if t.snap != nil {
		rail = t.snap.Rail
	}
	switch {
	case rail <= collapsedRail:
		if hide && rail > 0 {
			return 0
		}
		if t.railWant > 0 {
			return t.railWant
		}
		return defaultRail
	default:
		return collapsedRail
	}
}

// railClick consumes a mouse event over the sidebar. A left press on the bottom
// control row toggles the rail full <-> thin. A left press on a pane row focuses
// that pane. Every other rail event is swallowed so it never reaches a pane,
// and a drag that began in the rail stays swallowed outside it.
func (t *tui) railClick(ev mouseEvent, x, y int) bool {
	if t.snap == nil || t.snap.Rail <= 0 {
		return false
	}
	inside := x >= 0 && x < t.snap.Rail && y >= 0 && y < t.snap.Rows-1
	if !inside {
		if !t.railPress {
			return false
		}
		if ev.release || ev.btn&32 != 0 {
			if ev.release {
				t.railPress = false
			}
			return true
		}
		t.railPress = false
		return false
	}
	if ev.release {
		t.railPress = false
		return true
	}
	// A right press on a row opens the sidebar menu for that row. The press
	// arms railPress and the menu swallows its release, so the pane menu
	// never sees it.
	if ev.btn&3 == 2 && ev.btn&32 == 0 && ev.btn&64 == 0 {
		t.railPress = true
		if it, ok := railItemAt(t.snap, x, y); ok {
			t.openRailMenu(it, x, y)
		}
		return true
	}
	if ev.btn&3 == 0 && ev.btn&32 == 0 && ev.btn&64 == 0 {
		t.railPress = true
		if railControlAt(t.snap, x, y) {
			// The bottom row is the control: it toggles the rail and
			// never focuses a pane or folds a window.
			if t.conn != nil {
				t.toggleRailControl()
			}
		} else if id := railPaneAt(t.snap, x, y); id != "" && t.conn != nil {
			_ = t.conn.send(Msg{Type: "req", Action: "focus", Target: id})
		} else if id := railWindowAt(t.snap, x, y); id != "" && t.conn != nil {
			_ = t.conn.send(Msg{Type: "req", Action: "toggle-fold", Target: id})
		}
	}
	return true
}

// arm shows the action box. It stays up until an action runs or esc
// dismisses it; it never expires on a timer.
func (t *tui) arm() {
	t.armed = true
	// A prefix that arrived in pieces waited with pending set. The arm starts a
	// fresh sequence, so that wait is over.
	t.pending = false
	t.toasts = nil
	t.dirty = true
}

func (t *tui) disarm() {
	t.armed = false
	t.dirty = true
}

func (t *tui) action(name string, fill func(*Msg)) {
	target := ""
	if t.snap != nil {
		target = t.snap.Focus
	}
	t.actionOn(target, name, fill)
}

// windowForPane resolves a pane id to its window id from the snapshot. An
// unknown pane returns "".
func (t *tui) windowForPane(pane string) string {
	if t.snap == nil {
		return ""
	}
	for _, p := range t.snap.All {
		if p.ID == pane {
			return p.Window
		}
	}
	return ""
}

func (t *tui) actionOn(target, name string, fill func(*Msg)) {
	m := Msg{Type: "req", Action: name, Target: target}
	if fill != nil {
		fill(&m)
	}
	_ = t.conn.send(m)
	t.disarm()
}

// draw writes one frame. A frame that changes one cell writes one row: the
// rows of every pane are fingerprinted, and each band of the picture (the
// boxes, the grips, the divider, the rail, the bar, the toasts and the
// overlays) is compared with its own bytes from the previous frame. A steady
// frame writes nothing. A frame never clears the screen unless the picture
// itself is not stable.
func (t *tui) draw() {
	win := t.snap
	if win == nil || win.Cols < 1 || win.Rows < 1 {
		return
	}

	// The bands that own a whole buffer of bytes. Each one is rendered every
	// frame and written only when its bytes differ from the last frame's.
	var box, grip, div, rail, bar, toasts, pal, help, sess, menu, rename bytes.Buffer
	for _, p := range visiblePanes(win) {
		drawPaneBox(&box, p.Rect, p.ID == win.Focus)
	}
	drawGrips(&grip, win)
	// The rail grip paints the same heavy line as a split divider, over the
	// strip its grab zone covers.
	drawDivider(&div, t.padZone, t.padAxis)
	drawRail(&rail, win)
	drawBar(&bar, win, t.armed, t.barInfo())
	drawToasts(&toasts, win, t.toasts)
	if t.sw != nil {
		t.swX, t.swY = drawSwitcher(&pal, t.sw, win, t.vts)
		t.askPreview()
	}
	if t.armed {
		drawHelp(&help, win.Cols, win.Rows, t.km().HelpEntries())
	}
	if t.mode == "sessions" {
		drawSessions(&sess, win.Cols, win.Rows, t.sessions, t.sel, t.session)
	}
	if t.mode == "menu" && t.menu != nil {
		drawMenu(&menu, t.menu)
	}
	if t.mode == "rename" {
		drawRename(&rename, win, t.renameID, t.renameDraft, t.renameKind)
	}

	// A picture that is not stable repaints whole and may clear: a size, a
	// layout, a pane set, a scroll or a screen-owning mode changed.
	sig := t.layoutSig(win)
	full := t.laysig != sig
	t.laysig = sig

	// The divider paints over the borders and grips between two panes, so a
	// divider that moves leaves cells the boxes must paint again.
	padFreed := !full && !bytes.Equal(t.last.div.buf, div.Bytes())
	var blank bytes.Buffer
	if padFreed {
		blankRect(&blank, t.last.divRect)
	}
	t.last.divRect = Rect{}
	if div.Len() > 0 {
		t.last.divRect = t.padZone
	}

	var floatBufs []bytes.Buffer
	for _, f := range t.floats {
		var fb bytes.Buffer
		f.Draw(&fb, t.hoverFor(f.ID, f.Rect.W))
		floatBufs = append(floatBufs, fb)
	}
	dirty := map[int]bool{}
	type band struct {
		p       *paint
		buf     *bytes.Buffer
		overlay bool
		force   bool
		write   bool
	}
	bands := []band{
		{p: &t.last.box, buf: &box, force: padFreed},
		{p: &t.last.grip, buf: &grip, force: padFreed},
		{p: &t.last.rail, buf: &rail},
		{p: &t.last.div, buf: &div},
		{p: &t.last.bar, buf: &bar},
		{p: &t.last.toasts, buf: &toasts, overlay: true},
		{p: &t.last.pal, buf: &pal, overlay: true},
		{p: &t.last.help, buf: &help, overlay: true},
		{p: &t.last.sess, buf: &sess, overlay: true},
		{p: &t.last.menu, buf: &menu, overlay: true},
		{p: &t.last.rename, buf: &rename, overlay: true},
	}
	for i := range t.floats {
		bands = append(bands, band{p: &t.floats[i].last, buf: &floatBufs[i], overlay: true})
	}
	// An overlay paints on top of everything, so the rows it leaves need the
	// whole picture back: the panes under it and the bands around it. Without a
	// clear the rows would keep the overlay's glyphs.
	overlayChanged := false
	for i := range bands {
		b := &bands[i]
		b.write = full || b.force || !bytes.Equal(b.p.buf, b.buf.Bytes())
		if !b.write {
			continue
		}
		old := b.p.rows
		b.p.buf = append(b.p.buf[:0], b.buf.Bytes()...)
		// Every band's rows matter, not only an overlay's: the rows it writes
		// go under a standing overlay, and the rows an overlay takes and leaves
		// belong to the panes again.
		b.p.rows = bandRows(b.p.buf)
		if !b.overlay {
			continue
		}
		overlayChanged = true
		if !full {
			for _, y := range old {
				dirty[y] = true
			}
			for _, y := range b.p.rows {
				dirty[y] = true
			}
		}
	}

	// Pane rows: fingerprint each content row and repaint only the rows that
	// moved. The fingerprint covers the cells, the selection on them and the
	// scrollback row they come from.
	paints := t.panePaints(win)
	sigs := make(map[string][]uint64, len(paints))
	for _, pp := range paints {
		sigs[pp.id] = paneRowSigs(pp)
	}
	if !full {
		for _, pp := range paints {
			old := t.rowsig[pp.id]
			for row, s := range sigs[pp.id] {
				if row >= len(old) || old[row] != s {
					dirty[pp.rect.Y+row] = true
				}
			}
		}
	}
	t.rowsig = sigs

	var body bytes.Buffer
	for y := 0; y < win.Rows; y++ {
		if !full && !dirty[y] {
			continue
		}
		for _, pp := range paints {
			if y < pp.rect.Y || y >= pp.rect.Y+pp.rect.H {
				continue
			}
			drawPaneRow(&body, pp.vt, pp.rect, y-pp.rect.Y, pp.row0, pp.sel, pp.hover)
		}
	}
	if padFreed {
		body.Write(blank.Bytes())
	}
	// The rows already written this frame, by any layer. An overlay is painted
	// last, so one that is older in the paint order goes back on top of every
	// row that a pane row, a freed divider or a band wrote this frame.
	written := map[int]bool{}
	for y := range dirty {
		written[y] = true
	}
	for _, y := range bandRows(blank.Bytes()) {
		written[y] = true
	}
	for i := range bands {
		b := &bands[i]
		// A band that moved an overlay comes back whole, and an overlay goes
		// back on top whenever a row under it was written.
		if !b.write {
			b.write = overlayChanged || (b.overlay && rowsHit(b.p.rows, written))
		}
		if !b.write {
			continue
		}
		body.Write(b.p.buf)
		for _, y := range b.p.rows {
			written[y] = true
		}
	}

	cur := t.cursorBlock()
	if body.Len() == 0 && !full && cur == t.last.cur {
		return
	}
	var out bytes.Buffer
	out.WriteString(frameOpen)
	out.WriteString("\x1b[?7l")
	if full {
		out.WriteString("\x1b[0m\x1b[2J\x1b[H")
	}
	out.Write(body.Bytes())
	out.WriteString(cur)
	out.WriteString("\x1b[?7h")
	out.WriteString(frameClose)
	t.writeFrame(out.Bytes())
	t.last.cur = cur
}

// writeFrame sends one frame to the frame writer.
func (t *tui) writeFrame(b []byte) {
	w := t.out
	if w == nil {
		w = os.Stdout
	}
	_, _ = w.Write(b)
}

// layoutSig is the shape of the picture. When it changes the cells cannot be
// diffed against the last frame: the size, the layout, a pane, the scrollback,
// the mode or the palette moved, so the next frame repaints whole.
func (t *tui) layoutSig(win *Snapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%dx%d r%d z=%s", win.Cols, win.Rows, win.Rail, win.Zoom)
	for _, p := range win.Panes {
		fmt.Fprintf(&b, " %s:%d,%d,%d,%d", p.ID, p.Rect.X, p.Rect.Y, p.Rect.W, p.Rect.H)
	}
	for _, f := range t.floats {
		fmt.Fprintf(&b, " F%s:%d,%d,%d,%d", f.ID, f.Rect.X, f.Rect.Y, f.Rect.W, f.Rect.H)
	}
	fmt.Fprintf(&b, " f=%s m=%s s=%s", win.Focus, t.mode, t.scroll)
	if t.sw != nil {
		fmt.Fprintf(&b, " p=%+v", t.sw.rect)
	}
	return b.String()
}

// panePaints is the content each pane shows this frame.
func (t *tui) panePaints(win *Snapshot) []panePaint {
	panes := visiblePanes(win)
	out := make([]panePaint, 0, len(panes))
	for _, p := range panes {
		pp := panePaint{id: p.ID, rect: p.Rect}
		pp.hover = t.hoverFor(p.ID, p.Rect.W)
		if p.ID == t.scroll && t.scrollVT != nil {
			pp.vt = t.scrollVT
			pp.row0, _ = scrollStart(t.scrollVT, p.Rect.H, t.offset)
		} else {
			pp.vt = t.vts[p.ID]
			if t.selText != nil && t.selText.pane == p.ID {
				pp.sel = t.selText.rangeIn(p.Rect.W)
			}
		}
		if pp.vt != nil {
			out = append(out, pp)
		}
	}
	return out
}

// paneRowSigs fingerprints one pane's content rows. Every visible change moves
// the row's hash: the glyph, its colours, its mode, the selection over it and
// the scrollback row it comes from.
func paneRowSigs(pp panePaint) []uint64 {
	cols, rows := pp.vt.Size()
	out := make([]uint64, pp.rect.H)
	pp.vt.Lock()
	defer pp.vt.Unlock()
	for row := 0; row < pp.rect.H; row++ {
		h := uint64(14695981039346656037)
		for x := 0; x < pp.rect.W; x++ {
			var g vt10x.Glyph
			if x < cols && pp.row0+row < rows {
				g = normCell(pp.vt.Cell(x, pp.row0+row))
			} else {
				g = normCell(g)
			}
			h = mix(h, uint64(g.Char))
			h = mix(h, uint64(g.FG))
			h = mix(h, uint64(g.BG))
			h = mix(h, uint64(g.Mode))
			if pp.sel.has(row*pp.rect.W+x) || pp.hover.has(row*pp.rect.W+x) {
				h = mix(h, 1)
				if pp.hover.has(row*pp.rect.W + x) {
					h = mix(h, 2)
				}
			}
		}
		out[row] = h
	}
	return out
}

// mix folds one value into a row fingerprint (FNV-1a).
func mix(h, v uint64) uint64 {
	return (h ^ v) * 1099511628211
}

// bandRows returns the rows a band's bytes address, in screen order. A band
// writes with absolute cursor moves, so its own bytes say where it lands.
// It reads every cursor position form: ESC [ row ; col H, ESC [ row H and the
// bare ESC [ H (both finals, H and f, like a terminal does). A band that moves
// the cursor another way addresses no row here, so keep bands on absolute
// moves: TestBandsUseAbsoluteCursorMoves fails if one does.
func bandRows(buf []byte) []int {
	var rows []int
	seen := map[int]bool{}
	for i := 0; i+2 < len(buf); i++ {
		if buf[i] != 0x1b || buf[i+1] != '[' {
			continue
		}
		j := i + 2
		n := 0
		for j < len(buf) && (buf[j] >= '0' && buf[j] <= '9') {
			n = n*10 + int(buf[j]-'0')
			j++
		}
		// The rest of the parameter bytes, up to the final byte.
		for j < len(buf) && (buf[j] == ';' || (buf[j] >= '0' && buf[j] <= '9')) {
			j++
		}
		if j >= len(buf) || (buf[j] != 'H' && buf[j] != 'f') {
			continue
		}
		// A zero or one row, or a bare move, is the top row.
		y := 0
		if n > 1 {
			y = n - 1
		}
		if !seen[y] {
			seen[y] = true
			rows = append(rows, y)
		}
	}
	sort.Ints(rows)
	return rows
}

// rowsHit reports whether any of rows is in the dirty set.
func rowsHit(rows []int, dirty map[int]bool) bool {
	for _, y := range rows {
		if dirty[y] {
			return true
		}
	}
	return false
}

// cursorBlock is the tail of a frame that places the cursor, or hides it. It
// is empty when the frame leaves the cursor alone.
func (t *tui) cursorBlock() string {
	if t.sw != nil {
		// The float owns the cursor while it is up: the query cell, shown.
		return fmt.Sprintf("\x1b[%d;%dH\x1b[?25h", t.swY+1, t.swX+1)
	}
	win := t.snap
	if win == nil || t.mode != "" {
		return ""
	}
	p := t.paneByID(win.Focus)
	if p == nil {
		return ""
	}
	vt := t.vts[p.ID]
	if vt == nil {
		return ""
	}
	vt.Lock()
	cur := vt.Cursor()
	visible := vt.CursorVisible()
	vt.Unlock()
	if visible && cur.X < p.Rect.W && cur.Y < p.Rect.H {
		return fmt.Sprintf("\x1b[%d;%dH\x1b[?25h", p.Rect.Y+cur.Y+1, p.Rect.X+cur.X+1)
	}
	return "\x1b[?25l"
}

// conn wraps the server socket with a write mutex.
type conn struct {
	net.Conn
	mu sync.Mutex
}

func dialServer(name string) (*conn, error) {
	nc, err := ensureServer(name)
	if err != nil {
		return nil, err
	}
	return &conn{Conn: nc}, nil
}

func (c *conn) send(m Msg) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return writeMsg(c.Conn, m)
}
