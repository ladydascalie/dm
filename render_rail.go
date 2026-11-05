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
)

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
