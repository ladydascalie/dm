package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
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
	cols    int
	rows    int
	snap    *Snapshot
	vts     map[string]vt10x.Terminal
	armed   bool
	toasts  []toast // oldest first, newest last
	dirty   bool

	// out is the frame sink: os.Stdout in a session, a buffer in a test.
	out io.Writer

	mode     string // "" or "sessions" or "scroll" or "menu" or "rename"
	sessions []SessionInfo
	sel      int
	menu     *menu
	// renameDraft is the text the rename prompt holds while mode is "rename".
	renameDraft string

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

	// pal is the floating television switcher, nil when closed. palOut is
	// its byte stream, nil once the child has gone.
	pal    *palette
	palOut chan []byte

	// rowsig holds one fingerprint per pane content row, so a steady frame
	// repaints only the rows that moved. laysig is the shape of the picture:
	// when it changes the next frame repaints whole. last is what the previous
	// frame painted, so each band is written only when its bytes change.
	rowsig map[string][]uint64
	laysig string
	last   lastFrame
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

// panePaint is one pane's content for one frame: the terminal to read, the
// first row to show from it and the selection to mark.
type panePaint struct {
	id   string
	rect Rect
	vt   vt10x.View
	row0 int
	sel  *selRange
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
	t.cols, t.rows, _ = term.GetSize(fd)
	defer closePalette(t)

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
				if t.pal != nil {
					t.pal.resize(paletteRect(cols, rows))
				}
				t.dirty = true
			}
		case b, ok := <-t.palOut:
			if !ok {
				paletteClosed(t)
				break
			}
			paletteBytes(t, b)
		case <-ticker.C:
			now := time.Now()
			t.expireToasts(now)
			t.expirePaste(now)
			if t.armed && t.pending && now.Sub(t.escAt) > 40*time.Millisecond {
				t.pending = false
				t.inbuf = t.inbuf[:0]
				t.disarm()
			}
			if t.mode != "" && t.pending && now.Sub(t.escAt) > 40*time.Millisecond {
				t.pending = false
				switch t.mode {
				case "menu":
					t.closeMenu()
				case "sessions":
					t.closePicker()
				case "scroll":
					t.exitScroll()
				}
			}
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
	case "close":
		t.quit = true
	case "exit":
		t.notify("pane " + m.Target + " exited")
	case "resp":
		if !m.OK {
			t.notify(m.Error)
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
	label string
	key   string
	run   func()
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
		{"Split right", "|", func() { t.actionOn(p.ID, "split", func(m *Msg) { m.Dir = "right" }) }},
		{"Split down", "-", func() { t.actionOn(p.ID, "split", func(m *Msg) { m.Dir = "down" }) }},
		{"Split left", "<", func() { t.actionOn(p.ID, "split", func(m *Msg) { m.Dir = "left" }) }},
		{"Split up", "^", func() { t.actionOn(p.ID, "split", func(m *Msg) { m.Dir = "up" }) }},
		{"Focus", "", func() { t.actionOn(p.ID, "focus", nil) }},
		{"Close pane", "x", func() { t.actionOn(p.ID, "close-pane", nil) }},
		{"Scrollback", "[", func() { t.startScroll(p) }},
		{"New window", "c", func() { t.action("new-window", nil) }},
		{"New workspace", "t", func() { t.action("new-workspace", nil) }},
		{"Rename workspace", "r", func() { t.closeMenu(); t.openRenamePrompt() }},
		{"Switch session", "s", func() { t.openPicker() }},
		{"Detach", "d", func() { t.quit = true }},
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

// menuKey consumes one key while the menu is open. It returns false when it
// needs more bytes for an escape sequence.
func (t *tui) menuKey() bool {
	if t.menu == nil {
		t.closeMenu()
		return true
	}
	if len(t.inbuf) == 0 {
		return true
	}
	b := t.inbuf[0]
	if b == 0x1b {
		if len(t.inbuf) < 3 {
			if !t.pending {
				t.pending = true
				t.escAt = time.Now()
			}
			return false
		}
		seq := string(t.inbuf[:3])
		t.inbuf = t.inbuf[3:]
		switch seq {
		case "\x1b[A":
			t.menuMove(-1)
		case "\x1b[B":
			t.menuMove(1)
		default:
			t.closeMenu()
		}
		return true
	}
	t.inbuf = t.inbuf[1:]
	t.pending = false
	switch b {
	case 'j':
		t.menuMove(1)
	case 'k':
		t.menuMove(-1)
	case '\r', '\n':
		t.menuPick(t.menu.sel)
	case 'q', 0x1b:
		t.closeMenu()
	default:
		if b >= '1' && b <= '9' {
			if i := int(b - '1'); i < len(t.menu.items) {
				t.menuPick(i)
				return true
			}
		}
		t.closeMenu()
	}
	return true
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
		if t.pal != nil {
			// The prefix key closes the float without choosing. A bare esc
			// cannot: television prints the highlighted entry when it quits.
			if bytes.IndexByte(t.inbuf, 0x02) >= 0 {
				t.inbuf = nil
				closePalette(t)
				return
			}
			_, _ = t.pal.ptmx.Write(t.inbuf)
			t.inbuf = nil
			return
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
			t.renameKey()
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
			pi := -1
			if !t.pasteOff {
				pi = bytes.Index(t.inbuf, pasteStart)
			}
			ci := bytes.IndexByte(t.inbuf, 0x02)
			if ci >= 0 && (pi < 0 || ci < pi) {
				if ci > 0 {
					_ = t.conn.send(Msg{Type: "keys", Keys: t.inbuf[:ci]})
				}
				t.inbuf = t.inbuf[ci+1:]
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
			_ = t.conn.send(Msg{Type: "keys", Keys: t.inbuf})
			t.inbuf = nil
			return
		}
		b := t.inbuf[0]
		if b == 0x1b {
			if len(t.inbuf) < 3 {
				if !t.pending {
					t.pending = true
					t.escAt = time.Now()
				}
				return
			}
			seq := string(t.inbuf[:3])
			t.inbuf = t.inbuf[3:]
			switch seq {
			case "\x1b[A":
				t.action("focus-dir", func(m *Msg) { m.Dir = "up" })
			case "\x1b[B":
				t.action("focus-dir", func(m *Msg) { m.Dir = "down" })
			case "\x1b[C":
				t.action("focus-dir", func(m *Msg) { m.Dir = "right" })
			case "\x1b[D":
				t.action("focus-dir", func(m *Msg) { m.Dir = "left" })
			default:
				t.disarm()
			}
			continue
		}
		t.inbuf = t.inbuf[1:]
		t.pending = false
		t.key(b)
	}
}

// pickerKey consumes one key while the session picker is open. It returns false
// when it needs more bytes for an escape sequence.
func (t *tui) pickerKey() bool {
	b := t.inbuf[0]
	if b == 0x1b {
		if len(t.inbuf) < 3 {
			if !t.pending {
				t.pending = true
				t.escAt = time.Now()
			}
			return false
		}
		seq := string(t.inbuf[:3])
		t.inbuf = t.inbuf[3:]
		switch seq {
		case "\x1b[A":
			t.moveSel(-1)
		case "\x1b[B":
			t.moveSel(1)
		case "\x1b[C", "\x1b[D":
			t.closePicker()
		default:
			t.closePicker()
		}
		return true
	}
	t.inbuf = t.inbuf[1:]
	t.pending = false
	switch b {
	case '\r', '\n':
		t.pickSession()
	case 'j':
		t.moveSel(1)
	case 'k':
		t.moveSel(-1)
	case 'q', 's':
		t.closePicker()
	default:
		if b >= '1' && b <= '9' {
			if i := int(b - '1'); i < len(t.sessions) {
				t.sel = i
				t.pickSession()
				return true
			}
		}
		t.closePicker()
	}
	return true
}

// scrollKey consumes one key in scrollback mode. It returns false when it
// needs more bytes for an escape sequence.
func (t *tui) scrollKey() bool {
	b := t.inbuf[0]
	if b == 0x1b {
		if len(t.inbuf) < 3 {
			if !t.pending {
				t.pending = true
				t.escAt = time.Now()
			}
			return false
		}
		seq := string(t.inbuf[:3])
		t.inbuf = t.inbuf[3:]
		switch seq {
		case "\x1b[A":
			t.scrollBy(1)
		case "\x1b[B":
			t.scrollBy(-1)
		case "\x1b[5", "\x1b[6":
			// PgUp/PgDn arrive as ESC [ 5 ~ / ESC [ 6 ~: keep the fourth byte.
			if len(t.inbuf) > 0 && t.inbuf[0] == '~' {
				t.inbuf = t.inbuf[1:]
			}
			if seq == "\x1b[5" {
				t.scrollBy(t.panHeight())
			} else {
				t.scrollBy(-t.panHeight())
			}
		default:
			t.exitScroll()
		}
		return true
	}
	t.inbuf = t.inbuf[1:]
	t.pending = false
	switch b {
	case 'k':
		t.scrollBy(1)
	case 'j':
		t.scrollBy(-1)
	case 'g':
		t.scrollBy(t.maxOff)
	case 'G', 'q', '[', 0x1b:
		t.exitScroll()
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
	vt := vt10x.New(vt10x.WithSize(p.Rect.W, p.Rect.H+histRows))
	_, _ = vt.Write(t.hist[p.ID])
	t.scrollVT = vt
	_, t.maxOff = scrollStart(vt, p.Rect.H, 0)
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

// paneAtBox returns the pane whose whole box contains the point, border
// included. A click on a pane's frame belongs to that pane.
func (t *tui) paneAtBox(x, y int) *PaneInfo {
	if t.snap == nil {
		return nil
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

func (t *tui) handleMouse(ev mouseEvent) {
	if t.pal != nil {
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
	t.updatePadHover(x, y)
	if t.mode == "menu" {
		if t.menu == nil {
			t.closeMenu()
			return
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
	if snap == nil || snap.Root == nil {
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
// draft starts as the current name: the snapshot names no workspace itself, so
// the name comes from the rail entries. A workspace the snapshot does not list
// opens an empty draft; the prompt still opens.
func (t *tui) openRenamePrompt() {
	if t.snap == nil {
		t.disarm()
		return
	}
	t.renameDraft = ""
	for _, p := range t.snap.All {
		if p.Workspace == t.snap.Workspace {
			t.renameDraft = p.WorkspaceName
			break
		}
	}
	t.mode = "rename"
	t.armed = false
	t.pending = false
	t.dirty = true
}

// closeRename drops the prompt and its draft, whatever it held.
func (t *tui) closeRename() {
	t.mode = ""
	t.renameDraft = ""
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
	target := t.snap.Workspace
	t.closeRename()
	if name == "" {
		return
	}
	if t.conn != nil {
		_ = t.conn.send(Msg{Type: "req", Action: "rename-workspace", Target: target, Name: name})
	}
}

// renameKey consumes one key while the rename prompt is open. No byte leaves
// the client: every key edits or ends the draft. Esc cancels at once, so the
// prompt never waits for the rest of an escape sequence.
func (t *tui) renameKey() {
	if len(t.inbuf) == 0 {
		return
	}
	b := t.inbuf[0]
	t.inbuf = t.inbuf[1:]
	t.pending = false
	switch {
	case b == 0x1b:
		// A lone esc cancels. An arrow key cancels too: its remaining bytes
		// are dropped rather than typed into the draft.
		t.closeRename()
	case b == '\r' || b == '\n':
		t.submitRename()
	case b == 0x7f || b == 0x08:
		if r := []rune(t.renameDraft); len(r) > 0 {
			t.renameDraft = string(r[:len(r)-1])
		}
		t.dirty = true
	case b == 0x15:
		t.renameDraft = ""
		t.dirty = true
	case b >= 0x20 && b <= 0x7e:
		t.renameDraft += string(rune(b))
		t.dirty = true
	}
}

func (t *tui) key(b byte) {
	switch b {
	case 0x02:
		_ = t.conn.send(Msg{Type: "keys", Keys: []byte{0x02}})
		t.disarm()
	case '|':
		t.action("split", func(m *Msg) { m.Dir = "right" })
	case '-', '_':
		t.action("split", func(m *Msg) { m.Dir = "down" })
	case '<':
		t.action("split", func(m *Msg) { m.Dir = "left" })
	case '[':
		t.startScrollOnFocus()
	case '^':
		t.action("split", func(m *Msg) { m.Dir = "up" })
	case 'h':
		t.action("focus-dir", func(m *Msg) { m.Dir = "left" })
	case 'j':
		t.action("focus-dir", func(m *Msg) { m.Dir = "down" })
	case 'k':
		t.action("focus-dir", func(m *Msg) { m.Dir = "up" })
	case 'l':
		t.action("focus-dir", func(m *Msg) { m.Dir = "right" })
	case 'n':
		t.action("next-pane", nil)
	case 'p':
		t.action("prev-pane", nil)
	case 'x':
		t.action("close-pane", nil)
	case 'c':
		t.action("new-window", nil)
	case 'w':
		t.action("next-window", nil)
	case 'W':
		t.action("prev-window", nil)
	case 't':
		t.action("new-workspace", nil)
	case ')':
		t.action("next-workspace", nil)
	case 'a':
		openPalette(t)
	case 's':
		t.openPicker()
	case 'r':
		t.openRenamePrompt()
	case 'b':
		t.toggleRail()
	case '?':
		t.disarm()
	case 'd', 'q':
		t.quit = true
	case 0x1b:
		t.disarm()
	default:
		if b >= '0' && b <= '9' {
			idx := int(b - '1')
			if b == '0' {
				idx = 9
			}
			t.action("select-window", func(m *Msg) { m.Index = idx })
			return
		}
		t.notify(fmt.Sprintf("no binding for %q", string(b)))
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
// stays on `prefix r`, so a thin rail expands here instead of going hidden.
func (t *tui) toggleRailControl() {
	want := t.railToggleWidth(false)
	t.action("set-rail", func(m *Msg) { m.Index = want })
}

// railToggleWidth is the width a rail toggle asks for. The key and the bottom
// control both take their width from here, so the two routes cannot drift. A
// hidden or thin rail comes back at the last expanded width, or the default
// when none was seen. hide lets `prefix r` reach the hidden state; the control
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
	for _, p := range win.Panes {
		drawPaneBox(&box, p.Rect, p.ID == win.Focus)
	}
	drawGrips(&grip, win)
	// The rail grip shares the padZone hint but is not a split divider, so it
	// paints no heavy line of its own.
	if t.padAxis != railPadAxis {
		drawDivider(&div, t.padZone, t.padAxis)
	}
	drawRail(&rail, win)
	drawBar(&bar, win, t.armed, t.barInfo())
	drawToasts(&toasts, win, t.toasts)
	if t.pal != nil {
		drawPaneFrom(&pal, t.pal.vt, t.pal.rect, 0, nil)
		drawPaneBox(&pal, t.pal.rect, true)
	}
	if t.armed {
		drawHelp(&help, win.Cols, win.Rows)
	}
	if t.mode == "sessions" {
		drawSessions(&sess, win.Cols, win.Rows, t.sessions, t.sel, t.session)
	}
	if t.mode == "menu" && t.menu != nil {
		drawMenu(&menu, t.menu)
	}
	if t.mode == "rename" {
		drawRename(&rename, win, win.Workspace, t.renameDraft)
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

	dirty := map[int]bool{}
	bands := []struct {
		p       *paint
		buf     *bytes.Buffer
		overlay bool
		force   bool
		write   bool
	}{
		{p: &t.last.box, buf: &box, force: padFreed},
		{p: &t.last.grip, buf: &grip, force: padFreed},
		{p: &t.last.div, buf: &div},
		{p: &t.last.rail, buf: &rail},
		{p: &t.last.bar, buf: &bar},
		{p: &t.last.toasts, buf: &toasts, overlay: true},
		{p: &t.last.pal, buf: &pal, overlay: true},
		{p: &t.last.help, buf: &help, overlay: true},
		{p: &t.last.sess, buf: &sess, overlay: true},
		{p: &t.last.menu, buf: &menu, overlay: true},
		{p: &t.last.rename, buf: &rename, overlay: true},
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
			drawPaneRow(&body, pp.vt, pp.rect, y-pp.rect.Y, pp.row0, pp.sel)
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
	fmt.Fprintf(&b, "%dx%d r%d", win.Cols, win.Rows, win.Rail)
	for _, p := range win.Panes {
		fmt.Fprintf(&b, " %s:%d,%d,%d,%d", p.ID, p.Rect.X, p.Rect.Y, p.Rect.W, p.Rect.H)
	}
	fmt.Fprintf(&b, " f=%s m=%s s=%s", win.Focus, t.mode, t.scroll)
	if t.pal != nil {
		fmt.Fprintf(&b, " p=%+v", t.pal.rect)
	}
	return b.String()
}

// panePaints is the content each pane shows this frame.
func (t *tui) panePaints(win *Snapshot) []panePaint {
	out := make([]panePaint, 0, len(win.Panes))
	for _, p := range win.Panes {
		pp := panePaint{id: p.ID, rect: p.Rect}
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
			if pp.sel.has(row*pp.rect.W + x) {
				h = mix(h, 1)
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
	if t.pal != nil {
		return "\x1b[?25l"
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
