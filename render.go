package main

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	drawPaneFrom(buf, vt, r, 0, nil, nil)
}

// drawPaneFrom paints rows [row0, row0+r.H) of vt into r. row0 > 0 shows
// scrollback kept in a taller terminal. Cells inside sel get reverse video on
// top of the pane's own colours; cells inside hover get an underline.
func drawPaneFrom(buf *bytes.Buffer, vt vt10x.View, r Rect, row0 int, sel, hover *selRange) {
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
			hovered := hover.has(y*r.W + x)
			selected := sel.has(y*r.W + x)
			if hovered {
				g.Mode |= attrUnderline
			}
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

// drawPaneRow paints one row of a pane's content: the same bytes a full pane
// paint writes for that row.
func drawPaneRow(buf *bytes.Buffer, vt vt10x.View, r Rect, row, row0 int, sel, hover *selRange) {
	drawPaneFrom(buf, vt, Rect{X: r.X, Y: r.Y + row, W: r.W, H: 1}, row0+row, selRow(sel, row, r.W), selRow(hover, row, r.W))
}

// selRow moves a pane-wide cell range onto one row, nil when the row holds
// none of it.
func selRow(s *selRange, row, w int) *selRange {
	if s == nil {
		return nil
	}
	lo, hi := s.lo-row*w, s.hi-row*w
	if lo < 0 {
		lo = 0
	}
	if hi > w-1 {
		hi = w - 1
	}
	if lo > hi {
		return nil
	}
	return &selRange{lo: lo, hi: hi}
}

// blankRect paints default blanks over a rectangle, so a band that left the
// screen does not keep its glyphs.
func blankRect(buf *bytes.Buffer, r Rect) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	line := "\x1b[0m" + strings.Repeat(" ", r.W)
	for y := r.Y; y < r.Y+r.H; y++ {
		fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, r.X+1)
		buf.WriteString(line)
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

// railKind is the row type the sidebar paints: a workspace row, a window line,
// that window's dim second line, a pane, or the gap between workspace blocks.
type railKind int

const (
	railWorkspace railKind = iota
	railWindow
	railWindowNote
	railPane
	railGap
)

// railItem is one sidebar row. It exists so drawing and hit-testing share the
// same grouping.
type railItem struct {
	kind      railKind
	text      string
	workspace string
	folder    string
	branch    string
	worktree  string
	message   string
	pane      string
	window    string
	status    string
	count     int
	added     int
	deleted   int
	accent    rgb
	agent     bool
	dirty     bool
	inRepo    bool
	folded    bool
	focus     bool
	active    bool
}

// railItems flattens a snapshot into sidebar rows in the server's order: a
// workspace row, a window line per window, that window's note line when it has
// a message and is unfolded, then its pane rows when it is unfolded. A gap row
// separates workspace blocks. The thin rail keeps its glyph column: one blank
// row per header, no note lines, no gaps, and panes whatever the fold.
func railItems(snap *Snapshot) []railItem {
	if snap == nil {
		return nil
	}
	compact := railCompact(snap.Rail)
	panes := map[string]int{}
	focus := map[string]int{}
	first := map[string]int{}
	withBranch := map[string]int{}
	for i, p := range snap.All {
		panes[p.Workspace]++
		if _, ok := first[p.Window]; !ok {
			first[p.Window] = i
		}
		if p.Focus {
			focus[p.Window] = i
		}
		if p.Branch != "" {
			if _, ok := withBranch[p.Window]; !ok {
				withBranch[p.Window] = i
			}
		}
	}
	// The window line takes its folder and git state from one pane: the
	// focused pane, else the first pane in that window that carries a branch,
	// else the window's first pane.
	source := func(window string) (AllPane, bool) {
		if i, ok := focus[window]; ok && snap.All[i].Branch != "" {
			return snap.All[i], true
		}
		if i, ok := withBranch[window]; ok {
			return snap.All[i], true
		}
		if i, ok := focus[window]; ok {
			return snap.All[i], true
		}
		if i, ok := first[window]; ok {
			return snap.All[i], true
		}
		return AllPane{}, false
	}
	// A workspace keeps its preferred hue when it is free. When two live
	// workspaces hash alike, the later one takes the next free slot, so no two
	// workspaces in a session ever share an accent.
	used := map[rgb]bool{}
	accent := func(id string) rgb {
		pref := railAccent(id)
		start := 0
		for i, a := range railAccents {
			if a == pref {
				start = i
				break
			}
		}
		for i := 0; i < len(railAccents); i++ {
			a := railAccents[(start+i)%len(railAccents)]
			if !used[a] {
				used[a] = true
				return a
			}
		}
		return pref
	}
	var out []railItem
	ws, win := "", ""
	for _, p := range snap.All {
		if p.Workspace != ws {
			if ws != "" && !compact {
				out = append(out, railItem{kind: railGap})
			}
			ws, win = p.Workspace, ""
			name := p.WorkspaceName
			if name == "" {
				name = p.Workspace
			}
			out = append(out, railItem{
				kind: railWorkspace, text: name, workspace: p.Workspace,
				count: panes[p.Workspace], accent: accent(p.Workspace),
				active: p.WorkspaceActive,
			})
		}
		if p.Window != win {
			win = p.Window
			item := railItem{
				kind: railWindow, text: p.WindowName, window: p.Window,
				folded: !p.WindowUnfolded,
			}
			if !compact {
				if src, ok := source(p.Window); ok {
					item.folder = railFolder(src.Cwd, src.Root)
					item.branch = src.Branch
					item.worktree = src.Worktree
					item.inRepo = src.InRepo
					item.dirty = src.Dirty
					item.added = src.Added
					item.deleted = src.Deleted
					item.message = src.Message
				}
			}
			out = append(out, item)
			if !compact && item.message != "" && p.WindowUnfolded {
				out = append(out, railItem{
					kind: railWindowNote, text: item.message, window: p.Window,
				})
			}
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
		out = append(out, railItem{
			kind: railPane, text: label, pane: p.ID,
			status: p.Status, agent: p.Agent, focus: p.Focus,
		})
	}
	return out
}

// railFolder is the folder a window line shows for a pane: its path below the
// repository root, the folder's own name when the pane sits at the root, and
// the folder name alone outside git, so every pane still names a folder.
func railFolder(cwd, root string) string {
	if cwd == "" {
		return ""
	}
	cwd = filepath.Clean(cwd)
	if root == "" {
		return folderName(cwd)
	}
	root = filepath.Clean(root)
	if cwd == root {
		return folderName(root)
	}
	rel, err := filepath.Rel(root, cwd)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return folderName(cwd)
	}
	return filepath.ToSlash(rel)
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
	railBG       = rgb{20, 19, 26}    // #14131a
	railFocusBG  = rgb{31, 30, 39}    // #1f1e27
	railText     = rgb{233, 234, 241} // #e9eaf1
	railDim      = rgb{143, 147, 168} // #8f93a8
	railHeader   = rgb{166, 169, 189} // #a6a9bd
	railWindowFg = rgb{149, 153, 174} // #9599ae
	railDiffAdd  = rgb{120, 220, 130} // #78dc82 diff stats, added lines
	railDiffDel  = rgb{255, 125, 125} // #ff7d7d diff stats, deleted lines
	railWorking  = rgb{245, 190, 80}  // #f5be50 working amber, dirty dot

	// The status bar reuses the rail's ground, focus fill, text and dim. It adds
	// one accent for the active window mark and names the ground it paints
	// between segments. There is no second background.
	barAccent = rgb{122, 192, 235} // #7ac0eb cyan, active window mark
	barGutter = railBG             // ground between segments
)

// railAccents are the six workspace hues, in a fixed order. Each one is
// readable on both rail backgrounds, so a workspace header keeps its identity
// without losing contrast.
var railAccents = []rgb{
	{187, 154, 247}, // #bb9af7 violet
	{110, 220, 235}, // #6edceb cyan
	{240, 140, 215}, // #f08cd7 magenta
	{126, 169, 255}, // #7ea9ff blue
	{240, 160, 90},  // #f0a05a orange
	{140, 215, 155}, // #8cd79b green
}

// railAccent returns the hue a workspace keeps for its whole life. FNV-1a is
// the seed; the mix step matters because its low bits repeat for ids of equal
// length, and a workspace id is short.
func railAccent(workspace string) rgb {
	h := fnv.New32a()
	_, _ = h.Write([]byte(workspace))
	x := h.Sum32()
	x ^= x >> 16
	x *= 0x7feb352d
	x ^= x >> 15
	x *= 0x846ca68b
	x ^= x >> 16
	return railAccents[x%uint32(len(railAccents))]
}

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

// railGlyph is the state mark for a pane row and its colour: amber working,
// teal-blue idle, red blocked, green done, pink-red error, dim grey unknown.
// Every state gets a distinct icon or, without Nerd Fonts, a distinct Unicode
// glyph. The question mark marks an agent whose state has not arrived; drawRail
// leaves the cell blank for a shell, which reports no state.
func railGlyph(status string, nerd bool) (glyph string, colour rgb) {
	switch status {
	case "working":
		glyph, colour = "●", railWorking
		if nerd {
			glyph = "\uf013"
		}
	case "idle":
		glyph, colour = "○", rgb{120, 200, 225}
		if nerd {
			glyph = "\uf111"
		}
	case "blocked":
		glyph, colour = "▲", rgb{255, 110, 110}
		if nerd {
			glyph = "\uf071"
		}
	case "done":
		glyph, colour = "✓", rgb{110, 225, 145}
		if nerd {
			glyph = "\uf00c"
		}
	case "error":
		glyph, colour = "✕", rgb{255, 130, 160}
		if nerd {
			glyph = "\uf057"
		}
	default:
		glyph, colour = "?", railDim
		if nerd {
			glyph = "\uf128"
		}
	}
	return glyph, colour
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

// railSeg is one run of a rail row: its text, its colour, and its own weight.
type railSeg struct {
	text   string
	colour rgb
	bold   bool
}

// runeCells is the cell width of a run of text.
func runeCells(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

// segCells is the cell width of a segment run.
func segCells(segs []railSeg) int {
	n := 0
	for _, seg := range segs {
		n += runeCells(seg.text)
	}
	return n
}

// clipSegs keeps the first budget cells of a segment run.
func clipSegs(segs []railSeg, budget int) []railSeg {
	var out []railSeg
	for _, seg := range segs {
		if budget <= 0 {
			break
		}
		var keep []rune
		for _, r := range seg.text {
			rw := runeWidth(r)
			if rw > budget {
				budget = 0
				break
			}
			keep = append(keep, r)
			budget -= rw
		}
		if len(keep) > 0 {
			seg.text = string(keep)
			out = append(out, seg)
		}
	}
	return out
}

// elideSegs cuts a segment run to a cell budget and ends it with an ellipsis,
// so a long label shrinks before the right-aligned stats lose a cell.
func elideSegs(segs []railSeg, budget int) []railSeg {
	if budget <= 0 {
		return nil
	}
	if budget == 1 {
		return []railSeg{{text: "…", colour: railDim}}
	}
	out := clipSegs(segs, budget-1)
	return append(out, railSeg{text: "…", colour: railDim})
}

// writeRailSeg paints one run of a row on its background.
func writeRailSeg(buf *bytes.Buffer, seg railSeg, bg rgb, bold bool) {
	if seg.text == "" {
		return
	}
	buf.WriteString("\x1b[0m" + bg.bg())
	if bold || seg.bold {
		buf.WriteString("\x1b[1m")
	}
	buf.WriteString(seg.colour.fg())
	buf.WriteString(seg.text)
}

// railStats is the right-aligned git tail of a window line: an amber dot when
// the tree is dirty, then the added and deleted line counts, or "clean".
func railStats(it railItem) []railSeg {
	if !it.inRepo {
		return nil
	}
	var segs []railSeg
	if it.dirty {
		segs = append(segs, railSeg{text: "●", colour: railWorking})
	}
	if it.added > 0 {
		segs = append(segs, railSeg{text: "+" + strconv.Itoa(it.added), colour: railDiffAdd})
	}
	if it.deleted > 0 {
		segs = append(segs, railSeg{text: "-" + strconv.Itoa(it.deleted), colour: railDiffDel})
	}
	if len(segs) == 0 {
		return []railSeg{{text: "clean", colour: railDim}}
	}
	out := make([]railSeg, 0, len(segs)*2)
	for i, seg := range segs {
		if i > 0 {
			out = append(out, railSeg{text: " ", colour: railDim})
		}
		out = append(out, seg)
	}
	return out
}

// railRow composes one sidebar row at width w. left is the row's own run,
// right the run pinned to the right edge and tail the run that follows left
// and yields first. compact and nerd are the rail's own switches.
func railRow(it railItem, w int, compact, nerd bool) (left, right, tail []railSeg) {
	switch it.kind {
	case railWorkspace:
		if !compact {
			name := railText
			if !it.active {
				name = railHeader
			}
			left = []railSeg{
				{text: "\u258c", colour: it.accent},
				{text: " ", colour: name},
				{text: it.text, colour: name, bold: true},
			}
			right = []railSeg{{text: strconv.Itoa(it.count), colour: railDim}}
		}
	case railWindow:
		base := railWindowFg
		if !compact {
			caret := "\u25be"
			if it.folded {
				caret = "\u25b8"
			}
			if nerd {
				caret = "\uf0d7"
				if it.folded {
					caret = "\uf0da"
				}
			}
			// The folder names the window. A window whose panes reported no
			// directory falls back to the window name.
			label := it.folder
			if label == "" {
				label = it.text
			}
			left = []railSeg{
				{text: "  ", colour: base},
				{text: caret, colour: base},
				{text: " ", colour: base},
				{text: label, colour: base},
			}
			// The branch trails the folder in dim grey, and only when it says
			// something the label does not.
			if it.branch != "" && it.branch != label {
				tail = []railSeg{
					{text: "  ", colour: railDim},
					{text: it.branch, colour: railDim},
				}
			}
			right = railStats(it)
		}
	case railWindowNote:
		if !compact {
			base := railDim
			left = []railSeg{
				{text: "    ", colour: base},
				{text: it.text, colour: base},
			}
		}
	case railPane:
		base := railDim
		if it.focus || it.agent {
			base = railText
		}
		g, colour := railGlyph(it.status, nerd)
		// A shell has no state to report, so the fallback mark would be a
		// question about nothing. Leave the cell blank instead.
		if !it.agent && (it.status == "" || it.status == "unknown") {
			g = " "
		}
		mark := railMark(it.agent, nerd)
		if compact {
			left = []railSeg{
				{text: mark, colour: colour},
				{text: g, colour: colour},
			}
		} else {
			left = []railSeg{
				{text: "      ", colour: base},
				{text: mark, colour: colour},
				{text: g, colour: colour},
				{text: " " + it.text, colour: base},
			}
		}
	case railGap:
		// The rail background and nothing else.
	}
	rightCells := segCells(right)
	if rightCells > w {
		// A tail that does not fit goes whole: half a stats pair lies.
		right, rightCells = nil, 0
	}
	budget := w - rightCells
	if rightCells > 0 && budget > 0 {
		budget--
	}
	// The folder outranks the branch: a window line gives up the branch tail
	// whole, and only then elides the folder.
	tailCells := segCells(tail)
	leftCells := segCells(left) + tailCells
	if tailCells > 0 && leftCells > budget {
		tail, leftCells = nil, segCells(left)
	}
	if leftCells > budget {
		left = elideSegs(left, budget)
	}
	return left, right, tail
}

// railPaneAt returns the pane whose row is under x,y, or "" for any other row,
// an empty row, or a point outside the rail.
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

// railWindowAt returns the window whose row is under x,y, for a fold click. A
// window line and its note line both fold the window.
func railWindowAt(snap *Snapshot, x, y int) string {
	if snap == nil || snap.Rail <= 0 {
		return ""
	}
	if x < 0 || x >= snap.Rail || y < 0 || y >= snap.Rows-1 {
		return ""
	}
	items := railItems(snap)
	if y >= len(items) {
		return ""
	}
	if k := items[y].kind; k != railWindow && k != railWindowNote {
		return ""
	}
	return items[y].window
}

// drawRail paints the sidebar above the status bar. The rail paints its own
// background so contrast does not depend on the terminal theme. Rows past the
// screen are clipped, and every row is written into at most Rail columns. A
// left run is elided with an ellipsis before the right-aligned stats give up a
// cell, so the stats survive a narrow rail.
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
		var left, right, tail []railSeg
		bg, bold := railBG, false
		if y < len(items) {
			it := items[y]
			left, right, tail = railRow(it, w, compact, nerd)
			// A focused pane row paints its own background and every run bold.
			if it.kind == railPane && it.focus {
				bg, bold = railFocusBG, true
			}
		}
		leftCells := segCells(left) + segCells(tail)
		rightCells := segCells(right)
		fmt.Fprintf(buf, "\x1b[%d;1H", y+1)
		buf.WriteString("\x1b[0m" + bg.bg())
		for _, seg := range left {
			writeRailSeg(buf, seg, bg, bold)
		}
		for _, seg := range tail {
			writeRailSeg(buf, seg, bg, bold)
		}
		if pad := w - leftCells - rightCells; pad > 0 {
			buf.WriteString(strings.Repeat(" ", pad))
		}
		for _, seg := range right {
			writeRailSeg(buf, seg, bg, bold)
		}
		buf.WriteString("\x1b[0m")
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

// drawBar paints the one-line status bar on the last row. It paints the
// segment model span by span: reset, ground, foreground, weight, text. The
// pinned block is anchored to the last column and the gutter fills the gap, so
// the whole row is written and no cell of the previous frame shows through.
// drawBar writes at most snap.Cols cells and never touches another row.
func drawBar(buf *bytes.Buffer, snap *Snapshot, armed bool, info string) {
	if snap == nil || snap.Rows < 1 || snap.Cols < 1 {
		return
	}
	segs := barSegments(snap, armed, info)
	left, pinned := barSplitPinned(segs)
	fmt.Fprintf(buf, "\x1b[%d;1H", snap.Rows)
	writeBarSegs(buf, left)
	if pad := snap.Cols - barCells(segs); pad > 0 {
		buf.WriteString("\x1b[0m" + barGutter.bg() + strings.Repeat(" ", pad))
	}
	writeBarSegs(buf, pinned)
	buf.WriteString("\x1b[0m")
}

// barSplitPinned splits a span run at its first pinned span: the left block,
// then the right block that is anchored to the last column.
func barSplitPinned(segs []barSeg) (left, pinned []barSeg) {
	for i, seg := range segs {
		if seg.pin {
			return segs[:i], segs[i:]
		}
	}
	return segs, nil
}

// writeBarSegs paints spans left to right, each one reset before its colours.
func writeBarSegs(buf *bytes.Buffer, segs []barSeg) {
	for _, seg := range segs {
		if seg.text == "" {
			continue
		}
		buf.WriteString("\x1b[0m" + seg.bg.bg() + seg.fg.fg())
		if seg.bold {
			buf.WriteString("\x1b[1m")
		}
		buf.WriteString(seg.text)
	}
}

// barSeg is one painted span of the status bar. A pinned segment belongs to
// the right block, which sits flush against the last column; the rest form the
// left block, in paint order.
type barSeg struct {
	text string
	fg   rgb
	bg   rgb
	bold bool
	pin  bool
}

// barCells is the painted cell width of a span run.
func barCells(segs []barSeg) int {
	n := 0
	for _, seg := range segs {
		n += runeCells(seg.text)
	}
	return n
}

// barClipText keeps the longest prefix of s that fits budget cells. A wide rune
// that would straddle the limit is dropped, never half painted.
func barClipText(s string, budget int) string {
	if budget <= 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		w := runeWidth(r)
		if w > budget {
			break
		}
		b.WriteRune(r)
		budget -= w
	}
	return b.String()
}

// barElideText shrinks s to budget cells and ends it with an ellipsis.
func barElideText(s string, budget int) string {
	if budget <= 0 {
		return ""
	}
	if budget == 1 {
		return "…"
	}
	return barClipText(s, budget-1) + "…"
}

// barClipSegs keeps the first budget cells of a span run, whole runes only.
func barClipSegs(segs []barSeg, budget int) []barSeg {
	var out []barSeg
	for _, seg := range segs {
		if budget <= 0 {
			break
		}
		text := barClipText(seg.text, budget)
		if text == "" {
			// The run ended on a wide rune the budget cannot hold, so no later
			// span can be painted in order either.
			break
		}
		budget -= runeCells(text)
		seg.text = text
		out = append(out, seg)
	}
	return out
}

// barJoin lays groups left to right with one background gutter cell between
// them, so neighbouring chips never touch.
func barJoin(groups [][]barSeg) []barSeg {
	var out []barSeg
	for i, group := range groups {
		if i > 0 {
			out = append(out, barSeg{text: " ", fg: railDim, bg: railBG})
		}
		out = append(out, group...)
	}
	return out
}

// barWindow is one window chip of the left block: its index in the window
// list, its name, and whether it is the active window.
type barWindow struct {
	num    int
	name   string
	active bool
}

// barFocusChip is the focus chip of the left block: a state mark, the pane id,
// and the pane name and status word while they survive elision.
type barFocusChip struct {
	glyph  string
	colour rgb
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
}

// barFocus builds the focus chip from the focused pane. An agent reports a
// state, so it takes that state's glyph and colour; a shell gets the prompt
// marker in dim text. A pane the snapshot does not list gets no chip.
func barFocus(snap *Snapshot) *barFocusChip {
	var p PaneInfo
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
	glyph, colour := "▶", railDim
	if agent {
		glyph, colour = railGlyph(status, nerdFonts())
	}
	return &barFocusChip{
		glyph: glyph, colour: colour,
		id: p.ID, name: p.Name, status: status,
	}
}

// barLeftFor reads the left block from a snapshot in its full, unelided form.
func barLeftFor(snap *Snapshot) *barLeft {
	l := &barLeft{
		session:   snap.Session,
		workspace: snap.Workspace,
		winNames:  true,
		showName:  true,
		showStat:  true,
	}
	for i, name := range snap.Windows {
		l.windows = append(l.windows, barWindow{num: i + 1, name: name, active: i == snap.ActiveWindow})
	}
	l.focus = barFocus(snap)
	return l
}

// groups builds the left block spans as they currently stand: session,
// workspace, one chip per window, then the focus chip.
func (l *barLeft) groups() [][]barSeg {
	var out [][]barSeg
	if l.session != "" {
		out = append(out, []barSeg{{text: l.session, fg: railText, bg: railBG}})
	}
	if l.workspace != "" {
		out = append(out, []barSeg{{text: l.workspace, fg: railHeader, bg: railBG}})
	}
	for _, w := range l.windows {
		if w.active {
			// The active window reads as active: an accent mark, then the chip
			// in bold on the focus fill.
			text := strconv.Itoa(w.num)
			if l.winNames && w.name != "" {
				text += " " + w.name
			}
			out = append(out, []barSeg{
				{text: "▸ ", fg: barAccent, bg: railFocusBG},
				{text: text, fg: railText, bg: railFocusBG, bold: true},
			})
			continue
		}
		text := strconv.Itoa(w.num)
		if l.winNames && w.name != "" {
			text += " " + w.name
		}
		out = append(out, []barSeg{{text: text, fg: railDim, bg: railBG}})
	}
	if l.focus != nil {
		chip := []barSeg{
			{text: l.focus.glyph, fg: l.focus.colour, bg: railFocusBG},
			{text: " " + l.focus.id, fg: railText, bg: railFocusBG, bold: true},
		}
		if l.showName && l.focus.name != "" {
			chip = append(chip, barSeg{text: " " + l.focus.name, fg: railDim, bg: railFocusBG})
		}
		if l.showStat && l.focus.status != "" {
			chip = append(chip, barSeg{text: " " + l.focus.status, fg: railDim, bg: railFocusBG})
		}
		out = append(out, chip)
	}
	return out
}

// fit elides the left block to budget cells in the ladder the footer uses:
// window names, then trailing window chips, then the workspace name, then the
// session name, then the focus chip's status word and name. The focus chip
// keeps its glyph and id last, and the result never exceeds the budget.
func (l *barLeft) fit(budget int) []barSeg {
	if budget <= 0 {
		return nil
	}
	cur := func() []barSeg { return barJoin(l.groups()) }
	if segs := cur(); barCells(segs) <= budget {
		return segs
	}

	// 1. Window chips lose their name and keep the number.
	l.winNames = false
	if segs := cur(); barCells(segs) <= budget {
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
		if segs := cur(); barCells(segs) <= budget {
			return segs
		}
	}

	// 3. The workspace name, then 4. the session name, shrink to a trailing
	// ellipsis. A name that cannot shrink leaves its ellipsis alone; later
	// steps make the room.
	for _, name := range []*string{&l.workspace, &l.session} {
		if *name == "" {
			continue
		}
		avail := budget - (barCells(cur()) - runeCells(*name))
		if avail < 1 {
			avail = 1
		}
		*name = barElideText(*name, avail)
		if segs := cur(); barCells(segs) <= budget {
			return segs
		}
	}

	// 5. The focus chip drops its status word, then its name.
	l.showStat = false
	if segs := cur(); barCells(segs) <= budget {
		return segs
	}
	l.showName = false
	if segs := cur(); barCells(segs) <= budget {
		return segs
	}

	// 6. Only the glyph and the id are left; a line too narrow for that is cut.
	return barClipSegs(cur(), budget)
}

// barPinned is the right block: the prefix flag while armed, then the scroll
// info. Both are anchored to the last column, so both are pinned.
func barPinned(armed bool, info string) []barSeg {
	var out []barSeg
	if armed {
		out = append(out, barSeg{text: "PREFIX ", fg: railText, bg: railBG, bold: true, pin: true})
	}
	if info != "" {
		out = append(out, barSeg{text: info, fg: railDim, bg: railBG, pin: true})
	}
	return out
}

// barFitPinned fits the right block to cols cells on its own. A pinned segment
// goes whole, leftmost first, and the survivor is clipped with an ellipsis.
func barFitPinned(segs []barSeg, cols int) []barSeg {
	for barCells(segs) > cols {
		if len(segs) > 1 {
			segs = segs[1:]
			continue
		}
		if len(segs) == 0 {
			break
		}
		segs[0].text = barElideText(segs[0].text, cols)
		break
	}
	return segs
}

// barSegments returns the status bar as spans in paint order: the left block,
// then the pinned right block. It sizes the left block against the room the
// pinned block leaves, minus one gutter cell, so the blocks never touch and the
// line never runs past snap.Cols cells. The pinned block is never dropped to
// make room for the left one.
func barSegments(snap *Snapshot, armed bool, info string) []barSeg {
	if snap == nil || snap.Cols <= 0 {
		return nil
	}
	right := barFitPinned(barPinned(armed, info), snap.Cols)
	budget := snap.Cols - barCells(right)
	if len(right) > 0 {
		// Keep one gutter cell between the blocks. drawBar spends it as part of
		// the pad, so the pinned block still ends on the last column.
		budget--
	}
	if budget < 0 {
		budget = 0
	}
	left := barLeftFor(snap).fit(budget)
	return append(left, right...)
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

// drawRename paints the rename prompt as one full row above the status bar,
// over the last pane row, on the rail ground. The label and the caret are
// fixed: the draft takes what is left and loses its head first, so a long name
// still shows its end and the caret stays on screen. drawRename writes at most
// snap.Cols cells and touches one row.
func drawRename(buf *bytes.Buffer, snap *Snapshot, id, draft string) {
	if snap == nil || snap.Rows < 2 || snap.Cols < 1 {
		return
	}
	const caret = "▏"
	labelRoom := snap.Cols - runeCells(caret)
	if labelRoom < 0 {
		labelRoom = 0
	}
	segs := clipSegs([]railSeg{{text: " rename workspace " + id + ": ", colour: railDim}}, labelRoom)
	draftRoom := snap.Cols - runeCells(caret) - segCells(segs)
	segs = append(segs,
		railSeg{text: elideRename(draft, draftRoom), colour: railText},
		railSeg{text: caret, colour: barAccent},
	)
	fmt.Fprintf(buf, "\x1b[%d;1H\x1b[0m", snap.Rows-1)
	buf.WriteString(railBG.bg())
	for _, seg := range segs {
		buf.WriteString(seg.colour.fg())
		buf.WriteString(seg.text)
	}
	if pad := snap.Cols - segCells(segs); pad > 0 {
		buf.WriteString(railBG.bg())
		buf.WriteString(strings.Repeat(" ", pad))
	}
	buf.WriteString("\x1b[0m")
}

// elideRename keeps the last budget cells of s and marks a cut head with an
// ellipsis. It never splits a wide rune.
func elideRename(s string, budget int) string {
	if budget <= 0 {
		return ""
	}
	if runeCells(s) <= budget {
		return s
	}
	room := budget - 1 // the ellipsis takes one cell
	if room <= 0 {
		return ""
	}
	r := []rune(s)
	used := 0
	i := len(r)
	for i > 0 {
		w := runeWidth(r[i-1])
		if used+w > room {
			break
		}
		used += w
		i--
	}
	return "…" + string(r[i:])
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// drawHelp paints the bottom-right action preview from the configured keymap
// rows. The first row is the prefix header.
func drawHelp(buf *bytes.Buffer, cols, rows int, keys [][2]string) {
	width := 28
	height := len(keys) + 2
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
	for i, kv := range keys {
		fmt.Fprintf(buf, "\x1b[%d;%dH│ %-8s %-*s│", y+3+i, x+1, kv[0], width-11, kv[1])
	}
	fmt.Fprintf(buf, "\x1b[%d;%dH└%s┘", y+height, x+1, strings.Repeat("─", width-2))
	buf.WriteString("\x1b[0m")
}
