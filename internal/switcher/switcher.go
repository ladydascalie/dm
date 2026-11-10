package switcher

import (
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"dm/internal/layout"
	"dm/internal/rail"
	"dm/internal/wire"
)

// SortMode is how the table orders its rows.
type SortMode int

const (
	// SortRecent puts the newest agent status change first. It is the default.
	SortRecent SortMode = iota
	// SortProject groups the rows by project name and keeps the snapshot's
	// order inside a group. A pane outside a project sorts last.
	SortProject
	// SortStatus puts the states that want attention first: blocked, error,
	// working, done, idle, unknown, then a shell.
	SortStatus
)

// String is the mode's footer label.
func (m SortMode) String() string {
	switch m {
	case SortProject:
		return "project"
	case SortStatus:
		return "status"
	}
	return "recent"
}

// nextSort is the mode CycleSort lands on.
func (m SortMode) nextSort() SortMode { return (m + 1) % 3 }

// The preview band's share of the body, in percent, and the bounds
// ResizePreview keeps it in.
const (
	previewRatioDefault = 60
	previewRatioMin     = 20
	previewRatioMax     = 85
)

// A float smaller than this hides the preview band, so the table still works
// when the dashboard is drawn on a small screen.
const (
	previewMinRows = 10
	previewMinCols = 60
)

// Switcher is the dashboard's model: one row per pane in the snapshot, the rows
// the query keeps, the selection, the ordering, the preview band's size and
// scroll, and the preview request with the lines it got back. Drawing and key
// handling live elsewhere; this file is the model that answers them.
type Switcher struct {
	Rect layout.Rect

	// all is the snapshot's pane list, and rows are the panes the query keeps
	// in the sort mode's order. sel is one of rows, or -1 when nothing
	// survives. top is the first row on screen, so a redraw does not jump.
	all  []wire.AllPane
	rows []wire.AllPane
	sel  int
	top  int

	// query is the filter's text and filter says whether it is live. A query
	// only filters while the dashboard is in filter mode, because outside it
	// every letter is a key. Leaving the mode drops the query with it, so the
	// table shows every pane again the moment the letters are keys again.
	query  []rune
	filter bool

	mode      SortMode
	ratio     int
	offset    int
	previewOn bool

	// The preview request: whether one is outstanding, the newest request id,
	// the pane it asked for, and the lines the server sent for it.
	asked bool
	req   int
	pane  string
	lines []string
}

// NewSwitcher opens the dashboard over a snapshot inside rect.
func NewSwitcher(rect layout.Rect, snap *wire.Snapshot) *Switcher {
	s := &Switcher{Rect: rect, sel: -1, ratio: previewRatioDefault, previewOn: true}
	s.Sync(snap)
	return s
}

// Sync rebuilds the rows after a snapshot. The selection stays on its own pane
// when that pane survives, and falls back to the nearest row when it is gone. A
// preview of a pane that is gone is stale, so its request and lines go with it.
//
// The dashboard owns the whole screen, so a snapshot whose size differs moves
// the rect with it: a grow and a shrink both land here, and the painter keeps
// its clamp only as a guard.
func (s *Switcher) Sync(snap *wire.Snapshot) {
	s.all = s.all[:0]
	if snap != nil {
		if snap.Cols != s.Rect.W || snap.Rows != s.Rect.H {
			s.Rect = layout.Rect{W: snap.Cols, H: snap.Rows}
		}
		s.all = append(s.all, snap.All...)
	}
	if s.asked && !s.hasPane(s.pane) {
		s.asked, s.pane, s.lines, s.offset = false, "", nil, 0
	}
	prev := s.Target()
	s.rebuild(s.sel, prev)
}

// Filtering reports whether the dashboard is in filter mode, where every
// printable key edits the query instead of acting on the table.
func (s *Switcher) Filtering() bool {
	return s.filter
}

// StartFilter enters filter mode and keeps the table as it is. The query starts
// empty, so the first keystroke in the mode narrows a full table.
func (s *Switcher) StartFilter() {
	if s.filter {
		return
	}
	s.filter = true
	if len(s.query) > 0 {
		s.query = nil
		s.refilter()
	}
}

// StopFilter leaves filter mode and drops the query with it, so the table shows
// every pane again. A query outliving the mode would hide rows while the
// letters stopped being text.
func (s *Switcher) StopFilter() {
	if !s.filter {
		return
	}
	s.filter = false
	if len(s.query) == 0 {
		return
	}
	s.query = nil
	s.refilter()
}

// AppendText adds a rune to the query and reselects the best match. Text
// outside filter mode is ignored: there the letters are keys.
func (s *Switcher) AppendText(r rune) {
	if !s.filter {
		return
	}
	s.query = append(s.query, r)
	s.refilter()
}

