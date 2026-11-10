package client

import (
	"bytes"
	"fmt"
	"strings"

	"dm/internal/cells"
	"dm/internal/layout"
	"dm/internal/panel"
	"dm/internal/theme"
	"dm/internal/wire"
)

// diffViewID is the pager float's id.
const diffViewID = "diff"

// diffView is the read-only diff pager: the lines the server sent, the first
// line in view, and the float that draws them. It takes no input beyond
// scrolling and closing, so it owns no draft and changes nothing.
type diffView struct {
	lines []string
	off   int
	th    theme.Theme
	float *panel.FloatingPane
}

// diffTitle names the pager after the worktree the change belongs to. A pane in
// the main checkout falls back to its branch, then to its id, so the border
// always says where the diff came from.
func diffTitle(p wire.AllPane, pane string) string {
	name := p.Worktree
	if name == "" {
		name = p.Branch
	}
	if name == "" {
		name = pane
	}
	return " diff " + name + " "
}

// newDiffView opens the pager on one pane's diff.
func newDiffView(snap *wire.Snapshot, title string, lines []string) *diffView {
	d := &diffView{
		lines: lines,
		th:    theme.DefaultTheme(),
		float: &panel.FloatingPane{ID: diffViewID, Active: true, Title: title},
	}
	d.float.Paint = d.paint
	d.sync(snap)
	return d
}

// diffRect is where the pager sits: centred, eight cells clear of the screen's
// sides, at most 110 columns wide and rows-4 tall, so the box and its border
// always fit.
func diffRect(snap *wire.Snapshot) layout.Rect {
	w := snap.Cols - 8
	if w > 110 {
		w = 110
	}
	if w < 10 {
		w = snap.Cols - 2
	}
	h := snap.Rows - 4
	if h < 3 {
		h = snap.Rows - 2
	}
	x := (snap.Cols - w) / 2
	y := (snap.Rows - (h + 2)) / 2
	if x+w > snap.Cols-1 {
		x = snap.Cols - 1 - w
	}
	if y+h > snap.Rows-1 {
		y = snap.Rows - 1 - h
	}
	if x < 1 {
		x = 1
	}
	if y < 1 {
		y = 1
	}
	return layout.Rect{X: x, Y: y, W: w, H: h}
}

// sync fits the float to the screen and its hint to the keys.
func (d *diffView) sync(snap *wire.Snapshot) {
	d.float.Rect = diffRect(snap)
	d.float.Hint = " j/k scroll  pgup/pgdn page  g/G ends  q esc close "
	d.clamp()
}

// RowCount is the number of rows the pager holds.
func (d *diffView) RowCount() int {
	return len(d.lines)
}

// Offset is the first row in view.
func (d *diffView) Offset() int {
	return d.off
}

// Scroll moves the window by n rows and stops at the ends of the diff.
func (d *diffView) scroll(n int) {
	d.off += n
	d.clamp()
}

// clamp keeps the window on the rows, so the last page ends the diff.
func (d *diffView) clamp() {
	max := len(d.lines) - d.float.Rect.H
	if max < 0 {
		max = 0
	}
	if d.off > max {
		d.off = max
	}
	if d.off < 0 {
		d.off = 0
	}
}

// paint writes the visible window into the float's content rect, on the rail
// ground so nothing of the frame underneath shows through.
func (d *diffView) paint(buf *bytes.Buffer, inner layout.Rect) {
	if inner.W <= 0 || inner.H <= 0 {
		return
	}
	ground := d.th.Palette.RailBG
	for row := 0; row < inner.H; row++ {
		i := d.off + row
		fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m%s", inner.Y+row+1, inner.X+1, ground.BG())
		if i >= len(d.lines) {
			buf.WriteString(strings.Repeat(" ", inner.W))
			buf.WriteString("\x1b[0m")
			continue
		}
		line := cells.Clip(d.lines[i], inner.W)
		buf.WriteString(diffLineStyle(d.th, d.lines[i]))
		buf.WriteString(line)
		if n := inner.W - cells.Cells(line); n > 0 {
			buf.WriteString(strings.Repeat(" ", n))
		}
		buf.WriteString("\x1b[0m")
	}
}

// diffLineStyle is the colour one diff line paints in: added lines green,
// removed lines red, hunk headers bright, and the file headers, the index lines
// and the stat block dim.
func diffLineStyle(th theme.Theme, line string) string {
	p := th.Palette
	switch {
	case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
		return p.RailDim.FG()
	case strings.HasPrefix(line, "+"):
		return p.RailDiffAdd.FG()
	case strings.HasPrefix(line, "-"):
		return p.RailDiffDel.FG()
	case strings.HasPrefix(line, "@@"):
		return p.RailText.FG() + "\x1b[1m"
	}
	return p.RailDim.FG()
}

// openDiff closes the dashboard and asks the server for the selected pane's
// uncommitted diff. The reply opens the pager.
func (t *tui) openDiff() {
	if t.sw == nil || t.conn == nil {
		return
	}
	pane := t.sw.Target()
	if pane == "" {
		return
	}
	closeSwitcher(t)
	t.diffID++
	t.diffPane = pane
	_ = t.conn.Send(wire.Msg{Type: "req", Action: "diff", Target: pane, ID: t.diffID})
}

// openDiffView puts the diff reply in a read-only pager over the panes.
func (t *tui) openDiffView(lines []string) {
	if t.snap == nil || len(lines) == 0 {
		t.notify("diff: nothing to show")
		return
	}
	t.diff = newDiffView(t.snap, diffTitle(t.allPane(t.diffPane), t.diffPane), lines)
	t.AddFloatingPane(t.diff.float)
	t.mode = "diff"
	t.inbuf = nil
	t.pending = false
	t.dirty = true
}

// closeDiff takes the pager down and gives the keys back to the panes.
func (t *tui) closeDiff() {
	if t.diff == nil {
		return
	}
	t.RemoveFloatingPane(t.diff.float.ID)
	t.diff = nil
	t.mode = ""
	// The close key stays in the buffer while the pending timer waits. Drop the
	// whole buffer here, so a later keypress cannot forward a stray escape to a
	// pane.
	t.inbuf = nil
	t.pending = false
	t.dirty = true
}

// allPane finds a pane in the snapshot by id, for a label the pane carried when
// the diff was asked for.
func (t *tui) allPane(id string) wire.AllPane {
	if t.snap == nil {
		return wire.AllPane{ID: id}
	}
	for _, p := range t.snap.All {
		if p.ID == id {
			return p
		}
	}
	return wire.AllPane{ID: id}
}

// diffWheel scrolls the pager from the wheel. A notch up goes toward the top of
// the diff. Every other mouse event is swallowed.
func (t *tui) diffWheel(ev mouseEvent) {
	if t.diff == nil {
		return
	}
	if ev.btn&64 == 0 || ev.release {
		return
	}
	step := -3
	if ev.btn&1 == 0 {
		step = 3
	}
	t.diff.scroll(step)
	t.dirty = true
}
