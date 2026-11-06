package main

// The client side of the message box float: the tests that drive it through a
// tui. The view's own tests live in internal/boxview.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"dm/internal/box"
	"dm/internal/boxview"
	"dm/internal/theme"
	"dm/internal/wire"

	"github.com/hinshun/vt10x"
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

// boxScreen replays the float's bytes into a fresh screen, so a test reads what
// a terminal would show.
func boxScreen(t *testing.T, b *boxview.BoxView, cols, rows int) vt10x.Terminal {
	t.Helper()
	var buf bytes.Buffer
	b.Float.Paint = floatVTPaint(b.Float.VT, nil)
	b.Float.Draw(&buf, theme.DefaultTheme())
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
func nextBoxMsg(t *testing.T, got chan wire.Msg) wire.Msg {
	t.Helper()
	select {
	case m := <-got:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("no request")
	}
	return wire.Msg{}
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
	var req box.ListReq
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
	res, err := json.Marshal(box.ListReply{Entries: boxEntries()})
	if err != nil {
		t.Fatal(err)
	}
	tui.apply(wire.Msg{Type: "resp", Action: "msg-list", OK: true, Res: res})
	screen := boxScreen(t, tui.box, tui.snap.Cols, tui.snap.Rows)
	r := tui.box.Float.Rect
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
	tui.apply(wire.Msg{Type: "box"})
	if m := nextBoxMsg(t, got); m.Action != "msg-list" {
		t.Fatalf("push asked for %q, want msg-list", m.Action)
	}
	feed(tui, "q")
	tui.apply(wire.Msg{Type: "box"})
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

	s.handleReq(cl, wire.Msg{
		Type: "req", Action: "msg-post",
		Res: json.RawMessage(`{"key":"lane/a","state":"done","msg":"gate green"}`),
	})
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	push, err := wire.ReadMsg(conn)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if push.Type != "box" {
		t.Fatalf("first frame = %q, want the box push", push.Type)
	}
	reply, err := wire.ReadMsg(conn)
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
	var req box.WatchersReq
	if err := json.Unmarshal(ask.Res, &req); err != nil {
		t.Fatalf("watchers request: %v", err)
	}

	tui.apply(wire.Msg{Type: "watch"})
	noMsg(t, got)
	if !tui.box.WatchWant {
		t.Fatal("a held-back ask is not marked for the ticker")
	}

	tui.box.WatchAt = time.Now().Add(-boxview.WatchInterval)
	tui.apply(wire.Msg{Type: "watch"})
	if m := nextBoxMsg(t, got); m.Action != "msg-watchers" {
		t.Fatalf("push asked for %q, want msg-watchers", m.Action)
	}

	feed(tui, "q")
	tui.apply(wire.Msg{Type: "watch"})
	noMsg(t, got)
}

// TestBoxViewWatchFlush: the ticker sends the ask that the window held back.
func TestBoxViewWatchFlush(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02m")
	nextBoxMsg(t, got)
	nextBoxMsg(t, got)
	tui.apply(wire.Msg{Type: "watch"})
	noMsg(t, got)
	tui.box.WatchAt = time.Now().Add(-boxview.WatchInterval)
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
	list, err := json.Marshal(box.ListReply{Entries: boxEntries()})
	if err != nil {
		t.Fatal(err)
	}
	tui.apply(wire.Msg{Type: "resp", Action: "msg-list", OK: true, Res: list})

	tui.box.WatchAt = time.Now().Add(-boxview.WatchInterval)
	tui.apply(wire.Msg{Type: "watch"})
	if m := nextBoxMsg(t, got); m.Action != "msg-watchers" {
		t.Fatalf("push asked for %q, want msg-watchers", m.Action)
	}
	res, err := json.Marshal(box.WatchersReply{Watchers: boxWatches()})
	if err != nil {
		t.Fatal(err)
	}
	tui.apply(wire.Msg{Type: "resp", Action: "msg-watchers", OK: true, Res: res})

	feed(tui, "w")
	r := tui.box.Float.Rect
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
