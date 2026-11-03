package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hinshun/vt10x"
	"golang.org/x/term"
)

const (
	histMax  = 512 << 10
	histRows = 2000
)

type tui struct {
	session string
	conn    *conn
	cols    int
	rows    int
	snap    *Snapshot
	vts     map[string]vt10x.Terminal
	armed   bool
	toast   string
	toastAt time.Time
	dirty   bool

	mode     string // "" or "sessions" or "scroll" or "menu"
	sessions []SessionInfo
	sel      int
	menu     *menu

	hist     map[string][]byte
	scroll   string // pane id shown from scrollback
	offset   int
	maxOff   int
	scrollVT vt10x.Terminal

	quit     bool
	switchTo string

	armAt   time.Time
	escAt   time.Time
	inbuf   []byte
	pending bool

	dragging   bool
	dragFirst  string
	dragSecond string
	dragAxis   string
	dragStart  int
	dragSpace  int
	dragAt     time.Time
}

// runClient owns the terminal. A session switch returns to this loop with a new
// name, so raw mode and the stdin reader survive it.
func runClient(name string) error {
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
	out.WriteString("\x1b[?1049h\x1b[?25l\x1b[2J\x1b[H\x1b[?1000h\x1b[?1002h\x1b[?1006h")
	os.Stdout.Write(out.Bytes())
	defer os.Stdout.WriteString("\x1b[?1006l\x1b[?1002l\x1b[?1000l\x1b[?25h\x1b[?1049l\x1b[0m")

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

	t := &tui{session: name, conn: c, vts: map[string]vt10x.Terminal{},
		hist: map[string][]byte{}}
	t.cols, t.rows, _ = term.GetSize(fd)

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
			if t.toast != "" && now.Sub(t.toastAt) > 3*time.Second {
				t.toast = ""
				t.dirty = true
			}
			if t.armed && now.Sub(t.armAt) > 3*time.Second {
				t.armed = false
				t.inbuf = nil
				t.dirty = true
			}
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
	switch m.Type {
	case "snapshot":
		t.snap = m.Snap
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
			h := append(t.hist[m.Target], m.Data...)
			if len(h) > histMax {
				h = h[len(h)-histMax:]
			}
			t.hist[m.Target] = h
			t.dirty = true
		}
	case "close":
		t.quit = true
	case "exit":
		t.toast = "pane " + m.Target + " exited"
		t.toastAt = time.Now()
		t.dirty = true
	case "resp":
		if !m.OK {
			t.toast = m.Error
			t.toastAt = time.Now()
			t.dirty = true
		}
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

func (t *tui) processInput() {
	for len(t.inbuf) > 0 {
		if ev, n, ok := parseMouse(t.inbuf); ok {
			t.inbuf = t.inbuf[n:]
			t.handleMouse(ev)
			continue
		} else if n == 0 {
			return // partial sequence, wait for the rest
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
			if i := bytes.IndexByte(t.inbuf, 0x02); i >= 0 {
				if i > 0 {
					_ = t.conn.send(Msg{Type: "keys", Keys: t.inbuf[:i]})
				}
				t.inbuf = t.inbuf[i+1:]
				t.arm()
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

// startScroll builds a tall terminal from the pane's byte history. The live
// screen keeps updating; the tall one is a frozen snapshot until the user
// returns to the bottom.
func (t *tui) startScroll(p *PaneInfo) {
	if p.Alt {
		t.toast = "app is on the alternate screen; use its own scroll"
		t.toastAt = time.Now()
		t.dirty = true
		return
	}
	vt := vt10x.New(vt10x.WithSize(p.Rect.W, p.Rect.H+histRows))
	_, _ = vt.Write(t.hist[p.ID])
	t.scrollVT = vt
	t.scroll = p.ID
	t.offset = 0
	_, t.maxOff = scrollStart(vt, p.Rect.H, 0)
	t.mode = "scroll"
	t.armed = false
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
		t.toast = "app is on the alternate screen; use its own scroll"
		t.toastAt = time.Now()
		t.dirty = true
		return
	}
	t.startScroll(p)
	if t.maxOff == 0 {
		t.exitScroll()
		t.toast = "no scrollback yet"
		t.toastAt = time.Now()
		return
	}
	t.offset = 1
	t.dirty = true
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

func (t *tui) handleMouse(ev mouseEvent) {
	if t.snap == nil {
		return
	}
	x, y := ev.x-1, ev.y-1
	if t.mode == "menu" {
		left := ev.btn&3 == 0 && ev.btn&32 == 0 && !ev.release
		if left && t.menu != nil {
			if i := t.menu.itemAt(x, y); i >= 0 {
				t.menuPick(i)
			} else {
				t.closeMenu()
			}
		} else if ev.btn&3 == 2 && ev.btn&32 == 0 && !ev.release {
			t.closeMenu()
		}
		return
	}
	// Right press is the mux's context menu, even over an app that owns the
	// mouse. It is a window-manager gesture, not the app's.
	if ev.btn&3 == 2 && ev.btn&32 == 0 && !ev.release {
		p := t.paneAt(x, y)
		if p == nil {
			p = t.paneByID(t.snap.Focus)
		}
		if p != nil {
			t.openMenu(p, x, y)
		}
		return
	}
	left := ev.btn&3 == 0 && ev.btn&32 == 0 && !ev.release
	if t.dragging {
		if ev.release {
			t.dragTo(x, y, true)
			t.dragging = false
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
	// A divider belongs to the mux, unless its app claims the mouse.
	if left {
		if ds, ok := dragPress(t.snap, x, y); ok {
			t.startDrag(ds)
			return
		}
	}
	p := t.paneAt(x, y)
	if p == nil {
		return
	}
	// A left press focuses the pane even when the app owns the mouse. The
	// mux's focus is not the app's business; tmux does the same.
	if focus := focusForClick(p, t.snap.Focus, ev); focus != "" {
		_ = t.conn.send(Msg{Type: "req", Action: "focus", Target: focus})
	}
	if p.Mouse {
		// The pane daemon owns the mode; the client's VT may have missed the
		// enable sequence if it scrolled out of the replay ring.
		_ = t.conn.send(Msg{Type: "keys", Keys: ev.raw})
		return
	}
	if left {
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

// dragStart is the split a mouse press resolved to.
type dragStart struct {
	first  string
	second string
	axis   string
	rect   Rect
}

// dragPress decides whether a left press at x,y starts a divider drag. The
// split's own rect is used for the ratio, so nested splits keep their full
// span. A pane that claimed the mouse anywhere in the split's subtree owns the
// divider too, so no drag starts.
func dragPress(snap *Snapshot, x, y int) (dragStart, bool) {
	if snap == nil || snap.Root == nil {
		return dragStart{}, false
	}
	n, first, second, axis, r, ok := padHit(snap.Root,
		paneArea(snap.Cols, snap.Rows), x, y)
	if !ok {
		return dragStart{}, false
	}
	if splitClaimsMouse(snap, n) {
		return dragStart{}, false
	}
	return dragStart{first: first, second: second, axis: axis, rect: r}, true
}

// splitClaimsMouse reports whether any pane in the split's subtree wants mouse
// events.
func splitClaimsMouse(snap *Snapshot, n *Node) bool {
	if snap == nil || n == nil {
		return false
	}
	for _, id := range allPanes(n) {
		for i := range snap.Panes {
			if snap.Panes[i].ID == id && snap.Panes[i].Mouse {
				return true
			}
		}
	}
	return false
}

func (t *tui) startDrag(d dragStart) {
	t.dragging = true
	t.dragFirst = d.first
	t.dragSecond = d.second
	t.dragAxis = d.axis
	t.dragStart = d.rect.X
	space := d.rect.W
	if d.axis == "col" {
		t.dragStart = d.rect.Y
		space = d.rect.H
	}
	t.dragSpace = space - 1
	t.dragAt = time.Time{}
	t.dirty = true
}

// dragTo sends the split ratio the pointer asks for. Unless forced, it sends
// at most once every 30 ms; release forces the final position through.
func (t *tui) dragTo(x, y int, force bool) {
	if t.dragSpace < 6 {
		return
	}
	pos := x
	if t.dragAxis == "col" {
		pos = y
	}
	off := pos - t.dragStart
	if off < 3 {
		off = 3
	}
	if off > t.dragSpace-3 {
		off = t.dragSpace - 3
	}
	if !force && !t.dragAt.IsZero() && time.Since(t.dragAt) < 30*time.Millisecond {
		return
	}
	t.dragAt = time.Now()
	ratio := float64(off) / float64(t.dragSpace)
	_ = t.conn.send(Msg{Type: "req", Action: "resize-split",
		Target: t.dragFirst, Other: t.dragSecond, Ratio: ratio})
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
		t.toast = err.Error()
		t.toastAt = time.Now()
		t.disarm()
		return
	}
	if len(sessions) == 0 {
		t.toast = "no sessions"
		t.toastAt = time.Now()
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
	case 's':
		t.openPicker()
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
		t.toast = fmt.Sprintf("no binding for %q", string(b))
		t.toastAt = time.Now()
		t.disarm()
	}
}

func (t *tui) arm() {
	t.armed = true
	t.armAt = time.Now()
	t.toast = ""
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

func (t *tui) draw() {
	if t.snap == nil {
		return
	}
	var b bytes.Buffer
	b.WriteString("\x1b[0m\x1b[?7l\x1b[2J\x1b[H")
	win := t.snap
	for _, p := range win.Panes {
		if p.ID == t.scroll && t.scrollVT != nil {
			start, _ := scrollStart(t.scrollVT, p.Rect.H, t.offset)
			drawPaneFrom(&b, t.scrollVT, p.Rect, start)
		} else if vt := t.vts[p.ID]; vt != nil {
			drawPane(&b, vt, p.Rect)
		}
		drawPaneBox(&b, p.Rect, p.ID == win.Focus)
	}
	scrollInfo := ""
	if t.scroll != "" {
		scrollInfo = fmt.Sprintf("SCROLL %s +%d/%d", t.scroll, t.offset, t.maxOff)
	}
	drawBar(&b, win, t.armed, t.toast, scrollInfo)
	if t.armed {
		drawHelp(&b, win.Cols, win.Rows)
	}
	if t.mode == "sessions" {
		drawSessions(&b, win.Cols, win.Rows, t.sessions, t.sel, t.session)
	}
	if t.mode == "menu" && t.menu != nil {
		drawMenu(&b, t.menu)
	}
	if p := t.paneByID(win.Focus); p != nil && t.mode == "" {
		if vt := t.vts[p.ID]; vt != nil {
			vt.Lock()
			cur := vt.Cursor()
			visible := vt.CursorVisible()
			vt.Unlock()
			if visible && cur.X < p.Rect.W && cur.Y < p.Rect.H {
				fmt.Fprintf(&b, "\x1b[%d;%dH\x1b[?25h", p.Rect.Y+cur.Y+1, p.Rect.X+cur.X+1)
			} else {
				b.WriteString("\x1b[?25l")
			}
		}
	}
	b.WriteString("\x1b[?7h")
	os.Stdout.Write(b.Bytes())
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
