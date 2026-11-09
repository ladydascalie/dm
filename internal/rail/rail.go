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

// RailKind is the row type the sidebar paints: a project header, a workspace
// row, a window line, that window's dim second line, a pane, or the gap
// between workspace blocks.
type RailKind int

const (
	Project RailKind = iota
	Workspace
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
	// Project is the project the row belongs to, empty outside a project.
	// ProjectAccent is that project's accent slot, set on every row of the
	// group so the painter can tie them with one bar.
	Project       string
	ProjectAccent int
	Agent         bool
	Dirty         bool
	InRepo        bool
	Folded        bool
	Focus         bool
	Active        bool
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
	// A slot is preferred by hashing its name. When two live names hash alike,
	// the later one takes the next free slot, so no two names in a session ever
	// share an accent.
	slots := len(theme.DefaultTheme().Accents)
	allocator := func() func(string) int {
		used := map[int]bool{}
		return func(name string) int {
			pref := RailAccentSlot(name)
			for i := 0; i < slots; i++ {
				slot := (pref + i) % slots
				if !used[slot] {
					used[slot] = true
					return slot
				}
			}
			return pref
		}
	}
	// Workspaces keep their old per-workspace slot for the callers that still
	// read it, though the rail no longer paints it. Projects own their column.
	accent := allocator()
	projectAccent := allocator()
	// A project row names its group once, before the group's first workspace,
	// and carries the number of workspaces in the group.
	projectCount := map[string]int{}
	seen := map[string]bool{}
	slotsFor := map[string]int{}
	for _, p := range snap.All {
		if seen[p.Workspace] {
			continue
		}
		seen[p.Workspace] = true
		if p.Project != "" {
			projectCount[p.Project]++
		}
	}
	var out []RailItem
	ws, win, project, prev := "", "", "", ""
	slot := 0
	for _, p := range snap.All {
		if p.Workspace != ws {
			if ws != "" {
				gap := RailItem{Kind: Gap}
				// A gap between two workspaces of one project stays inside the
				// group, so the bar runs on through it.
				if project != "" && p.Project == project {
					gap.Project, gap.ProjectAccent = project, slot
				}
				out = append(out, gap)
			}
			ws, win = p.Workspace, ""
			project, prev, slot = p.Project, project, 0
			if project != "" {
				s, ok := slotsFor[project]
				if !ok {
					s = projectAccent(project)
					slotsFor[project] = s
				}
				slot = s
				if project != prev {
					out = append(out, RailItem{
						Kind: Project, Text: project, Project: project,
						ProjectAccent: slot, Count: projectCount[project],
					})
				}
			}
			name := p.WorkspaceName
			if name == "" {
				name = p.Workspace
			}
			out = append(out, RailItem{
				Kind: Workspace, Text: name, Workspace: p.Workspace,
				Count: panes[p.Workspace], Accent: accent(p.Workspace),
				Project: project, ProjectAccent: slot,
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
				Project: project, ProjectAccent: slot,
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
					Project: project, ProjectAccent: slot,
					Folder: item.Folder, Cwd: item.Cwd,
				})
			}
		}
		// A folded window hides only its panes.
		if !p.WindowUnfolded {
			continue
		}
		row := RailPaneRow(p)
		row.Project, row.ProjectAccent = project, slot
		out = append(out, row)
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

// RailAccentSlot returns the accent slot a name prefers. FNV-1a is the seed;
// the mix step matters because its low bits repeat for names of equal length,
// and both a workspace id and a project name are short.
func RailAccentSlot(name string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
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

// ResolveGlyphs turns an auto set into the set the terminal can show: Nerd
// Font glyphs when the font is present, Unicode otherwise. Any other set is
// returned as it is.
func ResolveGlyphs(set theme.GlyphSet) theme.GlyphSet {
	if set != theme.GlyphAuto {
		return set
	}
	if NerdFonts() {
		return theme.GlyphNerd
	}
	return theme.GlyphUnicode
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
func RailGlyph(th theme.Theme, status string, set theme.GlyphSet) (glyph string, colour theme.RGB) {
	style := StateStyle(th, status).For(set)
	return style.Glyph, style.Colour
}

// RailMark is the two-cell prefix of a pane row: an agent icon, a shell icon,
// or the fallback agent bar. The status glyph lands in column 3 either way.
// Every icon here is from the Font Awesome 4 range that even old Nerd Font
// patches carry; newer code points such as the FA5 robot render as tofu.
func RailMark(agent bool, set theme.GlyphSet) string {
	switch {
	case agent && set == theme.GlyphNerd:
		return "\uf135 "
	case agent && set == theme.GlyphASCII:
		return "a "
	case agent:
		return "\u258e "
	case set == theme.GlyphNerd:
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
func RailStats(th theme.Theme, it RailItem, set theme.GlyphSet) []segment.Seg {
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

// RailBar is the cell the full rail paints in column 0 of an item row: the
// theme's workspace-bar glyph in the row's project accent, or a blank outside
// a project. It sits before the row's own runs, and the row composes one cell
// narrower to keep the right edge stable.
func RailBar(th theme.Theme, it RailItem) segment.Seg {
	if it.Project == "" {
		return segment.Seg{Text: " ", FG: th.Palette.RailDim}
	}
	return segment.Seg{Text: th.Glyphs.WorkspaceBar, FG: th.Accents[it.ProjectAccent]}
}

// RailRow composes one sidebar row at width w. left is the row's own run,
// right the run pinned to the right edge and tail the run that follows left
// and yields first. compact and set are the rail's own switches.
func RailRow(th theme.Theme, it RailItem, w int, compact bool, set theme.GlyphSet) (left, right, tail []segment.Seg) {
	p, g := th.Palette, th.Glyphs
	switch it.Kind {
	case Project:
		// The bar in column 0 is the writer's. The row itself is a space, the
		// project's name in its accent, and the group's workspace count.
		if !compact {
			name := th.Accents[it.ProjectAccent]
			left = []segment.Seg{
				{Text: " ", FG: name},
				{Text: it.Text, FG: name, Bold: true},
			}
			right = []segment.Seg{{Text: strconv.Itoa(it.Count), FG: p.RailDim}}
		}
	case Workspace:
		if !compact {
			// The project owns the colour: the active workspace is bright, the
			// others header grey, both bold. No per-workspace accent.
			name := p.RailHeader
			if it.Active {
				name = p.RailText
			}
			caret := g.FoldOpen
			if it.Folded {
				caret = g.FoldClosed
			}

			left = []segment.Seg{
				{Text: caret, FG: name},
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
			right = RailStats(th, it, set)
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
		mark, colour := RailGlyph(th, it.Status, set)
		// A shell has no state to report, so the fallback mark would be a
		// question about nothing. Leave the cell blank instead.
		if !it.Agent && (it.Status == "" || it.Status == "unknown") {
			mark = " "
		}
		glyph := RailMark(it.Agent, set)
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

// RailFoldAt returns the workspace or window whose fold caret is under x,y. The
// project bar holds column 0, so a workspace caret sits in column 1 and a
// window caret in column 3. It only answers for the caret cell, so a click on
// the row's name still focuses the row's first pane.
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
		if x != 1 {
			return ""
		}
		return it.Workspace
	case Window:
		if x != 3 {
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
func RailControlSegs(th theme.Theme, compact bool, set theme.GlyphSet) []segment.Seg {
	if compact {
		caret := "▶"
		switch set {
		case theme.GlyphASCII:
			caret = ">"
		case theme.GlyphNerd:
			caret = "\uf0da"
		}
		return []segment.Seg{
			{Text: "  ", FG: th.Palette.RailDim},
			{Text: caret, FG: th.Palette.RailDim},
		}
	}
	mark := "◀ "
	switch set {
	case theme.GlyphASCII:
		mark = "< "
	case theme.GlyphNerd:
		mark = "\uf0d9 "
	}
	return []segment.Seg{
		{Text: mark, FG: th.Palette.RailDim},
		{Text: "collapse", FG: th.Palette.RailDim},
	}
}

// DrawRail paints the sidebar above the status bar. The rail paints its own
// background so contrast does not depend on the terminal theme. Rows past the
// screen are clipped, and every row is written into at most Rail columns. The
// last column above the control row is the grip strip, so no row paints there.
// A left run is elided with an ellipsis before the right-aligned stats give up
// a cell, so the stats survive a narrow rail.
func DrawRail(buf *bytes.Buffer, th theme.Theme, snap *wire.Snapshot, set theme.GlyphSet) {
	if snap == nil || snap.Rail <= 0 || snap.Cols < 1 || snap.Rows < 3 {
		return
	}
	p := th.Palette
	w := snap.Rail
	rows := snap.Rows - 1
	items := RailItems(snap)
	compact := layout.RailCompact(snap.Rail)
	// The status bar holds Rows-1 and the control holds Rows-2, so items draw
	// in rows 0..Rows-3 and clip there. Nothing overwrites the control row.
	controlAt := snap.Rows - 2
	gripAt := controlAt / 2
	for y := 0; y < rows; y++ {
		var left, right, tail []segment.Seg
		bg, bold := p.RailBG, false
		// The grip strip owns the rail's last column above the control row, so
		// no row paints there and the hover line never covers a label.
		rowW := w
		if y < controlAt {
			rowW = w - 1
		}
		if y == controlAt {
			left = RailControlSegs(th, compact, set)
			if segment.SegCells(left) > rowW {
				left = segment.ElideSegs(th, left, rowW)
			}
		} else if y < len(items) {
			it := items[y]
			// The full rail gives column 0 to the project bar. The thin rail
			// paints no bar and no project row, so its rows keep their width.
			itemW := rowW
			if !compact {
				left = append(left, RailBar(th, it))
				itemW = rowW - 1
			}
			l, r, t := RailRow(th, it, itemW, compact, set)
			left = append(left, l...)
			right, tail = r, t
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
		if y < controlAt {
			fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, w)
			buf.WriteString("\x1b[0m" + bg.BG())
			if y == gripAt {
				buf.WriteString(p.RailDim.FG() + th.Glyphs.Grip)
			} else {
				buf.WriteString(" ")
			}
		}
		buf.WriteString("\x1b[0m")
	}
}
