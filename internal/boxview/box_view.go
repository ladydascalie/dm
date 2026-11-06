// Package boxview paints the message box float: its rows, its watcher page,
// its scroll window and its borders.
package boxview

// The message box as a floating pane. The view is read-only: it lists the
// session's entries newest first, one row each, and follows the log while it is
// open through the server's box push. It also keeps the presence list, so a
// reader can see who is parked on which filter and how far each has read. Keys
// scroll it and toggle the watcher page; esc closes it.

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"dm/internal/box"
	"dm/internal/cells"
	"dm/internal/layout"
	"dm/internal/panel"
	"dm/internal/theme"
	"dm/internal/wire"

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
	// WatchInterval is the shortest gap between watcher requests. A burst of
	// watch pushes asks the server once.
	WatchInterval = 100 * time.Millisecond

	// boxReceiptMinW is the narrowest rect that carries the receipt marker.
	// Below it the marker is dropped whole, so the message keeps its cells.
	boxReceiptMinW = 60
)

// BoxView is the open message-box float: the entries the server sent, the rows
// of the newest one shown at the top, the watchers the server last reported,
// and the float that draws them.
type BoxView struct {
	entries []box.Entry
	off     int
	// new counts the entries that arrived above a scrolled-back window. It is
	// zero while the window follows the newest entry.
	new int

	watchers []box.Watch
	// page is true while the float shows the watcher list instead of the log,
	// and pOff is the first watch row in view.
	page bool
	pOff int
	// WatchAt is when the view last asked for the watcher list, and WatchWant
	// marks a push whose ask the interval held back.
	WatchAt   time.Time
	WatchWant bool

	// Float is the pane that draws the view.
	Float *panel.FloatingPane
	// snap sizes the float: the view fits the log until it reaches its cap,
	// so a short log reads in a short box and a long one fills the screen.
	snap *wire.Snapshot
}

// NewBoxView opens the float on a screen. The log stays empty until the reply
// lands.
func NewBoxView(snap *wire.Snapshot, th theme.Theme) *BoxView {
	b := &BoxView{Float: &panel.FloatingPane{ID: "box", Active: true}, snap: snap}
	b.Sync(snap, th)
	return b
}

// BoxViewRect is where the float sits: centred over the pane area, one cell
// clear of the rail, at most 96 columns and 22 rows. The height follows n, the
// larger of the entry count and the watch count, so a short list reads in a
// short box and a list longer than the cap scrolls. A parked watch exists
// before the first entry, so the watcher page sizes itself from n too.
func BoxViewRect(snap *wire.Snapshot, n int) layout.Rect {
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
	return clampRect(layout.Rect{X: x, Y: y, W: w, H: h}, snap.Cols, snap.Rows)
}

// clampRect keeps r inside a cols by rows screen.
func clampRect(r layout.Rect, cols, rows int) layout.Rect {
	if r.W > cols {
		r.W = cols
	}
	if r.H > rows {
		r.H = rows
	}
	if r.W < 0 {
		r.W = 0
	}
	if r.H < 0 {
		r.H = 0
	}
	if r.X < 0 {
		r.X = 0
	}
	if r.Y < 0 {
		r.Y = 0
	}
	if r.X+r.W > cols {
		r.X = cols - r.W
	}
	if r.Y+r.H > rows {
		r.Y = rows - r.H
	}
	if r.X < 0 {
		r.X = 0
	}
	if r.Y < 0 {
		r.Y = 0
	}
	return r
}

// Sync fits the float to the screen after a snapshot or a rail change.
func (b *BoxView) Sync(snap *wire.Snapshot, th theme.Theme) {
	b.snap = snap
	r := BoxViewRect(snap, b.rows())
	if r == b.Float.Rect {
		return
	}
	b.Float.Rect = r
	b.clamp()
	b.pageClamp()
	b.paint(th)
}

// rows is the number of rows the float fits: the larger list wins, so the
// watcher page is usable before the first entry lands.
func (b *BoxView) rows() int {
	if len(b.watchers) > len(b.entries) {
		return len(b.watchers)
	}
	return len(b.entries)
}

