package main

// The message box as a floating pane. The view is read-only: it lists the
// session's entries newest first, one row each, and follows the log while it is
// open through the server's box push. It also keeps the presence list, so a
// reader can see who is parked on which filter and how far each has read. Keys
// scroll it and toggle the watcher page; esc closes it.

import (
	"bytes"
	"dm/internal/cells"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hinshun/vt10x"
)

// The columns of one entry row, in order: receipt, seq, state, key, from, msg.
// The fixed widths let a column read down the list.
const (
	boxSeqW   = 4
	boxStateW = 7
	boxKeyW   = 14
	boxFromW  = 6
	// boxReceiptW is the receipt marker: a two-cell count and the space that
	// separates it from the seq column.
	boxReceiptW = 3
)

// The columns of one watcher row, in order: pane, filter, cursor. The wait
// mark follows the cursor.
const (
	boxPaneW   = 8
	boxFilterW = 16
)

const (
	// boxWatchInterval is the shortest gap between watcher requests. A burst
	// of watch pushes asks the server once.
	boxWatchInterval = 100 * time.Millisecond

	// boxReceiptMinW is the narrowest rect that carries the receipt marker.
	// Below it the marker is dropped whole, so the message keeps its cells.
	boxReceiptMinW = 60
)

// boxView is the open message-box float: the entries the server sent, the rows
// of the newest one shown at the top, the watchers the server last reported,
// and the float that draws them.
type boxView struct {
	entries []entry
	off     int
	// new counts the entries that arrived above a scrolled-back window. It is
	// zero while the window follows the newest entry.
	new int

	watchers []watch
	// page is true while the float shows the watcher list instead of the log,
	// and pOff is the first watch row in view.
	page bool
	pOff int
	// watchAt is when the view last asked for the watcher list, and watchWant
	// marks a push whose ask the interval held back.
	watchAt   time.Time
	watchWant bool

	float *FloatingPane
	// snap sizes the float: the view fits the log until it reaches its cap,
	// so a short log reads in a short box and a long one fills the screen.
	snap *Snapshot
}

// newBoxView opens the float on a screen. The log stays empty until the reply
// lands.
func newBoxView(snap *Snapshot, th Theme) *boxView {
	b := &boxView{float: &FloatingPane{ID: "box", Active: true}, snap: snap}
	b.sync(snap, th)
	return b
}

// boxViewRect is where the float sits: centred over the pane area, one cell
// clear of the rail, at most 96 columns and 22 rows. The height follows n, the
// larger of the entry count and the watch count, so a short list reads in a
// short box and a list longer than the cap scrolls. A parked watch exists
// before the first entry, so the watcher page sizes itself from n too.
func boxViewRect(snap *Snapshot, n int) Rect {
	left := snap.Rail
	if left < 0 {
		left = 0
	}
	w := snap.Cols - left - 6
	if w > 96 {
		w = 96
	}
	if w < 20 {
		w = snap.Cols - left - 2
	}
	if w < 8 {
		w = snap.Cols - 2
	}
	h := snap.Rows - 6
	if h > 22 {
		h = 22
	}
	if n < h {
		h = n
	}
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	x := left + (snap.Cols-left-w)/2
	if x < 1 {
		x = 1
	}
	y := (snap.Rows - h) / 2
	if y < 1 {
		y = 1
	}
	return clampRect(Rect{X: x, Y: y, W: w, H: h}, snap.Cols, snap.Rows)
}

// sync fits the float to the screen after a snapshot or a rail change.
func (b *boxView) sync(snap *Snapshot, th Theme) {
	b.snap = snap
	r := boxViewRect(snap, b.rows())
	if r == b.float.Rect {
		return
	}
	b.float.Rect = r
	b.clamp()
	b.pageClamp()
	b.paint(th)
}

// rows is the number of rows the float fits: the larger list wins, so the
// watcher page is usable before the first entry lands.
func (b *boxView) rows() int {
	if len(b.watchers) > len(b.entries) {
		return len(b.watchers)
	}
	return len(b.entries)
}

