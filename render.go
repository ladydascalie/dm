package main

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/hinshun/vt10x"
)

const (
	attrReverse   = 1 << 0
	attrUnderline = 1 << 1
	attrBold      = 1 << 2
)

func isDefault(c vt10x.Color) bool {
	return c == vt10x.DefaultFG || c == vt10x.DefaultBG || c == vt10x.DefaultCursor
}

func sgr(g vt10x.Glyph, cur *vt10x.Glyph) string {
	if cur != nil && g.FG == cur.FG && g.BG == cur.BG && g.Mode == cur.Mode {
		return ""
	}
	var p []string
	p = append(p, "0")
	if g.Mode&attrBold != 0 {
		p = append(p, "1")
	}
	if g.Mode&attrUnderline != 0 {
		p = append(p, "4")
	}
	// vt10x swaps FG and BG when it writes a cell with reverse on, but leaves
	// erased cells unswapped. Emitting SGR 7 on top of swapped colours would
	// double invert. Use 7 only when both colours are the terminal default,
	// which is the case the swap collapses and reverse video would be lost.
	if g.Mode&attrReverse != 0 && isDefault(g.FG) && isDefault(g.BG) {
		p = append(p, "7")
	}
	p = append(p, colorSGR(g.FG, false))
	p = append(p, colorSGR(g.BG, true))
	return "\x1b[" + strings.Join(p, ";") + "m"
}

// colorSGR maps a vt10x colour to an SGR parameter. vt10x stores ANSI colours
// as 0..15, xterm colours as 16..255 and 24-bit colours as r<<16|g<<8|b, using
// values above 0xffffff as "terminal default".
func colorSGR(c vt10x.Color, bg bool) string {
	base := 30
	if bg {
		base = 40
	}
	switch {
	case isDefault(c):
		if bg {
			return "49"
		}
		return "39"
	case c < 8:
		return fmt.Sprint(base + int(c))
	case c < 16:
		return fmt.Sprint(base + 60 + int(c) - 8)
	case c < 256:
		if bg {
			return "48;5;" + fmt.Sprint(int(c))
		}
		return "38;5;" + fmt.Sprint(int(c))
	default:
		r := (int(c) >> 16) & 0xff
		g := (int(c) >> 8) & 0xff
		b := int(c) & 0xff
		if bg {
			return fmt.Sprintf("48;2;%d;%d;%d", r, g, b)
		}
		return fmt.Sprintf("38;2;%d;%d;%d", r, g, b)
	}
}

// normCell maps a stored glyph onto the cell drawPane would paint: a zero or
// control character becomes a space, and a fully zero cell becomes a default
// blank. The VT allocates fresh rows as zero glyphs, so this is the only way to
// read them as the terminal shows them.
func normCell(g vt10x.Glyph) vt10x.Glyph {
	if g.Char == 0 {
		g.Char = ' '
	}
	if g.FG == 0 && g.BG == 0 && g.Mode == 0 && g.Char == ' ' {
		g.FG, g.BG = vt10x.DefaultFG, vt10x.DefaultBG
	}
	return g
}

// drawPane paints one pane's cells inside r.
func drawPane(buf *bytes.Buffer, vt vt10x.View, r Rect) {
	drawPaneFrom(buf, vt, r, 0)
}

// drawPaneFrom paints rows [row0, row0+r.H) of vt into r. row0 > 0 shows
// scrollback kept in a taller terminal.
func drawPaneFrom(buf *bytes.Buffer, vt vt10x.View, r Rect, row0 int) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	cols, rows := vt.Size()
	vt.Lock()
	defer vt.Unlock()
	for y := 0; y < r.H; y++ {
		fmt.Fprintf(buf, "\x1b[%d;%dH", r.Y+y+1, r.X+1)
		var cur *vt10x.Glyph
		for x := 0; x < r.W; x++ {
			var g vt10x.Glyph
			if x < cols && row0+y < rows {
				g = normCell(vt.Cell(x, row0+y))
			} else {
				g = normCell(g)
			}
			buf.WriteString(sgr(g, cur))
			gg := g
			cur = &gg
			if g.Char < 0x20 {
				buf.WriteByte(' ')
			} else {
				buf.WriteRune(g.Char)
			}
		}
	}
}

