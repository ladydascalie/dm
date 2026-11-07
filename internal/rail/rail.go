package rail

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"dm/internal/layout"
	"dm/internal/segment"
	"dm/internal/theme"
	"dm/internal/wire"
	"dm/internal/wlabel"
)

// RailKind is the row type the sidebar paints: a workspace row, a window line,
// that window's dim second line, a pane, or the gap between workspace blocks.
type RailKind int

const (
	Workspace RailKind = iota
	Window
	WindowNote
	Pane
	Gap
)

// RailItem is one sidebar row. It exists so drawing and hit-testing share the
// same grouping.
type RailItem struct {
	Kind       RailKind
	Text       string
	Label      string
	Workspace  string
	Folder     string
	Cwd        string
	Branch     string
	Worktree   string
	Message    string
	Pane       string
	PaneName   string
	Window     string
	WindowName string
	Status     string
	Count      int
	Added      int
	Deleted    int
	Accent     int // an accent slot, resolved against the theme by the painter
	Agent      bool
	Dirty      bool
	InRepo     bool
	Folded     bool
	Focus      bool
	Active     bool
}

// WindowSource returns the pane a window's label and git context come from:
// the focused pane when it carries a branch, else the first pane in the window
// that carries a branch, else the focused pane, else the window's first pane.
func WindowSource(snap *wire.Snapshot, window string) (wire.AllPane, bool) {
	var focus, first, withBranch *wire.AllPane
	for i := range snap.All {
		p := &snap.All[i]
		if p.Window != window {
			continue
		}
		if first == nil {
			first = p
		}
		if p.Focus {
			focus = p
		}
		if p.Branch != "" && withBranch == nil {
			withBranch = p
		}
	}
	if focus != nil && focus.Branch != "" {
		return *focus, true
	}
	if withBranch != nil {
		return *withBranch, true
	}
	if focus != nil {
		return *focus, true
	}
	if first != nil {
		return *first, true
	}
	return wire.AllPane{}, false
}

// WindowVars returns the label template variables for a window: its id and
// name, and the source pane's working directory and repository root.
func WindowVars(snap *wire.Snapshot, w wire.WindowInfo) wlabel.Vars {
	v := wlabel.Vars{ID: w.ID, Name: w.Name, Named: w.Named}
	if p, ok := WindowSource(snap, w.ID); ok {
		v.Cwd, v.Root = p.Cwd, p.Root
	}
	return v
}

