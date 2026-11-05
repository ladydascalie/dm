package main

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"dm/internal/cells"
	"dm/internal/layout"
	"dm/internal/panel"
	"dm/internal/rail"
	"dm/internal/segment"
	"dm/internal/theme"
	"dm/internal/wire"
)

// drawMenu paints the right-click menu over the panes. The panel owns the border,
// the title row and the interior clip; the menu owns its item rows and the
// selection highlight. The title row is a fill row: dim, with the label between
// the border rune and a border-rune fill, where the panel's label style is
// bright, so the row is one write and its cells are the menu's.
func drawMenu(buf *bytes.Buffer, th theme.Theme, m *menu) {
	if m == nil || m.w < 8 {
		return
	}
	inner := layout.Rect{X: m.x + 1, Y: m.y + 1, W: m.w - 2, H: len(m.items)}
	panel.PaintPanel(buf, th, inner, panel.PanelTitle{
		Text:  th.Glyphs.BorderHorizontal + " " + m.title + " ",
		Style: "\x1b[0m\x1b[90m",
		Fill:  th.Glyphs.BorderHorizontal,
	}, "", false, func(b *bytes.Buffer, r layout.Rect) {
		for i, it := range m.items {
			line := cells.Pad(" "+cells.Pad(it.label, 22)+" "+cells.Pad(it.key, 4), r.W)
			colour := "\x1b[0m\x1b[90m"
			if i == m.sel {
				colour = "\x1b[0m\x1b[30;46m"
			}
			fmt.Fprintf(b, "\x1b[%d;%dH%s%s", r.Y+i+1, r.X+1, colour, line)
		}
	})
	buf.WriteString("\x1b[0m")
}

// drawBar paints the one-line status bar on the last row. It paints the
// segment model span by span: reset, ground, foreground, weight, text. The
// pinned block is anchored to the last column and the gutter fills the gap, so
// the whole row is written and no cell of the previous frame shows through.
// drawBar writes at most snap.Cols cells and never touches another row.
func drawBar(buf *bytes.Buffer, th theme.Theme, snap *wire.Snapshot, armed bool, info string) {
	if snap == nil || snap.Rows < 1 || snap.Cols < 1 {
		return
	}
	segs := barSegments(th, snap, armed, info)
	left, pinned := barSplitPinned(segs)
	fmt.Fprintf(buf, "\x1b[%d;1H", snap.Rows)
	for _, s := range left {
		segment.Write(buf, s)
	}
	if n := snap.Cols - segment.SegCells(segs); n > 0 {
		// The bar's ground is the rail's: there is no second background.
		buf.WriteString("\x1b[0m" + th.Palette.RailBG.BG() + strings.Repeat(" ", n))
	}
	for _, s := range pinned {
		segment.Write(buf, s)
	}
	buf.WriteString("\x1b[0m")
}

// barSplitPinned splits a span run at its first pinned span: the left block,
// then the right block that is anchored to the last column.
func barSplitPinned(segs []segment.Seg) (left, pinned []segment.Seg) {
	for i, s := range segs {
		if s.Pin {
			return segs[:i], segs[i:]
		}
	}
	return segs, nil
}

// barWindow is one window chip of the left block: its index in the window
// list, its name, and whether it is the active window.
type barWindow struct {
	num    int
	name   string
	active bool
}

// barFocusChip is the focus chip of the left block: a state mark, the pane id,
// and the pane name and status word while they survive elision. The zoom mark
// sits after the id, so the chip names the zoomed state.
type barFocusChip struct {
	glyph  string
	colour theme.RGB
	id     string
	name   string
	status string
}

// barLeft is the left block while it is being elided. The strings hold their
// current, already shrunk text, and the flags hold the parts still shown.
type barLeft struct {
	session   string
	workspace string
	windows   []barWindow
	focus     *barFocusChip
	winNames  bool
	showName  bool
	showStat  bool
	showZoom  bool
}

