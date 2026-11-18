package boxview

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"dm/internal/box"
	"dm/internal/cells"
	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/testutil"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"
)

// boxEntries is a log with one of four states, so a row test covers the column
// layout and the state colours.
func boxEntries() []box.Entry {
	return []box.Entry{
		{Seq: 9, Key: "lane/a", State: box.StateDone, From: "w3p1", Msg: "gate green"},
		{Seq: 8, Key: "lane/a", State: box.StateWorking, From: "w3p1", Msg: "running tests"},
		{Seq: 7, Key: "lane/b", State: box.StateBlocked, From: "w2p2", Msg: "waiting on review"},
		{Seq: 6, Key: "lane/c", State: box.StateError, From: "w1p1", Msg: "build failed"},
	}
}

// boxWatches is a presence list in the server's order: pane, then key, then
// branch. The first watch is parked, so the title and the page have a wait to
// show, and the second trails one seq behind the newest entry.
func boxWatches() []box.Watch {
	at := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	return []box.Watch{
		{Pane: "w1p1", Key: "lane/a", Cursor: 9, Parked: true, At: at},
		{Pane: "w1p2", KeyPrefix: "lane/", Cursor: 8, At: at},
	}
}

// receiptWatches is boxWatches plus a watch on another key with a high cursor.
// It matches neither lane/a entry, so the receipt count proves the filter is
// part of the rule and not just the cursor.
func receiptWatches() []box.Watch {
	return append(boxWatches(), box.Watch{Pane: "w1p3", Key: "lane/b", Cursor: 9})
}

// boxVTPaint is the content painter for a float backed by a terminal. It is a
// copy of the client's painter: the moved test cannot reach package main.
func boxVTPaint(term *vt.Term) func(*bytes.Buffer, layout.Rect) {
	return func(buf *bytes.Buffer, r layout.Rect) {
		testutil.PaintTerm(buf, term, r, 0)
	}
}

// boxScreen replays the float's bytes into a fresh screen, so a test reads what
// a terminal would show.
func boxScreen(t *testing.T, b *BoxView, cols, rows int) *vt.Term {
	t.Helper()
	var buf bytes.Buffer
	b.Float.Paint = boxVTPaint(b.Float.VT)
	b.Float.Draw(&buf, theme.DefaultTheme())
	screen := vt.NewTerm(cols, rows)
	screen.Write(buf.Bytes())
	screen.Flush()
	return screen
}

// boxScreenRow reads one row of a replayed screen, trailing blanks dropped.
func boxScreenRow(screen *vt.Term, y, x, w int) string {
	var b strings.Builder
	for i := 0; i < w; i++ {
		c := screen.Grid().Cell(x+i, y).Content
		if c == "" {
			c = " "
		}
		b.WriteString(c)
	}
	return strings.TrimRight(b.String(), " ")
}

// boxStateColour is the state's hue as the theme gives it, the colour the row
// painter puts on the state word.
func boxStateColour(th theme.Theme, st box.State) theme.RGB {
	if s, ok := th.States[string(st)]; ok {
		return s.Colour
	}
	return th.Palette.RailDim
}

// stateCellX is the screen column of the first cell of the state word in a
// drawn row. The columns before it are ASCII, so the byte offset is the cell
// offset.
func stateCellX(row string, r layout.Rect, state string) int {
	return r.X + strings.Index(row, state)
}