// set replaces the log after a list reply or a box push. A scrolled-back
// window keeps its top entry by seq, so new entries do not slide the text
// under the reader. A window at the top follows the newest entry.
func (b *boxView) set(entries []entry, th Theme) {
	top := 0
	if b.off > 0 && b.off < len(b.entries) {
		top = b.entries[b.off].Seq
	}
	b.entries = entries
	b.float.Rect = boxViewRect(b.snap, b.rows())
	if top != 0 {
		if i := boxSeqIndex(entries, top); i >= 0 {
			// The entries between the old top and the new one arrived above
			// the window; the rest of the log did not move.
			b.new += i - b.off
			if b.new < 0 {
				b.new = 0
			}
			b.off = i
		}
	}
	b.clamp()
	b.pageClamp()
	b.paint(th)
}

// setWatchers replaces the watcher page after a msg-watchers reply. The float
// holds the page whole while the page is longer than the log.
func (b *boxView) setWatchers(watchers []watch, th Theme) {
	b.watchers = watchers
	b.float.Rect = boxViewRect(b.snap, b.rows())
	b.clamp()
	b.pageClamp()
	b.paint(th)
}

// boxSeqIndex finds an entry by seq in the newest-first log.
func boxSeqIndex(entries []entry, seq int) int {
	for i, e := range entries {
		if e.Seq == seq {
			return i
		}
	}
	return -1
}

// scroll moves the window by n rows, positive toward older rows. The page and
// the log keep their own offsets, so a toggle leaves each where it was.
func (b *boxView) scroll(n int, th Theme) {
	if b.page {
		b.pOff += n
		b.pageClamp()
	} else {
		b.off += n
		b.clamp()
	}
	b.paint(th)
}

// pageClamp keeps the watcher page on its list.
func (b *boxView) pageClamp() {
	max := len(b.watchers) - b.float.Rect.H
	if max < 0 {
		max = 0
	}
	if b.pOff > max {
		b.pOff = max
	}
	if b.pOff < 0 {
		b.pOff = 0
	}
}

// listLen is the number of rows in the list the float shows.
func (b *boxView) listLen() int {
	if b.page {
		return len(b.watchers)
	}
	return len(b.entries)
}

// clamp keeps the window on the log: the offset never passes the oldest row
// that still fills the pane, a log that shrank goes back to the top, and the
// arrival count clears once the reader is at the top.
func (b *boxView) clamp() {
	max := len(b.entries) - b.float.Rect.H
	if max < 0 {
		max = 0
	}
	if b.off > max {
		b.off = max
	}
	if b.off < 0 {
		b.off = 0
	}
	if b.off == 0 {
		b.new = 0
	}
}

// togglePage swaps the float between the entry list and the watcher page.
func (b *boxView) togglePage(th Theme) {
	b.page = !b.page
	b.paint(th)
}

// paint rewrites the float's terminal from the entries or the watcher list.
// Every row is placed absolutely, so a view that scrolled paints its new
// window whole.
func (b *boxView) paint(th Theme) {
	r := b.float.Rect
	b.float.Title = b.title()
	b.float.Hint = b.hint()
	if r.W < 1 || r.H < 1 {
		b.float.VT = vt10x.New(vt10x.WithSize(1, 1))
		return
	}
	vt := vt10x.New(vt10x.WithSize(r.W, r.H))
	var buf bytes.Buffer
	if b.page {
		b.paintWatchers(&buf, r, th)
	} else {
		b.paintEntries(&buf, r, th)
	}
	_, _ = vt.Write(buf.Bytes())
	b.float.VT = vt
}

// paintEntries draws the window of the log. A wide enough rect carries the
// receipt count in every row.
func (b *boxView) paintEntries(buf *bytes.Buffer, r Rect, th Theme) {
	if len(b.entries) == 0 {
		boxPaintNote(buf, 1, "no entries yet", r.W)
		return
	}
	for row := 0; row < r.H; row++ {
		i := b.off + row
		if i >= len(b.entries) {
			break
		}
		fmt.Fprintf(buf, "\x1b[%d;1H", row+1)
		receipt := -1
		if r.W >= boxReceiptMinW {
			receipt = b.received(b.entries[i])
		}
		boxPaintRow(buf, th, b.entries[i], r.W, receipt)
	}
}

