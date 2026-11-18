package client

// The client side of the notify/ topic: the catch-up requests the box
// broadcasts drive and the toasts the caught-up entries raise.

import (
	"encoding/json"
	"testing"
	"time"

	"dm/internal/box"
	"dm/internal/boxview"
	"dm/internal/keymap"
	"dm/internal/theme"
	"dm/internal/wire"
)

// notifyReply is the resp a catch-up expects: the entries newest first under
// the request id the client sent.
func notifyReply(id int, entries []box.Entry) wire.Msg {
	body, err := json.Marshal(box.ListReply{Entries: entries})
	if err != nil {
		panic(err)
	}
	return wire.Msg{Type: "resp", Action: "msg-list", ID: id, OK: true, Res: body}
}

// notifyTUI builds a bare client with the focused pane set, so a reply tests
// the suppression and the cursor without a server. notifyID starts at one:
// the request always numbers itself before it sends, so a reply the client
// never asked for carries no id. The route map says the id names a catch-up.
func notifyTUI(focus string) *tui {
	tui := &tui{snap: &wire.Snapshot{Focus: focus}, notifyID: 1}
	tui.notifyRouteTag(1, notifyRouteCatchUp)
	return tui
}

// expectRequest waits for the next message the client sends, timing out like
// nextBoxMsg does.
func expectRequest(t *testing.T, got chan wire.Msg) wire.Msg {
	t.Helper()
	select {
	case m := <-got:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("no catch-up request sent")
	}
	return wire.Msg{}
}

// expectNothing gives the pipe a moment, then fails if a message showed up.
func expectNothing(t *testing.T, got chan wire.Msg) {
	t.Helper()
	select {
	case m := <-got:
		t.Fatalf("unexpected message %v", m)
	case <-time.After(50 * time.Millisecond):
	}
}

// A catch-up asks for the notify/ branch, capped, with no cursor before the
// first sight. A snapshot attach and a box broadcast drive the same ask; a
// broadcast that lands while one is in flight waits for the reply.
func TestNotifyCatchUpAsksForNotifyBranch(t *testing.T) {
	t.Parallel()
	tui, got := clientTUI(t, nil)
	tui.apply(wire.Msg{Type: "box"})
	m := expectRequest(t, got)
	if m.Type != "req" || m.Action != "msg-list" || m.ID != 1 {
		t.Fatalf("request = %+v, want an id'd msg-list", m)
	}
	var req box.ListReq
	if err := json.Unmarshal(m.Res, &req); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if req.KeyPrefix != box.NotifyPrefix || req.Limit != 3 || req.AfterSeq != nil {
		t.Fatalf("request = %+v, want keyPrefix notify/ limit 3 no cursor", req)
	}
	// The broadcast during the in-flight request sends no second one; the
	// reply re-arms it, now with the cursor.
	tui.apply(wire.Msg{Type: "box"})
	expectNothing(t, got)
	tui.apply(notifyReply(1, nil))
	m = expectRequest(t, got)
	if m.ID != 2 {
		t.Fatalf("request id = %d, want the re-armed 2", m.ID)
	}
	req = box.ListReq{}
	if err := json.Unmarshal(m.Res, &req); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if req.AfterSeq == nil || *req.AfterSeq != 0 {
		t.Fatalf("request = %+v, want cursor 0", req)
	}
}

// The first catch-up after attach toasts the newest entries it got, not the
// backlog behind them, and the cursor lands on the newest seq.
func TestNotifyFirstCatchUpToastsBacklogCap(t *testing.T) {
	t.Parallel()
	tui := notifyTUI("")
	entries := []box.Entry{
		{Seq: 10, Key: "notify/b", State: box.StateDone, Msg: "third"},
		{Seq: 9, Key: "notify/b", State: box.StateWorking, Msg: "second"},
		{Seq: 8, Key: "notify/c", State: box.StateNote, Msg: "first"},
	}
	tui.apply(notifyReply(tui.notifyID, entries))
	if got := len(tui.toasts); got != 3 {
		t.Fatalf("toasts = %d, want 3: %+v", got, tui.toasts)
	}
	if got := toastParts(tui.toasts[2]); got != "done notify/b third" {
		t.Fatalf("newest toast = %q, want the newest entry", got)
	}
	if tui.notifyCursor != 10 || !tui.notifySeen {
		t.Fatalf("cursor = %d seen = %v, want 10 and caught up", tui.notifyCursor, tui.notifySeen)
	}
}

