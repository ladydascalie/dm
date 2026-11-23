package client

// The client side of the floats: opening and closing them, the requests the
// message box sends and the replies it takes, and the keys and wheel each
// float consumes. The views themselves live in internal/boxview and
// internal/keymapview.

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"dm/internal/box"
	"dm/internal/boxview"
	"dm/internal/browser"
	"dm/internal/keymap"
	"dm/internal/keymapview"
	"dm/internal/wire"
)

// openBoxView opens the message box as a floating pane, or closes it when it is
// already up. filter is the topic prefix it opens on: empty for the whole log,
// notify/ for the notification centre. The view asks for the whole log and the
// watcher list; the replies fill it, and the filter narrows it in the client,
// so a toggle costs no round trip.
func openBoxView(t *tui, filter string) {
	if t.snap == nil {
		t.disarm()
		return
	}
	if t.box != nil {
		closeBoxView(t)
		return
	}
	t.box = boxview.NewBoxView(t.snap, t.theme(), t.km())
	t.box.SetRead(t.notifyRead, t.theme())
	t.box.SetFilter(filter, t.theme())
	t.AddFloatingPane(t.box.Float)
	t.mode = "box"
	t.armed = false
	t.boxRequest()
	t.boxWatchers(time.Now())
	t.dirty = true
}

// closeBoxView takes the float down, gives the keyboard back to the panes and
// marks every entry it held read, so the bar badge counts from the same mark
// the rows carried.
func closeBoxView(t *tui) {
	if t.box == nil {
		return
	}
	closeBodyReader(t)
	if mark := t.box.MarkReadAll(); mark > t.notifyRead {
		t.notifyRead = mark
	}
	t.RemoveFloatingPane(t.box.Float.ID)
	t.box = nil
	t.mode = ""
	// The close key stays in the buffer while the pending timer waits. Drop
	// the whole buffer here, so a later keypress cannot forward a stray escape
	// to a pane.
	t.inbuf = nil
	t.pending = false
	t.dirty = true
	t.openDeferredAsk()
}

// boxRequest asks the server for the whole log. The reply carries the entries
// newest first.
func (t *tui) boxRequest() {
	if t.conn == nil {
		return
	}
	_ = t.conn.Send(wire.Msg{
		Type: "req", Action: "msg-list",
		Res: json.RawMessage(`{"key":"","limit":0}`),
	})
}

// boxWatchers asks the server for the watcher list, unless it asked within the
// last boxview.WatchInterval. A held-back ask stays marked and the ticker
// flushes it. The view is not a watcher: the request carries no from.
func (t *tui) boxWatchers(now time.Time) {
	b := t.box
	if b == nil || t.conn == nil {
		return
	}
	if !b.WatchAt.IsZero() && now.Sub(b.WatchAt) < boxview.WatchInterval {
		b.WatchWant = true
		return
	}
	b.WatchAt = now
	b.WatchWant = false
	_ = t.conn.Send(wire.Msg{
		Type: "req", Action: "msg-watchers",
		Res: json.RawMessage(`{}`),
	})
}

// flushBoxWatchers sends a watcher ask that the interval held back.
func (t *tui) flushBoxWatchers(now time.Time) {
	if t.box == nil || !t.box.WatchWant {
		return
	}
	t.boxWatchers(now)
}

// takeBoxList fills the view from a msg-list reply. A closed view, a malformed
// reply and an error reply leave the picture alone; an error becomes a toast.
func (t *tui) takeBoxList(m wire.Msg) {
	if t.box == nil {
		return
	}
	if !m.OK {
		t.notify(m.Error)
		return
	}
	var rep box.ListReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.notify("box: " + err.Error())
		return
	}
	t.box.Set(rep.Entries, t.theme())
	t.dirty = true
}

