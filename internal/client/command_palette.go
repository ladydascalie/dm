package client

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"dm/internal/cells"
	"dm/internal/configdoc"
	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/palette"
	"dm/internal/panel"
	"dm/internal/theme"
	"dm/internal/wire"
)

// commandPaletteBounds bounds the float: the narrowest panel it opens at and
// the column the command names clip to.
const (
	commandPaletteMinW = 40
	commandPaletteMaxW = 65 // borders, row chrome and the widest command name
)

// commandPalette is the open command palette. It is client-owned state: it
// opens on the key, ends on a pick or a close, and never enters the session
// tree. The command list is the main keymap's actions, so a row dispatches
// through the same runAction path the real key uses.
type commandPalette struct {
	pal *palette.Model
	// total is the command count the palette opened with. The float keeps
	// its height while the query filters, so the body is sized for the
	// whole list, not the rows left.
	total int
	// capRows is the visible-row cap the config named when the palette
	// opened. The float keeps its height while the query filters.
	capRows int
	// help is the open help float: ? over a selected command explains it.
	// The palette stays up under it.
	help bool
}

// commandPaletteCommands builds the palette's rows from the source the help
// view shares: the main keymap's actions, described by the configdoc table.
// The action id is the same string runAction dispatches on.
func commandPaletteCommands(cfg *keymap.Config) []palette.Command {
	var out []palette.Command
	for _, m := range cfg.Keymaps() {
		if m.Name != "main" {
			continue
		}
		for _, a := range m.Actions {
			out = append(out, palette.Command{
				Name:   a.Action,
				Hint:   configdoc.ActionDesc[a.Action],
				Action: a.Action,
				Key:    cfg.Key("main", a.Action),
			})
		}
	}
	return out
}

// commandPaletteGeom sizes and centres the float: wide enough for a name and
// its description, tall enough for the query line and as many rows as the row
// cap allows and the screen holds. A screen too small for the query line and
// one row returns an empty rect.
func commandPaletteGeom(win *wire.Snapshot, rows, cap int) layout.Rect {
	if win == nil || win.Cols < 1 || win.Rows < 1 {
		return layout.Rect{}
	}
	w := commandPaletteMaxW
	if w > win.Cols-2 {
		w = win.Cols - 2
	}
	if w < commandPaletteMinW {
		return layout.Rect{}
	}
	body := rows
	if body > cap {
		body = cap
	}
	if body > win.Rows-4 {
		body = win.Rows - 4
	}
	if body < 1 {
		return layout.Rect{}
	}
	// The query line and the list sit inside the borders.
	h := body + 3
	return layout.Rect{
		X: (win.Cols - w) / 2,
		Y: (win.Rows - h) / 2,
		W: w,
		H: h,
	}
}

// openCommandPalette raises the palette over the live panes. A second call
// while it is open closes it and picks nothing.
func openCommandPalette(t *tui) {
	if t.cp != nil {
		closeCommandPalette(t)
		return
	}
	if t.snap == nil || t.snap.Cols < 1 || t.snap.Rows < 1 {
		return
	}
	commands := commandPaletteCommands(t.km())
	if len(commands) == 0 {
		return
	}
	if g := commandPaletteGeom(t.snap, len(commands), t.km().PaletteRows()); g.W < 1 || g.H < 1 {
		return
	}
	t.cp = &commandPalette{pal: palette.New(commands), total: len(commands), capRows: t.km().PaletteRows()}
	t.disarm()
	t.dirty = true
}

// closeCommandPalette drops the palette without acting on a choice. Its
// buffered input goes with it, so no key pressed over it reaches a pane
// afterwards.
func closeCommandPalette(t *tui) {
	if t.cp == nil {
		return
	}
	t.cp = nil
	t.inbuf = nil
	t.pending = false
	t.dirty = true
}

// paletteArrowUp returns the length of the up-arrow sequence at the head of
// buf, in both encodings the terminal can send, or 0 for none. The palette
// reads the arrows by hand: it owns its keys without a keymap mode.
func paletteArrowUp(buf []byte) int {
	for _, seq := range [][]byte{{0x1b, '[', 'A'}, {0x1b, 'O', 'A'}} {
		if bytes.HasPrefix(buf, seq) {
			return len(seq)
		}
	}
	return 0
}

// paletteArrowDown is paletteArrowUp for the down arrow.
func paletteArrowDown(buf []byte) int {
	for _, seq := range [][]byte{{0x1b, '[', 'B'}, {0x1b, 'O', 'B'}} {
		if bytes.HasPrefix(buf, seq) {
			return len(seq)
		}
	}
	return 0
}