// paintWatchers draws the watcher page in the server's order, clipped to the
// rect and to its rows.
func (b *boxView) paintWatchers(buf *bytes.Buffer, r Rect, th Theme) {
	if len(b.watchers) == 0 {
		boxPaintNote(buf, 1, "no watchers yet", r.W)
		return
	}
	for row := 0; row < r.H; row++ {
		i := b.pOff + row
		if i >= len(b.watchers) {
			break
		}
		fmt.Fprintf(buf, "\x1b[%d;1H", row+1)
		boxPaintWatch(buf, b.watchers[i], r.W)
	}
}

// boxPaintNote writes a dim placeholder line at an absolute row.
func boxPaintNote(buf *bytes.Buffer, row int, text string, w int) {
	fmt.Fprintf(buf, "\x1b[%d;1H\x1b[0m\x1b[90m %s", row, cells.Clip(text, w-1))
}

// title is the count in the top border, the visible rows when the log is
// longer than the box, the parked watches, and the entries that arrived above
// a scrolled-back window. The last two are omitted when they are zero.
func (b *boxView) title() string {
	n := len(b.entries)
	var head string
	switch n {
	case 0:
		head = "box  empty"
	case 1:
		head = "box  1 entry"
	default:
		rows := b.float.Rect.H
		if n <= rows {
			head = fmt.Sprintf("box  %d entries", n)
		} else {
			last := b.off + rows
			if last > n {
				last = n
			}
			head = fmt.Sprintf("box  %d entries  %d-%d", n, b.off+1, last)
		}
	}
	parts := []string{head}
	if parked := b.parked(); parked > 0 {
		parts = append(parts, fmt.Sprintf("%d watching", parked))
	}
	if b.new > 0 {
		parts = append(parts, fmt.Sprintf("+%d", b.new))
	}
	return " " + strings.Join(parts, "  ") + " "
}

// hint names the keys the view takes, in its bottom border.
func (b *boxView) hint() string {
	if b.page {
		return " j/k scroll  w log  esc close "
	}
	return " j/k scroll  w watchers  esc close "
}

// parked counts the watches whose wait is outstanding.
func (b *boxView) parked() int {
	n := 0
	for _, w := range b.watchers {
		if w.Parked {
			n++
		}
	}
	return n
}

// received counts the watches a filter and a cursor say were served this
// entry: the filter matches its key and the cursor is at or past its seq.
func (b *boxView) received(e entry) int {
	n := 0
	for _, w := range b.watchers {
		if watchMatches(w, e.Key) && w.Cursor >= e.Seq {
			n++
		}
	}
	return n
}

// watchMatches reports whether a watch's filter covers a key. Filter is an
// exact key or a branch. A watch with neither read the whole log, so it covers
// every key.
func watchMatches(w watch, key string) bool {
	switch {
	case w.Key != "":
		return w.Key == key
	case w.KeyPrefix != "":
		return strings.HasPrefix(key, w.KeyPrefix)
	}
	return true
}

// watchFilter is a watch's filter as the page prints it: the key, or the
// branch with a trailing star.
func watchFilter(w watch) string {
	if w.Key != "" {
		return w.Key
	}
	if w.KeyPrefix != "" {
		return w.KeyPrefix + "*"
	}
	return ""
}

// boxPaintWatch writes one watcher row into buf, dim text. The row is clipped
// to width cells.
func boxPaintWatch(buf *bytes.Buffer, w watch, width int) {
	pane := cells.Pad(cells.Clip(w.Pane, boxPaneW), boxPaneW)
	filter := cells.Pad(cells.Clip(watchFilter(w), boxFilterW), boxFilterW)
	row := fmt.Sprintf("%s %s @%d", pane, filter, w.Cursor)
	if w.Parked {
		row += " wait"
	}
	buf.WriteString("\x1b[0m\x1b[90m")
	buf.WriteString(cells.Clip(row, width))
}

