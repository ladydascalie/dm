package client

// The merged message view: one float for the whole log. main's message-box
// opens it unfiltered, main's notification-centre opens the same float on the
// notify branch, and one key handler runs every key through the box keymap.

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"dm/internal/box"
	"dm/internal/boxview"
	"dm/internal/testutil"
	"dm/internal/theme"
	"dm/internal/wire"
)

// viewEntries is a mixed log newest first: a plain lane entry, then notify
// entries about two panes, one free post under the bare topic and a body.
func viewEntries() []box.Entry {
	at := time.Now()
	return []box.Entry{
		{Seq: 5, At: at, Key: "lane/a", State: box.StateWorking, From: "w2p1", Msg: "plain entry"},
		{Seq: 4, At: at, Key: "notify/w1p1", State: box.StateBlocked, From: "w1p1", Msg: "waiting on tests"},
		{Seq: 3, At: at, Key: "notify/w1p2", State: box.StateDone, From: "w1p2", Msg: "gate green"},
		{Seq: 2, At: at, Key: "notify/", State: box.StateNote, Msg: "free post", Body: "the long body"},
		{Seq: 1, At: at, Key: "lane/a", State: box.StateError, From: "w3p1", Msg: "build failed"},
	}
}

// viewReply is the resp a whole-log read expects.
func viewReply(t *testing.T, entries []box.Entry) wire.Msg {
	t.Helper()
	res, err := json.Marshal(box.ListReply{Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	return wire.Msg{Type: "resp", Action: "msg-list", OK: true, Res: res}
}

// openView feeds the keys that open the merged float and takes the two requests
// the open sends: the whole log, then the watcher list.
func openView(t *testing.T, tui *tui, got chan wire.Msg, keys string) {
	t.Helper()
	feed(tui, keys)
	if tui.box == nil || tui.mode != "box" {
		t.Fatalf("%q opened mode %q box %v, want the message view", keys, tui.mode, tui.box)
	}
	if len(tui.floats) != 1 {
		t.Fatalf("floats = %d, want the one view", len(tui.floats))
	}
	m := nextBoxMsg(t, got)
	if m.Action != "msg-list" || m.ID != 0 {
		t.Fatalf("open sent %+v, want the whole log", m)
	}
	var req box.ListReq
	if err := json.Unmarshal(m.Res, &req); err != nil {
		t.Fatalf("list request: %v", err)
	}
	if req.Key != "" || req.KeyPrefix != "" || req.Limit != 0 {
		t.Fatalf("list request = %+v, want the whole log", req)
	}
	if m := nextBoxMsg(t, got); m.Action != "msg-watchers" {
		t.Fatalf("open sent %q, want the watcher list", m.Action)
	}
}

// fillView lands one whole-log reply.
func fillView(t *testing.T, tui *tui, entries []box.Entry) {
	t.Helper()
	tui.apply(viewReply(t, entries))
}

// TestViewOpensOnN: N opens the same float on the notify branch. The float asks
// for the whole log once, and the filter narrows it in the client.
func TestViewOpensOnN(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	openView(t, tui, got, "\x02N")
	if got := tui.box.Filter(); got != box.NotifyPrefix {
		t.Fatalf("filter = %q, want the notify branch", got)
	}
	fillView(t, tui, viewEntries())
	if got, want := tui.box.ListLen(), 3; got != want {
		t.Fatalf("rows = %d, want the %d notify entries", got, want)
	}
	if got := tui.box.Float.Title; !strings.HasPrefix(got, " notifications  3 entries") {
		t.Fatalf("title = %q, want the notify name and count", got)
	}
	r := tui.box.Float.Rect
	screen := boxScreen(t, tui.box, tui.snap.Cols, tui.snap.Rows)
	top := boxScreenRow(screen, r.Y, r.X, r.W)
	if !strings.Contains(top, "waiting") {
		t.Fatalf("top row = %q, want the newest notify entry", top)
	}
	if !strings.Contains(top, "*") {
		t.Fatalf("top row = %q, want the unread mark", top)
	}
	if strings.Contains(top, "plain entry") {
		t.Fatalf("top row = %q, want the filter to hide the lane entry", top)
	}
}

// TestViewOpensUnfiltered: m opens the same float with the filter off, so every
// entry shows under the box name.
func TestViewOpensUnfiltered(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	openView(t, tui, got, "\x02m")
	if got := tui.box.Filter(); got != "" {
		t.Fatalf("filter = %q, want every entry", got)
	}
	fillView(t, tui, viewEntries())
	if got, want := tui.box.ListLen(), 5; got != want {
		t.Fatalf("rows = %d, want %d", got, want)
	}
	if got := tui.box.Float.Title; !strings.HasPrefix(got, " box  5 entries") {
		t.Fatalf("title = %q, want the box name and count", got)
	}
}

// TestViewFilterToggleKeepsSelection: m swaps the filter and the selection
// rides the entry it sat on, so a toggle never loses the row.
func TestViewFilterToggleKeepsSelection(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	openView(t, tui, got, "\x02N")
	fillView(t, tui, viewEntries())
	feed(tui, "j") // seq 3, a notify entry
	e, ok := tui.box.Selected()
	if !ok || e.Seq != 3 {
		t.Fatalf("selected = %+v ok %v, want seq 3", e, ok)
	}
	feed(tui, "m") // filter off, the same entry is still shown
	if got := tui.box.Filter(); got != "" {
		t.Fatalf("filter = %q, want every entry", got)
	}
	if e, ok := tui.box.Selected(); !ok || e.Seq != 3 {
		t.Fatalf("selected after the toggle = %+v ok %v, want seq 3", e, ok)
	}
	if got, want := tui.box.ListLen(), 5; got != want {
		t.Fatalf("rows = %d, want %d", got, want)
	}
}

// TestViewUnreadClearsOnBodyAndClose: opening a body marks that entry read, and
// closing the float marks every entry the view held read, so the badge and the
// marks agree.
func TestViewUnreadClearsOnBodyAndClose(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	openView(t, tui, got, "\x02N")
	fillView(t, tui, viewEntries())
	// The badge counts the notify cache, which the catch-up feeds.
	tui.notifyRecentMerge(viewEntries()[1:4])
	if got := tui.box.Unread(); got != 3 {
		t.Fatalf("unread = %d, want 3", got)
	}
	feed(tui, "jj") // seq 2, the entry with a body
	feed(tui, "\r") // its body
	if tui.reader == nil || tui.reader.e.Seq != 2 {
		t.Fatalf("enter opened %+v, want the reader at seq 2", tui.reader)
	}
	if tui.notifyRead != 2 {
		t.Fatalf("read mark = %d, want 2", tui.notifyRead)
	}
	if got := tui.box.Unread(); got != 2 {
		t.Fatalf("unread after one body = %d, want 2: seq 4 and 3 stay unread", got)
	}
	feed(tui, "\x1b") // back to the list
	if tui.reader != nil {
		t.Fatal("esc left the reader up")
	}
	// Closing marks what the view held read: the three notify entries, and the
	// badge drops with the marks.
	if got, want := tui.barBadgeOf(theme.DefaultTheme(), theme.GlyphNerd).n, 2; got != want {
		t.Fatalf("badge = %d before the close, want %d", got, want)
	}
	feed(tui, "q")
	if tui.box != nil || tui.mode != "" {
		t.Fatal("q left the view up")
	}
	if tui.notifyRead != 4 {
		t.Fatalf("read mark = %d, want it at the newest notify seq 4", tui.notifyRead)
	}
	if got := tui.barBadgeOf(theme.DefaultTheme(), theme.GlyphNerd).n; got != 0 {
		t.Fatalf("badge = %d after the close, want 0", got)
	}
}

// TestViewJumpPane: p closes the float and focuses the pane the entry is about.
func TestViewJumpPane(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	openView(t, tui, got, "\x02N")
	fillView(t, tui, viewEntries())
	feed(tui, "p")
	if tui.box != nil || tui.mode != "" {
		t.Fatal("p left the view up")
	}
	m := nextBoxMsg(t, got)
	if m.Action != "focus" || m.Target != "w1p1" {
		t.Fatalf("jump sent %+v, want focus for w1p1", m)
	}
}

// TestViewJumpPaneRefuses: a key that names no pane, and a pane the session
// does not carry, raise a toast and leave the float where it is.
func TestViewJumpPaneRefuses(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		entry box.Entry
	}{
		{"a bare topic names no pane", box.Entry{Seq: 2, Key: "notify/", State: box.StateNote, Msg: "free post"}},
		{"a plain key names no pane", box.Entry{Seq: 2, Key: "lane/a", State: box.StateNote, Msg: "plain"}},
		{"an unknown pane", box.Entry{Seq: 2, Key: "notify/w9p9", State: box.StateNote, Msg: "gone"}},
	} {
		tui, got := switcherTUI(t)
		openView(t, tui, got, "\x02m")
		fillView(t, tui, []box.Entry{c.entry})
		feed(tui, "p")
		if tui.box == nil {
			t.Fatalf("%s: p closed the view", c.name)
		}
		if len(tui.toasts) == 0 {
			t.Fatalf("%s: p raised no toast", c.name)
		}
		testutil.NoMsg(t, got)
	}
}

// TestViewClearEntry: x clears the selected entry's key through msg-clear, and
// the reply refetches the whole log.
func TestViewClearEntry(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	openView(t, tui, got, "\x02N")
	fillView(t, tui, viewEntries())
	feed(tui, "j") // seq 3
	feed(tui, "x")
	m := nextBoxMsg(t, got)
	if m.Action != "msg-clear" || m.ID == 0 {
		t.Fatalf("x sent %+v, want an id'd msg-clear", m)
	}
	var req box.ClearReq
	if err := json.Unmarshal(m.Res, &req); err != nil {
		t.Fatalf("clear request: %v", err)
	}
	if req.Key != "notify/w1p2" || req.KeyPrefix != "" {
		t.Fatalf("clear request = %+v, want the selected entry's key", req)
	}
	res, err := json.Marshal(box.ClearReply{Cleared: 1})
	if err != nil {
		t.Fatal(err)
	}
	tui.apply(wire.Msg{Type: "resp", Action: "msg-clear", ID: m.ID, OK: true, Res: res})
	refetch := nextBoxMsg(t, got)
	if refetch.Action != "msg-list" || refetch.ID != 0 {
		t.Fatalf("after the clear the client sent %+v, want the whole log", refetch)
	}
}

// TestViewClearFilter: X under the filter clears the notify branch alone, so a
// key the filter hides is never named. Unfiltered, X clears what the view
// shows, which is the whole log.
func TestViewClearFilter(t *testing.T) {
	t.Parallel()
	t.Run("filtered", func(t *testing.T) {
		t.Parallel()
		tui, got := switcherTUI(t)
		openView(t, tui, got, "\x02N")
		fillView(t, tui, viewEntries())
		feed(tui, "X")
		m := nextBoxMsg(t, got)
		if m.Action != "msg-clear" {
			t.Fatalf("X sent %+v, want msg-clear", m)
		}
		var req box.ClearReq
		if err := json.Unmarshal(m.Res, &req); err != nil {
			t.Fatal(err)
		}
		if req.Key != "" || req.KeyPrefix != box.NotifyPrefix {
			t.Fatalf("clear request = %+v, want the notify prefix", req)
		}
		if len(tui.notifyRecent) != 0 {
			t.Fatalf("cache = %+v, want it emptied", tui.notifyRecent)
		}
	})
	t.Run("unfiltered", func(t *testing.T) {
		t.Parallel()
		tui, got := switcherTUI(t)
		openView(t, tui, got, "\x02m")
		fillView(t, tui, viewEntries())
		feed(tui, "X")
		m := nextBoxMsg(t, got)
		var req box.ClearReq
		if err := json.Unmarshal(m.Res, &req); err != nil {
			t.Fatal(err)
		}
		if req.Key != "" || req.KeyPrefix != "" {
			t.Fatalf("clear request = %+v, want the whole log", req)
		}
	})
}

// TestViewReaderOverList: enter opens the body reader on the selected entry,
// the reader keeps the box keys, and a close key takes both down.
func TestViewReaderOverList(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	openView(t, tui, got, "\x02N")
	fillView(t, tui, viewEntries())
	// Enter opens the selected entry whether it carries a body or not: a
	// summary wraps in the same reader.
	feed(tui, "\r")
	if tui.reader == nil {
		t.Fatal("enter did not open the reader for a summary")
	}
	if got := tui.reader.lines; len(got) != 1 || got[0] != tui.reader.e.Msg {
		t.Fatalf("reader lines = %q, want the summary", got)
	}
	feed(tui, "q")
	if tui.reader != nil {
		t.Fatal("q did not close the summary reader")
	}
	feed(tui, "jj") // seq 2, the entry with a body
	feed(tui, "\r")
	if tui.reader == nil {
		t.Fatal("enter did not open the reader")
	}
	if want := boxview.ReaderRect(tui.snap, tui.box.ListLen()); tui.reader.Float.Rect != want {
		t.Fatalf("reader rect = %+v, want %+v", tui.reader.Float.Rect, want)
	}
	// q closes the reader first and leaves the list up.
	feed(tui, "q")
	if tui.reader != nil || tui.box == nil {
		t.Fatalf("q left reader %v box %v, want the list page back", tui.reader, tui.box)
	}
	// The next q closes the view.
	feed(tui, "q")
	if tui.box != nil || tui.mode != "" || len(tui.floats) != 0 {
		t.Fatalf("q left box %v mode %q floats %d", tui.box, tui.mode, len(tui.floats))
	}
}

// TestViewCloseTakesTheReaderDown: a close key with the reader up leaves no
// orphaned float behind.
func TestViewCloseTakesTheReaderDown(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	openView(t, tui, got, "\x02N")
	fillView(t, tui, viewEntries())
	feed(tui, "jj") // seq 2, the entry with a body
	feed(tui, "\r")
	if tui.reader == nil {
		t.Fatal("enter did not open the reader")
	}
	feed(tui, "N") // the main keymap's notification-centre closes the float
	if tui.box != nil || tui.mode != "" {
		t.Fatal("N left the view up")
	}
	if tui.reader != nil || len(tui.floats) != 0 {
		t.Fatalf("reader %v floats %d, want none", tui.reader, len(tui.floats))
	}
}

// TestViewCloseKeys: esc, q and an unbound key all close the float and hand the
// keyboard back.
func TestViewCloseKeys(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"q", "N", "\x03", "\x01"} {
		tui, got := switcherTUI(t)
		openView(t, tui, got, "\x02N")
		fillView(t, tui, viewEntries())
		feed(tui, key)
		if tui.box != nil || tui.mode != "" || len(tui.floats) != 0 {
			t.Fatalf("%q left the view up", key)
		}
	}
	// A lone esc is the head of an arrow sequence, so it closes once the
	// pending-escape timeout passes, never before.
	tui, got := switcherTUI(t)
	openView(t, tui, got, "\x02N")
	fillView(t, tui, viewEntries())
	feed(tui, "\x1b")
	if tui.box == nil {
		t.Fatal("a lone esc closed the view before the timeout")
	}
	if !tui.expireMode(time.Now().Add(2 * escTimeout)) {
		t.Fatal("the pending escape never expired")
	}
	if tui.box != nil || tui.mode != "" {
		t.Fatal("esc did not close the view")
	}
}

