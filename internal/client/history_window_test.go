package client

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"dm/internal/keymap"
	"dm/internal/testutil"
	"dm/internal/wire"
)

// The scrollback window follows the history-lines config node: a reload that
// moves it drops every cached ring and the open tall terminal, so the next
// scroll asks for a fresh replay at the new height. A reload that keeps the
// value keeps the cache.

// writeHistoryConfig writes a config.kdl carrying only the scrollback window
// under dir, which the test sets as XDG_CONFIG_HOME.
func writeHistoryConfig(t *testing.T, dir string, lines int) {
	t.Helper()
	cfgDir := filepath.Join(dir, "dm")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	src := "history-lines " + strconv.Itoa(lines) + "\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.kdl"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestHistoryWindowFollowsConfig checks the loaded node becomes the client's
// scrollback window, and a tui with no config reports the built-in default.
func TestHistoryWindowFollowsConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeHistoryConfig(t, dir, 4096)

	c, _ := clientTUI(t, nil)
	if err := c.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if c.histRows != 4096 {
		t.Fatalf("histRows = %d, want 4096", c.histRows)
	}
	if got := c.histWindow(); got != 4096 {
		t.Fatalf("histWindow() = %d, want 4096", got)
	}

	// A tui that never loaded a config, like the frame tests, carries a zero
	// field and reports the default window.
	bare := &tui{}
	want := keymap.LoadDefault().HistoryLines()
	if got := bare.histWindow(); got != want {
		t.Fatalf("bare histWindow() = %d, want the default %d", got, want)
	}
}

// TestHistoryReloadDropsCacheOnWindowChange checks a reload that moves the
// window drops the ring of every pane and the open tall terminal, and the next
// scroll asks again and rebuilds at the new height.
func TestHistoryReloadDropsCacheOnWindowChange(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeHistoryConfig(t, dir, 100)

	c, sent := clientTUI(t, nil)
	c.histRows = 100
	p := c.paneByID("a")
	c.startScrollOnFocus()
	if m := testutil.NextMsg(t, sent); m.Action != "replay" || m.Target != "a" {
		t.Fatalf("first scroll sent %+v, want a replay request for a", m)
	}
	// The ring is longer than the window plus the pane at either height, so
	// the tall terminal stops at the window and proves the height it used.
	ring := []byte(strings.Repeat("ROW\r\n", 400))
	c.apply(wire.Msg{Type: "replay", Target: "a", Data: ring})
	if c.scrollVT == nil {
		t.Fatal("the replay did not fill the scrollback")
	}
	if got, want := tallRows(c), 100+p.Rect.H; got != want {
		t.Fatalf("tall terminal holds %d rows, want the 100-line window plus the pane: %d", got, want)
	}

	writeHistoryConfig(t, dir, 300)
	if err := c.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if m := testutil.NextMsg(t, sent); m.Action != "reload" {
		t.Fatalf("reload sent %+v, want a reload request for the server", m)
	}
	if c.histRows != 300 {
		t.Fatalf("histRows = %d, want 300", c.histRows)
	}
	if len(c.hist) != 0 || len(c.histMeta) != 0 {
		t.Fatalf("kept a cached ring across the window change: hist=%d meta=%d", len(c.hist), len(c.histMeta))
	}
	if c.scroll != "" || c.scrollVT != nil {
		t.Fatalf("kept a tall terminal at the old height: scroll=%q vt=%v", c.scroll, c.scrollVT != nil)
	}

	// The next scroll asks again, and the tall terminal is built at the new
	// window.
	c.startScrollOnFocus()
	if m := testutil.NextMsg(t, sent); m.Action != "replay" || m.Target != "a" {
		t.Fatalf("the scroll after the reload sent %+v, want a fresh replay request", m)
	}
	c.apply(wire.Msg{Type: "replay", Target: "a", Data: ring})
	if c.scrollVT == nil {
		t.Fatal("the fresh replay did not fill the scrollback")
	}
	if got, want := tallRows(c), 300+p.Rect.H; got != want {
		t.Fatalf("tall terminal holds %d rows after the reload, want the 300-line window plus the pane: %d", got, want)
	}
}

// TestHistoryReloadKeepsCacheOnSameWindow checks a reload that keeps the value
// keeps the cached ring and the open tall terminal.
func TestHistoryReloadKeepsCacheOnSameWindow(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeHistoryConfig(t, dir, 2000)

	c, sent := clientTUI(t, nil)
	c.histRows = 2000
	c.startScrollOnFocus()
	if m := testutil.NextMsg(t, sent); m.Action != "replay" {
		t.Fatalf("first scroll sent %+v", m)
	}
	ring := []byte(strings.Repeat("ROW\r\n", 40))
	c.apply(wire.Msg{Type: "replay", Target: "a", Data: ring})
	if c.scrollVT == nil {
		t.Fatal("the replay did not fill the scrollback")
	}

	if err := c.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if c.histRows != 2000 {
		t.Fatalf("histRows = %d, want 2000", c.histRows)
	}
	if got := string(c.hist["a"]); got != string(ring) {
		t.Fatalf("the reload dropped the ring: %d bytes, want %d", len(got), len(ring))
	}
	if _, ok := c.histMeta["a"]; !ok {
		t.Fatal("the reload dropped the cache key")
	}
	if c.scroll != "a" || c.scrollVT == nil {
		t.Fatalf("the reload closed scrollback: scroll=%q vt=%v", c.scroll, c.scrollVT != nil)
	}
}

// tallRows is how many rows the open scrollback terminal holds.
func tallRows(t *tui) int {
	if t.scrollVT == nil {
		return 0
	}
	_, r := t.scrollVT.Grid().Size()
	return r
}
