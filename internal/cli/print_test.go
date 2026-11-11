package cli

// The table and row printers: what each column holds, how padding and
// elision work, and what a missing value prints as.

import (
	"os"
	"strings"
	"testing"

	"dm/internal/box"
	"encoding/json"

	"dm/internal/paths"
	"dm/internal/session"
	"dm/internal/transport"
	"dm/internal/wire"
)

// TestBoxPrintEmptyPrintsNothing proves an empty list prints no rows and no
// header.
func TestBoxPrintEmptyPrintsNothing(t *testing.T) {
	t.Parallel()
	out := captureStdout(t, func() { boxPrint(nil, 80) })
	if out != "" {
		t.Fatalf("boxPrint of no entries printed %q", out)
	}
}

// TestBoxPrintRows proves a list prints one row per entry, padded to the
// widest column, with no trailing spaces.
func TestBoxPrintRows(t *testing.T) {
	t.Parallel()
	entries := []box.Entry{
		{Seq: 1, Key: "lane/a", State: box.StateNote, From: "w1p1", Msg: "first"},
		{Seq: 10, Key: "lane/b", State: box.StateDone, From: "w2p1", Msg: "second"},
	}
	out := captureStdout(t, func() { boxPrint(entries, 0) })
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("boxPrint wrote %d rows: %q", len(lines), out)
	}
	if !strings.Contains(lines[0], "lane/a") || strings.Contains(lines[0], "  first") {
		t.Fatalf("row %q is wrong", lines[0])
	}
	if !strings.HasPrefix(lines[1], "10 ") {
		t.Fatalf("second row %q does not right-align the wider seq", lines[1])
	}
}

// TestBoxPrintElides proves a width cuts the message to the row budget and
// leaves an ellipsis where the text was cut.
func TestBoxPrintElides(t *testing.T) {
	t.Parallel()
	e := box.Entry{Seq: 1, Key: "k", State: box.StateNote, Msg: strings.Repeat("x", 60)}
	out := captureStdout(t, func() { boxPrint([]box.Entry{e}, 30) })
	if !strings.Contains(out, "…") {
		t.Fatalf("elided row has no ellipsis: %q", out)
	}
	if strings.Contains(out, strings.Repeat("x", 60)) {
		t.Fatalf("elided row carries the whole message: %q", out)
	}
}

// TestBoxPrintWatchersEmptyPrintsNothing proves an empty watch list prints
// nothing.
func TestBoxPrintWatchersEmptyPrintsNothing(t *testing.T) {
	t.Parallel()
	if out := captureStdout(t, func() { boxPrintWatchers(nil) }); out != "" {
		t.Fatalf("boxPrintWatchers of no watches printed %q", out)
	}
}

// TestBoxPrintWatchersRows proves each row names the pane, the filter, the
// cursor and the parked state, padded to the widest column.
func TestBoxPrintWatchersRows(t *testing.T) {
	t.Parallel()
	ws := []box.Watch{
		{Pane: "w1p1", Key: "lane/a", Cursor: 3},
		{Pane: "w10p1", KeyPrefix: "lane", Cursor: 120, Parked: true},
	}
	out := captureStdout(t, func() { boxPrintWatchers(ws) })
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("boxPrintWatchers wrote %q", out)
	}
	if !strings.Contains(lines[1], "lane*") || !strings.Contains(lines[1], "wait") {
		t.Fatalf("prefix watch row %q lost its star or parked state", lines[1])
	}
	if !strings.Contains(lines[0], "lane/a") || strings.Contains(lines[0], "wait") {
		t.Fatalf("exact watch row %q is wrong", lines[0])
	}
	if lines[0] != strings.TrimRight(lines[0], " ") {
		t.Fatalf("watch row %q carries trailing spaces", lines[0])
	}
}

// TestBoxFilter proves a prefix filter prints as the branch with a star, and
// an exact key prints bare.
func TestBoxFilter(t *testing.T) {
	t.Parallel()
	if got := boxFilter(box.Watch{KeyPrefix: "lane"}); got != "lane*" {
		t.Fatalf("boxFilter = %q, want lane*", got)
	}
	if got := boxFilter(box.Watch{Key: "lane/a"}); got != "lane/a" {
		t.Fatalf("boxFilter = %q, want the bare key", got)
	}
}

// TestPrintTableLastColumnUnpadded proves the last column carries no padding
// and no row carries trailing spaces.
func TestPrintTableLastColumnUnpadded(t *testing.T) {
	t.Parallel()
	out := captureStdout(t, func() {
		printTable([]string{"NAME", "STATE"}, [][]string{
			{"longname", "live"},
			{"a", "dead"},
		})
	})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for i, l := range lines {
		if l != strings.TrimRight(l, " ") {
			t.Fatalf("table row %d %q carries trailing spaces", i, l)
		}
	}
	if !strings.Contains(lines[0], "NAME") || !strings.Contains(lines[1], "longname") {
		t.Fatalf("table = %q", out)
	}
}

