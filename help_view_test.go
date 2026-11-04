package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// helpBoxDraw paints the help box for a screen and returns its bytes.
func helpBoxDraw(t *testing.T, cols, rows int, keys [][2]string) string {
	t.Helper()
	var buf bytes.Buffer
	drawHelp(&buf, cols, rows, keys)
	return buf.String()
}

// helpBoxWidths is the widest key and action label in cells.
func helpBoxWidths(keys [][2]string) (keyW, actionW int) {
	for _, kv := range keys {
		keyW = maxInt(keyW, runeCells(kv[0]))
		actionW = maxInt(actionW, runeCells(kv[1]))
	}
	return keyW, actionW
}

// helpBoxPainted is a band replayed from its absolute moves: every cell and
// what was written into it, plus the furthest cell addressed.
type helpBoxPainted struct {
	writes map[[2]int][]rune
	maxRow int
	maxCol int
}

// helpBoxParse replays the band: a move sets the cursor, an SGR changes
// nothing, and a rune takes one cell of the width runeWidth gives it. It fails
// on anything else, so a relative move or an unknown escape cannot slip by.
func helpBoxParse(t *testing.T, s string) helpBoxPainted {
	t.Helper()
	p := helpBoxPainted{writes: map[[2]int][]rune{}}
	row, col := 0, 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			if i+1 >= len(s) || s[i+1] != '[' {
				t.Fatalf("escape %q at %d", s[i:], i)
			}
			j := i + 2
			for j < len(s) && (s[j] == ';' || (s[j] >= '0' && s[j] <= '9')) {
				j++
			}
			if j >= len(s) {
				t.Fatalf("truncated escape at %d: %q", i, s)
			}
			params, final := s[i+2:j], s[j]
			i = j + 1
			switch final {
			case 'm':
				// Colour only: it moves no cursor and paints no cell.
				continue
			case 'H':
				var r, c int
				if _, err := fmt.Sscanf(params, "%d;%d", &r, &c); err != nil {
					t.Fatalf("bad move %q: %v", params, err)
				}
				row, col = r-1, c-1
				continue
			default:
				t.Fatalf("band carries %q, want an absolute move", s[i-2:j+1])
			}
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		cell := [2]int{row, col}
		p.writes[cell] = append(p.writes[cell], r)
		if row > p.maxRow {
			p.maxRow = row
		}
		if col+1 > p.maxCol {
			p.maxCol = col + 1
		}
		col += runeWidth(r)
	}
	return p
}

// helpBoxBounds is the smallest rectangle that holds every painted cell.
func helpBoxBounds(t *testing.T, p helpBoxPainted) (x0, y0, x1, y1 int) {
	t.Helper()
	first := true
	for cell := range p.writes {
		if first {
			x0, y0, x1, y1 = cell[1], cell[0], cell[1], cell[0]
			first = false
			continue
		}
		x0 = min(x0, cell[1])
		y0 = min(y0, cell[0])
		x1 = maxInt(x1, cell[1])
		y1 = maxInt(y1, cell[0])
	}
	if first {
		t.Fatal("the band painted nothing")
	}
	return x0, y0, x1, y1
}

