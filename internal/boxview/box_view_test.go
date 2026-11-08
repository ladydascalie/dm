package boxview

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"dm/internal/box"
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

// TestBoxViewListsNewestFirst: the rows follow the entries newest first, the
// state word carries the rail's colour for that state, and the borders carry
// the title and the key hint.
func TestBoxViewListsNewestFirst(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 30, Rail: 34}
	b := NewBoxView(snap, theme.DefaultTheme())
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
	got := screen.Grid().Cell(r.X+boxReceiptW+boxSeqW+1, r.Y).Style.Fg
	if got.Kind != vt.ColorRGB || got.R != uint8(want.R) || got.G != uint8(want.G) || got.B != uint8(want.B) {
		t.Fatalf("done colour = %+v, want %v", got, want)
	}
}

// TestBoxViewScrollClamps: the window stops at the newest row and at the oldest
// row that still fills the pane.
func TestBoxViewScrollClamps(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}
	b := NewBoxView(snap, theme.DefaultTheme())
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
	if b.off != max {
		t.Fatalf("off = %d, want the oldest row %d", b.off, max)
	}
	b.Scroll(-100, theme.DefaultTheme())
	if b.off != 0 {
		t.Fatalf("off = %d, want the newest row", b.off)
	}
	// A log that shrinks under the window pulls the view back to the top.
	b.Scroll(max, theme.DefaultTheme())
	b.Set(entries[:2], theme.DefaultTheme())
	if b.off != 0 {
		t.Fatalf("off = %d after the log shrank, want 0", b.off)
	}
}

// TestBoxViewFitsLog: a short log makes a short box, a long one fills the cap,
// and the title names the rows in view once the log scrolls.
func TestBoxViewFitsLog(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}
	b := NewBoxView(snap, theme.DefaultTheme())
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
	b.Scroll(3, theme.DefaultTheme())
	if got := b.title(); !strings.Contains(got, "4-25") {
		t.Fatalf("title = %q, want the rows in view", got)
	}
}

// TestBoxViewTitleWatching: the title counts parked watches, omits the count
// when none is parked, and keeps the entry count.
func TestBoxViewTitleWatching(t *testing.T) {
	b := NewBoxView(&wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}, theme.DefaultTheme())
	b.Set(boxEntries(), theme.DefaultTheme())
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

// TestBoxViewReceipt: a wide view prefixes each row with the number of watches
// served that entry. A narrow view drops the marker whole.
func TestBoxViewReceipt(t *testing.T) {
	wide := NewBoxView(&wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}, theme.DefaultTheme())
	wide.watchers = receiptWatches()
	wide.Set(boxEntries(), theme.DefaultTheme())
	if wide.Float.Rect.W < boxReceiptMinW {
		t.Fatalf("wide rect = %d, want at least %d", wide.Float.Rect.W, boxReceiptMinW)
	}
	screen := boxScreen(t, wide, 100, 30)
	r := wide.Float.Rect
	if row := boxScreenRow(screen, r.Y, r.X, r.W); !strings.HasPrefix(row, " 1 ") || !strings.Contains(row, "gate green") {
		t.Fatalf("first row = %q, want one receipt", row)
	}
	if row := boxScreenRow(screen, r.Y+1, r.X, r.W); !strings.HasPrefix(row, " 2 ") {
		t.Fatalf("second row = %q, want two receipts", row)
	}

	narrow := NewBoxView(&wire.Snapshot{Cols: 50, Rows: 30, Rail: 0}, theme.DefaultTheme())
	narrow.watchers = receiptWatches()
	narrow.Set(boxEntries(), theme.DefaultTheme())
	if narrow.Float.Rect.W >= boxReceiptMinW {
		t.Fatalf("narrow rect = %d, want under %d", narrow.Float.Rect.W, boxReceiptMinW)
	}
	screen = boxScreen(t, narrow, 50, 30)
	r = narrow.Float.Rect
	want := boxRowText(boxEntries()[0], r.W, -1)
	if got := boxScreenRow(screen, r.Y, r.X, r.W); got != want {
		t.Fatalf("narrow row = %q, want no marker %q", got, want)
	}
}

