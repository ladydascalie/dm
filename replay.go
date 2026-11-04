package main

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/hinshun/vt10x"
)

// replayRows replays a pane's raw output ring through a terminal the size of
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
// vt10x has no scroll hook, so a scroll is noticed by comparing row 0 before
// and after each line. When row 0 moved, the screen scrolled and the old row 0
// belongs in the history. A clear sequence (2J or 3J) wipes the whole screen,
// so every row on it goes to history first. Nothing is captured while the
// alternate screen is active: those rows are the program's screen, not the
// shell's history.
func replayRows(ring []byte, w, h, keep int) [][]vt10x.Glyph {
	if w < 1 || h < 1 {
		return nil
	}
	vt := vt10x.New(vt10x.WithSize(w, h))
	rows := make([][]vt10x.Glyph, 0, h+64)
	add := func(row []vt10x.Glyph) {
		rows = append(rows, row)
		if keep > 0 && len(rows) > keep {
			rows = append(rows[:0], rows[len(rows)-keep:]...)
		}
	}
	for len(ring) > 0 {
		i := bytes.IndexByte(ring, '\n')
		var piece []byte
		if i < 0 {
			piece, ring = ring, nil
		} else {
			piece, ring = ring[:i+1], ring[i+1:]
		}
		alt := vt.Mode()&vt10x.ModeAltScreen != 0
		clears := bytes.Contains(piece, []byte("\x1b[2J")) || bytes.Contains(piece, []byte("\x1b[3J"))
		var top []vt10x.Glyph
		row := 0
		if !alt {
			if clears {
				for y := 0; y < h; y++ {
					add(glyphRow(vt, y, w))
				}
			} else {
				top = glyphRow(vt, 0, w)
				row = vt.Cursor().Y
			}
		}
		_, _ = vt.Write(piece)
		if alt || vt.Mode()&vt10x.ModeAltScreen != 0 {
			continue
		}
		// A change to row 0 while the cursor sat below it is a scroll: text
		// was written at row 0 when the cursor was there, which is not history.
		if !clears && row > 0 && !sameGlyphs(top, glyphRow(vt, 0, w)) {
			add(top)
		}
	}
	for y := 0; y < h; y++ {
		add(glyphRow(vt, y, w))
	}
	return rows
}

// blankGlyphRow reports whether a row holds no text. Colour alone does not
// make a row non-blank: a history window skips leading rows whose glyphs are
// all spaces, as contentRows does.
func blankGlyphRow(row []vt10x.Glyph) bool {
	for _, g := range row {
		if g.Char != ' ' {
			return false
		}
	}
	return true
}

// glyphRow copies one row of a terminal as glyphs. Cells past the terminal's
// width are default blanks. The caller owns the result.
func glyphRow(v vt10x.View, y, w int) []vt10x.Glyph {
	row := make([]vt10x.Glyph, w)
	cols, rows := v.Size()
	if y < 0 || y >= rows {
		return row
	}
	for x := 0; x < w; x++ {
		var g vt10x.Glyph
		if x < cols {
			g = v.Cell(x, y)
		}
		row[x] = normCell(g)
	}
	return row
}

// sameGlyphs reports whether two rows hold the same glyphs and styles.
func sameGlyphs(a, b []vt10x.Glyph) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Char != b[i].Char || a[i].Mode != b[i].Mode ||
			a[i].FG != b[i].FG || a[i].BG != b[i].BG {
			return false
		}
	}
	return true
}

// plainRow renders one glyph row as text, trailing blanks trimmed.
func plainRow(row []vt10x.Glyph) string {
	var b strings.Builder
	for _, g := range row {
		if g.Char < 0x20 {
			b.WriteByte(' ')
		} else {
			b.WriteRune(g.Char)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// rowANSI renders one glyph row as ANSI text, trailing default blanks trimmed.
func rowANSI(row []vt10x.Glyph) string {
	var b strings.Builder
	var cur *vt10x.Glyph
	last := -1
	for i, g := range row {
		if g.Char != ' ' || g.Mode != 0 || !isDefault(g.FG) || !isDefault(g.BG) {
			last = i
		}
	}
	for i := 0; i <= last; i++ {
		g := row[i]
		b.WriteString(sgr(g, cur))
		gg := g
		cur = &gg
		if g.Char < 0x20 {
			b.WriteByte(' ')
		} else {
			b.WriteRune(g.Char)
		}
	}
	if cur != nil {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// buildTall paints rows into a terminal as tall as the row count. Each row is
// placed on its own line, so the last row sits at the bottom. The cursor is
// parked on the last row: scrollStart reads it to extend the reachable bottom,
// and the tall picture's bottom is its last row even when that row is blank.
func buildTall(rows [][]vt10x.Glyph, w int) vt10x.Terminal {
	if w < 1 {
		w = 1
	}
	if len(rows) < 1 {
		return vt10x.New(vt10x.WithSize(w, 1))
	}
	vt := vt10x.New(vt10x.WithSize(w, len(rows)))
	var b bytes.Buffer
	for i, row := range rows {
		fmt.Fprintf(&b, "\x1b[%d;1H", i+1)
		b.WriteString(rowANSI(row))
	}
	fmt.Fprintf(&b, "\x1b[%d;1H", len(rows))
	_, _ = vt.Write(b.Bytes())
	return vt
}