// takeBoxWatchers fills the watcher page from a msg-watchers reply. Replies
// arrive in request order on the one connection, so the newest reply is the
// newest state. A closed view, a malformed reply and an error reply leave the
// page alone; an error becomes a toast.
func (t *tui) takeBoxWatchers(m wire.Msg) {
	if t.box == nil {
		return
	}
	if !m.OK {
		t.notify(m.Error)
		return
	}
	var rep box.WatchersReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.notify("box: " + err.Error())
		return
	}
	t.box.SetWatchers(rep.Watchers, t.theme())
	t.dirty = true
}

// boxWheel scrolls the view from the wheel. A notch up goes toward older
// entries, the way it goes back in scrollback.
func (t *tui) boxWheel(ev mouseEvent) {
	if t.box == nil {
		return
	}
	t.updateLinkHover(ev.x-1, ev.y-1, ev.btn&16 != 0)
	// Ctrl+click on a URL in the float opens it. The click is consumed; a
	// ctrl+click elsewhere does nothing.
	if ev.btn&16 != 0 && ev.btn&3 == 0 && ev.btn&32 == 0 && ev.btn&64 == 0 && !ev.release {
		r := t.box.Float.Rect
		x, y := ev.x-1, ev.y-1
		if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
			if u := urlAtRow(t.box.Float.VT, r.W, y-r.Y, x-r.X); u != "" {
				if err := browser.Open(u); err != nil {
					t.notify("open: " + err.Error())
				} else {
					t.notify("opened " + u)
				}
				return
			}
		}
	}
	if ev.btn&64 == 0 || ev.release {
		return
	}
	step := -3
	if ev.btn&1 == 0 {
		step = 3
	}
	if t.reader != nil {
		t.reader.Scroll(step, t.theme())
	} else {
		t.box.Scroll(step, t.theme())
	}
	t.dirty = true
}

// boxKey consumes one key while the message box float is up. Every key belongs
// to the view: nothing reaches a pane. It returns false when it needs more bytes
// for an escape sequence.
func (t *tui) boxKey() bool {
	if len(t.inbuf) == 0 {
		return true
	}
	if p := t.km().PrefixBytes(); len(p) > 0 && bytes.HasPrefix(t.inbuf, p) {
		// The prefix key closes the reader when it is up, else the view.
		t.inbuf = append([]byte(nil), t.inbuf[len(p):]...)
		t.pending = false
		if t.reader != nil {
			closeBodyReader(t)
			return true
		}
		closeBoxView(t)
		return true
	}
	if t.reader != nil && len(t.inbuf) == 1 && t.inbuf[0] == 0x1b {
		// A lone esc closes the reader at once: the reader binds no escape
		// sequences of its own beyond the page keys it shares, so waiting for
		// the pending-escape timeout buys nothing.
		t.inbuf = t.inbuf[1:]
		t.pending = false
		closeBodyReader(t)
		t.dirty = true
		return true
	}
	action, n, state := t.km().Match("box", t.inbuf)
	switch state {
	case keymap.Partial:
		if !t.pending {
			t.pending = true
			t.escAt = time.Now()
		}
		return false
	case keymap.Match:
		t.inbuf = append([]byte(nil), t.inbuf[n:]...)
		t.pending = false
	default:
		// An unbound key closes the view.
		t.inbuf = t.inbuf[1:]
		t.pending = false
		closeBoxView(t)
		return true
	}
	if t.reader != nil {
		// The reader takes the scroll keys and gives the close keys back to
		// the list page; anything else closes it too.
		t.readerAction(action)
		t.dirty = true
		return true
	}
	switch action {
	case "up", "up-arrow":
		t.box.Scroll(-1, t.theme())
	case "down", "down-arrow":
		t.box.Scroll(1, t.theme())
	case "page-up":
		t.box.Scroll(-t.box.Float.Rect.H, t.theme())
	case "page-down":
		t.box.Scroll(t.box.Float.Rect.H, t.theme())
	case "top":
		t.box.Scroll(-t.box.ListLen(), t.theme())
	case "bottom":
		t.box.Scroll(t.box.ListLen(), t.theme())
	case "toggle-page":
		t.box.TogglePage(t.theme())
	case "body":
		// An entry without a body opens nothing.
		openBodyReader(t)
	case "filter-notify":
		t.box.ToggleFilter(t.theme())
	case "jump-pane":
		t.boxJumpPane()
		return true
	case "clear-entry":
		t.boxClearEntry()
	case "clear-filter":
		t.boxClearFilter()
	default:
		closeBoxView(t)
		return true
	}
	t.dirty = true
	return true
}