// TestViewBottomAndScroll: G selects the oldest row and the window follows it,
// so a long log scrolls to the selection.
func TestViewBottomAndScroll(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	openView(t, tui, got, "\x02m")
	entries := make([]box.Entry, 0, 25)
	for seq := 25; seq >= 1; seq-- {
		entries = append(entries, box.Entry{Seq: seq, Key: "notify/w1p1",
			State: box.StateNote, From: "w1p1", Msg: "entry " + strconv.Itoa(seq)})
	}
	fillView(t, tui, entries)
	h := tui.box.Float.Rect.H
	feed(tui, "G")
	if e, ok := tui.box.Selected(); !ok || e.Seq != 1 {
		t.Fatalf("selected = %+v ok %v, want the oldest entry", e, ok)
	}
	if got, want := tui.box.Top(), len(entries)-h; got != want {
		t.Fatalf("window top = %d, want the last full window %d", got, want)
	}
	r := tui.box.Float.Rect
	screen := boxScreen(t, tui.box, tui.snap.Cols, tui.snap.Rows)
	if row := boxScreenRow(screen, r.Y+r.H-1, r.X, r.W); !strings.Contains(row, "entry 1") {
		t.Fatalf("last row = %q, want the oldest entry", row)
	}
	if row := boxScreenRow(screen, r.Y, r.X, r.W); strings.Contains(row, "entry 25") {
		t.Fatalf("top row = %q, want the newest entry scrolled out", row)
	}
}