// screenBytes serialises the VT's visible screen as ANSI that a fresh terminal
// of the same size reproduces exactly: same cells, cursor, visibility, buffer,
// wrap and mouse modes. The pane daemon owns the truth; a client that starts
// from fresh state gets the picture here and takes the ring separately as
// history. A fresh VT starts on the primary screen, so only entering the
// alternate screen needs an escape; the alternate-screen buffer is cleared and
// painted like the primary one.
func screenBytes(vt vt10x.View) []byte {
	var b bytes.Buffer
	cols, rows := vt.Size()
	vt.Lock()
	defer vt.Unlock()
	m := vt.Mode()
	if m&vt10x.ModeAltScreen != 0 {
		b.WriteString("\x1b[?1049h")
	}
	// Autowrap off while painting: writing a full row must not wrap into the
	// next one. The pane's own mode is restored at the end.
	b.WriteString("\x1b[?7l\x1b[0m\x1b[2J")
	for y := 0; y < rows; y++ {
		fmt.Fprintf(&b, "\x1b[%d;1H", y+1)
		var cur *vt10x.Glyph
		for x := 0; x < cols; x++ {
			g := normCell(vt.Cell(x, y))
			b.WriteString(sgr(g, cur))
			gg := g
			cur = &gg
			if g.Char < 0x20 {
				b.WriteByte(' ')
			} else {
				b.WriteRune(g.Char)
			}
		}
	}
	c := vt.Cursor()
	fmt.Fprintf(&b, "\x1b[%d;%dH", c.Y+1, c.X+1)
	if vt.CursorVisible() {
		b.WriteString("\x1b[?25h")
	} else {
		b.WriteString("\x1b[?25l")
	}
	if m&vt10x.ModeWrap != 0 {
		b.WriteString("\x1b[?7h")
	} else {
		b.WriteString("\x1b[?7l")
	}
	mouse := []struct {
		flag vt10x.ModeFlag
		seq  string
	}{
		{vt10x.ModeMouseX10, "\x1b[?9h"},
		{vt10x.ModeMouseButton, "\x1b[?1000h"},
		{vt10x.ModeMouseMotion, "\x1b[?1002h"},
		{vt10x.ModeMouseMany, "\x1b[?1003h"},
		{vt10x.ModeMouseSgr, "\x1b[?1006h"},
	}
	for _, mm := range mouse {
		if m&mm.flag != 0 {
			b.WriteString(mm.seq)
		}
	}
	return b.Bytes()
}

func drawDividers(buf *bytes.Buffer, n *Node, r Rect) {
	if n == nil || n.Pane != "" || len(n.Kids) == 0 {
		return
	}
	k := len(n.Kids)
	horizontal := n.Dir == "row"
	space := r.W
	if !horizontal {
		space = r.H
	}
	space -= k - 1
	if space < k {
		space = k
	}
	sizes := splitSizes(n, space)
	off := 0
	for i, kid := range n.Kids {
		size := sizes[i]
		if horizontal {
			drawDividers(buf, kid, Rect{X: r.X + off, Y: r.Y, W: size, H: r.H})
		} else {
			drawDividers(buf, kid, Rect{X: r.X, Y: r.Y + off, W: r.W, H: size})
		}
		if i < k-1 {
			buf.WriteString("\x1b[0m\x1b[90m")
			if horizontal {
				for y := 0; y < r.H; y++ {
					fmt.Fprintf(buf, "\x1b[%d;%dH│", r.Y+y+1, r.X+off+size+1)
				}
			} else {
				fmt.Fprintf(buf, "\x1b[%d;%dH", r.Y+off+size+1, r.X+1)
				buf.WriteString(strings.Repeat("─", r.W))
			}
		}
		off += size + 1
	}
}

// contentRows returns the first row and the row after the last that hold any
// glyph, using the cursor when the screen is blank below the output.
func contentRows(v vt10x.View) (top, bottom int) {
	cols, rows := v.Size()
	found := false
	for y := 0; y < rows; y++ {
		blank := true
		for x := 0; x < cols; x++ {
			g := v.Cell(x, y)
			if g.Char != 0 && g.Char != ' ' {
				blank = false
				break
			}
		}
		if !blank {
			if !found {
				top = y
				found = true
			}
			bottom = y + 1
		}
	}
	if cur := v.Cursor(); cur.Y+1 > bottom {
		bottom = cur.Y + 1
	}
	if !found && bottom == 0 {
		return 0, 0
	}
	return top, bottom
}

