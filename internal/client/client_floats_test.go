package client

// The help float's query row: the UTF-8 half of triage row 39. The client-side
// tests the keymap float already had live in keymap_view_client_test.go; this
// file holds what le-residual adds on top of them.

import (
	"strings"
	"testing"

	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/testutil"
)

// TestHelpSearchTakesUTF8 proves the help query takes a multi-byte rune whole,
// in one read or split across two, where the byte filter used to drop it one
// byte at a time in silence.
func TestHelpSearchTakesUTF8(t *testing.T) {
	t.Parallel()
	tui := &tui{snap: testutil.TestSnapshot(&layout.Node{Pane: "a"}, 100, 30, nil), keymap: keymap.LoadDefault()}
	tui.runAction("help")
	feed(tui, "/")
	if tui.keys == nil || !tui.keys.Searching {
		t.Fatal("slash did not start a search")
	}
	feed(tui, "caf\xc3\xa9")
	if got := keymapTitle(t, tui.keys, 100, 30); !strings.Contains(got, "/café") {
		t.Fatalf("title = %q, want the live query café", got)
	}
	// A rune split across reads waits for its tail instead of dropping the
	// lead byte, and the tail joins it whole.
	feed(tui, "\xe2")
	if got := keymapTitle(t, tui.keys, 100, 30); !strings.Contains(got, "/café") {
		t.Fatalf("a split lead byte changed the query: %q", got)
	}
	if !tui.pending {
		t.Fatal("a split rune did not hold the query")
	}
	feed(tui, "\x82\xac")
	if got := keymapTitle(t, tui.keys, 100, 30); !strings.Contains(got, "/café€") {
		t.Fatalf("title = %q, want the live query café€", got)
	}
	// A control byte and a broken byte are still not text.
	feed(tui, "\x01\xff")
	if got := keymapTitle(t, tui.keys, 100, 30); !strings.Contains(got, "/café€") {
		t.Fatalf("title = %q, want the live query café€", got)
	}
}