// TestBoxViewListsNewestFirst: the rows follow the entries newest first, the
// state word carries the rail's colour for that state, and the borders carry
// the title and the key hint.
func TestBoxViewListsNewestFirst(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 30, Rail: 34}
	b := NewBoxView(snap, theme.DefaultTheme(), keymap.LoadDefault())
	b.Set(boxEntries(), theme.DefaultTheme())
	screen := boxScreen(t, b, snap.Cols, snap.Rows)
	r := b.Float.Rect

	if got := boxScreenRow(screen, r.Y-1, r.X+2, r.W-2); !strings.HasPrefix(got, "box  4 entries") {
		t.Fatalf("title = %q, want the entry count", got)
	}
	first := boxScreenRow(screen, r.Y, r.X, r.W)
	for _, want := range []string{"9", "done", "lane/a", "w3p1", "gate green"} {
		if !strings.Contains(first, want) {
			t.Fatalf("first row %q misses %q", first, want)
		}
	}
	last := boxScreenRow(screen, r.Y+3, r.X, r.W)
	if !strings.Contains(last, "build failed") || !strings.Contains(last, "error") {
		t.Fatalf("oldest row = %q, want the last entry", last)
	}
	if got := boxScreenRow(screen, r.Y+r.H, r.X+2, r.W-2); !strings.Contains(got, "esc close") {
		t.Fatalf("hint = %q, want the close key", got)
	}

	// The state word carries the rail's colour for that state.
	want := boxStateColour(theme.DefaultTheme(), box.StateDone)
	got := screen.Grid().Cell(stateCellX(first, r, "done"), r.Y).Style.Fg
	if got.Kind != vt.ColorRGB || got.R != uint8(want.R) || got.G != uint8(want.G) || got.B != uint8(want.B) {
		t.Fatalf("done colour = %+v, want %v", got, want)
	}
}

// TestBoxViewScrollClamps: the window stops at the newest row and at the oldest
// row that still fills the pane.
func TestBoxViewScrollClamps(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}
	b := NewBoxView(snap, theme.DefaultTheme(), keymap.LoadDefault())
	entries := make([]box.Entry, 0, 40)
	for i := 0; i < 40; i++ {
		entries = append(entries, box.Entry{Seq: i + 1, State: box.StateNote, Msg: "note"})
	}
	b.Set(entries, theme.DefaultTheme())
	max := len(entries) - b.Float.Rect.H
	if max <= 0 {
		t.Fatalf("fixture is too short to scroll: %d entries in %d rows", len(entries), b.Float.Rect.H)
	}
	b.Scroll(100, theme.DefaultTheme())
	if b.list.View.Off != max {
		t.Fatalf("off = %d, want the oldest row %d", b.list.View.Off, max)
	}
	b.Scroll(-100, theme.DefaultTheme())
	if b.list.View.Off != 0 {
		t.Fatalf("off = %d, want the newest row", b.list.View.Off)
	}
	// A log that shrinks under the window pulls the view back to the top.
	b.Scroll(max, theme.DefaultTheme())
	b.Set(entries[:2], theme.DefaultTheme())
	if b.list.View.Off != 0 {
		t.Fatalf("off = %d after the log shrank, want 0", b.list.View.Off)
	}
}

// TestBoxViewFitsLog: a short log makes a short box, a long one fills the cap,
// and the title names the rows in view once the log scrolls.
func TestBoxViewFitsLog(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}
	b := NewBoxView(snap, theme.DefaultTheme(), keymap.LoadDefault())
	b.Set(boxEntries(), theme.DefaultTheme())
	if got, want := b.Float.Rect.H, len(boxEntries()); got != want {
		t.Fatalf("height = %d, want the log length %d", got, want)
	}
	entries := make([]box.Entry, 60)
	for i := range entries {
		entries[i] = box.Entry{Seq: i + 1, State: box.StateNote, Msg: "note"}
	}
	b.Set(entries, theme.DefaultTheme())
	if b.Float.Rect.H != 22 {
		t.Fatalf("height = %d, want the cap of 22 rows", b.Float.Rect.H)
	}
	// Twenty-four js put the selection on the window's last row and the
	// window top three rows down, so the title names rows 4 to 25.
	b.Scroll(24, theme.DefaultTheme())
	if got := b.title(); !strings.Contains(got, "4-25") {
		t.Fatalf("title = %q, want the rows in view", got)
	}
}

// TestBoxViewTitleWatching: the title counts parked watches, omits the count
// when none is parked, and keeps the entry count.
func TestBoxViewTitleWatching(t *testing.T) {
	b := NewBoxView(&wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}, theme.DefaultTheme(), keymap.LoadDefault())
	b.Set(boxEntries(), theme.DefaultTheme())
	b.SetRead(boxEntries()[0].Seq, theme.DefaultTheme())
	b.watchers = boxWatches()
	b.paint(theme.DefaultTheme())
	if got, want := b.title(), " box  4 entries  1 watching "; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
	b.watchers[1].Parked = true
	if got, want := b.title(), " box  4 entries  2 watching "; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
	b.watchers = nil
	if got, want := b.title(), " box  4 entries "; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
}

