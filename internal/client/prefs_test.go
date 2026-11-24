package client

import (
	"os"
	"path/filepath"
	"testing"

	"dm/internal/keymap"
	"dm/internal/switcher"
	"dm/internal/testutil"
	"dm/internal/wire"
)

// prefsTUI is a client over a real prefs file in a temp directory, the way
// attachConn leaves one: the path is set, so the sort key writes it and opening
// the dashboard reads what it holds.
func prefsTUI(t *testing.T, p prefs) (*tui, chan wire.Msg, string) {
	t.Helper()
	tui, got := switcherTUI(t)
	path := filepath.Join(t.TempDir(), "prefs.json")
	if err := savePrefs(path, p); err != nil {
		t.Fatalf("savePrefs: %v", err)
	}
	tui.prefsPath = path
	tui.prefs = loadPrefs(path)
	return tui, got, path
}

// TestDashboardSortFromPrefs opens the dashboard on the ordering the prefs file
// saved. The snapshot's recent order and its status order disagree, so the row
// the dashboard selects proves the rows were built in the saved mode.
func TestDashboardSortFromPrefs(t *testing.T) {
	t.Parallel()
	tui, got, _ := prefsTUI(t, prefs{DashboardSort: "status"})
	tui.snap.All[0].Status, tui.snap.All[0].StatusChangedAt = "idle", 200
	tui.snap.All[1].Status, tui.snap.All[1].StatusChangedAt = "blocked", 100
	feed(tui, "\x02a")
	if tui.sw == nil {
		t.Fatal("prefix a did not open the switcher")
	}
	if m := tui.sw.SortMode(); m != switcher.SortStatus {
		t.Fatalf("the dashboard opened on %v, want the saved status mode", m)
	}
	if !tui.sw.Jump(1) {
		t.Fatal("the dashboard has no first row")
	}
	if it, ok := tui.sw.Selected(); !ok || it.Pane != "w1p2" {
		t.Fatalf("the first row is %+v, want w1p2, the blocked pane the saved mode puts first", it)
	}
	testutil.NoMsg(t, got)
}

// TestDashboardSortCycleWritesAndReopens: the sort key writes the mode it lands
// on, and the next open in the same client reads it back.
func TestDashboardSortCycleWritesAndReopens(t *testing.T) {
	t.Parallel()
	tui, got, path := prefsTUI(t, prefs{})
	feed(tui, "\x02a")
	if m := tui.sw.SortMode(); m != switcher.SortRecent {
		t.Fatalf("a fresh prefs file opened on %v, want recent", m)
	}
	feed(tui, "s")
	if m := tui.sw.SortMode(); m != switcher.SortProject {
		t.Fatalf("the sort key landed on %v, want project", m)
	}
	if got := loadPrefs(path).DashboardSort; got != "project" {
		t.Fatalf("prefs file holds %q, want project", got)
	}
	feed(tui, "\x02")
	if tui.sw != nil {
		t.Fatal("the second prefix press did not close the dashboard")
	}
	tui.prefs = loadPrefs(path)
	feed(tui, "\x02a")
	if m := tui.sw.SortMode(); m != switcher.SortProject {
		t.Fatalf("the reopen landed on %v, want the saved project mode", m)
	}
	testutil.NoMsg(t, got)
}

// TestDashboardSortMissingAndMalformed: a file that is absent or unreadable and
// one that does not parse both open the dashboard on recent.
func TestDashboardSortMissingAndMalformed(t *testing.T) {
	t.Parallel()
	malformed := filepath.Join(t.TempDir(), "prefs.json")
	if err := os.WriteFile(malformed, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"missing":   filepath.Join(t.TempDir(), "prefs.json"),
		"malformed": malformed,
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			tui, got := switcherTUI(t)
			tui.prefsPath = path
			tui.prefs = loadPrefs(path)
			feed(tui, "\x02a")
			if m := tui.sw.SortMode(); m != switcher.SortRecent {
				t.Fatalf("the dashboard opened on %v, want recent", m)
			}
			testutil.NoMsg(t, got)
		})
	}
}

// TestDashboardSortNoWrite: a client with no prefs path and a read-only client
// both cycle the ordering and write nothing.
func TestDashboardSortNoWrite(t *testing.T) {
	t.Parallel()
	t.Run("no path", func(t *testing.T) {
		tui, got := switcherTUI(t)
		feed(tui, "\x02a")
		feed(tui, "s")
		if m := tui.sw.SortMode(); m != switcher.SortProject {
			t.Fatalf("the sort key landed on %v, want project", m)
		}
		if tui.prefs.DashboardSort != "" {
			t.Fatalf("a client with no prefs path saved %q", tui.prefs.DashboardSort)
		}
		testutil.NoMsg(t, got)
	})
	t.Run("read only", func(t *testing.T) {
		tui, got, path := prefsTUI(t, prefs{})
		tui.readOnly = true
		feed(tui, "\x02a")
		feed(tui, "s")
		if m := tui.sw.SortMode(); m != switcher.SortProject {
			t.Fatalf("the sort key landed on %v, want project", m)
		}
		if got := loadPrefs(path).DashboardSort; got != "" {
			t.Fatalf("a read-only client saved %q", got)
		}
		testutil.NoMsg(t, got)
	})
}

// TestDashboardPeekSideFromConfig: the dashboard opens on the band side the
// config names, and on the down band when it names none.
func TestDashboardPeekSideFromConfig(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"absent", "", true},
		{"down", `dashboard-peek "down"`, true},
		{"right", `dashboard-peek "right"`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tui, got := switcherTUI(t)
			if c.src != "" {
				// The file is the load path, so the built-in keymap still
				// binds the key that opens the dashboard.
				path := filepath.Join(t.TempDir(), "config.kdl")
				if err := os.WriteFile(path, []byte(c.src+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				cfg, err := keymap.LoadPath(path)
				if err != nil {
					t.Fatalf("LoadPath: %v", err)
				}
				tui.keymap = cfg
			}
			feed(tui, "\x02a")
			if tui.sw == nil {
				t.Fatal("prefix a did not open the switcher")
			}
			if got := tui.sw.PeekDown(); got != c.want {
				t.Fatalf("PeekDown = %v, want %v", got, c.want)
			}
			testutil.NoMsg(t, got)
		})
	}
}

// TestSavePrefsReplacesAtomically proves the saved file is complete after a
// save over an existing file, and that the directory is private.
func TestSavePrefsReplacesAtomically(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "state")
	path := filepath.Join(dir, "prefs.json")
	if err := savePrefs(path, prefs{DashboardSort: "recent"}); err != nil {
		t.Fatalf("savePrefs: %v", err)
	}
	if err := savePrefs(path, prefs{DashboardSort: "project"}); err != nil {
		t.Fatalf("savePrefs over an existing file: %v", err)
	}
	if got := loadPrefs(path); got.DashboardSort != "project" {
		t.Fatalf("loadPrefs = %+v, want the project mode", got)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("prefs directory mode = %o, want 700", perm)
	}
	file, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := file.Mode().Perm(); perm != 0o600 {
		t.Fatalf("prefs file mode = %o, want 600", perm)
	}
	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 {
		t.Fatalf("prefs directory holds %d entries, want the file alone", len(left))
	}
}