// Set replaces the log after a list reply or a box push. A scrolled-back
// window keeps its top entry by seq, so new entries do not slide the text
// under the reader. A window at the top follows the newest entry.
func (b *BoxView) Set(entries []box.Entry, th theme.Theme) {
	top := 0
	if b.off > 0 && b.off < len(b.entries) {
		top = b.entries[b.off].Seq
	}
	b.entries = entries
	b.Float.Rect = BoxViewRect(b.snap, b.rows())
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

// SetWatchers replaces the watcher page after a msg-watchers reply. The float
// holds the page whole while the page is longer than the log.
func (b *BoxView) SetWatchers(watchers []box.Watch, th theme.Theme) {
	b.watchers = watchers
	b.Float.Rect = BoxViewRect(b.snap, b.rows())
	b.clamp()
	b.pageClamp()
	b.paint(th)
}

// boxSeqIndex finds an entry by seq in the newest-first log.
func boxSeqIndex(entries []box.Entry, seq int) int {
	for i, e := range entries {
		if e.Seq == seq {
			return i
		}
	}
	return -1
}

// Scroll moves the window by n rows, positive toward older rows. The page and
// the log keep their own offsets, so a toggle leaves each where it was.
func (b *BoxView) Scroll(n int, th theme.Theme) {
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
func (b *BoxView) pageClamp() {
	max := len(b.watchers) - b.Float.Rect.H
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

// ListLen is the number of rows in the list the float shows.
func (b *BoxView) ListLen() int {
	if b.page {
		return len(b.watchers)
	}
	return len(b.entries)
}

// clamp keeps the window on the log: the offset never passes the oldest row
// that still fills the pane, a log that shrank goes back to the top, and the
// arrival count clears once the reader is at the top.
func (b *BoxView) clamp() {
	max := len(b.entries) - b.Float.Rect.H
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

// TogglePage swaps the float between the entry list and the watcher page.
func (b *BoxView) TogglePage(th theme.Theme) {
	b.page = !b.page
	b.paint(th)
}

// paint rewrites the float's terminal from the entries or the watcher list.
// Every row is placed absolutely, so a view that scrolled paints its new
// window whole.
func (b *BoxView) paint(th theme.Theme) {
	r := b.Float.Rect
	b.Float.Title = b.title()
	b.Float.Hint = b.hint()
	if r.W < 1 || r.H < 1 {
		b.Float.VT = vt10x.New(vt10x.WithSize(1, 1))
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
	b.Float.VT = vt
}

// paintEntries draws the window of the log. A wide enough rect carries the
// receipt count in every row.
func (b *BoxView) paintEntries(buf *bytes.Buffer, r layout.Rect, th theme.Theme) {
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
func (b *BoxView) paintWatchers(buf *bytes.Buffer, r layout.Rect, th theme.Theme) {
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
func (b *BoxView) title() string {
	n := len(b.entries)
	var head string
	switch n {
	case 0:
		head = "box  empty"
	case 1:
		head = "box  1 entry"
	default:
		rows := b.Float.Rect.H
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
func (b *BoxView) hint() string {
	if b.page {
		return " j/k scroll  w log  esc close "
	}
	return " j/k scroll  w watchers  esc close "
}

// parked counts the watches whose wait is outstanding.
func (b *BoxView) parked() int {
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
func (b *BoxView) received(e box.Entry) int {
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
func watchMatches(w box.Watch, key string) bool {
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
func watchFilter(w box.Watch) string {
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
func boxPaintWatch(buf *bytes.Buffer, w box.Watch, width int) {
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
func boxPaintRow(buf *bytes.Buffer, th theme.Theme, e box.Entry, w int, receipt int) {
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
	buf.WriteString("\x1b[0m" + boxStateColour(th, e.State).FG())
	buf.WriteString(state)
	buf.WriteString("\x1b[0m\x1b[90m")
	buf.WriteString(row[start+len(state):])
}

// boxRowText is the plain text of one entry row, columns padded and the whole
// clipped to w cells.
func boxRowText(e box.Entry, w int, receipt int) string {
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
func boxStateColour(th theme.Theme, st box.State) theme.RGB {
	if st == box.StateNote {
		return th.Palette.RailDim
	}
	if s, ok := th.States[string(st)]; ok {
		return s.Colour
	}
	if s, ok := th.States["unknown"]; ok {
		return s.Colour
	}
	return theme.DefaultTheme().States["unknown"].Colour
}