// TestViewKeysFollowTheKeymap: a config that rebinds the filter, the jump, the
// clears and the close moves the view's keys with it, all through the box
// keymap.
func TestViewKeysFollowTheKeymap(t *testing.T) {
	t.Parallel()
	cfg := testutil.KeymapTestLoad(t, `
keymap "main" {
    "send-prefix" "prefix"
    "notification-centre" "n"
}
keymap "box" {
    "up" "k"
    "down" "j"
    "filter-notify" "f"
    "jump-pane" "J"
    "clear-entry" "D"
    "clear-filter" "Z"
    "close" "q"
    "close-esc" "esc"
    "close-ctrl-c" "ctrl+c"
}
`)
	tui, got := switcherTUI(t)
	openView(t, tui, got, "\x02m") // the default message-box key, unfiltered
	fillView(t, tui, viewEntries())
	// A reload swaps the config, and the open float follows it.
	tui.keymap = cfg
	tui.refreshHints(cfg)
	feed(tui, "f") // the rebound filter opens the notify branch
	if got := tui.box.Filter(); got != box.NotifyPrefix {
		t.Fatalf("filter = %q, want the notify branch", got)
	}
	// The selection keeps its row, so it sits on the newest notify entry.
	if e, ok := tui.box.Selected(); !ok || e.Seq != 4 {
		t.Fatalf("selected = %+v ok %v, want seq 4", e, ok)
	}
	feed(tui, "D") // the rebound clear-entry
	m := nextBoxMsg(t, got)
	if m.Action != "msg-clear" {
		t.Fatalf("D sent %+v, want msg-clear", m)
	}
	var req box.ClearReq
	if err := json.Unmarshal(m.Res, &req); err != nil {
		t.Fatal(err)
	}
	if req.Key != "notify/w1p1" || req.KeyPrefix != "" {
		t.Fatalf("clear request = %+v, want the selected entry's key", req)
	}
	feed(tui, "Z") // the rebound clear-filter
	m = nextBoxMsg(t, got)
	if err := json.Unmarshal(m.Res, &req); err != nil {
		t.Fatal(err)
	}
	if req.Key != "" || req.KeyPrefix != box.NotifyPrefix {
		t.Fatalf("clear request = %+v, want the notify prefix", req)
	}
	feed(tui, "J") // the rebound jump-pane
	if tui.box != nil || tui.mode != "" {
		t.Fatal("J left the view up")
	}
	if m := nextBoxMsg(t, got); m.Action != "focus" || m.Target != "w1p1" {
		t.Fatalf("J sent %+v, want focus for w1p1", m)
	}
}

