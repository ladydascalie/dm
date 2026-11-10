package client

// The notification centre: a summoned float that lists the recent notify/
// entries newest first, one row per entry. The toast covers arrival; the
// centre covers browsing and unread state.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dm/internal/box"
	"dm/internal/boxview"
	"dm/internal/cells"
	"dm/internal/keymap"
	"dm/internal/panel"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"
)

// notifyCentreLimit caps one centre read: the recent entries, not the log.
const notifyCentreLimit = 50

// notifyCentre is the open float: the entries the server last sent, the row
// the operator has selected, and the pane that draws it.
type notifyCentre struct {
	entries []box.Entry
	sel     int
	// read is the client's read mark: the seq below which every entry is
	// read. The tui owns it; the copy here keeps the paint honest.
	read  int
	Float *panel.FloatingPane
}

// newNotifyCentre opens the float on a screen. The list stays empty until the
// reply lands.
func newNotifyCentre(snap *wire.Snapshot, th theme.Theme) *notifyCentre {
	c := &notifyCentre{Float: &panel.FloatingPane{ID: "notif", Active: true}}
	c.Sync(snap, th)
	return c
}

// Sync fits the float to the screen after a snapshot or a rail change.
func (c *notifyCentre) Sync(snap *wire.Snapshot, th theme.Theme) {
	c.Float.Rect = boxview.BoxViewRect(snap, len(c.entries))
	if c.sel >= len(c.entries) {
		c.sel = len(c.entries) - 1
	}
	if c.sel < 0 {
		c.sel = 0
	}
	c.paint(th)
}

// paint rewrites the float's terminal from the rows. Every row is placed
// absolutely, so a moved selection repaints the window whole. Unselected rows
// sit in the theme's dim colour, the way the box paints its rows.
func (c *notifyCentre) paint(th theme.Theme) {
	r := c.Float.Rect
	c.Float.Title = c.title()
	c.Float.Hint = " j/k move  enter body  g pane  m box  x clear  X clear all  esc close "
	if r.W < 1 || r.H < 1 {
		c.Float.VT = vt.NewTerm(1, 1)
		return
	}
	term := vt.NewTerm(r.W, r.H)
	var buf bytes.Buffer
	for row := 0; row < r.H; row++ {
		i := row
		if i >= len(c.entries) {
			break
		}
		fmt.Fprintf(&buf, "\x1b[%d;1H", row+1)
		mark := " "
		if c.unreadAt(i) {
			mark = "*"
		}
		// The selected row keeps its full intensity; the rest sit dim, the
		// way the box paints its rows.
		if i == c.sel {
			buf.WriteString("\x1b[0m")
		} else {
			buf.WriteString("\x1b[0m" + th.Palette.RailDim.FG())
		}
		buf.WriteString(cells.Clip(notifyRowText(c.entries[i], mark), r.W))
	}
	term.Write(buf.Bytes())
	term.Flush()
	c.Float.VT = term
}

// notifyRowText is one row: unread mark, state, pane id, app, age, then the
// summary the entry carries.
func notifyRowText(e box.Entry, mark string) string {
	pane := strings.TrimPrefix(e.Key, box.NotifyPrefix)
	app := e.From
	if app == "" {
		app = "-"
	}
	return fmt.Sprintf("%s %-7s %-5s %-10s %4s  %s", mark, e.State, pane,
		app, notifyAge(e.At), e.Msg)
}

