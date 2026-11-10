package client

// The client side of the notify/ topic: the catch-up requests the box
// broadcasts drive and the toasts the caught-up entries raise.

import (
	"encoding/json"
	"testing"
	"time"

	"dm/internal/box"
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
	if tui.toasts[2].text != "notify/b done: third" {
		t.Fatalf("newest toast = %q, want the newest entry", tui.toasts[2].text)
	}
	if tui.notifyCursor != 10 || !tui.notifySeen {
		t.Fatalf("cursor = %d seen = %v, want 10 and caught up", tui.notifyCursor, tui.notifySeen)
	}
}

// An entry about the focused pane raises no toast but still moves the cursor,
// so it never comes back; an entry about another pane in the same reply does.
func TestNotifyFocusedPaneSkipsToast(t *testing.T) {
	tui := notifyTUI("a")
	entries := []box.Entry{
		{Seq: 6, Key: "notify/b", State: box.StateDone, Msg: "gate green"},
		{Seq: 5, Key: "notify/a", State: box.StateBlocked, Msg: "waiting on tests"},
	}
	tui.apply(notifyReply(tui.notifyID, entries))
	if got := toastTexts(tui); len(got) != 1 || got[0] != "notify/b done: gate green" {
		t.Fatalf("toasts = %q, want only the other pane's entry", got)
	}
	if tui.notifyCursor != 6 {
		t.Fatalf("cursor = %d, want 6", tui.notifyCursor)
	}
}

// A repeat of the same entry text inside the dedupe window joins the last
// toast instead of stacking a second one.
func TestNotifyRepeatDoesNotStack(t *testing.T) {
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
	tui := notifyTUI("")
	tui.notifyCursor = 10
	tui.notifySeen = true
	entries := []box.Entry{
		{Seq: 11, Key: "notify/b", State: box.StateDone, Msg: "fresh"},
		{Seq: 9, Key: "notify/b", State: box.StateWorking, Msg: "old"},
	}
	tui.apply(notifyReply(tui.notifyID, entries))
	if got := toastTexts(tui); len(got) != 1 || got[0] != "notify/b done: fresh" {
		t.Fatalf("toasts = %q, want only the unseen entry", got)
	}
	if tui.notifyCursor != 11 {
		t.Fatalf("cursor = %d, want 11", tui.notifyCursor)
	}
}

// The reply only counts when its id names the catch-up request.
func TestNotifyReplyWantsRearm(t *testing.T) {
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

// The catch-up and the centre share one id counter; a reply routes by the tag
// its request left, so an open centre's list never runs through the toast
// handler and a late reply lands nowhere.
func TestNotifyRoutesSharedIds(t *testing.T) {
	tui, got := clientTUI(t, nil)
	tui.ncentre = newNotifyCentre(tui.snap, theme.DefaultTheme())
	tui.notifyCatchUp()
	tui.notifyCentreRequest()
	first := expectRequest(t, got)
	second := expectRequest(t, got)
	if first.ID != 1 || second.ID != 2 {
		t.Fatalf("request ids = %d, %d, want the shared counter 1, 2", first.ID, second.ID)
	}
	// The centre's reply carries entries past the cursor; misrouted, they
	// would toast. Routed, they fill the centre.
	tui.apply(centreReply(t, 2, []box.Entry{
		{Seq: 21, Key: "notify/b", State: box.StateDone, Msg: "centre one"},
		{Seq: 20, Key: "notify/b", State: box.StateDone, Msg: "centre two"},
	}))
	if got := toastTexts(tui); len(got) != 0 {
		t.Fatalf("toasts = %q, want none from the centre's reply", got)
	}
	if got := len(tui.ncentre.entries); got != 2 {
		t.Fatalf("centre entries = %d, want 2", got)
	}
	// The catch-up's own reply raises its toasts and moves the cursor.
	tui.apply(notifyReply(1, []box.Entry{
		{Seq: 11, Key: "notify/b", State: box.StateDone, Msg: "catch-up"},
	}))
	if got := toastTexts(tui); len(got) != 1 {
		t.Fatalf("toasts = %q, want the catch-up's", got)
	}
	if tui.notifyCursor != 11 {
		t.Fatalf("cursor = %d, want 11", tui.notifyCursor)
	}
	// A third round trip: the centre's next refresh routes too.
	tui.notifyCentreRequest()
	third := expectRequest(t, got)
	tui.apply(centreReply(t, third.ID, []box.Entry{
		{Seq: 22, Key: "notify/b", State: box.StateDone, Msg: "centre three"},
	}))
	if got := len(tui.ncentre.entries); got != 1 {
		t.Fatalf("centre entries = %d, want the newest reply alone", got)
	}
}