// barFocus builds the focus chip from the focused pane. An agent reports a
// state, so it takes that state's glyph and colour; a shell gets the prompt
// marker in dim text. A pane the snapshot does not list gets no chip.
func barFocus(th theme.Theme, snap *wire.Snapshot) *barFocusChip {
	var p wire.PaneInfo
	found := false
	for _, cand := range snap.Panes {
		if cand.ID == snap.Focus {
			p, found = cand, true
			break
		}
	}
	if !found {
		return nil
	}
	status := p.Status
	if status == "" {
		status = "unknown"
	}
	// The rail's pane list carries the agent flag; a status word alone marks an
	// agent when the list is absent.
	agent := status != "unknown"
	for _, a := range snap.All {
		if a.ID == snap.Focus {
			agent = a.Agent
			break
		}
	}
	shell := rail.StateStyle(th, "shell")
	glyph, colour := shell.Glyph, shell.Colour
	if agent {
		glyph, colour = rail.RailGlyph(th, status, rail.NerdFonts())
	}
	return &barFocusChip{
		glyph: glyph, colour: colour,
		id: p.ID, name: p.Name, status: status,
	}
}

// barLeftFor reads the left block from a snapshot in its full, unelided form.
func barLeftFor(th theme.Theme, snap *wire.Snapshot) *barLeft {
	l := &barLeft{
		session:   snap.Session,
		workspace: snap.Workspace,
		winNames:  true,
		showName:  true,
		showStat:  true,
		showZoom:  snap.Zoom != "",
	}
	for i, name := range snap.Windows {
		l.windows = append(l.windows, barWindow{num: i + 1, name: name, active: i == snap.ActiveWindow})
	}
	l.focus = barFocus(th, snap)
	return l
}

// groups builds the left block spans as they currently stand: session,
// workspace, one chip per window, then the focus chip.
func (l *barLeft) groups(th theme.Theme) [][]segment.Seg {
	p, g := th.Palette, th.Glyphs
	var out [][]segment.Seg
	if l.session != "" {
		out = append(out, []segment.Seg{{Text: l.session, FG: p.RailText, BG: p.RailBG}})
	}
	if l.workspace != "" {
		out = append(out, []segment.Seg{{Text: l.workspace, FG: p.RailHeader, BG: p.RailBG}})
	}
	for _, w := range l.windows {
		if w.active {
			// The active window reads as active: an accent mark, then the chip
			// in bold on the focus fill.
			text := strconv.Itoa(w.num)
			if l.winNames && w.name != "" {
				text += " " + w.name
			}
			out = append(out, []segment.Seg{
				{Text: g.ActiveMark + " ", FG: p.BarAccent, BG: p.RailFocusBG},
				{Text: text, FG: p.RailText, BG: p.RailFocusBG, Bold: true},
			})
			continue
		}
		text := strconv.Itoa(w.num)
		if l.winNames && w.name != "" {
			text += " " + w.name
		}
		out = append(out, []segment.Seg{{Text: text, FG: p.RailDim, BG: p.RailBG}})
	}
	if l.focus != nil {
		chip := []segment.Seg{
			{Text: l.focus.glyph, FG: l.focus.colour, BG: p.RailFocusBG},
			{Text: " " + l.focus.id, FG: p.RailText, BG: p.RailFocusBG, Bold: true},
		}
		if l.showZoom {
			chip = append(chip, segment.Seg{Text: " Z", FG: p.BarAccent, BG: p.RailFocusBG, Bold: true})
		}
		if l.showName && l.focus.name != "" {
			chip = append(chip, segment.Seg{Text: " " + l.focus.name, FG: p.RailDim, BG: p.RailFocusBG})
		}
		if l.showStat && l.focus.status != "" {
			chip = append(chip, segment.Seg{Text: " " + l.focus.status, FG: p.RailDim, BG: p.RailFocusBG})
		}
		out = append(out, chip)
	}
	return out
}