// notifyAge is the entry's age as a short suffix: 42s, 7m, 3h, 2d.
func notifyAge(at time.Time) string {
	if at.IsZero() {
		return "-"
	}
	d := time.Since(at)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// title is the count in the top border and the unread among them.
func (c *notifyCentre) title() string {
	n := len(c.entries)
	head := " notifications  empty"
	switch n {
	case 0:
	case 1:
		head = " notifications  1 entry"
	default:
		head = fmt.Sprintf(" notifications  %d entries", n)
	}
	if u := c.unread(); u > 0 {
		head += fmt.Sprintf("  %d unread", u)
	}
	return head
}

// unread counts the entries past the client's read mark.
func (c *notifyCentre) unread() int {
	n := 0
	for i := range c.entries {
		if c.unreadAt(i) {
			n++
		}
	}
	return n
}

// unreadAt reports whether the row is unread: the toast layer saw it but the
// operator has not opened it here yet.
func (c *notifyCentre) unreadAt(i int) bool {
	return c.entries[i].Seq > c.read
}

// notifPaint repaints the centre with the client's read mark and marks the
// frame dirty.
func (t *tui) notifPaint() {
	if t.ncentre == nil {
		return
	}
	t.ncentre.read = t.notifyRead
	t.ncentre.paint(t.theme())
	t.dirty = true
}

// Move moves the selection by n rows and repaints.
func (c *notifyCentre) Move(n int, th theme.Theme) {
	c.sel += n
	if c.sel >= len(c.entries) {
		c.sel = len(c.entries) - 1
	}
	if c.sel < 0 {
		c.sel = 0
	}
	c.paint(th)
}

// selected is the entry the operator acts on.
func (c *notifyCentre) selected() (box.Entry, bool) {
	if c.sel < 0 || c.sel >= len(c.entries) {
		return box.Entry{}, false
	}
	return c.entries[c.sel], true
}

// openNotifyCentre summons the centre, or closes it when it is already up.
// Opening asks for the recent notify/ entries; the reply fills the float.
func openNotifyCentre(t *tui) {
	if t.snap == nil {
		t.disarm()
		return
	}
	if t.ncentre != nil {
		closeNotifyCentre(t)
		return
	}
	t.ncentre = newNotifyCentre(t.snap, t.theme())
	t.AddFloatingPane(t.ncentre.Float)
	t.mode = "notif"
	t.armed = false
	t.notifyCentreRequest()
	t.dirty = true
}

// closeNotifyCentre takes the float down, gives the keyboard back to the
// panes and marks what the operator saw read, so the list opens clean next
// time unless something newer arrived.
func closeNotifyCentre(t *tui) {
	if t.ncentre == nil {
		return
	}
	for _, e := range t.ncentre.entries {
		if e.Seq > t.notifyRead {
			t.notifyRead = e.Seq
		}
	}

	t.RemoveFloatingPane(t.ncentre.Float.ID)
	t.ncentre = nil
	t.mode = ""
	// The close key stays in the buffer while the pending timer waits. Drop
	// the whole buffer here, so a later keypress cannot forward a stray
	// escape to a pane.
	t.inbuf = nil
	t.pending = false
	t.dirty = true
}

// notifyCentreRequest asks the server for the recent notify/ entries.
func (t *tui) notifyCentreRequest() {
	if t.conn == nil {
		return
	}
	t.notifyID++
	t.notifyRouteTag(t.notifyID, notifyRouteCentre)
	_ = t.conn.Send(wire.Msg{
		Type: "req", Action: "msg-list", ID: t.notifyID,
		Res: []byte(fmt.Sprintf(`{"keyPrefix":"notify/","limit":%d}`, notifyCentreLimit)),
	})
}

// takeNotifyCentreList fills the float from the reply. A malformed reply and
// an error reply leave the picture alone; an error becomes a toast.
func (t *tui) takeNotifyCentreList(m wire.Msg) {
	if t.ncentre == nil {
		return
	}
	if !m.OK {
		t.notify(m.Error)
		return
	}
	var rep box.ListReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.notify("notifications: " + err.Error())
		return
	}
	t.ncentre.entries = rep.Entries
	// The centre's list is the server's own recent window, so it is the
	// truth the badge counts from while the centre is open.
	t.notifyRecent = append(t.notifyRecent[:0], rep.Entries...)
	if t.ncentre.sel >= len(rep.Entries) {
		t.ncentre.sel = len(rep.Entries) - 1
	}
	if t.ncentre.sel < 0 {
		t.ncentre.sel = 0
	}
	t.ncentre.Sync(t.snap, t.theme())
	t.notifPaint()
}

// notifKey consumes one key while the centre is up. It returns false when it
// needs more bytes for an escape sequence.
func (t *tui) notifKey() bool {
	if len(t.inbuf) == 0 {
		return true
	}
	if p := t.km().PrefixBytes(); len(p) > 0 && bytes.HasPrefix(t.inbuf, p) {
		// The prefix key closes the reader when it is up, else the centre.
		t.inbuf = append([]byte(nil), t.inbuf[len(p):]...)
		t.pending = false
		if t.reader != nil {
			closeBodyReader(t)
			return true
		}
		closeNotifyCentre(t)
		return true
	}
	if t.reader != nil && len(t.inbuf) == 1 && t.inbuf[0] == 0x1b {
		t.inbuf = t.inbuf[1:]
		t.pending = false
		closeBodyReader(t)
		t.dirty = true
		return true
	}
	if len(t.inbuf) == 1 && t.inbuf[0] == 0x1b {
		// A lone esc closes the centre at once: the centre binds no escape
		// sequences of its own beyond the page keys it shares, so waiting
		// for the pending-escape timeout buys nothing.
		t.inbuf = t.inbuf[1:]
		t.pending = false
		closeNotifyCentre(t)
		t.dirty = true
		return true
	}
	// The keys the centre binds itself: the box keymap owns j/k and the
	// arrows, so these come off the top before it answers.
	switch t.inbuf[0] {
	case 'N':
		t.inbuf = t.inbuf[1:]
		t.pending = false
		closeNotifyCentre(t)
		return true
	case '\r':
		t.inbuf = t.inbuf[1:]
		t.pending = false
		t.notifOpenBody()
		t.dirty = true
		return true
	case 'g':
		t.inbuf = t.inbuf[1:]
		t.pending = false
		t.notifJumpPane()
		t.dirty = true
		return true
	case 'G':
		t.inbuf = t.inbuf[1:]
		t.pending = false
		t.ncentre.Move(len(t.ncentre.entries), t.theme())
		t.dirty = true
		return true
	case 'm':
		t.inbuf = t.inbuf[1:]
		t.pending = false
		t.notifToBox()
		t.dirty = true
		return true
	case 'x':
		t.inbuf = t.inbuf[1:]
		t.pending = false
		t.notifClearKey()
		t.dirty = true
		return true
	case 'X':
		t.inbuf = t.inbuf[1:]
		t.pending = false
		t.notifClearAll()
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
		// An unbound key closes the centre.
		t.inbuf = t.inbuf[1:]
		t.pending = false
		closeNotifyCentre(t)
		return true
	}
	if t.reader != nil {
		// The reader takes the scroll keys and gives the close keys back to
		// the centre.
		t.readerAction(action)
		t.dirty = true
		return true
	}
	switch action {
	case "up", "up-arrow":
		t.ncentre.Move(-1, t.theme())
	case "down", "down-arrow":
		t.ncentre.Move(1, t.theme())
	case "page-up":
		t.ncentre.Move(-t.ncentre.Float.Rect.H, t.theme())
	case "page-down":
		t.ncentre.Move(t.ncentre.Float.Rect.H, t.theme())
	case "close", "close-esc", "close-ctrl-c":
		closeNotifyCentre(t)
	default:
		closeNotifyCentre(t)
		return true
	}
	t.dirty = true
	return true
}

