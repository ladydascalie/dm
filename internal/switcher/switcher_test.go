package switcher

import (
	"reflect"
	"testing"
	"time"

	"dm/internal/layout"
	"dm/internal/rail"
	"dm/internal/wire"
)

// switcherTestNow is the second the fixture's status stamps are measured from.
// The clock is frozen to it, so every age column reads the same in every run.
const switcherTestNow = int64(1_700_000_000)

// freezeSwitcherClock stops the table's clock at a fixed second.
func freezeSwitcherClock(t *testing.T) {
	t.Helper()
	old := switcherNow
	switcherNow = func() time.Time { return time.Unix(switcherTestNow, 0) }
	t.Cleanup(func() { switcherNow = old })
}

// switcherSnap is a session for the dashboard: five panes across two projects
// and one outside any project. The stamps, projects and states are shuffled so
// each sort mode gives its own order, and every column has something to show:
// a name, an app in place of a name, an id in place of both, a dirty tree, a
// clean one, a pane outside git, and a long title.
func switcherSnap() *wire.Snapshot {
	now := switcherTestNow
	return &wire.Snapshot{
		Cols: 160, Rows: 30, Focus: "w1p1",
		All: []wire.AllPane{
			{
				ID: "w1p1", Name: "serve", Project: "dm", App: "pi",
				Title: "dm server", Status: "working", Agent: true,
				StatusChangedAt: now - 30,
				Workspace:       "s1", Window: "w1", Focus: true,
				Cwd: "/work/dm/api", Root: "/work/dm", Branch: "dm-api", InRepo: true,
				Added: 3, Deleted: 12,
			},
			{
				ID: "w2p1", Name: "build", Project: "web", App: "make",
				Title: "make all", Status: "blocked", Agent: true,
				StatusChangedAt: now - 270,
				Workspace:       "s1", Window: "w2",
				Cwd: "/work/dm/web", Root: "/work/dm", Branch: "web-work", Worktree: "web",
				InRepo: true,
			},
			{
				ID: "w2p2", Project: "dm", App: "nvim",
				Title: "api.go", Status: "done", Agent: true,
				StatusChangedAt: now - 7800,
				Workspace:       "s1", Window: "w2",
				Cwd: "/work/web", Root: "/work/web", Worktree: "web", InRepo: true,
				Added: 7,
			},
			{
				ID: "w3p1", Name: "docs", Project: "web",
				Status: "idle", Agent: true, StatusChangedAt: now - 3*86400 - 3600,
				Workspace: "s2", Window: "w3", Cwd: "/work/docs", Root: "/work/docs",
			},
			{
				ID: "w4p1", Title: "notes", Workspace: "s2", Window: "w4",
				Cwd: "/tmp",
			},
		},
	}
}

// switcherFull is the dashboard opened over the whole screen.
func switcherFull(cols, rows int) *Switcher {
	snap := switcherSnap()
	snap.Cols, snap.Rows = cols, rows
	return NewSwitcher(layout.Rect{W: cols, H: rows}, snap)
}

// switcherRowIDs lists the row panes in table order.
func switcherRowIDs(s *Switcher) []string {
	out := make([]string, len(s.rows))
	for i, p := range s.rows {
		out[i] = p.ID
	}
	return out
}

// switcherType types a whole query, which is what filter mode is for.
func switcherType(s *Switcher, q string) {
	s.StartFilter()
	for _, r := range q {
		s.AppendText(r)
	}
}

// TestSwitcherRows checks the dashboard lists every pane of the snapshot, in
// the snapshot's order under the default sort, and that the selected row is a
// pane item the client can pick.
func TestSwitcherRows(t *testing.T) {
	s := switcherFull(160, 30)
	if got, want := switcherRowIDs(s), []string{"w1p1", "w2p1", "w2p2", "w3p1", "w4p1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	if s.RowCount() != 5 {
		t.Fatalf("RowCount = %d, want 5", s.RowCount())
	}
	it, ok := s.Selected()
	if !ok || it.Kind != rail.Pane || it.Pane != "w1p1" {
		t.Fatalf("selected = %+v, %v, want the pane w1p1", it, ok)
	}
	if it.Text != "serve" || s.Target() != "w1p1" {
		t.Fatalf("selected label %q target %q, want serve and w1p1", it.Text, s.Target())
	}
	// The label falls back to the app, then to the pane id.
	s.Jump(3)
	if it, _ := s.Selected(); it.Text != "nvim" {
		t.Fatalf("label of a pane with only an app = %q, want nvim", it.Text)
	}
	s.Jump(5)
	if it, _ := s.Selected(); it.Text != "w4p1" {
		t.Fatalf("label of a pane with no name = %q, want the pane id", it.Text)
	}
	// An empty snapshot has no rows and no selection.
	s.Sync(&wire.Snapshot{})
	if s.RowCount() != 0 || s.Target() != "" {
		t.Fatalf("empty snapshot keeps %d rows, target %q", s.RowCount(), s.Target())
	}
	if _, ok := s.Selected(); ok {
		t.Fatal("an empty snapshot still selects a row")
	}
}