// helpBoxRowText is row between two columns as the terminal ends up: the last
// write to a cell wins and an unwritten cell is a space.
func helpBoxRowText(p helpBoxPainted, row, from, to int) string {
	var b strings.Builder
	for col := from; col <= to; col++ {
		runes := p.writes[[2]int{row, col}]
		if len(runes) == 0 {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(runes[len(runes)-1])
	}
	return b.String()
}

// TestHelpBoxSizesToWidestRowAt100x30 proves the box holds every default row
// whole, that both border rows own a row, and that the frame carries its title
// and hint.
func TestHelpBoxSizesToWidestRowAt100x30(t *testing.T) {
	keys := LoadDefault().HelpRows()
	keyW, actionW := helpBoxWidths(keys)
	p := helpBoxParse(t, helpBoxDraw(t, 100, 30, keys))
	x0, y0, x1, y1 := helpBoxBounds(t, p)

	if want := keyW + 2 + actionW + 2; x1-x0+1 != want {
		t.Fatalf("box width = %d, want %d (keyW %d, actionW %d)", x1-x0+1, want, keyW, actionW)
	}
	if x0 < 0 || y0 < 0 || x1 > 99 || y1 > 29 {
		t.Fatalf("box %d,%d..%d,%d is not inside 100x30", x0, y0, x1, y1)
	}
	if want := len(keys) + 2; y1-y0+1 != want {
		t.Fatalf("box height = %d, want %d for %d rows", y1-y0+1, want, len(keys))
	}
	if y1 != y0+len(keys)+1 {
		t.Fatalf("bottom border row %d, want %d on its own row", y1, y0+len(keys)+1)
	}

	// Every content row writes exactly the same columns and carries the full
	// label pair, so the widest row sits inside the box and nothing is cut.
	for i, kv := range keys {
		row := y0 + 1 + i
		want := helpBoxPad(kv[0], keyW) + "  " + helpBoxPad(kv[1], actionW)
		if got := helpBoxRowText(p, row, x0+1, x1-1); got != want {
			t.Fatalf("row %d = %q, want %q", i, got, want)
		}
		if strings.Contains(helpBoxRowText(p, row, x0+1, x1-1), "\u2026") {
			t.Fatalf("row %d was cut: %q", i, helpBoxRowText(p, row, x0+1, x1-1))
		}
	}

	if got := helpBoxRowText(p, y0, x0+2, x1-1); !strings.HasPrefix(got, "prefix") {
		t.Fatalf("title row = %q, want it to read prefix", got)
	}
	if got := helpBoxRowText(p, y1, x0+2, x1-1); !strings.HasPrefix(got, "esc") {
		t.Fatalf("hint row = %q, want it to read esc", got)
	}
}

// TestHelpBoxStaysInBoundsOnNarrowScreens proves a small screen keeps the box
// off the rail, the toast row and the bar, and inside the screen.
func TestHelpBoxStaysInBoundsOnNarrowScreens(t *testing.T) {
	for _, c := range []struct{ cols, rows int }{{40, 15}, {26, 12}} {
		keys := LoadDefault().HelpRows()
		p := helpBoxParse(t, helpBoxDraw(t, c.cols, c.rows, keys))
		x0, y0, x1, y1 := helpBoxBounds(t, p)
		if x0 < 0 || y0 < 0 || x1 > c.cols-1 || y1 > c.rows-1 {
			t.Fatalf("%dx%d: box %d,%d..%d,%d is past the screen", c.cols, c.rows, x0, y0, x1, y1)
		}
		if p.maxCol > c.cols-1 || p.maxRow > c.rows-1 {
			t.Fatalf("%dx%d: band addresses %d,%d, past the screen", c.cols, c.rows, p.maxRow+1, p.maxCol+1)
		}
		// effectiveRail never exceeds maxRail(cols), so a left border at or
		// right of it clears every rail width.
		if left := maxRail(c.cols); x0 < left {
			t.Fatalf("%dx%d: box starts at %d, want its border at or right of the rail %d", c.cols, c.rows, x0, left)
		}
		if y1 > c.rows-3 {
			t.Fatalf("%dx%d: box bottom %d touches the toast row or the bar", c.cols, c.rows, y1)
		}
		for cell := range p.writes {
			if cell[0] >= c.rows-2 {
				t.Fatalf("%dx%d: box paints row %d, which belongs to the toast row or the bar", c.cols, c.rows, cell[0])
			}
		}
		if y1-y0+1 >= len(keys)+2 {
			t.Fatalf("%dx%d: box height %d, want trailing rows dropped", c.cols, c.rows, y1-y0+1)
		}
	}
}

// TestHelpBoxNarrowDropsActionColumn proves a narrow box shows the key column
// alone, with no action label bleeding in.
func TestHelpBoxNarrowDropsActionColumn(t *testing.T) {
	keys := LoadDefault().HelpRows()
	keyW, actionW := helpBoxWidths(keys)
	p := helpBoxParse(t, helpBoxDraw(t, 26, 12, keys))
	x0, y0, x1, y1 := helpBoxBounds(t, p)

	if got, want := x1-x0+1, keyW+2; got != want {
		t.Fatalf("box width = %d, want %d for the key column alone", got, want)
	}
	if x1-x0+1 >= keyW+2+actionW+2 {
		t.Fatalf("box width %d still holds the action column", x1-x0+1)
	}
	for i, kv := range keys[:y1-y0-1] {
		want := helpBoxPad(helpBoxElide(kv[0], x1-x0-1), x1-x0-1)
		if got := helpBoxRowText(p, y0+1+i, x0+1, x1-1); got != want {
			t.Fatalf("row %d = %q, want the key column alone %q", i, got, want)
		}
	}
}

// TestHelpBoxEllipsisMarksCutLabel proves a box too narrow for its key column
// cuts a label and says so.
func TestHelpBoxEllipsisMarksCutLabel(t *testing.T) {
	keys := LoadDefault().HelpRows()
	keyW, _ := helpBoxWidths(keys)
	p := helpBoxParse(t, helpBoxDraw(t, 12, 12, keys))
	x0, y0, x1, y1 := helpBoxBounds(t, p)
	// The interior is narrower than the key column, so the action column is
	// gone as well as the label being cut.
	if x1-x0-1 >= keyW {
		t.Fatalf("interior %d cells still holds the %d cell key column", x1-x0-1, keyW)
	}
	found := false
	for row := y0 + 1; row <= y1-1; row++ {
		got := strings.TrimRight(helpBoxRowText(p, row, x0+1, x1-1), " ")
		if runeCells(got) > x1-x0-1 {
			t.Fatalf("row %d = %q, wider than the %d cell interior", row, got, x1-x0-1)
		}
		if strings.HasSuffix(got, "\u2026") {
			found = true
		}
	}
	if !found {
		t.Fatal("a clipped key label carries no ellipsis")
	}
}

// TestHelpBoxRowsShareColumns is the misalignment the operator saw: every
// content row must paint the same columns, once each.
func TestHelpBoxRowsShareColumns(t *testing.T) {
	keys := LoadDefault().HelpRows()
	p := helpBoxParse(t, helpBoxDraw(t, 100, 30, keys))
	x0, y0, x1, y1 := helpBoxBounds(t, p)

	for row := y0 + 1; row <= y1-1; row++ {
		for col := x0 + 1; col <= x1-1; col++ {
			if writes := p.writes[[2]int{row, col}]; len(writes) != 1 {
				t.Fatalf("content cell %d,%d painted %d times", row, col, len(writes))
			}
		}
	}
}