// boxPaintRow writes one entry into buf, dim text with the state word in the
// colour the rail gives that state. A receipt of zero or more prefixes a
// two-cell count; a negative receipt drops the marker. The row is clipped to
// w cells.
func boxPaintRow(buf *bytes.Buffer, th Theme, e entry, w int, receipt int) {
	row := boxRowText(e, w, receipt)
	start := boxSeqW + 1
	if receipt >= 0 {
		start += boxReceiptW
	}
	state := string(e.State)
	if start+len(state) > cells.Cells(row) {
		buf.WriteString(row)
		return
	}
	buf.WriteString("\x1b[0m\x1b[90m")
	buf.WriteString(row[:start])
	buf.WriteString("\x1b[0m" + boxStateColour(th, e.State).fg())
	buf.WriteString(state)
	buf.WriteString("\x1b[0m\x1b[90m")
	buf.WriteString(row[start+len(state):])
}

// boxRowText is the plain text of one entry row, columns padded and the whole
// clipped to w cells.
func boxRowText(e entry, w int, receipt int) string {
	key := cells.Pad(cells.Clip(e.Key, boxKeyW), boxKeyW)
	from := cells.Pad(cells.Clip(e.From, boxFromW), boxFromW)
	mark := ""
	if receipt >= 0 {
		if receipt > 99 {
			receipt = 99
		}
		mark = fmt.Sprintf("%2d ", receipt)
	}
	return cells.Clip(mark+fmt.Sprintf("%*d %s %s %s %s", boxSeqW, e.Seq,
		cells.Pad(string(e.State), boxStateW), key, from, e.Msg), w)
}

// boxStateColour is the state's hue. It is the rail's status colour, so a lane
// reads the same in the box as on the rail. note carries no progress meaning,
// so it is dim text.
func boxStateColour(th Theme, st boxState) rgb {
	if st == stateNote {
		return th.Palette.RailDim
	}
	_, colour := railGlyph(th, string(st), false)
	return colour
}

// openBoxView opens the message box as a floating pane, or closes it when it is
// already up. The view asks for the whole log and the watcher list; the replies
// fill it.
func openBoxView(t *tui) {
	if t.snap == nil {
		t.disarm()
		return
	}
	if t.box != nil {
		closeBoxView(t)
		return
	}
	t.box = newBoxView(t.snap, t.theme())
	t.AddFloatingPane(t.box.float)
	t.mode = "box"
	t.armed = false
	t.boxRequest()
	t.boxWatchers(time.Now())
	t.dirty = true
}

// closeBoxView takes the float down and gives the keyboard back to the panes.
func closeBoxView(t *tui) {
	if t.box == nil {
		return
	}
	t.RemoveFloatingPane(t.box.float.ID)
	t.box = nil
	t.mode = ""
	// The close key stays in the buffer while the pending timer waits. Drop
	// the whole buffer here, so a later keypress cannot forward a stray escape
	// to a pane.
	t.inbuf = nil
	t.pending = false
	t.dirty = true
}

// boxRequest asks the server for the whole log. The reply carries the entries
// newest first.
func (t *tui) boxRequest() {
	if t.conn == nil {
		return
	}
	_ = t.conn.send(Msg{
		Type: "req", Action: "msg-list",
		Res: json.RawMessage(`{"key":"","limit":0}`),
	})
}

// boxWatchers asks the server for the watcher list, unless it asked within the
// last boxWatchInterval. A held-back ask stays marked and the ticker flushes it.
// The view is not a watcher: the request carries no from.
func (t *tui) boxWatchers(now time.Time) {
	b := t.box
	if b == nil || t.conn == nil {
		return
	}
	if !b.watchAt.IsZero() && now.Sub(b.watchAt) < boxWatchInterval {
		b.watchWant = true
		return
	}
	b.watchAt = now
	b.watchWant = false
	_ = t.conn.send(Msg{
		Type: "req", Action: "msg-watchers",
		Res: json.RawMessage(`{}`),
	})
}

