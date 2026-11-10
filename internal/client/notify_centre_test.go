package client

// The notification centre: N summons it, the rows list the notify/ entries
// newest first with their unread marks, enter opens the reader, g jumps to
// the pane and m hands the entry to the box view.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"dm/internal/box"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"
)

// centreEntries is a notify/ log newest first: the newest entry is about the
// focused pane, an older one about another pane, and one free post under the
// bare topic.
func centreEntries() []box.Entry {
	at := time.Now()
	return []box.Entry{
		{Seq: 3, At: at, Key: "notify/w1p1", State: box.StateBlocked,
			From: "w1p1", Msg: "waiting on tests"},
		{Seq: 2, At: at, Key: "notify/w1p2", State: box.StateDone,
			From: "w1p2", Msg: "gate green"},
		{Seq: 1, At: at, Key: "notify/", State: box.StateNote,
			Msg: "free post"},
	}
}

// centreReply is the resp a centre read expects: the entries under the
// request id the client sent.
func centreReply(t *testing.T, id int, entries []box.Entry) wire.Msg {
	t.Helper()
	res, err := json.Marshal(box.ListReply{Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	return wire.Msg{Type: "resp", Action: "msg-list", ID: id, OK: true, Res: res}
}

// centreScreen replays the centre's bytes into a fresh screen, so a test
// reads what a terminal would show.
func centreScreen(t *testing.T, tui *tui) *vt.Term {
	t.Helper()
	var buf bytes.Buffer
	tui.ncentre.Float.Paint = floatVTPaint(tui.ncentre.Float.VT, nil)
	tui.ncentre.Float.Draw(&buf, theme.DefaultTheme())
	screen := vt.NewTerm(tui.snap.Cols, tui.snap.Rows)
	screen.Write(buf.Bytes())
	screen.Flush()
	return screen
}

// openCentre summons the centre and fills it with one reply.
func openCentre(t *testing.T, tui *tui, got chan wire.Msg, entries []box.Entry) {
	t.Helper()
	feed(tui, "\x02N")
	if tui.ncentre == nil || tui.mode != "notif" {
		t.Fatalf("N opened mode %q centre %v, want the centre", tui.mode, tui.ncentre)
	}
	m := nextBoxMsg(t, got)
	if m.Action != "msg-list" || m.ID != 1 {
		t.Fatalf("centre request = %+v, want an id'd msg-list", m)
	}
	var req box.ListReq
	if err := json.Unmarshal(m.Res, &req); err != nil {
		t.Fatalf("centre request body: %v", err)
	}
	if req.KeyPrefix != box.NotifyPrefix || req.Limit != 50 {
		t.Fatalf("centre request = %+v, want the notify branch capped at 50", req)
	}
	tui.apply(centreReply(t, m.ID, entries))
}

// N opens the float and the reply lands newest first with the unread marks.
func TestCentreOpensOnN(t *testing.T) {
	tui, got := switcherTUI(t)
	openCentre(t, tui, got, centreEntries())
	if len(tui.floats) != 1 {
		t.Fatalf("floats = %d, want the centre", len(tui.floats))
	}
	if got := tui.ncentre.entries; len(got) != 3 || got[0].Seq != 3 || got[2].Seq != 1 {
		t.Fatalf("entries = %+v, want newest first", got)
	}
	if got := tui.ncentre.title(); !strings.Contains(got, "3 unread") {
		t.Fatalf("title = %q, want the unread count", got)
	}
	r := tui.ncentre.Float.Rect
	screen := centreScreen(t, tui)
	if row := boxScreenRow(screen, r.Y, r.X, r.W); !strings.Contains(row, "waiting on tests") {
		t.Fatalf("top row = %q, want the newest entry", row)
	}
	if row := boxScreenRow(screen, r.Y, r.X, r.W); !strings.Contains(row, "*") {
		t.Fatalf("top row = %q, want the unread mark", row)
	}
	if row := boxScreenRow(screen, r.Y+1, r.X, r.W); !strings.Contains(row, "*") {
		t.Fatalf("second row = %q, want the unread mark", row)
	}
}

// The marks clear as the operator opens entries, and a swept centre opens
// clean.
func TestCentreUnreadClearsOnOpen(t *testing.T) {
	tui, got := switcherTUI(t)
	openCentre(t, tui, got, centreEntries())
	// enter opens the newest entry's body and clears its mark.
	feed(tui, "\r")
	if tui.reader == nil || tui.reader.e.Seq != 3 {
		t.Fatalf("enter opened %+v, want the reader at seq 3", tui.reader)
	}
	if tui.notifyRead != 3 {
		t.Fatalf("read mark = %d, want 3", tui.notifyRead)
	}
	feed(tui, "\x1b") // back to the centre
	if tui.reader != nil {
		t.Fatal("esc left the reader up")
	}
	tui.ncentre.read = tui.notifyRead
	tui.ncentre.paint(theme.DefaultTheme())
	r := tui.ncentre.Float.Rect
	screen := centreScreen(t, tui)
	for row := 0; row < r.H; row++ {
		if line := boxScreenRow(screen, r.Y+row, r.X, r.W); strings.Contains(line, "*") {
			t.Fatalf("row %d = %q, want every mark cleared by the open", row, line)
		}
	}
	// Closing the centre sweeps what the operator saw; the next open is clean.
	feed(tui, "q")
	if tui.ncentre != nil || tui.mode != "" {
		t.Fatal("q left the centre up")
	}
	feed(tui, "\x02N")
	m := nextBoxMsg(t, got)
	if m.Action != "msg-list" || m.ID == 0 {
		t.Fatalf("centre request = %+v, want an id'd msg-list", m)
	}
	tui.apply(centreReply(t, m.ID, centreEntries()))
	tui.ncentre.read = tui.notifyRead
	tui.ncentre.paint(theme.DefaultTheme())
	if got := tui.ncentre.title(); strings.Contains(got, "unread") {
		t.Fatalf("title = %q, want no unread after the sweep", got)
	}
}

// g focuses the pane the entry is about and closes the centre.
func TestCentreJumpPane(t *testing.T) {
	tui, got := switcherTUI(t)
	openCentre(t, tui, got, centreEntries())
	feed(tui, "g")
	if tui.ncentre != nil || tui.mode != "" {
		t.Fatal("g left the centre up")
	}
	m := nextBoxMsg(t, got)
	if m.Action != "focus" || m.Target != "w1p1" {
		t.Fatalf("jump sent %+v, want focus for w1p1", m)
	}
}

// A topic that names no pane or an unknown pane does not jump.
func TestCentreJumpPaneRefuses(t *testing.T) {
	tui, got := switcherTUI(t)
	openCentre(t, tui, got, centreEntries())
	feed(tui, "G") // the free post under the bare topic names no pane
	feed(tui, "g")
	if tui.ncentre == nil {
		t.Fatal("g on a paneless entry closed the centre")
	}
	select {
	case m := <-got:
		if m.Action == "focus" {
			t.Fatalf("jump sent %+v, want nothing", m)
		}
	default:
	}
}

// m hands the entry to the box view, which lands with it at the top.
func TestCentreToBox(t *testing.T) {
	tui, got := switcherTUI(t)
	openCentre(t, tui, got, centreEntries())
	feed(tui, "m")
	if tui.ncentre != nil || tui.box == nil || tui.mode != "box" {
		t.Fatalf("m left centre %v box %v mode %q", tui.ncentre, tui.box, tui.mode)
	}
	nextBoxMsg(t, got) // the log the open asked for
	nextBoxMsg(t, got) // the watcher list the open asked for
	// A log longer than the float, so the window has somewhere to go: the
	// notify entry sits at row 20 of 25, and the clamp holds the window at
	// the oldest row that still fills the pane.
	entries := make([]box.Entry, 0, 25)
	for seq := 25; seq >= 1; seq-- {
		e := box.Entry{Seq: seq, Key: "lane/a", State: box.StateWorking, Msg: "filler"}
		if seq == 5 {
			e = box.Entry{Seq: seq, Key: "notify/w1p1", State: box.StateBlocked,
				From: "w1p1", Msg: "jump target"}
		}
		entries = append(entries, e)
	}
	res, err := json.Marshal(box.ListReply{Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	tui.apply(wire.Msg{Type: "resp", Action: "msg-list", OK: true, Res: res})
	h := tui.box.Float.Rect.H
	wantTop := 20
	if wantTop > len(entries)-h {
		wantTop = len(entries) - h
	}
	if tui.boxTop != wantTop {
		t.Fatalf("box top = %d, want the clamped row %d", tui.boxTop, wantTop)
	}
	screen := boxScreen(t, tui.box, tui.snap.Cols, tui.snap.Rows)
	r := tui.box.Float.Rect
	if row := boxScreenRow(screen, r.Y+20-tui.boxTop, r.X, r.W); !strings.Contains(row, "jump target") {
		t.Fatalf("entry row = %q, want the handed-over entry in the window", row)
	}
}

// esc, q and N close the centre and hand the keyboard back.
func TestCentreCloseKeys(t *testing.T) {
	for _, key := range []string{"\x1b", "q", "N"} {
		tui, got := switcherTUI(t)
		openCentre(t, tui, got, centreEntries())
		feed(tui, key)
		if tui.ncentre != nil || tui.mode != "" || len(tui.floats) != 0 {
			t.Fatalf("%q left the centre up", key)
		}
	}
}