// TestSwitcherFilter holds the query to the pane's own fields and lands the
// selection on the best match.
func TestSwitcherFilter(t *testing.T) {
	cases := []struct {
		query string
		want  []string
		pick  string
	}{
		{query: "serve", want: []string{"w1p1"}, pick: "w1p1"},
		{query: "nvim", want: []string{"w2p2"}, pick: "w2p2"},
		{query: "api.go", want: []string{"w2p2"}, pick: "w2p2"},
		{query: "dm-api", want: []string{"w1p1"}, pick: "w1p1"},
		{query: "web-work", want: []string{"w2p1"}, pick: "w2p1"},
		{query: "/work/docs", want: []string{"w3p1"}, pick: "w3p1"},
		// The project, the branch and the folder all count.
		{query: "dm", want: []string{"w1p1", "w2p1", "w2p2"}, pick: "w1p1"},
		// A subsequence finds a pane too.
		{query: "mk", want: []string{"w2p1"}, pick: "w2p1"},
		// Nothing matches: no rows, no selection.
		{query: "zzz", want: nil, pick: ""},
	}
	for _, c := range cases {
		s := switcherFull(160, 30)
		switcherType(s, c.query)
		got := switcherRowIDs(s)
		if len(got) == 0 {
			got = nil
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("query %q keeps %v, want %v", c.query, got, c.want)
		}
		if s.Target() != c.pick {
			t.Fatalf("query %q selects %q, want %q", c.query, s.Target(), c.pick)
		}
	}
	// The case of the query and of the field do not matter.
	s := switcherFull(160, 30)
	switcherType(s, "SERVE")
	if got := switcherRowIDs(s); !reflect.DeepEqual(got, []string{"w1p1"}) {
		t.Fatalf("upper-case query keeps %v", got)
	}
	if s.Query() != "serve" {
		t.Fatalf("Query = %q, want the lower-case form", s.Query())
	}
}

// TestSwitcherBackspace drops rows with the query and gets them back when the
// query shrinks to nothing.
func TestSwitcherBackspace(t *testing.T) {
	s := switcherFull(160, 30)
	all := switcherRowIDs(s)
	switcherType(s, "dm")
	if got := switcherRowIDs(s); !reflect.DeepEqual(got, []string{"w1p1", "w2p1", "w2p2"}) {
		t.Fatalf("query dm keeps %v", got)
	}
	s.Backspace()
	if got, want := switcherRowIDs(s), []string{"w1p1", "w2p1", "w2p2", "w3p1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("backspace to d keeps %v, want %v", got, want)
	}
	s.Backspace()
	if got := switcherRowIDs(s); !reflect.DeepEqual(got, all) {
		t.Fatalf("backspace on an empty query keeps %v, want %v", got, all)
	}
	switcherType(s, "zzz")
	s.ClearQuery()
	if got := switcherRowIDs(s); !reflect.DeepEqual(got, all) {
		t.Fatalf("clearQuery keeps %v, want %v", got, all)
	}
}

