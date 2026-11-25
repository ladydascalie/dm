package client

// The git facts float: what the rail's git mark stands for on one window, drawn
// beside the rail on the row that owns the mark. The facts ride the rail row the
// mark was painted on, so the box and the row never read two different probes.
// One float at a time: a click on another mark moves it, a click on its own mark
// closes it. Its bottom-right corner is a grip: a drag sizes the box and the
// size outlives the box in the prefs, and a click of the grip puts the size back
// to automatic. A box taller than its facts lists the changed files.

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"

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
	// gitMaxSavedCols and gitMaxSavedRows bound a remembered size. A value past
	// them is one no drag could have made, so the box opens automatic instead.
	gitMaxSavedCols = 400
	gitMaxSavedRows = 200
)

// gitFloat is the open facts box: the window whose mark opened it, the panel
// that draws it and the state of a grip drag.
type gitFloat struct {
	window string
	float  *panel.FloatingPane
	// resizing is a drag of the grip in flight. top is the box's outer top row
	// at the press, pressX and pressY the grip cell, and at the drag's own
	// clock. moved records that the pointer left that cell, so a press and
	// release on one cell is a reset and a drag is a size.
	resizing bool
	moved    bool
	top      int
	pressX   int
	pressY   int
	at       time.Time
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
	if t.padAxis == gitPadAxis {
		// The hover that said resize went with the grip it named.
		t.padZone, t.padAxis = layout.Rect{}, ""
	}
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
	rect := t.gitRect(t.snap, th, it, row)
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
// readable. A remembered size overrides the automatic one the facts ask for.
func (t *tui) gitRect(snap *wire.Snapshot, th theme.Theme, it rail.RailItem, row int) layout.Rect {
	// The rect is the interior and the border sits one cell out on every side,
	// so the outside box is boxW by h+2 and its left border lands on the column
	// the rail ends at.
	boxW, h := t.gitSavedSize()
	if boxW == 0 {
		boxW, h = gitWidth(th, it), gitRows
	}
	// A row with no changed names has nothing to show below its facts, so it
	// keeps the facts height instead of growing empty.
	if len(it.FileNames) == 0 {
		h = gitRows
	}
	// A remembered height still leaves the status bar its row, however short
	// the terminal it opens on turns out to be.
	if max := snap.Rows - gitChromeRows; h > max {
		h = max
	}
	if h < gitRows {
		h = gitRows
	}
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

// gitSavedSize is the size the prefs remember: the outside width and the
// interior height, zero zero for automatic. A value no drag could have made, so
// a malformed, negative or absurd one, reads as automatic: a preference never
// fails a start and never toasts.
func (t *tui) gitSavedSize() (cols, rows int) {
	c, r := t.prefs.GitBoxCols, t.prefs.GitBoxRows
	if c < gitMinCols || c > gitMaxSavedCols || r < gitRows || r > gitMaxSavedRows {
		return 0, 0
	}
	return c, r
}

// setGitSize remembers the box's size for the next open and every window. Zero
// in either bound is the automatic size. A drag sets it on every motion and
// saves it once, on release, so a drag costs one write rather than one a frame.
func (t *tui) setGitSize(cols, rows int) {
	t.prefs.GitBoxCols, t.prefs.GitBoxRows = cols, rows
}

// saveGitSize writes the remembered size through the atomic prefs save. A client
// with no prefs file, such as a browser page, and a read-only client keep the
// size for this session and write nothing: a size is a convenience, never a disk
// error to toast.
func (t *tui) saveGitSize() {
	if t.prefsPath == "" || t.readOnly {
		return
	}
	_ = savePrefs(t.prefsPath, t.prefs)
}

// gitGripAt reports whether x,y is the box's one drag handle: the bottom-right
// corner cell of the frame.
func gitGripAt(r layout.Rect, x, y int) bool {
	return x == r.X+r.W && y == r.Y+r.H
}

// gitBoxAt reports whether x,y is a cell of the box's frame, borders included,
// so the box can claim the pointer over the cells it covers.
func gitBoxAt(r layout.Rect, x, y int) bool {
	return x >= r.X-1 && x <= r.X+r.W && y >= r.Y-1 && y <= r.Y+r.H
}

// gitGripCell is the grip in terminal cells, which the hover zone and the drag
// read back from the rect after every resize.
func gitGripCell(r layout.Rect) layout.Rect {
	return layout.Rect{X: r.X + r.W, Y: r.Y + r.H, W: 1, H: 1}
}

// gitMouse lets the facts box own the pointer inside its frame: a left press on
// the grip starts a resize, and every other event there does nothing at all, so
// a click on the box never lands on the pane or the rail standing under it. A
// drag in flight keeps the pointer wherever it goes, so the box's edges still
// follow it past the terminal's. The wheel is the one exception: it never acts
// on the box, and the pane under it keeps scrolling while the pointer rests
// there.
func (t *tui) gitMouse(ev mouseEvent, x, y int) bool {
	v := t.git
	if v == nil {
		return false
	}
	if v.resizing {
		switch {
		case ev.release:
			t.gitResizeEnd(x, y)
		case ev.btn&32 == 0:
			// The button went away without its release: the size it reached
			// stands and the grip lets go.
			v.resizing, v.moved = false, false
			t.saveGitSize()
			t.updatePadHover(x, y)
			t.dirty = true
		default:
			t.gitDragTo(x, y, false)
		}
		return true
	}
	// A wheel over the box is not the box's: only a press and the grip are, so
	// the pane under the pointer keeps scrolling while the box stands there.
	if !gitBoxAt(v.float.Rect, x, y) || ev.btn&64 != 0 {
		return false
	}
	if ev.btn&3 == 0 && ev.btn&32 == 0 && !ev.release && gitGripAt(v.float.Rect, x, y) {
		t.gitResizePress(x, y)
	}
	return true
}

// gitResizePress starts a drag of the grip. It records the box's outer top row,
// which the height is measured from, and the cell the press landed on, so a
// press that never leaves it is a reset rather than a drag.
func (t *tui) gitResizePress(x, y int) {
	v := t.git
	if v == nil {
		return
	}
	v.resizing, v.moved = true, false
	v.at = time.Time{}
	v.top = v.float.Rect.Y - 1
	v.pressX, v.pressY = x, y
	t.dirty = true
}

// gitResizeEnd ends a drag of the grip. A drag that moved keeps the size it
// reached; a press and release on the grip cell with no movement puts the box
// back to its automatic size, so the operator can get the hugging box back.
func (t *tui) gitResizeEnd(x, y int) {
	v := t.git
	if v == nil {
		return
	}
	if v.moved || x != v.pressX || y != v.pressY {
		t.gitDragTo(x, y, true)
	} else {
		t.setGitSize(0, 0)
	}
	v.resizing, v.moved = false, false
	t.saveGitSize()
	t.syncGit()
	// The hint follows the pointer again: a release away from the grip leaves
	// none, and the bar stops saying what the grip does.
	t.updatePadHover(x, y)
	t.dirty = true
}

// gitDragTo sizes the box from the pointer: its right border follows the
// pointer's column and its bottom border the pointer's row, measured from the
// box's left and top borders. It is local and sends nothing. Unless forced it
// applies at most once every dragEvery; a release forces the last size through,
// so a fast drag never lands short.
func (t *tui) gitDragTo(x, y int, force bool) {
	v := t.git
	if v == nil || t.snap == nil || !v.resizing {
		return
	}
	if x != v.pressX || y != v.pressY {
		v.moved = true
	}
	if !force && !v.at.IsZero() && time.Since(v.at) < dragEvery {
		return
	}
	v.at = time.Now()
	// The left border stays on the rail, so the pointer's column names the right
	// border and the cells between the two are the outside width. The rows
	// between the top border and the pointer's row, less the bottom border
	// itself, are the interior height.
	cols := x - t.snap.Rail + 1
	if cols < gitMinCols {
		cols = gitMinCols
	}
	if max := t.snap.Cols - t.snap.Rail; max >= gitMinCols && cols > max {
		cols = max
	}
	rows := y - v.top - 1
	if rows < gitRows {
		rows = gitRows
	}
	if max := t.snap.Rows - gitChromeRows - v.top; max >= gitRows && rows > max {
		rows = max
	}
	t.setGitSize(cols, rows)
	t.syncGit()
	// The hint follows the grip the drag moved: the status bar keeps saying
	// what the pointer holds until it lets go.
	if t.git != nil {
		t.padZone, t.padAxis = gitGripCell(t.git.float.Rect), gitPadAxis
	}
	t.dirty = true
}

// paintGitGrip paints the grip over the box's bottom-right corner: the theme's
// grip glyph in the rail's dim, and in the rail accent while the pointer rests
// on it or drags it. The panel's box pass owns that corner, so the box paints
// the grip itself, after the panel, and the corner rule steps aside for it.
func (t *tui) paintGitGrip(buf *bytes.Buffer, th theme.Theme, r layout.Rect) {
	fg := th.Palette.RailDim
	if t.gitGripHot(r) {
		fg = th.Palette.BarAccent
	}
	fmt.Fprintf(buf, "\x1b[%d;%dH", r.Y+r.H+1, r.X+r.W+1)
	segment.Write(buf, segment.Seg{Text: th.Glyphs.Grip, FG: fg, BG: th.Palette.RailBG})
}

// gitGripHot reports whether the grip answers: the pointer rests on it, or a
// drag the grip started is in flight.
func (t *tui) gitGripHot(r layout.Rect) bool {
	if t.git == nil {
		return false
	}
	if t.git.resizing {
		return true
	}
	return t.mouseSeen && gitGripAt(r, t.mouseX, t.mouseY)
}

// gitHint is the box's bottom border: how the box leaves and how it grows, in
// the voice the ask float's hint bar uses. It names the corner rather than the
// mark, because the grip is the one affordance on the box itself; clicking the
// rail's mark again still closes it, it is just not advertised. The two hints
// fit a normal border; at the box's width floor the bar drops the corner and
// keeps esc, and the grip still answers the pointer there.
func gitHint() *hintbar.Bar {
	return &hintbar.Bar{Hints: []hintbar.Hint{
		{Keys: []string{"esc"}, Label: "close", Keep: true},
		{Keys: []string{"drag"}, Label: "corner"},
	}}
}

// gitFact is one row of the box: a key and its value.
type gitFact struct {
	key   string
	value []segment.Seg
}

// groundGitSegs puts every run of the box on the box's ground. PaintPanel fills
// the interior with the rail ground before the content paints, and segment.Write
// always writes a background, so a run left on the zero RGB punches a black hole
// through the fill. One call per painted run keeps the box one colour.
func groundGitSegs(th theme.Theme, segs []segment.Seg) []segment.Seg {
	for i := range segs {
		segs[i].BG = th.Palette.RailBG
	}
	return segs
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
	// The sha recedes so the subject leads, and a missing subject leaves the
	// sha alone rather than trailing two spaces.
	head := none
	if it.Head != "" {
		head = []segment.Seg{
			{Text: it.Head, FG: p.RailDim},
			{Text: "  ", FG: p.RailDim},
			{Text: it.HeadSubject, FG: p.RailText},
		}
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
		changes = append(changes, segment.Seg{Text: gitFileCount(it.Files), FG: p.RailDim})
	}
	if len(changes) == 0 {
		changes = dim("none")
	}
	// The state reads the way the rail's own dot reads: amber for a dirty tree,
	// quiet for a clean one, so the box and the row agree at a glance.
	state := dim("clean")
	if it.Dirty {
		state = []segment.Seg{{Text: "dirty", FG: p.RailWorking}}
	}
	upstream := none
	if it.Upstream != "" {
		// The tracking ref, then the commits HEAD has that the upstream does
		// not and the reverse. Both counts are real answers, so a zero reads as
		// one, but a zero standing on its own is noise: an arrow takes a colour
		// only when it carries commits.
		ahead, behind := p.RailDim, p.RailDim
		if it.Ahead > 0 {
			ahead = p.RailDiffAdd
		}
		if it.Behind > 0 {
			behind = p.RailText
		}
		upstream = []segment.Seg{
			{Text: it.Upstream, FG: p.RailText},
			{Text: "  ", FG: p.RailDim},
			{Text: "\u2191" + strconv.Itoa(it.Ahead), FG: ahead},
			{Text: " ", FG: p.RailDim},
			{Text: "\u2193" + strconv.Itoa(it.Behind), FG: behind},
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

// gitMoreFiles is the last row of a file list the box cannot show whole.
func gitMoreFiles(n int) string {
	if n == 1 {
		return "1 more file"
	}
	return strconv.Itoa(n) + " more files"
}

// paintGitFloat paints the facts inside the box: one fact a row, the key in the
// rail's dim and the value in its text. A value longer than the box elides with
// the set's mark and never wraps, so the box keeps one row a fact however narrow
// it stands. A box with rows to spare lists the changed names under the facts.
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
	facts := gitFacts(th, it)
	for i, fact := range facts {
		if i >= r.H {
			return
		}
		fmt.Fprintf(b, "\x1b[%d;%dH", r.Y+i+1, r.X+1)
		key := " " + fact.key + strings.Repeat(" ", gitKeyCol-2-cells.Cells(fact.key)) + " "
		for _, s := range groundGitSegs(th, []segment.Seg{{Text: key, FG: th.Palette.RailDim}}) {
			segment.Write(b, s)
		}
		value := fact.value
		if segment.SegCells(value) > budget {
			// A value that does not fit ends in the set's mark: it never
			// wraps, so the box keeps one row a fact.
			value = segment.ElideSegs(th, value, budget)
		}
		for _, s := range groundGitSegs(th, value) {
			segment.Write(b, s)
		}
	}
	t.paintGitFiles(b, r, th, it, len(facts))
}

// paintGitFiles paints the changed names the box has rows for: a rule row under
// the facts, then one row a name, then a last row counting the names the box
// could not show. The names come from the rail row the facts come from, so the
// box can never list a file the row did not carry. A row with no names, and a
// box with no spare rows, paints none of this: the facts alone are the box.
func (t *tui) paintGitFiles(b *bytes.Buffer, r layout.Rect, th theme.Theme, it rail.RailItem, used int) {
	rows := r.H - used - 1
	if rows < 1 || len(it.FileNames) == 0 {
		return
	}
	y := r.Y + used
	// The rule separates the list from the facts, so the box reads as two parts
	// rather than one loose column.
	fmt.Fprintf(b, "\x1b[%d;%dH", y+1, r.X+1)
	for _, s := range groundGitSegs(th, []segment.Seg{{Text: strings.Repeat(th.Glyphs.DividerRow, r.W), FG: th.Palette.RailDim}}) {
		segment.Write(b, s)
	}
	// The probe counts every changed file, not only the names it lists, so a
	// list the box cuts short still ends by saying how many are out of sight.
	total := it.Files
	if total < len(it.FileNames) {
		total = len(it.FileNames)
	}
	shown := len(it.FileNames)
	moreRow := 0
	if total > rows {
		moreRow = 1
		if shown > rows-1 {
			shown = rows - 1
		}
	}
	for i := 0; i < shown; i++ {
		t.gitFileLine(b, th, r, y+1+i, it.FileNames[i])
	}
	if moreRow == 1 {
		t.gitFileLine(b, th, r, y+1+shown, gitMoreFiles(total-shown))
	}
}

// gitFileLine paints one name on row row of the box: one cell in from the
// border, in the rail's dim, elided with the set's mark so a long path never
// wraps or pushes the box past the width the drag left it.
func (t *tui) gitFileLine(b *bytes.Buffer, th theme.Theme, r layout.Rect, row int, name string) {
	fmt.Fprintf(b, "\x1b[%d;%dH", row+1, r.X+1)
	segs := []segment.Seg{{Text: " " + name, FG: th.Palette.RailDim}}
	if segment.SegCells(segs) > r.W {
		segs = segment.ElideSegs(th, segs, r.W)
	}
	for _, s := range groundGitSegs(th, segs) {
		segment.Write(b, s)
	}
}