// receiptPrefix is the leading text of a row the painter writes: the leading
// cell, the unread mark for a read entry, then the receipt count.
func receiptPrefix(count int) string {
	return "  " + fmt.Sprintf("%2d ", count)
}

// seqPrefix is the leading text of a row without the receipt column: the
// leading cell, the blank unread mark and the seq.
func seqPrefix(seq int) string {
	return "  " + fmt.Sprintf("%4d ", seq)
}

// TestBoxViewReceipt: a wide view prefixes each row with the number of watches
// served that entry. A narrow view drops the marker whole.
func TestBoxViewReceipt(t *testing.T) {
	th := theme.DefaultTheme()
	wide := NewBoxView(&wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}, th, keymap.LoadDefault())
	wide.watchers = receiptWatches()
	wide.Set(boxEntries(), th)
	wide.SetRead(boxEntries()[0].Seq, th)
	if wide.Float.Rect.W < 60 {
		t.Fatalf("wide rect = %d, want at least the receipt width", wide.Float.Rect.W)
	}
	screen := boxScreen(t, wide, 100, 30)
	r := wide.Float.Rect
	if row := boxScreenRow(screen, r.Y, r.X, r.W); !strings.HasPrefix(row, receiptPrefix(1)) || !strings.Contains(row, "gate green") {
		t.Fatalf("first row = %q, want one receipt", row)
	}
	if row := boxScreenRow(screen, r.Y+1, r.X, r.W); !strings.HasPrefix(row, receiptPrefix(2)) {
		t.Fatalf("second row = %q, want two receipts", row)
	}

	narrow := NewBoxView(&wire.Snapshot{Cols: 50, Rows: 30, Rail: 0}, th, keymap.LoadDefault())
	narrow.watchers = receiptWatches()
	narrow.Set(boxEntries(), th)
	narrow.SetRead(boxEntries()[0].Seq, th)
	if narrow.Float.Rect.W >= 60 {
		t.Fatalf("narrow rect = %d, want under the receipt width", narrow.Float.Rect.W)
	}
	screen = boxScreen(t, narrow, 50, 30)
	r = narrow.Float.Rect
	row := boxScreenRow(screen, r.Y, r.X, r.W)
	if !strings.HasPrefix(row, seqPrefix(boxEntries()[0].Seq)) {
		t.Fatalf("narrow row = %q, want no marker", row)
	}
}

// TestBoxViewReceiptBoundary: the marker is drawn at exactly 60 columns and
// dropped one column short. The rect is the screen width less the six cells
// BoxViewRect keeps clear.
func TestBoxViewReceiptBoundary(t *testing.T) {
	th := theme.DefaultTheme()
	e := boxEntries()[0]
	for _, tc := range []struct {
		cols int
		want int
	}{
		{66, 60},
		{65, 59},
	} {
		snap := &wire.Snapshot{Cols: tc.cols, Rows: 30, Rail: 0}
		b := NewBoxView(snap, th, keymap.LoadDefault())
		b.watchers = boxWatches()
		b.Set([]box.Entry{e}, th)
		b.SetRead(e.Seq, th)
		if got := b.Float.Rect.W; got != tc.want {
			t.Fatalf("cols %d: rect = %d, want %d", tc.cols, got, tc.want)
		}
		screen := boxScreen(t, b, tc.cols, 30)
		r := b.Float.Rect
		row := boxScreenRow(screen, r.Y, r.X, r.W)
		if tc.want >= 60 {
			if !strings.HasPrefix(row, receiptPrefix(1)) {
				t.Fatalf("cols %d: row = %q, want the receipt marker", tc.cols, row)
			}
			continue
		}
		if !strings.HasPrefix(row, seqPrefix(e.Seq)) {
			t.Fatalf("cols %d: row = %q, want no marker", tc.cols, row)
		}
	}
}

