package client

// The git facts float: what the rail's git mark stands for on one window, drawn
// beside the rail on the row that owns the mark. The facts ride the rail row the
// mark was painted on, so the box and the row never read two different probes.
// One float at a time: a click on another mark moves it, a click on its own mark
// closes it.

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"dm/internal/cells"
	"dm/internal/hintbar"
	"dm/internal/layout"
	"dm/internal/panel"
	"dm/internal/rail"
	"dm/internal/segment"
	"dm/internal/theme"
	"dm/internal/wire"
)

// gitFloatID is the float's id in the client's float stack.
const gitFloatID = "git"

const (
	// gitKeyCol is the interior column a fact's value starts in: one space in
	// from the border, the longest key and one space after it.
	gitKeyCol = 10
	// gitMinCols and gitMaxCols bound the box's outside width. The box follows
	// the longest value between them, so a long subject elides instead of
	// stretching the box across the desk.
	gitMinCols = 30
	gitMaxCols = 44
	// gitRows is how many facts the box lists: worktree, branch, head,
	// changes, state and upstream.
	gitRows = 6
	// gitChromeRows is what the box needs beyond its facts: the top border, the
	// bottom border and the status bar row it must leave alone. A terminal with
	// fewer rows than the two together has no room to stand the box beside the
	// bar, so no box opens there.
	gitChromeRows = 3
)

// gitFloat is the open facts box: the window whose mark opened it and the
// panel that draws it.
type gitFloat struct {
	window string
	float  *panel.FloatingPane
}

// gitMarkPress is the mark's left click: it opens the box on that window, moves
// an open box to it, or closes the box when its own mark was clicked. One mark
// is open at a time, so a second mark never adds a second box.
func (t *tui) gitMarkPress(window string) {
	if window == "" {
		return
	}
	if t.git != nil && t.git.window == window {
		t.closeGitFloat()
		return
	}
	t.closeGitFloat()
	if t.snap == nil {
		return
	}
	v := &gitFloat{window: window}
	v.float = &panel.FloatingPane{
		ID:     gitFloatID,
		Active: true,
		Hint:   gitHint(),
	}
	v.float.Paint = func(b *bytes.Buffer, r layout.Rect) { t.paintGitFloat(b, r) }
	t.git = v
	t.AddFloatingPane(v.float)
	// The anchor is the sync's business, so an open and a moved box place
	// themselves through one rule.
	t.syncGit()
	t.dirty = true
}

// closeGitFloat takes the facts box down. The mark stops inverting on the next
// frame and the keys go back to whatever the desk had.
func (t *tui) closeGitFloat() {
	if t.git == nil {
		return
	}
	t.RemoveFloatingPane(gitFloatID)
	t.git = nil
	t.dirty = true
}

// takeDesk hands the desk to a surface that owns it: the git facts box stands
// down rather than being painted through the surface's rows. Every open path
// that takes the desk calls this one helper, so the rule is stated once and the
// surfaces cannot drift apart. The ask float is left alone: it is added to the
// float stack after the box, so it paints on top of it and needs no hand-over.
func (t *tui) takeDesk() {
	t.closeGitFloat()
}

// gitOpen is the window whose git mark the box has open, "" when none, so the
// rail inverts one mark and never two.
func (t *tui) gitOpen() string {
	if t.git == nil {
		return ""
	}
	return t.git.window
}

// syncGit anchors the open box on the window's mark and closes it when that mark
// is no longer drawn: a fold hid the row, the rail scrolled it out, the list
// shrank or the rail went thin. A terminal too short to stand the box beside
// the status bar has no honest anchor either, so it closes there rather than
// painting a border over the bar. It reads the row through the same rule the
// hit test does, so the box and the mark cannot part.
func (t *tui) syncGit() {
	v := t.git
	if v == nil {
		return
	}
	row := -1
	if t.snap != nil && t.snap.Rows >= gitRows+gitChromeRows {
		row = rail.RailGitMarkRow(t.snap, v.window, t.railOff)
	}
	if row < 0 {
		t.closeGitFloat()
		return
	}
	it, ok := rail.RailWindowItem(t.snap, v.window)
	if !ok {
		t.closeGitFloat()
		return
	}
	th := t.theme()
	title := th.Glyphs.GitMark + " " + gitLabel(it)
	rect := gitRect(t.snap, th, it, row)
	if v.float.Title != title {
		v.float.Title = title
		t.dirty = true
	}
	if v.float.Rect != rect {
		v.float.Rect = rect
		t.dirty = true
	}
}

