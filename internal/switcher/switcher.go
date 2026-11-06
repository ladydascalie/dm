package switcher

import (
	"strings"
	"unicode/utf8"

	"dm/internal/layout"
	"dm/internal/rail"
	"dm/internal/wire"
)

// switcher is the quick switcher's state: the float's rect, the rail tree it
// shows, the rows the query keeps, the selection, and the preview request and
// its lines. Drawing and key handling live elsewhere; this file is the model
// that answers them.
type Switcher struct {
	Rect layout.Rect

	// tree is the rail tree the float shows: never compact, every workspace
	// unfolded, gaps kept. rows are the surviving indices into tree, in tree
	// order, and sel is one of them. A sel of -1 selects nothing.
	tree []rail.RailItem
	rows []int
	sel  int
	top  int // first list row on screen, so a redraw does not jump

	// panes carries each pane's own folder, branch, worktree and names. A pane
	// row's rail.RailItem holds none of them, and the query matches them.
	panes map[string]wire.AllPane

	query []rune

	// The preview: whether a request is outstanding, the newest request id and
	// the pane it asked for, and the lines the server sent for it.
	asked bool
	req   int
	pane  string
	lines []string
}

// NewSwitcher opens the switcher over a snapshot inside rect.
func NewSwitcher(rect layout.Rect, snap *wire.Snapshot) *Switcher {
	s := &Switcher{Rect: rect, sel: -1}
	s.Sync(snap)
	return s
}

// sync rebuilds the tree after a snapshot. The selection stays on its own row
// when that row survives, and falls back to the first pane row; when no pane
// row survives it selects nothing.
func (s *Switcher) Sync(snap *wire.Snapshot) {
	prev, prevOK := s.Selected()
	s.tree = switcherTree(snap)
	s.panes = switcherPanes(snap)
	s.filter()
	s.top = 0
	// A preview of a pane that is gone is stale, so forget the request with it.
	if s.asked && !s.hasPane(s.pane) {
		s.asked, s.pane, s.lines = false, "", nil
	}
	if prevOK {
		id := switcherTarget(prev)
		for i, row := range s.rows {
			if it := s.tree[row]; it.Kind == prev.Kind && switcherTarget(it) == id {
				s.sel = i
				return
			}
		}
	}
	s.sel = s.firstPane()
}

// switcherTree is the rail tree the float shows: rail.RailItems on a copy of the
// snapshot with no rail, so the rows are never compact, and with every workspace
// forced unfolded, so a folded workspace never hides a pane the switcher could
// jump to. Gap rows stay to keep the blocks apart, and only they are
// unselectable.
func switcherTree(snap *wire.Snapshot) []rail.RailItem {
	if snap == nil {
		return nil
	}
	cp := *snap
	cp.Rail = 0
	cp.All = make([]wire.AllPane, len(snap.All))
	copy(cp.All, snap.All)
	for i := range cp.All {
		cp.All[i].WorkspaceUnfolded = true
	}
	return rail.RailItems(&cp)
}

// switcherPanes indexes the snapshot by pane id, so a pane row can match on the
// folder, branch, worktree and names the rail only puts on its header rows.
func switcherPanes(snap *wire.Snapshot) map[string]wire.AllPane {
	if snap == nil {
		return nil
	}
	out := make(map[string]wire.AllPane, len(snap.All))
	for _, p := range snap.All {
		out[p.ID] = p
	}
	return out
}

// hasPane reports whether a pane is still in the tree.
func (s *Switcher) hasPane(id string) bool {
	if id == "" {
		return false
	}
	for _, it := range s.tree {
		if it.Kind == rail.Pane && it.Pane == id {
			return true
		}
	}
	return false
}

// appendText adds a rune to the query and reselects.
func (s *Switcher) AppendText(r rune) {
	s.query = append(s.query, r)
	s.refilter()
}

// backspace removes the query's last rune and reselects.
func (s *Switcher) Backspace() {
	if len(s.query) == 0 {
		return
	}
	s.query = s.query[:len(s.query)-1]
	s.refilter()
}

// clearQuery empties the query and reselects.
func (s *Switcher) ClearQuery() {
	if len(s.query) == 0 {
		return
	}
	s.query = nil
	s.refilter()
}

// refilter rebuilds the rows for the query and lands the selection on the best
// surviving pane row, as close as possible to the row selected before.
func (s *Switcher) refilter() {
	prev := -1
	if s.sel >= 0 && s.sel < len(s.rows) {
		prev = s.rows[s.sel]
	}
	s.filter()
	s.top = 0
	s.reselect(prev)
}