// TestBoxViewAnchor: a scrolled-back window keeps its top entry when the log
// grows and names what arrived above it. At the top the view follows the
// newest entry and the count clears.
func TestBoxViewAnchor(t *testing.T) {
	b := NewBoxView(&wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}, theme.DefaultTheme(), keymap.LoadDefault())
	entries := make([]box.Entry, 40)
	for i := range entries {
		entries[i] = box.Entry{Seq: 40 - i, State: box.StateNote, Msg: "note"}
	}
	b.Set(entries, theme.DefaultTheme())
	b.Scroll(100, theme.DefaultTheme())
	top := b.list.Entries[b.list.View.Off].Seq
	if b.list.Entries[b.list.View.Sel].Seq != 1 || b.list.View.Off != len(entries)-b.Float.Rect.H {
		t.Fatalf("selection = %d off = %d, want the oldest row in the window's last row", b.list.Entries[b.list.View.Sel].Seq, b.list.View.Off)
	}

	// Two pushes, one entry each: the count must add up, not restart from the
	// current index.
	b.Set(append([]box.Entry{{Seq: 41, State: box.StateNote, Msg: "note"}}, entries...), theme.DefaultTheme())
	if got := b.list.Entries[b.list.View.Off].Seq; got != top {
		t.Fatalf("top seq = %d after one entry, want %d", got, top)
	}
	if b.new != 1 {
		t.Fatalf("new = %d after one entry, want 1", b.new)
	}
	b.Set(append([]box.Entry{{Seq: 42, State: box.StateNote, Msg: "note"}}, b.list.Entries...), theme.DefaultTheme())
	if got := b.list.Entries[b.list.View.Off].Seq; got != top {
		t.Fatalf("top seq = %d after two entries, want %d", got, top)
	}
	if b.new != 2 {
		t.Fatalf("new = %d after two entries, want 2", b.new)
	}
	if got := b.title(); !strings.Contains(got, "+2") {
		t.Fatalf("title = %q, want the arrival count", got)
	}

	b.Scroll(-b.ListLen(), theme.DefaultTheme())
	b.Set(append([]box.Entry{{Seq: 44, State: box.StateNote, Msg: "note"}}, b.list.Entries...), theme.DefaultTheme())
	if b.list.View.Off != 0 || b.new != 0 || strings.Contains(b.title(), "+") {
		t.Fatalf("off = %d, new = %d, title = %q, want the newest entry", b.list.View.Off, b.new, b.title())
	}
}

// TestBoxViewSelectionFollows: j and k move the selection, the window
// follows once the selection would leave the view, and the clamps hold.
func TestBoxViewSelectionFollows(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}
	b := NewBoxView(snap, theme.DefaultTheme(), keymap.LoadDefault())
	entries := make([]box.Entry, 0, 30)
	for i := 0; i < 30; i++ {
		entries = append(entries, box.Entry{Seq: 30 - i, State: box.StateNote, Msg: "note"})
	}
	b.Set(entries, theme.DefaultTheme())
	if e, ok := b.Selected(); !ok || e.Seq != 30 {
		t.Fatalf("selected = %+v ok %v, want the newest entry", e, ok)
	}
	h := b.Float.Rect.H
	// h moves down put the selection on the window's last row, so the window
	// top moved with it.
	b.Scroll(h, theme.DefaultTheme())
	if e, _ := b.Selected(); e.Seq != 30-h {
		t.Fatalf("selection sat still at %d, want %d", e.Seq, 30-h)
	}
	if b.list.View.Off != 1 {
		t.Fatalf("off = %d, want 1", b.list.View.Off)
	}
	// A move inside the window leaves the window alone.
	b.Scroll(-1, theme.DefaultTheme())
	if e, _ := b.Selected(); e.Seq != 30-h+1 {
		t.Fatalf("selection = %d, want %d", e.Seq, 30-h+1)
	}
	if b.list.View.Off != 1 {
		t.Fatalf("off = %d, want it still at 1", b.list.View.Off)
	}
	// The clamps hold at both ends.
	b.Scroll(1000, theme.DefaultTheme())
	if b.list.View.Off != len(entries)-h || b.list.View.Sel != len(entries)-1 {
		t.Fatalf("off = %d sel = %d, want the oldest full window and the last row", b.list.View.Off, b.list.View.Sel)
	}
	b.Scroll(-1000, theme.DefaultTheme())
	if b.list.View.Off != 0 || b.list.View.Sel != 0 {
		t.Fatalf("off = %d sel = %d, want the newest row", b.list.View.Off, b.list.View.Sel)
	}
}