// gitRect is the box's interior rect: its left edge one cell right of the rail
// and its top edge on the row that owns the mark, clamped so the box stays on
// the terminal at the bottom and the right. The bottom clamp keeps the box's
// bottom border on Rows-2 at most, because Rows-1 is the status bar. The box
// opens over the layout and never over the rail, so the mark and its row stay
// readable.
func gitRect(snap *wire.Snapshot, th theme.Theme, it rail.RailItem, row int) layout.Rect {
	// The rect is the interior and the border sits one cell out on every side,
	// so the outside box is boxW by gitRows+2 and its left border lands on the
	// column the rail ends at.
	boxW := gitWidth(th, it)
	h := gitRows
	// x and y are the outer box's top left corner. The box opens beside the
	// rail and on the mark's row, and the two clamps keep it on the terminal
	// at the right and the bottom. A terminal too narrow for the box beside the
	// rail gives the box the room the rail leaves rather than sliding it over
	// the mark it describes.
	x := snap.Rail
	if max := snap.Cols - boxW; x > max {
		x = max
	}
	if x < snap.Rail {
		x = snap.Rail
		boxW = snap.Cols - snap.Rail
	}
	if boxW < 3 {
		boxW = 3
	}
	w := boxW - 2
	// The box's outside height is h plus both borders, and the status bar owns
	// the terminal's last row, so the lowest its top edge may stand is
	// Rows-(h+3).
	y := row
	if max := snap.Rows - (h + gitChromeRows); y > max {
		y = max
	}
	if y < 0 {
		y = 0
	}
	return layout.Rect{X: x + 1, Y: y + 1, W: w, H: h}
}

// gitWidth is the box's outside width: both borders, the key column and the
// longest value, held between gitMinCols and gitMaxCols. The title takes its
// cells too, so a long window name never clips the mark that opens the box.
func gitWidth(th theme.Theme, it rail.RailItem) int {
	longest := 0
	for _, fact := range gitFacts(th, it) {
		if n := segment.SegCells(fact.value); n > longest {
			longest = n
		}
	}
	w := gitKeyCol + longest + 2
	// The title row carries the glyph, a space and the label, one cell clear of
	// each corner.
	if title := cells.Cells(th.Glyphs.GitMark) + 1 + cells.Cells(gitLabel(it)) + 4; title > w {
		w = title
	}
	if w < gitMinCols {
		w = gitMinCols
	}
	if w > gitMaxCols {
		w = gitMaxCols
	}
	return w
}

// gitLabel is the name the title and the rail row share: the rendered label,
// the folder, or the window's own name. It is the same fallback the rail paints
// with, so the box never names a window differently from its row.
func gitLabel(it rail.RailItem) string {
	for _, s := range []string{it.Label, it.Folder, it.Text} {
		if s != "" {
			return s
		}
	}
	return it.Window
}

// gitHint is the box's bottom border: how the box leaves, in the voice the ask
// float's hint bar uses. esc and the mark are the two ways the box always
// offers, and the two together fill the narrowest border exactly, so neither is
// ever dropped: a click on the mark that opened the box closes it again. esc
// keeps a Keep so it survives even a row shorter than the minimum.
func gitHint() *hintbar.Bar {
	return &hintbar.Bar{Hints: []hintbar.Hint{
		{Keys: []string{"esc"}, Label: "close", Keep: true},
		{Keys: []string{"click"}, Label: "mark"},
	}}
}

// gitFact is one row of the box: a key and its value.
type gitFact struct {
	key   string
	value []segment.Seg
}

