package client

// The body reader: the list page opens one entry's body in a scrolling
// overlay, the entry renders the body below the summary line, and an entry
// without a body offers no reader.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"dm/internal/box"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"
)

// bodyEntries is a log whose newest entry carries a body and whose second
// entry does not.
func bodyEntries() []box.Entry {
	return []box.Entry{
		{Seq: 9, Key: "lane/a", State: box.StateDone, From: "w3p1", Msg: "short summary",
			Body: "first body line\nsecond body line\nthird\nfourth\nfifth\n"},
		{Seq: 8, Key: "lane/a", State: box.StateWorking, From: "w3p1", Msg: "plain entry"},
		{Seq: 7, Key: "lane/b", State: box.StateNote, From: "w2p1", Msg: "note"},
	}
}

// openBoxWith opens the float and fills it with one list reply.
func openBoxWith(t *testing.T, entries []box.Entry) *tui {
	t.Helper()
	tui, _ := switcherTUI(t)
	feed(tui, "\x02m")
	res, err := json.Marshal(box.ListReply{Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	tui.apply(wire.Msg{Type: "resp", Action: "msg-list", OK: true, Res: res})
	return tui
}

// readerScreen replays the reader's bytes into a fresh screen, so a test
// reads what a terminal would show.
func readerScreen(t *testing.T, r *bodyReader, cols, rows int) *vt.Term {
	t.Helper()
	var buf bytes.Buffer
	r.Float.Paint = floatVTPaint(r.Float.VT, nil)
	r.Float.Draw(&buf, theme.DefaultTheme())
	screen := vt.NewTerm(cols, rows)
	screen.Write(buf.Bytes())
	screen.Flush()
	return screen
}

// v opens the reader on the entry at the top of the window, and the overlay
// carries the body below the summary line.
func TestBodyReaderOpensOnV(t *testing.T) {
	tui := openBoxWith(t, bodyEntries())
	if !strings.Contains(tui.box.Float.Hint, "v body") {
		t.Fatalf("hint = %q, want the affordance", tui.box.Float.Hint)
	}
	feed(tui, "v")
	if tui.reader == nil {
		t.Fatal("v did not open the reader")
	}
	if got := len(tui.floats); got != 2 {
		t.Fatalf("floats = %d, want the box and the reader", got)
	}
	if tui.reader.e.Seq != 9 {
		t.Fatalf("reader entry seq = %d, want 9", tui.reader.e.Seq)
	}
	if got := tui.reader.lines; len(got) != 6 || got[0] != "short summary" ||
		got[1] != "first body line" || got[5] != "fifth" {
		t.Fatalf("reader lines = %q, want the summary with the body below", got)
	}
	if tui.reader.off != 0 {
		t.Fatalf("reader offset = %d, want the top", tui.reader.off)
	}
}

// The painted overlay shows the summary line and the body below it.
func TestBodyReaderPaintsBodyBelowTheLine(t *testing.T) {
	tui := openBoxWith(t, bodyEntries())
	feed(tui, "v")
	r := tui.reader.Float.Rect
	screen := readerScreen(t, tui.reader, tui.snap.Cols, tui.snap.Rows)
	if row := boxScreenRow(screen, r.Y, r.X, r.W); !strings.Contains(row, "short summary") {
		t.Fatalf("first content row = %q, want the summary line", row)
	}
	if row := boxScreenRow(screen, r.Y+1, r.X, r.W); !strings.Contains(row, "first body line") {
		t.Fatalf("second content row = %q, want the first body line", row)
	}
}

// The reader scrolls and the list page keeps the reader's place separate.
func TestBodyReaderScrolls(t *testing.T) {
	tui := openBoxWith(t, bodyEntries())
	feed(tui, "v")
	feed(tui, "j")
	if tui.reader.off != 1 {
		t.Fatalf("offset = %d, want 1", tui.reader.off)
	}
	feed(tui, "G")
	if want := len(tui.reader.lines) - tui.reader.Float.Rect.H; tui.reader.off != want || want == 0 {
		t.Fatalf("offset = %d, want the bottom at %d", tui.reader.off, want)
	}
	feed(tui, "g")
	if tui.reader.off != 0 {
		t.Fatalf("offset = %d, want the top", tui.reader.off)
	}
	// The list window did not move while the reader scrolled.
	if tui.boxTop != 0 {
		t.Fatalf("list top = %d, want 0", tui.boxTop)
	}
	// esc hands the keyboard back to the list page.
	feed(tui, "\x1b")
	if tui.reader != nil || len(tui.floats) != 1 {
		t.Fatalf("esc left the reader up: %v", tui.reader)
	}
}

// A repeat inside the window and the close keys put the list page back.
func TestBodyReaderClosesOnQ(t *testing.T) {
	tui := openBoxWith(t, bodyEntries())
	feed(tui, "v")
	feed(tui, "q")
	if tui.reader != nil || len(tui.floats) != 1 || tui.box == nil {
		t.Fatalf("q did not close only the reader")
	}
}

// An entry without a body shows no reader affordance and v opens nothing.
func TestBodylessEntryHasNoAffordance(t *testing.T) {
	entries := []box.Entry{
		{Seq: 10, Key: "lane/a", State: box.StateDone, From: "w3p1", Msg: "plain"},
		{Seq: 9, Key: "lane/a", State: box.StateDone, From: "w3p1", Msg: "with body", Body: "text"},
	}
	tui := openBoxWith(t, entries)
	if strings.Contains(tui.box.Float.Hint, "v body") {
		t.Fatalf("hint = %q, want no reader affordance", tui.box.Float.Hint)
	}
	feed(tui, "v")
	if tui.reader != nil {
		t.Fatal("v opened a reader for an entry without a body")
	}
}

// The reader follows a resize: a snapshot refits the float and the reader
// rides the same rect.
func TestBodyReaderFollowsResize(t *testing.T) {
	tui := openBoxWith(t, bodyEntries())
	feed(tui, "v")
	big := *tui.snap
	big.Cols += 20
	big.Rows += 10
	tui.apply(wire.Msg{Type: "snapshot", Snap: &big})
	if tui.reader == nil {
		t.Fatal("the snapshot closed the reader")
	}
	if tui.reader.Float.Rect != tui.box.Float.Rect {
		t.Fatalf("reader rect = %+v, want the float's %+v", tui.reader.Float.Rect, tui.box.Float.Rect)
	}
}