// TestSwitcherFilterMode holds the mode's rule: the query only filters while
// filter mode is on, and leaving the mode drops it so the table is whole again.
// Outside the mode a letter is a key, so text never narrows the table there.
func TestSwitcherFilterMode(t *testing.T) {
	s := switcherFull(160, 30)
	all := switcherRowIDs(s)
	if s.Filtering() {
		t.Fatal("the dashboard opens outside filter mode")
	}
	// Text outside the mode is not text: the client routes letters to actions,
	// and the model refuses the edit either way.
	s.AppendText('d')
	s.Backspace()
	s.ClearQuery()
	if s.Filtering() || s.Search.Input.String() != "" || s.RowCount() != len(all) {
		t.Fatalf("typing outside the mode changed the table: query %q rows %d", s.Query(), s.RowCount())
	}
	s.StartFilter()
	if !s.Filtering() {
		t.Fatal("StartFilter did not enter the mode")
	}
	s.AppendText('d')
	if got, want := switcherRowIDs(s), []string{"w1p1", "w2p1", "w2p2", "w3p1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("query d keeps %v, want %v", got, want)
	}
	if s.Query() != "d" {
		t.Fatalf("Query = %q, want d", s.Query())
	}
	// A second StartFilter keeps the query it already has.
	s.StartFilter()
	if s.Query() != "d" {
		t.Fatalf("a second StartFilter cleared the query to %q", s.Query())
	}
	// Leaving the mode drops the query and shows every pane again.
	s.StopFilter()
	if s.Filtering() || s.Query() != "" {
		t.Fatalf("StopFilter left filtering %v and query %q", s.Filtering(), s.Query())
	}
	if got := switcherRowIDs(s); !reflect.DeepEqual(got, all) {
		t.Fatalf("after StopFilter the table keeps %v, want %v", got, all)
	}
	// StopFilter outside the mode is a no-op.
	s.StopFilter()
	if s.Filtering() || s.RowCount() != len(all) {
		t.Fatal("StopFilter outside the mode changed the table")
	}
	// Starting the mode again begins from an empty query, so one keystroke
	// narrows a whole table.
	switcherType(s, "serve")
	if got := switcherRowIDs(s); !reflect.DeepEqual(got, []string{"w1p1"}) {
		t.Fatalf("second filter keeps %v, want w1p1", got)
	}
}

// TestSwitcherDeleteWord cuts the word before the caret, spaces in front of it
// included, and reselects. Outside filter mode ctrl+w is not text, so the
// query and the table stay as they are.
func TestSwitcherDeleteWord(t *testing.T) {
	s := switcherFull(160, 30)
	all := switcherRowIDs(s)
	s.DeleteWord()
	if s.Filtering() || s.Query() != "" || !reflect.DeepEqual(switcherRowIDs(s), all) {
		t.Fatalf("DeleteWord outside the mode changed the table: query %q", s.Query())
	}
	switcherType(s, "dock one")
	s.DeleteWord()
	if got := s.Query(); got != "dock " {
		t.Fatalf("after one delete the query = %q, want %q", got, "dock ")
	}
	s.DeleteWord()
	if got := s.Query(); got != "" {
		t.Fatalf("after two deletes the query = %q, want empty", got)
	}
	if got := switcherRowIDs(s); !reflect.DeepEqual(got, all) {
		t.Fatalf("an empty query keeps %v, want every row %v", got, all)
	}
}

// TestSwitcherMove walks the selection and holds at both ends.
func TestSwitcherMove(t *testing.T) {
	s := switcherFull(160, 30)
	var walked []string
	for i := 0; i < s.RowCount()+2; i++ {
		walked = append(walked, s.Target())
		s.Move(1)
	}
	want := []string{"w1p1", "w2p1", "w2p2", "w3p1", "w4p1", "w4p1", "w4p1"}
	if !reflect.DeepEqual(walked, want) {
		t.Fatalf("walked %v, want %v", walked, want)
	}
	s.Move(-2)
	if s.Target() != "w2p2" {
		t.Fatalf("two rows up = %q, want w2p2", s.Target())
	}
	s.Move(-30)
	if s.Target() != "w1p1" {
		t.Fatalf("up past the top = %q, want w1p1", s.Target())
	}
}

// TestSwitcherJump checks the 1-9 jump keys select the n'th row.
func TestSwitcherJump(t *testing.T) {
	s := switcherFull(160, 30)
	if !s.Jump(2) || s.Target() != "w2p1" {
		t.Fatalf("jump 2 selects %q, want w2p1", s.Target())
	}
	if !s.Jump(5) || s.Target() != "w4p1" {
		t.Fatalf("jump 5 selects %q, want w4p1", s.Target())
	}
	for _, n := range []int{0, -1, 6, 9} {
		if s.Jump(n) {
			t.Fatalf("jump %d found a row", n)
		}
	}
	if s.Target() != "w4p1" {
		t.Fatalf("a refused jump moved the selection to %q", s.Target())
	}
	// A query shortens the table, and the jump numbers follow it.
	switcherType(s, "dm")
	if !s.Jump(3) || s.Target() != "w2p2" {
		t.Fatalf("jump 3 over three rows selects %q, want w2p2", s.Target())
	}
	if s.Jump(4) {
		t.Fatal("jump 4 found a row in a three-row table")
	}
}