// scrollStart maps a scrollback offset onto the first row to draw and clamps
// the offset to the content that actually exists.
func scrollStart(v vt10x.View, rectH, offset int) (start, max int) {
	top, bottom := contentRows(v)
	if bottom == 0 {
		return 0, 0
	}
	if bottom < rectH {
		bottom = rectH
	}
	max = bottom - rectH - top
	if max < 0 {
		max = 0
	}
	if offset > max {
		offset = max
	}
	start = bottom - rectH - offset
	if start < 0 {
		start = 0
	}
	return start, max
}

// dividerHit reports the divider under x,y: the owning split, the panes on
// its left/above and right/below side, the split's axis and its full rect.
// Coordinates are zero-based terminal cells.
func dividerHit(n *Node, r Rect, x, y int) (split *Node, first, second, axis string, sr Rect, ok bool) {
	if n == nil || n.Pane != "" || len(n.Kids) == 0 {
		return nil, "", "", "", Rect{}, false
	}
	k := len(n.Kids)
	horizontal := n.Dir == "row"
	space := r.W
	if !horizontal {
		space = r.H
	}
	space -= k - 1
	if space < k {
		space = k
	}
	sizes := splitSizes(n, space)
	rects := make([]Rect, k)
	off := 0
	for i := range n.Kids {
		size := sizes[i]
		if horizontal {
			rects[i] = Rect{X: r.X + off, Y: r.Y, W: size, H: r.H}
		} else {
			rects[i] = Rect{X: r.X, Y: r.Y + off, W: r.W, H: size}
		}
		off += size + 1
	}
	for i := 0; i < k; i++ {
		if i < k-1 {
			kr := rects[i]
			if horizontal && x == kr.X+kr.W && y >= r.Y && y < r.Y+r.H {
				first = paneAtRect(n.Kids[i], kr, x-1, y)
				second = paneAtRect(n.Kids[i+1], rects[i+1], x+1, y)
				if first == "" || second == "" {
					return nil, "", "", "", Rect{}, false
				}
				return n, first, second, n.Dir, r, true
			}
			if !horizontal && y == kr.Y+kr.H && x >= r.X && x < r.X+r.W {
				first = paneAtRect(n.Kids[i], kr, x, y-1)
				second = paneAtRect(n.Kids[i+1], rects[i+1], x, y+1)
				if first == "" || second == "" {
					return nil, "", "", "", Rect{}, false
				}
				return n, first, second, n.Dir, r, true
			}
		}
		if sn, f, sec, ax, sr, hit := dividerHit(n.Kids[i], rects[i], x, y); hit {
			return sn, f, sec, ax, sr, hit
		}
	}
	return nil, "", "", "", Rect{}, false
}

// paneAtRect returns the pane leaf under x,y inside a node's rectangle.
func paneAtRect(n *Node, r Rect, x, y int) string {
	if n == nil {
		return ""
	}
	if n.Pane != "" {
		return n.Pane
	}
	k := len(n.Kids)
	if k == 0 {
		return ""
	}
	horizontal := n.Dir == "row"
	space := r.W
	if !horizontal {
		space = r.H
	}
	space -= k - 1
	if space < k {
		space = k
	}
	sizes := splitSizes(n, space)
	off := 0
	for i, kid := range n.Kids {
		size := sizes[i]
		var kr Rect
		if horizontal {
			kr = Rect{X: r.X + off, Y: r.Y, W: size, H: r.H}
		} else {
			kr = Rect{X: r.X, Y: r.Y + off, W: r.W, H: size}
		}
		if x >= kr.X && x < kr.X+kr.W && y >= kr.Y && y < kr.Y+kr.H {
			return paneAtRect(kid, kr, x, y)
		}
		off += size + 1
	}
	return ""
}

