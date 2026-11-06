package main

// The client side of the message box float: opening and closing it, the
// requests it sends, the replies it takes, and the keys and wheel it consumes.
// The view itself lives in internal/boxview.

import (
	"bytes"
	"encoding/json"
	"time"

	"dm/internal/box"
	"dm/internal/boxview"
	"dm/internal/keymap"
	"dm/internal/wire"
)

// openBoxView opens the message box as a floating pane, or closes it when it is
// already up. The view asks for the whole log and the watcher list; the replies
// fill it.
func openBoxView(t *tui) {
	if t.snap == nil {
		t.disarm()
		return
	}
	if t.box != nil {
		closeBoxView(t)
		return
	}
	t.box = boxview.NewBoxView(t.snap, t.theme())
	t.AddFloatingPane(t.box.Float)
	t.mode = "box"
	t.armed = false
	t.boxRequest()
	t.boxWatchers(time.Now())
	t.dirty = true
}

// closeBoxView takes the float down and gives the keyboard back to the panes.
func closeBoxView(t *tui) {
	if t.box == nil {
		return
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
				if err := openURL(u); err != nil {
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
	t.box.Scroll(step, t.theme())
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
		// The prefix key closes the view.
		t.inbuf = append([]byte(nil), t.inbuf[len(p):]...)
		t.pending = false
		closeBoxView(t)
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
	default:
		closeBoxView(t)
		return true
	}
	t.dirty = true
	return true
}
