package main

// The full keymap view as a floating pane. It lists every mode the config
// carries and every binding in each, so a reader sees all the keys the client
// takes without leaving the session. The view is read-only: keys scroll it and
// close it, and the wheel scrolls it.

import (
	"bytes"
	"dm/internal/cells"
	"fmt"
	"strings"
	"time"

	"github.com/hinshun/vt10x"
)

const (
	// keymapViewID is the view float's id.
	keymapViewID = "keymap"

	// keymapViewW is the widest rect the view takes, and keymapViewGap is the
	// rows it keeps clear of the screen's edges.
	keymapViewW   = 96
	keymapViewGap = 6
)

// keymapView is the open keymap float: the rows built when it opened, the
// first row in view, and the float that draws them.
type keymapView struct {
	rows      []string
	off       int
	query     string
	matches   []int
	match     int
	searching bool
	float     *FloatingPane
	snap      *Snapshot
}

// newKeymapView opens the float on a screen. The rows are built once, so a
// scroll never re-reads the config.
func newKeymapView(snap *Snapshot, cfg *Config) *keymapView {
	k := &keymapView{float: &FloatingPane{ID: keymapViewID, Active: true}, snap: snap}
	k.rows = keymapRows(cfg)
	k.sync(snap)
	return k
}

// keymapRows builds the view's rows: the prefix, a blank, then every mode as a
// header and one "key  label" row per action, a blank between modes. The action
// row carries the key the config displays, padded to the widest key, and the
// action name with its hyphens turned into spaces.
func keymapRows(cfg *Config) []string {
	modes := cfg.Keymaps()
	w := 0
	for _, m := range modes {
		for _, a := range m.Actions {
			if n := cells.Cells(a.Key); n > w {
				w = n
			}
		}
	}
	rows := []string{" prefix " + cfg.PrefixName(), ""}
	for i, m := range modes {
		if i > 0 {
			rows = append(rows, "")
		}
		rows = append(rows, " ["+m.Name+"]")
		for _, a := range m.Actions {
			rows = append(rows, fmt.Sprintf(" %s  %s",
				cells.Pad(a.Key, w), strings.ReplaceAll(a.Action, "-", " ")))
		}
	}
	return rows
}

// keymapViewRect is where the float sits: centred over the pane area, one cell
// clear of the rail, at most 96 columns and rows-6. The height follows n, the
// row count, so a short list reads in a short box. A list longer than the cap
// scrolls.
func keymapViewRect(snap *Snapshot, n int) Rect {
	left := snap.Rail
	if left < 0 {
		left = 0
	}
	w := snap.Cols - left - 6
	if w > keymapViewW {
		w = keymapViewW
	}
	if w < 20 {
		w = snap.Cols - left - 2
	}
	if w < 8 {
		w = snap.Cols - 2
	}
	h := snap.Rows - keymapViewGap
	if n < h {
		h = n
	}
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	x := left + (snap.Cols-left-w)/2
	if x < 1 {
		x = 1
	}
	y := (snap.Rows - h) / 2
	if y < 1 {
		y = 1
	}
	return clampRect(Rect{X: x, Y: y, W: w, H: h}, snap.Cols, snap.Rows)
}

// sync fits the float to the screen after a snapshot or a rail change and
// repaints the window.
func (k *keymapView) sync(snap *Snapshot) {
	k.snap = snap
	k.float.Rect = keymapViewRect(snap, len(k.rows))
	k.clamp()
	k.paint()
}

// scroll moves the window by n rows. The offset stops at the first row and at
// the last row that still fills the rect.
func (k *keymapView) scroll(n int) {
	k.off += n
	k.clamp()
	k.paint()
}

// clamp keeps the window on the rows.
func (k *keymapView) clamp() {
	max := len(k.rows) - k.float.Rect.H
	if max < 0 {
		max = 0
	}
	if k.off > max {
		k.off = max
	}
	if k.off < 0 {
		k.off = 0
	}
}

// startSearch begins a new query. The old query and its matches go.
func (k *keymapView) startSearch() {
	k.searching = true
	k.setQuery("")
}

// cancelSearch drops the query and its highlight.
func (k *keymapView) cancelSearch() {
	k.searching = false
	k.setQuery("")
}

// acceptSearch keeps the query and its highlight and returns to normal keys.
func (k *keymapView) acceptSearch() {
	k.searching = false
	k.paint()
}

// appendQuery adds one printable byte to the query.
func (k *keymapView) appendQuery(s string) {
	k.setQuery(k.query + s)
}

// backspaceQuery removes the last byte of the query.
func (k *keymapView) backspaceQuery() {
	if k.query == "" {
		return
	}
	k.setQuery(k.query[:len(k.query)-1])
}

// setQuery replaces the query, recomputes the matching binding rows and puts
// the first match in view. An empty query clears the highlight.
func (k *keymapView) setQuery(q string) {
	k.query = strings.ToLower(q)
	k.matches = k.matches[:0]
	if k.query != "" {
		for i, row := range k.rows {
			if keymapHeader(row) || strings.TrimSpace(row) == "" {
				continue
			}
			if strings.Contains(strings.ToLower(row), k.query) {
				k.matches = append(k.matches, i)
			}
		}
	}
	k.match = 0
	k.jumpToMatch()
	k.paint()
}

// jumpToMatch centres the current match in the window.
func (k *keymapView) jumpToMatch() {
	if len(k.matches) == 0 {
		return
	}
	k.off = k.matches[k.match] - k.float.Rect.H/2
	k.clamp()
}