// Backspace removes the query's last rune and reselects.
func (s *Switcher) Backspace() {
	if !s.filter || len(s.query) == 0 {
		return
	}
	s.query = s.query[:len(s.query)-1]
	s.refilter()
}

// ClearQuery empties the query and reselects. The table stays in the mode it is
// in, so the next keystroke is still a letter of the query.
func (s *Switcher) ClearQuery() {
	if !s.filter || len(s.query) == 0 {
		return
	}
	s.query = nil
	s.refilter()
}

// Query is the query as a lower-case string to match against.
func (s *Switcher) Query() string {
	return strings.ToLower(string(s.query))
}

// RowCount is how many panes the query keeps.
func (s *Switcher) RowCount() int {
	return len(s.rows)
}

// Move steps the selection d rows and stops at either end of the table.
func (s *Switcher) Move(d int) {
	if len(s.rows) == 0 {
		s.sel = -1
		return
	}
	i := s.sel
	if i < 0 {
		i = 0
	}
	i += d
	if i < 0 {
		i = 0
	}
	if i >= len(s.rows) {
		i = len(s.rows) - 1
	}
	s.sel = i
}

// Jump selects the n'th row, numbered from one the way the 1-9 keys are, and
// reports whether that row exists.
func (s *Switcher) Jump(n int) bool {
	if n < 1 || n > len(s.rows) {
		return false
	}
	s.sel = n - 1
	return true
}

// Selected is the highlighted pane row. It is always a rail.Pane item built
// from the AllPane, so a caller that reads Kind and Pane keeps working.
func (s *Switcher) Selected() (rail.RailItem, bool) {
	if s.sel < 0 || s.sel >= len(s.rows) {
		return rail.RailItem{}, false
	}
	return paneItem(s.rows[s.sel]), true
}

// Target is the pane id of the selected row, or empty when nothing is selected.
func (s *Switcher) Target() string {
	if s.sel < 0 || s.sel >= len(s.rows) {
		return ""
	}
	return s.rows[s.sel].ID
}

// SortMode is the table's current ordering.
func (s *Switcher) SortMode() SortMode {
	return s.mode
}

// CycleSort moves to the next ordering, keeps the selected pane selected, and
// returns the mode it landed on.
func (s *Switcher) CycleSort() SortMode {
	s.mode = s.mode.nextSort()
	prev := s.Target()
	s.rebuild(s.sel, prev)
	return s.mode
}

// PreviewRatio is the preview band's share of the body, in percent.
func (s *Switcher) PreviewRatio() int {
	return s.ratio
}

// ResizePreview widens or narrows the preview band by d percent, clamps it to
// its bounds, and returns the ratio it kept.
func (s *Switcher) ResizePreview(d int) int {
	s.ratio = clampPreviewRatio(s.ratio + d)
	return s.ratio
}

// SetPreviewShare applies the configured starting share of the body, in
// percent. Zero opens the dashboard on the table alone and leaves the peek key
// to show the band at its default share. A share inside the bounds is kept; one
// outside them is clamped into them.
func (s *Switcher) SetPreviewShare(pct int) {
	if pct <= 0 {
		s.previewOn = false
		return
	}
	s.previewOn = true
	s.ratio = clampPreviewRatio(pct)
}

// clampPreviewRatio holds the band's share between its bounds.
func clampPreviewRatio(pct int) int {
	if pct < previewRatioMin {
		return previewRatioMin
	}
	if pct > previewRatioMax {
		return previewRatioMax
	}
	return pct
}

// PreviewOn reports whether the preview band is visible: toggled on and with a
// float big enough to hold a table beside it.
func (s *Switcher) PreviewOn() bool {
	return s.previewOn && s.previewFits()
}

// TogglePreview turns the band on or off for the peek key and reports the new
// state.
func (s *Switcher) TogglePreview() bool {
	s.previewOn = !s.previewOn
	return s.previewOn
}

// PreviewOffset is how many rows the preview is scrolled up from the bottom of
// its content. It is zero at the newest row.
func (s *Switcher) PreviewOffset() int {
	return s.offset
}

// ScrollPreview moves the preview d rows, positive toward older rows, and
// clamps it between the newest row and the oldest one the read lines hold.
func (s *Switcher) ScrollPreview(d int) int {
	max := len(s.lines) - s.bodyRows()
	if max < 0 {
		max = 0
	}
	s.offset += d
	if s.offset < 0 {
		s.offset = 0
	}
	if s.offset > max {
		s.offset = max
	}
	return s.offset
}

// PreviewLines are the lines the server sent for the selected pane.
func (s *Switcher) PreviewLines() []string {
	return s.lines
}

