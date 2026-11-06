package main

// The client side of the keymap float: the tests that drive it through a tui.
// The view's own tests live in internal/keymapview.

import (
	"bytes"
	"strings"
	"testing"

	"dm/internal/keymap"
	"dm/internal/keymapview"
	"dm/internal/layout"
	"dm/internal/theme"

	"github.com/hinshun/vt10x"
)

// keymapScreen replays the float's bytes into a fresh screen, so a test reads
// what a terminal would show.
func keymapScreen(t *testing.T, k *keymapview.KeymapView, cols, rows int) vt10x.Terminal {
	t.Helper()
	var buf bytes.Buffer
	k.Float.Paint = floatVTPaint(k.Float.VT, nil)
	k.Float.Draw(&buf, theme.DefaultTheme())
	screen := vt10x.New(vt10x.WithSize(cols, rows))
	if _, err := screen.Write(buf.Bytes()); err != nil {
		t.Fatalf("replay the keymap view: %v", err)
	}
	return screen
}

// keymapTitle reads the float's top border, which carries the live query and
// the position of the active match.
func keymapTitle(t *testing.T, k *keymapview.KeymapView, cols, rows int) string {
	t.Helper()
	r := k.Float.Rect
	return boxScreenRow(keymapScreen(t, k, cols, rows), r.Y-1, r.X+2, r.W-2)
}

// keymapHint reads the float's bottom border, which carries the key hint.
func keymapHint(t *testing.T, k *keymapview.KeymapView, cols, rows int) string {
	t.Helper()
	r := k.Float.Rect
	return boxScreenRow(keymapScreen(t, k, cols, rows), r.Y+r.H, r.X+2, r.W-2)
}

// TestKeymapSearchInput: / starts a query, typing filters, enter keeps it, n
// moves to the next match, and esc clears it.
func TestKeymapSearchInput(t *testing.T) {
	tui := &tui{snap: testSnapshot(&layout.Node{Pane: "a"}, 100, 30, nil), keymap: keymap.LoadDefault()}
	tui.runAction("help")
	feed(tui, "/")
	if tui.keys == nil || !tui.keys.Searching {
		t.Fatal("slash did not start a search")
	}
	if got := keymapHint(t, tui.keys, 100, 30); !strings.Contains(got, "enter keep") {
		t.Fatalf("search hint = %q, want enter keep", got)
	}
	for _, b := range []byte("split") {
		feed(tui, string(b))
	}
	if got := keymapTitle(t, tui.keys, 100, 30); !strings.Contains(got, "/split") {
		t.Fatalf("title = %q, want the live query", got)
	}
	feed(tui, "\r")
	if tui.keys.Searching {
		t.Fatal("enter left the query being typed")
	}
	if got := keymapTitle(t, tui.keys, 100, 30); !strings.Contains(got, "/split") {
		t.Fatalf("title = %q, want the query kept", got)
	}
	if got := keymapHint(t, tui.keys, 100, 30); !strings.Contains(got, "n/N") {
		t.Fatalf("match hint = %q, want n/N", got)
	}
	feed(tui, "n")
	if got := keymapTitle(t, tui.keys, 100, 30); !strings.Contains(got, "2/") {
		t.Fatalf("title = %q, want the second match", got)
	}
	feed(tui, "/")
	feed(tui, "x")
	feed(tui, "\x1b")
	if tui.keys.Searching {
		t.Fatal("esc left the query being typed")
	}
	if got := keymapTitle(t, tui.keys, 100, 30); strings.Contains(got, "/") {
		t.Fatalf("title = %q, want the query cleared", got)
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
	max := tui.keys.RowCount() - tui.keys.Float.Rect.H
	if max < 1 {
		t.Fatalf("fixture is too short to scroll: %d rows in %d rows",
			tui.keys.RowCount(), tui.keys.Float.Rect.H)
	}
	feed(tui, "j")
	if tui.keys == nil || tui.keys.Offset() != 1 {
		t.Fatalf("j did not scroll the view: %+v", tui.keys)
	}
	feed(tui, "\x1b[6~")
	if tui.keys == nil || tui.keys.Offset() != max {
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
