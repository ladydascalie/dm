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

// notifyToastText is the one line a toast shows: topic, state, message, in the
// server renderer's order.
func notifyToastText(e box.Entry) string {
	return e.Key + " " + string(e.State) + ": " + e.Msg
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
		t.notify(notifyToastText(e))
	}
	t.notifySeen = true
	if t.notifyWant {
		t.notifyWant = false
		t.notifyCatchUp()
	}
}
