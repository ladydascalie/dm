package main

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
	"unicode"

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

// selRange is a row-major cell range inside one pane's content: lo and hi are
// cell offsets from the pane's top-left, lo <= hi.
type selRange struct{ lo, hi int }

func (s *selRange) has(off int) bool {
	return s != nil && off >= s.lo && off <= s.hi
}

// drawPane paints one pane's cells inside r.
func drawPane(buf *bytes.Buffer, vt vt10x.View, r Rect) {
	drawPaneFrom(buf, vt, r, 0, nil)
}

// drawPaneFrom paints rows [row0, row0+r.H) of vt into r. row0 > 0 shows
// scrollback kept in a taller terminal. Cells inside sel get reverse video on
// top of the pane's own colours.
func drawPaneFrom(buf *bytes.Buffer, vt vt10x.View, r Rect, row0 int, sel *selRange) {
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
			selected := sel.has(y*r.W + x)
			buf.WriteString(sgr(g, cur))
			if selected {
				buf.WriteString("\x1b[7m")
			}
			gg := g
			cur = &gg
			if g.Char < 0x20 {
				buf.WriteByte(' ')
			} else {
				buf.WriteRune(g.Char)
			}
			if selected {
				buf.WriteString("\x1b[27m")
			}
		}
	}
}

// selOrder returns the two cells in row-major order.
func selOrder(a, b cell) (cell, cell) {
	if a.y > b.y || (a.y == b.y && a.x > b.x) {
		return b, a
	}
	return a, b
}

// extractSelection reads the selected cells from vt row by row. Trailing spaces
// go, lines join with a newline, and no newline follows the last line.
func extractSelection(vt vt10x.View, w int, a, b cell) string {
	if vt == nil || w < 1 {
		return ""
	}
	start, end := selOrder(a, b)
	cols, rows := vt.Size()
	vt.Lock()
	defer vt.Unlock()
	lines := make([]string, 0, end.y-start.y+1)
	for y := start.y; y <= end.y; y++ {
		x0, x1 := 0, w-1
		if y == start.y {
			x0 = start.x
		}
		if y == end.y {
			x1 = end.x
		}
		var line strings.Builder
		for x := x0; x <= x1; x++ {
			var g vt10x.Glyph
			if x >= 0 && x < cols && y >= 0 && y < rows {
				g = normCell(vt.Cell(x, y))
			} else {
				g = normCell(g)
			}
			if g.Char < 0x20 {
				line.WriteByte(' ')
			} else {
				line.WriteRune(g.Char)
			}
		}
		lines = append(lines, strings.TrimRight(line.String(), " "))
	}
	return strings.Join(lines, "\n")
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

// drawPaneBox draws the border of one pane. The active pane is bright cyan;
// the rest are dim, so the active one reads at a glance.
func drawPaneBox(buf *bytes.Buffer, content Rect, active bool) {
	x0, y0 := content.X-1, content.Y-1
	x1, y1 := content.X+content.W, content.Y+content.H
	if content.W <= 0 || content.H <= 0 || x0 < 0 || y0 < 0 {
		return
	}
	if active {
		buf.WriteString("\x1b[0m\x1b[1;96m")
	} else {
		buf.WriteString("\x1b[0m\x1b[90m")
	}
	put := func(x, y int, s string) {
		fmt.Fprintf(buf, "\x1b[%d;%dH%s", y+1, x+1, s)
	}
	put(x0, y0, "┌")
	put(x1, y0, "┐")
	put(x0, y1, "└")
	put(x1, y1, "┘")
	for x := x0 + 1; x < x1; x++ {
		put(x, y0, "─")
		put(x, y1, "─")
	}
	for y := y0 + 1; y < y1; y++ {
		put(x0, y, "│")
		put(x1, y, "│")
	}
}

// drawGrips marks every divider at its midpoint, so a handle is visible before
// the pointer finds it. The hover paints over the whole divider.
func drawGrips(buf *bytes.Buffer, snap *Snapshot) {
	if snap == nil || snap.Root == nil || snap.Rail < 0 {
		return
	}
	buf.WriteString("\x1b[0m\x1b[90m")
	var walk func(n *Node, a Rect)
	walk = func(n *Node, a Rect) {
		if n == nil || n.Pane != "" {
			return
		}
		boxes := childBoxRects(n, a)
		for i := 0; i+1 < len(boxes); i++ {
			p := padRect(n, boxes, i)
			if n.Dir == "row" {
				y := p.Y + p.H/2
				fmt.Fprintf(buf, "\x1b[%d;%dH•", y+1, p.X+1)
				if p.W > 1 {
					fmt.Fprintf(buf, "\x1b[%d;%dH•", y+1, p.X+2)
				}
			} else {
				x := p.X + p.W/2
				fmt.Fprintf(buf, "\x1b[%d;%dH•", p.Y+1, x+1)
				if p.H > 1 {
					fmt.Fprintf(buf, "\x1b[%d;%dH•", p.Y+2, x+1)
				}
			}
		}
		for i, b := range boxes {
			walk(n.Kids[i], b)
		}
	}
	walk(snap.Root, paneAreaFor(snap.Cols, snap.Rows, snap.Rail))
	buf.WriteString("\x1b[0m")
}

// drawDivider paints the hovered divider: heavy line glyphs over both border
// cells, so the grab target is visible before the press.
func drawDivider(buf *bytes.Buffer, z Rect, axis string) {
	if z.W <= 0 || z.H <= 0 {
		return
	}
	glyph := "┃"
	if axis == "col" {
		glyph = "━"
	}
	buf.WriteString("\x1b[0m\x1b[1;96m")
	line := strings.Repeat(glyph, z.W)
	for y := z.Y; y < z.Y+z.H; y++ {
		fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, z.X+1)
		buf.WriteString(line)
	}
	buf.WriteString("\x1b[0m")
}