// TestBoxViewSelectionKeepsBySeq: a refill that arrives over a scrolled-back
// window keeps the selection on its entry; a window at the top keeps it on
// the newest entry.
func TestBoxViewSelectionKeepsBySeq(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}
	b := NewBoxView(snap, theme.DefaultTheme(), keymap.LoadDefault())
	entries := make([]box.Entry, 0, 40)
	for i := 0; i < 40; i++ {
		entries = append(entries, box.Entry{Seq: 40 - i, State: box.StateNote, Msg: "note"})
	}
	b.Set(entries, theme.DefaultTheme())

	// Scrolled back: the selection rides its entry through the arrival.
	b.Scroll(1000, theme.DefaultTheme())
	top := b.list.Entries[b.list.View.Off].Seq
	b.Set(append([]box.Entry{{Seq: 41, State: box.StateNote, Msg: "note"}}, entries...), theme.DefaultTheme())
	if b.list.Entries[b.list.View.Sel].Seq != 1 {
		t.Fatalf("selection seq = %d, want 1", b.list.Entries[b.list.View.Sel].Seq)
	}
	if b.list.Entries[b.list.View.Off].Seq != top {
		t.Fatalf("top seq = %d, want %d", b.list.Entries[b.list.View.Off].Seq, top)
	}
	if b.new != 1 {
		t.Fatalf("new = %d, want 1", b.new)
	}

	// At the top: the selection rides the newest entry instead.
	b = NewBoxView(snap, theme.DefaultTheme(), keymap.LoadDefault())
	b.Set(entries, theme.DefaultTheme())
	b.Set(append([]box.Entry{{Seq: 41, State: box.StateNote, Msg: "note"}}, entries...), theme.DefaultTheme())
	if e, _ := b.Selected(); e.Seq != 41 {
		t.Fatalf("selection seq = %d, want the newest entry 41", e.Seq)
	}
	if b.list.View.Off != 0 || b.new != 0 {
		t.Fatalf("off = %d new = %d, want the window still at the top", b.list.View.Off, b.new)
	}
}

// TestBoxViewPaintsSelectionBright: the selected row sits at full intensity
// and the bright cell moves with the selection.
func TestBoxViewPaintsSelectionBright(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}
	b := NewBoxView(snap, theme.DefaultTheme(), keymap.LoadDefault())
	b.Set(boxEntries(), theme.DefaultTheme())
	screen := boxScreen(t, b, snap.Cols, snap.Rows)
	r := b.Float.Rect
	rowStyle := func(screen *vt.Term, y int) vt.Style {
		return screen.Grid().Cell(r.X, y).Style
	}
	if rowStyle(screen, r.Y) == rowStyle(screen, r.Y+1) {
		t.Fatalf("row styles match, want the selected row brighter")
	}
	b.Scroll(1, theme.DefaultTheme())
	screen = boxScreen(t, b, snap.Cols, snap.Rows)
	if rowStyle(screen, r.Y) == rowStyle(screen, r.Y+1) {
		t.Fatalf("row styles still match after the selection moved down")
	}
}

