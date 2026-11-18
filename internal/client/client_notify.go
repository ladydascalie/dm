package client

// The client side of the notify/ topic: the catch-up requests the box
// broadcasts drive and the toasts the caught-up entries raise. The entry
// always reaches the box log view through the ordinary refetch; only the
// toast lives here.

import (
	"encoding/json"

	"dm/internal/box"
	"dm/internal/wire"
)

// notifyFetch caps one catch-up reply. An unseen backlog therefore toasts at
// most its three newest entries, and the toast stack itself holds toastMax.
const notifyFetch = 3

// notifyRecentLimit caps the recent notify/ cache the bar badge counts from.
const notifyRecentLimit = 50

// The reads an id'd message request can serve; the reply carries only the id,
// so the route rides in the client's map.
const (
	notifyRouteCatchUp = "catchup"
	notifyRouteClear   = "clear"
)

// notifyRouteTag tags an outstanding id'd message request with the read it
// serves. The map builds itself: a tui constructed by hand has no map until the
// first request goes out.
func (t *tui) notifyRouteTag(id int, route string) {
	if t.notifyRoute == nil {
		t.notifyRoute = map[int]string{}
	}
	t.notifyRoute[id] = route
}

// notifySuppress reports whether the operator is already looking at the pane
// the entry is about, matching the server's renderer-side rule: a topic of
// notify/<pane-id> with that pane focused stays silent, and a bare notify/
// topic names no pane so it never suppresses.
func (t *tui) notifySuppress(key string) bool {
	if !box.IsNotifyTopic(key) {
		return false
	}
	pane := key[len(box.NotifyPrefix):]
	if pane == "" {
		return false
	}
	return t.snap != nil && t.snap.Focus == pane
}

// notifyCatchUp asks the server for the notify/ entries the client has not
// seen. The box broadcast triggers it; the reply carries the entries newest
// first. Only one catch-up is in flight, so a broadcast that lands mid-request
// leaves notifyWant and the reply re-arms it.
func (t *tui) notifyCatchUp() {
	if t.conn == nil {
		return
	}
	if t.notifyPending {
		t.notifyWant = true
		return
	}
	req := box.ListReq{KeyPrefix: box.NotifyPrefix, Limit: notifyFetch}
	if t.notifySeen {
		after := t.notifyCursor
		req.AfterSeq = &after
	}
	body, err := json.Marshal(req)
	if err != nil {
		return
	}
	t.notifyPending = true
	t.notifyID++
	t.notifyRouteTag(t.notifyID, notifyRouteCatchUp)
	_ = t.conn.Send(wire.Msg{Type: "req", Action: "msg-list", ID: t.notifyID, Res: body})
}

// takeNotifyList raises the toasts the caught-up entries deserve. A malformed
// reply and an error reply stay silent: the catch-up is background traffic,
// and a failed ask retries on the next broadcast. The entry itself is never
// lost here; only the toast can be, and the box view refetch owns the log.
func (t *tui) takeNotifyList(m wire.Msg) {
	t.notifyPending = false
	if !m.OK {
		return
	}
	var rep box.ListReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		return
	}
	// The reply is newest first, so walk it backwards: the oldest entry is
	// raised first and the newest sits on top of the toast stack.
	for i := len(rep.Entries) - 1; i >= 0; i-- {
		e := rep.Entries[i]
		if e.Seq <= t.notifyCursor {
			continue
		}
		t.notifyCursor = e.Seq
		if t.notifySuppress(e.Key) {
			continue
		}
		t.notifyEntry(e)
	}
	t.notifyRecentMerge(rep.Entries)
	t.notifySeen = true
	if t.notifyWant {
		t.notifyWant = false
		t.notifyCatchUp()
	}
}

// notifyRecentMerge folds a msg-list reply into the client's recent entries.
// The reply is newest first; the known seqs are skipped and the cache keeps
// its newest-first order, capped at notifyRecentLimit.
func (t *tui) notifyRecentMerge(entries []box.Entry) {
	known := make(map[int]bool, len(t.notifyRecent))
	for _, e := range t.notifyRecent {
		known[e.Seq] = true
	}
	var fresh []box.Entry
	for _, e := range entries {
		if !known[e.Seq] {
			fresh = append(fresh, e)
			known[e.Seq] = true
		}
	}
	for i := len(fresh) - 1; i >= 0; i-- {
		t.notifyRecent = append([]box.Entry{fresh[i]}, t.notifyRecent...)
	}
	if len(t.notifyRecent) > notifyRecentLimit {
		t.notifyRecent = t.notifyRecent[:notifyRecentLimit]
	}
}

// notifyRecentDropKey drops every cached entry under key, the rows a msg-clear
// with that key removed server-side. The badge follows the cache, so it cannot
// keep a cleared entry alive.
func (t *tui) notifyRecentDropKey(key string) {
	out := make([]box.Entry, 0, len(t.notifyRecent))
	for _, e := range t.notifyRecent {
		if e.Key != key {
			out = append(out, e)
		}
	}
	t.notifyRecent = out
}

// notifyUnread counts the cached entries past the read mark: the same rows the
// box view marks unread, so the bar badge and the marks never drift.
func (t *tui) notifyUnread() int {
	n := 0
	for _, e := range t.notifyRecent {
		if e.Seq > t.notifyRead {
			n++
		}
	}
	return n
}