// wideRanges are the East Asian Wide and Fullwidth code point ranges. They are
// sorted and non-overlapping, so runeWidth can binary-search them.
var wideRanges = [][2]rune{
	{0x1100, 0x115F},
	{0x2E80, 0x303E},
	{0x3041, 0x33FF},
	{0x3400, 0x4DBF},
	{0x4E00, 0x9FFF},
	{0xA000, 0xA4CF},
	{0xA960, 0xA97F},
	{0xAC00, 0xD7A3},
	{0xF900, 0xFAFF},
	{0xFE10, 0xFE19},
	{0xFE30, 0xFE6F},
	{0xFF00, 0xFF60},
	{0xFFE0, 0xFFE6},
	{0x1B000, 0x1B2FF},
	{0x1F004, 0x1F004},
	{0x1F0CF, 0x1F0CF},
	{0x1F18E, 0x1F18E},
	{0x1F191, 0x1F19A},
	{0x1F200, 0x1F320},
	{0x1F32D, 0x1F335},
	{0x1F337, 0x1F37C},
	{0x1F37E, 0x1F393},
	{0x1F3A0, 0x1F3CA},
	{0x1F3CF, 0x1F3D3},
	{0x1F3E0, 0x1F3F0},
	{0x1F3F4, 0x1F3F4},
	{0x1F3F8, 0x1F43E},
	{0x1F440, 0x1F440},
	{0x1F442, 0x1F4FC},
	{0x1F4FF, 0x1F53D},
	{0x1F54B, 0x1F54E},
	{0x1F550, 0x1F567},
	{0x1F57A, 0x1F57A},
	{0x1F595, 0x1F596},
	{0x1F5A4, 0x1F5A4},
	{0x1F5FB, 0x1F64F},
	{0x1F680, 0x1F6C5},
	{0x1F6CC, 0x1F6CC},
	{0x1F6D0, 0x1F6D2},
	{0x1F6EB, 0x1F6EC},
	{0x1F6F4, 0x1F6FA},
	{0x1F7E0, 0x1F7EB},
	{0x1F90D, 0x1F971},
	{0x1F973, 0x1F976},
	{0x1F97A, 0x1F9A2},
	{0x1F9A5, 0x1F9AA},
	{0x1F9AE, 0x1F9CA},
	{0x1F9CD, 0x1F9FF},
	{0x1FA70, 0x1FA73},
	{0x1FA78, 0x1FA7A},
	{0x1FA80, 0x1FA82},
	{0x1FA90, 0x1FA95},
	{0x20000, 0x2FFFD},
	{0x30000, 0x3FFFD},
}