// TestViewHintNamesOnlyBoxKeys: every key the hint paints is a key the box
// keymap binds, so no key in the corner is a literal.
func TestViewHintNamesOnlyBoxKeys(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	openView(t, tui, got, "\x02N")
	fillView(t, tui, viewEntries())
	km := tui.km()
	bound := make(map[string]bool)
	for _, action := range []string{
		"up", "up-arrow", "down", "down-arrow", "page-up", "page-down",
		"top", "bottom", "toggle-page", "body", "filter-notify", "jump-pane",
		"clear-entry", "clear-filter", "close", "close-ctrl-c", "close-esc",
	} {
		if name := km.Key("box", action); name != "" {
			bound[name] = true
		}
	}
	bar := tui.box.Float.Hint
	if bar == nil || len(bar.Hints) == 0 {
		t.Fatal("the view has no hint")
	}
	for _, h := range bar.Hints {
		for _, key := range h.Keys {
			if key != "" && !bound[key] {
				t.Fatalf("hint key %q is not a box keymap key", key)
			}
		}
	}
}

// TestViewSyncReusesTheTerm: a snapshot with the same rect repaints nothing and
// the float's Term rides through it, while a rect that moved rebuilds it.
func TestViewSyncReusesTheTerm(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	openView(t, tui, got, "\x02m")
	fillView(t, tui, viewEntries())
	term := tui.box.Float.VT
	if term == nil {
		t.Fatal("the view opened with no Term")
	}
	tui.apply(wire.Msg{Type: "snapshot", Snap: tui.snap})
	if tui.box.Float.VT != term {
		t.Fatal("a snapshot with the same rect rebuilt the Term")
	}
	big := *tui.snap
	big.Cols += 20
	tui.apply(wire.Msg{Type: "snapshot", Snap: &big})
	if tui.box.Float.VT == term {
		t.Fatal("a snapshot with a new rect kept the old Term")
	}
	cols, rows := tui.box.Float.VT.Grid().Size()
	if cols != tui.box.Float.Rect.W || rows != tui.box.Float.Rect.H {
		t.Fatalf("rebuilt Term is %dx%d, want the rect %dx%d",
			cols, rows, tui.box.Float.Rect.W, tui.box.Float.Rect.H)
	}
}