// Query is the query as a lower-case string to match against.
func (s *Switcher) Query() string {
	return strings.ToLower(string(s.query))
}

// RowCount is how many rows the query keeps.
func (s *Switcher) RowCount() int {
	return len(s.rows)
}

// PreviewLines are the lines the server sent for the selected pane.
func (s *Switcher) PreviewLines() []string {
	return s.lines
}

// filter rebuilds the surviving row indices for the current query, in tree
// order. An empty query keeps every row. Otherwise a pane survives by its own
// match, and a workspace header by its own match or by a pane under it. A gap
// survives between two surviving blocks.
func (s *Switcher) filter() {
	q := s.Query()
	keep := make([]bool, len(s.tree))
	blocks := switcherBlocks(s.tree)
	for bi := range blocks {
		b := &blocks[bi]
		_, wsMatch := s.match(s.tree[b.row], q)
		under := false
		for _, p := range b.panes {
			_, ok := s.match(s.tree[p], q)
			keep[p] = ok
			under = under || ok
		}
		b.keep = wsMatch || under
		if wsMatch {
			for _, p := range b.panes {
				keep[p] = true
			}
		}
		keep[b.row] = b.keep
	}
	for bi, b := range blocks {
		if b.gap >= 0 && bi+1 < len(blocks) && b.keep && blocks[bi+1].keep {
			keep[b.gap] = true
		}
	}
	s.rows = s.rows[:0]
	for i, k := range keep {
		if k {
			s.rows = append(s.rows, i)
		}
	}
}

// switcherBlock is one workspace header, the pane rows under it and the gap row
// that follows the block, if any.
type switcherBlock struct {
	row   int   // the workspace header
	panes []int // the pane rows under it
	gap   int
	keep  bool
}

// switcherBlocks splits a rail tree into its workspace blocks. Panes group
// directly under the workspace header: the flat rail has no window row.
func switcherBlocks(tree []rail.RailItem) []switcherBlock {
	var out []switcherBlock
	for i, it := range tree {
		switch it.Kind {
		case rail.Workspace:
			out = append(out, switcherBlock{row: i, gap: -1})
		case rail.Gap:
			if n := len(out); n > 0 {
				out[n-1].gap = i
			}
		case rail.Pane:
			if len(out) == 0 {
				continue
			}
			b := &out[len(out)-1]
			b.panes = append(b.panes, i)
		}
	}
	return out
}

// move steps the selection d selectable rows, never landing on a gap, and stops
// at either end of the list.
func (s *Switcher) Move(d int) {
	if len(s.rows) == 0 {
		s.sel = -1
		return
	}
	if d == 0 {
		return
	}
	step, steps := 1, d
	if d < 0 {
		step, steps = -1, -d
	}
	i := s.sel
	if i < 0 {
		i = -1
		if step < 0 {
			i = len(s.rows)
		}
	}
	for n := 0; n < steps; n++ {
		next := i + step
		for next >= 0 && next < len(s.rows) && s.tree[s.rows[next]].Kind == rail.Gap {
			next += step
		}
		if next < 0 || next >= len(s.rows) {
			break
		}
		i = next
	}
	if i >= 0 && i < len(s.rows) {
		s.sel = i
	}
}

// selected is the highlighted row.
func (s *Switcher) Selected() (rail.RailItem, bool) {
	if s.sel < 0 || s.sel >= len(s.rows) {
		return rail.RailItem{}, false
	}
	return s.tree[s.rows[s.sel]], true
}

// target is the id of the selected row: the workspace id for a workspace
// header and the pane id for a pane row. It is empty when nothing is selected.
func (s *Switcher) Target() string {
	it, ok := s.Selected()
	if !ok {
		return ""
	}
	return switcherTarget(it)
}

// switcherTarget is the id a row stands for.
func switcherTarget(it rail.RailItem) string {
	switch it.Kind {
	case rail.Workspace:
		return it.Workspace
	case rail.Window:
		return it.Window
	case rail.Pane:
		return it.Pane
	}
	return ""
}

// firstPane is the row index of the first pane row the query keeps, or -1.
func (s *Switcher) firstPane() int {
	for i, row := range s.rows {
		if s.tree[row].Kind == rail.Pane {
			return i
		}
	}
	return -1
}

// setPreview records a preview request for a pane and drops the lines of the
// request before it, so a stale screen is never drawn.
func (s *Switcher) SetPreview(id int, pane string) {
	s.asked, s.req, s.pane, s.lines = true, id, pane, nil
}