// boxJumpPane focuses the pane the selected entry is about and closes the
// float. A key that names no pane, and a pane the session does not carry, raise
// a toast instead: the float stays where it is.
func (t *tui) boxJumpPane() {
	e, ok := t.box.Selected()
	if !ok {
		return
	}
	pane := ""
	if box.IsNotifyTopic(e.Key) {
		pane = strings.TrimPrefix(e.Key, box.NotifyPrefix)
	}
	if pane == "" {
		t.notify("no pane named by " + e.Key)
		return
	}
	if t.paneByID(pane) == nil {
		t.notify("no pane " + pane)
		return
	}
	closeBoxView(t)
	t.actionOn(pane, "focus", nil)
}

// boxClearEntry clears the selected entry's key through msg-clear and drops it
// from the recent cache, so the badge and the list agree until the reply
// refetches the log.
func (t *tui) boxClearEntry() {
	e, ok := t.box.Selected()
	if !ok {
		return
	}
	t.notifyRecentDropKey(e.Key)
	t.boxClear(box.ClearReq{Key: e.Key})
}

// boxClearFilter clears what the view shows: the notify branch while the filter
// is on, and the whole log otherwise. Rows the filter excludes are never named,
// so a filtered clear cannot touch a key the operator cannot see.
func (t *tui) boxClearFilter() {
	t.notifyRecent = nil
	if t.box.Filter() != "" {
		t.boxClear(box.ClearReq{KeyPrefix: box.NotifyPrefix})
		return
	}
	t.boxClear(box.ClearReq{})
}

// boxClear sends one msg-clear tagged with the view's route. The reply handler
// owns the refetch; a session without a connection clears nothing.
func (t *tui) boxClear(req box.ClearReq) {
	if t.conn == nil {
		return
	}
	body, err := json.Marshal(req)
	if err != nil {
		return
	}
	t.notifyID++
	t.notifyRouteTag(t.notifyID, notifyRouteClear)
	_ = t.conn.Send(wire.Msg{Type: "req", Action: "msg-clear", ID: t.notifyID, Res: body})
}

// openKeymapView opens the keymap view as a floating pane, or closes it when it
// is already up. The rows come from the client's keymap.
func openKeymapView(t *tui) {
	if t.snap == nil {
		t.disarm()
		return
	}
	if t.keys != nil {
		closeKeymapView(t)
		return
	}
	t.keys = keymapview.NewKeymapView(t.snap, t.km())
	t.AddFloatingPane(t.keys.Float)
	t.mode = "help"
	t.armed = false
	t.dirty = true
}

// closeKeymapView takes the float down and gives the keyboard back to the
// panes.
func closeKeymapView(t *tui) {
	if t.keys == nil {
		return
	}
	t.RemoveFloatingPane(t.keys.Float.ID)
	t.keys = nil
	t.mode = ""
	// The close key stays in the buffer while the pending timer waits. Drop
	// the whole buffer here, so a later keypress cannot forward a stray escape
	// to a pane.
	t.inbuf = nil
	t.pending = false
	t.dirty = true
	t.openDeferredAsk()
}

