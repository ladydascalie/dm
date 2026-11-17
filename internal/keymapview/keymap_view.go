// Package keymapview paints the keymap float: its rows, its search, its scroll
// window and its borders.
package keymapview

// The full keymap view as a floating pane. It lists every mode the config
// carries and every binding in each, so a reader sees all the keys the client
// takes without leaving the session. The view is read-only: keys scroll it and
// close it, and the wheel scrolls it.

import (
	"bytes"
	"fmt"
	"strings"

	"dm/internal/cells"
	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/panel"
	"dm/internal/ui"
	"dm/internal/vt"
	"dm/internal/wire"
)

const (
	// keymapViewID is the view float's id.
	keymapViewID = "keymap"

	// keymapViewW is the widest rect the view takes, and keymapViewGap is the
	// rows it keeps clear of the screen's edges.
	keymapViewW   = 96
	keymapViewGap = 6
)

// KeymapView is the open keymap float: the rows built when it opened, the
// first row in view, and the float that draws them.
type KeymapView struct {
	rows []string
	// win is the window over rows: the first row in view. The view selects
	// nothing, so the window carries no selection and only its offset moves.
	win ui.Viewport
	// Search is the query, the committed query and the matching binding rows,
	// with the cursor on the active one. Searching says whether the query is
	// being typed.
	Search    ui.Search
	Searching bool
	// Float is the pane that draws the view.
	Float *panel.FloatingPane
	snap  *wire.Snapshot
	// km names the key of every hint the view paints. The rows are built once
	// from the config the view opened with; the hint is derived from km at
	// paint time, so a reload moves it without a rebuild.
	km *keymap.Config
}

// NewKeymapView opens the float on a screen. The rows are built once, so a
// scroll never re-reads the config.
func NewKeymapView(snap *wire.Snapshot, cfg *keymap.Config) *KeymapView {
	k := &KeymapView{Float: &panel.FloatingPane{ID: keymapViewID, Active: true}, snap: snap, km: cfg}
	k.rows = KeymapRows(cfg)
	k.Sync(snap)
	return k
}

// SetKeymap swaps the config the hint is derived from and rewrites the border
// hint in place, so a reload that rebinds a key moves the hint. The rows keep
// the config the view listed when it opened.
func (k *KeymapView) SetKeymap(cfg *keymap.Config) {
	k.km = cfg
	k.Float.Hint = k.hint()
}

