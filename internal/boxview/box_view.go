// Package boxview paints the message box float: its rows, its watcher page,
// its scroll window and its borders.
package boxview

// The message box as a floating pane. The view is read-only: it lists the
// session's entries newest first, one row each, and follows the log while it is
// open through the server's box push. A topic filter narrows it: the whole log,
// or the notify branch the notification centre opens on. It also keeps the
// presence list, so a reader can see who is parked on which filter and how far
// each has read. Keys scroll it, swap the watcher page, toggle the filter and
// act on the selected entry; every key it names comes from the box keymap, so a
// rebind moves the hint with it.
//
// The rows, the selection, the filter and the unread marks live in ui.MessageList,
// which owns the window and the row painter. The view keeps the float, the
// watcher page, the title and the hint.

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"dm/internal/box"
	"dm/internal/cells"
	"dm/internal/hintbar"
	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/panel"
	"dm/internal/theme"
	"dm/internal/ui"
	"dm/internal/vt"
	"dm/internal/wire"
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
)

// BoxView is the open message-box float: the log under its topic filter, the
// watchers the server last reported, and the float that draws them.
type BoxView struct {
	// list is the log under its filter: the rows, the selection, the window,
	// the read mark and the row painter.
	list ui.MessageList
	// new counts the entries that arrived above a scrolled-back window. It is
	// zero while the window follows the newest entry.
	new int

	watchers []box.Watch
	// page is true while the float shows the watcher list instead of the log,
	// and watches is the window over the watch rows. The page selects nothing,
	// so the window carries no selection.
	page    bool
	watches ui.Viewport
	// WatchAt is when the view last asked for the watcher list, and WatchWant
	// marks a push whose ask the interval held back.
	WatchAt   time.Time
	WatchWant bool

	// Float is the pane that draws the view.
	Float *panel.FloatingPane
	// snap sizes the float: the view fits the log until it reaches its cap,
	// so a short log reads in a short box and a long one fills the screen.
	snap *wire.Snapshot
	// th is the theme the float last painted. Sync compares it, so a reload
	// that changes the theme repaints a float whose rect did not move.
	th theme.Theme
	// km names the key of every hint the view paints. A reload swaps it
	// through SetKeymap, so a rebind moves the border without reopening the
	// float.
	km *keymap.Config
}

// NewBoxView opens the float on a screen. km is the config the hint names its
// keys from. The log stays empty until the reply lands.
func NewBoxView(snap *wire.Snapshot, th theme.Theme, km *keymap.Config) *BoxView {
	b := &BoxView{Float: &panel.FloatingPane{ID: "box", Active: true}, snap: snap, km: km}
	b.Sync(snap, th)
	return b
}