// keymapKey consumes one key while the keymap view is up. Every key belongs to
// the view: nothing reaches a pane. It returns false when it needs more bytes
// for an escape sequence.
func (t *tui) keymapKey() bool {
	if len(t.inbuf) == 0 {
		return true
	}
	if t.keys.Searching {
		return t.keymapSearchKey()
	}
	cfg := t.km()
	if p := cfg.PrefixBytes(); len(p) > 0 && bytes.HasPrefix(t.inbuf, p) {
		// The prefix key closes the view.
		t.inbuf = append([]byte(nil), t.inbuf[len(p):]...)
		t.pending = false
		closeKeymapView(t)
		return true
	}
	// A config that predates the help mode carries no bindings for the view.
	// The built-in mode takes over, so the view always scrolls and closes.
	if !cfg.HasMode("help") {
		cfg = builtinKeymap()
	}
	action, n, state := cfg.Match("help", t.inbuf)
	switch state {
	case keymap.Partial:
		if !t.pending {
			t.pending = true
			t.escAt = time.Now()
		}
		return false
	case keymap.Match:
		t.inbuf = append([]byte(nil), t.inbuf[n:]...)
		t.pending = false
	default:
		// An unbound key closes the view.
		t.inbuf = t.inbuf[1:]
		t.pending = false
		closeKeymapView(t)
		return true
	}
	switch action {
	case "up", "up-arrow":
		t.keys.Scroll(-1)
	case "down", "down-arrow":
		t.keys.Scroll(1)
	case "page-up":
		t.keys.Scroll(-t.keys.Float.Rect.H)
	case "page-down":
		t.keys.Scroll(t.keys.Float.Rect.H)
	case "top":
		t.keys.Scroll(-t.keys.RowCount())
	case "bottom":
		t.keys.Scroll(t.keys.RowCount())
	case "search":
		t.keys.StartSearch()
	case "next-match":
		t.keys.NextMatch(1)
	case "prev-match":
		t.keys.NextMatch(-1)
	default:
		// Every close action, and any action the view does not scroll with,
		// closes it.
		closeKeymapView(t)
		return true
	}
	t.dirty = true
	return true
}

// keymapSearchKey consumes one key of the query. Enter keeps the query, esc
// clears it, backspace edits and ctrl+u clears. Printable runes append whole,
// so the query holds the operator's language; the byte filter used to drop a
// multi-byte rune one byte at a time in silence. Every other control key is
// swallowed: nothing reaches a pane while the query is typed. It returns
// false while a multi-byte rune is still arriving.
func (t *tui) keymapSearchKey() bool {
	if len(t.inbuf) == 0 {
		return true
	}
	if t.inbuf[0] == 0x1b {
		t.keys.CancelSearch()
		t.inbuf = nil
		t.pending = false
		t.dirty = true
		return true
	}
	r, size, ok, wait := textRune(t.inbuf)
	if wait {
		// A multi-byte rune is still arriving. The expiry closes on a
		// stall, as it does for a pending escape.
		if !t.pending {
			t.pending = true
			t.escAt = time.Now()
		}
		return false
	}
	t.inbuf = t.inbuf[size:]
	t.pending = false
	switch r {
	case '\r', '\n':
		t.keys.AcceptSearch()
	case 0x7f, 0x08:
		t.keys.BackspaceQuery()
	case 0x15:
		t.keys.SetQuery("")
	case 0x17:
		t.keys.DeleteWordQuery()
	default:
		if ok {
			t.keys.AppendQuery(string(r))
		}
	}
	t.dirty = true
	return true
}

// keymapWheel scrolls the view from the wheel. A notch up goes toward the top,
// the way it goes back in scrollback. Every other mouse event is swallowed.
func (t *tui) keymapWheel(ev mouseEvent) {
	if t.keys == nil {
		return
	}
	t.updateLinkHover(ev.x-1, ev.y-1, ev.btn&16 != 0)
	if ev.btn&64 == 0 || ev.release {
		return
	}
	step := -3
	if ev.btn&1 == 0 {
		step = 3
	}
	t.keys.Scroll(step)
	t.dirty = true
}
