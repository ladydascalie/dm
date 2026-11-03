package main

// The message box as a floating pane. The view is read-only: it lists the
// session's entries newest first, one row each, and follows the log while it is
// open through the server's box push. Keys scroll it; esc closes it.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hinshun/vt10x"
)

// The columns of one row, in order: seq, state, key, from, msg. The first four
// keep a fixed width, so a column reads down the list.
const (
	boxSeqW   = 4
	boxStateW = 7
	boxKeyW   = 14
	boxFromW  = 6
)

// boxView is the open message-box float: the entries the server sent, the row
// of the newest one shown at the top, and the float that draws them.
type boxView struct {
	entries []entry
	off     int
	float   *FloatingPane
	// snap sizes the float: the view fits the log until it reaches its cap,
	// so a short log reads in a short box and a long one fills the screen.
	snap *Snapshot
}

// boxHint names the keys the view takes, in its bottom border.
const boxHint = " j/k scroll  esc close "

// newBoxView opens the float on a screen. The log stays empty until the reply
// lands.
func newBoxView(snap *Snapshot) *boxView {
	b := &boxView{float: &FloatingPane{ID: "box", Active: true}, snap: snap}
	b.sync(snap)
	return b
}

// boxViewRect is where the float sits: centred over the pane area, one cell
// clear of the rail, at most 96 columns and 22 rows. The height follows n, the
// number of entries, so a short log reads in a short box and a log longer than
// the cap scrolls.
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
func (b *boxView) sync(snap *Snapshot) {
	b.snap = snap
	r := boxViewRect(snap, len(b.entries))
	if r == b.float.Rect {
		return
	}
	b.float.Rect = r
	b.clamp()
	b.paint()
}

// set replaces the log after a list reply or a box push.
func (b *boxView) set(entries []entry) {
	b.entries = entries
	b.float.Rect = boxViewRect(b.snap, len(entries))
	b.clamp()
	b.paint()
}

// scroll moves the window by n rows, positive toward older entries.
func (b *boxView) scroll(n int) {
	b.off += n
	b.clamp()
	b.paint()
}

// clamp keeps the window on the log: the offset never passes the oldest row
// that still fills the pane, and a log that shrank goes back to the top.
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
}

// paint rewrites the float's terminal from the entries. Every row is placed
// absolutely, so a view that scrolled paints its new window whole.
func (b *boxView) paint() {
	r := b.float.Rect
	b.float.Title = b.title()
	b.float.Hint = boxHint
	if r.W < 1 || r.H < 1 {
		b.float.VT = vt10x.New(vt10x.WithSize(1, 1))
		return
	}
	vt := vt10x.New(vt10x.WithSize(r.W, r.H))
	var buf bytes.Buffer
	if len(b.entries) == 0 {
		buf.WriteString("\x1b[0m\x1b[90m no entries yet")
	}
	for row := 0; row < r.H; row++ {
		i := b.off + row
		if i >= len(b.entries) {
			break
		}
		fmt.Fprintf(&buf, "\x1b[%d;1H", row+1)
		boxPaintRow(&buf, b.entries[i], r.W)
	}
	_, _ = vt.Write(buf.Bytes())
	b.float.VT = vt
}

// title is the count in the top border, and the visible rows when the log is
// longer than the box.
func (b *boxView) title() string {
	n := len(b.entries)
	switch n {
	case 0:
		return " box  empty "
	case 1:
		return " box  1 entry "
	}
	rows := b.float.Rect.H
	if n <= rows {
		return fmt.Sprintf(" box  %d entries ", n)
	}
	last := b.off + rows
	if last > n {
		last = n
	}
	return fmt.Sprintf(" box  %d entries  %d-%d ", n, b.off+1, last)
}

// boxPaintRow writes one entry into buf, dim text with the state word in the
// colour the rail gives that state. The row is clipped to w cells.
func boxPaintRow(buf *bytes.Buffer, e entry, w int) {
	row := boxRowText(e, w)
	start := boxSeqW + 1
	state := string(e.State)
	if start+len(state) > runeCells(row) {
		buf.WriteString(row)
		return
	}
	buf.WriteString("\x1b[0m\x1b[90m")
	buf.WriteString(row[:start])
	buf.WriteString("\x1b[0m" + boxStateColour(e.State).fg())
	buf.WriteString(state)
	buf.WriteString("\x1b[0m\x1b[90m")
	buf.WriteString(row[start+len(state):])
}

// boxRowText is the plain text of one entry row, columns padded and the whole
// clipped to w cells.
func boxRowText(e entry, w int) string {
	key := boxPad(clipCells(e.Key, boxKeyW), boxKeyW)
	from := boxPad(clipCells(e.From, boxFromW), boxFromW)
	return clipCells(fmt.Sprintf("%*d %s %s %s %s", boxSeqW, e.Seq,
		boxPad(string(e.State), boxStateW), key, from, e.Msg), w)
}

// boxStateColour is the state's hue. It is the rail's status colour, so a lane
// reads the same in the box as on the rail. note carries no progress meaning,
// so it is dim text.
func boxStateColour(st boxState) rgb {
	if st == stateNote {
		return railDim
	}
	_, colour := railGlyph(string(st), false)
	return colour
}

// openBoxView opens the message box as a floating pane, or closes it when it is
// already up. The view asks for the whole log; the reply fills it.
func openBoxView(t *tui) {
	if t.snap == nil {
		t.disarm()
		return
	}
	if t.box != nil {
		closeBoxView(t)
		return
	}
	t.box = newBoxView(t.snap)
	t.AddFloatingPane(t.box.float)
	t.mode = "box"
	t.armed = false
	t.boxRequest()
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
	_ = t.conn.send(Msg{Type: "req", Action: "msg-list",
		Res: json.RawMessage(`{"key":"","limit":0}`)})
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
	t.box.set(rep.Entries)
	t.dirty = true
}

// boxWheel scrolls the view from the wheel. A notch up goes toward older
// entries, the way it goes back in scrollback.
func (t *tui) boxWheel(ev mouseEvent) {
	if t.box == nil || ev.btn&64 == 0 || ev.release {
		return
	}
	step := -3
	if ev.btn&1 == 0 {
		step = 3
	}
	t.box.scroll(step)
	t.dirty = true
}

// boxKey consumes one key while the message box float is up. Every key belongs
// to the view: nothing reaches a pane. It returns false when it needs more bytes
// for an escape sequence.
func (t *tui) boxKey() bool {
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
			t.box.scroll(-1)
		case "\x1b[B":
			t.box.scroll(1)
		case "\x1b[5", "\x1b[6":
			// PgUp/PgDn arrive as ESC [ 5 ~ / ESC [ 6 ~: keep the fourth byte.
			if len(t.inbuf) > 0 && t.inbuf[0] == '~' {
				t.inbuf = t.inbuf[1:]
			}
			if seq == "\x1b[5" {
				t.box.scroll(-t.box.float.Rect.H)
			} else {
				t.box.scroll(t.box.float.Rect.H)
			}
		default:
			closeBoxView(t)
			return true
		}
		t.dirty = true
		return true
	}
	t.inbuf = t.inbuf[1:]
	t.pending = false
	switch b {
	case 'k':
		t.box.scroll(-1)
	case 'j':
		t.box.scroll(1)
	case 'g':
		t.box.scroll(-len(t.box.entries))
	case 'G':
		t.box.scroll(len(t.box.entries))
	case 'q', 'm', 0x02, 0x03:
		closeBoxView(t)
		return true
	}
	t.dirty = true
	return true
}
