package client

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"time"

	"dm/internal/cells"
	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/panel"
	"dm/internal/theme"
	"dm/internal/wire"
)

// layoutPresetNames are the five presets the palette lists, in the cycle order
// select-layout next walks. They mirror the server's presetOrder; the client
// cannot import it, so the two lists must be kept in step by hand.
var layoutPresetNames = []string{
	"even-horizontal", "even-vertical", "main-horizontal", "main-vertical", "tiled",
}

// layoutCustomRow is the palette's last row: the window's live tree.
const layoutCustomRow = 5

// layoutRowCount is the palette's row count: five presets and the custom row.
const layoutRowCount = 6

// layoutSwitcher is the open layout palette. It is client-owned state: it
// opens on the key, ends on pick or close, and never enters the session tree.
type layoutSwitcher struct {
	// sel is the selected row: 0 to 4 name a preset, 5 is the custom row.
	sel int
	// panes are the active window's pane ids at open time. draw refreshes
	// them from the snapshot every frame, so a pane that exits while the
	// palette is open updates the preview.
	panes []string
	// focus is the focused pane id, refreshed with panes.
	focus string

	// pressed remembers a left press on a list row, so a press and release on
	// the same cell is a click. pressX and pressY are that cell.
	pressed bool
	pressX  int
	pressY  int
}

// layoutPaletteGeometry bounds the palette: the list column, the preview band
// and the smallest screen that shows the preview at all.
const (
	layoutListW       = 22 // the list column's inner width
	layoutPreviewMinW = 24
	layoutPreviewMinH = 12
	layoutPreviewMaxW = 40
	layoutPreviewMaxH = 20
)

// layoutPalette is where the palette paints: the outer rect, the preview band
// and the list and footer rows, all in screen cells. An empty rect paints
// nothing.
type layoutPalette struct {
	rect    layout.Rect
	preview layout.Rect // the preview band; empty when the screen dropped it
	listY   int
	footY   int
}

// openLayoutSwitcher raises the layout palette over the live panes. A second
// call while it is open closes it and picks nothing.
func openLayoutSwitcher(t *tui) {
	if t.mode == "layout" {
		closeLayoutSwitcher(t)
		return
	}
	if t.snap == nil || t.snap.Cols < 1 || t.snap.Rows < 1 {
		return
	}
	panes := layout.AllPanes(t.snap.Root)
	if len(panes) == 0 {
		return
	}
	l := &layoutSwitcher{panes: panes, focus: t.snap.Focus}
	l.sel = l.currentRow(t.snap)
	t.lsw = l
	// The palette owns the keyboard through its own mode, but claims no
	// screen: the panes stay live under it, the way the dashboard's float
	// shape stays live.
	t.mode = "layout"
	t.disarm()
	t.dirty = true
}

// closeLayoutSwitcher drops the palette without acting on a choice. Its
// buffered input goes with it, so no key pressed over it reaches a pane
// afterwards.
func closeLayoutSwitcher(t *tui) {
	if t.lsw == nil && t.mode != "layout" {
		return
	}
	t.lsw = nil
	if t.mode == "layout" {
		t.mode = ""
	}
	t.inbuf = nil
	t.pending = false
	t.dirty = true
}

// refresh re-reads the active window's pane ids and focus from the snapshot.
// draw calls it once a frame while the palette is open.
func (l *layoutSwitcher) refresh(win *wire.Snapshot) {
	if win == nil {
		return
	}
	l.panes = layout.AllPanes(win.Root)
	l.focus = win.Focus
}

// currentRow returns the row that carries the current mark: the row matching
// the active window's stored layout name, or the custom row when the name is
// empty. It is also the row the palette opens on.
func (l *layoutSwitcher) currentRow(win *wire.Snapshot) int {
	name := ""
	if i := win.ActiveWindow; i >= 0 && i < len(win.Windows) {
		name = win.Windows[i].Layout
	}
	for i, n := range layoutPresetNames {
		if n == name {
			return i
		}
	}
	return layoutCustomRow
}

