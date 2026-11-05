package main

import (
	"bytes"
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/wire"

	"github.com/hinshun/vt10x"
)

// keymapScreen replays the float's bytes into a fresh screen, so a test reads
// what a terminal would show.
func keymapScreen(t *testing.T, k *keymapView, cols, rows int) vt10x.Terminal {
	t.Helper()
	var buf bytes.Buffer
	k.float.Draw(&buf, DefaultTheme(), nil)
	screen := vt10x.New(vt10x.WithSize(cols, rows))
	if _, err := screen.Write(buf.Bytes()); err != nil {
		t.Fatalf("replay the keymap view: %v", err)
	}
	return screen
}

// keymapScreenText reads every row of a replayed screen inside the float, so a
// test can look for a line wherever it landed.
func keymapScreenText(screen vt10x.Terminal, r layout.Rect) []string {
	rows := make([]string, 0, r.H)
	for y := r.Y; y < r.Y+r.H; y++ {
		rows = append(rows, boxScreenRow(screen, y, r.X, r.W))
	}
	return rows
}

// keymapRowsContain reports whether any row contains want.
func keymapRowsContain(rows []string, want string) bool {
	for _, row := range rows {
		if strings.Contains(row, want) {
			return true
		}
	}
	return false
}

// TestKeymapsListsModes: only the modes the config carries are listed, in the
// fixed order, and each mode's actions follow the order it declares them in.
// A key with two terminal encodings is one action, and the row label spells the
// action name with spaces.
func TestKeymapsListsModes(t *testing.T) {
	cfg := keymapTestLoad(t, `
prefix "ctrl+a"

keymap "box" {
    "down" "j"
    "close" "q"
}

keymap "main" {
    "split-right" "|"
    "split-down" "-"
    "send-prefix" "prefix"
    "help" "?"
}

keymap "help" {
    "down" "j"
    "close" "esc"
}

keymap "scroll" {
    "enter" "enter"
    "backspace" "backspace"
    "clear" "ctrl+u"
}
`)
	got := cfg.Keymaps()
	var names []string
	for _, m := range got {
		names = append(names, m.Name)
	}
	if want := "main,scroll,box,help"; strings.Join(names, ",") != want {
		t.Fatalf("modes = %v, want %s", names, want)
	}

	wantMain := []KeymapAction{
		{Action: "split-right", Key: "|"},
		{Action: "split-down", Key: "-"},
		{Action: "send-prefix", Key: "ctrl+a"},
		{Action: "help", Key: "?"},
	}
	if len(got[0].Actions) != len(wantMain) {
		t.Fatalf("main actions = %+v, want %+v", got[0].Actions, wantMain)
	}
	for i, a := range got[0].Actions {
		if a != wantMain[i] {
			t.Fatalf("main action %d = %+v, want %+v", i, a, wantMain[i])
		}
	}

	// enter and backspace each have two encodings in one action.
	scroll := got[1]
	wantScroll := []string{"enter", "backspace", "clear"}
	if len(scroll.Actions) != len(wantScroll) {
		t.Fatalf("scroll actions = %+v, want %v", scroll.Actions, wantScroll)
	}
	for i, a := range scroll.Actions {
		if a.Action != wantScroll[i] {
			t.Fatalf("scroll action %d = %q, want %q", i, a.Action, wantScroll[i])
		}
	}

	rows := keymapRows(cfg)
	for _, label := range []string{"split right", "split down", "send prefix"} {
		if !keymapRowsContain(rows, label) {
			t.Fatalf("rows miss the label %q: %v", label, rows)
		}
	}
	for _, row := range rows {
		if strings.Contains(row, "split-right") {
			t.Fatalf("row %q keeps the action's hyphens", row)
		}
	}
	if !keymapRowsContain(rows, "ctrl+a") {
		t.Fatalf("rows miss the prefix display name: %v", rows)
	}
}