// notifWheel scrolls the centre, or the reader over it.
func (t *tui) notifWheel(ev mouseEvent) {
	if t.ncentre == nil {
		return
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
		t.ncentre.Move(step, t.theme())
	}
	t.dirty = true
}

// notifOpenBody opens the selected entry's body in the reader overlay and
// clears the entry's unread mark.
func (t *tui) notifOpenBody() {
	e, ok := t.ncentre.selected()
	if !ok {
		return
	}
	if t.reader != nil {
		closeBodyReader(t)
	}
	if e.Seq > t.notifyRead {
		t.notifyRead = e.Seq
	}
	t.reader = newBodyReader(e, t.ncentre.Float.Rect, t.theme())
	t.AddFloatingPane(t.reader.Float)
}

// notifJumpPane focuses the pane the selected entry is about and closes the
// centre. A bare notify/ topic names no pane, and an unknown pane gets a
// toast instead of a silent jump.
func (t *tui) notifJumpPane() {
	e, ok := t.ncentre.selected()
	if !ok {
		return
	}
	pane := strings.TrimPrefix(e.Key, box.NotifyPrefix)
	if pane == "" {
		return
	}
	if t.paneByID(pane) == nil {
		t.notify("no pane " + pane)
		return
	}
	closeNotifyCentre(t)
	t.actionOn(pane, "focus", nil)
}

// notifToBox hands the entry to the box view: it closes the centre, opens the
// log and asks it to land with the entry at the top of the window.
func (t *tui) notifToBox() {
	e, ok := t.ncentre.selected()
	if !ok {
		return
	}
	closeNotifyCentre(t)
	t.boxJumpSeq = e.Seq
	openBoxView(t)
}

// notifClearKey clears the selected entry's key through msg-clear and lets
// the reply refetch the centre, so the row leaves the list.
func (t *tui) notifClearKey() {
	e, ok := t.ncentre.selected()
	if !ok {
		return
	}
	t.notifyRecentDropKey(e.Key)
	t.notifyClear(box.ClearReq{Key: e.Key})
}

// notifClearAll clears the whole notify/ topic and lets the reply refetch the
// centre, so the list empties.
func (t *tui) notifClearAll() {
	t.notifyRecent = nil
	t.notifyClear(box.ClearReq{KeyPrefix: box.NotifyPrefix})
}

// notifyClear sends one msg-clear tagged with the centre's route. The reply
// handler owns the refetch; a session without a connection clears nothing.
func (t *tui) notifyClear(req box.ClearReq) {
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

// notifyRecentMerge folds a msg-list reply into the client's recent entries.
// The reply is newest first; the known seqs are skipped and the cache keeps
// its newest-first order, capped at notifyCentreLimit.
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
	if len(t.notifyRecent) > notifyCentreLimit {
		t.notifyRecent = t.notifyRecent[:notifyCentreLimit]
	}
}

// notifyRecentDropKey drops every cached entry under key, the rows a msg-clear
// with that key removed server-side. The badge follows the centre's list, so
// the cache cannot keep a cleared entry alive.
func (t *tui) notifyRecentDropKey(key string) {
	out := make([]box.Entry, 0, len(t.notifyRecent))
	for _, e := range t.notifyRecent {
		if e.Key != key {
			out = append(out, e)
		}
	}
	t.notifyRecent = out
}

// notifyUnread counts the cached entries past the read mark: the same rows
// the centre marks unread, so the bar badge and the marks never drift.
func (t *tui) notifyUnread() int {
	n := 0
	for _, e := range t.notifyRecent {
		if e.Seq > t.notifyRead {
			n++
		}
	}
	return n
}