// layoutKey consumes one key while the palette is open. It returns false when
// it needs more bytes for an escape sequence.
func (t *tui) layoutKey() bool {
	if t.lsw == nil {
		closeLayoutSwitcher(t)
		return true
	}
	if len(t.inbuf) == 0 {
		return true
	}
	action, n, state := t.km().Match("layout", t.inbuf)
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
		// An unbound key closes the palette, as the menu's does.
		t.inbuf = append([]byte(nil), t.inbuf[escapeRun(t.inbuf):]...)
		t.pending = false
		closeLayoutSwitcher(t)
		return true
	}
	switch action {
	case "down", "next-arrow":
		t.layoutMove(1)
	case "up", "prev-arrow":
		t.layoutMove(-1)
	case "pick":
		t.layoutPickRow(t.lsw.sel)
	case "close", "close-esc", "close-prefix":
		closeLayoutSwitcher(t)
	}
	return true
}

// layoutMove steps the selection and wraps at both ends.
func (t *tui) layoutMove(step int) {
	if t.lsw == nil {
		return
	}
	t.lsw.sel = (t.lsw.sel + step + layoutRowCount) % layoutRowCount
	t.dirty = true
}

// layoutPickRow runs one row. A preset sends select-layout with its name
// and closes; the reply's toast path is the action reply's own. On a window
// with fewer than two panes a preset row picks nothing: every layout is the
// one the single pane already fills. The custom row closes with nothing sent.
// esc and q close through the close actions and never reach here.
func (t *tui) layoutPickRow(row int) {
	if t.lsw == nil {
		return
	}
	if row < 0 || row >= layoutRowCount {
		return
	}
	if row < layoutCustomRow && len(t.lsw.panes) < 2 {
		t.dirty = true
		return
	}
	name := ""
	if row < layoutCustomRow {
		name = layoutPresetNames[row]
	}
	closeLayoutSwitcher(t)
	if name == "" {
		return
	}
	t.action("select-layout", func(m *wire.Msg) { m.Layout = name })
}

// layoutPaneArea is the real pane area the preview keeps the aspect of: the
// area the server laid the window out over.
func layoutPaneArea(win *wire.Snapshot) layout.Rect {
	return layout.PaneAreaForWithTabBar(win.Cols, win.Rows, win.Rail)
}

// layoutFooterText is the palette's footer line: the dropped-preview note on a
// screen too small for the band, the one-pane line on a window with a single
// pane, and the key hint otherwise.
func layoutFooterText(show bool, paneCount int) string {
	switch {
	case !show:
		return "no room here for the preview"
	case paneCount < 2:
		return "one pane: every layout is this"
	}
	return "enter pick  esc close"
}