// drawBar paints the one-line status bar at the bottom.
func drawBar(buf *bytes.Buffer, snap *Snapshot, armed bool, toast, scroll string) {
	fmt.Fprintf(buf, "\x1b[%d;1H", snap.Rows)
	buf.WriteString("\x1b[0m\x1b[7m")
	line := fmt.Sprintf(" %s | %s | win %d/%d %s | ", snap.Session, snap.Workspace,
		snap.ActiveWindow+1, len(snap.Windows), strings.Join(snap.Windows, " "))
	for _, p := range snap.Panes {
		if p.ID == snap.Focus {
			st := p.Status
			if st == "" {
				st = "unknown"
			}
			line += fmt.Sprintf("[%s %s] ", p.ID, st)
		}
	}
	if armed {
		line += "PREFIX "
	}
	if scroll != "" {
		line += scroll + " "
	}
	if toast != "" {
		line += "! " + toast + " "
	}
	width := snap.Cols
	runes := []rune(line)
	if len(runes) > width {
		runes = runes[:width]
	}
	buf.WriteString(string(runes))
	if pad := width - len(runes); pad > 0 {
		buf.WriteString(strings.Repeat(" ", pad))
	}
	buf.WriteString("\x1b[0m")
}

var keymap = [][2]string{
	{"|", "split right"},
	{"-", "split down"},
	{"< / ^", "split left / up"},
	{"[", "scrollback"},
	{"h j k l", "focus pane"},
	{"n / p", "next/prev pane"},
	{"x", "close pane"},
	{"c", "new window"},
	{"w / W", "next/prev win"},
	{"0-9", "select window"},
	{"t", "new workspace"},
	{")", "next workspace"},
	{"s", "switch session"},
	{"d / q", "detach"},
	{"ctrl+b", "send prefix"},
}

// drawSessions paints the centred session picker.
func drawSessions(buf *bytes.Buffer, cols, rows int, sessions []SessionInfo, sel int, current string) {
	w := 46
	if w > cols-2 {
		w = cols - 2
	}
	h := len(sessions) + 5
	x := (cols - w) / 2
	y := (rows - h) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	pad := func(text string) string {
		r := []rune(text)
		if len(r) > w-2 {
			r = r[:w-2]
		}
		return string(r) + strings.Repeat(" ", w-2-len(r))
	}
	buf.WriteString("\x1b[0m\x1b[90m")
	fmt.Fprintf(buf, "\x1b[%d;%dH┌%s┐", y+1, x+1, pad("─ switch session "))
	for i, s := range sessions {
		mark := " "
		if i == sel {
			mark = ">"
		}
		state := "dead"
		if s.Live {
			state = "live"
		}
		now := ""
		if s.Name == current {
			now = "now"
		}
		colour := "\x1b[90m"
		if i == sel {
			colour = "\x1b[97m"
		}
		line := fmt.Sprintf("%s %-18s %-4s %-3s", mark, s.Name, state, now)
		fmt.Fprintf(buf, "\x1b[%d;%dH│%s%s\x1b[90m│", y+2+i, x+1, colour, pad(line))
	}
	fmt.Fprintf(buf, "\x1b[%d;%dH├%s┤", y+2+len(sessions), x+1, strings.Repeat("─", w-2))
	fmt.Fprintf(buf, "\x1b[%d;%dH│%s│", y+3+len(sessions), x+1,
		pad(" j/k move  enter switch  esc cancel"))
	fmt.Fprintf(buf, "\x1b[%d;%dH└%s┘", y+4+len(sessions), x+1, strings.Repeat("─", w-2))
	buf.WriteString("\x1b[0m")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// drawHelp paints the bottom-right action preview.
func drawHelp(buf *bytes.Buffer, cols, rows int) {
	width := 28
	height := len(keymap) + 2
	x := cols - width - 2
	y := rows - height - 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	buf.WriteString("\x1b[0m\x1b[90m")
	fmt.Fprintf(buf, "\x1b[%d;%dH┌%s┐", y+1, x+1, strings.Repeat("─", width-2))
	fmt.Fprintf(buf, "\x1b[%d;%dH│\x1b[97m %-*s\x1b[90m│", y+2, x+1, width-3, "prefix")
	for i, kv := range keymap {
		fmt.Fprintf(buf, "\x1b[%d;%dH│ %-8s %-*s│", y+3+i, x+1, kv[0], width-11, kv[1])
	}
	fmt.Fprintf(buf, "\x1b[%d;%dH└%s┘", y+height, x+1, strings.Repeat("─", width-2))
	buf.WriteString("\x1b[0m")
}