// flushBoxWatchers sends a watcher ask that the interval held back.
func (t *tui) flushBoxWatchers(now time.Time) {
	if t.box == nil || !t.box.watchWant {
		return
	}
	t.boxWatchers(now)
}

// takeBoxList fills the view from a msg-list reply. A closed view, a malformed
// reply and an error reply leave the picture alone; an error becomes a toast.
func (t *tui) takeBoxList(m Msg) {
	if t.box == nil {
		return
	}
	if !m.OK {
		t.notify(m.Error)
		return
	}
	var rep listReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.notify("box: " + err.Error())
		return
	}
	t.box.set(rep.Entries, t.theme())
	t.dirty = true
}

// takeBoxWatchers fills the watcher page from a msg-watchers reply. Replies
// arrive in request order on the one connection, so the newest reply is the
// newest state. A closed view, a malformed reply and an error reply leave the
// page alone; an error becomes a toast.
func (t *tui) takeBoxWatchers(m Msg) {
	if t.box == nil {
		return
	}
	if !m.OK {
		t.notify(m.Error)
		return
	}
	var rep watchersReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.notify("box: " + err.Error())
		return
	}
	t.box.setWatchers(rep.Watchers, t.theme())
	t.dirty = true
}

// boxWheel scrolls the view from the wheel. A notch up goes toward older
// entries, the way it goes back in scrollback.
func (t *tui) boxWheel(ev mouseEvent) {
	if t.box == nil {
		return
	}
	t.updateLinkHover(ev.x-1, ev.y-1, ev.btn&16 != 0)
	// Ctrl+click on a URL in the float opens it. The click is consumed; a
	// ctrl+click elsewhere does nothing.
	if ev.btn&16 != 0 && ev.btn&3 == 0 && ev.btn&32 == 0 && ev.btn&64 == 0 && !ev.release {
		r := t.box.float.Rect
		x, y := ev.x-1, ev.y-1
		if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
			if u := urlAtRow(t.box.float.VT, r.W, y-r.Y, x-r.X); u != "" {
				if err := openURL(u); err != nil {
					t.notify("open: " + err.Error())
				} else {
					t.notify("opened " + u)
				}
				return
			}
		}
	}
	if ev.btn&64 == 0 || ev.release {
		return
	}
	step := -3
	if ev.btn&1 == 0 {
		step = 3
	}
	t.box.scroll(step, t.theme())
	t.dirty = true
}

// boxKey consumes one key while the message box float is up. Every key belongs
// to the view: nothing reaches a pane. It returns false when it needs more bytes
// for an escape sequence.
func (t *tui) boxKey() bool {
	if len(t.inbuf) == 0 {
		return true
	}
	if p := t.km().PrefixBytes(); len(p) > 0 && bytes.HasPrefix(t.inbuf, p) {
		// The prefix key closes the view.
		t.inbuf = append([]byte(nil), t.inbuf[len(p):]...)
		t.pending = false
		closeBoxView(t)
		return true
	}
	action, n, state := t.km().Match("box", t.inbuf)
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
		// An unbound key closes the view.
		t.inbuf = t.inbuf[1:]
		t.pending = false
		closeBoxView(t)
		return true
	}
	switch action {
	case "up", "up-arrow":
		t.box.scroll(-1, t.theme())
	case "down", "down-arrow":
		t.box.scroll(1, t.theme())
	case "page-up":
		t.box.scroll(-t.box.float.Rect.H, t.theme())
	case "page-down":
		t.box.scroll(t.box.float.Rect.H, t.theme())
	case "top":
		t.box.scroll(-t.box.listLen(), t.theme())
	case "bottom":
		t.box.scroll(t.box.listLen(), t.theme())
	case "toggle-page":
		t.box.togglePage(t.theme())
	default:
		closeBoxView(t)
		return true
	}
	t.dirty = true
	return true
}
