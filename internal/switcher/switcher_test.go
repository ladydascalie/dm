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

// switcherType types a whole query.
func switcherType(s *Switcher, q string) {
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
	// A shorter float than ten rows drops the band, and so does one narrower
	// than sixty columns.
	for _, r := range []layout.Rect{{W: 160, H: 9}, {W: 59, H: 30}} {
		small := NewSwitcher(r, switcherSnap())
		if small.PreviewOn() {
			t.Fatalf("float %+v keeps the preview", r)
		}
	}
}

// TestSwitcherScrollPreview checks the scroll clamps to the lines the model
// holds and to the bottom of them.
func TestSwitcherScrollPreview(t *testing.T) {
	s := switcherFull(160, 30)
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
