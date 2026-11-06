package keymapview

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"

	"github.com/hinshun/vt10x"
)

// keymapTestLoad parses a keymap from text. It is a copy of the client's
// helper: the moved test cannot reach package main.
func keymapTestLoad(t *testing.T, src string) *keymap.Config {
	t.Helper()
	cfg, err := keymap.ParseConfig(src)
	if err != nil {
		t.Fatalf("parse test keymap: %v", err)
	}
	return cfg
}

// keymapVTPaint is the content painter for a float backed by a terminal. It is
// a copy of the client's painter: the moved test cannot reach package main.
func keymapVTPaint(v vt10x.Terminal) func(*bytes.Buffer, layout.Rect) {
	return func(buf *bytes.Buffer, r layout.Rect) {
		if r.W <= 0 || r.H <= 0 {
			return
		}
		cols, rows := v.Size()
		v.Lock()
		defer v.Unlock()
		for y := 0; y < r.H; y++ {
			fmt.Fprintf(buf, "\x1b[%d;%dH", r.Y+y+1, r.X+1)
			var cur *vt10x.Glyph
			for x := 0; x < r.W; x++ {
				var g vt10x.Glyph
				if x < cols && y < rows {
					g = vt.NormCell(v.Cell(x, y))
				} else {
					g = vt.NormCell(g)
				}
				buf.WriteString(vt.SGR(g, cur))
				gg := g
				cur = &gg
				if g.Char < 0x20 {
					buf.WriteByte(' ')
				} else {
					buf.WriteRune(g.Char)
				}
			}
		}
	}
}

// keymapScreen replays the float's bytes into a fresh screen, so a test reads
// what a terminal would show.
func keymapScreen(t *testing.T, k *KeymapView, cols, rows int) vt10x.Terminal {
	t.Helper()
	var buf bytes.Buffer
	k.Float.Paint = keymapVTPaint(k.Float.VT)
	k.Float.Draw(&buf, theme.DefaultTheme())
	screen := vt10x.New(vt10x.WithSize(cols, rows))
	if _, err := screen.Write(buf.Bytes()); err != nil {
		t.Fatalf("replay the keymap view: %v", err)
	}
	return screen
}

// keymapScreenRow reads one row of a replayed screen, trailing blanks dropped.
func keymapScreenRow(screen vt10x.Terminal, y, x, w int) string {
	var b strings.Builder
	screen.Lock()
	defer screen.Unlock()
	for i := 0; i < w; i++ {
		r := screen.Cell(x+i, y).Char
		if r == 0 {
			r = ' '
		}
		b.WriteRune(r)
	}
	return strings.TrimRight(b.String(), " ")
}

// keymapScreenText reads every row of a replayed screen inside the float, so a
// test can look for a line wherever it landed.
func keymapScreenText(screen vt10x.Terminal, r layout.Rect) []string {
	rows := make([]string, 0, r.H)
	for y := r.Y; y < r.Y+r.H; y++ {
		rows = append(rows, keymapScreenRow(screen, y, r.X, r.W))
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

	wantMain := []keymap.KeymapAction{
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

	rows := KeymapRows(cfg)
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
	k := NewKeymapView(snap, keymap.LoadDefault())
	screen := keymapScreen(t, k, snap.Cols, snap.Rows)
	r := k.Float.Rect

	title := keymapScreenRow(screen, r.Y-1, r.X+2, r.W-2)
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
	hint := keymapScreenRow(screen, r.Y+r.H, r.X+2, r.W-2)
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
	k := NewKeymapView(snap, keymap.LoadDefault())
	h := k.Float.Rect.H
	max := len(k.rows) - h
	if max < 1 {
		t.Fatalf("fixture is too short to scroll: %d rows in %d rows", len(k.rows), h)
	}

	k.Scroll(1000)
	if k.off != max {
		t.Fatalf("off = %d, want the last row %d", k.off, max)
	}
	k.Scroll(-1000)
	if k.off != 0 {
		t.Fatalf("off = %d, want the first row 0", k.off)
	}

	// The paint shows the window that starts at the offset.
	k.Scroll(1)
	screen := keymapScreen(t, k, snap.Cols, snap.Rows)
	rows := keymapScreenText(screen, k.Float.Rect)
	if got := rows[1]; !strings.Contains(got, "[main]") {
		t.Fatalf("after one scroll the second row = %q, want the main header", got)
	}

	// A snapshot that changes the screen keeps the window on the rows.
	k.Scroll(1000)
	smaller := &wire.Snapshot{Cols: 100, Rows: 30, Rail: 0}
	k.Sync(smaller)
	if k.off > len(k.rows)-k.Float.Rect.H {
		t.Fatalf("sync left the window past the last row: off=%d rows=%d h=%d",
			k.off, len(k.rows), k.Float.Rect.H)
	}
}

// TestKeymapSearch: a query matches binding rows only, scrolls the first
// match into view, and next/prev move between matches.
func TestKeymapSearch(t *testing.T) {
	snap := &wire.Snapshot{Cols: 120, Rows: 40, Rail: 30}
	k := NewKeymapView(snap, keymap.LoadDefault())
	k.StartSearch()
	k.AppendQuery("split")
	if k.query != "split" || len(k.matches) < 2 {
		t.Fatalf("query %q matches %v, want at least two split rows", k.query, k.matches)
	}
	k.AcceptSearch()
	if got := k.title(); !strings.Contains(got, "1/") {
		t.Fatalf("title = %q, want the match position", got)
	}
	if got := k.hint(); !strings.Contains(got, "n/N") {
		t.Fatalf("hint = %q, want the next/prev keys", got)
	}
	first := k.matches[0]
	if first < k.off || first >= k.off+k.Float.Rect.H {
		t.Fatalf("first match %d is outside the window starting at %d", first, k.off)
	}
	k.NextMatch(1)
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
	k.CancelSearch()
	if k.query != "" || len(k.matches) != 0 {
		t.Fatalf("cancel left query %q matches %v", k.query, k.matches)
	}
}