// commandPaletteKey consumes one key while the palette is open. The palette
// owns every key: printable bytes feed the query, backspace edits, the
// arrows and ctrl+p/n walk the selection, enter invokes, esc closes. It
// returns false when it needs more bytes for an escape sequence.
func (t *tui) commandPaletteKey() bool {
	if t.cp == nil {
		return true
	}
	if len(t.inbuf) == 0 {
		return true
	}
	if n := paletteArrowUp(t.inbuf); n > 0 {
		t.takePaletteBytes(n)
		t.closePaletteHelp()
		t.cp.pal.Cycle(-1)
		t.dirty = true
		return true
	}
	if n := paletteArrowDown(t.inbuf); n > 0 {
		t.takePaletteBytes(n)
		t.closePaletteHelp()
		t.cp.pal.Cycle(1)
		t.dirty = true
		return true
	}
	b := t.inbuf[0]
	switch {
	case b == 0x1b:
		n := escapeRun(t.inbuf)
		if n == len(t.inbuf) && n > 1 && (t.inbuf[1] == '[' || t.inbuf[1] == 'O') {
			// The head of a CSI sequence is still arriving. Wait for the
			// rest; the expiry closes on a stall.
			if !t.pending {
				t.pending = true
				t.escAt = time.Now()
			}
			return false
		}
		t.takePaletteBytes(n)
		if n == 1 {
			// A lone esc closes the help float when it is open and the
			// palette otherwise. A longer sequence carries no key the
			// palette knows, so it is swallowed whole.
			if !t.closePaletteHelp() {
				closeCommandPalette(t)
			}
		} else {
			t.dirty = true
		}
	case b == 0x7f || b == 0x08:
		t.takePaletteBytes(1)
		t.closePaletteHelp()
		t.cp.pal.Backspace()
		t.dirty = true
	case b == 0x0d || b == 0x0a:
		t.takePaletteBytes(1)
		t.invokeCommandPalette()
	case b == 0x10:
		// ctrl+p walks the selection up, the way the arrows do.
		t.takePaletteBytes(1)
		t.closePaletteHelp()
		t.cp.pal.Cycle(-1)
		t.dirty = true
	case b == 0x0e:
		// ctrl+n walks the selection down.
		t.takePaletteBytes(1)
		t.closePaletteHelp()
		t.cp.pal.Cycle(1)
		t.dirty = true
	case b == '?':
		// ? never enters the query: over a selected command it opens the
		// help float, over the open one it closes it.
		t.takePaletteBytes(1)
		t.togglePaletteHelp()
		t.dirty = true
	case b >= 0x20 && b < 0x7f:
		t.takePaletteBytes(1)
		t.closePaletteHelp()
		t.cp.pal.AppendRune(rune(b))
		t.dirty = true
	default:
		// A control byte the palette does not name, or the head byte of a
		// multi-byte rune: the switcher takes neither as text, and neither
		// does the palette.
		t.takePaletteBytes(1)
		t.closePaletteHelp()
	}
	return true
}

// closePaletteHelp closes the palette's help float when it is open and
// reports whether it was. The palette itself stays up.
func (t *tui) closePaletteHelp() bool {
	if t.cp == nil || !t.cp.help {
		return false
	}
	t.cp.help = false
	t.dirty = true
	return true
}

// togglePaletteHelp opens the help float for the selected command, or closes
// the one that is open. With no command under the selection it does nothing:
// there is nothing to explain.
func (t *tui) togglePaletteHelp() {
	if t.cp == nil {
		return
	}
	if t.cp.help {
		t.cp.help = false
		t.dirty = true
		return
	}
	if _, ok := t.cp.pal.Selected(); ok {
		t.cp.help = true
		t.dirty = true
	}
}

// takePaletteBytes consumes n bytes of buffered input and clears the wait for
// a sequence's tail.
func (t *tui) takePaletteBytes(n int) {
	t.inbuf = append([]byte(nil), t.inbuf[n:]...)
	t.pending = false
}

// expireCommandPalette closes a palette whose lone esc never finished
// arriving. A bare esc is the close key, so a stalled one means close.
func (t *tui) expireCommandPalette(now time.Time) {
	if t.cp == nil || !t.pending {
		return
	}
	if now.Sub(t.escAt) <= escTimeout {
		return
	}
	t.pending = false
	t.inbuf = t.inbuf[:0]
	closeCommandPalette(t)
}