// An entry about the focused pane raises no toast but still moves the cursor,
// so it never comes back; an entry about another pane in the same reply does.
func TestNotifyFocusedPaneSkipsToast(t *testing.T) {
	t.Parallel()
	tui := notifyTUI("a")
	entries := []box.Entry{
		{Seq: 6, Key: "notify/b", State: box.StateDone, Msg: "gate green"},
		{Seq: 5, Key: "notify/a", State: box.StateBlocked, Msg: "waiting on tests"},
	}
	tui.apply(notifyReply(tui.notifyID, entries))
	if got := toastParts(tui.toasts[0]); len(tui.toasts) != 1 || got != "done notify/b gate green" {
		t.Fatalf("toasts = %q, want only the other pane's entry", got)
	}
	if tui.notifyCursor != 6 {
		t.Fatalf("cursor = %d, want 6", tui.notifyCursor)
	}
}

// A repeat of the same entry text inside the dedupe window joins the last
// toast instead of stacking a second one.
func TestNotifyRepeatDoesNotStack(t *testing.T) {
	t.Parallel()
	tui := notifyTUI("")
	first := []box.Entry{{Seq: 5, Key: "notify/b", State: box.StateWorking, Msg: "running tests"}}
	tui.apply(notifyReply(tui.notifyID, first))
	again := []box.Entry{{Seq: 6, Key: "notify/b", State: box.StateWorking, Msg: "running tests"}}
	tui.apply(notifyReply(tui.notifyID, again))
	if got := toastTexts(tui); len(got) != 1 {
		t.Fatalf("toasts = %q, want one", got)
	}
}

// The cursor skips entries the client has already seen, so a late reply that
// overlaps the cursor toasts only the newcomer.
func TestNotifyCursorSkipsSeen(t *testing.T) {
	t.Parallel()
	tui := notifyTUI("")
	tui.notifyCursor = 10
	tui.notifySeen = true
	entries := []box.Entry{
		{Seq: 11, Key: "notify/b", State: box.StateDone, Msg: "fresh"},
		{Seq: 9, Key: "notify/b", State: box.StateWorking, Msg: "old"},
	}
	tui.apply(notifyReply(tui.notifyID, entries))
	if got := toastParts(tui.toasts[0]); len(tui.toasts) != 1 || got != "done notify/b fresh" {
		t.Fatalf("toasts = %q, want only the unseen entry", got)
	}
	if tui.notifyCursor != 11 {
		t.Fatalf("cursor = %d, want 11", tui.notifyCursor)
	}
}

// The reply only counts when its id names the catch-up request.
func TestNotifyReplyWantsRearm(t *testing.T) {
	t.Parallel()
	tui := notifyTUI("")
	tui.notifySeen = true
	tui.notifyCursor = 4
	tui.apply(notifyReply(2, []box.Entry{{Seq: 5, Key: "notify/b", State: box.StateDone, Msg: "lost"}}))
	if len(tui.toasts) != 0 || tui.notifyCursor != 4 {
		t.Fatalf("stray reply landed: %+v cursor %d", tui.toasts, tui.notifyCursor)
	}
	tui.apply(notifyReply(1, []box.Entry{{Seq: 5, Key: "notify/b", State: box.StateDone, Msg: "kept"}}))
	if got := toastTexts(tui); len(got) != 1 {
		t.Fatalf("toasts = %q, want one", got)
	}
}

