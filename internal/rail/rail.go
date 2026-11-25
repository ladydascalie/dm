package rail

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
	"unicode/utf8"

	"dm/internal/cells"
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
	// ShelfHeader is the foldable header of the shelf section that follows the
	// last workspace. Shelf is one shelved pane's row under it.
	ShelfHeader
	Shelf
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
	// State is the state a group row rolls up: a window row carries the worst
	// state of its panes, a workspace row the worst of its windows and a
	// project row the worst of its workspaces. A pane row carries its own
	// state word in Status instead. An unknown member reads as no state, so the
	// empty word is the one that paints nothing.
	State   string
	Count   int
	Added   int
	Deleted int
	// Head is the short sha of the commit the row's HEAD names and HeadSubject
	// its subject. Upstream is the ref the branch tracks; Ahead and Behind
	// count the commits each side has that the other does not. Files counts the
	// files the working tree changes and FileNames lists the first of them in
	// git's order. A window row carries them, so the git float reads the same
	// probe the row was painted from.
	Head        string
	HeadSubject string
	Upstream    string
	Ahead       int
	Behind      int
	Files       int
	FileNames   []string
	Accent      int // an accent slot, resolved against the theme by the painter
	// Project is the project the row belongs to, empty outside a project.
	// ProjectAccent is that project's accent slot, set on every row of the
	// group so the painter can tie them with one bar.
	Project       string
	ProjectAccent int
	App           string
	Agent         bool
	Dirty         bool
	InRepo        bool
	Folded        bool
	Focus         bool
	Active        bool
	// Ask is true when the session lists a question on this pane. The rail
	// paints the badge, so a question in a window the reader is not looking at
	// still says so without stealing the desk.
	Ask bool
}