// nextMatch moves to the next or previous matching row and centres it.
func (k *keymapView) nextMatch(dir int) {
	if len(k.matches) == 0 {
		return
	}
	k.match = (k.match + dir + len(k.matches)) % len(k.matches)
	k.jumpToMatch()
	k.paint()
}

// title is the float's top border: keymaps, plus the live query and the
// position of the active match.
func (k *keymapView) title() string {
	if k.query == "" {
		return " keymaps "
	}
	pos := "0/0"
	if len(k.matches) > 0 {
		pos = fmt.Sprintf("%d/%d", k.match+1, len(k.matches))
	}
	return " keymaps  /" + k.query + "  " + pos + " "
}

// hint is the float's bottom border: the normal keys, the search keys while
// the query is typed, or the match keys after enter keeps it.
func (k *keymapView) hint() string {
	switch {
	case k.searching:
		return " enter keep  esc clear "
	case k.query != "":
		return " n/N next  / edit  esc close "
	default:
		return " j/k scroll  g/G ends  / search  esc close "
	}
}

// style is the SGR a row paints in. A mode header is bright white, the active
// match is black on yellow, every other match is black on white, and a normal
// binding is dim.
func (k *keymapView) style(i int) string {
	switch {
	case keymapHeader(k.rows[i]):
		return "\x1b[0m\x1b[97m"
	case k.activeMatch(i):
		return "\x1b[0m\x1b[30;103m"
	case k.query != "" && strings.Contains(strings.ToLower(k.rows[i]), k.query):
		return "\x1b[0m\x1b[30;107m"
	default:
		return "\x1b[0m\x1b[90m"
	}
}

// activeMatch reports whether row i is the match n/N last moved to.
func (k *keymapView) activeMatch(i int) bool {
	return len(k.matches) > 0 && k.matches[k.match] == i
}

// paint rewrites the float's terminal from the rows. Every row is placed
// absolutely, so a view that scrolled paints its new window whole. A mode
// header is bright; a binding row is dim.
func (k *keymapView) paint() {
	r := k.float.Rect
	k.float.Title = k.title()
	k.float.Hint = k.hint()
	if r.W < 1 || r.H < 1 {
		k.float.VT = vt10x.New(vt10x.WithSize(1, 1))
		return
	}
	vt := vt10x.New(vt10x.WithSize(r.W, r.H))
	var buf bytes.Buffer
	for row := 0; row < r.H; row++ {
		i := k.off + row
		if i >= len(k.rows) {
			break
		}
		style := k.style(i)
		fmt.Fprintf(&buf, "\x1b[%d;1H%s%s", row+1, style, cells.Clip(k.rows[i], r.W))
	}
	_, _ = vt.Write(buf.Bytes())
	k.float.VT = vt
}

// keymapHeader reports whether a row is a header rather than a binding.
func keymapHeader(row string) bool {
	return strings.HasPrefix(row, " prefix ") || (strings.HasPrefix(row, " [") && strings.HasSuffix(row, "]"))
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
	t.keys = newKeymapView(t.snap, t.km())
	t.AddFloatingPane(t.keys.float)
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
	t.RemoveFloatingPane(t.keys.float.ID)
	t.keys = nil
	t.mode = ""
	// The close key stays in the buffer while the pending timer waits. Drop
	// the whole buffer here, so a later keypress cannot forward a stray escape
	// to a pane.
	t.inbuf = nil
	t.pending = false
	t.dirty = true
}

// keymapKey consumes one key while the keymap view is up. Every key belongs to
// the view: nothing reaches a pane. It returns false when it needs more bytes
// for an escape sequence.
func (t *tui) keymapKey() bool {
	if len(t.inbuf) == 0 {
		return true
	}
	if t.keys.searching {
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
	case Partial:
		if !t.pending {
			t.pending = true
			t.escAt = time.Now()
		}
		return false
	case Match:
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
		t.keys.scroll(-1)
	case "down", "down-arrow":
		t.keys.scroll(1)
	case "page-up":
		t.keys.scroll(-t.keys.float.Rect.H)
	case "page-down":
		t.keys.scroll(t.keys.float.Rect.H)
	case "top":
		t.keys.scroll(-len(t.keys.rows))
	case "bottom":
		t.keys.scroll(len(t.keys.rows))
	case "search":
		t.keys.startSearch()
	case "next-match":
		t.keys.nextMatch(1)
	case "prev-match":
		t.keys.nextMatch(-1)
	default:
		// Every close action, and any action the view does not scroll with,
		// closes it.
		closeKeymapView(t)
		return true
	}
	t.dirty = true
	return true
}

// keymapSearchKey consumes one byte of the query. Enter keeps the query, esc
// clears it, backspace edits and ctrl+u clears. Printable bytes append. Every
// other control key is swallowed: nothing reaches a pane while the query is
// typed.
func (t *tui) keymapSearchKey() bool {
	if len(t.inbuf) == 0 {
		return true
	}
	b := t.inbuf[0]
	switch {
	case b == 0x1b:
		t.keys.cancelSearch()
		t.inbuf = nil
		t.pending = false
	case b == '\r' || b == '\n':
		t.keys.acceptSearch()
		t.inbuf = t.inbuf[1:]
		t.pending = false
	case b == 0x7f || b == 0x08:
		t.keys.backspaceQuery()
		t.inbuf = t.inbuf[1:]
		t.pending = false
	case b == 0x15:
		t.keys.setQuery("")
		t.inbuf = t.inbuf[1:]
		t.pending = false
	case b >= 0x20 && b < 0x7f:
		t.keys.appendQuery(string(b))
		t.inbuf = t.inbuf[1:]
		t.pending = false
	default:
		t.inbuf = t.inbuf[1:]
		t.pending = false
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
	t.keys.scroll(step)
	t.dirty = true
}
