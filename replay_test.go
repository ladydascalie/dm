package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hinshun/vt10x"
)

// ringOf builds a raw output ring of plain lines, each ending in \r\n.
func ringOf(prefix string, n int) []byte {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "%s-%03d\r\n", prefix, i)
	}
	return []byte(b.String())
}

// plainRows renders replay rows as text for assertions.
func plainRows(rows [][]vt10x.Glyph) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = plainRow(row)
	}
	return out
}

// TestReplayRowsStream pins the basic case: a stream longer than the screen
// keeps every scrolled row and ends with the final screen, in order.
func TestReplayRowsStream(t *testing.T) {
	rows := replayRows(ringOf("LINE", 20), 20, 5, 0)
	got := plainRows(rows)
	// The trailing newline scrolls one more row, so the final screen is the
	// last four lines and a blank bottom row: 16 history rows plus 5 screen rows.
	if len(got) != 21 {
		t.Fatalf("replay kept %d rows, want 21:\n%q", len(got), got)
	}
	if got[0] != "LINE-000" {
		t.Fatalf("oldest row = %q, want LINE-000", got[0])
	}
	if got[15] != "LINE-015" || got[16] != "LINE-016" || got[19] != "LINE-019" || got[20] != "" {
		t.Fatalf("history/final screen seam wrong:\n%q", got[14:])
	}
}

// TestReplayRowsNoScroll keeps the final screen when the output never scrolled.
func TestReplayRowsNoScroll(t *testing.T) {
	rows := replayRows(ringOf("LINE", 3), 20, 5, 0)
	got := plainRows(rows)
	if len(got) != 5 {
		t.Fatalf("replay kept %d rows, want 5", len(got))
	}
	if got[0] != "LINE-000" || got[2] != "LINE-002" || got[3] != "" {
		t.Fatalf("final screen = %q", got)
	}
}

// TestReplayRowsKeep bounds the result to its most recent rows.
func TestReplayRowsKeep(t *testing.T) {
	rows := replayRows(ringOf("LINE", 20), 20, 2, 3)
	got := plainRows(rows)
	if len(got) != 3 {
		t.Fatalf("replay kept %d rows, want 3:\n%q", len(got), got)
	}
	if got[1] != "LINE-019" {
		t.Fatalf("newest text row = %q, want LINE-019:\n%q", got[1], got)
	}
}

// TestReplayRowsAltScreen leaves alternate-screen rows out: they are the
// program's screen, not the shell's history.
func TestReplayRowsAltScreen(t *testing.T) {
	ring := []byte("PRIMARY-1\r\n\x1b[?1049hALT-1\r\nALT-2\r\n\x1b[?1049lPRIMARY-2\r\n")
	rows := replayRows(ring, 20, 3, 0)
	got := plainRows(rows)
	for _, row := range got {
		if strings.Contains(row, "ALT-") {
			t.Fatalf("alternate screen row leaked into history: %q", got)
		}
	}
	if got[0] != "PRIMARY-1" || got[1] != "PRIMARY-2" {
		t.Fatalf("primary screen lost across the alt round trip:\n%q", got)
	}
}

// TestReplayRowsClear captures the screen before a clear wipes it.
func TestReplayRowsClear(t *testing.T) {
	ring := []byte("A\r\nB\r\n\x1b[2J\x1b[HC\r\n")
	rows := replayRows(ring, 20, 3, 0)
	got := plainRows(rows)
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "A") || !strings.Contains(joined, "B") {
		t.Fatalf("cleared rows were not captured:\n%q", got)
	}
	if got[len(got)-3] != "C" {
		t.Fatalf("the final screen's top row = %q, want C:\n%q", got[len(got)-3], got)
	}
}

// TestReplayRowsEmptyScreen answers a blank final screen without history.
func TestReplayRowsEmptyScreen(t *testing.T) {
	rows := replayRows(nil, 20, 4, 0)
	if len(rows) != 4 {
		t.Fatalf("replay kept %d rows, want 4", len(rows))
	}
	for i, row := range rows {
		if got := plainRow(row); got != "" {
			t.Fatalf("row %d = %q, want blank", i, got)
		}
	}
}