// SetPreview records a preview request for a pane and drops the lines of the
// request before it, so a stale screen is never drawn. A new pane starts at the
// newest row of its screen.
func (s *Switcher) SetPreview(id int, pane string) {
	if pane != s.pane {
		s.offset = 0
	}
	s.asked, s.req, s.pane, s.lines = true, id, pane, nil
}

// PreviewDue reports whether the selected pane is not the one asked for, so the
// caller should ask again.
func (s *Switcher) PreviewDue() bool {
	id := s.Target()
	if id == "" {
		return false
	}
	return !s.asked || id != s.pane
}

// AcceptPreview takes the lines for the newest request and reports whether it
// did. A reply to an older request is refused.
func (s *Switcher) AcceptPreview(id int, lines []string) bool {
	if !s.asked || id != s.req {
		return false
	}
	s.lines = lines
	return true
}

// refilter rebuilds the rows after a query edit and lands the selection on the
// best match, so a typed query shows the nearest of its hits.
func (s *Switcher) refilter() {
	s.top = 0
	s.rebuild(s.sel, "")
}

// rebuild filters and sorts the rows, then takes a selection: keep is the pane
// id to hold when it survives, and old is the row index the reselect measured
// from when it does not.
func (s *Switcher) rebuild(old int, keep string) {
	s.rows = s.rows[:0]
	q := s.Query()
	for _, p := range s.all {
		if _, ok := matchPane(p, q); ok {
			s.rows = append(s.rows, p)
		}
	}
	s.sortRows()
	if keep != "" {
		for i, p := range s.rows {
			if p.ID == keep {
				s.sel = i
				return
			}
		}
	}
	s.sel = s.pick(old)
}

// sortRows orders the rows for the current sort mode. Every mode is stable, so
// panes the mode cannot tell apart keep the snapshot's order.
func (s *Switcher) sortRows() {
	switch s.mode {
	case SortProject:
		sort.SliceStable(s.rows, func(i, j int) bool {
			a, b := s.rows[i].Project, s.rows[j].Project
			if (a == "") != (b == "") {
				return a != ""
			}
			return strings.ToLower(a) < strings.ToLower(b)
		})
	case SortStatus:
		sort.SliceStable(s.rows, func(i, j int) bool {
			return statusRank(s.rows[i].Status) < statusRank(s.rows[j].Status)
		})
	default:
		sort.SliceStable(s.rows, func(i, j int) bool {
			return s.rows[i].StatusChangedAt > s.rows[j].StatusChangedAt
		})
	}
}

// statusRank orders the states for SortStatus: what wants attention first,
// then the reported states, then the ones that report nothing.
func statusRank(status string) int {
	switch status {
	case "blocked":
		return 0
	case "error":
		return 1
	case "working":
		return 2
	case "done":
		return 3
	case "idle":
		return 4
	case "unknown":
		return 5
	}
	return 6
}

// pick is the row a query edit selects: the best-scoring match, with the
// distance to the row selected before breaking a tie, so an edit keeps the
// operator near where they were.
func (s *Switcher) pick(old int) int {
	if len(s.rows) == 0 {
		return -1
	}
	q := s.Query()
	if q == "" {
		if old < 0 {
			return 0
		}
		if old >= len(s.rows) {
			return len(s.rows) - 1
		}
		return old
	}
	best := -1
	var bestScore switcherScore
	bestDist := 0
	for i, p := range s.rows {
		sc, _ := matchPane(p, q)
		dist := i - old
		if old < 0 {
			dist = i
		}
		if dist < 0 {
			dist = -dist
		}
		if best < 0 || sc.better(bestScore) || (sc == bestScore && dist < bestDist) {
			best, bestScore, bestDist = i, sc, dist
		}
	}
	return best
}

// hasPane reports whether a pane is still in the snapshot.
func (s *Switcher) hasPane(id string) bool {
	if id == "" {
		return false
	}
	for _, p := range s.all {
		if p.ID == id {
			return true
		}
	}
	return false
}

// previewFits reports whether the float has room for a table and a preview
// band side by side.
func (s *Switcher) previewFits() bool {
	return s.Rect.W >= previewMinCols && s.Rect.H >= previewMinRows
}

// bodyRows is how many rows the float's body holds: the box, the query row, the
// header and the footer taken out. A float that has not been measured has none.
func (s *Switcher) bodyRows() int {
	if s.Rect.H < 6 {
		return 0
	}
	return s.Rect.H - 5
}