// runeWidth is the number of terminal cells a rune paints: two for an East
// Asian wide or fullwidth rune, zero for a combining mark, one otherwise.
func runeWidth(r rune) int {
	if r == 0 || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) {
		return 0
	}
	if r < wideRanges[0][0] {
		return 1
	}
	lo, hi := 0, len(wideRanges)
	for lo < hi {
		mid := (lo + hi) / 2
		switch {
		case r < wideRanges[mid][0]:
			hi = mid
		case r > wideRanges[mid][1]:
			lo = mid + 1
		default:
			return 2
		}
	}
	return 1
}

// railKind is the row type the sidebar paints: a workspace header, a window
// header, or a pane.
type railKind int

const (
	railWorkspace railKind = iota
	railWindow
	railPane
)

// railItem is one sidebar row. It exists so drawing and hit-testing share the
// same grouping.
type railItem struct {
	kind     railKind
	text     string
	branch   string
	worktree string
	pane     string
	window   string
	status   string
	agent    bool
	folded   bool
	focus    bool
	active   bool
}

// railItems flattens a snapshot into sidebar rows in the server's order: a
// workspace header, a window header per window, then its panes when the window
// is unfolded. The thin rail always lists panes, because that is all it can
// show. A pane row labels itself with its recorded name, then the app= it last
// reported, then the pane id.
func railItems(snap *Snapshot) []railItem {
	if snap == nil {
		return nil
	}
	// The workspace branch comes from its focused pane, else the first pane
	// that reported one, so a workspace header names the repo it is working in.
	branch := map[string]string{}
	for _, p := range snap.All {
		if p.Branch == "" {
			continue
		}
		if _, ok := branch[p.Workspace]; !ok || p.Focus {
			branch[p.Workspace] = p.Branch
		}
	}
	compact := railCompact(snap.Rail)
	var out []railItem
	ws, win := "", ""
	for _, p := range snap.All {
		if p.Workspace != ws {
			ws = p.Workspace
			win = ""
			out = append(out, railItem{
				kind: railWorkspace,
				text: p.WorkspaceName, branch: branch[p.Workspace],
				active: p.WorkspaceActive,
			})
		}
		if p.Window != win {
			win = p.Window
			out = append(out, railItem{
				kind: railWindow, text: p.WindowName,
				window: p.Window, folded: !p.WindowUnfolded,
			})
		}
		if !compact && !p.WindowUnfolded {
			continue
		}
		label := p.Name
		if label == "" {
			label = p.App
		}
		if label == "" {
			label = p.ID
		}
		// A linked worktree is shown only when an agent lives there, so a
		// shell in another checkout does not clutter the rail.
		worktree := ""
		if p.Agent {
			worktree = p.Worktree
		}
		out = append(out, railItem{
			kind: railPane, text: label, pane: p.ID,
			status: p.Status, agent: p.Agent, worktree: worktree,
			focus: p.Focus,
		})
	}
	return out
}

// rgb is an explicit colour. The rail never uses palette slots: a theme may
// map a slot to a colour that fails contrast on its own background, and the
// rail must pass WCAG AA on every theme.
type rgb struct{ r, g, b int }