// TestPrintPaneTableProvesTheFallbacks proves the pane table stars the focus,
// names an unnamed pane shell or agent, and dashes empty cells.
func TestPrintPaneTableProvesTheFallbacks(t *testing.T) {
	t.Parallel()
	panes := []wire.AllPane{
		{ID: "w1p1", Agent: true, Workspace: "s1", Window: "w1", Focus: true},
		{ID: "w1p2", Status: "unknown", Workspace: "s1", Window: "w1",
			Cwd: "/tmp", Branch: "main"},
	}
	out := captureStdout(t, func() { printPaneTable(panes) })
	for _, want := range []string{"w1p1*", "agent", "shell", "-", "/tmp", "main"} {
		if !strings.Contains(out, want) {
			t.Fatalf("pane table misses %q: %q", want, out)
		}
	}
}

// TestPrintPaneTableNoPanes proves an empty tree says so instead of printing
// a bare header.
func TestPrintPaneTableNoPanes(t *testing.T) {
	t.Parallel()
	out := captureStdout(t, func() { printPaneTable(nil) })
	if strings.TrimSpace(out) != "no panes" {
		t.Fatalf("printPaneTable of no panes printed %q", out)
	}
}

// TestPaneLabelFallsBack proves the label chain: name, then app, then agent
// id, then "(shell)".
func TestPaneLabelFallsBack(t *testing.T) {
	t.Parallel()
	cases := []struct {
		p    wire.AllPane
		want string
	}{
		{wire.AllPane{ID: "w1p1", Name: "alpha"}, "alpha"},
		{wire.AllPane{ID: "w1p1", App: "vim"}, "vim"},
		{wire.AllPane{ID: "w1p1", Agent: true}, "w1p1"},
		{wire.AllPane{ID: "w1p1"}, "(shell)"},
	}
	for _, tc := range cases {
		if got := paneLabel(tc.p); got != tc.want {
			t.Errorf("paneLabel(%+v) = %q, want %q", tc.p, got, tc.want)
		}
	}
}

// TestPaneLabelAppendsDetails proves the label carries status, message,
// branch, worktree and the workspace-path suffix when they exist.
func TestPaneLabelAppendsDetails(t *testing.T) {
	t.Parallel()
	p := wire.AllPane{
		ID: "w1p1", Name: "alpha", Status: "working", Message: "on it",
		Branch: "main", Worktree: "w-cli",
		WorkspaceName: "main", WindowName: "one",
	}
	got := paneLabel(p)
	for _, want := range []string{"working", "on it", "main", "w-cli", "main > one"} {
		if !strings.Contains(got, want) {
			t.Errorf("paneLabel misses %q: %q", want, got)
		}
	}
}

// TestDashIfEmpty proves an empty cell prints as a dash, so a gap is visible.
func TestDashIfEmpty(t *testing.T) {
	t.Parallel()
	if got := dashIfEmpty(""); got != "-" {
		t.Fatalf("dashIfEmpty = %q", got)
	}
	if got := dashIfEmpty("x"); got != "x" {
		t.Fatalf("dashIfEmpty = %q", got)
	}
}

// TestSessionSummaryDeadFromState proves a dead session's row is read from
// its state file: counts, project and the focused pane's cwd.
// Stays serial: the row is read through session.LoadState, which resolves
// DM_STATE per call, and t.Setenv cannot run in a parallel test.
func TestSessionSummaryDeadFromState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DM_STATE", dir)
	st := &session.State{
		Name: "old", Active: 0,
		Workspaces: []*session.Workspace{{
			ID: "s1", Name: "main", Project: "dm", Active: 0,
			Windows: []*session.Window{{ID: "w1", Name: "one", Focus: "w1p1"}},
		}},
		Panes: map[string]*session.PaneState{"w1p1": {Cwd: "/tmp/dm"}},
	}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.SessionDir("old"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.StatePath("old"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	row := sessionSummary(transport.SessionInfo{Name: "old", Live: false})
	want := []string{"old", "dead", "1", "1", "1", "dm", "/tmp/dm"}
	for i, w := range want {
		if row[i] != w {
			t.Fatalf("sessionSummary row = %v, want %v", row, want)
		}
	}
}

// TestSessionSummaryDeadWithoutState proves a session with no state file
// still prints a row of zeros and dashes.
func TestSessionSummaryDeadWithoutState(t *testing.T) {
	t.Parallel()
	row := sessionSummary(transport.SessionInfo{Name: "ghost", Live: false})
	want := "ghost dead 0 0 0 - -"
	if got := strings.Join(row, " "); got != want {
		t.Fatalf("sessionSummary = %q, want %q", got, want)
	}
}