// TestBoxViewReceiptBoundary: the marker is drawn at exactly boxReceiptMinW
// columns and dropped one column short. The rect is the screen width less the
// six cells BoxViewRect keeps clear.
func TestBoxViewReceiptBoundary(t *testing.T) {
	entries := boxEntries()[:1]
	for _, tc := range []struct {
		cols int
		want int
	}{
		{66, boxReceiptMinW},
		{65, boxReceiptMinW - 1},
	} {
		snap := &wire.Snapshot{Cols: tc.cols, Rows: 30, Rail: 0}
		b := NewBoxView(snap, theme.DefaultTheme())
		b.watchers = boxWatches()
		b.Set(entries, theme.DefaultTheme())
		if got := b.Float.Rect.W; got != tc.want {
			t.Fatalf("cols %d: rect = %d, want %d", tc.cols, got, tc.want)
		}
		screen := boxScreen(t, b, tc.cols, 30)
		r := b.Float.Rect
		row := boxScreenRow(screen, r.Y, r.X, r.W)
		if tc.want >= boxReceiptMinW {
			if !strings.HasPrefix(row, " 1 ") {
				t.Fatalf("cols %d: row = %q, want the receipt marker", tc.cols, row)
			}
			continue
		}
		if want := boxRowText(entries[0], r.W, -1); row != want {
			t.Fatalf("cols %d: row = %q, want no marker %q", tc.cols, row, want)
		}
	}
}

// TestBoxViewAnchor: a scrolled-back window keeps its top entry when the log
// grows and names what arrived above it. At the top the view follows the
// newest entry and the count clears.
func TestBoxViewAnchor(t *testing.T) {
	b := NewBoxView(&wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}, theme.DefaultTheme())
	entries := make([]box.Entry, 40)
	for i := range entries {
		entries[i] = box.Entry{Seq: 40 - i, State: box.StateNote, Msg: "note"}
	}
	b.Set(entries, theme.DefaultTheme())
	b.Scroll(5, theme.DefaultTheme())
	top := b.entries[b.off].Seq
	if top != 35 {
		t.Fatalf("top = %d, want 35", top)
	}

	// Two pushes, one entry each: the count must add up, not restart from the
	// current index.
	b.Set(append([]box.Entry{{Seq: 41, State: box.StateNote, Msg: "note"}}, entries...), theme.DefaultTheme())
	if got := b.entries[b.off].Seq; got != top {
		t.Fatalf("top seq = %d after one entry, want %d", got, top)
	}
	if b.new != 1 {
		t.Fatalf("new = %d after one entry, want 1", b.new)
	}
	b.Set(append([]box.Entry{{Seq: 42, State: box.StateNote, Msg: "note"}}, b.entries...), theme.DefaultTheme())
	if got := b.entries[b.off].Seq; got != top {
		t.Fatalf("top seq = %d after two entries, want %d", got, top)
	}
	if b.new != 2 {
		t.Fatalf("new = %d after two entries, want 2", b.new)
	}
	if got := b.title(); !strings.Contains(got, "+2") {
		t.Fatalf("title = %q, want the arrival count", got)
	}

	b.Scroll(-b.off, theme.DefaultTheme())
	b.Set(append([]box.Entry{{Seq: 44, State: box.StateNote, Msg: "note"}}, b.entries...), theme.DefaultTheme())
	if b.off != 0 || b.new != 0 || strings.Contains(b.title(), "+") {
		t.Fatalf("off = %d, new = %d, title = %q, want the newest entry", b.off, b.new, b.title())
	}
}

// TestBoxViewWatcherPageEmptyLog: the contract makes a parked wait appear
// before the first entry. The float sizes from the watches, so the page shows
// every watch, and the page has its own offset.
func TestBoxViewWatcherPageEmptyLog(t *testing.T) {
	few := append(boxWatches(), box.Watch{Pane: "w1p3", Key: "lane/b", Cursor: 4})
	b := NewBoxView(&wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}, theme.DefaultTheme())
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
	if b.pOff != 0 || b.off != 0 {
		t.Fatalf("offsets = %d/%d, want both at the first row", b.pOff, b.off)
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
	b = NewBoxView(&wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}, theme.DefaultTheme())
	b.Set(boxEntries(), theme.DefaultTheme())
	b.SetWatchers(many, theme.DefaultTheme())
	if b.Float.Rect.H != 22 {
		t.Fatalf("height = %d, want the cap of 22 rows", b.Float.Rect.H)
	}
	b.TogglePage(theme.DefaultTheme())
	b.Scroll(100, theme.DefaultTheme())
	if got := b.pOff; got != len(many)-b.Float.Rect.H {
		t.Fatalf("page offset = %d, want the last page %d", got, len(many)-b.Float.Rect.H)
	}
	if b.off != 0 {
		t.Fatalf("log offset = %d, want the page to scroll alone", b.off)
	}
	screen = boxScreen(t, b, 100, 30)
	r = b.Float.Rect
	last := boxScreenRow(screen, r.Y+r.H-1, r.X, r.W)
	if !strings.Contains(last, many[len(many)-1].Pane) {
		t.Fatalf("last row = %q, want the oldest watch %s", last, many[len(many)-1].Pane)
	}
	b.Scroll(-100, theme.DefaultTheme())
	if b.pOff != 0 {
		t.Fatalf("page offset = %d after scrolling back, want the first watch", b.pOff)
	}
}
