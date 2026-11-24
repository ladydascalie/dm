package keymapview

import (
	"bytes"
	"strings"
	"testing"

	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/testutil"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"
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
func keymapVTPaint(term *vt.Term) func(*bytes.Buffer, layout.Rect) {
	return func(buf *bytes.Buffer, r layout.Rect) {
		testutil.PaintTerm(buf, term, r, 0)
	}
}

// keymapScreen replays the float's bytes into a fresh screen, so a test reads
// what a terminal would show.
func keymapScreen(t *testing.T, k *KeymapView, cols, rows int) *vt.Term {
	t.Helper()
	var buf bytes.Buffer
	k.Float.Paint = keymapVTPaint(k.Float.VT)
	k.Float.Draw(&buf, theme.DefaultTheme())
	screen := vt.NewTerm(cols, rows)
	screen.Write(buf.Bytes())
	screen.Flush()
	return screen
}

// keymapScreenRow reads one row of a replayed screen, trailing blanks dropped.
func keymapScreenRow(screen *vt.Term, y, x, w int) string {
	var b strings.Builder
	for i := 0; i < w; i++ {
		c := screen.Grid().Cell(x+i, y).Content
		if c == "" {
			c = " "
		}
		b.WriteString(c)
	}
	return strings.TrimRight(b.String(), " ")
}

// keymapScreenText reads every row of a replayed screen inside the float, so a
// test can look for a line wherever it landed.
func keymapScreenText(screen *vt.Term, r layout.Rect) []string {
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
	hint := keymapScreenRow(screen, r.Y+r.H, r.X+1, r.W-2)
	if !strings.Contains(hint, "j/k  scroll") || !strings.Contains(hint, "esc  close") {
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
	if k.win.Off != max {
		t.Fatalf("off = %d, want the last row %d", k.win.Off, max)
	}
	k.Scroll(-1000)
	if k.win.Off != 0 {
		t.Fatalf("off = %d, want the first row 0", k.win.Off)
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
	if k.win.Off > len(k.rows)-k.Float.Rect.H {
		t.Fatalf("sync left the window past the last row: off=%d rows=%d h=%d",
			k.win.Off, len(k.rows), k.Float.Rect.H)
	}
}

// TestKeymapSearch: a query matches binding rows only, scrolls the first
// match into view, and next/prev move between matches.
func TestKeymapSearch(t *testing.T) {
	snap := &wire.Snapshot{Cols: 120, Rows: 40, Rail: 30}
	k := NewKeymapView(snap, keymap.LoadDefault())
	k.StartSearch()
	k.AppendQuery("split")
	if k.Search.Input.String() != "split" || len(k.Search.Matches) < 2 {
		t.Fatalf("query %q matches %v, want at least two split rows", k.Search.Input.String(), k.Search.Matches)
	}
	k.AcceptSearch()
	if got := k.title(); !strings.Contains(got, "1/") {
		t.Fatalf("title = %q, want the match position", got)
	}
	if got := k.hint().Text(k.Float.Rect.W-2, theme.DefaultTheme()); !strings.Contains(got, "n/N") {
		t.Fatalf("hint = %q, want the next/prev keys", got)
	}
	first := k.Search.Matches[0]
	if first < k.win.Off || first >= k.win.Off+k.Float.Rect.H {
		t.Fatalf("first match %d is outside the window starting at %d", first, k.win.Off)
	}
	k.NextMatch(1)
	if k.Search.Cursor != 1 {
		t.Fatalf("next match = %d, want 1", k.Search.Cursor)
	}
	active := k.Search.Matches[k.Search.Cursor]
	other := k.Search.Matches[0]
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
	if k.Search.Input.String() != "" || len(k.Search.Matches) != 0 {
		t.Fatalf("cancel left query %q matches %v", k.Search.Input.String(), k.Search.Matches)
	}
}

// TestKeymapSearchRuneBackspaceAndTypedTitle: backspace cuts one rune, not
// one byte, and the title shows the query as typed, not lowercased.
func TestKeymapSearchRuneBackspaceAndTypedTitle(t *testing.T) {
	snap := &wire.Snapshot{Cols: 120, Rows: 40, Rail: 30}
	k := NewKeymapView(snap, keymap.LoadDefault())
	k.StartSearch()
	k.AppendQuery("sär")
	if k.Search.Input.String() != "sär" {
		t.Fatalf("query = %q, want sär", k.Search.Input.String())
	}
	k.BackspaceQuery()
	if k.Search.Input.String() != "sä" {
		t.Fatalf("query after backspace = %q, want sä", k.Search.Input.String())
	}
	k.BackspaceQuery()
	if k.Search.Input.String() != "s" {
		t.Fatalf("query after two backspaces = %q, want s", k.Search.Input.String())
	}

	// The match test folds case; the title keeps what the operator typed.
	k.StartSearch()
	k.AppendQuery("Prefix")
	if len(k.Search.Matches) == 0 {
		t.Fatal("the case-folded match test found nothing for Prefix")
	}
	if got := k.title(); !strings.Contains(got, "/Prefix") {
		t.Fatalf("title = %q, want the typed query /Prefix", got)
	}
}

// TestKeymapSearchDeleteWord cuts the word before the caret, the spaces in
// front of it included, and refilters the binding rows.
func TestKeymapSearchDeleteWord(t *testing.T) {
	snap := &wire.Snapshot{Cols: 120, Rows: 40, Rail: 30}
	k := NewKeymapView(snap, keymap.LoadDefault())
	k.StartSearch()
	k.AppendQuery("split pa")
	k.DeleteWordQuery()
	if got := k.Search.Input.String(); got != "split " {
		t.Fatalf("after one delete the query = %q, want %q", got, "split ")
	}
	if len(k.Search.Matches) == 0 {
		t.Fatal("the refilter found no match for the remaining word")
	}
	k.DeleteWordQuery()
	if got := k.Search.Input.String(); got != "" {
		t.Fatalf("after two deletes the query = %q, want empty", got)
	}
	if len(k.Search.Matches) != 0 {
		t.Fatalf("an empty query kept %v, want no matches", k.Search.Matches)
	}
}

// TestKeymapViewHintNamesTheKeymap: the border hint names the help keymap's
// keys, so SetKeymap renames it in place when a reload rebinds one.
func TestKeymapViewHintNamesTheKeymap(t *testing.T) {
	snap := &wire.Snapshot{Cols: 120, Rows: 40, Rail: 30}
	cfg := keymapTestLoad(t, `keymap "help" {
		"down" "n"
		"up" "a"
		"top" "t"
		"bottom" "B"
		"search" "f"
		"close-esc" "ctrl+q"
	}`)
	k := NewKeymapView(snap, keymap.LoadDefault())
	wide := k.Float.Rect.W - 2
	if got, want := k.Float.Hint.Text(wide, theme.DefaultTheme()), " j/k  scroll │  g/G  ends │  /  search │  esc  close"; got != want {
		t.Fatalf("hint = %q, want %q", got, want)
	}
	k.SetKeymap(cfg)
	if got, want := k.Float.Hint.Text(wide, theme.DefaultTheme()), " n/a  scroll │  t/B  ends │  f  search │  ctrl+q  close"; got != want {
		t.Fatalf("hint after SetKeymap = %q, want %q", got, want)
	}
}