// TestBoxViewWatcherPageEmptyLog: the contract makes a parked wait appear
// before the first entry. The float sizes from the watches, so the page shows
// every watch, and the page has its own offset.
func TestBoxViewWatcherPageEmptyLog(t *testing.T) {
	few := append(boxWatches(), box.Watch{Pane: "w1p3", Key: "lane/b", Cursor: 4})
	b := NewBoxView(&wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}, theme.DefaultTheme(), keymap.LoadDefault())
	b.SetWatchers(few, theme.DefaultTheme())
	if got, want := b.Float.Rect.H, len(few); got != want {
		t.Fatalf("height = %d, want one row per watch %d", got, want)
	}
	b.TogglePage(theme.DefaultTheme())
	screen := boxScreen(t, b, 100, 30)
	r := b.Float.Rect
	for i, w := range few {
		row := boxScreenRow(screen, r.Y+i, r.X, r.W)
		if !strings.Contains(row, w.Pane) {
			t.Fatalf("row %d = %q, want pane %s", i, row, w.Pane)
		}
	}
	if b.watches.Off != 0 || b.list.View.Off != 0 {
		t.Fatalf("offsets = %d/%d, want both at the first row", b.watches.Off, b.list.View.Off)
	}
	if got := b.title(); !strings.Contains(got, "1 watching") {
		t.Fatalf("title = %q, want the parked wait", got)
	}

	// More watches than the cap: the page scrolls on its own offset and the
	// log stays at the top.
	many := make([]box.Watch, 25)
	for i := range many {
		many[i] = box.Watch{Pane: fmt.Sprintf("p%02d", i+1), Key: "lane/a", Cursor: i}
	}
	b = NewBoxView(&wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}, theme.DefaultTheme(), keymap.LoadDefault())
	b.Set(boxEntries(), theme.DefaultTheme())
	b.SetWatchers(many, theme.DefaultTheme())
	if b.Float.Rect.H != 22 {
		t.Fatalf("height = %d, want the cap of 22 rows", b.Float.Rect.H)
	}
	b.TogglePage(theme.DefaultTheme())
	b.Scroll(100, theme.DefaultTheme())
	if got := b.watches.Off; got != len(many)-b.Float.Rect.H {
		t.Fatalf("page offset = %d, want the last page %d", got, len(many)-b.Float.Rect.H)
	}
	if b.list.View.Off != 0 {
		t.Fatalf("log offset = %d, want the page to scroll alone", b.list.View.Off)
	}
	screen = boxScreen(t, b, 100, 30)
	r = b.Float.Rect
	last := boxScreenRow(screen, r.Y+r.H-1, r.X, r.W)
	if !strings.Contains(last, many[len(many)-1].Pane) {
		t.Fatalf("last row = %q, want the oldest watch %s", last, many[len(many)-1].Pane)
	}
	b.Scroll(-100, theme.DefaultTheme())
	if b.watches.Off != 0 {
		t.Fatalf("page offset = %d after scrolling back, want the first watch", b.watches.Off)
	}
}

// TestBoxViewRepaintsOnThemeChange: the float's content is a cached terminal
// the client blits every frame, so a config reload reaches it only through
// Sync. A theme that changed repaints even when the rect did not move.
func TestBoxViewRepaintsOnThemeChange(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}
	b := NewBoxView(snap, theme.DefaultTheme(), keymap.LoadDefault())
	b.Set(boxEntries(), theme.DefaultTheme())
	changed := theme.DefaultTheme()
	changed.States[string(box.StateDone)] = theme.StateStyle{
		Glyph: changed.States[string(box.StateDone)].Glyph, Colour: theme.RGB{R: 200, G: 100, B: 50},
	}
	b.Sync(snap, changed)
	screen := boxScreen(t, b, snap.Cols, snap.Rows)
	r := b.Float.Rect
	row := boxScreenRow(screen, r.Y, r.X, r.W)
	want := boxStateColour(changed, box.StateDone)
	got := screen.Grid().Cell(stateCellX(row, r, "done"), r.Y).Style.Fg
	if got.Kind != vt.ColorRGB || got.R != uint8(want.R) || got.G != uint8(want.G) || got.B != uint8(want.B) {
		t.Fatalf("done colour after reload = %+v, want %v", got, want)
	}
}

// TestBoxViewRowHighlightWideSeq: a seq past the four-cell column prints whole
// and shifts the later columns right, so the state highlight follows the
// rendered prefix instead of a fixed offset.
func TestBoxViewRowHighlightWideSeq(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}
	b := NewBoxView(snap, theme.DefaultTheme(), keymap.LoadDefault())
	entries := []box.Entry{
		{Seq: 12345, Key: "lane/a", State: box.StateDone, From: "w3p1", Msg: "gate green"},
	}
	b.Set(entries, theme.DefaultTheme())
	screen := boxScreen(t, b, snap.Cols, snap.Rows)
	r := b.Float.Rect
	row := boxScreenRow(screen, r.Y, r.X, r.W)
	if !strings.Contains(row, "12345 done") {
		t.Fatalf("row = %q, want the wide seq whole before the state", row)
	}
	want := boxStateColour(theme.DefaultTheme(), box.StateDone)
	for i := range "done" {
		got := screen.Grid().Cell(stateCellX(row, r, "done")+i, r.Y).Style.Fg
		if got.Kind != vt.ColorRGB || got.R != uint8(want.R) || got.G != uint8(want.G) || got.B != uint8(want.B) {
			t.Fatalf("state cell %d colour = %+v, want %v", i, got, want)
		}
	}
}

