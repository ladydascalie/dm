package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hinshun/vt10x"
)

// boxEntries is a log with one of four states, so a row test covers the column
// layout and the state colours.
func boxEntries() []entry {
	return []entry{
		{Seq: 9, Key: "lane/a", State: stateDone, From: "w3p1", Msg: "gate green"},
		{Seq: 8, Key: "lane/a", State: stateWorking, From: "w3p1", Msg: "running tests"},
		{Seq: 7, Key: "lane/b", State: stateBlocked, From: "w2p2", Msg: "waiting on review"},
		{Seq: 6, Key: "lane/c", State: stateError, From: "w1p1", Msg: "build failed"},
	}
}

// boxScreen replays the float's bytes into a fresh screen, so a test reads what
// a terminal would show.
func boxScreen(t *testing.T, b *boxView, cols, rows int) vt10x.Terminal {
	t.Helper()
	var buf bytes.Buffer
	b.float.Draw(&buf)
	screen := vt10x.New(vt10x.WithSize(cols, rows))
	if _, err := screen.Write(buf.Bytes()); err != nil {
		t.Fatalf("replay the box: %v", err)
	}
	return screen
}

// boxScreenRow reads one row of a replayed screen, trailing blanks dropped.
func boxScreenRow(screen vt10x.Terminal, y, x, w int) string {
	var b strings.Builder
	for i := 0; i < w; i++ {
		r := switcherCell(screen, x+i, y)
		if r == 0 {
			r = ' '
		}
		b.WriteRune(r)
	}
	return strings.TrimRight(b.String(), " ")
}

// nextBoxMsg takes the next request the client sends, failing on a timeout.
func nextBoxMsg(t *testing.T, got chan Msg) Msg {
	t.Helper()
	select {
	case m := <-got:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("no request")
	}
	return Msg{}
}

// TestBoxViewListsNewestFirst: the rows follow the entries newest first, the
// state word carries the rail's colour for that state, and the borders carry
// the title and the key hint.
func TestBoxViewListsNewestFirst(t *testing.T) {
	snap := &Snapshot{Cols: 100, Rows: 30, Rail: 34}
	b := newBoxView(snap)
	b.set(boxEntries())
	screen := boxScreen(t, b, snap.Cols, snap.Rows)
	r := b.float.Rect

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

	// The state word carries the rail's colour for that state. vt10x stores a
	// true colour as r<<16|g<<8|b.
	want := boxStateColour(stateDone)
	screen.Lock()
	got := screen.Cell(r.X+boxReceiptW+boxSeqW+1, r.Y).FG
	screen.Unlock()
	if got != vt10x.Color(want.r<<16|want.g<<8|want.b) {
		t.Fatalf("done colour = %v, want %v", got, want)
	}
}

// TestBoxViewScrollClamps: the window stops at the newest row and at the oldest
// row that still fills the pane.
func TestBoxViewScrollClamps(t *testing.T) {
	snap := &Snapshot{Cols: 100, Rows: 30, Rail: 0}
	b := newBoxView(snap)
	entries := make([]entry, 0, 40)
	for i := 0; i < 40; i++ {
		entries = append(entries, entry{Seq: i + 1, State: stateNote, Msg: "note"})
	}
	b.set(entries)
	max := len(entries) - b.float.Rect.H
	if max <= 0 {
		t.Fatalf("fixture is too short to scroll: %d entries in %d rows", len(entries), b.float.Rect.H)
	}
	b.scroll(100)
	if b.off != max {
		t.Fatalf("off = %d, want the oldest row %d", b.off, max)
	}
	b.scroll(-100)
	if b.off != 0 {
		t.Fatalf("off = %d, want the newest row", b.off)
	}
	// A log that shrinks under the window pulls the view back to the top.
	b.scroll(max)
	b.set(entries[:2])
	if b.off != 0 {
		t.Fatalf("off = %d after the log shrank, want 0", b.off)
	}
}