// TestViewRefetchClearsTheLostRows: a refetch that shrinks the log leaves no
// row of the longer one behind.
func TestViewRefetchClearsTheLostRows(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	openView(t, tui, got, "\x02m")
	fillView(t, tui, viewEntries())
	fillView(t, tui, viewEntries()[:2])
	if got, want := tui.box.Float.Rect.H, 2; got != want {
		t.Fatalf("rect height = %d, want the shorter log's %d", got, want)
	}
	r := tui.box.Float.Rect
	screen := boxScreen(t, tui.box, tui.snap.Cols, tui.snap.Rows)
	if row := boxScreenRow(screen, r.Y, r.X, r.W); !strings.Contains(row, "plain entry") {
		t.Fatalf("top row = %q, want the newest entry", row)
	}
	for y := r.Y + 2; y < r.Y+4; y++ {
		if row := boxScreenRow(screen, y, r.X+1, r.W-2); strings.Contains(row, "gate green") {
			t.Fatalf("row %d = %q, want the dropped entry's row gone", y, row)
		}
	}
}

// TestViewClearKeepsTheMarkMonotonic: a clear reply that refetches a shorter
// log never walks the read mark back over an entry the operator read.
func TestViewClearKeepsTheMarkMonotonic(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	openView(t, tui, got, "\x02N")
	fillView(t, tui, viewEntries())
	feed(tui, "jj")
	feed(tui, "\r") // the body of seq 2
	if tui.notifyRead != 2 {
		t.Fatalf("read mark = %d, want 2", tui.notifyRead)
	}
	feed(tui, "\x1b") // back to the list
	feed(tui, "X")
	m := nextBoxMsg(t, got)
	res, err := json.Marshal(box.ClearReply{Cleared: 3})
	if err != nil {
		t.Fatal(err)
	}
	tui.apply(wire.Msg{Type: "resp", Action: "msg-clear", ID: m.ID, OK: true, Res: res})
	nextBoxMsg(t, got) // the refetch
	fillView(t, tui, nil)
	feed(tui, "\x1b") // esc closes the emptied view after the timeout
	tui.expireMode(time.Now().Add(2 * escTimeout))
	if tui.box != nil {
		t.Fatal("esc did not close the view")
	}
	if tui.notifyRead != 2 {
		t.Fatalf("read mark = %d, want it to stay at 2", tui.notifyRead)
	}
}