// invokeCommandPalette runs the selected command and closes the palette
// first, so a command that opens another modal never stacks on it. An empty
// selection closes silently.
func (t *tui) invokeCommandPalette() {
	cmd, ok := t.cp.pal.Selected()
	closeCommandPalette(t)
	if !ok {
		return
	}
	t.runAction(cmd.Action)
}

// drawCommandPalette paints the float: a bordered panel with the query line
// on top and the filtered rows under it, the selection on the focus ground.
// It returns the query cursor cell. Drawing goes through the pal paint band,
// the path the switcher draws on.
func drawCommandPalette(buf *bytes.Buffer, th theme.Theme, cp *commandPalette, win *wire.Snapshot) (cx, cy int) {
	rows := cp.pal.Rows()
	g := commandPaletteGeom(win, cp.total, cp.capRows)
	if g.W < 3 || g.H < 3 {
		return 0, 0
	}
	inner := layout.Rect{X: g.X + 1, Y: g.Y + 1, W: g.W - 2, H: g.H - 2}
	panel.PaintPanel(buf, th, inner, panel.PanelTitle{
		Text:  th.Glyphs.BorderHorizontal + " palette ",
		Style: "\x1b[0m\x1b[90m" + groundSGR(th.Palette.RailBG),
		Fill:  th.Glyphs.BorderHorizontal,
	}, "", false, func(b *bytes.Buffer, r layout.Rect) {
		// PaintPanel fills the interior with the rail ground; the query and
		// the rows paint their own grounds on top of it.
		hint := ""
		if !cp.help {
			hint = paletteHelpHint
		}
		cx, cy = paintPaletteQuery(b, th, cp.pal.Query(), r, hint)
		paintPaletteRows(b, th, rows, cp.pal.SelectedIndex(), r)
	})
	if cp.help {
		paintPaletteHelp(buf, th, cp, g)
	}
	buf.WriteString("\x1b[0m")
	return cx, cy
}

// paletteHelpWidth is the widest line the help float wraps its description
// to, and the widest box it opens at.
const paletteHelpWidth = 56

// paletteHelpHint is the query line's right-edge signpost: ? explains the
// command under the selection.
const paletteHelpHint = "? help"

// paintPaletteHelp paints the help float for the selected command: a small
// panel centred over the palette, carrying the command's key, its name and
// its description wrapped to the float's width. A palette too small to hold
// it paints nothing.
func paintPaletteHelp(buf *bytes.Buffer, th theme.Theme, cp *commandPalette, g layout.Rect) {
	cmd, ok := cp.pal.Selected()
	if !ok {
		return
	}
	lines := paletteWrap(cmd.Hint, paletteHelpWidth)
	h := len(lines) + 3 // key line, borders, one blank row of air
	w := paletteHelpWidth + 2
	if w > g.W-2 {
		w = g.W - 2
	}
	if w < 10 {
		return
	}
	for len(lines) > 0 && h > g.H-2 {
		lines = lines[:len(lines)-1]
		h = len(lines) + 3
	}
	if h < 3 || len(lines) == 0 {
		return
	}
	r := layout.Rect{
		X: g.X + (g.W-w)/2,
		Y: g.Y + (g.H-h)/2,
		W: w,
		H: h,
	}
	inner := layout.Rect{X: r.X + 1, Y: r.Y + 1, W: r.W - 2, H: r.H - 2}
	panel.PaintPanel(buf, th, inner, panel.PanelTitle{
		Text:  th.Glyphs.BorderHorizontal + " " + cmd.Name + " ",
		Style: "\x1b[0m\x1b[90m" + groundSGR(th.Palette.RailBG),
		Fill:  th.Glyphs.BorderHorizontal,
	}, "", false, func(b *bytes.Buffer, r layout.Rect) {
		fmt.Fprintf(b, "\x1b[%d;%dH", r.Y+1, r.X+1)
		b.WriteString("\x1b[0m")
		b.WriteString(th.Palette.RailBG.BG())
		b.WriteString(th.Palette.RailText.FG())
		if key := cmd.Key; key != "" {
			b.WriteString(key + "  ")
		}
		b.WriteString("\x1b[0m")
		for i, line := range lines {
			fmt.Fprintf(b, "\x1b[%d;%dH", r.Y+2+i, r.X+1)
			b.WriteString("\x1b[0m")
			b.WriteString(th.Palette.RailBG.BG())
			b.WriteString(th.Palette.RailDim.FG())
			b.WriteString(cells.Pad(line, r.W))
			b.WriteString("\x1b[0m")
		}
	})
}

