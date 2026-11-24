package client

// The shelf float: the panes set aside, one chip each on a strip row with the
// active member's screen under it. The server owns the shelf and the active
// index; the client draws them and asks the server to move, pop or resize.

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"dm/internal/cells"
	"dm/internal/hintbar"
	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/panel"
	"dm/internal/rail"
	"dm/internal/segment"
	"dm/internal/vt"
	"dm/internal/wire"
)

// shelfView is the shelf float: the panel the client draws, the member its
// screen shows, and the terminal size inside the box with the strip row taken
// off. A screen answer for any other member is dropped: it would land in a
// terminal nothing paints.
type shelfView struct {
	float  *panel.FloatingPane
	active string
	cols   int
	rows   int
}

// shelfActiveIndex is the index of the shelf's active member, clamped the way
// the server clamps the state so a stale snapshot cannot index past the slice.
func shelfActiveIndex(snap *wire.Snapshot, members []wire.PaneInfo) int {
	if len(members) == 0 {
		return 0
	}
	if snap == nil || snap.ShelfActive < 0 || snap.ShelfActive >= len(members) {
		return 0
	}
	return snap.ShelfActive
}

// shelfRect is where the shelf float sits. A centred shelf takes a share of
// the pane area and lands in the middle of it, so the rail, the tab bar and the
// status bar stay visible. The pane layout lays the box over the focused pane,
// inset one cell so its border lands on the pane's own frame. A focused pane
// too small for a strip and a row of screen falls back to the centred box.
func (t *tui) shelfRect() layout.Rect {
	return shelfRectFor(t.snap, t.km().ShelfCentered(), t.km().ShelfSize())
}

// shelfRectFor is the geometry under shelfRect, so a test can name a placement
// and a share without a config.
func shelfRectFor(snap *wire.Snapshot, centered bool, size int) layout.Rect {
	if snap == nil {
		return layout.Rect{}
	}
	if !centered {
		for i := range snap.Panes {
			p := &snap.Panes[i]
			if p.ID != snap.Focus || p.Shelf {
				continue
			}
			r := layout.Rect{X: p.Rect.X + 1, Y: p.Rect.Y + 1, W: p.Rect.W - 2, H: p.Rect.H - 2}
			if r.W >= minShelfWidth && r.H >= minShelfHeight {
				return r
			}
			break
		}
	}
	return centeredShelfRect(snap, size)
}

// The smallest shelf box worth drawing: enough width for a couple of chips and
// enough rows for the strip and three rows of screen. minShelfShare is the
// smallest share the geometry honours, matching the config's floor.
const (
	minShelfWidth  = 24
	minShelfHeight = 6
	minShelfShare  = 20
)

// centeredShelfRect centres the box over the pane area, at size percent of it.
// The panel draws its border one cell outside the rect, so the share is the
// whole box and the interior gives up two cells each way. A share too small for
// the strip grows to the minimum unless the area cannot hold it.
func centeredShelfRect(snap *wire.Snapshot, size int) layout.Rect {
	area := layout.PaneAreaForWithTabBar(snap.Cols, snap.Rows, snap.Rail)
	if area.W < 2 || area.H < 2 {
		return floatRect(snap)
	}
	if size < minShelfShare {
		size = minShelfShare
	}
	if size > 100 {
		size = 100
	}
	boxW := area.W * size / 100
	boxH := area.H * size / 100
	w, h := boxW-2, boxH-2
	if w < minShelfWidth {
		w = minShelfWidth
	}
	if h < minShelfHeight {
		h = minShelfHeight
	}
	if w > area.W-2 {
		w = area.W - 2
	}
	if h > area.H-2 {
		h = area.H - 2
	}
	x := area.X + (area.W-boxW)/2 + 1
	y := area.Y + (area.H-boxH)/2 + 1
	if x < area.X+1 {
		x = area.X + 1
	}
	if y < area.Y+1 {
		y = area.Y + 1
	}
	if x+w+1 > area.X+area.W {
		x = area.X + area.W - w - 1
	}
	if y+h+1 > area.Y+area.H {
		y = area.Y + area.H - h - 1
	}
	return layout.Rect{X: x, Y: y, W: w, H: h}
}

