package vt

import "bytes"

// star fills one redaction cell. The style comes from the cell it replaces, so
// a repaint keeps the colours the value was drawn with.
const star = "*"

// Scrub rewrites every occurrence of a value in the terminal's picture to
// redaction cells and returns the number of cells it changed.
//
// It is the toggle's repair path: the stream stage redacts bytes as they are
// published, so a value the operator revealed while the switch was off is in
// the grid with no bytes left to re-run. The grid is rewritten in place,
// because a snapshot cannot be rebuilt from a pane's byte ring: the ring is
// bounded and drops the output a program's incremental drawing still needs.
//
// Both screens are scrubbed. The alternate screen can hold a value the
// primary screen never showed, and the terminal swaps between them without
// changing the bytes the pane keeps.
func (t *Term) Scrub(values []string) int {
	if len(values) == 0 {
		return 0
	}
	g := t.grid
	n := scrubScreen(g.lines, g.wrapped, values)
	if g.altLines != nil {
		n += scrubScreen(g.altLines, g.altWrapped, values)
	}
	return n
}

// scrubScreen rewrites one screen's rows, one logical line at a time. A row a
// printable rune reached by autowrap is the tail of the row above it, so a
// value that wraps is matched over the pair: the terminal drew one line, and a
// row-by-row scan would miss the value it split.
func scrubScreen(lines [][]Cell, wrapped []bool, values []string) int {
	changed := 0
	for y := 0; y < len(lines); y++ {
		if y > 0 && y < len(wrapped) && wrapped[y] {
			continue
		}
		last := y
		for last+1 < len(lines) && last+1 < len(wrapped) && wrapped[last+1] {
			last++
		}
		changed += scrubRows(lines[y:last+1], values)
	}
	return changed
}

// cellRef is one cell of a logical line. y is the row inside the line, not the
// screen row, so the caller maps it back through the slice it passed.
type cellRef struct{ x, y int }

// scrubRows rewrites the matches in one logical line: the rows a single line
// of output occupies. Wide cells appear once, at the slot that owns them.
func scrubRows(rows [][]Cell, values []string) int {
	var (
		refs   []cellRef
		starts []int
		text   []byte
	)
	for y, line := range rows {
		for x := 0; x < len(line); x++ {
			if line[x].IsContinuation() {
				continue
			}
			refs = append(refs, cellRef{x: x, y: y})
			starts = append(starts, len(text))
			text = append(text, line[x].Content...)
		}
	}
	changed := 0
	for _, v := range values {
		if v == "" {
			continue
		}
		needle := []byte(v)
		for from := 0; from+len(needle) <= len(text); {
			i := bytes.Index(text[from:], needle)
			if i < 0 {
				break
			}
			start := from + i
			end := start + len(needle)
			// The match has to begin at a cell boundary: half a cluster is
			// not a run the terminal ever drew.
			first := cellStartingAt(starts, start)
			if first < 0 {
				from = start + 1
				continue
			}
			last := lastCellBefore(starts, end)
			if last < first {
				from = end
				continue
			}
			for k := first; k <= last; k++ {
				r := refs[k]
				paintStar(rows[r.y], r.x)
				changed++
			}
			from = end
		}
	}
	return changed
}

// cellStartingAt returns the index of the cell that starts at byte offset off,
// or -1 when no cell does.
func cellStartingAt(starts []int, off int) int {
	for i, s := range starts {
		if s == off {
			return i
		}
		if s > off {
			return -1
		}
	}
	return -1
}

// lastCellBefore returns the index of the last cell that starts before offset
// off, or -1 when none does.
func lastCellBefore(starts []int, off int) int {
	last := -1
	for i, s := range starts {
		if s >= off {
			break
		}
		last = i
	}
	return last
}

// paintStar turns one cell into a redaction cell. A wide cell gives its
// continuation slot back before it shrinks to one column, so the row keeps its
// width and no half cluster is left behind.
func paintStar(line []Cell, x int) {
	if x < 0 || x >= len(line) || line[x].IsContinuation() {
		return
	}
	if line[x].IsWide() && x+1 < len(line) {
		line[x+1] = blankCell(line[x+1].Style)
	}
	line[x] = Cell{Content: star, Width: WidthNormal, Style: line[x].Style}
}