// fit elides the left block to budget cells in the ladder the footer uses:
// window names, then trailing window chips, then the workspace name, then the
// session name, then the focus chip's status word and name. The focus chip
// keeps its glyph and id last, and the result never exceeds the budget.
func (l *barLeft) fit(th theme.Theme, budget int) []segment.Seg {
	if budget <= 0 {
		return nil
	}
	gap := segment.Seg{Text: " ", FG: th.Palette.RailDim, BG: th.Palette.RailBG}
	cur := func() []segment.Seg { return segment.Join(l.groups(th), gap) }
	if segs := cur(); segment.SegCells(segs) <= budget {
		return segs
	}

	// 1. Window chips lose their name and keep the number.
	l.winNames = false
	if segs := cur(); segment.SegCells(segs) <= budget {
		return segs
	}

	// 2. Trailing window chips go, never the active one.
	for len(l.windows) > 1 {
		i := len(l.windows) - 1
		for i >= 0 && l.windows[i].active {
			i--
		}
		if i < 0 {
			break
		}
		l.windows = append(l.windows[:i], l.windows[i+1:]...)
		if segs := cur(); segment.SegCells(segs) <= budget {
			return segs
		}
	}

	// 3. The workspace name, then 4. the session name, shrink to a trailing
	// cells.Ellipsis. A name that cannot shrink leaves its cells.Ellipsis alone; later
	// steps make the room.
	for _, name := range []*string{&l.workspace, &l.session} {
		if *name == "" {
			continue
		}
		avail := budget - (segment.SegCells(cur()) - cells.Cells(*name))
		if avail < 1 {
			avail = 1
		}
		*name = cells.Elide(*name, avail)
		if segs := cur(); segment.SegCells(segs) <= budget {
			return segs
		}
	}

	// 5. The focus chip drops its status word, then the zoom mark, then its
	// name. The glyph and the id are last, and the clip below cuts them.
	l.showStat = false
	if segs := cur(); segment.SegCells(segs) <= budget {
		return segs
	}
	l.showZoom = false
	if segs := cur(); segment.SegCells(segs) <= budget {
		return segs
	}
	l.showName = false
	if segs := cur(); segment.SegCells(segs) <= budget {
		return segs
	}

	// 6. Only the glyph and the id are left; a line too narrow for that is cut
	// run by run. The first run the room cannot hold ends the line, so no later
	// run paints out of order.
	return segment.ClipSegs(cur(), budget)
}

// barPinned is the right block: the prefix flag while armed, then the scroll
// info. Both are anchored to the last column, so both are pinned.
func barPinned(th theme.Theme, armed bool, info string) []segment.Seg {
	p := th.Palette
	var out []segment.Seg
	if armed {
		out = append(out, segment.Seg{Text: "PREFIX ", FG: p.RailText, BG: p.RailBG, Bold: true, Pin: true})
	}
	if info != "" {
		out = append(out, segment.Seg{Text: info, FG: p.RailDim, BG: p.RailBG, Pin: true})
	}
	return out
}

// barFitPinned fits the right block to cols cells on its own. A pinned segment
// goes whole, leftmost first, and the survivor is clipped with an cells.Ellipsis.
func barFitPinned(segs []segment.Seg, cols int) []segment.Seg {
	for segment.SegCells(segs) > cols {
		if len(segs) > 1 {
			segs = segs[1:]
			continue
		}
		if len(segs) == 0 {
			break
		}
		segs[0].Text = cells.Elide(segs[0].Text, cols)
		break
	}
	return segs
}

// barSegments returns the status bar as spans in paint order: the left block,
// then the pinned right block. It sizes the left block against the room the
// pinned block leaves, minus one gutter cell, so the blocks never touch and the
// line never runs past snap.Cols cells. The pinned block is never dropped to
// make room for the left one.
func barSegments(th theme.Theme, snap *wire.Snapshot, armed bool, info string) []segment.Seg {
	if snap == nil || snap.Cols <= 0 {
		return nil
	}
	right := barFitPinned(barPinned(th, armed, info), snap.Cols)
	budget := snap.Cols - segment.SegCells(right)
	if len(right) > 0 {
		// Keep one gutter cell between the blocks. drawBar spends it as part of
		// the pad, so the pinned block still ends on the last column.
		budget--
	}
	if budget < 0 {
		budget = 0
	}
	left := barLeftFor(th, snap).fit(th, budget)
	return append(left, right...)
}