// RailItems flattens a snapshot into sidebar rows in the server's order: a
// workspace row, then per window a Window row, that window's note row when it
// has a message and is unfolded, and its pane rows when it is unfolded. A gap
// row separates workspace blocks. A folded workspace hides its windows and
// panes; a folded window hides only its panes. The thin rail is a glyph
// column: one pane row per pane, in snapshot order, whatever the fold. It has
// no workspace row, no window row, no note row and no gap row.
func RailItems(snap *wire.Snapshot) []RailItem {
	if snap == nil {
		return nil
	}
	if layout.RailCompact(snap.Rail) {
		out := make([]RailItem, 0, len(snap.All))
		for _, p := range snap.All {
			out = append(out, RailPaneRow(p))
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
	source := func(window string) (wire.AllPane, bool) {
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
		return wire.AllPane{}, false
	}
	// A workspace keeps its preferred slot when it is free. When two live
	// workspaces hash alike, the later one takes the next free slot, so no two
	// workspaces in a session ever share an accent.
	used := map[int]bool{}
	slots := len(theme.DefaultTheme().Accents)
	accent := func(id string) int {
		pref := RailAccentSlot(id)
		for i := 0; i < slots; i++ {
			slot := (pref + i) % slots
			if !used[slot] {
				used[slot] = true
				return slot
			}
		}
		return pref
	}
	var out []RailItem
	ws, win := "", ""
	for _, p := range snap.All {
		if p.Workspace != ws {
			if ws != "" {
				out = append(out, RailItem{Kind: Gap})
			}
			ws, win = p.Workspace, ""
			name := p.WorkspaceName
			if name == "" {
				name = p.Workspace
			}
			out = append(out, RailItem{
				Kind: Workspace, Text: name, Workspace: p.Workspace,
				Count: panes[p.Workspace], Accent: accent(p.Workspace),
				Active: p.WorkspaceActive,
				Folded: !p.WorkspaceUnfolded,
			})
		}
		// A folded workspace hides its windows and their panes.
		if !p.WorkspaceUnfolded {
			continue
		}
		if p.Window != win {
			win = p.Window
			item := RailItem{
				Kind: Window, Text: p.WindowName, Window: p.Window,
				WindowName: p.WindowName, Workspace: p.Workspace,
				Folded: !p.WindowUnfolded,
			}
			if src, ok := source(p.Window); ok {
				item.Folder = RailFolder(src.Cwd, src.Root)
				item.Cwd = src.Cwd
				item.Branch = src.Branch
				item.Worktree = src.Worktree
				item.InRepo = src.InRepo
				item.Dirty = src.Dirty
				item.Added = src.Added
				item.Deleted = src.Deleted
				item.Message = src.Message
			}
			item.Label = wlabel.Render(snap.WindowLabel, WindowVars(snap, wire.WindowInfo{ID: p.Window, Name: p.WindowName, Named: p.WindowNamed}))
			out = append(out, item)
			if item.Message != "" && p.WindowUnfolded {
				out = append(out, RailItem{
					Kind: WindowNote, Text: item.Message, Window: p.Window,
					WindowName: p.WindowName, Workspace: p.Workspace,
					Folder: item.Folder, Cwd: item.Cwd,
				})
			}
		}
		// A folded window hides only its panes.
		if !p.WindowUnfolded {
			continue
		}
		out = append(out, RailPaneRow(p))
	}
	return out
}

// RailPaneRow is the pane row both rails draw. The label falls back from the
// pane name to its app to its id.
func RailPaneRow(p wire.AllPane) RailItem {
	label := p.Name
	if label == "" {
		label = p.App
	}
	if label == "" {
		label = p.ID
	}
	return RailItem{
		Kind: Pane, Text: label, Pane: p.ID, PaneName: p.Name,
		Window: p.Window, WindowName: p.WindowName, Workspace: p.Workspace,
		Status: p.Status, Agent: p.Agent, Focus: p.Focus,
	}
}

// RailFolder is the folder a window line shows for a pane: its path below the
// repository root, the folder's own name when the pane sits at the root, and
// the folder name alone outside git, so every pane still names a folder.
// folderName is the default window label for a directory: its last path
// element, with the filesystem root spelled out. It mirrors paths.FolderName,
// which rail cannot import without pulling the session filesystem into the
// paint layer.
func folderName(dir string) string {
	if dir == "" {
		return ""
	}
	dir = filepath.Clean(dir)
	base := filepath.Base(dir)
	if base == "/" || base == "." || base == string(filepath.Separator) {
		return "root"
	}
	return base
}

func RailFolder(cwd, root string) string {
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

// RailAccentSlot returns the accent slot a workspace prefers. FNV-1a is the
// seed; the mix step matters because its low bits repeat for ids of equal
// length, and a workspace id is short.
func RailAccentSlot(workspace string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(workspace))
	x := h.Sum32()
	x ^= x >> 16
	x *= 0x7feb352d
	x ^= x >> 15
	x *= 0x846ca68b
	x ^= x >> 16
	return int(x % uint32(len(theme.DefaultTheme().Accents)))
}

// Nerd Font icons. The font check runs once; DM_NERD=1 or 0 overrides it.
var (
	nerdOnce sync.Once
	nerdOn   bool
)

func NerdFonts() bool {
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

// StateStyle returns the mark for a status. A status the theme does not name
// falls back to the unknown style.
func StateStyle(th theme.Theme, status string) theme.StateStyle {
	if s, ok := th.States[status]; ok {
		return s
	}
	if s, ok := th.States["unknown"]; ok {
		return s
	}
	return theme.DefaultTheme().States["unknown"]
}

// RailGlyph is the state mark for a pane row and its colour: amber working,
// teal-blue idle, red blocked, green done, pink-red error, dim grey unknown.
// Every state gets a distinct icon or, without Nerd Fonts, a distinct Unicode
// glyph. The question mark marks an agent whose state has not arrived; DrawRail
// leaves the cell blank for a shell, which reports no state. A theme's glyph
// replaces both forms; an empty NerdGlyph keeps the Unicode glyph.
func RailGlyph(th theme.Theme, status string, nerd bool) (glyph string, colour theme.RGB) {
	style := StateStyle(th, status)
	glyph = style.Glyph
	if nerd && style.NerdGlyph != "" {
		glyph = style.NerdGlyph
	}
	return glyph, style.Colour
}

// RailMark is the two-cell prefix of a pane row: an agent icon, a shell icon,
// or the fallback agent bar. The status glyph lands in column 3 either way.
// Every icon here is from the Font Awesome 4 range that even old Nerd Font
// patches carry; newer code points such as the FA5 robot render as tofu.
func RailMark(agent, nerd bool) string {
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

// WriteRailSeg paints one run on the rail's row ground. The rail carries one
// background and one weight per row, so they arrive from the caller and ride
// the run only for the length of this write. The switcher's rail rows call it
// too.
func WriteRailSeg(buf *bytes.Buffer, s segment.Seg, ground theme.RGB, bold bool) {
	s.BG = ground
	s.Bold = s.Bold || bold
	segment.Write(buf, s)
}

// RailStats is the right-aligned git tail of a window line: an amber dot when
// the tree is dirty, then the added and deleted line counts, or "clean".
func RailStats(th theme.Theme, it RailItem) []segment.Seg {
	if !it.InRepo {
		return nil
	}
	p := th.Palette
	var segs []segment.Seg
	if it.Dirty {
		segs = append(segs, segment.Seg{Text: th.Glyphs.Dirty, FG: p.RailWorking})
	}
	if it.Added > 0 {
		segs = append(segs, segment.Seg{Text: "+" + strconv.Itoa(it.Added), FG: p.RailDiffAdd})
	}
	if it.Deleted > 0 {
		segs = append(segs, segment.Seg{Text: "-" + strconv.Itoa(it.Deleted), FG: p.RailDiffDel})
	}
	if len(segs) == 0 {
		return []segment.Seg{{Text: "clean", FG: p.RailDim}}
	}
	out := make([]segment.Seg, 0, len(segs)*2)
	for i, s := range segs {
		if i > 0 {
			out = append(out, segment.Seg{Text: " ", FG: p.RailDim})
		}
		out = append(out, s)
	}
	return out
}

// RailRow composes one sidebar row at width w. left is the row's own run,
// right the run pinned to the right edge and tail the run that follows left
// and yields first. compact and nerd are the rail's own switches.
func RailRow(th theme.Theme, it RailItem, w int, compact, nerd bool) (left, right, tail []segment.Seg) {
	p, g := th.Palette, th.Glyphs
	switch it.Kind {
	case Workspace:
		if !compact {
			name := th.Accents[it.Accent]
			if !it.Active {
				name = p.RailHeader
			}
			caret := g.FoldOpen
			if it.Folded {
				caret = g.FoldClosed
			}
			if nerd {
				caret = g.NerdFoldOpen
				if it.Folded {
					caret = g.NerdFoldClosed
				}
			}
			left = []segment.Seg{
				{Text: caret, FG: th.Accents[it.Accent]},
				{Text: " ", FG: name},
				{Text: it.Text, FG: name, Bold: true},
			}
			if mark := th.RailMarks.Workspace; mark != "" {
				left = append(left, segment.Seg{Text: " " + mark, FG: name})
			}
			right = []segment.Seg{{Text: strconv.Itoa(it.Count), FG: p.RailDim}}
		}
	case Window:
		base := p.RailWindow
		if !compact {
			caret := g.FoldOpen
			if it.Folded {
				caret = g.FoldClosed
			}
			if nerd {
				caret = g.NerdFoldOpen
				if it.Folded {
					caret = g.NerdFoldClosed
				}
			}
			// The label template names the window. A window with no rendered
			// directory falls back to the window name.
			label := it.Label
			if label == "" {
				label = it.Folder
			}
			if label == "" {
				label = it.Text
			}
			left = []segment.Seg{
				{Text: "  ", FG: base},
				{Text: caret, FG: base},
				{Text: " ", FG: base},
				{Text: label, FG: base},
			}
			// The branch trails the folder in dim grey, and only when it says
			// something the label does not.
			if it.Branch != "" && it.Branch != label {
				tail = []segment.Seg{
					{Text: "  ", FG: p.RailDim},
					{Text: it.Branch, FG: p.RailDim},
				}
			}
			right = RailStats(th, it)
		}
	case WindowNote:
		if !compact {
			base := p.RailDim
			left = []segment.Seg{
				{Text: "    ", FG: base},
				{Text: it.Text, FG: base},
			}
		}
	case Pane:
		base := p.RailDim
		if it.Focus {
			base = p.RailText
		}
		mark, colour := RailGlyph(th, it.Status, nerd)
		// A shell has no state to report, so the fallback mark would be a
		// question about nothing. Leave the cell blank instead.
		if !it.Agent && (it.Status == "" || it.Status == "unknown") {
			mark = " "
		}
		glyph := RailMark(it.Agent, nerd)
		if compact {
			left = []segment.Seg{
				{Text: glyph, FG: colour},
				{Text: mark, FG: colour},
			}
		} else {
			left = []segment.Seg{
				{Text: "      ", FG: base},
				{Text: glyph, FG: colour},
				{Text: mark, FG: colour},
				{Text: " " + it.Text, FG: base},
			}
			// The dot is a level marker, not part of the name. It only joins the
			// row when the row has room for it, so a narrow rail never loses the
			// pane name to the marker.
			if pm := th.RailMarks.Pane; pm != "" && segment.SegCells(left)+2 <= w {
				left = append(left, segment.Seg{Text: " " + pm, FG: p.RailDim})
			}
		}
	case Gap:
		// The rail background and nothing else.
	}
	rightCells := segment.SegCells(right)
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
	tailCells := segment.SegCells(tail)
	leftCells := segment.SegCells(left) + tailCells
	if tailCells > 0 && leftCells > budget {
		tail, leftCells = nil, segment.SegCells(left)
	}
	if leftCells > budget {
		left = segment.ElideSegs(th, left, budget)
	}
	return left, right, tail
}

// RailPaneAt returns the pane whose row is under x,y, or "" for any other row,
// an empty row, a point outside the rail, or the rail's bottom control row.
func RailPaneAt(snap *wire.Snapshot, x, y int) string {
	if snap == nil || snap.Rail <= 0 {
		return ""
	}
	if x < 0 || x >= snap.Rail || y < 0 || y >= snap.Rows-2 {
		return ""
	}
	items := RailItems(snap)
	if y >= len(items) || items[y].Kind != Pane {
		return ""
	}
	return items[y].Pane
}

// RailWindowAt returns the window whose row is under x,y, for a focus click. A
// window line and its note line both answer. The thin rail has no window row,
// so it never answers.
func RailWindowAt(snap *wire.Snapshot, x, y int) string {
	if snap == nil || snap.Rail <= 0 {
		return ""
	}
	if x < 0 || x >= snap.Rail || y < 0 || y >= snap.Rows-2 {
		return ""
	}
	if layout.RailCompact(snap.Rail) {
		return ""
	}
	items := RailItems(snap)
	if y >= len(items) {
		return ""
	}
	if k := items[y].Kind; k != Window && k != WindowNote {
		return ""
	}
	return items[y].Window
}

// RailWorkspaceAt returns the workspace whose row is under x,y. The thin rail
// has no workspace row, so it never answers.
func RailWorkspaceAt(snap *wire.Snapshot, x, y int) string {
	if snap == nil || snap.Rail <= 0 {
		return ""
	}
	if x < 0 || x >= snap.Rail || y < 0 || y >= snap.Rows-2 {
		return ""
	}
	if layout.RailCompact(snap.Rail) {
		return ""
	}
	items := RailItems(snap)
	if y >= len(items) {
		return ""
	}
	if items[y].Kind != Workspace {
		return ""
	}
	return items[y].Workspace
}

// RailFoldAt returns the workspace or window whose fold caret is under x,y. A
// workspace caret sits in column 0 and a window caret in column 2. It only
// answers for the caret cell, so a click on the row's name still focuses the
// row's first pane.
func RailFoldAt(snap *wire.Snapshot, x, y int) string {
	if snap == nil || snap.Rail <= 0 {
		return ""
	}
	if x < 0 || x >= snap.Rail || y < 0 || y >= snap.Rows-2 {
		return ""
	}
	if layout.RailCompact(snap.Rail) {
		return ""
	}
	items := RailItems(snap)
	if y >= len(items) {
		return ""
	}
	it := items[y]
	switch it.Kind {
	case Workspace:
		if x != 0 {
			return ""
		}
		return it.Workspace
	case Window:
		if x != 2 {
			return ""
		}
		return it.Window
	}
	return ""
}

// RailItemAt returns the rail row under x,y. A point outside the rail, on the
// bottom control row, or past the last item reports false. The gap row between
// workspace blocks is a row, so it answers true.
func RailItemAt(snap *wire.Snapshot, x, y int) (RailItem, bool) {
	if snap == nil || snap.Rail <= 0 {
		return RailItem{}, false
	}
	if x < 0 || x >= snap.Rail || y < 0 || y >= snap.Rows-2 {
		return RailItem{}, false
	}
	items := RailItems(snap)
	if y >= len(items) {
		return RailItem{}, false
	}
	return items[y], true
}

// RailControlAt reports whether x,y is the rail's bottom control row, whose
// click toggles the rail between full and thin. The whole row inside the rail
// is the target, so the row belongs to the control and to no item.
func RailControlAt(snap *wire.Snapshot, x, y int) bool {
	if snap == nil || snap.Rail <= 0 || snap.Rows < 2 {
		return false
	}
	return x >= 0 && x < snap.Rail && y == snap.Rows-2
}

// RailControlSegs is the left run of the control row: the move a click makes.
// A rail wider than the thin one names the move and collapses. The thin rail
// shows the expand caret in the state glyph's column and leaves the rest
// blank, so a narrow rail loses no pane row to a label.
func RailControlSegs(th theme.Theme, compact, nerd bool) []segment.Seg {
	if compact {
		caret := "▶"
		if nerd {
			caret = "\uf0da"
		}
		return []segment.Seg{
			{Text: "  ", FG: th.Palette.RailDim},
			{Text: caret, FG: th.Palette.RailDim},
		}
	}
	mark := "◀ "
	if nerd {
		mark = "\uf0d9 "
	}
	return []segment.Seg{
		{Text: mark, FG: th.Palette.RailDim},
		{Text: "collapse", FG: th.Palette.RailDim},
	}
}

// DrawRail paints the sidebar above the status bar. The rail paints its own
// background so contrast does not depend on the terminal theme. Rows past the
// screen are clipped, and every row is written into at most Rail columns. A
// left run is elided with an ellipsis before the right-aligned stats give up a
// cell, so the stats survive a narrow rail.
func DrawRail(buf *bytes.Buffer, th theme.Theme, snap *wire.Snapshot) {
	if snap == nil || snap.Rail <= 0 || snap.Cols < 1 || snap.Rows < 3 {
		return
	}
	p := th.Palette
	w := snap.Rail
	rows := snap.Rows - 1
	items := RailItems(snap)
	nerd := NerdFonts()
	compact := layout.RailCompact(snap.Rail)
	// The status bar holds Rows-1 and the control holds Rows-2, so items draw
	// in rows 0..Rows-3 and clip there. Nothing overwrites the control row.
	controlAt := snap.Rows - 2
	gripAt := controlAt / 2
	for y := 0; y < rows; y++ {
		var left, right, tail []segment.Seg
		bg, bold := p.RailBG, false
		rowW := w
		if y < controlAt && y == gripAt {
			rowW = w - 1
		}
		if y == controlAt {
			left = RailControlSegs(th, compact, nerd)
			if segment.SegCells(left) > rowW {
				left = segment.ElideSegs(th, left, rowW)
			}
		} else if y < len(items) {
			it := items[y]
			left, right, tail = RailRow(th, it, rowW, compact, nerd)
			// A focused pane row paints its own background and every run bold.
			if it.Kind == Pane && it.Focus {
				bg, bold = p.RailFocusBG, true
			}
		}
		leftCells := segment.SegCells(left) + segment.SegCells(tail)
		rightCells := segment.SegCells(right)
		fmt.Fprintf(buf, "\x1b[%d;1H", y+1)
		buf.WriteString("\x1b[0m" + bg.BG())
		for _, s := range left {
			WriteRailSeg(buf, s, bg, bold)
		}
		for _, s := range tail {
			WriteRailSeg(buf, s, bg, bold)
		}
		if n := rowW - leftCells - rightCells; n > 0 {
			buf.WriteString(strings.Repeat(" ", n))
		}
		for _, s := range right {
			WriteRailSeg(buf, s, bg, bold)
		}
		if y < controlAt && y == gripAt {
			fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, w)
			buf.WriteString("\x1b[0m" + p.RailBG.BG() + p.RailDim.FG() + th.Glyphs.Grip)
		}
		buf.WriteString("\x1b[0m")
	}
}
