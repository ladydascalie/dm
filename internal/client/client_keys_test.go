package client

import (
	"os"
	"path/filepath"
	"testing"

	"dm/internal/keymap"
	"dm/internal/testutil"
)

// TestSwitcherFilterTakesUTF8 proves the filter query takes a multi-byte rune
// whole, in one read or split across two, where the bytes used to be dropped
// byte by byte in silence.
func TestSwitcherFilterTakesUTF8(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	feed(tui, "/")
	if !tui.sw.Filtering() {
		t.Fatal("the filter key did not enter filter mode")
	}
	feed(tui, "caf\xc3\xa9")
	if q := tui.sw.Query(); q != "café" {
		t.Fatalf("query = %q, want café", q)
	}
	// A rune split across reads waits for its tail, and the tail joins it.
	feed(tui, "\xe2")
	if q := tui.sw.Query(); q != "café" {
		t.Fatalf("a split lead byte changed the query: %q", q)
	}
	if !tui.pending {
		t.Fatal("a split rune did not hold the dashboard")
	}
	feed(tui, "\x82\xac")
	if q := tui.sw.Query(); q != "café€" {
		t.Fatalf("query = %q, want café€", q)
	}
	testutil.NoMsg(t, got)
}

// TestSwitcherStaleCloseBinding proves a prefix remap retires the shipped
// ctrl+b close binding: ctrl+b is then just a key and must not close the
// dashboard too, while the prefix itself still does.
func TestSwitcherStaleCloseBinding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.kdl")
	if err := os.WriteFile(path, []byte("prefix \"ctrl+a\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := keymap.LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	tui, got := switcherTUI(t)
	tui.keymap = cfg
	feed(tui, "\x01a")
	if tui.sw == nil {
		t.Fatal("prefix a did not open the dashboard")
	}
	feed(tui, "\x02")
	if tui.sw == nil {
		t.Fatal("the stale ctrl+b binding closed the dashboard")
	}
	feed(tui, "\x01")
	if tui.sw != nil {
		t.Fatal("the prefix did not close the dashboard")
	}
	testutil.NoMsg(t, got)
}