// TestSwitcherSort walks the three orderings and keeps the selected pane.
func TestSwitcherSort(t *testing.T) {
	s := switcherFull(160, 30)
	if s.SortMode() != SortRecent {
		t.Fatalf("default sort = %v, want recent", s.SortMode())
	}
	if got, want := switcherRowIDs(s), []string{"w1p1", "w2p1", "w2p2", "w3p1", "w4p1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("recent order = %v, want %v", got, want)
	}
	s.Jump(3)
	if s.CycleSort() != SortProject {
		t.Fatalf("first cycle = %v, want project", s.SortMode())
	}
	if got, want := switcherRowIDs(s), []string{"w1p1", "w2p2", "w2p1", "w3p1", "w4p1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("project order = %v, want %v", got, want)
	}
	if s.Target() != "w2p2" {
		t.Fatalf("project sort moved the selection to %q, want w2p2", s.Target())
	}
	if s.CycleSort() != SortStatus {
		t.Fatalf("second cycle = %v, want status", s.SortMode())
	}
	if got, want := switcherRowIDs(s), []string{"w2p1", "w1p1", "w2p2", "w3p1", "w4p1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("status order = %v, want %v", got, want)
	}
	if s.CycleSort() != SortRecent {
		t.Fatalf("third cycle = %v, want recent", s.SortMode())
	}
	if got, want := switcherRowIDs(s), []string{"w1p1", "w2p1", "w2p2", "w3p1", "w4p1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("recent order again = %v, want %v", got, want)
	}
}

// TestSwitcherProjectSortByID proves the project sort puts the panes of one
// project in pane id order, whatever order the snapshot lists them in, and
// reads the id's numbers as numbers: w10p1 comes after w9p1, not before it.
func TestSwitcherProjectSortByID(t *testing.T) {
	snap := &wire.Snapshot{Cols: 160, Rows: 30, All: []wire.AllPane{
		{ID: "w10p1", Project: "dm"},
		{ID: "w2p2", Project: "web"},
		{ID: "w9p2", Project: "dm"},
		{ID: "w9p1", Project: "dm"},
		{ID: "w2p1", Project: "web"},
		{ID: "w1p1", Title: "notes"},
	}}
	s := NewSwitcher(layout.Rect{W: 160, H: 30}, snap)
	if s.CycleSort() != SortProject {
		t.Fatalf("first cycle = %v, want project", s.SortMode())
	}
	if got, want := switcherRowIDs(s), []string{"w9p1", "w9p2", "w10p1", "w2p1", "w2p2", "w1p1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("project order = %v, want %v", got, want)
	}
}

// TestSwitcherSync keeps the selection on its pane across a snapshot and picks
// the nearest row when the pane is gone.
func TestSwitcherSync(t *testing.T) {
	snap := switcherSnap()
	s := NewSwitcher(layout.Rect{W: 160, H: 30}, snap)
	s.Jump(3)
	if s.Target() != "w2p2" {
		t.Fatalf("jump 3 selects %q", s.Target())
	}
	s.Sync(switcherSnap())
	if s.Target() != "w2p2" {
		t.Fatalf("sync moved the selection to %q", s.Target())
	}
	// The pane is gone: the row that took its place is selected.
	cp := *snap
	cp.All = append(append([]wire.AllPane{}, snap.All[:2]...), snap.All[3:]...)
	s.Sync(&cp)
	if s.Target() != "w3p1" {
		t.Fatalf("sync without the selected pane selects %q, want w3p1", s.Target())
	}
	if s.RowCount() != 4 {
		t.Fatalf("rows after the pane went = %d, want 4", s.RowCount())
	}
}