// askPanes is the set of panes the session lists a question on. The snapshot
// carries the asks, so the rail reads them without the client handing anything
// down.
func askPanes(snap *wire.Snapshot) map[string]bool {
	if snap == nil || len(snap.Ask) == 0 {
		return nil
	}
	out := make(map[string]bool, len(snap.Ask))
	for _, a := range snap.Ask {
		out[a.Pane] = true
	}
	return out
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

// Paint is one frame of the rail's own state: Off is the first item row
// painted on the rail's first line, the client's scroll offset; Hover is the
// item row the pointer rests on, -1 when none; Drop is the live drag's hint,
// nil when none; Open is the window whose git mark a float has open, empty when
// none.
type Paint struct {
	Off   int
	Hover int
	Drop  *Drop
	Open  string
}

// Drop is the live drag's paint hint: Row is the items row the drop boundary
// rests above, Lift is the dragged window's id, LiftWs the dragged
// workspace's id (one of the two is set), and Accent is the dragged
// project's accent slot for the insertion line. A nil Drop paints a
// drag-free rail.
type Drop struct {
	Row    int
	Lift   string
	LiftWs string
	Accent int
}

// RailItems flattens a snapshot into sidebar rows in the server's order: a
// workspace row, then per window a Window row, that window's note row when it
// has a message and is unfolded, and its pane rows when it is unfolded. A gap
// row separates workspace blocks. A folded workspace hides its windows and
// panes; a folded window hides only its panes. The thin rail is a glyph
// column: one pane row per pane, in snapshot order, whatever the fold. It has
// no workspace row, no window row, no note row and no gap row. Its pane rows
// still carry their project and accent, so the bar ties them to their
// project even when no project row is drawn. Shelved panes are in no window,
// so the server lists them in All with an empty workspace and window and the
// shelf section names them below. They are skipped here, or their empty
// workspace would paint a nameless group row that folds nothing.
func RailItems(snap *wire.Snapshot) []RailItem {
	if snap == nil {
		return nil
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
	// The server pins a slot to a project name and carries it in the
	// snapshot, so a project keeps its hue when its neighbours join and
	// leave. The painter trusts the carried slot. A snapshot that predates
	// the field carries none, and those names fall back to the live
	// allocator over the snapshot's own names.
	carried := map[string]int{}
	for _, p := range snap.All {
		if p.Project == "" || !p.ProjectAccentSet {
			continue
		}
		if _, ok := carried[p.Project]; !ok {
			carried[p.Project] = p.ProjectAccent
		}
	}
	live := allocator()
	projectAccent := func(name string) int {
		if slot, ok := carried[name]; ok {
			return slot
		}
		return live(name)
	}
	asked := askPanes(snap)
	if layout.RailCompact(snap.Rail) {
		seen := map[string]int{}
		out := make([]RailItem, 0, len(snap.All))
		for _, p := range snap.All {
			if p.Shelf {
				continue
			}
			row := RailPaneRow(p)
			row.Ask = asked[p.ID]
			if p.Project != "" {
				s, ok := seen[p.Project]
				if !ok {
					s = projectAccent(p.Project)
					seen[p.Project] = s
				}
				row.Project, row.ProjectAccent = p.Project, s
			}
			out = append(out, row)
		}
		return out
	}
	panes := map[string]int{}
	focus := map[string]int{}
	first := map[string]int{}
	withBranch := map[string]int{}
	// The state a group row rolls up is the worst state of its members, in
	// the order the snapshot lists them: a window row the worst of its panes,
	// a workspace row the worst of its windows, a project row the worst of its
	// workspaces. A pane that never reported contributes nothing to the
	// ranking, so a group of them rolls up to no state at all.
	bars := map[string][]string{}
	wsWindows := map[string][]string{}
	winSeen := map[string]bool{}
	projWorkspaces := map[string][]string{}
	wsSeen := map[string]bool{}
	for i, p := range snap.All {
		if p.Shelf {
			continue
		}
		panes[p.Workspace]++
		bars[p.Window] = append(bars[p.Window], p.Status)
		if !winSeen[p.Window] {
			winSeen[p.Window] = true
			wsWindows[p.Workspace] = append(wsWindows[p.Workspace], p.Window)
		}
		if !wsSeen[p.Workspace] {
			wsSeen[p.Workspace] = true
			projWorkspaces[p.Project] = append(projWorkspaces[p.Project], p.Workspace)
		}
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
	windowState := map[string]string{}
	for w, statuses := range bars {
		windowState[w] = railRollup(statuses)
	}
	workspaceState := map[string]string{}
	for ws, wins := range wsWindows {
		statuses := make([]string, len(wins))
		for i, w := range wins {
			statuses[i] = windowState[w]
		}
		workspaceState[ws] = railRollup(statuses)
	}
	projectState := map[string]string{}
	for name, wss := range projWorkspaces {
		statuses := make([]string, len(wss))
		for i, ws := range wss {
			statuses[i] = workspaceState[ws]
		}
		projectState[name] = railRollup(statuses)
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
	// Workspaces keep their old per-workspace slot for the callers that still
	// read it, though the rail no longer paints it. Projects own their column.
	accent := allocator()
	// A project row names its group once, before the group's first workspace,
	// and carries the number of workspaces in the group.
	projectCount := map[string]int{}
	seen := map[string]bool{}
	slotsFor := map[string]int{}
	for _, p := range snap.All {
		if p.Shelf || seen[p.Workspace] {
			continue
		}
		seen[p.Workspace] = true
		if p.Project != "" {
			projectCount[p.Project]++
		}
	}
	var out []RailItem
	ws, win, project, prev := "", "", "", ""
	folded := false
	slot := 0
	for _, p := range snap.All {
		if p.Shelf {
			continue
		}
		if p.Workspace != ws {
			if ws != "" && !folded {
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
			folded = p.ProjectFolded
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
						State: projectState[project], Folded: folded,
					})
				}
			}
			// A folded project hides its workspaces, windows and panes.
			if folded {
				continue
			}
			name := p.WorkspaceName
			if name == "" {
				name = p.Workspace
			}
			out = append(out, RailItem{
				Kind: Workspace, Text: name, Workspace: p.Workspace,
				Count: panes[p.Workspace], Accent: accent(p.Workspace),
				Project: project, ProjectAccent: slot,
				State:  workspaceState[p.Workspace],
				Active: p.WorkspaceActive,
				Folded: !p.WorkspaceUnfolded,
			})
		}
		// A folded project or workspace hides its windows and their panes.
		if folded || !p.WorkspaceUnfolded {
			continue
		}
		if p.Window != win {
			win = p.Window
			item := RailItem{
				Kind: Window, Text: p.WindowName, Window: p.Window,
				WindowName: p.WindowName, Workspace: p.Workspace,
				Project: project, ProjectAccent: slot,
				State: windowState[p.Window], Folded: !p.WindowUnfolded,
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
				item.Head = src.Head
				item.HeadSubject = src.HeadSubject
				item.Upstream = src.Upstream
				item.Ahead = src.Ahead
				item.Behind = src.Behind
				item.Files = src.Files
				item.FileNames = src.FileNames
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
		row.Ask = asked[p.ID]
		out = append(out, row)
	}
	// The shelf section follows the last workspace, so a pane set aside keeps
	// a row to click and to read its state from. A folded section hides its
	// rows the way a folded window hides its panes. The thin rail has no
	// section rows, so it shows no shelf.
	if shelf := ShelfPanes(snap); len(shelf) > 0 {
		out = append(out, RailItem{
			Kind: ShelfHeader, Text: "shelf", Count: len(shelf),
			Folded: snap.ShelfFolded,
		})
		if !snap.ShelfFolded {
			for i, p := range shelf {
				row := ShelfRow(p)
				row.Focus = i == snap.ShelfActive
				row.Ask = asked[p.ID]
				out = append(out, row)
			}
		}
	}
	return out
}

// ShelfPanes returns the snapshot's shelved panes in shelf order: the pane
// entries carrying the shelf flag, in the order the server listed them. They
// carry no rect, so nothing here reads geometry.
func ShelfPanes(snap *wire.Snapshot) []wire.PaneInfo {
	if snap == nil {
		return nil
	}
	var out []wire.PaneInfo
	for _, p := range snap.Panes {
		if p.Shelf {
			out = append(out, p)
		}
	}
	return out
}

// ShelfMark is the mark the shelf section and the status bar's shelf chip
// paint: a stack of panes on hold. The glyph follows the resolved set, so an
// ASCII terminal gets an ASCII mark.
func ShelfMark(set theme.GlyphSet) string {
	switch set {
	case theme.GlyphNerd:
		return "\uf0c5 "
	case theme.GlyphASCII:
		return "# "
	}
	return "\u25a4 "
}

// stateRank orders the state words by how much they ask of the operator, so a
// chip can name the member that asks most: an error or a block outranks work
// in flight, and a pane that never reported ranks last, with a shell.
var stateRank = map[string]int{
	"error":   5,
	"blocked": 4,
	"working": 3,
	"idle":    2,
	"done":    1,
}

// StateRank is the rank of one state word. A word no state names ranks with an
// unknown pane, which is the bottom of the order.
func StateRank(status string) int {
	if r, ok := stateRank[status]; ok {
		return r
	}
	return 0
}

// WorstState returns the state word of the members that asks most, in the
// order the statuses arrive, and "" for no members. A pane that never reported
// contributes its empty word, which ranks last, so a shelf of plain shells
// answers "" and the caller paints the unknown mark.
func WorstState(statuses []string) string {
	worst, rank := "", -1
	for _, st := range statuses {
		if r := StateRank(st); r > rank {
			worst, rank = st, r
		}
	}
	return worst
}

// railRollup is the state a group row paints: the worst state of its members,
// with an unknown one read as no state at all, so a group of panes that never
// reported paints nothing rather than a question mark.
func railRollup(statuses []string) string {
	if worst := WorstState(statuses); worst != "unknown" {
		return worst
	}
	return ""
}

// ShelfRow is one shelved pane's rail row. The label falls back from the pane
// name to its id, and a status word alone marks an agent: a shelved pane
// carries no app through the snapshot's pane entry.
func ShelfRow(p wire.PaneInfo) RailItem {
	label := p.Name
	if label == "" {
		label = p.ID
	}
	return RailItem{
		Kind: Shelf, Text: label, Pane: p.ID, PaneName: p.Name,
		Status: p.Status,
		Agent:  p.Status != "" && p.Status != "unknown",
	}
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
		Status: p.Status, Agent: p.Agent, Focus: p.Focus, App: p.App,
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

// RailMark is the two-cell prefix a thin rail row and a shelf row carry: an
// agent icon, a shell icon, or the fallback agent bar. The status glyph lands
// in the third cell either way. The full rail's pane row paints no mark at all.
// Every icon here is from the Font Awesome 4 range that even old Nerd Font
// patches carry; newer code points such as the FA5 robot render as tofu. The
// pi agent is the one exception: its Nerd mark is the plain Greek letter pi,
// which every font has.
func RailMark(agent bool, app string, set theme.GlyphSet) string {
	switch {
	case agent && app == "pi" && set == theme.GlyphNerd:
		return "\u03c0 "
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

// railGroupTail composes the right run of a project or workspace row: the
// count of hidden members while the row is folded, then the row's rolled-up
// state at the margin. An open row paints the state alone, because its members
// are right there and the row needs no count to say so.
func railGroupTail(th theme.Theme, it RailItem, showCount bool, set theme.GlyphSet) []segment.Seg {
	state := railStateSeg(th, it.State, set)
	if !showCount {
		return []segment.Seg{state}
	}
	return []segment.Seg{
		{Text: strconv.Itoa(it.Count), FG: th.Palette.RailDim},
		{Text: " ", FG: th.Palette.RailDim},
		state,
	}
}

// railGitCount formats a line count for the git field: up to four digits read
// as themselves, and past that a whole unit, k then M then G, rounded to the
// nearest one. A count that rounds up to a thousand of its unit moves to the
// next unit, so 999999 reads 1M and never 1000k. Past 999G there is no bigger
// unit, so a count that large keeps G.
func railGitCount(n int) string {
	if n < 0 {
		n = 0
	}
	if n < 10000 {
		return strconv.Itoa(n)
	}
	units := []struct {
		div    int
		suffix string
	}{{1000, "k"}, {1000000, "M"}, {1000000000, "G"}}
	for _, u := range units {
		if v := (n + u.div/2) / u.div; v < 1000 || u.suffix == "G" {
			return strconv.Itoa(v) + u.suffix
		}
	}
	return strconv.Itoa(n)
}

// padCells pads s on the right to n cells, so a signed count starts in the same
// cell whether it is one digit or five.
func padCells(s string, n int) string {
	if c := cells.Cells(s); c < n {
		return s + strings.Repeat(" ", n-c)
	}
	return s
}

// railGitField is the git field of a window row: two fixed columns of six
// cells, added lines then removed, each left aligned from its sign so every
// plus sits in one cell and every minus in another. A column with nothing to
// show is six cells of air, never a zero.
func railGitField(th theme.Theme, it RailItem) []segment.Seg {
	p := th.Palette
	add := segment.Seg{Text: strings.Repeat(" ", gitFieldCols), FG: p.RailDim}
	del := segment.Seg{Text: strings.Repeat(" ", gitFieldCols), FG: p.RailDim}
	if it.Added > 0 {
		add = segment.Seg{Text: padCells("+"+railGitCount(it.Added), gitFieldCols), FG: p.RailDiffAdd}
	}
	if it.Deleted > 0 {
		del = segment.Seg{Text: padCells("-"+railGitCount(it.Deleted), gitFieldCols), FG: p.RailDiffDel}
	}
	return []segment.Seg{add, del}
}

// railStateSeg is the one state cell of a row: the theme's mark for the state
// word and its colour, or one blank cell when the row has nothing to say. An
// unknown state and a shell that never reported both paint the blank, so no
// row asks a question about a state it does not have.
func railStateSeg(th theme.Theme, status string, set theme.GlyphSet) segment.Seg {
	if status == "" || status == "unknown" {
		return segment.Seg{Text: " ", FG: th.Palette.RailDim}
	}
	glyph, colour := RailGlyph(th, status, set)
	return segment.Seg{Text: glyph, FG: colour}
}

// windowLabelText is the text a window row paints: the rendered label, the
// folder below the repository root, or the window's own name.
func windowLabelText(it RailItem) string {
	for _, s := range []string{it.Label, it.Folder, it.Text} {
		if s != "" {
			return s
		}
	}
	return ""
}

// railTailFits reports whether a window row of width w can keep a right run of
// rightCells beside its whole label. It is the rent every right-aligned run of a
// window row pays: a run that does not fit stands down whole rather than take a
// cell from the name. The ladder asks it for the field first, then for the mark,
// then lets the label elide.
func railTailFits(it RailItem, w, rightCells int) bool {
	return windowChrome+cells.Cells(windowLabelText(it))+rightCells+rowGapCells <= w
}

// railMarkShown reports whether a window row's git mark has something to say:
// uncommitted work in the tree, or commits the branch has that its upstream
// does not or the other way round. A clean row in step with its upstream keeps
// the cell blank until the pointer rests on it.
func railMarkShown(it RailItem) bool {
	return it.Dirty || it.Ahead > 0 || it.Behind > 0
}

// railWindowTail composes the right run of a window row: the git field when the
// row is in a repository, the mark cell, one cell of air and the state at the
// margin. The ladder drops the field whole first, then the mark, so the label
// loses no cell to a field the row cannot hold. It returns the run and the index
// of the mark cell in it, -1 when the ladder dropped the mark.
func railWindowTail(th theme.Theme, it RailItem, w int, set theme.GlyphSet) ([]segment.Seg, int) {
	p := th.Palette
	state := railStateSeg(th, it.State, set)
	if !it.InRepo {
		return []segment.Seg{state}, -1
	}
	mark := segment.Seg{Text: " ", FG: gitMarkQuiet(p)}
	if railMarkShown(it) {
		mark.Text = th.Glyphs.GitMark
	}
	air := segment.Seg{Text: " ", FG: p.RailDim}
	if railTailFits(it, w, gitFieldCells+gitMarkCells+gitAirCells+1) {
		right := append(railGitField(th, it), mark, air, state)
		return right, gitFieldCells
	}
	if railTailFits(it, w, gitMarkCells+gitAirCells+1) {
		return []segment.Seg{mark, air, state}, 0
	}
	return []segment.Seg{state}, -1
}

// RailAccentColour is the theme colour for an accent slot. A slot outside
// the theme, such as one a hand-edited state file carried, falls back to the
// dim rail colour instead of indexing past the palette. The switcher paints
// the same pinned slot, so both painters share the guard.
func RailAccentColour(th theme.Theme, slot int) theme.RGB {
	if slot < 0 || slot >= len(th.Accents) {
		return th.Palette.RailDim
	}
	return th.Accents[slot]
}

// RailBar is the bar cell of an item row: the theme's workspace-bar glyph
// in the row's project accent, or a blank outside a project. The full rail
// paints it in column 0; the thin rail paints it between the agent mark and
// the state glyph. It sits before the row's own runs, and the row composes
// one cell narrower to keep the right edge stable.
func RailBar(th theme.Theme, it RailItem) segment.Seg {
	if it.Project == "" {
		return segment.Seg{Text: " ", FG: th.Palette.RailDim}
	}
	return segment.Seg{Text: th.Glyphs.WorkspaceBar, FG: RailAccentColour(th, it.ProjectAccent)}
}

const (
	// windowChrome is the cells a window row spends before its label: the
	// two-space indent, the fold caret and the space after it. Every run of the
	// row's tail pays its rent against them.
	windowChrome = 4
	// gitFieldCols is one git field column: a sign and up to five cells, so a
	// four digit count reads as itself and a longer one as a whole unit.
	gitFieldCols = 6
	// gitFieldCells is both columns together.
	gitFieldCells = 2 * gitFieldCols
	// gitMarkCells is the width of the git mark cell between the field and the
	// air. It is the cell the hit test answers.
	gitMarkCells = 1
	// gitAirCells is the one cell of air between the mark and the state.
	gitAirCells = 1
	// rowGapCells is the one cell between a row's left run and its right run,
	// so a label never touches the run beside it.
	rowGapCells = 1
)

// gitMarkRot is how far the mark's rest colour walks from the bar accent toward
// the rail's ground. The hue survives at this distance, so the mark reads as the
// rail's own accent, and its value drops enough that the counts still lead. The
// pointer takes the glyph back to full accent and an open float inverts it.
const gitMarkRot = 0.7

// gitMarkQuiet is the quiet accent a git mark rests in.
func gitMarkQuiet(p theme.Palette) theme.RGB {
	return mixRGB(p.BarAccent, p.RailBG, gitMarkRot)
}

// mixRGB walks one colour toward another by rot, the way hintbar derives its
// pipe tone.
func mixRGB(from, to theme.RGB, rot float64) theme.RGB {
	walk := func(a, b int) int {
		return int(math.Round(float64(a) + (float64(b)-float64(a))*rot))
	}
	return theme.RGB{R: walk(from.R, to.R), G: walk(from.G, to.G), B: walk(from.B, to.B)}
}

// RailRow composes one sidebar row at width w. left is the row's own run,
// right the run pinned to the right edge and tail the run that follows left
// and yields first. compact and set are the rail's own switches.
func RailRow(th theme.Theme, it RailItem, w int, compact bool, set theme.GlyphSet) (left, right, tail []segment.Seg) {
	p, g := th.Palette, th.Glyphs
	switch it.Kind {
	case Project:
		// The bar in column 0 is the writer's. The row itself is the fold
		// caret, a space and the project's name in its accent. A folded row
		// carries the group's workspace count, the only evidence of what it
		// hides; an open row carries none, because its workspaces are right
		// there. The state rolls up from the group and sits at the margin.
		if !compact {
			name := RailAccentColour(th, it.ProjectAccent)
			caret := g.FoldOpen
			if it.Folded {
				caret = g.FoldClosed
			}
			left = []segment.Seg{
				{Text: caret, FG: name},
				{Text: " ", FG: name},
				{Text: it.Text, FG: name, Bold: true},
			}
			right = railGroupTail(th, it, it.Folded, set)
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
			right = railGroupTail(th, it, it.Folded, set)
		}
	case Window:
		base := p.RailWindow
		if !compact {
			caret := g.FoldOpen
			if it.Folded {
				caret = g.FoldClosed
			}
			left = []segment.Seg{
				{Text: "  ", FG: base},
				{Text: caret, FG: base},
				{Text: " ", FG: base},
				{Text: windowLabelText(it), FG: base},
			}
			// A window row closes with the git field, the mark, one cell of air
			// and the state at the margin. The ladder drops the field whole
			// first and the mark second, so a narrow row keeps its name and its
			// state.
			right, _ = railWindowTail(th, it, w, set)
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
		if compact {
			// The thin rail keeps its three cells: the bar, the agent glyph and
			// the state.
			mark, colour := RailGlyph(th, it.Status, set)
			// The thin rail keeps its composition and its silence: a shell
			// with no state leaves the cell blank, the way it always did.
			if !it.Agent && (it.Status == "" || it.Status == "unknown") {
				mark = " "
			}
			glyph := RailMark(it.Agent, it.App, set)
			// The bar goes full left, the agent mark gives up its trailing
			// space to sit between the bar and the state glyph, so the state
			// glyph keeps its column.
			m1, _ := utf8.DecodeRuneInString(glyph)
			left = []segment.Seg{
				RailBar(th, it),
				{Text: string(m1), FG: colour},
				{Text: mark, FG: colour},
			}
			if it.Ask {
				left = append(left, segment.Seg{Text: "?", FG: p.BarAccent})
			}
			break
		}
		// Six cells of indent, no icon, the name from column 7, and the state
		// glyph alone at the margin: the glyph is the row's only icon.
		left = []segment.Seg{
			{Text: "      ", FG: base},
			{Text: it.Text, FG: base},
		}
		right = []segment.Seg{railStateSeg(th, it.Status, set)}
		if it.Ask {
			// The badge stands one cell left of the state, so the state keeps
			// the margin and a question never pushes a pane's name out.
			right = append([]segment.Seg{
				{Text: "?", FG: p.BarAccent, Bold: true},
				{Text: " ", FG: p.RailDim},
			}, right...)
		}
	case ShelfHeader:
		if !compact {
			base := p.RailHeader
			caret := g.FoldOpen
			if it.Folded {
				caret = g.FoldClosed
			}
			left = []segment.Seg{
				{Text: caret, FG: base},
				{Text: " ", FG: base},
				{Text: it.Text, FG: base, Bold: true},
				{Text: " " + ShelfMark(set), FG: base},
			}
			right = []segment.Seg{{Text: strconv.Itoa(it.Count), FG: p.RailDim}}
		}
	case Shelf:
		// A shelved pane keeps the pane row's shape: the agent mark, the
		// state glyph and the name, so its state reads at a glance.
		base := p.RailDim
		if it.Focus {
			base = p.RailText
		}
		mark, colour := RailGlyph(th, it.Status, set)
		if !it.Agent && (it.Status == "" || it.Status == "unknown") {
			mark = " "
		}
		glyph := RailMark(it.Agent, it.App, set)
		if compact {
			m1, _ := utf8.DecodeRuneInString(glyph)
			left = []segment.Seg{
				RailBar(th, it),
				{Text: string(m1), FG: colour},
				{Text: mark, FG: colour},
			}
			if it.Ask {
				left = append(left, segment.Seg{Text: "?", FG: p.BarAccent})
			}
		} else {
			left = []segment.Seg{
				{Text: "      ", FG: base},
				{Text: glyph, FG: colour},
				{Text: mark, FG: colour},
				{Text: " " + it.Text, FG: base},
			}
			if it.Ask {
				right = []segment.Seg{{Text: "?", FG: p.BarAccent, Bold: true}}
			}
		}
	case Gap:
		// The rail background and nothing else.
	}
	rightCells := segment.SegCells(right)
	if rightCells > w {
		// A right run that does not fit goes whole: half a field lies.
		right, rightCells = nil, 0
	}
	budget := w - rightCells
	if rightCells > 0 && budget > 0 {
		budget--
	}
	// A row gives up its tail whole before the left run elides, so a tail is
	// never truncated.
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

// railItemRow resolves a screen row to the item row it shows, through the
// client's scroll offset. It reports -1 for a row outside the rail's own
// columns, a point off the rail, an empty row or a row past the last item.
func railItemRow(snap *wire.Snapshot, y, off int) int {
	if snap == nil || snap.Rail <= 0 || y < 0 || y >= snap.Rows-2 {
		return -1
	}
	row := y + off
	if row < 0 || row >= len(RailItems(snap)) {
		return -1
	}
	return row
}

// RailPaneAt returns the pane whose row is under x,y, or "" for any other row,
// an empty row, a point outside the rail, or the rail's bottom control row. off
// is the rail's scroll offset, the first item row painted.
func RailPaneAt(snap *wire.Snapshot, x, y, off int) string {
	if snap == nil || snap.Rail <= 0 || x < 0 || x >= snap.Rail {
		return ""
	}
	items := RailItems(snap)
	i := railItemRow(snap, y, off)
	if i < 0 || items[i].Kind != Pane {
		return ""
	}
	return items[i].Pane
}

// RailShelfAt returns the shelved pane whose row is under x,y, or "" for any
// other row. A click on the row focuses that member: the server moves the
// shelf's active index and leaves the window focus alone. A shelf row answers;
// a row above it does not.
func RailShelfAt(snap *wire.Snapshot, x, y, off int) string {
	if snap == nil || snap.Rail <= 0 || x < 0 || x >= snap.Rail {
		return ""
	}
	items := RailItems(snap)
	i := railItemRow(snap, y, off)
	if i < 0 || items[i].Kind != Shelf {
		return ""
	}
	return items[i].Pane
}

// RailShelfHeaderAt reports whether x,y is the body of the shelf section
// header, the part right of the fold caret. A press there summons the shelf
// float, the same answer the status bar's shelf chip gives, so the mark the
// header paints is a control and not decoration. The caret cell belongs to the
// fold, and no other row answers. The thin rail paints no section header, so
// it never answers.
func RailShelfHeaderAt(snap *wire.Snapshot, x, y, off int) bool {
	if snap == nil || snap.Rail <= 0 || x < 0 || x >= snap.Rail {
		return false
	}
	if layout.RailCompact(snap.Rail) {
		return false
	}
	// Cell 0 is the project bar and cell 1 the caret, so the body starts
	// at cell 2. The rest of the row, the name, the mark and the count,
	// belongs to the body.
	if x < 2 {
		return false
	}
	items := RailItems(snap)
	i := railItemRow(snap, y, off)
	return i >= 0 && items[i].Kind == ShelfHeader
}

// RailWindowAt returns the window whose row is under x,y, for a focus click. A
// window line and its note line both answer. The thin rail has no window row,
// so it never answers.
func RailWindowAt(snap *wire.Snapshot, x, y, off int) string {
	if snap == nil || snap.Rail <= 0 || x < 0 || x >= snap.Rail {
		return ""
	}
	if layout.RailCompact(snap.Rail) {
		return ""
	}
	items := RailItems(snap)
	i := railItemRow(snap, y, off)
	if i < 0 {
		return ""
	}
	if k := items[i].Kind; k != Window && k != WindowNote {
		return ""
	}
	return items[i].Window
}

// railWindowIndex returns the item row that draws a window's line, or -1 when
// no drawn row carries it. A folded window still draws its row; a folded
// project or workspace hides it.
func railWindowIndex(items []RailItem, window string) int {
	for i := range items {
		if items[i].Kind == Window && items[i].Window == window {
			return i
		}
	}
	return -1
}

// railGitMarkItem is the row under y, and whether it is a window row inside a
// repository: the one row kind that paints a git mark. Every mark rule reads
// this, so the hit test and the float anchor cannot disagree about which rows
// carry one.
func railGitMarkItem(snap *wire.Snapshot, y, off int) (RailItem, bool) {
	if snap == nil || snap.Rail <= 0 || layout.RailCompact(snap.Rail) {
		return RailItem{}, false
	}
	i := railItemRow(snap, y, off)
	if i < 0 {
		return RailItem{}, false
	}
	it := RailItems(snap)[i]
	if it.Kind != Window || !it.InRepo {
		return RailItem{}, false
	}
	return it, true
}

// RailWindowItem returns the rail row that draws a window, and false when no
// drawn row carries it: a folded project or workspace hides its windows. A
// folded window still draws its row, so it answers. A git float reads its facts
// from this row, the same one the mark was painted on.
func RailWindowItem(snap *wire.Snapshot, window string) (RailItem, bool) {
	if snap == nil || snap.Rail <= 0 || window == "" || layout.RailCompact(snap.Rail) {
		return RailItem{}, false
	}
	items := RailItems(snap)
	i := railWindowIndex(items, window)
	if i < 0 {
		return RailItem{}, false
	}
	return items[i], true
}

// railGitMarkKept reports whether the ladder keeps a window row's mark at item
// width itemW: the whole label must fit beside the mark, one cell of air and the
// state.
func railGitMarkKept(it RailItem, itemW int) bool {
	return railTailFits(it, itemW, gitMarkCells+gitAirCells+1)
}

// railGitMarkCol is the screen column of a window row's mark cell for a rail w
// cells wide. The grip strip owns the last column and the project bar the
// first, so the row composes w-2 cells, its right run ends on the state at the
// margin and the mark sits one cell of air and one cell of state before it. The
// second value reports whether the ladder kept the mark at all.
func railGitMarkCol(it RailItem, w int) (int, bool) {
	if !railGitMarkKept(it, w-2) {
		return 0, false
	}
	return w - 2 - gitAirCells - gitMarkCells, true
}

// RailGitMarkRow is the screen row the window's git mark is painted on, or -1
// when the mark is not on the visible rail: no drawn row carries the window, the
// window's directory is outside git, the rail is thin, the ladder dropped the
// mark, or the row is scrolled out of the item rows. The float anchors on this
// row and the hit test resolves its rows the same way, so the box and the mark
// cannot part.
func RailGitMarkRow(snap *wire.Snapshot, window string, off int) int {
	if snap == nil || snap.Rail <= 0 || window == "" || layout.RailCompact(snap.Rail) {
		return -1
	}
	items := RailItems(snap)
	i := railWindowIndex(items, window)
	if i < 0 || !items[i].InRepo {
		return -1
	}
	if _, ok := railGitMarkCol(items[i], snap.Rail); !ok {
		return -1
	}
	y := i - off
	if y < 0 || y >= snap.Rows-2 {
		return -1
	}
	return y
}

// RailGitMarkAt returns the window whose git mark is under x,y, or "" for any
// other cell, row or rail. The mark is one cell, the third from the row's right
// edge: one cell of air and the state sit after it. It is painted when the
// worktree is dirty or the branch is ahead of or behind its upstream, and also
// on a clean row under the pointer, so the cell is the click target at rest too.
// A directory outside git paints no mark, a pane row paints none, a row the fold
// hides is not drawn and a row too narrow to keep the mark beside its label has
// dropped it, so none of them answer. The thin rail has no window row, so it
// never answers.
func RailGitMarkAt(snap *wire.Snapshot, x, y, off int) string {
	if snap == nil || snap.Rail <= 0 || x < 0 || x >= snap.Rail {
		return ""
	}
	it, ok := railGitMarkItem(snap, y, off)
	if !ok {
		return ""
	}
	if col, kept := railGitMarkCol(it, snap.Rail); !kept || x != col {
		return ""
	}
	return it.Window
}

// RailWorkspaceAt returns the workspace whose row is under x,y. The thin rail
// has no workspace row, so it never answers.
func RailWorkspaceAt(snap *wire.Snapshot, x, y, off int) string {
	if snap == nil || snap.Rail <= 0 || x < 0 || x >= snap.Rail {
		return ""
	}
	if layout.RailCompact(snap.Rail) {
		return ""
	}
	items := RailItems(snap)
	i := railItemRow(snap, y, off)
	if i < 0 || items[i].Kind != Workspace {
		return ""
	}
	return items[i].Workspace
}

// RailFoldAt returns the project, workspace or window whose fold caret is
// under x,y. The project bar holds column 0, so a project or workspace caret
// sits in column 1 and a window caret in column 3. A project target carries
// wire.ProjectTargetPrefix. It only answers for the caret cell, so a click on
// the row's name still focuses the row's first pane.
func RailFoldAt(snap *wire.Snapshot, x, y, off int) string {
	if snap == nil || snap.Rail <= 0 || x < 0 || x >= snap.Rail {
		return ""
	}
	if layout.RailCompact(snap.Rail) {
		return ""
	}
	items := RailItems(snap)
	i := railItemRow(snap, y, off)
	if i < 0 {
		return ""
	}
	it := items[i]
	switch it.Kind {
	case Project:
		if x != 1 {
			return ""
		}
		return wire.ProjectTargetPrefix + it.Project
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
	case ShelfHeader:
		if x != 1 {
			return ""
		}
		return wire.ShelfFoldTarget
	}
	return ""
}

// RailItemAt returns the rail row under x,y. A point outside the rail, on the
// bottom control row, or past the last item reports false. The gap row between
// workspace blocks is a row, so it answers true. off is the rail's scroll
// offset, the first item row painted.
func RailItemAt(snap *wire.Snapshot, x, y, off int) (RailItem, bool) {
	if snap == nil || snap.Rail <= 0 || x < 0 || x >= snap.Rail {
		return RailItem{}, false
	}
	items := RailItems(snap)
	i := railItemRow(snap, y, off)
	if i < 0 {
		return RailItem{}, false
	}
	return items[i], true
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

// ClampOffset bounds a rail scroll offset to the rows the rail can show, so a
// list that just shrank never leaves the rail blank or a pane row out of
// reach. Every row above the control row carries an item, so one page is
// Rows-2 rows high.
func ClampOffset(snap *wire.Snapshot, off int) int {
	if snap == nil || snap.Rows < 3 || off <= 0 {
		return 0
	}
	max := len(RailItems(snap)) - (snap.Rows - 2)
	if max < 0 {
		max = 0
	}
	if off > max {
		return max
	}
	return off
}

// DrawRail paints the sidebar above the status bar. The rail paints its own
// background so contrast does not depend on the terminal theme. Rows past the
// screen are clipped, and every row is written into at most Rail columns. The
// last column above the control row is the grip strip, so no row paints there.
// A right run stands down whole before the left run elides, so the rail never
// paints half a field, and a window row's ladder gives up the field before the
// mark and the mark before the label. A Paint carries the frame's
// scroll offset, the row the pointer rests on (-1 when none) and the live
// drag's hint (nil when none) and the window whose git mark a float has open
// (empty when none). The hovered row paints RailHoverBG, with the focused pane
// row keeping its stronger ground. A git mark rests in the quiet accent, takes
// the full accent under the pointer, and inverts while its float is up, so one
// mark at most ever says which box is open. The rows of a lifted window
// or workspace dim out of place, and the row the drop boundary rests above is
// replaced by an insertion line in the dragged project's accent, so the
// landing spot reads between the rows instead of lighting one.
func DrawRail(buf *bytes.Buffer, th theme.Theme, snap *wire.Snapshot, set theme.GlyphSet, paint Paint) {
	if snap == nil || snap.Rail <= 0 || snap.Cols < 1 || snap.Rows < 3 {
		return
	}
	p := th.Palette
	w := snap.Rail
	rows := snap.Rows - 1
	items := RailItems(snap)
	off := ClampOffset(snap, paint.Off)
	hover := paint.Hover
	if hover < 0 || hover >= len(items) {
		hover = -1
	}
	drop := paint.Drop
	compact := layout.RailCompact(snap.Rail)
	// The status bar holds Rows-1 and the control holds Rows-2, so items draw
	// in rows 0..Rows-3 and clip there. Nothing overwrites the control row.
	controlAt := snap.Rows - 2
	gripAt := controlAt / 2
	for y := 0; y < rows; y++ {
		var left, right, tail []segment.Seg
		bg, bold := p.RailBG, false
		// markAt is the first run of a git mark that survived the row's width,
		// -1 when the row carries none or dropped it whole. markOpen says the
		// mark is the one the open float belongs to.
		markAt := -1
		markOpen := false
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
		} else if i := y + off; i < len(items) {
			it := items[i]
			// The full rail gives column 0 to the project bar. The thin rail
			// paints no project row and composes its own bar inside RailRow, so
			// its rows keep their width.
			itemW := rowW
			if !compact {
				left = append(left, RailBar(th, it))
				itemW = rowW - 1
			}
			l, r, t := RailRow(th, it, itemW, compact, set)
			left = append(left, l...)
			right, tail = r, t
			// A focused pane row paints its own background and every run bold.
			// A hovered row lights lightly; the gap row never lights, so a
			// highlight always means the row answers a click.
			// A lifted row drops its fill and its emphasis: what the pointer
			// carries is in the air, so nothing under it answers.
			lift := drop != nil && ((drop.Lift != "" && it.Window == drop.Lift) ||
				(drop.LiftWs != "" && it.Workspace == drop.LiftWs)) && it.Kind != Gap
			// A window row inside a repository carries the mark as the third
			// run from the end of its right run: the mark, one cell of air and
			// the state. The ladder may have dropped the mark whole, and the
			// same rule decides both here and in the hit test.
			if !compact && it.Kind == Window && it.InRepo && railGitMarkKept(it, itemW) &&
				len(right) >= gitMarkCells+gitAirCells+1 {
				markAt = len(right) - (gitMarkCells + gitAirCells + 1)
			}
			switch {
			case lift:
				bg, bold = p.RailBG, false
			case (it.Kind == Pane || it.Kind == Shelf) && it.Focus:
				bg, bold = p.RailFocusBG, true
			case i == hover && it.Kind != Gap:
				bg = p.RailHoverBG
			}
			if markAt >= 0 && it.Window == paint.Open && paint.Open != "" && !lift {
				markOpen = true
			}
			if markAt >= 0 && (i == hover || markOpen) && !lift {
				// A clean row paints no mark until the pointer rests on it or
				// its box is open, so the facts box always has a cell to open
				// from and keep. The pointer takes the glyph to the full
				// accent; the open mark inverts below.
				right[markAt].Text = th.Glyphs.GitMark
				right[markAt].FG = p.BarAccent
			}
			if lift {
				for i := range left {
					left[i].FG = p.RailDim
				}
				for i := range right {
					right[i].FG = p.RailDim
				}
				for i := range tail {
					tail[i].FG = p.RailDim
				}
			}
		}
		// The drop boundary replaces its row with an insertion line in the
		// dragged project's accent, so the landing spot sits between the rows
		// it splits. A boundary past the list paints on the first empty row.
		if drop != nil && drop.Row >= 0 && y == drop.Row-off && y < controlAt {
			line := th.Glyphs.BorderHorizontal
			if set == theme.GlyphASCII {
				line = "-"
			}
			left = []segment.Seg{{
				Text: strings.Repeat(line, rowW),
				FG:   th.Accents[drop.Accent%len(th.Accents)],
			}}
			right, tail = nil, nil
			bg, bold = p.RailBG, false
		}
		leftCells := segment.SegCells(left) + segment.SegCells(tail)
		rightCells := segment.SegCells(right)
		fmt.Fprintf(buf, "\x1b[%d;1H", y+1)
		buf.WriteString("\x1b[0m")
		buf.WriteString(bg.BG())
		for _, s := range left {
			WriteRailSeg(buf, s, bg, bold)
		}
		for _, s := range tail {
			WriteRailSeg(buf, s, bg, bold)
		}
		if n := rowW - leftCells - rightCells; n > 0 {
			buf.WriteString(strings.Repeat(" ", n))
		}
		for j, s := range right {
			if markOpen && j == markAt {
				// The open mark inverts: the box fills with the accent and
				// the glyph takes the rail's ground. Only the open window's
				// mark is ever in this state.
				s.FG, s.BG = p.RailBG, p.BarAccent
				segment.Write(buf, s)
				continue
			}
			WriteRailSeg(buf, s, bg, bold)
		}
		if y < controlAt {
			fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, w)
			buf.WriteString("\x1b[0m")
			buf.WriteString(bg.BG())
			if y == gripAt {
				buf.WriteString(p.RailDim.FG())
				buf.WriteString(th.Glyphs.Grip)
			} else {
				buf.WriteString(" ")
			}
		}
		buf.WriteString("\x1b[0m")
	}
}