// SetKeymap swaps the config the hint is derived from and rewrites the border
// hint in place, so a reload that rebinds a key moves the hint without
// reopening the float.
func (b *BoxView) SetKeymap(km *keymap.Config) {
	b.km = km
	b.Float.Hint = b.hint()
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

// ReaderMinRows is the shortest reader overlay. The reader copies the list's
// rect, but a short log would leave it one or two rows tall and the body would
// sit below the window, so the overlay keeps at least this many rows.
const ReaderMinRows = 12

// ReaderRect is where the body reader sits: the list's rect widened to at
// least ReaderMinRows, centred and clamped the way the list's rect is. A list
// taller than the floor keeps its own height, so a long log reads the whole
// entry in one screen.
func ReaderRect(snap *wire.Snapshot, listRows int) layout.Rect {
	if listRows < ReaderMinRows {
		listRows = ReaderMinRows
	}
	return BoxViewRect(snap, listRows)
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

// Sync fits the float to the screen after a snapshot or a rail change. The
// float's content is a cached terminal the client blits every frame, so a
// config reload only reaches it here: a theme that changed repaints even when
// the rect did not move.
func (b *BoxView) Sync(snap *wire.Snapshot, th theme.Theme) {
	b.snap = snap
	r := BoxViewRect(snap, b.rows())
	if r == b.Float.Rect && themeEqual(b.th, th) {
		return
	}
	b.Float.Rect = r
	b.paint(th)
}

// themeEqual reports whether two themes paint the box the same way. Theme
// carries a map, so it cannot compare with ==; the box reads the palette, the
// accents, the glyphs, the rail marks and the per-state styles, so it compares
// those.
func themeEqual(a, b theme.Theme) bool {
	if a.Palette != b.Palette || a.Accents != b.Accents ||
		a.Glyphs != b.Glyphs || a.RailMarks != b.RailMarks {
		return false
	}
	if len(a.States) != len(b.States) {
		return false
	}
	for k, sa := range a.States {
		if sb, ok := b.States[k]; !ok || sa != sb {
			return false
		}
	}
	return true
}

// rows is the number of rows the float fits: the larger list wins, so the
// watcher page is usable before the first entry lands.
func (b *BoxView) rows() int {
	if len(b.watchers) > b.list.Len() {
		return len(b.watchers)
	}
	return b.list.Len()
}

// Set replaces the log after a list reply or a box push. A scrolled-back
// window keeps its top entry by seq in the rows the filter shows, so an
// arrival the filter hides does not move the text under the reader. A window
// at the top follows the newest entry instead.
func (b *BoxView) Set(entries []box.Entry, th theme.Theme) {
	// View.Off is an index in the filtered rows, so the window anchor and its
	// new row are read and found through the filter as well.
	old := b.list.View.Off
	follow := old == 0
	top := b.list.SeqAt(old)
	// The list keeps the selection on its seq. A window at the top moves the
	// selection to the newest entry instead, the way a log reader expects.
	b.list.Set(entries)
	b.Float.Rect = BoxViewRect(b.snap, b.rows())
	switch {
	case follow:
		b.list.Top()
		b.new = 0
	case top != 0:
		if i := b.list.Index(top); i >= 0 {
			// The rows the filter shows between the old top and the new one
			// arrived above the window; the rest of the view did not move.
			b.new += i - old
			if b.new < 0 {
				b.new = 0
			}
			b.list.View.Off = i
		}
	}
	b.paint(th)
}

// SetFilter narrows the log to a topic prefix; an empty prefix shows every
// entry. The selection stays on its seq when the new filter shows it, and the
// float refits to the rows that are left.
func (b *BoxView) SetFilter(prefix string, th theme.Theme) {
	if prefix == b.list.Filter {
		return
	}
	b.list.SetFilter(prefix)
	b.Float.Rect = BoxViewRect(b.snap, b.rows())
	b.paint(th)
}

// Filter is the topic prefix the log is narrowed to, empty when it shows every
// entry.
func (b *BoxView) Filter() string {
	return b.list.Filter
}

// ToggleFilter swaps between the whole log and the notify topic: an unfiltered
// log shows the notify branch, and a filtered one opens to every entry.
func (b *BoxView) ToggleFilter(th theme.Theme) {
	if b.list.Filter == "" {
		b.SetFilter(box.NotifyPrefix, th)
		return
	}
	b.SetFilter("", th)
}

// SetRead raises the list's read mark to seq; an entry is unread when its seq
// is above it. The mark never walks back.
func (b *BoxView) SetRead(seq int, th theme.Theme) {
	if seq <= b.list.Read {
		return
	}
	b.list.Read = seq
	b.paint(th)
}

// MarkRead marks one entry read, the entry whose body the operator opened.
func (b *BoxView) MarkRead(seq int, th theme.Theme) {
	b.list.MarkRead(seq)
	b.paint(th)
}

// MarkReadAll marks every entry the filter shows read and reports the mark.
// The caller mirrors it, so the bar badge counts from the same seq.
func (b *BoxView) MarkReadAll() int {
	b.list.MarkReadAll()
	return b.list.Read
}

// SetWatchers replaces the watcher page after a msg-watchers reply. The float
// holds the page whole while the page is longer than the log.
func (b *BoxView) SetWatchers(watchers []box.Watch, th theme.Theme) {
	b.watchers = watchers
	b.Float.Rect = BoxViewRect(b.snap, b.rows())
	b.paint(th)
}

// Scroll moves the selection by n rows, positive toward older rows, and the
// window follows so the selection stays in view. The watcher page keeps its
// own offset and scrolls, so a toggle leaves each where it was.
func (b *BoxView) Scroll(n int, th theme.Theme) {
	if b.page {
		b.watches.Off += n
		b.fitWatches()
	} else {
		b.list.Move(n)
	}
	b.paint(th)
}

// ListLen is the number of rows in the list the float shows.
func (b *BoxView) ListLen() int {
	if b.page {
		return len(b.watchers)
	}
	return b.list.Len()
}

// fit sizes both windows to their lists and the float and holds them on their
// rows. It runs on every paint, so a resize, a shorter log and a new entry
// all land here. The arrival count clears once the window is back at the
// newest entry.
func (b *BoxView) fit() {
	b.list.View.H = b.Float.Rect.H
	b.list.View.Ensure()
	b.fitWatches()
	if b.list.View.Off == 0 {
		b.new = 0
	}
}

// fitWatches sizes the page's window to the watcher rows and the float. The
// page selects nothing, so its window carries no selection.
func (b *BoxView) fitWatches() {
	b.watches.N, b.watches.H, b.watches.Sel = len(b.watchers), b.Float.Rect.H, -1
	b.watches.Clamp()
}

// Selected is the entry the selection sits on: the one the body reader opens.
// The watcher page has no selection.
func (b *BoxView) Selected() (box.Entry, bool) {
	if b.page {
		return box.Entry{}, false
	}
	return b.list.Selected()
}

// Top is the index of the window's first row. The view owns the window; a
// caller reads it here instead of keeping its own mirror.
func (b *BoxView) Top() int {
	return b.list.View.Off
}

// TogglePage swaps the float between the entry list and the watcher page.
func (b *BoxView) TogglePage(th theme.Theme) {
	b.page = !b.page
	b.paint(th)
}

// Unread is the count of visible entries past the read mark.
func (b *BoxView) Unread() int {
	return b.list.Unread()
}

// paint rewrites the float's terminal from the entries or the watcher list.
// Every row is placed absolutely, so a view that scrolled paints its new
// window whole.
func (b *BoxView) paint(th theme.Theme) {
	b.th = th
	b.fit()
	r := b.Float.Rect
	b.Float.Title = b.title()
	b.Float.Hint = b.hint()
	if r.W < 1 || r.H < 1 {
		b.Float.VT = vt.NewTerm(1, 1)
		return
	}
	term := vt.NewTerm(r.W, r.H)
	var buf bytes.Buffer
	if b.page {
		b.paintWatchers(&buf, r, th)
	} else {
		b.paintEntries(&buf, r, th)
	}
	term.Write(buf.Bytes())
	term.Flush()
	b.Float.VT = term
}

// paintEntries draws the window of the log with the list's own painter. The
// receipt count per entry comes from the watches: the filters that cover the
// entry's key and whose cursor is at or past its seq.
func (b *BoxView) paintEntries(buf *bytes.Buffer, r layout.Rect, th theme.Theme) {
	if b.list.Len() == 0 {
		boxPaintNote(buf, 1, b.emptyNote(), r.W)
		return
	}
	receipts := make(map[int]int, len(b.list.Entries))
	for _, e := range b.list.Entries {
		receipts[e.Seq] = b.received(e)
	}
	b.list.Draw(buf, th, ui.MessageListProps{
		Rect:     layout.Rect{W: r.W, H: r.H},
		Dim:      th.Palette.RailDim,
		Text:     th.Palette.RailText,
		Active:   true,
		Receipts: receipts,
	})
}

// emptyNote is the placeholder the list page paints when it has no rows. A
// filter can hide every entry of a log that is not empty, so the note names the
// filter when one is on.
func (b *BoxView) emptyNote() string {
	if b.list.Filter != "" {
		return "no entries under " + b.list.Filter
	}
	return "no entries yet"
}

// paintWatchers draws the watcher page in the server's order, clipped to the
// rect and to its rows.
func (b *BoxView) paintWatchers(buf *bytes.Buffer, r layout.Rect, th theme.Theme) {
	if len(b.watchers) == 0 {
		boxPaintNote(buf, 1, "no watchers yet", r.W)
		return
	}
	for row := 0; row < r.H; row++ {
		i := b.watches.Off + row
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

// title is the count in the top border, naming the filter: box unfiltered,
// notifications filtered. It carries the visible rows when the log is longer
// than the box, the parked watches, the entries that arrived above a
// scrolled-back window and the unread count. Each is omitted when it is zero.
func (b *BoxView) title() string {
	n := b.list.Len()
	name := "box"
	if b.list.Filter != "" {
		name = "notifications"
	}
	var head string
	switch n {
	case 0:
		head = name + "  empty"
	case 1:
		head = name + "  1 entry"
	default:
		rows := b.Float.Rect.H
		if n <= rows {
			head = fmt.Sprintf("%s  %d entries", name, n)
		} else {
			last := b.list.View.Off + rows
			if last > n {
				last = n
			}
			head = fmt.Sprintf("%s  %d entries  %d-%d", name, n, b.list.View.Off+1, last)
		}
	}
	parts := []string{head}
	if parked := b.parked(); parked > 0 {
		parts = append(parts, fmt.Sprintf("%d watching", parked))
	}
	if b.new > 0 {
		parts = append(parts, fmt.Sprintf("+%d", b.new))
	}
	if unread := b.list.Unread(); unread > 0 {
		parts = append(parts, fmt.Sprintf("%d unread", unread))
	}
	return " " + strings.Join(parts, "  ") + " "
}

// hint is the float's bottom border: the keys the view takes, every one named
// from the box keymap, so a rebind moves the hint. The entry keys show only
// while the log page has a row to act on: the watcher page has no selection.
//
// The bar drops hints from the tail on a narrow border, so the least useful
// hint sits nearest the tail. Move, body and close carry Keep, so the keys a
// cramped box still needs survive whatever the width drops. The droppable
// hints are ordered by reverse worth, which keeps the old drop order: clear all
// goes first, then notify or all, then pane, then clear, then watch.
func (b *BoxView) hint() *hintbar.Bar {
	keep := func(h hintbar.Hint) hintbar.Hint {
		h.Keep = true
		return h
	}
	move := keep(hintbar.Hint{Keys: []string{b.key("down"), b.key("up")}, Label: "move"})
	closeHint := keep(hintbar.Hint{Keys: []string{b.key("close-esc")}, Label: "close"})
	if b.page {
		return &hintbar.Bar{Hints: []hintbar.Hint{
			move,
			{Keys: []string{b.key("toggle-page")}, Label: "log"},
			closeHint,
		}}
	}
	hints := []hintbar.Hint{move}
	if e, ok := b.Selected(); ok {
		if e.Msg != "" || e.Body != "" {
			hints = append(hints, keep(hintbar.Hint{Keys: []string{b.key("body")}, Label: "body"}))
		}
	}
	label := "notify"
	if b.list.Filter != "" {
		label = "all"
	}
	hints = append(hints,
		hintbar.Hint{Keys: []string{b.key("toggle-page")}, Label: "watch"},
		hintbar.Hint{Keys: []string{b.key("clear-entry")}, Label: "clear"},
	)
	if _, ok := b.Selected(); ok {
		hints = append(hints,
			hintbar.Hint{Keys: []string{b.key("jump-pane")}, Label: "pane"},
			hintbar.Hint{Keys: []string{b.key("filter-notify")}, Label: label},
			hintbar.Hint{Keys: []string{b.key("clear-filter")}, Label: "clear all"},
		)
	}
	hints = append(hints, closeHint)
	return &hintbar.Bar{Hints: hints}
}

// key is the key the box keymap shows for action. A view without a config
// names no key, and the hint drops that key rather than inventing one.
func (b *BoxView) key(action string) string {
	if b.km == nil {
		return ""
	}
	return b.km.Key("box", action)
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