// layoutPaletteGeom places the palette, centred, sized to the wider of the
// list column and the preview. The preview keeps the pane area's aspect inside
// the 24x12 to 40x20 band. A screen too small for the minimum preview drops
// it: the palette shows the list alone with a footer note. A screen too small
// for the list alone paints nothing.
func layoutPaletteGeom(win *wire.Snapshot, paneCount int) layoutPalette {
	var g layoutPalette
	if win == nil || win.Cols < 1 || win.Rows < 1 {
		return g
	}
	// The preview's size: the largest scale that fits the band's maximum
	// while keeping the pane area's aspect, then lifted to the band's minimum
	// where the aspect allows it. An extreme aspect stretches one axis to its
	// minimum; the maximum is never left.
	area := layoutPaneArea(win)
	aw, ah := area.W, area.H
	if aw < 1 {
		aw = 1
	}
	if ah < 1 {
		ah = 1
	}
	s := math.Min(float64(layoutPreviewMaxW)/float64(aw), float64(layoutPreviewMaxH)/float64(ah))
	pw := int(math.Round(float64(aw) * s))
	ph := int(math.Round(float64(ah) * s))
	if pw < layoutPreviewMinW {
		pw = layoutPreviewMinW
	}
	if ph < layoutPreviewMinH {
		ph = layoutPreviewMinH
	}
	if pw > layoutPreviewMaxW {
		pw = layoutPreviewMaxW
	}
	if ph > layoutPreviewMaxH {
		ph = layoutPreviewMaxH
	}

	show := win.Cols >= pw+2 && win.Rows >= ph+layoutRowCount+3
	// The palette is as wide as its widest column: the list, the preview or
	// the footer line.
	w := layoutListW
	if show && pw > w {
		w = pw
	}
	if n := cells.Cells(layoutFooterText(show, paneCount)); n > w {
		w = n
	}
	w += 2                  // the borders
	h := layoutRowCount + 1 // the list and the footer
	if show {
		h += ph
	}
	if w > win.Cols-2 {
		w = win.Cols - 2
	}
	if w < 1 || h+2 > win.Rows {
		return g
	}
	g.rect = layout.Rect{X: (win.Cols - w) / 2, Y: (win.Rows - h - 2) / 2, W: w, H: h + 2}
	if g.rect.X < 0 {
		g.rect.X = 0
	}
	if g.rect.Y < 0 {
		g.rect.Y = 0
	}
	innerW := w - 2
	if show && pw > innerW {
		// A screen clamp narrowed the palette below the preview; the band
		// fills the interior and keeps its height.
		pw = innerW
	}
	if show {
		g.preview = layout.Rect{
			X: g.rect.X + 1 + (innerW-pw)/2,
			Y: g.rect.Y + 1,
			W: pw,
			H: ph,
		}
	}
	g.listY = g.rect.Y + 1 + g.preview.H
	g.footY = g.listY + layoutRowCount
	return g
}

// rowAt names the palette row under a zero-based screen point: the row's
// index, -2 for a cell inside the palette that is no row (the preview band
// takes no clicks), -1 for a cell outside the palette.
func (g layoutPalette) rowAt(x, y int) int {
	if x < g.rect.X || x >= g.rect.X+g.rect.W || y < g.rect.Y || y >= g.rect.Y+g.rect.H {
		return -1
	}
	if y >= g.listY && y < g.listY+layoutRowCount &&
		x >= g.rect.X+1 && x < g.rect.X+g.rect.W-1 {
		return y - g.listY
	}
	return -2
}

// layoutMouse runs the pointer while the palette is open. Motion lights the
// row under it, a press and release on one row cell picks it, and a press
// outside the palette closes it. The preview band takes no clicks.
func (t *tui) layoutMouse(ev mouseEvent, x, y int) {
	if t.lsw == nil {
		closeLayoutSwitcher(t)
		return
	}
	g := layoutPaletteGeom(t.snap, len(t.lsw.panes))
	if g.rect.W < 1 {
		return
	}
	row := g.rowAt(x, y)
	if ev.btn&64 != 0 {
		return // a wheel over the palette is not a click
	}
	if ev.btn&32 != 0 {
		// Motion: hover lights the row under the pointer.
		if row >= 0 && row != t.lsw.sel {
			t.lsw.sel = row
			t.dirty = true
		}
		return
	}
	if ev.release {
		pressed := t.lsw.pressed
		t.lsw.pressed = false
		if pressed && row >= 0 && x == t.lsw.pressX && y == t.lsw.pressY {
			// A press and release on one cell is a click; the pick may
			// close the palette, so the press mark goes first.
			t.layoutPickRow(row)
		}
		return
	}
	// A press on a row selects it and remembers the cell, so a release on the
	// same cell is a click that runs it.
	if row >= 0 {
		if row != t.lsw.sel {
			t.lsw.sel = row
			t.dirty = true
		}
		t.lsw.pressed = true
		t.lsw.pressX = x
		t.lsw.pressY = y
		return
	}
	if row == -1 {
		// A press outside the palette closes it.
		closeLayoutSwitcher(t)
	}
}