// matchPane scores a pane against the query and reports whether it matches. The
// fields are the pane's own: its recorded name, its app, its OSC title, its
// branch, worktree and folder, its project and its id.
func matchPane(p wire.AllPane, q string) (switcherScore, bool) {
	if q == "" {
		return switcherScore{}, true
	}
	var best switcherScore
	found := false
	for _, field := range paneFields(p) {
		sc, ok := switcherField(field, q)
		if !ok {
			continue
		}
		if !found || sc.better(best) {
			best, found = sc, true
		}
	}
	return best, found
}

// paneFields are the strings a pane row matches on.
func paneFields(p wire.AllPane) []string {
	return []string{p.Name, p.App, p.Title, p.Branch, p.Worktree, p.Cwd, p.Project, p.ID}
}

// paneItem is the rail item a selected pane hands back. The client reads Kind
// and Pane from it, and the label, state and folder are there for a caller that
// wants to describe the pane.
func paneItem(p wire.AllPane) rail.RailItem {
	label := paneLabel(p)
	it := rail.RailItem{
		Kind: rail.Pane, Pane: p.ID, Text: label, PaneName: label,
		Status: p.Status, Message: p.Message, Agent: p.Agent,
		Project: p.Project,
		Folder:  rail.RailFolder(p.Cwd, p.Root), Cwd: p.Cwd,
		Branch: p.Branch, Worktree: p.Worktree,
		Added: p.Added, Deleted: p.Deleted, Dirty: p.Dirty, InRepo: p.InRepo,
		Workspace: p.Workspace, Window: p.Window, WindowName: p.WindowName,
		Focus: p.Focus, Active: p.WorkspaceActive,
	}
	if p.Project != "" {
		it.ProjectAccent = rail.RailAccentSlot(p.Project)
	}
	return it
}

// paneLabel is the agent label: the recorded name, else the reporting app, else
// the pane id.
func paneLabel(p wire.AllPane) string {
	if p.Name != "" {
		return p.Name
	}
	if p.App != "" {
		return p.App
	}
	return p.ID
}

// paneState is the status cell's word and its theme style: a pane that never
// reported a state and is not an agent is a shell, and a pane that reports
// nothing at all is unknown.
func paneState(p wire.AllPane) string {
	if p.Status != "" {
		return p.Status
	}
	if p.Agent {
		return "unknown"
	}
	return "shell"
}

// switcherClock is how long ago a status change was: seconds under a minute,
// then minutes, hours and days. A pane the server has no stamp for reads "-".
func switcherClock(at, now int64) string {
	if at <= 0 {
		return "-"
	}
	d := now - at
	if d < 0 {
		d = 0
	}
	switch {
	case d < 60:
		return strconv.FormatInt(d, 10) + "s"
	case d < 3600:
		return strconv.FormatInt(d/60, 10) + "m"
	case d < 86400:
		return strconv.FormatInt(d/3600, 10) + "h"
	}
	return strconv.FormatInt(d/86400, 10) + "d"
}

// switcherNow is the clock the table reads. It is a var so a test can stop the
// clock instead of racing it.
var switcherNow = time.Now

// switcherScore ranks how well a pane matches: quality 0 is a field prefix, 1 a
// substring, 2 a subsequence, and a shorter field then an earlier match break a
// tie.
type switcherScore struct {
	quality int
	length  int
	at      int
}

// better reports whether a ranks above b.
func (a switcherScore) better(b switcherScore) bool {
	if a.quality != b.quality {
		return a.quality < b.quality
	}
	if a.length != b.length {
		return a.length < b.length
	}
	return a.at < b.at
}

const (
	switcherPrefix = iota
	switcherSubstring
	switcherSubsequence
)

// switcherField scores the query against one field, case-insensitively.
func switcherField(field, q string) (switcherScore, bool) {
	h := strings.ToLower(field)
	if h == "" {
		return switcherScore{}, false
	}
	if at := strings.Index(h, q); at == 0 {
		return switcherScore{quality: switcherPrefix, length: utf8.RuneCountInString(field)}, true
	} else if at > 0 {
		return switcherScore{
			quality: switcherSubstring,
			length:  utf8.RuneCountInString(field),
			at:      utf8.RuneCountInString(h[:at]),
		}, true
	}
	if at, ok := switcherSubseq(h, q); ok {
		return switcherScore{
			quality: switcherSubsequence,
			length:  utf8.RuneCountInString(field),
			at:      at,
		}, true
	}
	return switcherScore{}, false
}

// switcherSubseq reports whether q is a subsequence of h and the rune index of
// the first rune it matched.
func switcherSubseq(h, q string) (int, bool) {
	qr := []rune(q)
	first, n := -1, 0
	for at, r := range []rune(h) {
		if n < len(qr) && r == qr[n] {
			if first < 0 {
				first = at
			}
			n++
		}
	}
	return first, n == len(qr) && len(qr) > 0
}