// shelfToggle opens the shelf float over the focused pane, or closes it when it
// is already up. An empty shelf has nothing to show, so it says so and opens
// nothing.
func (t *tui) shelfToggle() {
	if t.shelf != nil {
		t.closeShelfFloat()
		return
	}
	if t.snap == nil || t.conn == nil {
		t.disarm()
		return
	}
	members := rail.ShelfPanes(t.snap)
	if len(members) == 0 {
		t.notify("the shelf is empty")
		t.disarm()
		return
	}
	r := t.shelfRect()
	member := members[shelfActiveIndex(t.snap, members)].ID
	sv := &shelfView{active: member, cols: r.W, rows: r.H - 1}
	sv.float = &panel.FloatingPane{ID: "shelf", Rect: r, Active: true}
	sv.float.Paint = t.shelfPaint()
	t.shelf = sv
	t.AddFloatingPane(sv.float)
	t.mode = "shelf"
	t.armed = false
	t.inbuf = nil
	t.pending = false
	t.shelfBorders()
	t.summonShelf()
	t.dirty = true
}

// closeShelfFloat takes the shelf down and gives the keyboard back to the
// panes. The member keeps running: closing the shelf is not popping it.
func (t *tui) closeShelfFloat() {
	sv := t.shelf
	if sv == nil {
		return
	}
	t.RemoveFloatingPane(sv.float.ID)
	t.shelf = nil
	t.mode = ""
	// The close key stays in the buffer while the pending timer waits. Drop
	// the whole buffer, so a later keypress cannot forward a stray byte to a
	// pane.
	t.inbuf = nil
	t.pending = false
	t.dirty = true
	t.openDeferredAsk()
}

// summonShelf readies the active member's terminal at the box's size and asks
// the pane for its own screen and the size its program must reflow to. A pane
// the client had on screen before it was shelved already holds its bytes; a
// pane the client has never seen, which is every member after a restart, is
// filled from this screen.
func (t *tui) summonShelf() {
	sv := t.shelf
	if sv == nil || sv.active == "" || sv.cols < 1 || sv.rows < 1 {
		return
	}
	term := t.vts[sv.active]
	if term == nil {
		term = vt.NewTerm(sv.cols, sv.rows)
		t.vts[sv.active] = term
	}
	if c, r := term.Grid().Size(); c != sv.cols || r != sv.rows {
		resizeTerm(term, sv.cols, sv.rows)
	}
	if t.conn == nil {
		return
	}
	// The pane's own PTY takes the box's size through the resize frame a float
	// uses, so its program reflows to the cells the shelf draws.
	_ = t.conn.Send(wire.Msg{Type: "resize", Target: sv.active, Cols: sv.cols, Rows: sv.rows})
	_ = t.conn.Send(wire.Msg{Type: "req", Action: "screen", Target: sv.active})
}

// syncShelf keeps the float on the active member, fit to the focused pane and
// titled with the count. A shelf that emptied closes; a member or a box that
// moved is summoned again at the new size.
func (t *tui) syncShelf(snap *wire.Snapshot) {
	sv := t.shelf
	if sv == nil {
		return
	}
	members := rail.ShelfPanes(snap)
	if len(members) == 0 {
		t.closeShelfFloat()
		return
	}
	at := shelfActiveIndex(snap, members)
	r := shelfRectFor(snap, t.km().ShelfCentered(), t.km().ShelfSize())
	moved := r != sv.float.Rect
	if moved {
		sv.float.Rect = r
		sv.cols, sv.rows = r.W, r.H-1
	}
	if id := members[at].ID; id != sv.active || moved {
		sv.active = id
		t.summonShelf()
	}
	t.shelfBorders()
	t.dirty = true
}

// shelfBorders writes the panel's title and hint: the title names how many
// panes are set aside, the hint names the chords the shelf answers.
func (t *tui) shelfBorders() {
	sv := t.shelf
	if sv == nil || sv.float == nil {
		return
	}
	n := 0
	if t.snap != nil {
		n = len(rail.ShelfPanes(t.snap))
	}
	title := fmt.Sprintf(" shelf · %d ", n)
	if n == 1 {
		title = " shelf · 1 "
	}
	sv.float.Title = cells.Clip(title, sv.float.Rect.W-2)
	prefix := t.km().PrefixName()
	sv.float.Hint = &hintbar.Bar{Hints: []hintbar.Hint{
		{Keys: []string{prefix + " F"}, Label: "closes"},
		{Keys: []string{prefix + " n/p"}, Label: "moves"},
		{Keys: []string{prefix + " G"}, Label: "pops"},
	}}
}