// The catch-up and a view clear share one id counter; a reply routes by the tag
// its request left, so a clear reply never raises a toast and a catch-up reply
// never refetches the float.
func TestNotifyRoutesSharedIds(t *testing.T) {
	t.Parallel()
	tui, got := clientTUI(t, nil)
	tui.notifyCatchUp()
	tui.box = boxview.NewBoxView(tui.snap, theme.DefaultTheme(), keymap.LoadDefault())
	tui.boxClear(box.ClearReq{Key: "notify/b"})
	first := expectRequest(t, got)
	second := expectRequest(t, got)
	if first.Action != "msg-list" || first.ID != 1 {
		t.Fatalf("catch-up = %+v, want msg-list id 1", first)
	}
	if second.Action != "msg-clear" || second.ID != 2 {
		t.Fatalf("clear = %+v, want msg-clear id 2", second)
	}

	// The clear's reply refetches the open float and raises no toast.
	res, err := json.Marshal(box.ClearReply{Cleared: 1})
	if err != nil {
		t.Fatal(err)
	}
	tui.apply(wire.Msg{Type: "resp", Action: "msg-clear", ID: second.ID, OK: true, Res: res})
	if got := toastTexts(tui); len(got) != 0 {
		t.Fatalf("toasts = %q, want none from a clear", got)
	}
	if m := expectRequest(t, got); m.Action != "msg-list" || m.ID != 0 {
		t.Fatalf("after the clear the client sent %+v, want the float's log", m)
	}

	// The catch-up's own reply raises its toasts and moves the cursor.
	tui.apply(notifyReply(first.ID, []box.Entry{
		{Seq: 11, Key: "notify/b", State: box.StateDone, Msg: "catch-up"},
	}))
	if got := toastTexts(tui); len(got) != 1 {
		t.Fatalf("toasts = %q, want the catch-up's", got)
	}
	if tui.notifyCursor != 11 {
		t.Fatalf("cursor = %d, want 11", tui.notifyCursor)
	}
	// A catch-up id on a clear reply lands nowhere: the tag says catch-up.
	tui.notifyCatchUp()
	third := expectRequest(t, got)
	if third.Action != "msg-list" {
		t.Fatalf("catch-up = %+v, want msg-list", third)
	}
	tui.apply(wire.Msg{Type: "resp", Action: "msg-clear", ID: third.ID, OK: true, Res: res})
	expectNothing(t, got)
}

// A notify toast keeps the entry's state and topic, so the box can name the
// state in its border and the topic above the message. An entry with no message
// carries no message row.
func TestNotifyToastKeepsEntryParts(t *testing.T) {
	t.Parallel()
	tui := notifyTUI("")
	tui.apply(notifyReply(tui.notifyID, []box.Entry{
		{Seq: 2, Key: "notify/demo", State: box.StateDone},
		{Seq: 1, Key: "notify/b", State: box.StateBlocked, Msg: "waiting on tests"},
	}))
	if got := len(tui.toasts); got != 2 {
		t.Fatalf("toasts = %d, want both entries", got)
	}
	full := tui.toasts[0]
	if full.state != "blocked" || full.topic != "notify/b" || full.text != "waiting on tests" {
		t.Fatalf("toast = %+v, want the entry's state, topic and message", full)
	}
	bare := tui.toasts[1]
	if bare.state != "done" || bare.topic != "notify/demo" || bare.text != "" {
		t.Fatalf("toast = %+v, want the entry's state and topic and no message", bare)
	}
}

// Every notify entry toasts, whatever its state: the toast surfaces the log, so
// a note or a working entry is not silent.
func TestNotifyEveryStateToasts(t *testing.T) {
	t.Parallel()
	tui := notifyTUI("")
	tui.apply(notifyReply(tui.notifyID, []box.Entry{
		{Seq: 3, Key: "notify/a", State: box.StateWorking, Msg: "running"},
		{Seq: 2, Key: "notify/a", State: box.StateNote, Msg: "noted"},
		{Seq: 1, Key: "notify/a", State: box.StateError, Msg: "crashed"},
	}))
	if got := len(tui.toasts); got != 3 {
		t.Fatalf("toasts = %d, want one per state: %+v", got, tui.toasts)
	}
}

// Two panes finishing with the same message are two announcements, so the dedupe
// compares the topic as well as the message.
func TestNotifyDedupeComparesTopic(t *testing.T) {
	t.Parallel()
	tui := notifyTUI("")
	tui.apply(notifyReply(tui.notifyID, []box.Entry{
		{Seq: 2, Key: "notify/b", State: box.StateDone, Msg: "ok"},
		{Seq: 1, Key: "notify/a", State: box.StateDone, Msg: "ok"},
	}))
	if got := len(tui.toasts); got != 2 {
		t.Fatalf("toasts = %d, want one per pane: %+v", got, tui.toasts)
	}
	// The same pane in the same state with the same message inside the dedupe
	// window is one toast, not two.
	tui.apply(notifyReply(tui.notifyID, []box.Entry{
		{Seq: 3, Key: "notify/b", State: box.StateDone, Msg: "ok"},
	}))
	if got := len(tui.toasts); got != 2 {
		t.Fatalf("toasts = %d, want the repeat joined: %+v", got, tui.toasts)
	}
}