// TestBoxViewFilterNamesInTheTitle: the title names the filter, and the unread
// count rides it, so box and notifications never read the same.
func TestBoxViewFilterNamesInTheTitle(t *testing.T) {
	th := theme.DefaultTheme()
	b := NewBoxView(&wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}, th, keymap.LoadDefault())
	entries := []box.Entry{
		{Seq: 9, Key: "notify/w1p1", State: box.StateDone, From: "w1p1", Msg: "gate green"},
		{Seq: 8, Key: "lane/a", State: box.StateWorking, From: "w3p1", Msg: "running tests"},
	}
	b.Set(entries, th)
	b.SetRead(9, th)
	if got, want := b.title(), " box  2 entries "; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
	b.ToggleFilter(th)
	if got, want := b.title(), " notifications  1 entry "; got != want {
		t.Fatalf("filtered title = %q, want %q", got, want)
	}
	if got := b.Filter(); got != box.NotifyPrefix {
		t.Fatalf("filter = %q, want the notify branch", got)
	}
	b.ToggleFilter(th)
	if got := b.Filter(); got != "" {
		t.Fatalf("filter = %q, want every entry", got)
	}
}

// TestBoxViewFilterToggleKeepsSelection: the filter narrows the log, and the
// selection rides the entry it sat on when the new filter still shows it.
func TestBoxViewFilterToggleKeepsSelection(t *testing.T) {
	th := theme.DefaultTheme()
	b := NewBoxView(&wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}, th, keymap.LoadDefault())
	entries := []box.Entry{
		{Seq: 9, Key: "lane/a", State: box.StateDone, From: "w3p1", Msg: "gate green"},
		{Seq: 8, Key: "notify/w1p1", State: box.StateWorking, From: "w1p1", Msg: "running tests"},
		{Seq: 7, Key: "lane/b", State: box.StateNote, From: "w2p2", Msg: "note"},
	}
	b.Set(entries, th)
	b.Scroll(1, th) // seq 8, under the notify topic
	if e, ok := b.Selected(); !ok || e.Seq != 8 {
		t.Fatalf("selected = %+v ok %v, want seq 8", e, ok)
	}
	b.ToggleFilter(th)
	if e, ok := b.Selected(); !ok || e.Seq != 8 {
		t.Fatalf("selected after the toggle = %+v ok %v, want seq 8", e, ok)
	}
	if got, want := b.ListLen(), 1; got != want {
		t.Fatalf("rows = %d, want %d", got, want)
	}
	if e, ok := b.Selected(); !ok || e.Key != "notify/w1p1" {
		t.Fatalf("selected entry = %+v, want the notify row", e)
	}
}

// TestBoxViewFilteredWindowHoldsOnAHiddenArrival: the window offset is a row
// index in the filtered view, so an arrival the filter hides leaves the top
// entry, the offset and the arrival count alone.
func TestBoxViewFilteredWindowHoldsOnAHiddenArrival(t *testing.T) {
	th := theme.DefaultTheme()
	snap := &wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}
	b := NewBoxView(snap, th, keymap.LoadDefault())
	// 80 entries, alternating notify and lane, newest first.
	entries := make([]box.Entry, 0, 80)
	for seq := 80; seq >= 1; seq-- {
		key := "lane/a"
		if seq%2 == 0 {
			key = "notify/w1p1"
		}
		entries = append(entries, box.Entry{Seq: seq, Key: key, State: box.StateNote, Msg: "note"})
	}
	b.Set(entries, th)
	b.SetFilter(box.NotifyPrefix, th)
	b.Scroll(30, th) // push the window off the top row
	if b.list.View.Off == 0 {
		t.Fatalf("off = 0, want a scrolled-back window")
	}
	top := b.list.SeqAt(b.list.View.Off)
	if top == 0 {
		t.Fatal("the window top names no row")
	}
	// A lane entry arrives: the filter hides it, so nothing visible moved.
	next := append([]box.Entry{{Seq: 81, Key: "lane/a", State: box.StateNote, Msg: "hidden"}}, entries...)
	b.Set(next, th)
	if got := b.list.SeqAt(b.list.View.Off); got != top {
		t.Fatalf("top seq = %d after a hidden arrival, want %d", got, top)
	}
	if b.new != 0 || strings.Contains(b.title(), "+") {
		t.Fatalf("new = %d title = %q, want no arrival named", b.new, b.title())
	}
	// A notify entry arrives: it lands above the window, the top holds and the
	// arrival is counted once.
	shown := append([]box.Entry{{Seq: 82, Key: "notify/w1p1", State: box.StateNote, Msg: "shown"}}, next...)
	b.Set(shown, th)
	if got := b.list.SeqAt(b.list.View.Off); got != top {
		t.Fatalf("top seq = %d after a shown arrival, want %d", got, top)
	}
	if b.new != 1 {
		t.Fatalf("new = %d, want 1", b.new)
	}
	if got := b.title(); !strings.Contains(got, "+1") {
		t.Fatalf("title = %q, want the arrival count", got)
	}
}