// shelfPaint is the shelf float's content painter: the chip strip on its first
// row and the active member's screen under it. The strip is composed from the
// same helpers the dashboard paints with: segment runs over cells, one chip per
// member.
func (t *tui) shelfPaint() func(*bytes.Buffer, layout.Rect) {
	return func(buf *bytes.Buffer, inner layout.Rect) {
		sv := t.shelf
		if sv == nil || t.snap == nil || inner.H < 1 {
			return
		}
		t.paintShelfStrip(buf, inner)
		body := layout.Rect{X: inner.X, Y: inner.Y + 1, W: inner.W, H: inner.H - 1}
		if body.H > 0 {
			drawPaneFrom(buf, t.vts[sv.active], body, 0, nil, nil)
		}
	}
}

// paintShelfStrip paints the chip row: the shelf mark, then one chip per member
// with its state glyph and name. The active chip carries the focus ground and
// the bold weight, so the member whose screen fills the box below is the one
// that reads as selected. The row is elided to the box's width as one run set.
func (t *tui) paintShelfStrip(buf *bytes.Buffer, inner layout.Rect) {
	th, set := t.theme(), t.glyphSet()
	p := th.Palette
	members := rail.ShelfPanes(t.snap)
	sv := t.shelf
	groups := make([][]segment.Seg, 0, len(members)+1)
	groups = append(groups, []segment.Seg{{Text: rail.ShelfMark(set), FG: p.BarAccent, BG: p.RailBG}})
	for _, m := range members {
		status := m.Status
		if status == "" {
			status = "unknown"
		}
		glyph, colour := rail.RailGlyph(th, status, set)
		name := m.Name
		if name == "" {
			name = m.ID
		}
		chip := []segment.Seg{
			{Text: glyph, FG: colour, BG: p.RailBG},
			{Text: " " + name, FG: p.RailDim, BG: p.RailBG},
		}
		if sv != nil && m.ID == sv.active {
			chip = []segment.Seg{
				{Text: glyph, FG: colour, BG: p.RailFocusBG},
				{Text: " " + name, FG: p.RailText, BG: p.RailFocusBG, Bold: true},
			}
		}
		groups = append(groups, chip)
	}
	segs := segment.Join(groups, segment.Seg{Text: " ", FG: p.RailDim, BG: p.RailBG})
	if segment.SegCells(segs) > inner.W {
		segs = segment.ElideSegs(th, segs, inner.W)
	}
	fmt.Fprintf(buf, "\x1b[%d;%dH", inner.Y+1, inner.X+1)
	buf.WriteString("\x1b[0m")
	buf.WriteString(groundSGR(p.RailBG))
	for _, s := range segs {
		segment.Write(buf, s)
	}
	if n := inner.W - segment.SegCells(segs); n > 0 {
		buf.WriteString(strings.Repeat(" ", n))
	}
	buf.WriteString("\x1b[0m")
}

// shelfKey consumes one read in shelf mode. The prefix is the only byte the
// client keeps for itself: with it, F closes the shelf, n and p move the active
// member and G pops it. Every other byte is the active member's own input and
// goes to it whole.
func (t *tui) shelfKey() bool {
	sv := t.shelf
	if sv == nil {
		return true
	}
	prefix := t.km().PrefixBytes()
	if len(prefix) == 0 || !bytes.HasPrefix(t.inbuf, prefix) {
		keys := append([]byte(nil), t.inbuf...)
		t.inbuf = t.inbuf[:0]
		t.pending = false
		if t.conn != nil && sv.active != "" {
			_ = t.conn.Send(wire.Msg{Type: "keys", Target: sv.active, Keys: keys})
		}
		return true
	}
	rest := t.inbuf[len(prefix):]
	if len(rest) == 0 {
		// The chord key is still arriving. The prefix stays in the buffer so
		// the next read still reads as a chord; a prefix that never finishes
		// expires and is dropped, leaving the shelf up.
		if !t.pending {
			t.pending = true
			t.escAt = time.Now()
		}
		return false
	}
	action, n, state := t.km().Match("main", rest)
	if state == keymap.Match && escapeRun(rest) > n {
		// The match covered only the head of one escape sequence, so the key as
		// a whole has no binding: alt+x must not run the esc action and leave
		// its tail for the member. armedKey treats it the same way.
		state = keymap.NoMatch
	}
	switch state {
	case keymap.Partial:
		// The chord key is still arriving; the buffer keeps the prefix so the
		// rest of the sequence still matches against it.
		if !t.pending {
			t.pending = true
			t.escAt = time.Now()
		}
		return false
	case keymap.Match:
		t.inbuf = append([]byte(nil), rest[n:]...)
		t.pending = false
		t.shelfAction(action)
		return true
	}
	// The prefix is the chrome's byte, so an unbound chord is spent here and
	// nothing reaches the member. The whole key goes, not one byte: the tail of
	// an escape sequence such as alt+x would otherwise arrive as typed input.
	cut := escapeRun(rest)
	if cut < 1 {
		cut = 1
	}
	chord := string(rest[:cut])
	t.inbuf = append([]byte(nil), rest[cut:]...)
	t.pending = false
	t.notify(fmt.Sprintf("no shelf binding for %q", chord))
	return true
}