// TestKeymapViewPaints: the float carries the title, a mode header, a key and
// action row, and the key hint.
func TestKeymapViewPaints(t *testing.T) {
	snap := &wire.Snapshot{Cols: 120, Rows: 40, Rail: 30}
	k := newKeymapView(snap, LoadDefault())
	screen := keymapScreen(t, k, snap.Cols, snap.Rows)
	r := k.float.Rect

	title := boxScreenRow(screen, r.Y-1, r.X+2, r.W-2)
	if !strings.Contains(title, "keymaps") {
		t.Fatalf("title = %q, want keymaps", title)
	}
	rows := keymapScreenText(screen, r)
	if !keymapRowsContain(rows, "[main]") {
		t.Fatalf("rows miss the main header: %v", rows)
	}
	if !keymapRowsContain(rows, "split right") {
		t.Fatalf("rows miss a main binding: %v", rows)
	}
	for _, row := range k.rows {
		if strings.Contains(row, "scrollback") && keymapHeader(row) {
			t.Fatalf("scrollback row looks like a header: %q", row)
		}
	}
	hint := boxScreenRow(screen, r.Y+r.H, r.X+2, r.W-2)
	if !strings.Contains(hint, "j/k scroll") || !strings.Contains(hint, "esc close") {
		t.Fatalf("hint = %q, want the scroll and close keys", hint)
	}

	// The rect keeps clear of the rail and the screen edges.
	if r.X <= snap.Rail || r.X+r.W > snap.Cols || r.Y < 1 || r.Y+r.H >= snap.Rows {
		t.Fatalf("rect %+v does not clear the rail and the edges", r)
	}
	if r.W > keymapViewW {
		t.Fatalf("rect width = %d, want at most %d", r.W, keymapViewW)
	}
}

// TestKeymapViewScrollsAndClamps: the window stops at the first row and at the
// last row that still fills the rect, and the paint follows the offset.
func TestKeymapViewScrollsAndClamps(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 14, Rail: 0}
	k := newKeymapView(snap, LoadDefault())
	h := k.float.Rect.H
	max := len(k.rows) - h
	if max < 1 {
		t.Fatalf("fixture is too short to scroll: %d rows in %d rows", len(k.rows), h)
	}

	k.scroll(1000)
	if k.off != max {
		t.Fatalf("off = %d, want the last row %d", k.off, max)
	}
	k.scroll(-1000)
	if k.off != 0 {
		t.Fatalf("off = %d, want the first row 0", k.off)
	}

	// The paint shows the window that starts at the offset.
	k.scroll(1)
	screen := keymapScreen(t, k, snap.Cols, snap.Rows)
	rows := keymapScreenText(screen, k.float.Rect)
	if got := rows[1]; !strings.Contains(got, "[main]") {
		t.Fatalf("after one scroll the second row = %q, want the main header", got)
	}

	// A snapshot that changes the screen keeps the window on the rows.
	k.scroll(1000)
	smaller := &wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}
	k.sync(smaller)
	if k.off > len(k.rows)-k.float.Rect.H {
		t.Fatalf("sync left the window past the last row: off=%d rows=%d h=%d",
			k.off, len(k.rows), k.float.Rect.H)
	}
}

// TestKeymapSearch: a query matches binding rows only, scrolls the first
// match into view, and next/prev move between matches.
func TestKeymapSearch(t *testing.T) {
	snap := &wire.Snapshot{Cols: 120, Rows: 40, Rail: 30}
	k := newKeymapView(snap, LoadDefault())
	k.startSearch()
	k.appendQuery("split")
	if k.query != "split" || len(k.matches) < 2 {
		t.Fatalf("query %q matches %v, want at least two split rows", k.query, k.matches)
	}
	k.acceptSearch()
	if got := k.title(); !strings.Contains(got, "1/") {
		t.Fatalf("title = %q, want the match position", got)
	}
	if got := k.hint(); !strings.Contains(got, "n/N") {
		t.Fatalf("hint = %q, want the next/prev keys", got)
	}
	first := k.matches[0]
	if first < k.off || first >= k.off+k.float.Rect.H {
		t.Fatalf("first match %d is outside the window starting at %d", first, k.off)
	}
	k.nextMatch(1)
	if k.match != 1 {
		t.Fatalf("next match = %d, want 1", k.match)
	}
	active := k.matches[k.match]
	other := k.matches[0]
	if k.style(active) == k.style(other) {
		t.Fatalf("active and other match share style %q", k.style(active))
	}
	if k.style(active) != "\x1b[0m\x1b[30;103m" {
		t.Fatalf("active style = %q, want the yellow match", k.style(active))
	}
	if k.style(other) != "\x1b[0m\x1b[30;107m" {
		t.Fatalf("other match style = %q, want the white match", k.style(other))
	}
	k.cancelSearch()
	if k.query != "" || len(k.matches) != 0 {
		t.Fatalf("cancel left query %q matches %v", k.query, k.matches)
	}
}

