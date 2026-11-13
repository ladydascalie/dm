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
	commandPaletteMaxW = 65 // borders, row chrome, the name column and a description
	commandPaletteName = 24
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
			})
		}
	}
	return out
}

// commandPaletteGeom sizes and centres the float: wide enough for a name and
// its description, tall enough for the query line and as many rows as the
// screen holds. A screen too small for the query line and one row returns an
// empty rect.
func commandPaletteGeom(win *wire.Snapshot, rows int) layout.Rect {
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
	if g := commandPaletteGeom(t.snap, len(commands)); g.W < 1 || g.H < 1 {
		return
	}
	t.cp = &commandPalette{pal: palette.New(commands), total: len(commands)}
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
		t.cp.pal.Cycle(-1)
		t.dirty = true
		return true
	}
	if n := paletteArrowDown(t.inbuf); n > 0 {
		t.takePaletteBytes(n)
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
			// A lone esc closes. A longer sequence carries no key the
			// palette knows, so it is swallowed whole.
			closeCommandPalette(t)
		} else {
			t.dirty = true
		}
	case b == 0x7f || b == 0x08:
		t.takePaletteBytes(1)
		t.cp.pal.Backspace()
		t.dirty = true
	case b == 0x0d || b == 0x0a:
		t.takePaletteBytes(1)
		t.invokeCommandPalette()
	case b == 0x10:
		// ctrl+p walks the selection up, the way the arrows do.
		t.takePaletteBytes(1)
		t.cp.pal.Cycle(-1)
		t.dirty = true
	case b == 0x0e:
		// ctrl+n walks the selection down.
		t.takePaletteBytes(1)
		t.cp.pal.Cycle(1)
		t.dirty = true
	case b >= 0x20 && b < 0x7f:
		t.takePaletteBytes(1)
		t.cp.pal.AppendRune(rune(b))
		t.dirty = true
	default:
		// A control byte the palette does not name, or the head byte of a
		// multi-byte rune: the switcher takes neither as text, and neither
		// does the palette.
		t.takePaletteBytes(1)
	}
	return true
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
	g := commandPaletteGeom(win, cp.total)
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
		cx, cy = paintPaletteQuery(b, th, cp.pal.Query(), r)
		paintPaletteRows(b, th, rows, cp.pal.SelectedIndex(), r)
	})
	buf.WriteString("\x1b[0m")
	return cx, cy
}

// paintPaletteQuery paints the query line: the prompt, the query text and the
// ground to the end of the row. It returns the cursor cell, which sits after
// the last query rune.
func paintPaletteQuery(buf *bytes.Buffer, th theme.Theme, query string, r layout.Rect) (cx, cy int) {
	ground, dim, text := th.Palette.RailBG, th.Palette.RailDim, th.Palette.RailText
	prompt := "> "
	w := r.W - cells.Cells(prompt)
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
	if n := r.W - used; n > 0 {
		buf.WriteString(strings.Repeat(" ", n))
	}
	buf.WriteString("\x1b[0m")
	return r.X + used, r.Y
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
		name := cells.Pad(cells.Clip(c.Name, commandPaletteName), commandPaletteName)
		line := " " + mark + " " + name
		used := 3 + commandPaletteName
		hintW := r.W - used - 2
		buf.WriteString(th.Palette.RailText.FG())
		buf.WriteString(line)
		if hintW > 0 {
			// The hint speaks in the dim colour: it describes, it does not
			// carry the row.
			hint := cells.Clip(c.Hint, hintW)
			buf.WriteString(th.Palette.RailDim.FG())
			buf.WriteString("  " + hint)
			used += 2 + cells.Cells(hint)
		}
		if n := r.W - used; n > 0 {
			buf.WriteString(strings.Repeat(" ", n))
		}
		buf.WriteString("\x1b[0m")
	}
}
