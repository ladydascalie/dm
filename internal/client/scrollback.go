package client

import (
	"bytes"

	"dm/internal/vt"
)

// This file builds the scrollback view from a pane's raw replay ring in the new
// VT. It mirrors internal/vt's replay helpers until those move into the VT
// package, so the client needs no second terminal implementation.

// replayRows replays a pane's raw output ring through a terminal the size of
// the pane's screen. It returns the rows the screen showed, oldest first: the
// rows that scrolled off the top, then the final screen's rows. keep bounds the
// result to its most recent rows; 0 keeps every row.
func replayRows(ring []byte, w, h, keep int) [][]vt.Cell {
	if w < 1 || h < 1 {
		return nil
	}
	term := vt.NewTerm(w, h)
	g := term.Grid()
	rows := make([][]vt.Cell, 0, h+64)
	add := func(row []vt.Cell) {
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
		alt := g.Modes().AltScreen
		clears := bytes.Contains(piece, []byte("\x1b[2J")) || bytes.Contains(piece, []byte("\x1b[3J"))
		var top []vt.Cell
		row := 0
		if !alt {
			if clears {
				for y := 0; y < h; y++ {
					add(cellRow(g, y, w))
				}
			} else {
				top = cellRow(g, 0, w)
				row = g.Cursor().Y
			}
		}
		term.Write(piece)
		if alt || g.Modes().AltScreen {
			if line {
				term.Write([]byte("\n"))
			}
			continue
		}
		// A change to row 0 while the cursor sat below it is a scroll: text
		// was written at row 0 when the cursor was there, which is not history.
		if !clears && row > 0 && !sameCells(top, cellRow(g, 0, w)) {
			add(top)
		}
		if line {
			// A line feed on the bottom row scrolls the screen: the cursor
			// stays there and the old row 0 leaves.
			if !clears && g.Cursor().Y >= h-1 {
				add(cellRow(g, 0, w))
			}
			term.Write([]byte("\n"))
		}
	}
	for y := 0; y < h; y++ {
		add(cellRow(g, y, w))
	}
	return rows
}

// cellRow copies one row as cells. A continuation slot resolves to the wide
// cell it belongs to, so a cluster appears once per slot it covers.
func cellRow(g *vt.Grid, y, w int) []vt.Cell {
	row := make([]vt.Cell, w)
	cols, rows := g.Size()
	if y < 0 || y >= rows {
		return row
	}
	for x := 0; x < w; x++ {
		if x < cols {
			row[x] = g.Cell(x, y)
		} else {
			row[x] = vt.NewCell(" ", vt.DefaultStyle())
		}
	}
	return row
}

// sameCells reports whether two rows hold the same cells and styles.
func sameCells(a, b []vt.Cell) bool {
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

// buildTall paints rows into a terminal as tall as the row count. Each row is
// placed on its own line, so the last row sits at the bottom. The cursor is
// parked on the last row: scrollStart reads it to extend the reachable bottom,
// and the tall picture's bottom is its last row even when that row is blank.
func buildTall(rows [][]vt.Cell, w int) *vt.Term {
	if w < 1 {
		w = 1
	}
	if len(rows) < 1 {
		return vt.NewTerm(w, 1)
	}
	term := vt.NewTerm(w, len(rows))
	g := term.Grid()
	for y, row := range rows {
		for x := 0; x < w && x < len(row); {
			c := row[x]
			if c.Width == vt.WidthContinuation {
				x++
				continue
			}
			g.Write(x, y, c)
			if c.Width == vt.WidthWide {
				x += 2
			} else {
				x++
			}
		}
	}
	g.SetCursor(0, len(rows)-1)
	return term
}
