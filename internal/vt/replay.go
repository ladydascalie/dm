package vt

import (
	"bytes"
	"strings"
)

// ReplayRows replays a pane's raw output ring through a terminal the size of
// the pane's screen. It returns the rows the screen showed, oldest first: the
// rows that scrolled off the top, then the final screen's rows. keep bounds the
// result to its most recent rows; 0 keeps every row.
//
// The screen size is the pane's own, not a taller terminal. The app wrote its
// escape sequences for that screen, so its relative cursor moves and clears
// land where they did live. Replaying into a taller terminal loses the picture:
// the app never scrolls it and a final clear wipes most rows. That is why a
// scrollback built from raw bytes showed fragments.
//
// The VT has no scroll hook, so ReplayRows counts scrolls itself. A wrapped
// line is noticed by comparing row 0 of the text before and after it. A line
// feed is counted from the cursor: parked on the bottom row, it scrolls the
// screen and row 0 leaves. Comparing cells alone misses that scroll when the
// output repeats one line, because the screen refills with equal rows. A clear
// sequence (2J or 3J) wipes the whole screen, so every row on it goes to
// history first. Nothing is captured while the alternate screen is active:
// those rows are the program's screen, not the shell's history.
func ReplayRows(ring []byte, w, h, keep int) [][]Cell {
	if w < 1 || h < 1 {
		return nil
	}
	term := NewTerm(w, h)
	rows := make([][]Cell, 0, h+64)
	add := func(row []Cell) {
		rows = append(rows, row)
		if keep > 0 && len(rows) > keep {
			rows = append(rows[:0], rows[len(rows)-keep:]...)
		}
	}
	for len(ring) > 0 {
		i := bytes.IndexByte(ring, '\n')
		line := i >= 0
		var piece []byte
		if line {
			piece, ring = ring[:i], ring[i+1:]
		} else {
			piece, ring = ring, nil
		}
		alt := term.Grid().Modes().AltScreen
		clears := bytes.Contains(piece, []byte("\x1b[2J")) || bytes.Contains(piece, []byte("\x1b[3J"))
		var top []Cell
		row := 0
		if !alt {
			if clears {
				for y := 0; y < h; y++ {
					add(cellRow(term.Grid(), y, w))
				}
			} else {
				top = cellRow(term.Grid(), 0, w)
				row = term.Grid().Cursor().Y
			}
		}
		term.Write(piece)
		if alt || term.Grid().Modes().AltScreen {
			if line {
				term.Write([]byte("\n"))
			}
			continue
		}
		// A change to row 0 while the cursor sat below it is a scroll: text
		// was written at row 0 when the cursor was there, which is not history.
		if !clears && row > 0 && !sameCells(top, cellRow(term.Grid(), 0, w)) {
			add(top)
		}
		if line {
			// A line feed on the bottom row scrolls the screen: the cursor
			// stays there and the old row 0 leaves. Repeated output fills the
			// screen with equal rows, so the cell comparison cannot see this
			// scroll; the cursor can.
			if !clears && term.Grid().Cursor().Y >= h-1 {
				add(cellRow(term.Grid(), 0, w))
			}
			term.Write([]byte("\n"))
		}
	}
	// The parser holds the last grapheme cluster until the next rune proves it
	// complete. A copy must carry that cluster: the last line may end without a
	// newline and its final rune would otherwise be missing.
	term.Flush()
	for y := 0; y < h; y++ {
		add(cellRow(term.Grid(), y, w))
	}
	return rows
}

// cellRow copies one row of a grid as cells. A continuation slot resolves to
// the wide cell it belongs to, so a cluster appears once per slot it covers.
func cellRow(g *Grid, y, w int) []Cell {
	row := make([]Cell, w)
	cols, rows := g.Size()
	if y < 0 || y >= rows {
		return row
	}
	for x := 0; x < w; x++ {
		if x < cols {
			row[x] = g.Cell(x, y)
		} else {
			row[x] = NewCell(" ", DefaultStyle())
		}
	}
	return row
}

// sameCells reports whether two rows hold the same cells and styles.
func sameCells(a, b []Cell) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Content != b[i].Content || a[i].Width != b[i].Width ||
			a[i].Style != b[i].Style || a[i].Link != b[i].Link ||
			a[i].Protected != b[i].Protected || a[i].ISOProtected != b[i].ISOProtected {
			return false
		}
	}
	return true
}

// BlankRow reports whether a row holds no text. Colour alone does not
// make a row non-blank: a history window skips leading rows whose cells are
// all spaces, as contentRows does.
func BlankRow(row []Cell) bool {
	for _, c := range row {
		if !c.IsBlank() {
			return false
		}
	}
	return true
}

// PlainRow renders one cell row as text, trailing blanks trimmed.
func PlainRow(row []Cell) string {
	var b strings.Builder
	for _, c := range row {
		content := c.Content
		if content == "" {
			content = " "
		}
		b.WriteString(content)
	}
	return strings.TrimRight(b.String(), " ")
}

// BuildTall paints rows into a terminal as tall as the row count. Each row is
// placed on its own line, so the last row sits at the bottom. The cursor is
// parked on the last row: scrollStart reads it to extend the reachable bottom,
// and the tall picture's bottom is its last row even when that row is blank.
func BuildTall(rows [][]Cell, w int) *Term {
	if w < 1 {
		w = 1
	}
	if len(rows) < 1 {
		return NewTerm(w, 1)
	}
	term := NewTerm(w, len(rows))
	g := term.Grid()
	for y, row := range rows {
		for x := 0; x < w && x < len(row); {
			c := row[x]
			if c.Width == WidthContinuation {
				x++
				continue
			}
			g.Write(x, y, c)
			if c.Width == WidthWide {
				x += 2
			} else {
				x++
			}
		}
	}
	g.SetCursor(0, len(rows)-1)
	return term
}