// TestSwitcherPreview takes only the reply to the newest request, asks again
// when the selection moves, and starts a new pane at its newest row.
func TestSwitcherPreview(t *testing.T) {
	s := switcherFull(160, 30)
	if !s.PreviewDue() {
		t.Fatal("the preview is not due before anything was asked for")
	}
	s.SetPreview(7, "w1p1")
	if s.PreviewDue() {
		t.Fatal("the preview is due for the pane already asked for")
	}
	if s.AcceptPreview(6, []string{"stale"}) {
		t.Fatal("a reply to an older request was accepted")
	}
	if s.PreviewLines() != nil {
		t.Fatalf("stale reply left lines: %v", s.PreviewLines())
	}
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = "line"
	}
	if !s.AcceptPreview(7, lines) {
		t.Fatal("the reply to the newest request was refused")
	}
	if got := s.PreviewLines(); !reflect.DeepEqual(got, lines) {
		t.Fatalf("lines = %v, want the accepted reply", got)
	}
	// Scrolling the pane's screen and then moving to another pane resets it.
	s.ScrollPreview(10)
	if s.PreviewOffset() == 0 {
		t.Fatal("the preview did not scroll on a long screen")
	}
	s.Move(1)
	if !s.PreviewDue() {
		t.Fatal("the preview is not due for a new pane")
	}
	s.SetPreview(8, "w2p1")
	if s.PreviewLines() != nil || s.PreviewOffset() != 0 {
		t.Fatalf("a new request kept lines %v or offset %d", s.PreviewLines(), s.PreviewOffset())
	}
	if s.AcceptPreview(7, []string{"late"}) {
		t.Fatal("a reply to the replaced request was accepted")
	}
	if s.PreviewDue() {
		t.Fatal("the preview is due for the pane just asked for")
	}
	// A pane that leaves the snapshot takes its preview with it.
	cp := *switcherSnap()
	cp.All = append(append([]wire.AllPane{}, cp.All[:1]...), cp.All[2:]...)
	s.Sync(&cp)
	if s.PreviewLines() != nil {
		t.Fatalf("a gone pane kept the preview lines: %v", s.PreviewLines())
	}
	if !s.PreviewDue() {
		t.Fatal("the preview is not due after its pane went")
	}
}

// TestSwitcherPreviewRatio checks the band's default share and its bounds.
func TestSwitcherPreviewRatio(t *testing.T) {
	s := switcherFull(160, 30)
	if s.PreviewRatio() != previewRatioDefault {
		t.Fatalf("default ratio = %d, want %d", s.PreviewRatio(), previewRatioDefault)
	}
	if got := s.ResizePreview(100); got != previewRatioMax {
		t.Fatalf("a wide resize = %d, want the %d cap", got, previewRatioMax)
	}
	if got := s.ResizePreview(-100); got != previewRatioMin {
		t.Fatalf("a narrow resize = %d, want the %d floor", got, previewRatioMin)
	}
	s.ResizePreview(10)
	if got := s.PreviewRatio(); got != previewRatioMin+10 {
		t.Fatalf("ratio after a step = %d, want %d", got, previewRatioMin+10)
	}
}

// TestSwitcherPreviewToggle checks the peek key and the small-float rule that
// hides the band whatever the toggle says.
func TestSwitcherPreviewToggle(t *testing.T) {
	s := switcherFull(160, 30)
	if !s.PreviewOn() {
		t.Fatal("the preview is off by default")
	}
	if s.TogglePreview() || s.PreviewOn() {
		t.Fatal("the peek key did not turn the preview off")
	}
	if !s.TogglePreview() || !s.PreviewOn() {
		t.Fatal("the peek key did not turn the preview back on")
	}
	// A screen shorter than ten rows drops the band, and so does one narrower
	// than sixty columns. The dashboard owns the whole screen, so a small
	// screen is what makes a small float.
	for _, size := range [][2]int{{160, 9}, {59, 30}} {
		snap := switcherSnap()
		snap.Cols, snap.Rows = size[0], size[1]
		small := NewSwitcher(layout.Rect{W: size[0], H: size[1]}, snap)
		if small.PreviewOn() {
			t.Fatalf("a %dx%d screen keeps the preview", size[0], size[1])
		}
	}
}

