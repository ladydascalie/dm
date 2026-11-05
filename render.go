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
func drawPaneBox(buf *bytes.Buffer, th Theme, content Rect, active bool) {
	drawPaneBoxRows(buf, th, content, active, false)
}

// drawPaneBoxRows draws the border of one pane. skipTop leaves the top row
// alone, for a caller that writes it itself as one row, so no cell of it lands
// twice.
func drawPaneBoxRows(buf *bytes.Buffer, th Theme, content Rect, active bool, skipTop bool) {
	g := th.Glyphs
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
	if !skipTop {
		put(x0, y0, g.BorderTopLeft)
		put(x1, y0, g.BorderTopRight)
	}
	put(x0, y1, g.BorderBottomLeft)
	put(x1, y1, g.BorderBottomRight)
	for x := x0 + 1; x < x1; x++ {
		if !skipTop {
			put(x, y0, g.BorderHorizontal)
		}
		put(x, y1, g.BorderHorizontal)
	}
	for y := y0 + 1; y < y1; y++ {
		put(x0, y, g.BorderVertical)
		put(x1, y, g.BorderVertical)
	}
}

// drawGrips marks every divider at its midpoint, so a handle is visible before
// the pointer finds it. The hover paints over the whole divider. A zoomed pane
// covers every divider, so a grip would be a lie over a hidden split.
func drawGrips(buf *bytes.Buffer, th Theme, snap *Snapshot) {
	if snap == nil || snap.Root == nil || snap.Rail < 0 || snap.Zoom != "" {
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
				fmt.Fprintf(buf, "\x1b[%d;%dH%s", y+1, p.X+1, th.Glyphs.Grip)
				if p.W > 1 {
					fmt.Fprintf(buf, "\x1b[%d;%dH%s", y+1, p.X+2, th.Glyphs.Grip)
				}
			} else {
				x := p.X + p.W/2
				fmt.Fprintf(buf, "\x1b[%d;%dH%s", p.Y+1, x+1, th.Glyphs.Grip)
				if p.H > 1 {
					fmt.Fprintf(buf, "\x1b[%d;%dH%s", p.Y+2, x+1, th.Glyphs.Grip)
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
func drawDivider(buf *bytes.Buffer, th Theme, z Rect, axis string) {
	if z.W <= 0 || z.H <= 0 {
		return
	}
	glyph := th.Glyphs.DividerCol
	if axis == "col" {
		glyph = th.Glyphs.DividerRow
	}
	buf.WriteString("\x1b[0m\x1b[1;96m")
	line := strings.Repeat(glyph, z.W)
	for y := z.Y; y < z.Y+z.H; y++ {
		fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, z.X+1)
		buf.WriteString(line)
	}
	buf.WriteString("\x1b[0m")
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
	kind       railKind
	text       string
	workspace  string
	folder     string
	cwd        string
	branch     string
	worktree   string
	message    string
	pane       string
	window     string
	windowName string
	status     string
	count      int
	added      int
	deleted    int
	accent     int // an accent slot, resolved against the theme by the painter
	agent      bool
	dirty      bool
	inRepo     bool
	folded     bool
	focus      bool
	active     bool
}

// railItems flattens a snapshot into sidebar rows in the server's order: a
// workspace row, a window line per window, that window's note line when it has
// a message and is unfolded, then its pane rows when it is unfolded. A gap row
// separates workspace blocks. The thin rail is a glyph column: one pane row
// per pane, in snapshot order, whatever the fold. It has no workspace row, no
// window row, no note row and no gap row.
func railItems(snap *Snapshot) []railItem {
	if snap == nil {
		return nil
	}
	if railCompact(snap.Rail) {
		out := make([]railItem, 0, len(snap.All))
		for _, p := range snap.All {
			out = append(out, railPaneRow(p))
		}
		return out
	}
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
	// A workspace keeps its preferred slot when it is free. When two live
	// workspaces hash alike, the later one takes the next free slot, so no two
	// workspaces in a session ever share an accent.
	used := map[int]bool{}
	accent := func(id string) int {
		pref := railAccentSlot(id)
		for i := 0; i < accentSlots; i++ {
			slot := (pref + i) % accentSlots
			if !used[slot] {
				used[slot] = true
				return slot
			}
		}
		return pref
	}
	var out []railItem
	ws, win := "", ""
	for _, p := range snap.All {
		if p.Workspace != ws {
			if ws != "" {
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
				windowName: p.WindowName, workspace: p.Workspace,
				folded: !p.WindowUnfolded,
			}
			if src, ok := source(p.Window); ok {
				item.folder = railFolder(src.Cwd, src.Root)
				item.cwd = src.Cwd
				item.branch = src.Branch
				item.worktree = src.Worktree
				item.inRepo = src.InRepo
				item.dirty = src.Dirty
				item.added = src.Added
				item.deleted = src.Deleted
				item.message = src.Message
			}
			out = append(out, item)
			if item.message != "" && p.WindowUnfolded {
				out = append(out, railItem{
					kind: railWindowNote, text: item.message, window: p.Window,
					windowName: p.WindowName, workspace: p.Workspace,
					folder: item.folder, cwd: item.cwd,
				})
			}
		}
		if !p.WindowUnfolded {
			continue
		}
		out = append(out, railPaneRow(p))
	}
	return out
}

// railPaneRow is the pane row both rails draw. The label falls back from the
// pane name to its app to its id.
func railPaneRow(p AllPane) railItem {
	label := p.Name
	if label == "" {
		label = p.App
	}
	if label == "" {
		label = p.ID
	}
	return railItem{
		kind: railPane, text: label, pane: p.ID,
		window: p.Window, windowName: p.WindowName, workspace: p.Workspace,
		status: p.Status, agent: p.Agent, focus: p.Focus,
	}
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

// railAccentSlot returns the accent slot a workspace prefers. FNV-1a is the
// seed; the mix step matters because its low bits repeat for ids of equal
// length, and a workspace id is short.
func railAccentSlot(workspace string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(workspace))
	x := h.Sum32()
	x ^= x >> 16
	x *= 0x7feb352d
	x ^= x >> 15
	x *= 0x846ca68b
	x ^= x >> 16
	return int(x % accentSlots)
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

// stateStyle returns the mark for a status. A status the theme does not name
// falls back to the unknown style.
func stateStyle(th Theme, status string) StateStyle {
	if s, ok := th.States[status]; ok {
		return s
	}
	if s, ok := th.States["unknown"]; ok {
		return s
	}
	return DefaultTheme().States["unknown"]
}

// railGlyph is the state mark for a pane row and its colour: amber working,
// teal-blue idle, red blocked, green done, pink-red error, dim grey unknown.
// Every state gets a distinct icon or, without Nerd Fonts, a distinct Unicode
// glyph. The question mark marks an agent whose state has not arrived; drawRail
// leaves the cell blank for a shell, which reports no state. A theme's glyph
// replaces both forms; an empty NerdGlyph keeps the Unicode glyph.
func railGlyph(th Theme, status string, nerd bool) (glyph string, colour rgb) {
	style := stateStyle(th, status)
	glyph = style.Glyph
	if nerd && style.NerdGlyph != "" {
		glyph = style.NerdGlyph
	}
	return glyph, style.Colour
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

// writeRailSeg paints one run on the rail's row ground. The rail carries one
// background and one weight per row, so they arrive from the caller and ride
// the run only for the length of this write. The switcher's rail rows call it
// too.
func writeRailSeg(buf *bytes.Buffer, s seg, ground rgb, bold bool) {
	s.bg = ground
	s.bold = s.bold || bold
	write(buf, s)
}

// railStats is the right-aligned git tail of a window line: an amber dot when
// the tree is dirty, then the added and deleted line counts, or "clean".
func railStats(th Theme, it railItem) []seg {
	if !it.inRepo {
		return nil
	}
	p := th.Palette
	var segs []seg
	if it.dirty {
		segs = append(segs, seg{text: th.Glyphs.Dirty, fg: p.RailWorking})
	}
	if it.added > 0 {
		segs = append(segs, seg{text: "+" + strconv.Itoa(it.added), fg: p.RailDiffAdd})
	}
	if it.deleted > 0 {
		segs = append(segs, seg{text: "-" + strconv.Itoa(it.deleted), fg: p.RailDiffDel})
	}
	if len(segs) == 0 {
		return []seg{{text: "clean", fg: p.RailDim}}
	}
	out := make([]seg, 0, len(segs)*2)
	for i, s := range segs {
		if i > 0 {
			out = append(out, seg{text: " ", fg: p.RailDim})
		}
		out = append(out, s)
	}
	return out
}

// railRow composes one sidebar row at width w. left is the row's own run,
// right the run pinned to the right edge and tail the run that follows left
// and yields first. compact and nerd are the rail's own switches.
func railRow(th Theme, it railItem, w int, compact, nerd bool) (left, right, tail []seg) {
	p, g := th.Palette, th.Glyphs
	switch it.kind {
	case railWorkspace:
		if !compact {
			name := p.RailText
			if !it.active {
				name = p.RailHeader
			}
			left = []seg{
				{text: g.WorkspaceBar, fg: th.Accents[it.accent]},
				{text: " ", fg: name},
				{text: it.text, fg: name, bold: true},
			}
			right = []seg{{text: strconv.Itoa(it.count), fg: p.RailDim}}
		}
	case railWindow:
		base := p.RailWindow
		if !compact {
			caret := g.FoldOpen
			if it.folded {
				caret = g.FoldClosed
			}
			if nerd {
				caret = g.NerdFoldOpen
				if it.folded {
					caret = g.NerdFoldClosed
				}
			}
			// The folder names the window. A window whose panes reported no
			// directory falls back to the window name.
			label := it.folder
			if label == "" {
				label = it.text
			}
			left = []seg{
				{text: "  ", fg: base},
				{text: caret, fg: base},
				{text: " ", fg: base},
				{text: label, fg: base},
			}
			// The branch trails the folder in dim grey, and only when it says
			// something the label does not.
			if it.branch != "" && it.branch != label {
				tail = []seg{
					{text: "  ", fg: p.RailDim},
					{text: it.branch, fg: p.RailDim},
				}
			}
			right = railStats(th, it)
		}
	case railWindowNote:
		if !compact {
			base := p.RailDim
			left = []seg{
				{text: "    ", fg: base},
				{text: it.text, fg: base},
			}
		}
	case railPane:
		base := p.RailDim
		if it.focus || it.agent {
			base = p.RailText
		}
		mark, colour := railGlyph(th, it.status, nerd)
		// A shell has no state to report, so the fallback mark would be a
		// question about nothing. Leave the cell blank instead.
		if !it.agent && (it.status == "" || it.status == "unknown") {
			mark = " "
		}
		glyph := railMark(it.agent, nerd)
		if compact {
			left = []seg{
				{text: glyph, fg: colour},
				{text: mark, fg: colour},
			}
		} else {
			left = []seg{
				{text: "      ", fg: base},
				{text: glyph, fg: colour},
				{text: mark, fg: colour},
				{text: " " + it.text, fg: base},
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
		left = elideSegs(th, left, budget)
	}
	return left, right, tail
}

// railPaneAt returns the pane whose row is under x,y, or "" for any other row,
// an empty row, a point outside the rail, or the rail's bottom control row.
func railPaneAt(snap *Snapshot, x, y int) string {
	if snap == nil || snap.Rail <= 0 {
		return ""
	}
	if x < 0 || x >= snap.Rail || y < 0 || y >= snap.Rows-2 {
		return ""
	}
	items := railItems(snap)
	if y >= len(items) || items[y].kind != railPane {
		return ""
	}
	return items[y].pane
}

// railWindowAt returns the window whose row is under x,y, for a fold click. A
// window line and its note line both fold the window. The thin rail has no
// window row, so it never answers.
func railWindowAt(snap *Snapshot, x, y int) string {
	if snap == nil || snap.Rail <= 0 {
		return ""
	}
	if x < 0 || x >= snap.Rail || y < 0 || y >= snap.Rows-2 {
		return ""
	}
	if railCompact(snap.Rail) {
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

// railItemAt returns the rail row under x,y. A point outside the rail, on the
// bottom control row, or past the last item reports false. The gap row between
// workspace blocks is a row, so it answers true.
func railItemAt(snap *Snapshot, x, y int) (railItem, bool) {
	if snap == nil || snap.Rail <= 0 {
		return railItem{}, false
	}
	if x < 0 || x >= snap.Rail || y < 0 || y >= snap.Rows-2 {
		return railItem{}, false
	}
	items := railItems(snap)
	if y >= len(items) {
		return railItem{}, false
	}
	return items[y], true
}

// railControlAt reports whether x,y is the rail's bottom control row, whose
// click toggles the rail between full and thin. The whole row inside the rail
// is the target, so the row belongs to the control and to no item.
func railControlAt(snap *Snapshot, x, y int) bool {
	if snap == nil || snap.Rail <= 0 || snap.Rows < 2 {
		return false
	}
	return x >= 0 && x < snap.Rail && y == snap.Rows-2
}

// railControlSegs is the left run of the control row: the move a click makes.
// A rail wider than the thin one names the move and collapses. The thin rail
// shows the expand caret in the state glyph's column and leaves the rest
// blank, so a narrow rail loses no pane row to a label.
func railControlSegs(th Theme, compact, nerd bool) []seg {
	if compact {
		caret := "▶"
		if nerd {
			caret = "\uf0da"
		}
		return []seg{
			{text: "  ", fg: th.Palette.RailDim},
			{text: caret, fg: th.Palette.RailDim},
		}
	}
	mark := "◀ "
	if nerd {
		mark = "\uf0d9 "
	}
	return []seg{
		{text: mark, fg: th.Palette.RailDim},
		{text: "collapse", fg: th.Palette.RailDim},
	}
}

// drawRail paints the sidebar above the status bar. The rail paints its own
// background so contrast does not depend on the terminal theme. Rows past the
// screen are clipped, and every row is written into at most Rail columns. A
// left run is elided with an ellipsis before the right-aligned stats give up a
// cell, so the stats survive a narrow rail.
func drawRail(buf *bytes.Buffer, th Theme, snap *Snapshot) {
	if snap == nil || snap.Rail <= 0 || snap.Cols < 1 || snap.Rows < 3 {
		return
	}
	p := th.Palette
	w := snap.Rail
	rows := snap.Rows - 1
	items := railItems(snap)
	nerd := nerdFonts()
	compact := railCompact(snap.Rail)
	// The status bar holds Rows-1 and the control holds Rows-2, so items draw
	// in rows 0..Rows-3 and clip there. Nothing overwrites the control row.
	controlAt := snap.Rows - 2
	gripAt := controlAt / 2
	for y := 0; y < rows; y++ {
		var left, right, tail []seg
		bg, bold := p.RailBG, false
		if y == controlAt {
			left = railControlSegs(th, compact, nerd)
			if segCells(left) > w {
				left = elideSegs(th, left, w)
			}
		} else if y < len(items) {
			it := items[y]
			left, right, tail = railRow(th, it, w, compact, nerd)
			// A focused pane row paints its own background and every run bold.
			if it.kind == railPane && it.focus {
				bg, bold = p.RailFocusBG, true
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
		if n := w - leftCells - rightCells; n > 0 {
			buf.WriteString(strings.Repeat(" ", n))
		}
		for _, seg := range right {
			writeRailSeg(buf, seg, bg, bold)
		}
		if y < controlAt && y == gripAt {
			fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, w)
			buf.WriteString("\x1b[0m" + p.RailBG.bg() + p.RailDim.fg() + th.Glyphs.Grip)
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

// drawMenu paints the right-click menu over the panes. The panel owns the border,
// the title row and the interior clip; the menu owns its item rows and the
// selection highlight. The title row is a fill row: dim, with the label between
// the border rune and a border-rune fill, where the panel's label style is
// bright, so the row is one write and its cells are the menu's.
func drawMenu(buf *bytes.Buffer, th Theme, m *menu) {
	if m == nil || m.w < 8 {
		return
	}
	inner := Rect{X: m.x + 1, Y: m.y + 1, W: m.w - 2, H: len(m.items)}
	paintPanel(buf, th, inner, panelTitle{
		text:  th.Glyphs.BorderHorizontal + " " + m.title + " ",
		style: "\x1b[0m\x1b[90m",
		fill:  th.Glyphs.BorderHorizontal,
	}, "", false, func(b *bytes.Buffer, r Rect) {
		for i, it := range m.items {
			line := pad(" "+pad(it.label, 22)+" "+pad(it.key, 4), r.W)
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
func drawBar(buf *bytes.Buffer, th Theme, snap *Snapshot, armed bool, info string) {
	if snap == nil || snap.Rows < 1 || snap.Cols < 1 {
		return
	}
	segs := barSegments(th, snap, armed, info)
	left, pinned := barSplitPinned(segs)
	fmt.Fprintf(buf, "\x1b[%d;1H", snap.Rows)
	for _, s := range left {
		write(buf, s)
	}
	if n := snap.Cols - segCells(segs); n > 0 {
		// The bar's ground is the rail's: there is no second background.
		buf.WriteString("\x1b[0m" + th.Palette.RailBG.bg() + strings.Repeat(" ", n))
	}
	for _, s := range pinned {
		write(buf, s)
	}
	buf.WriteString("\x1b[0m")
}

// barSplitPinned splits a span run at its first pinned span: the left block,
// then the right block that is anchored to the last column.
func barSplitPinned(segs []seg) (left, pinned []seg) {
	for i, seg := range segs {
		if seg.pin {
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
	showZoom  bool
}

// barFocus builds the focus chip from the focused pane. An agent reports a
// state, so it takes that state's glyph and colour; a shell gets the prompt
// marker in dim text. A pane the snapshot does not list gets no chip.
func barFocus(th Theme, snap *Snapshot) *barFocusChip {
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
	shell := stateStyle(th, "shell")
	glyph, colour := shell.Glyph, shell.Colour
	if agent {
		glyph, colour = railGlyph(th, status, nerdFonts())
	}
	return &barFocusChip{
		glyph: glyph, colour: colour,
		id: p.ID, name: p.Name, status: status,
	}
}

// barLeftFor reads the left block from a snapshot in its full, unelided form.
func barLeftFor(th Theme, snap *Snapshot) *barLeft {
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
func (l *barLeft) groups(th Theme) [][]seg {
	p, g := th.Palette, th.Glyphs
	var out [][]seg
	if l.session != "" {
		out = append(out, []seg{{text: l.session, fg: p.RailText, bg: p.RailBG}})
	}
	if l.workspace != "" {
		out = append(out, []seg{{text: l.workspace, fg: p.RailHeader, bg: p.RailBG}})
	}
	for _, w := range l.windows {
		if w.active {
			// The active window reads as active: an accent mark, then the chip
			// in bold on the focus fill.
			text := strconv.Itoa(w.num)
			if l.winNames && w.name != "" {
				text += " " + w.name
			}
			out = append(out, []seg{
				{text: g.ActiveMark + " ", fg: p.BarAccent, bg: p.RailFocusBG},
				{text: text, fg: p.RailText, bg: p.RailFocusBG, bold: true},
			})
			continue
		}
		text := strconv.Itoa(w.num)
		if l.winNames && w.name != "" {
			text += " " + w.name
		}
		out = append(out, []seg{{text: text, fg: p.RailDim, bg: p.RailBG}})
	}
	if l.focus != nil {
		chip := []seg{
			{text: l.focus.glyph, fg: l.focus.colour, bg: p.RailFocusBG},
			{text: " " + l.focus.id, fg: p.RailText, bg: p.RailFocusBG, bold: true},
		}
		if l.showZoom {
			chip = append(chip, seg{text: " Z", fg: p.BarAccent, bg: p.RailFocusBG, bold: true})
		}
		if l.showName && l.focus.name != "" {
			chip = append(chip, seg{text: " " + l.focus.name, fg: p.RailDim, bg: p.RailFocusBG})
		}
		if l.showStat && l.focus.status != "" {
			chip = append(chip, seg{text: " " + l.focus.status, fg: p.RailDim, bg: p.RailFocusBG})
		}
		out = append(out, chip)
	}
	return out
}

// fit elides the left block to budget cells in the ladder the footer uses:
// window names, then trailing window chips, then the workspace name, then the
// session name, then the focus chip's status word and name. The focus chip
// keeps its glyph and id last, and the result never exceeds the budget.
func (l *barLeft) fit(th Theme, budget int) []seg {
	if budget <= 0 {
		return nil
	}
	gap := seg{text: " ", fg: th.Palette.RailDim, bg: th.Palette.RailBG}
	cur := func() []seg { return join(l.groups(th), gap) }
	if segs := cur(); segCells(segs) <= budget {
		return segs
	}

	// 1. Window chips lose their name and keep the number.
	l.winNames = false
	if segs := cur(); segCells(segs) <= budget {
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
		if segs := cur(); segCells(segs) <= budget {
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
		avail := budget - (segCells(cur()) - cells(*name))
		if avail < 1 {
			avail = 1
		}
		*name = elide(*name, avail)
		if segs := cur(); segCells(segs) <= budget {
			return segs
		}
	}

	// 5. The focus chip drops its status word, then the zoom mark, then its
	// name. The glyph and the id are last, and the clip below cuts them.
	l.showStat = false
	if segs := cur(); segCells(segs) <= budget {
		return segs
	}
	l.showZoom = false
	if segs := cur(); segCells(segs) <= budget {
		return segs
	}
	l.showName = false
	if segs := cur(); segCells(segs) <= budget {
		return segs
	}

	// 6. Only the glyph and the id are left; a line too narrow for that is cut
	// run by run. The first run the room cannot hold ends the line, so no later
	// run paints out of order.
	return clipSegs(cur(), budget)
}

// barPinned is the right block: the prefix flag while armed, then the scroll
// info. Both are anchored to the last column, so both are pinned.
func barPinned(th Theme, armed bool, info string) []seg {
	p := th.Palette
	var out []seg
	if armed {
		out = append(out, seg{text: "PREFIX ", fg: p.RailText, bg: p.RailBG, bold: true, pin: true})
	}
	if info != "" {
		out = append(out, seg{text: info, fg: p.RailDim, bg: p.RailBG, pin: true})
	}
	return out
}

// barFitPinned fits the right block to cols cells on its own. A pinned segment
// goes whole, leftmost first, and the survivor is clipped with an ellipsis.
func barFitPinned(segs []seg, cols int) []seg {
	for segCells(segs) > cols {
		if len(segs) > 1 {
			segs = segs[1:]
			continue
		}
		if len(segs) == 0 {
			break
		}
		segs[0].text = elide(segs[0].text, cols)
		break
	}
	return segs
}

// barSegments returns the status bar as spans in paint order: the left block,
// then the pinned right block. It sizes the left block against the room the
// pinned block leaves, minus one gutter cell, so the blocks never touch and the
// line never runs past snap.Cols cells. The pinned block is never dropped to
// make room for the left one.
func barSegments(th Theme, snap *Snapshot, armed bool, info string) []seg {
	if snap == nil || snap.Cols <= 0 {
		return nil
	}
	right := barFitPinned(barPinned(th, armed, info), snap.Cols)
	budget := snap.Cols - segCells(right)
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

// drawToasts paints the toast stack in the bottom-right corner, just above the
// status bar: newest at the bottom, each toast one row inside a dim border. The
// box is as wide as the longest text plus its two border cells, clamped to the
// screen; a longer text is truncated to fit. Only rows above the bar are used,
// so the bar and the pointer gestures over the panes are untouched.
func drawToasts(buf *bytes.Buffer, th Theme, snap *Snapshot, toasts []toast) {
	if snap == nil || len(toasts) == 0 || snap.Cols < 3 || snap.Rows < 2 {
		return
	}
	longest := 0
	for _, x := range toasts {
		if n := cells(x.text); n > longest {
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
		fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m\x1b[90m%s\x1b[97m%s\x1b[0m\x1b[90m%s\x1b[0m",
			row, snap.Cols-w+1, th.Glyphs.BorderVertical, pad(x.text, inner), th.Glyphs.BorderVertical)
	}
}

// drawSessions paints the centred session picker. The panel owns the border, the
// title row and the interior clip; the picker owns its rows, the selection
// highlight, the tee row that crosses the border and the key hint under it. The
// title row is a fill row: dim, and its trailing cells are spaces rather than
// the border rune, so the row is one write and its cells are the picker's.
func drawSessions(buf *bytes.Buffer, th Theme, cols, rows int, sessions []SessionInfo, sel int, current string) {
	g := th.Glyphs
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
	inner := Rect{X: x + 1, Y: y + 1, W: w - 2, H: len(sessions) + 2}
	paintPanel(buf, th, inner, panelTitle{
		text:  g.BorderHorizontal + " switch session ",
		style: "\x1b[0m\x1b[90m",
		fill:  " ",
	}, "", false, func(b *bytes.Buffer, r Rect) {
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
			colour := "\x1b[0m\x1b[90m"
			if i == sel {
				colour = "\x1b[0m\x1b[97m"
			}
			line := pad(mark+" "+pad(s.Name, 18)+" "+pad(state, 4)+" "+pad(now, 3), r.W)
			fmt.Fprintf(b, "\x1b[%d;%dH%s%s", r.Y+i+1, r.X+1, colour, line)
		}
		hint := pad(" j/k move  enter switch  esc cancel", r.W)
		fmt.Fprintf(b, "\x1b[%d;%dH\x1b[0m\x1b[90m%s", r.Y+len(sessions)+2, r.X+1, hint)
	})
	fmt.Fprintf(buf, "\x1b[%d;%dH%s%s%s", y+len(sessions)+2, x+1,
		g.BorderTeeLeft, strings.Repeat(g.BorderHorizontal, w-2), g.BorderTeeRight)
	buf.WriteString("\x1b[0m")
}

// renamePromptWidth is the inner width the rename float asks for. A narrower
// pane area wins, so the box grows only as far as the cells allow.
const renamePromptWidth = 40

// renameBoxGeometry places the rename prompt's float over the pane area. The
// interior is one input row; a border row sits above and below it. The box is
// centred over the pane area and clamped clear of the rail, the toast row and
// the status bar, so both borders own a row and no band under it is covered.
// It reports false when the screen cannot hold the box.
func renameBoxGeometry(cols, rows, rail int) (Rect, bool) {
	if cols < 1 || rows < 5 {
		return Rect{}, false
	}
	rail = effectiveRail(cols, rail)
	avail := cols - rail - 2
	if avail < 3 {
		return Rect{}, false
	}
	w := renamePromptWidth
	if w > avail {
		w = avail
	}
	x := rail + (cols-rail-(w+2))/2
	if x < rail {
		x = rail
	}
	if x+w+2 > cols {
		x = cols - w - 2
	}
	// The box is three rows: a top border, the input row and a bottom border.
	// The toast row holds rows-2 and the bar rows-1, so the bottom border stops
	// at rows-3.
	top := (rows - 4) / 2
	if top < 0 {
		top = 0
	}
	if top > rows-5 {
		top = rows - 5
	}
	return Rect{X: x + 1, Y: top + 1, W: w, H: 1}, true
}

// drawRename paints the rename prompt as a floating pane over the pane area.
// The title names the kind and the id, the hint names the keys that submit and
// cancel, and the one input row holds the label, the draft and the caret. The
// caret is fixed: the draft takes what is left and loses its head first, so a
// long name still shows its end and the caret stays on screen. The kind is
// optional: a call without it renames a workspace, as the frozen prompt test
// expects. A screen too small to hold the box paints nothing.
func drawRename(buf *bytes.Buffer, th Theme, snap *Snapshot, id, draft string, kind ...string) {
	if snap == nil {
		return
	}
	k := "workspace"
	if len(kind) > 0 && kind[0] != "" {
		k = kind[0]
	}
	inner, ok := renameBoxGeometry(snap.Cols, snap.Rows, snap.Rail)
	if !ok {
		return
	}
	f := &FloatingPane{
		ID:    "rename",
		Rect:  inner,
		Title: "rename " + k + " " + id,
		Hint:  "enter  esc",
		Paint: func(b *bytes.Buffer, r Rect) {
			renameInputRow(b, th, r, draft)
		},
	}
	f.Draw(buf, th, nil)
}

// renameInputRow paints the float's one input row: the dim label, the draft and
// the caret on the rail ground, padded to the full inner width so the row owns
// every cell inside the box. The draft keeps its tail and drops its head when
// it runs out of room, and a wide rune is cut whole.
func renameInputRow(buf *bytes.Buffer, th Theme, r Rect, draft string) {
	p := th.Palette
	const caret = "▏"
	labelRoom := r.W - cells(caret)
	if labelRoom < 0 {
		labelRoom = 0
	}
	segs := clipSegs([]seg{{text: " name: ", fg: p.RailDim}}, labelRoom)
	draftRoom := r.W - cells(caret) - segCells(segs)
	if cells(draft) > draftRoom {
		if draftRoom > 1 {
			draft = ellipsis + clipTail(draft, draftRoom-1)
		} else {
			draft = ""
		}
	}
	segs = append(segs,
		seg{text: draft, fg: p.RailText},
		seg{text: caret, fg: p.BarAccent},
	)
	fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m", r.Y+1, r.X+1)
	buf.WriteString(p.RailBG.bg())
	for _, seg := range segs {
		buf.WriteString(seg.fg.fg())
		buf.WriteString(seg.text)
	}
	if n := r.W - segCells(segs); n > 0 {
		buf.WriteString(p.RailBG.bg())
		buf.WriteString(strings.Repeat(" ", n))
	}
	buf.WriteString("\x1b[0m")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// drawHelp paints the bottom-right action preview from the configured keymap
// rows. The box sizes to its widest row and is painted by the floating pane
// primitive, so the top border carries the word prefix and the bottom border
// names the key that closes it. The box stays clear of the rail, the toast row
// and the status bar: it gives up columns, then its trailing rows, before it
// paints over any of them.
func drawHelp(buf *bytes.Buffer, th Theme, cols, rows int, keys []HelpEntry) {
	g, ok := helpBoxGeometry(cols, rows, keys)
	if !ok {
		return
	}
	f := &FloatingPane{
		Rect:  Rect{X: g.x, Y: g.y, W: g.inner, H: g.h},
		Title: "prefix",
		Hint:  "esc",
		Paint: func(b *bytes.Buffer, inner Rect) {
			helpBoxRows(b, inner, g)
		},
	}
	f.Draw(buf, th, nil)
}

// displayRow is one row of the help box: a category header or a key-action pair.
type displayRow struct {
	header string
	key    string
	action string
	color  string
}

// categorizeHelp groups the entries into sections, each with its own header and
// rows. A row's section is its tag when it has one, and the heuristic category
// otherwise. Sections appear in first-appearance order, so a row tagged into an
// earlier section joins it instead of opening a second header. A row keeps the
// table order inside its section.
func categorizeHelp(keys []HelpEntry) [][]displayRow {
	var categories [][]displayRow
	index := make(map[string]int, len(keys))
	for _, e := range keys {
		section := e.Section
		if section == "" {
			section = helpCategory(e.Action)
		}
		i, ok := index[section]
		if !ok {
			i = len(categories)
			index[section] = i
			categories = append(categories, []displayRow{{header: section, color: helpSectionColor(section)}})
		}
		categories[i] = append(categories[i], displayRow{key: e.Key, action: e.Action, color: helpSectionColor(section)})
	}
	return categories
}

// categorizeHelpRows flattens the categories into a single row list.
func categorizeHelpRows(keys []HelpEntry) []displayRow {
	cats := categorizeHelp(keys)
	var rows []displayRow
	for _, cat := range cats {
		rows = append(rows, cat...)
	}
	return rows
}

// helpCategory returns the category name for an action label.
func helpCategory(action string) string {
	switch {
	case strings.Contains(action, "split"):
		return "Layout"
	case strings.Contains(action, "pane") || strings.Contains(action, "scrollback"):
		return "Pane"
	case strings.Contains(action, "window") || strings.Contains(action, "win"):
		return "Window"
	case strings.Contains(action, "workspace") || strings.Contains(action, "session"):
		return "Session"
	default:
		return "Utility"
	}
}

var helpCategoryColors = map[string]string{
	"Layout":  "\x1b[96m", // cyan
	"Pane":    "\x1b[92m", // green
	"Window":  "\x1b[95m", // magenta
	"Session": "\x1b[93m", // yellow
	"Utility": "\x1b[94m", // blue
}

// helpSectionColor returns the colour a section paints. The five built-in names
// keep their own colour; any other name takes the Utility colour.
func helpSectionColor(section string) string {
	if color, ok := helpCategoryColors[section]; ok {
		return color
	}
	return helpCategoryColors["Utility"]
}

// helpColumn is one column of the help box: the key and action budgets and the
// rows it lists.
type helpColumn struct {
	keyW    int
	actionW int
	rows    []displayRow
}

// newHelpColumn sizes a column to its categories, flattened into rows.
func newHelpColumn(categories [][]displayRow) helpColumn {
	var rows []displayRow
	for _, cat := range categories {
		rows = append(rows, cat...)
	}
	keyW, actionW := 0, 0
	for _, r := range rows {
		if r.header == "" {
			keyW = maxInt(keyW, cells(r.key))
			actionW = maxInt(actionW, cells(r.action))
		}
	}
	if keyW < 1 {
		keyW = 1
	}
	return helpColumn{keyW: keyW, actionW: actionW, rows: rows}
}

// width is the cells the column paints.
func (c helpColumn) width() int {
	if c.actionW > 0 {
		return c.keyW + 2 + c.actionW
	}
	return c.keyW
}

// helpBoxLayout is the box drawHelp paints: the content origin and width, the
// number of rows that fit, and the columns it draws.
type helpBoxLayout struct {
	x, y    int
	inner   int
	h       int
	columns []helpColumn
}

// layoutHelpColumns picks the number of columns and the split of categories
// into them. It prefers more columns when they fit, and balances the heights.
func layoutHelpColumns(categories [][]displayRow, avail int) []helpColumn {
	n := len(categories)
	if n == 0 {
		return nil
	}
	maxCols := 4
	if n < maxCols {
		maxCols = n
	}
	for nCols := maxCols; nCols >= 1; nCols-- {
		cols := bestColumnSplit(categories, nCols, avail)
		if cols != nil {
			return cols
		}
	}
	return nil
}

// bestColumnSplit finds the split of categories into nCols columns that fits
// in avail cells and has the smallest maximum height. Ties go to the narrowest
// box.
func bestColumnSplit(categories [][]displayRow, nCols int, avail int) []helpColumn {
	n := len(categories)
	var best []helpColumn
	bestHeight := int(^uint(0) >> 1)
	bestWidth := 0

	var visit func(start, remaining int, current []helpColumn)
	visit = func(start, remaining int, current []helpColumn) {
		if remaining == 1 {
			all := make([]helpColumn, len(current)+1)
			copy(all, current)
			all[len(current)] = newHelpColumn(categories[start:])
			w := 0
			h := 0
			for i, c := range all {
				if i > 0 {
					w += 3
				}
				w += c.width()
				h = maxInt(h, len(c.rows))
			}
			if w <= avail {
				if h < bestHeight || (h == bestHeight && w < bestWidth) {
					bestHeight = h
					bestWidth = w
					best = all
				}
			}
			return
		}
		for i := start; i <= n-remaining; i++ {
			c := newHelpColumn(categories[start : i+1])
			visit(i+1, remaining-1, append(current, c))
		}
	}
	visit(0, nCols, nil)
	return best
}

// helpBoxGeometry sizes the box to its widest row and clamps it into the cells
// no other band owns. The fit is X-1 >= rail, X+width-1 <= cols-1, Y-1 >= 0 and
// Y+h <= rows-3, so both borders own a row and the toast row and the bar stay
// clear. It reports false when the screen cannot hold even a key column.
func helpBoxGeometry(cols, rows int, keys []HelpEntry) (helpBoxLayout, bool) {
	var g helpBoxLayout
	if cols < 3 || rows < 5 || len(keys) == 0 {
		return g, false
	}
	categories := categorizeHelp(keys)
	left := maxRail(cols) + 1
	avail := cols - left
	if avail < 3 {
		return g, false
	}
	helpCols := layoutHelpColumns(categories, avail-2)
	if helpCols == nil {
		// A narrow screen keeps the key column alone.
		col := newHelpColumn(categories)
		col.actionW = 0
		if room := avail - 2; col.keyW > room {
			col.keyW = room
		}
		if col.keyW < 1 {
			return g, false
		}
		helpCols = []helpColumn{col}
	}
	inner := 0
	h := 0
	for i, c := range helpCols {
		if i > 0 {
			inner += 3
		}
		inner += c.width()
		h = maxInt(h, len(c.rows))
	}
	if inner < 1 || inner+2 > avail {
		return g, false
	}
	width := inner + 2
	x := cols - width - 2
	if x < left {
		x = left
	}
	if h > rows-4 {
		h = rows - 4
	}
	if h < 1 {
		return g, false
	}
	y := rows - (h + 2) - 2
	if y < 1 {
		y = 1
	}
	if y+h > rows-3 {
		return g, false
	}
	return helpBoxLayout{x: x, y: y, inner: inner, h: h, columns: helpCols}, true
}

// helpBoxRows paints every content row of the box. Each row starts with an
// absolute cursor move and writes exactly its column's cells, so it neither
// keeps a glyph the frame before left nor paints past the border. A header is
// a coloured line, a key is bright and an action label is dim. Columns are
// separated by a dim vertical bar.
func helpBoxRows(buf *bytes.Buffer, inner Rect, g helpBoxLayout) {
	xOff := 0
	for ci, col := range g.columns {
		if ci > 0 {
			for i := 0; i < g.h; i++ {
				y := inner.Y + i
				fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m\x1b[90m \u2502 \x1b[0m", y+1, inner.X+xOff+1)
			}
			xOff += 3
		}
		maxRows := g.h
		if len(col.rows) < maxRows {
			maxRows = len(col.rows)
		}
		for i, r := range col.rows[:maxRows] {
			y := inner.Y + i
			x := inner.X + xOff
			if r.header != "" {
				color := r.color
				if color == "" {
					color = "\x1b[1m"
				}
				header := pad(elide(r.header, col.width()), col.width())
				fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m%s%s\x1b[0m", y+1, x+1, color, header)
				continue
			}
			color := r.color
			if color == "" {
				color = "\x1b[97m"
			}
			if col.actionW > 0 {
				fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, x+1)
				helpBoxKey(buf, r.key, color, col.keyW)
				buf.WriteString("  \x1b[90m")
				buf.WriteString(pad(elide(r.action, col.actionW), col.actionW))
				buf.WriteString("\x1b[0m")
				continue
			}
			fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, x+1)
			helpBoxKey(buf, r.key, color, col.width())
			buf.WriteString("\x1b[0m")
		}
		for i := maxRows; i < g.h; i++ {
			y := inner.Y + i
			x := inner.X + xOff
			fmt.Fprintf(buf, "\x1b[%d;%dH\x1b[0m%s\x1b[0m", y+1, x+1, strings.Repeat(" ", col.width()))
		}
		xOff += col.width()
	}
}

// helpBoxKey paints a key label with each alternative in the category colour
// and the " / " separator plain, so a literal slash reads as a key and a slash
// between keys reads as a separator. It pads the result to budget cells.
func helpBoxKey(buf *bytes.Buffer, key, color string, budget int) {
	key = elide(key, budget)
	parts := strings.Split(key, " / ")
	used := 0
	for i, part := range parts {
		if i > 0 {
			buf.WriteString("\x1b[0m / ")
			used += 3
		}
		buf.WriteString(color)
		buf.WriteString("\x1b[1m")
		buf.WriteString(part)
		used += cells(part)
	}
	buf.WriteString("\x1b[0m")
	if n := budget - used; n > 0 {
		buf.WriteString(strings.Repeat(" ", n))
	}
}