// gitFacts is the box's content, read from the rail row that opened it. A fact
// the probe did not answer says (none) in dim: never a zero, and never a clean
// tree it did not see.
func gitFacts(th theme.Theme, it rail.RailItem) []gitFact {
	p := th.Palette
	text := func(s string) []segment.Seg {
		return []segment.Seg{{Text: s, FG: p.RailText}}
	}
	dim := func(s string) []segment.Seg {
		return []segment.Seg{{Text: s, FG: p.RailDim}}
	}
	none := dim("(none)")

	worktree := text(it.Worktree)
	if it.Worktree == "" {
		worktree = dim("(main checkout)")
	}
	branch := text(it.Branch)
	if it.Branch == "" {
		branch = dim("(detached)")
	}
	head := none
	if it.Head != "" {
		head = text(strings.TrimRight(it.Head+"  "+it.HeadSubject, " "))
	}
	// A change is a line count and a file count. A row with neither says none,
	// so a zero never reads as work.
	var changes []segment.Seg
	if it.Added > 0 {
		changes = append(changes, segment.Seg{Text: "+" + strconv.Itoa(it.Added), FG: p.RailDiffAdd})
	}
	if it.Deleted > 0 {
		if len(changes) > 0 {
			changes = append(changes, segment.Seg{Text: " ", FG: p.RailDim})
		}
		changes = append(changes, segment.Seg{Text: "-" + strconv.Itoa(it.Deleted), FG: p.RailDiffDel})
	}
	if it.Files > 0 {
		if len(changes) > 0 {
			changes = append(changes, segment.Seg{Text: "  ", FG: p.RailDim})
		}
		changes = append(changes, segment.Seg{Text: gitFileCount(it.Files), FG: p.RailText})
	}
	if len(changes) == 0 {
		changes = text("none")
	}
	state := text("clean")
	if it.Dirty {
		state = text("dirty")
	}
	upstream := none
	if it.Upstream != "" {
		// The tracking ref, then the commits HEAD has that the upstream does
		// not and the reverse. Both counts are real answers here, so a zero
		// reads as one.
		upstream = []segment.Seg{
			{Text: it.Upstream, FG: p.RailText},
			{Text: "  ", FG: p.RailDim},
			{Text: "\u2191" + strconv.Itoa(it.Ahead), FG: p.RailDiffAdd},
			{Text: " ", FG: p.RailDim},
			{Text: "\u2193" + strconv.Itoa(it.Behind), FG: p.RailDim},
		}
	}
	return []gitFact{
		{key: "worktree", value: worktree},
		{key: "branch", value: branch},
		{key: "head", value: head},
		{key: "changes", value: changes},
		{key: "state", value: state},
		{key: "upstream", value: upstream},
	}
}

// gitFileCount is the files part of the changes row: one file, or many.
func gitFileCount(n int) string {
	if n == 1 {
		return "1 file"
	}
	return strconv.Itoa(n) + " files"
}

// paintGitFloat paints the facts inside the box: one fact a row, the key in the
// rail's dim and the value in its text. A value longer than the box elides with
// the set's mark and never wraps, so the box keeps one row a fact however narrow
// it stands.
func (t *tui) paintGitFloat(b *bytes.Buffer, r layout.Rect) {
	if t.git == nil || t.snap == nil {
		return
	}
	it, ok := rail.RailWindowItem(t.snap, t.git.window)
	if !ok {
		return
	}
	th := t.theme()
	budget := r.W - gitKeyCol
	if budget < 1 {
		return
	}
	for i, fact := range gitFacts(th, it) {
		if i >= r.H {
			return
		}
		fmt.Fprintf(b, "\x1b[%d;%dH", r.Y+i+1, r.X+1)
		key := " " + fact.key + strings.Repeat(" ", gitKeyCol-2-cells.Cells(fact.key)) + " "
		segment.Write(b, segment.Seg{Text: key, FG: th.Palette.RailDim})
		value := fact.value
		if segment.SegCells(value) > budget {
			// A value that does not fit ends in the set's mark: it never
			// wraps, so the box keeps one row a fact.
			value = segment.ElideSegs(th, value, budget)
		}
		for _, s := range value {
			segment.Write(b, s)
		}
	}
}