// TestBoxViewFitsLog: a short log makes a short box, a long one fills the cap,
// and the title names the rows in view once the log scrolls.
func TestBoxViewFitsLog(t *testing.T) {
	snap := &Snapshot{Cols: 100, Rows: 30, Rail: 0}
	b := newBoxView(snap)
	b.set(boxEntries())
	if got, want := b.float.Rect.H, len(boxEntries()); got != want {
		t.Fatalf("height = %d, want the log length %d", got, want)
	}
	entries := make([]entry, 60)
	for i := range entries {
		entries[i] = entry{Seq: i + 1, State: stateNote, Msg: "note"}
	}
	b.set(entries)
	if b.float.Rect.H != 22 {
		t.Fatalf("height = %d, want the cap of 22 rows", b.float.Rect.H)
	}
	b.scroll(3)
	if got := b.title(); !strings.Contains(got, "4-25") {
		t.Fatalf("title = %q, want the rows in view", got)
	}
}

// TestBoxPrefixOpensView: prefix m opens the float and asks for the whole log;
// q closes it and the float stack empties.
func TestBoxPrefixOpensView(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02m")
	if tui.box == nil || tui.mode != "box" || len(tui.floats) != 1 {
		t.Fatalf("box = %v, mode = %q, floats = %d", tui.box, tui.mode, len(tui.floats))
	}
	m := nextBoxMsg(t, got)
	if m.Type != "req" || m.Action != "msg-list" {
		t.Fatalf("open request = %+v, want a msg-list request", m)
	}
	var req listReq
	if err := json.Unmarshal(m.Res, &req); err != nil {
		t.Fatalf("list request: %v", err)
	}
	if req.Key != "" || req.Limit != 0 {
		t.Fatalf("list request = %+v, want the whole log", req)
	}
	feed(tui, "q")
	if tui.box != nil || tui.mode != "" || len(tui.floats) != 0 {
		t.Fatalf("after q: box = %v, mode = %q, floats = %d", tui.box, tui.mode, len(tui.floats))
	}
}

// TestBoxListReplyFillsView: the reply lands in the float, newest first.
func TestBoxListReplyFillsView(t *testing.T) {
	tui, _ := switcherTUI(t)
	feed(tui, "\x02m")
	res, err := json.Marshal(listReply{Entries: boxEntries()})
	if err != nil {
		t.Fatal(err)
	}
	tui.apply(Msg{Type: "resp", Action: "msg-list", OK: true, Res: res})
	if len(tui.box.entries) != 4 {
		t.Fatalf("entries = %d, want 4", len(tui.box.entries))
	}
	screen := boxScreen(t, tui.box, tui.snap.Cols, tui.snap.Rows)
	r := tui.box.float.Rect
	if row := boxScreenRow(screen, r.Y, r.X, r.W); !strings.Contains(row, "gate green") {
		t.Fatalf("top row = %q, want the newest entry", row)
	}
}