// TestKeymapSearchInput: / starts a query, typing filters, enter keeps it, n
// moves to the next match, and esc clears it.
func TestKeymapSearchInput(t *testing.T) {
	tui := &tui{snap: testSnapshot(&layout.Node{Pane: "a"}, 100, 30, nil), keymap: LoadDefault()}
	tui.runAction("help")
	feed(tui, "/")
	if tui.keys == nil || !tui.keys.searching {
		t.Fatal("slash did not start a search")
	}
	if got := tui.keys.hint(); !strings.Contains(got, "enter keep") {
		t.Fatalf("search hint = %q, want enter keep", got)
	}
	for _, b := range []byte("split") {
		feed(tui, string(b))
	}
	if tui.keys.query != "split" {
		t.Fatalf("query = %q, want split", tui.keys.query)
	}
	feed(tui, "\r")
	if tui.keys.searching || tui.keys.query != "split" {
		t.Fatalf("enter left searching=%v query=%q", tui.keys.searching, tui.keys.query)
	}
	if got := tui.keys.hint(); !strings.Contains(got, "n/N") {
		t.Fatalf("match hint = %q, want n/N", got)
	}
	feed(tui, "n")
	if tui.keys.match != 1 {
		t.Fatalf("n left match %d, want 1", tui.keys.match)
	}
	feed(tui, "/")
	feed(tui, "x")
	feed(tui, "\x1b")
	if tui.keys.searching || tui.keys.query != "" {
		t.Fatalf("esc left searching=%v query=%q", tui.keys.searching, tui.keys.query)
	}
}

// TestHelpKeyFallback: a config without a help mode still scrolls and closes
// with the built-in help bindings.
func TestHelpKeyFallback(t *testing.T) {
	// The screen is short enough that the listed rows do not fit.
	tui := &tui{snap: testSnapshot(&layout.Node{Pane: "a"}, 100, 12, nil)}
	tui.keymap = keymapTestLoad(t, `
prefix "ctrl+a"

keymap "main" {
    "help" "?"
    "split-right" "|"
    "split-down" "-"
    "close-pane" "x"
    "next-pane" "n"
}
`)
	tui.runAction("help")
	if tui.keys == nil || tui.mode != "help" {
		t.Fatalf("the view did not open: keys=%v mode=%q", tui.keys, tui.mode)
	}
	max := len(tui.keys.rows) - tui.keys.float.Rect.H
	if max < 1 {
		t.Fatalf("fixture is too short to scroll: %d rows in %d rows",
			len(tui.keys.rows), tui.keys.float.Rect.H)
	}
	feed(tui, "j")
	if tui.keys == nil || tui.keys.off != 1 {
		t.Fatalf("j did not scroll the view: %+v", tui.keys)
	}
	feed(tui, "\x1b[6~")
	if tui.keys == nil || tui.keys.off != max {
		t.Fatalf("pgdn did not page the view to the last row %d: %+v", max, tui.keys)
	}
	feed(tui, "q")
	if tui.keys != nil || tui.mode != "" {
		t.Fatalf("q did not close the view: keys=%v mode=%q", tui.keys, tui.mode)
	}

	// The prefix closes the view too.
	tui.runAction("help")
	feed(tui, "\x01")
	if tui.keys != nil || tui.mode != "" {
		t.Fatalf("the prefix did not close the view: keys=%v mode=%q", tui.keys, tui.mode)
	}
}