// KeymapRows builds the view's rows: the prefix, a blank, then every mode as a
// header and one "key  label" row per action, a blank between modes. The action
// row carries the key the config displays, padded to the widest key, and the
// action name with its hyphens turned into spaces.
func KeymapRows(cfg *keymap.Config) []string {
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

// KeymapViewRect is where the float sits: centred over the pane area, one cell
// clear of the rail, at most 96 columns and rows-6. The height follows n, the
// row count, so a short list reads in a short box. A list longer than the cap
// scrolls.
func KeymapViewRect(snap *wire.Snapshot, n int) layout.Rect {
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
	return clampRect(layout.Rect{X: x, Y: y, W: w, H: h}, snap.Cols, snap.Rows)
}

// clampRect keeps r inside a cols by rows screen.
func clampRect(r layout.Rect, cols, rows int) layout.Rect {
	if r.W > cols {
		r.W = cols
	}
	if r.H > rows {
		r.H = rows
	}
	if r.W < 0 {
		r.W = 0
	}
	if r.H < 0 {
		r.H = 0
	}
	if r.X < 0 {
		r.X = 0
	}
	if r.Y < 0 {
		r.Y = 0
	}
	if r.X+r.W > cols {
		r.X = cols - r.W
	}
	if r.Y+r.H > rows {
		r.Y = rows - r.H
	}
	if r.X < 0 {
		r.X = 0
	}
	if r.Y < 0 {
		r.Y = 0
	}
	return r
}

// Sync fits the float to the screen after a snapshot or a rail change and
// repaints the window.
func (k *KeymapView) Sync(snap *wire.Snapshot) {
	k.snap = snap
	k.Float.Rect = KeymapViewRect(snap, len(k.rows))
	k.fit()
	k.paint()
}

// RowCount is the number of rows the view lists.
func (k *KeymapView) RowCount() int {
	return len(k.rows)
}

// Offset is the first row in view.
func (k *KeymapView) Offset() int {
	return k.win.Off
}

// Scroll moves the window by n rows. The offset stops at the first row and at
// the last row that still fills the rect.
func (k *KeymapView) Scroll(n int) {
	k.win.Off += n
	k.fit()
	k.paint()
}

// fit sizes the window to the rows and the float and holds it on the rows.
func (k *KeymapView) fit() {
	k.win.N, k.win.H, k.win.Sel = len(k.rows), k.Float.Rect.H, -1
	k.win.Clamp()
}

// StartSearch begins a new query. The old query, its matches and the query it
// committed go.
func (k *KeymapView) StartSearch() {
	k.Searching = true
	k.Search.Clear()
	k.paint()
}

// CancelSearch drops the query and its highlight.
func (k *KeymapView) CancelSearch() {
	k.Searching = false
	k.Search.Clear()
	k.paint()
}

// AcceptSearch keeps the query and its highlight, makes it the accepted query
// and returns to normal keys.
func (k *KeymapView) AcceptSearch() {
	k.Searching = false
	k.Search.Commit()
	k.paint()
}

// AppendQuery adds text to the query at the caret and recomputes the matches.
func (k *KeymapView) AppendQuery(s string) {
	k.Search.Input.InsertString(s)
	k.refilter()
}

// BackspaceQuery removes the last rune of the query. A multi-byte rune goes
// whole: cutting one byte would leave a broken prefix that matches nothing.
func (k *KeymapView) BackspaceQuery() {
	if k.Search.Input.Empty() {
		return
	}
	k.Search.Input.Backspace()
	k.refilter()
}

// SetQuery replaces the draft query, recomputes the matching binding rows and
// puts the first match in view. An empty query clears the highlight. The query
// is kept as typed: the title shows it, and only the match test folds case.
func (k *KeymapView) SetQuery(q string) {
	k.Search.Input.SetValue(q)
	k.refilter()
}

// refilter rebuilds the match set from the draft and centres the first match.
// The match test folds case and reads the draft through the Search, so the
// highlight answers the query the title shows.
func (k *KeymapView) refilter() {
	lower := strings.ToLower(k.Search.Input.String())
	empty := lower == ""
	k.Search.Refilter(len(k.rows), func(i int) bool {
		if empty {
			return false
		}
		row := k.rows[i]
		if keymapHeader(row) || strings.TrimSpace(row) == "" {
			return false
		}
		return strings.Contains(strings.ToLower(row), lower)
	})
	k.jumpToMatch()
	k.paint()
}

// jumpToMatch centres the current match in the window.
func (k *KeymapView) jumpToMatch() {
	row := k.Search.Match()
	if row < 0 {
		return
	}
	k.win.Off = row - k.Float.Rect.H/2
	k.fit()
}

// NextMatch moves to the next or previous matching row and centres it.
func (k *KeymapView) NextMatch(dir int) {
	if k.Search.Count() == 0 {
		return
	}
	if dir < 0 {
		k.Search.Prev()
	} else {
		k.Search.Next()
	}
	k.jumpToMatch()
	k.paint()
}

// title is the float's top border: keymaps, plus the live query and the
// position of the active match.
func (k *KeymapView) title() string {
	q := k.Search.Input.String()
	if q == "" {
		return " keymaps "
	}
	pos := "0/0"
	if n := k.Search.Count(); n > 0 {
		pos = fmt.Sprintf("%d/%d", k.Search.Cursor+1, n)
	}
	return " keymaps  /" + q + "  " + pos + " "
}

// hint is the float's bottom border: the normal keys, the search keys while
// the query is typed, or the match keys after enter keeps it. The scroll,
// search and close keys come from the help keymap; the two keys the search
// itself owns, enter and esc, are the client's own and stay literal.
func (k *KeymapView) hint() string {
	switch {
	case k.Searching:
		return ui.Hints([]ui.Hint{
			{Keys: []string{"enter"}, Label: "keep"},
			{Keys: []string{"esc"}, Label: "clear"},
		})
	case !k.Search.Input.Empty():
		return ui.Hints([]ui.Hint{
			{Keys: []string{k.key("next-match"), k.key("prev-match")}, Label: "next"},
			{Keys: []string{k.key("search")}, Label: "edit"},
			{Keys: []string{k.key("close-esc")}, Label: "close"},
		})
	default:
		return ui.Hints([]ui.Hint{
			{Keys: []string{k.key("down"), k.key("up")}, Label: "scroll"},
			{Keys: []string{k.key("top"), k.key("bottom")}, Label: "ends"},
			{Keys: []string{k.key("search")}, Label: "search"},
			{Keys: []string{k.key("close-esc")}, Label: "close"},
		})
	}
}

// key is the key the help keymap shows for action. A view without a config
// names no key, and the hint drops that key rather than inventing one.
func (k *KeymapView) key(action string) string {
	if k.km == nil {
		return ""
	}
	return k.km.Key("help", action)
}

// style is the SGR a row paints in. A mode header is bright white, the active
// match is black on yellow, every other match is black on white, and a normal
// binding is dim.
func (k *KeymapView) style(i int) string {
	switch {
	case keymapHeader(k.rows[i]):
		return "\x1b[0m\x1b[97m"
	case k.activeMatch(i):
		return "\x1b[0m\x1b[30;103m"
	case k.rowMatch(i):
		return "\x1b[0m\x1b[30;107m"
	default:
		return "\x1b[0m\x1b[90m"
	}
}

// activeMatch reports whether row i is the match n/N last moved to.
func (k *KeymapView) activeMatch(i int) bool {
	return k.Search.Match() == i
}

// rowMatch reports whether the query matched row i.
func (k *KeymapView) rowMatch(i int) bool {
	for _, m := range k.Search.Matches {
		if m == i {
			return true
		}
	}
	return false
}

// paint rewrites the float's terminal from the rows. Every row is placed
// absolutely, so a view that scrolled paints its new window whole. A mode
// header is bright; a binding row is dim.
func (k *KeymapView) paint() {
	r := k.Float.Rect
	k.Float.Title = k.title()
	k.Float.Hint = k.hint()
	if r.W < 1 || r.H < 1 {
		k.Float.VT = vt.NewTerm(1, 1)
		return
	}
	term := vt.NewTerm(r.W, r.H)
	var buf bytes.Buffer
	for row := 0; row < r.H; row++ {
		i := k.win.Off + row
		if i >= len(k.rows) {
			break
		}
		style := k.style(i)
		fmt.Fprintf(&buf, "\x1b[%d;1H%s%s", row+1, style, cells.Clip(k.rows[i], r.W))
	}
	term.Write(buf.Bytes())
	term.Flush()
	k.Float.VT = term
}

// keymapHeader reports whether a row is a header rather than a binding.
func keymapHeader(row string) bool {
	return strings.HasPrefix(row, " prefix ") || (strings.HasPrefix(row, " [") && strings.HasSuffix(row, "]"))
}