// TestBoxViewEmptyNoteNamesTheFilter: a filter that hides every entry says so,
// because the log is not empty.
func TestBoxViewEmptyNoteNamesTheFilter(t *testing.T) {
	th := theme.DefaultTheme()
	b := NewBoxView(&wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}, th, keymap.LoadDefault())
	b.Set([]box.Entry{{Seq: 1, Key: "lane/a", State: box.StateNote, Msg: "note"}}, th)
	if got := b.emptyNote(); got != "no entries yet" {
		t.Fatalf("note = %q, want the empty log's", got)
	}
	b.SetFilter(box.NotifyPrefix, th)
	if got := b.emptyNote(); got != "no entries under "+box.NotifyPrefix {
		t.Fatalf("note = %q, want the filter named", got)
	}
	screen := boxScreen(t, b, 100, 30)
	r := b.Float.Rect
	if row := boxScreenRow(screen, r.Y, r.X, r.W); !strings.Contains(row, "no entries under notify/") {
		t.Fatalf("row = %q, want the filter named", row)
	}
}

// TestReaderRectFloor: a short list still gives the reader a full overlay, so
// a body is not hidden behind a one-row window.
func TestReaderRectFloor(t *testing.T) {
	t.Parallel()
	snap := &wire.Snapshot{Cols: 100, Rows: 30}
	short := BoxViewRect(snap, 1)
	r := ReaderRect(snap, 1)
	if r.H != ReaderMinRows {
		t.Fatalf("reader height = %d, want the floor %d", r.H, ReaderMinRows)
	}
	if r.W != short.W || r.X != short.X {
		t.Fatalf("reader rect = %+v, want the list's width and column %+v", r, short)
	}
	if r.Y+r.H > snap.Rows || r.Y < 0 {
		t.Fatalf("reader rect %+v leaves the screen", r)
	}
}

// TestReaderRectKeepsATallList: a list past the floor keeps its own height, so
// a long log reads whole.
func TestReaderRectKeepsATallList(t *testing.T) {
	t.Parallel()
	snap := &wire.Snapshot{Cols: 100, Rows: 40}
	r := ReaderRect(snap, 20)
	if r.H != 20 {
		t.Fatalf("reader height = %d, want the list's 20", r.H)
	}
}

// TestBoxHintDropsHintsOnANarrowFloat: a cramped border keeps move, body,
// clear, watch and close, and drops the wider hints whole.
func TestBoxHintDropsHintsOnANarrowFloat(t *testing.T) {
	t.Parallel()
	snap := &wire.Snapshot{Cols: 64, Rows: 30}
	b := NewBoxView(snap, theme.DefaultTheme(), keymap.LoadDefault())
	b.Set([]box.Entry{{Seq: 1, Key: "lane/a", Msg: "text"}}, theme.DefaultTheme())
	got := b.Float.Hint
	for _, want := range []string{"move", "body", "clear", "watch", "close"} {
		if !strings.Contains(got, want) {
			t.Fatalf("hint = %q, misses %q on a narrow float", got, want)
		}
	}
	if strings.Contains(got, "clear all") {
		t.Fatalf("hint = %q, want the widest hint dropped first", got)
	}
	if cells.Cells(got) > b.Float.Rect.W+2 {
		t.Fatalf("hint = %q is %d cells, the border holds %d", got, cells.Cells(got), b.Float.Rect.W+2)
	}
}