func (c rgb) fg() string { return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", c.r, c.g, c.b) }
func (c rgb) bg() string { return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", c.r, c.g, c.b) }

// lum is the WCAG relative luminance.
func (c rgb) lum() float64 {
	lin := func(v int) float64 {
		f := float64(v) / 255
		if f <= 0.04045 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.r) + 0.7152*lin(c.g) + 0.0722*lin(c.b)
}

// contrast is the WCAG contrast ratio, 1:1 to 21:1.
func contrast(a, b rgb) float64 {
	la, lb := a.lum(), b.lum()
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// The rail palette. Every foreground is at least 4.5:1 against both the rail
// background and the focused row background; TestRailContrast guards it.
var (
	railBG       = rgb{31, 30, 39}    // #1f1e27
	railFocusBG  = rgb{39, 38, 51}    // #272633
	railText     = rgb{233, 234, 241} // #e9eaf1
	railDim      = rgb{143, 147, 168} // #8f93a8
	railHeader   = rgb{166, 169, 189} // #a6a9bd
	railWindowFg = rgb{149, 153, 174} // #9599ae
)

// Nerd Font icons. The font check runs once; DM_NERD=1 or 0 overrides it.
var (
	nerdOnce sync.Once
	nerdOn   bool
)

func nerdFonts() bool {
	switch os.Getenv("DM_NERD") {
	case "1":
		return true
	case "0":
		return false
	}
	nerdOnce.Do(func() {
		out, err := exec.Command("fc-list", ":family").Output()
		nerdOn = err == nil && strings.Contains(strings.ToLower(string(out)), "nerd")
	})
	return nerdOn
}

// railGlyph is the state mark for a pane row and its colour. Every state gets a
// distinct icon or, without Nerd Fonts, a distinct Unicode glyph and colour.
// The question mark marks an agent whose state has not arrived; drawRail leaves
// the cell blank for a shell, which reports no state.
func railGlyph(status string, nerd bool) (glyph string, colour rgb) {
	switch status {
	case "working":
		if nerd {
			return "\uf013", rgb{224, 169, 76}
		}
		return "●", rgb{224, 169, 76}
	case "idle":
		if nerd {
			return "\uf111", rgb{159, 176, 207}
		}
		return "○", rgb{159, 176, 207}
	case "blocked":
		if nerd {
			return "\uf071", rgb{224, 112, 90}
		}
		return "▲", rgb{224, 112, 90}
	case "done":
		if nerd {
			return "\uf00c", rgb{95, 168, 135}
		}
		return "✓", rgb{95, 168, 135}
	case "error":
		if nerd {
			return "\uf057", rgb{228, 167, 158}
		}
		return "✕", rgb{228, 167, 158}
	default:
		if nerd {
			return "\uf128", railDim
		}
		return "?", railDim
	}
}

// railMark is the two-cell prefix of a pane row: an agent icon, a shell icon,
// or the fallback agent bar. The status glyph lands in column 3 either way.
// Every icon here is from the Font Awesome 4 range that even old Nerd Font
// patches carry; newer code points such as the FA5 robot render as tofu.
func railMark(agent, nerd bool) string {
	switch {
	case agent && nerd:
		return "\uf135 "
	case agent:
		return "\u258e "
	case nerd:
		return "\uf120 "
	default:
		return "  "
	}
}

// drawRail paints the sidebar above the status bar. The rail paints its own
// background so contrast does not depend on the terminal theme. Rows past the
// screen are clipped, and every row is written into at most Rail columns.
// railPaneAt returns the pane whose row is under x,y, or "" for a header, an
// empty row, or a point outside the rail. One-based screen coordinates are the
// caller's; x and y are zero-based here.
func railPaneAt(snap *Snapshot, x, y int) string {
	if snap == nil || snap.Rail <= 0 {
		return ""
	}
	if x < 0 || x >= snap.Rail || y < 0 || y >= snap.Rows-1 {
		return ""
	}
	items := railItems(snap)
	if y >= len(items) || items[y].kind != railPane {
		return ""
	}
	return items[y].pane
}

// railWindowAt returns the window whose row is under x,y, for a fold click.
func railWindowAt(snap *Snapshot, x, y int) string {
	if snap == nil || snap.Rail <= 0 {
		return ""
	}
	if x < 0 || x >= snap.Rail || y < 0 || y >= snap.Rows-1 {
		return ""
	}
	items := railItems(snap)
	if y >= len(items) || items[y].kind != railWindow {
		return ""
	}
	return items[y].window
}

func drawRail(buf *bytes.Buffer, snap *Snapshot) {
	if snap == nil || snap.Rail <= 0 || snap.Cols < 1 || snap.Rows < 2 {
		return
	}
	w := snap.Rail
	rows := snap.Rows - 1
	items := railItems(snap)
	nerd := nerdFonts()
	compact := railCompact(snap.Rail)
	for y := 0; y < rows; y++ {
		bg := railBG
		style := "\x1b[0m" + bg.bg() + railText.fg()
		glyphColour := railDim
		text := ""
		kind := railWorkspace
		agent := false
		wtAt := -1
		if y < len(items) {
			it := items[y]
			kind, agent = it.kind, it.agent
			switch it.kind {
			case railWorkspace:
				glyphColour = railHeader
				if it.active {
					style = "\x1b[0m" + bg.bg() + "\x1b[1m" + railText.fg()
					glyphColour = railText
				} else {
					style = "\x1b[0m" + bg.bg() + railHeader.fg()
				}
				if !compact {
					if nerd {
						text = "\uf07b " + it.text
					} else {
						text = it.text
					}
					if it.branch != "" {
						if nerd {
							text += "  \uf126 " + it.branch
						} else {
							text += " (" + it.branch + ")"
						}
					}
				}
			case railWindow:
				style = "\x1b[0m" + bg.bg() + railWindowFg.fg()
				glyphColour = railWindowFg
				if !compact {
					caret := "▾"
					if it.folded {
						caret = "▸"
					}
					if nerd {
						caret = "\uf0d7"
						if it.folded {
							caret = "\uf0da"
						}
					}
					text = "  " + caret + " " + it.text
				}
			case railPane:
				if it.focus {
					bg = railFocusBG
					style = "\x1b[0m" + bg.bg() + "\x1b[1m" + railText.fg()
				} else if it.agent {
					style = "\x1b[0m" + bg.bg() + railText.fg()
				} else {
					style = "\x1b[0m" + bg.bg() + railDim.fg()
				}
				g, colour := railGlyph(it.status, nerd)
				// A shell has no state to report, so the fallback mark would be a
				// question about nothing. Leave the cell blank instead.
				if !it.agent && (it.status == "" || it.status == "unknown") {
					g = " "
				}
				glyphColour = colour
				mark := railMark(it.agent, nerd)
				if compact {
					text = mark + g
				} else {
					text = "    " + mark + g + " " + it.text
					if it.worktree != "" {
						wtAt = len([]rune(text)) + 2
						text += "  " + it.worktree
					}
				}
			}
		}
		line := []rune(text)
		cut, cells := 0, 0
		for cut < len(line) {
			rw := runeWidth(line[cut])
			if cells+rw > w {
				break
			}
			cells += rw
			cut++
		}
		line = line[:cut]
		fmt.Fprintf(buf, "\x1b[%d;1H", y+1)
		buf.WriteString(style)
		for i, r := range line {
			if kind == railPane && wtAt >= 0 && i == wtAt {
				buf.WriteString("\x1b[0m" + bg.bg() + railDim.fg())
				buf.WriteString(string(line[i:]))
				break
			}
			if railIcon(kind, agent, nerd, compact, i) {
				buf.WriteString("\x1b[0m" + bg.bg() + glyphColour.fg())
				buf.WriteRune(r)
				buf.WriteString("\x1b[0m" + style)
				continue
			}
			buf.WriteRune(r)
		}
		if pad := w - cells; pad > 0 {
			buf.WriteString(strings.Repeat(" ", pad))
		}
		buf.WriteString("\x1b[0m")
	}
}

// railIcon reports whether the rune at index i in a row is an icon that takes
// its own colour: the workspace folder, the window mark, the pane marker and
// the state glyph.
func railIcon(kind railKind, agent, nerd, compact bool, i int) bool {
	switch kind {
	case railWorkspace:
		return nerd && i == 0
	case railWindow:
		return i == 2
	case railPane:
		if compact {
			return i == 2 || i == 0
		}
		return i == 4 || i == 6
	}
	return false
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

// padHit reports the divider under x,y: the owning split, the panes on each
// side, the axis, the split's rect and the two-cell divider rect.
func padHit(n *Node, area Rect, x, y int) (split *Node, first, second, axis string, sr, pr Rect, ok bool) {
	if n == nil || n.Pane != "" || len(n.Kids) == 0 {
		return nil, "", "", "", Rect{}, Rect{}, false
	}
	boxes := childBoxRects(n, area)
	horizontal := n.Dir == "row"
	for i := range n.Kids {
		if i < len(boxes)-1 {
			p := padRect(n, boxes, i)
			if x >= p.X && x < p.X+p.W && y >= p.Y && y < p.Y+p.H {
				// Probe one cell inside each box, next to the shared border.
				if horizontal {
					first = leafNear(n.Kids[i], boxes[i], boxes[i].X+boxes[i].W-2, y)
					second = leafNear(n.Kids[i+1], boxes[i+1], boxes[i+1].X+1, y)
				} else {
					first = leafNear(n.Kids[i], boxes[i], x, boxes[i].Y+boxes[i].H-2)
					second = leafNear(n.Kids[i+1], boxes[i+1], x, boxes[i+1].Y+1)
				}
				if first == "" || second == "" {
					return nil, "", "", "", Rect{}, Rect{}, false
				}
				return n, first, second, n.Dir, area, p, true
			}
		}
		if sn, f, sec, ax, sr, pr, hit := padHit(n.Kids[i], boxes[i], x, y); hit {
			return sn, f, sec, ax, sr, pr, hit
		}
	}
	return nil, "", "", "", Rect{}, Rect{}, false
}

// leafAt returns the leaf pane whose content rect contains x,y.
func leafAt(n *Node, area Rect, x, y int) string {
	if n == nil {
		return ""
	}
	if n.Pane != "" {
		r := insetBox(area)
		if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
			return n.Pane
		}
		return ""
	}
	for i, b := range childBoxRects(n, area) {
		if x >= b.X && x < b.X+b.W && y >= b.Y && y < b.Y+b.H {
			if id := leafAt(n.Kids[i], b, x, y); id != "" {
				return id
			}
		}
	}
	return ""
}

// leafNear returns the leaf at x,y, or the nearest leaf when the point lands
// on a border inside the subtree. A divider spans the whole split, so its
// pointer can sit exactly over a border of the child's own split; probing the
// exact cell would then find no pane and the divider would be dead.
func leafNear(n *Node, area Rect, x, y int) string {
	offsets := [][2]int{
		{0, 0},
		{-1, 0},
		{1, 0},
		{0, -1},
		{0, 1},
		{-2, 0},
		{2, 0},
		{0, -2},
		{0, 2},
	}
	for _, d := range offsets {
		if id := leafAt(n, area, x+d[0], y+d[1]); id != "" {
			return id
		}
	}
	return ""
}

// drawMenu paints the right-click menu over the panes.
func drawMenu(buf *bytes.Buffer, m *menu) {
	if m == nil || m.w < 8 {
		return
	}
	inner := m.w - 2
	title := menuPad("─ "+m.title+" ", inner, '─')
	fmt.Fprintf(buf, "\x1b[0m\x1b[90m\x1b[%d;%dH┌%s┐", m.y+1, m.x+1, title)
	for i, it := range m.items {
		line := menuPad(fmt.Sprintf(" %-22s %-4s", it.label, it.key), inner, ' ')
		colour := "\x1b[0m\x1b[90m"
		if i == m.sel {
			colour = "\x1b[0m\x1b[30;46m"
		}
		fmt.Fprintf(buf, "\x1b[%d;%dH│%s%s\x1b[0m\x1b[90m│", m.y+2+i, m.x+1, colour, line)
	}
	fmt.Fprintf(buf, "\x1b[%d;%dH└%s┘", m.y+len(m.items)+2, m.x+1, strings.Repeat("─", inner))
	buf.WriteString("\x1b[0m")
}

// menuPad truncates or pads s to exactly w runes.
func menuPad(s string, w int, fill rune) string {
	r := []rune(s)
	if len(r) > w {
		r = r[:w]
	}
	out := string(r)
	if n := w - len(r); n > 0 {
		out += strings.Repeat(string(fill), n)
	}
	return out
}

// drawBar paints the one-line status bar at the bottom.
func drawBar(buf *bytes.Buffer, snap *Snapshot, armed bool, scroll string) {
	fmt.Fprintf(buf, "\x1b[%d;1H", snap.Rows)
	buf.WriteString("\x1b[0m\x1b[7m")
	line := fmt.Sprintf(" %s | %s | win %d/%d %s | ", snap.Session, snap.Workspace,
		snap.ActiveWindow+1, len(snap.Windows), strings.Join(snap.Windows, " "))
	hlStart, hlEnd := 0, 0
	for _, p := range snap.Panes {
		if p.ID != snap.Focus {
			continue
		}
		st := p.Status
		if st == "" {
			st = "unknown"
		}
		seg := "▶ " + p.ID
		if p.Name != "" {
			seg += " " + p.Name
		}
		seg += " " + st + " "
		hlStart = len([]rune(line))
		line += seg
		hlEnd = len([]rune(line))
	}
	if armed {
		line += "PREFIX "
	}
	if scroll != "" {
		line += scroll + " "
	}
	width := snap.Cols
	runes := []rune(line)
	cut, cells := 0, 0
	for cut < len(runes) {
		rw := runeWidth(runes[cut])
		if cells+rw > width {
			break
		}
		cells += rw
		cut++
	}
	runes = runes[:cut]
	if hlEnd > len(runes) {
		hlEnd = len(runes)
	}
	if hlStart > hlEnd {
		hlStart = hlEnd
	}
	buf.WriteString(string(runes[:hlStart]))
	if hlEnd > hlStart {
		buf.WriteString("\x1b[0m\x1b[30;46m")
		buf.WriteString(string(runes[hlStart:hlEnd]))
		buf.WriteString("\x1b[0m\x1b[7m")
	}
	buf.WriteString(string(runes[hlEnd:]))
	if pad := width - cells; pad > 0 {
		buf.WriteString(strings.Repeat(" ", pad))
	}
	buf.WriteString("\x1b[0m")
}

// drawToasts paints the toast stack in the bottom-right corner, just above the
// status bar: newest at the bottom, each toast one row inside a dim border. The
// box is as wide as the longest text plus its two border cells, clamped to the
// screen; a longer text is truncated to fit. Only rows above the bar are used,
// so the bar and the pointer gestures over the panes are untouched.
func drawToasts(buf *bytes.Buffer, snap *Snapshot, toasts []toast) {
	if snap == nil || len(toasts) == 0 || snap.Cols < 3 || snap.Rows < 2 {
		return
	}
	longest := 0
	for _, x := range toasts {
		if n := len([]rune(x.text)); n > longest {
			longest = n
		}
	}
	w := longest + 2
	if w > snap.Cols {
		w = snap.Cols
	}
	inner := w - 2
	for i, x := range toasts {
		row := snap.Rows - 1 - (len(toasts) - 1 - i)
		if row < 1 {
			continue
		}
		fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m\x1b[90m│\x1b[97m%s\x1b[0m\x1b[90m│\x1b[0m",
			row, snap.Cols-w+1, menuPad(x.text, inner, ' '))
	}
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
	{"r", "cycle rail: full, thin, hidden"},
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