// TestBoxPushRefreshesOpenView: a post or a clear asks an open view for the log
// again, and a closed view asks for nothing.
func TestBoxPushRefreshesOpenView(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02m")
	nextBoxMsg(t, got) // the log the open asked for
	nextBoxMsg(t, got) // the watcher list the open asked for
	tui.apply(Msg{Type: "box"})
	if m := nextBoxMsg(t, got); m.Action != "msg-list" {
		t.Fatalf("push asked for %q, want msg-list", m.Action)
	}
	feed(tui, "q")
	tui.apply(Msg{Type: "box"})
	select {
	case m := <-got:
		t.Fatalf("a closed view sent %q", m.Action)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestBoxPostPushesAndEchoes: a post pushes one box frame to every attached
// client and the reply names the action it answers.
func TestBoxPostPushesAndEchoes(t *testing.T) {
	s := boxServer(t)
	cl, conn := boxConn(t)
	s.mu.Lock()
	s.clients[cl] = true
	s.mu.Unlock()
	t.Cleanup(func() {
		s.mu.Lock()
		delete(s.clients, cl)
		s.mu.Unlock()
		cl.shutdown()
	})

	s.handleReq(cl, Msg{Type: "req", Action: "msg-post",
		Res: json.RawMessage(`{"key":"lane/a","state":"done","msg":"gate green"}`)})
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	push, err := readMsg(conn)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if push.Type != "box" {
		t.Fatalf("first frame = %q, want the box push", push.Type)
	}
	reply, err := readMsg(conn)
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if reply.Type != "resp" || !reply.OK {
		t.Fatalf("reply = %+v, want a good resp", reply)
	}
	if reply.Action != "msg-post" {
		t.Fatalf("reply action = %q, want msg-post", reply.Action)
	}
}

// boxWatches is a presence list in the server's order: pane, then key, then
// branch. The first watch is parked, so the title and the page have a wait to
// show, and the second trails one seq behind the newest entry.
func boxWatches() []watch {
	at := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	return []watch{
		{Pane: "w1p1", Key: "lane/a", Cursor: 9, Parked: true, At: at},
		{Pane: "w1p2", KeyPrefix: "lane/", Cursor: 8, At: at},
	}
}

// TestBoxViewRequestsWatchers: the open asks for the log and the watcher list.
// A watch push inside the 100 ms window is held back; once the window passes
// the push asks again, and a closed view asks for nothing.
func TestBoxViewRequestsWatchers(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02m")
	if m := nextBoxMsg(t, got); m.Action != "msg-list" {
		t.Fatalf("open asked for %q, want msg-list", m.Action)
	}
	ask := nextBoxMsg(t, got)
	if ask.Action != "msg-watchers" {
		t.Fatalf("open asked for %q, want msg-watchers", ask.Action)
	}
	var req watchersReq
	if err := json.Unmarshal(ask.Res, &req); err != nil {
		t.Fatalf("watchers request: %v", err)
	}

	tui.apply(Msg{Type: "watch"})
	noMsg(t, got)
	if !tui.box.watchWant {
		t.Fatal("a held-back ask is not marked for the ticker")
	}

	tui.box.watchAt = time.Now().Add(-boxWatchInterval)
	tui.apply(Msg{Type: "watch"})
	if m := nextBoxMsg(t, got); m.Action != "msg-watchers" {
		t.Fatalf("push asked for %q, want msg-watchers", m.Action)
	}

	feed(tui, "q")
	tui.apply(Msg{Type: "watch"})
	noMsg(t, got)
}

// TestBoxViewWatchFlush: the ticker sends the ask that the window held back.
func TestBoxViewWatchFlush(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02m")
	nextBoxMsg(t, got)
	nextBoxMsg(t, got)
	tui.apply(Msg{Type: "watch"})
	noMsg(t, got)
	tui.box.watchAt = time.Now().Add(-boxWatchInterval)
	tui.flushBoxWatchers(time.Now())
	if m := nextBoxMsg(t, got); m.Action != "msg-watchers" {
		t.Fatalf("flush asked for %q, want msg-watchers", m.Action)
	}
}

// TestBoxViewWatcherPage: a watch push asks for the list, the reply fills the
// page, and w swaps the log for the page and back. The page rows carry pane,
// filter, cursor and the wait mark, and the hint names the key.
func TestBoxViewWatcherPage(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02m")
	nextBoxMsg(t, got) // msg-list
	nextBoxMsg(t, got) // msg-watchers
	list, err := json.Marshal(listReply{Entries: boxEntries()})
	if err != nil {
		t.Fatal(err)
	}
	tui.apply(Msg{Type: "resp", Action: "msg-list", OK: true, Res: list})

	tui.box.watchAt = time.Now().Add(-boxWatchInterval)
	tui.apply(Msg{Type: "watch"})
	if m := nextBoxMsg(t, got); m.Action != "msg-watchers" {
		t.Fatalf("push asked for %q, want msg-watchers", m.Action)
	}
	res, err := json.Marshal(watchersReply{Watchers: boxWatches()})
	if err != nil {
		t.Fatal(err)
	}
	tui.apply(Msg{Type: "resp", Action: "msg-watchers", OK: true, Res: res})

	feed(tui, "w")
	r := tui.box.float.Rect
	screen := boxScreen(t, tui.box, tui.snap.Cols, tui.snap.Rows)
	first := boxScreenRow(screen, r.Y, r.X, r.W)
	for _, want := range []string{"w1p1", "lane/a", "@9", "wait"} {
		if !strings.Contains(first, want) {
			t.Fatalf("page row %q misses %q", first, want)
		}
	}
	second := boxScreenRow(screen, r.Y+1, r.X, r.W)
	for _, want := range []string{"w1p2", "lane/*", "@8"} {
		if !strings.Contains(second, want) {
			t.Fatalf("page row %q misses %q", second, want)
		}
	}
	if strings.Contains(second, "wait") {
		t.Fatalf("page row %q carries a wait mark on an unparked watch", second)
	}
	if hint := boxScreenRow(screen, r.Y+r.H, r.X+2, r.W-2); !strings.Contains(hint, "w log") {
		t.Fatalf("hint = %q, want the log key", hint)
	}

	feed(tui, "w")
	screen = boxScreen(t, tui.box, tui.snap.Cols, tui.snap.Rows)
	if row := boxScreenRow(screen, r.Y, r.X, r.W); !strings.Contains(row, "gate green") {
		t.Fatalf("row after w = %q, want the log again", row)
	}
	if hint := boxScreenRow(screen, r.Y+r.H, r.X+2, r.W-2); !strings.Contains(hint, "w watchers") {
		t.Fatalf("hint = %q, want the page key", hint)
	}
}

// TestBoxViewTitleWatching: the title counts parked watches, omits the count
// when none is parked, and keeps the entry count.
func TestBoxViewTitleWatching(t *testing.T) {
	b := newBoxView(&Snapshot{Cols: 100, Rows: 30, Rail: 0})
	b.set(boxEntries())
	b.watchers = boxWatches()
	b.paint()
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
	wide := newBoxView(&Snapshot{Cols: 100, Rows: 30, Rail: 0})
	wide.watchers = boxWatches()
	wide.set(boxEntries())
	if wide.float.Rect.W < boxReceiptMinW {
		t.Fatalf("wide rect = %d, want at least %d", wide.float.Rect.W, boxReceiptMinW)
	}
	screen := boxScreen(t, wide, 100, 30)
	r := wide.float.Rect
	if row := boxScreenRow(screen, r.Y, r.X, r.W); !strings.HasPrefix(row, " 1 ") || !strings.Contains(row, "gate green") {
		t.Fatalf("first row = %q, want one receipt", row)
	}
	if row := boxScreenRow(screen, r.Y+1, r.X, r.W); !strings.HasPrefix(row, " 2 ") {
		t.Fatalf("second row = %q, want two receipts", row)
	}

	narrow := newBoxView(&Snapshot{Cols: 50, Rows: 30, Rail: 0})
	narrow.watchers = boxWatches()
	narrow.set(boxEntries())
	if narrow.float.Rect.W >= boxReceiptMinW {
		t.Fatalf("narrow rect = %d, want under %d", narrow.float.Rect.W, boxReceiptMinW)
	}
	screen = boxScreen(t, narrow, 50, 30)
	r = narrow.float.Rect
	want := boxRowText(boxEntries()[0], r.W, -1)
	if got := boxScreenRow(screen, r.Y, r.X, r.W); got != want {
		t.Fatalf("narrow row = %q, want no marker %q", got, want)
	}
}

// TestBoxViewAnchor: a scrolled-back window keeps its top entry when the log
// grows and names what arrived above it. At the top the view follows the
// newest entry and the count clears.
func TestBoxViewAnchor(t *testing.T) {
	b := newBoxView(&Snapshot{Cols: 100, Rows: 30, Rail: 0})
	entries := make([]entry, 40)
	for i := range entries {
		entries[i] = entry{Seq: 40 - i, State: stateNote, Msg: "note"}
	}
	b.set(entries)
	b.scroll(5)
	top := b.entries[b.off].Seq
	if top != 35 {
		t.Fatalf("top = %d, want 35", top)
	}

	newer := []entry{
		{Seq: 43, State: stateNote, Msg: "note"},
		{Seq: 42, State: stateNote, Msg: "note"},
		{Seq: 41, State: stateNote, Msg: "note"},
	}
	b.set(append(newer, entries...))
	if got := b.entries[b.off].Seq; got != top {
		t.Fatalf("top seq = %d after entries arrived, want %d", got, top)
	}
	if b.new != 3 {
		t.Fatalf("new = %d, want 3", b.new)
	}
	if got := b.title(); !strings.Contains(got, "+3") {
		t.Fatalf("title = %q, want the arrival count", got)
	}

	b.scroll(-b.off)
	b.set(append([]entry{{Seq: 44, State: stateNote, Msg: "note"}}, b.entries...))
	if b.off != 0 || b.new != 0 || strings.Contains(b.title(), "+") {
		t.Fatalf("off = %d, new = %d, title = %q, want the newest entry", b.off, b.new, b.title())
	}
}