// TestSwitcherScrollPreview checks the scroll clamps to the lines the model
// holds and to the bottom of them.
func TestSwitcherScrollPreview(t *testing.T) {
	s := switcherFull(160, 30)
	s.SetPeekDown(false)
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "line"
	}
	s.SetPreview(1, "w1p1")
	s.AcceptPreview(1, lines)
	// The body holds rows minus the border, the query, the header and the
	// footer: 25 rows on a 30-row float, so 75 rows can scroll.
	body := s.bodyRows()
	if body != 25 {
		t.Fatalf("body rows = %d, want 25", body)
	}
	if got := s.ScrollPreview(1000); got != len(lines)-body {
		t.Fatalf("scroll up = %d, want %d", got, len(lines)-body)
	}
	if got := s.ScrollPreview(1); got != len(lines)-body {
		t.Fatalf("scroll past the top = %d, want it to hold at %d", got, len(lines)-body)
	}
	if got := s.ScrollPreview(-1000); got != 0 {
		t.Fatalf("scroll down = %d, want the newest row", got)
	}
	// No lines at all, no scroll.
	s.SetPreview(2, "w2p1")
	if got := s.ScrollPreview(10); got != 0 {
		t.Fatalf("scroll with no preview = %d, want 0", got)
	}
}

// TestSwitcherClock pins the age column's buckets.
func TestSwitcherClock(t *testing.T) {
	now := switcherTestNow
	cases := []struct {
		at   int64
		want string
	}{
		{at: now, want: "0s"},
		{at: now - 5, want: "5s"},
		{at: now - 59, want: "59s"},
		{at: now - 60, want: "1m"},
		{at: now - 4*60 - 30, want: "4m"},
		{at: now - 3600, want: "1h"},
		{at: now - 2*3600 - 600, want: "2h"},
		{at: now - 86400, want: "1d"},
		{at: now - 3*86400 - 3600, want: "3d"},
		{at: now + 5, want: "0s"},
		{at: 0, want: "-"},
	}
	for _, c := range cases {
		if got := switcherClock(c.at, now); got != c.want {
			t.Fatalf("clock(%d) = %q, want %q", now-c.at, got, c.want)
		}
	}
}

// TestPaneItemPinnedAccent: the item carries the accent slot the server
// pinned, the hash only when the snapshot predates the field, so the switcher
// paints the hue the rail paints.
func TestPaneItemPinnedAccent(t *testing.T) {
	p := wire.AllPane{ID: "w1p1", Project: "a", ProjectAccent: 3, ProjectAccentSet: true}
	if it := paneItem(p); it.ProjectAccent != 3 {
		t.Fatalf("pinned ProjectAccent = %d, want 3", it.ProjectAccent)
	}
	p.ProjectAccentSet = false
	if it := paneItem(p); it.ProjectAccent != rail.RailAccentSlot("a") {
		t.Fatalf("fallback ProjectAccent = %d, want %d", it.ProjectAccent, rail.RailAccentSlot("a"))
	}
}