// shelfAction runs the one action a shelf chord named. Anything else withdraws
// the chord without touching the member's screen.
func (t *tui) shelfAction(action string) {
	switch action {
	case "toggle-shelf":
		t.closeShelfFloat()
	case "pop-pane":
		t.popShelf()
	case "next-pane":
		t.moveShelf(1)
	case "prev-pane":
		t.moveShelf(-1)
	case "reload":
		// A size or placement change is what the operator wants to see now,
		// so the config key works with the box up. reload refits the shelf.
		if err := t.reload(); err != nil {
			t.notify(err.Error())
		}
	default:
		t.notify(action + " is not a shelf key")
	}
}

// moveShelf moves the shelf's active member by step and summons it. The server
// owns the index: the client asks it to focus the member it wants, and the
// snapshot that comes back names it.
func (t *tui) moveShelf(step int) {
	if t.conn == nil || t.snap == nil {
		return
	}
	members := rail.ShelfPanes(t.snap)
	if len(members) < 2 {
		return
	}
	at := shelfActiveIndex(t.snap, members)
	next := members[(at+step+len(members))%len(members)]
	_ = t.conn.Send(wire.Msg{Type: "req", Action: "focus", Target: next.ID})
}

// popShelf puts the active member back in the layout and closes the float. The
// server splits the focused pane to the right with it and focuses it.
func (t *tui) popShelf() {
	sv := t.shelf
	if sv == nil || t.conn == nil || sv.active == "" {
		return
	}
	id := sv.active
	t.closeShelfFloat()
	_ = t.conn.Send(wire.Msg{Type: "req", Action: "pop-pane", Target: id})
	t.disarm()
}

// shelfPaste sends a pasted block to the active member, framed only when that
// pane asked for bracketed paste.
func (t *tui) shelfPaste(seq []byte) {
	sv := t.shelf
	if sv == nil || t.conn == nil || sv.active == "" {
		return
	}
	keys := t.pasteKeys(seq, sv.active)
	if len(keys) == 0 {
		return
	}
	_ = t.conn.Send(wire.Msg{Type: "keys", Target: sv.active, Keys: keys})
}

// applyShelfScreen restores a shelved pane's screen into the terminal the shelf
// float draws it in. A pane the shelf no longer shows drops the screen: it
// would land in a terminal nothing paints.
func (t *tui) applyShelfScreen(id string, snap *vt.Snapshot) {
	sv := t.shelf
	if sv == nil || snap == nil || id != sv.active || sv.cols < 1 || sv.rows < 1 {
		return
	}
	term := t.vts[id]
	if term == nil {
		term = vt.NewTerm(sv.cols, sv.rows)
		t.vts[id] = term
	}
	if err := term.Restore(snap); err != nil {
		return
	}
	if c, r := term.Grid().Size(); c != sv.cols || r != sv.rows {
		resizeTerm(term, sv.cols, sv.rows)
	}
	t.dirty = true
}

// shelfChipSpan is the cell span of the status bar's shelf chip, counted from
// the bar's left block. A bar too narrow to paint the chip answers an empty
// span, so a click never runs a chip the operator cannot see.
func (t *tui) shelfChipSpan() (int, int) {
	if t.snap == nil || t.snap.Cols < 1 || t.snap.Rows < 1 {
		return 0, 0
	}
	th, set := t.theme(), t.glyphSet()
	budget := barLeftBudget(th, t.snap, set, t.armed, t.barInfo(), t.barBadgeOf(th, set))
	l := barLeftFor(th, t.snap, set)
	segs := l.fit(th, budget)
	return l.shelfSpan(segment.SegCells(segs))
}

// shelfChipAt reports whether x is inside the status bar's shelf chip.
func (t *tui) shelfChipAt(x int) bool {
	lo, hi := t.shelfChipSpan()
	return hi > lo && x >= lo && x < hi
}