// previewDue reports whether the selected pane is not the one asked for, so the
// caller should ask again. Only a pane row has a screen to preview.
func (s *Switcher) PreviewDue() bool {
	it, ok := s.Selected()
	if !ok || it.Kind != rail.Pane {
		return false
	}
	return !s.asked || it.Pane != s.pane
}

// acceptPreview takes the lines for the newest request and reports whether it
// did. A reply to an older request is refused.
func (s *Switcher) AcceptPreview(id int, lines []string) bool {
	if !s.asked || id != s.req {
		return false
	}
	s.lines = lines
	return true
}

// reselect lands the selection on the best surviving pane row. Rows that score
// alike are broken by the distance to prev, the tree row selected before, so a
// query edit keeps the operator near where they were.
func (s *Switcher) reselect(prev int) {
	s.sel = -1
	var best switcherScore
	bestDist := 0
	q := s.Query()
	for i, row := range s.rows {
		if s.tree[row].Kind != rail.Pane {
			continue
		}
		sc, ok := s.match(s.tree[row], q)
		if !ok {
			// Kept by a header's match, so it survives without scoring.
			sc = switcherScore{quality: switcherNoMatch}
		}
		dist := row
		if prev >= 0 {
			dist = row - prev
			if dist < 0 {
				dist = -dist
			}
		}
		if s.sel < 0 || sc.better(best) || (sc == best && dist < bestDist) {
			s.sel, best, bestDist = i, sc, dist
		}
	}
}

// switcherScore ranks how well a row matches: quality 0 is a field prefix, 1 a
// substring, 2 a subsequence, and a shorter field then an earlier match break a
// tie.
type switcherScore struct {
	quality int
	length  int
	at      int
}

// better reports whether a ranks above b.
func (a switcherScore) better(b switcherScore) bool {
	if a.quality != b.quality {
		return a.quality < b.quality
	}
	if a.length != b.length {
		return a.length < b.length
	}
	return a.at < b.at
}

const (
	switcherPrefix = iota
	switcherSubstring
	switcherSubsequence
	// switcherNoMatch is the score of a pane row that survives on a header's
	// match alone: any match of its own ranks above it.
	switcherNoMatch
)

// match scores a row against the query and reports whether it matches. A row
// matches on the best of its fields; an empty query matches every row.
func (s *Switcher) match(it rail.RailItem, q string) (switcherScore, bool) {
	if q == "" {
		return switcherScore{}, true
	}
	var best switcherScore
	found := false
	for _, field := range s.fields(it) {
		sc, ok := switcherField(field, q)
		if !ok {
			continue
		}
		if !found || sc.better(best) {
			best, found = sc, true
		}
	}
	return best, found
}

// fields are the strings a row matches on: a pane its id, label, folder,
// branch, worktree and its window's and workspace's names, a window its name,
// id, folder, branch and worktree, a workspace its name and id.
func (s *Switcher) fields(it rail.RailItem) []string {
	switch it.Kind {
	case rail.Pane:
		p := s.panes[it.Pane]
		return []string{
			it.Pane, it.Text,
			rail.RailFolder(p.Cwd, p.Root), p.Branch, p.Worktree,
			p.WindowName, p.WorkspaceName,
		}
	case rail.Window:
		return []string{it.Text, it.Window, it.Folder, it.Branch, it.Worktree}
	case rail.Workspace:
		return []string{it.Text, it.Workspace}
	}
	return nil
}

// switcherField scores the query against one field, case-insensitively.
func switcherField(field, q string) (switcherScore, bool) {
	h := strings.ToLower(field)
	if h == "" {
		return switcherScore{}, false
	}
	if at := strings.Index(h, q); at == 0 {
		return switcherScore{quality: switcherPrefix, length: utf8.RuneCountInString(field)}, true
	} else if at > 0 {
		return switcherScore{
			quality: switcherSubstring,
			length:  utf8.RuneCountInString(field),
			at:      utf8.RuneCountInString(h[:at]),
		}, true
	}
	if at, ok := switcherSubseq(h, q); ok {
		return switcherScore{
			quality: switcherSubsequence,
			length:  utf8.RuneCountInString(field),
			at:      at,
		}, true
	}
	return switcherScore{}, false
}

// switcherSubseq reports whether q is a subsequence of h and the rune index of
// the first rune it matched.
func switcherSubseq(h, q string) (int, bool) {
	qr := []rune(q)
	first, n := -1, 0
	for at, r := range []rune(h) {
		if n < len(qr) && r == qr[n] {
			if first < 0 {
				first = at
			}
			n++
		}
	}
	return first, n == len(qr) && len(qr) > 0
}