// drawLayoutSwitcher paints the palette: a bordered panel with the preview
// band on top, the six rows under it and a footer. The row the selection sits
// on is highlighted; the row matching the window's stored layout name carries
// the current mark; on a one-pane window every preset row dims and the footer
// says why.
func drawLayoutSwitcher(buf *bytes.Buffer, th theme.Theme, win *wire.Snapshot, l *layoutSwitcher) {
	g := layoutPaletteGeom(win, len(l.panes))
	if g.rect.W < 1 || l == nil {
		return
	}
	inner := layout.Rect{X: g.rect.X + 1, Y: g.rect.Y + 1, W: g.rect.W - 2, H: g.rect.H - 2}
	panel.PaintPanel(buf, th, inner, panel.PanelTitle{
		Text:  th.Glyphs.BorderHorizontal + " layout ",
		Style: "\x1b[0m\x1b[90m",
		Fill:  th.Glyphs.BorderHorizontal,
	}, "", false, func(b *bytes.Buffer, r layout.Rect) {
		if g.preview.W > 0 {
			rects, ok := layout.MiniRects(win.Root, g.preview, l.panes, layoutRowName(l.sel))
			if ok {
				paintLayoutPreview(b, th, rects, l.focus)
			}
		}
		one := len(l.panes) < 2
		current := l.currentRow(win)
		for i := 0; i < layoutRowCount; i++ {
			mark := " "
			if i == l.sel {
				mark = ">"
			}
			now := ""
			if i == current {
				now = "now"
			}
			name := "(custom)"
			if i < layoutCustomRow {
				name = layoutPresetNames[i]
			}
			line := cells.Pad(" "+mark+" "+cells.Pad(name, 16)+" "+cells.Pad(now, 3), r.W)
			colour := "\x1b[0m"
			switch {
			case i == l.sel:
				colour = "\x1b[0m\x1b[30;46m"
			case one && i < layoutCustomRow:
				colour = "\x1b[0m\x1b[90m"
			}
			fmt.Fprintf(b, "\x1b[%d;%dH%s%s", g.listY+i+1, r.X+1, colour, line)
		}
		foot := layoutFooterText(g.preview.W > 0, len(l.panes))
		fmt.Fprintf(b, "\x1b[%d;%dH\x1b[0m\x1b[90m%s", g.footY+1, r.X+1, cells.Pad(foot, r.W))
	})
	buf.WriteString("\x1b[0m")
}

// layoutRowName is the preset name a row stands for, or "" for the custom row.
func layoutRowName(row int) string {
	if row < 0 || row >= layoutCustomRow {
		return ""
	}
	return layoutPresetNames[row]
}

// paintLayoutPreview draws each miniature rectangle as a bordered box with the
// pane id inside, elided to fit. The focused pane's box carries the accent.
func paintLayoutPreview(buf *bytes.Buffer, th theme.Theme, rects map[string]layout.Rect, focus string) {
	dim := "\x1b[0m\x1b[90m"
	accent := theme.Style{Fg: styleColor(th.Palette.BarAccent), Attrs: theme.AttrBold}.SGR()
	for id, r := range rects {
		style := dim
		if id == focus {
			style = accent
		}
		if r.W < 2 || r.H < 2 {
			continue
		}
		g := th.Glyphs
		fmt.Fprintf(buf, "%s\x1b[%d;%dH%s%s%s", style, r.Y+1, r.X+1,
			g.BorderTopLeft, strings.Repeat(g.BorderHorizontal, r.W-2), g.BorderTopRight)
		fmt.Fprintf(buf, "\x1b[%d;%dH%s%s%s", r.Y+r.H, r.X+1,
			g.BorderBottomLeft, strings.Repeat(g.BorderHorizontal, r.W-2), g.BorderBottomRight)
		for y := r.Y + 1; y < r.Y+r.H-1; y++ {
			fmt.Fprintf(buf, "\x1b[%d;%dH%s", y+1, r.X+1, g.BorderVertical)
			if r.W > 1 {
				fmt.Fprintf(buf, "\x1b[%d;%dH%s", y+1, r.X+r.W, g.BorderVertical)
			}
		}
		if r.W >= 4 && r.H >= 3 {
			fmt.Fprintf(buf, "\x1b[0m\x1b[%d;%dH%s", r.Y+2, r.X+2, cells.Elide(id, r.W-2))
		}
	}
	buf.WriteString("\x1b[0m")
}