// TestSwitcherParseSortMode pins the saved names and the fallback: an unknown
// name is the default, so a config the client cannot read still opens recent.
func TestSwitcherParseSortMode(t *testing.T) {
	cases := []struct {
		in   string
		want SortMode
	}{
		{"recent", SortRecent},
		{"project", SortProject},
		{"status", SortStatus},
		{"", SortRecent},
		{"nonsense", SortRecent},
		{"Project", SortRecent},
		{" status", SortRecent},
	}
	for _, c := range cases {
		if got := ParseSortMode(c.in); got != c.want {
			t.Fatalf("ParseSortMode(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestSwitcherSetSort switches the ordering the way the client does after it
// reads the saved mode: the rows rebuild and the selected pane stays selected.
func TestSwitcherSetSort(t *testing.T) {
	s := switcherFull(160, 30)
	s.Jump(3)
	if s.Target() != "w2p2" {
		t.Fatalf("jump 3 selects %q, want w2p2", s.Target())
	}
	s.SetSort(SortProject)
	if s.SortMode() != SortProject {
		t.Fatalf("SetSort(project) left the mode at %v", s.SortMode())
	}
	if got, want := switcherRowIDs(s), []string{"w1p1", "w2p2", "w2p1", "w3p1", "w4p1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("project order = %v, want %v", got, want)
	}
	if s.Target() != "w2p2" {
		t.Fatalf("SetSort moved the selection to %q, want w2p2", s.Target())
	}
	s.SetSort(SortStatus)
	if got, want := switcherRowIDs(s), []string{"w2p1", "w1p1", "w2p2", "w3p1", "w4p1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("status order = %v, want %v", got, want)
	}
	if s.Target() != "w2p2" {
		t.Fatalf("SetSort moved the selection to %q, want w2p2", s.Target())
	}
	// Setting the mode it already has keeps both the rows and the selection.
	s.SetSort(SortStatus)
	if got := s.Target(); got != "w2p2" {
		t.Fatalf("a repeated SetSort selects %q, want w2p2", got)
	}
	s.SetSort(SortRecent)
	if got, want := switcherRowIDs(s), []string{"w1p1", "w2p1", "w2p2", "w3p1", "w4p1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("recent order again = %v, want %v", got, want)
	}
	// A saved mode read from a file is the same call.
	s.SetSort(ParseSortMode("project"))
	if s.SortMode() != SortProject {
		t.Fatalf("SetSort(ParseSortMode(project)) left the mode at %v", s.SortMode())
	}
}

// switcherShelfSnap is switcherSnap with the pane that leads the recent and the
// status ordering set aside: it would sort first on its own, so the shelf is the
// only thing that puts it last.
func switcherShelfSnap() *wire.Snapshot {
	snap := switcherSnap()
	for i := range snap.All {
		if snap.All[i].ID == "w1p1" {
			snap.All[i].Shelf = true
		}
	}
	return snap
}

// TestSwitcherShelfSortsLast proves a shelved row sorts after every live pane in
// every mode, whatever the mode's comparator says, and that ShelfStart is where
// it begins.
func TestSwitcherShelfSortsLast(t *testing.T) {
	want := map[SortMode][]string{
		SortRecent:  {"w2p1", "w2p2", "w3p1", "w4p1", "w1p1"},
		SortProject: {"w2p2", "w2p1", "w3p1", "w4p1", "w1p1"},
		SortStatus:  {"w2p1", "w2p2", "w3p1", "w4p1", "w1p1"},
	}
	snap := switcherShelfSnap()
	snap.Cols, snap.Rows = 160, 30
	s := NewSwitcher(layout.Rect{W: 160, H: 30}, snap)
	for _, mode := range []SortMode{SortRecent, SortProject, SortStatus} {
		s.SetSort(mode)
		got := switcherRowIDs(s)
		if !reflect.DeepEqual(got, want[mode]) {
			t.Fatalf("%v order = %v, want %v", mode, got, want[mode])
		}
		if start := s.ShelfStart(); start != 4 {
			t.Fatalf("%v ShelfStart = %d, want 4", mode, start)
		}
		// Every row from ShelfStart on is shelved, and none before it.
		for i, id := range got {
			target := i >= 4
			if (id == "w1p1") != target {
				t.Fatalf("%v row %d (%s) sits on the wrong side of the shelf", mode, i, id)
			}
		}
	}
}

// TestSwitcherShelfStartWithNoShelf: a table with nothing shelved reports -1,
// and so does a query that keeps no shelved row.
func TestSwitcherShelfStartWithNoShelf(t *testing.T) {
	s := switcherFull(160, 30)
	if got := s.ShelfStart(); got != -1 {
		t.Fatalf("ShelfStart with no shelf = %d, want -1", got)
	}
	// A query that keeps a shelved row starts the shelf where that row is.
	shelf := switcherShelfSnap()
	shelf.Cols, shelf.Rows = 160, 30
	s = NewSwitcher(layout.Rect{W: 160, H: 30}, shelf)
	switcherType(s, "serve")
	if got := s.ShelfStart(); got != 0 {
		t.Fatalf("ShelfStart with only the shelved pane kept = %d, want 0", got)
	}
	// A query that keeps live rows only hides the shelf.
	switcherType(s, "docs")
	if got := s.ShelfStart(); got != -1 {
		t.Fatalf("ShelfStart with no shelved row kept = %d, want -1", got)
	}
}

// TestSwitcherSyncAsks records the panes the snapshot lists a question on, so
// the row can mark them, and clears the set with a snapshot that lists none.
func TestSwitcherSyncAsks(t *testing.T) {
	snap := switcherSnap()
	snap.Ask = []wire.Ask{{ID: "a1", Pane: "w2p1"}, {ID: "a2", Pane: "w3p1"}}
	s := NewSwitcher(layout.Rect{W: 160, H: 30}, snap)
	if !s.asks["w2p1"] || !s.asks["w3p1"] {
		t.Fatalf("asks = %v, want the two panes the snapshot lists", s.asks)
	}
	if s.asks["w1p1"] {
		t.Fatalf("asks = %v, want no mark on a pane without a question", s.asks)
	}
	// A snapshot that lists no question clears the set.
	s.Sync(switcherSnap())
	if len(s.asks) != 0 {
		t.Fatalf("asks after a snapshot with none = %v, want empty", s.asks)
	}
	// Answering the last question and syncing drops it, and a nil snapshot
	// drops every mark too.
	s.Sync(&wire.Snapshot{Cols: 160, Rows: 30, Ask: []wire.Ask{{ID: "a3", Pane: "w1p1"}}})
	if !s.asks["w1p1"] {
		t.Fatal("a new question did not mark its pane")
	}
	s.Sync(nil)
	if len(s.asks) != 0 {
		t.Fatalf("asks after a nil snapshot = %v, want empty", s.asks)
	}
}

// TestSwitcherPeekOrientation: the band sits under the table by default, which
// is what the shipped config asks for, and the right split stays available.
func TestSwitcherPeekOrientation(t *testing.T) {
	s := switcherFull(160, 30)
	if !s.PeekDown() {
		t.Fatal("the preview splits on the right by default")
	}
	s.SetPeekDown(false)
	if s.PeekDown() {
		t.Fatal("SetPeekDown(false) left the band under the table")
	}
	s.SetPeekDown(true)
	if !s.PeekDown() {
		t.Fatal("SetPeekDown(true) left the band beside the table")
	}
}

// TestSwitcherScrollPreviewDown checks the scroll clamps to the band under the
// table, whose height is the ratio's share of the body rather than the whole of
// it.
func TestSwitcherScrollPreviewDown(t *testing.T) {
	s := switcherFull(160, 30)
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "line"
	}
	s.SetPreview(1, "w1p1")
	s.AcceptPreview(1, lines)
	band := s.previewRows()
	if want := downBandRows(s.bodyRows(), previewRatioDefault); band != want || band != 15 {
		t.Fatalf("band rows = %d, want %d (15 for a 25-row body at 60%%)", band, want)
	}
	if got := s.ScrollPreview(1000); got != len(lines)-band {
		t.Fatalf("scroll up = %d, want %d", got, len(lines)-band)
	}
	if got := s.ScrollPreview(-1000); got != 0 {
		t.Fatalf("scroll down = %d, want the newest row", got)
	}
	// The band's share follows the ratio, so the clamp follows it too.
	s.ResizePreview(-20)
	if got, want := s.previewRows(), downBandRows(s.bodyRows(), s.PreviewRatio()); got != want {
		t.Fatalf("band rows at a narrower ratio = %d, want %d", got, want)
	}
	if got := s.ScrollPreview(1000); got != len(lines)-s.previewRows() {
		t.Fatalf("scroll at a narrower ratio = %d, want %d", got, len(lines)-s.previewRows())
	}
}

// TestSwitcherDownBandRows pins the band's floors: three table rows above the
// rule, three band rows below it, and no band in a body too short for both.
func TestSwitcherDownBandRows(t *testing.T) {
	cases := []struct {
		body  int
		ratio int
		want  int
	}{
		{body: 0, want: 0},
		{body: 6, want: 0},
		{body: 7, ratio: 60, want: 3},
		{body: 7, ratio: 85, want: 3},
		{body: 25, ratio: 60, want: 15},
		{body: 25, ratio: 20, want: 5},
		{body: 25, ratio: 85, want: 21},
		{body: 40, ratio: 85, want: 34},
	}
	for _, c := range cases {
		if got := downBandRows(c.body, c.ratio); got != c.want {
			t.Fatalf("downBandRows(%d, %d) = %d, want %d", c.body, c.ratio, got, c.want)
		}
		// The table always keeps its floor when a band exists.
		if band := downBandRows(c.body, c.ratio); band > 0 {
			if table := c.body - band - 1; table < 3 {
				t.Fatalf("downBandRows(%d, %d) leaves %d table rows", c.body, c.ratio, table)
			}
		}
	}
}