// paletteWrap wraps s to w cells, greedy on spaces. A word wider than the
// budget lands clipped on its own line.
func paletteWrap(s string, w int) []string {
	if w < 1 {
		return []string{cells.Clip(s, 1)}
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var out []string
	line := ""
	for _, word := range words {
		cand := word
		if line != "" {
			cand = line + " " + word
		}
		if cells.Cells(cand) <= w {
			line = cand
			continue
		}
		if line != "" {
			out = append(out, line)
			line = ""
		}
		if cells.Cells(word) > w {
			out = append(out, cells.Clip(word, w))
			continue
		}
		line = word
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

// paintPaletteQuery paints the query line: the prompt, the query text and the
// ground to the end of the row. It returns the cursor cell, which sits after
// the last query rune.
func paintPaletteQuery(buf *bytes.Buffer, th theme.Theme, query string, r layout.Rect, hint string) (cx, cy int) {
	ground, dim, text := th.Palette.RailBG, th.Palette.RailDim, th.Palette.RailText
	prompt := "> "
	hintW := 0
	if hint != "" {
		hintW = cells.Cells(hint) + 2
	}
	w := r.W - cells.Cells(prompt) - hintW
	if w < 0 {
		w = 0
	}
	q := cells.Clip(query, w)
	used := cells.Cells(prompt) + cells.Cells(q)
	fmt.Fprintf(buf, "\x1b[%d;%dH", r.Y+1, r.X+1)
	buf.WriteString("\x1b[0m")
	buf.WriteString(ground.BG())
	buf.WriteString(dim.FG())
	buf.WriteString(prompt)
	if q != "" {
		buf.WriteString("\x1b[0m")
		buf.WriteString(ground.BG())
		buf.WriteString(text.FG())
		buf.WriteString(q)
	}
	if hint != "" {
		// The hint sits on the row's right edge, in the dim colour: it
		// points at ? without competing with the query. The clip budget
		// above reserves its cells, so the two never meet.
		buf.WriteString(strings.Repeat(" ", max(1, r.W-used-hintW)))
		buf.WriteString("\x1b[0m")
		buf.WriteString(ground.BG())
		buf.WriteString(dim.FG())
		buf.WriteString(hint)
		used += r.W - used
	}
	if n := r.W - used; n > 0 {
		buf.WriteString(strings.Repeat(" ", n))
	}
	buf.WriteString("\x1b[0m")
	// The cell is one past the query's last cell, clamped inside the row: a
	// query as long as the line must not park the cursor on the border.
	maxX := r.X + r.W - 1
	if cx > maxX {
		cx = maxX
	}
	if cx < r.X {
		cx = r.X
	}
	return cx, r.Y
}

// paintPaletteRows paints the body: the filtered rows the viewport holds,
// best match first, the selection on the focus ground. A filter that leaves
// no rows says so on the first body row.
func paintPaletteRows(buf *bytes.Buffer, th theme.Theme, rows []palette.Command, sel int, r layout.Rect) {
	bodyH := r.H - 1
	if bodyH <= 0 {
		return
	}
	// The viewport follows the selection: it never scrolls past it, and it
	// sits still while the selection stays inside it.
	top := sel - bodyH + 1
	if top < 0 {
		top = 0
	}
	for i := 0; i < bodyH; i++ {
		y := r.Y + 1 + i
		fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, r.X+1)
		buf.WriteString("\x1b[0m")
		idx := top + i
		if idx >= len(rows) {
			if len(rows) == 0 && i == 0 {
				buf.WriteString(th.Palette.RailBG.BG())
				buf.WriteString(th.Palette.RailDim.FG())
				buf.WriteString(cells.Pad(" no match", r.W))
				buf.WriteString("\x1b[0m")
			} else {
				buf.WriteString(th.Palette.RailBG.BG())
				buf.WriteString(strings.Repeat(" ", r.W))
				buf.WriteString("\x1b[0m")
			}
			continue
		}
		selected := idx == sel
		ground := th.Palette.RailBG
		if selected {
			ground = th.Palette.RailFocusBG
		}
		buf.WriteString(ground.BG())
		c := rows[idx]
		mark := " "
		if selected {
			mark = ">"
		}
		// No hint column: the palette's width cut descriptions mid-word,
		// and ? explains the selection in a float instead.
		name := cells.Pad(cells.Clip(c.Name, r.W-4), r.W-4)
		line := " " + mark + " " + name
		buf.WriteString(th.Palette.RailText.FG())
		buf.WriteString(line)
		buf.WriteString("\x1b[0m")
	}
}
