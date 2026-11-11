package vt

// Rewrap re-wraps the primary screen at cols by rows.
//
// A resize that changes the width cannot keep the rows as they are: a row the
// app wrapped at the old width joins its continuation rows into one logical
// line, and that line wraps again at the new width. Rewrap does that with the
// screen's own rows. No output is replayed, so the screen keeps exactly the
// content it had and never pulls scrollback back onto it: the blank space a
// wider screen frees stays blank until the application paints it.
//
// A narrower screen drops rows from the top, and a taller screen leaves the
// new rows blank at the bottom, so the cursor and the prompt it usually sits
// on stay visible. Rows that leave the screen are still in the scroll ring
// for the scrollback view.
//
// The cursor keeps its place in its logical line. A line the new shape pushed
// off the top takes the cursor with it only as far as row 0, and a cursor
// that sat on blank space below the last line keeps its old cell.
//
// Rewrap refuses to run and returns false when the alternate screen is visible
// or the application set a scroll region: those screens are the application's
// picture, and it repaints them on SIGWINCH. The caller keeps the plain
// Resize for that case.
func (g *Grid) Rewrap(cols, rows int) bool {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	if g.modes.AltScreen {
		return false
	}
	full := g.scroll.Top == 0 && g.scroll.Bottom == g.rows-1 &&
		g.scroll.Left == 0 && g.scroll.Right == g.cols-1
	if !full {
		return false
	}
	old := g.cursor

	// Collect the logical lines: runs of rows joined by autowrap, each with
	// its trailing blank cells trimmed. While collecting, record the line that
	// holds the cursor and the number of clusters that precede the cursor in
	// it. A wrap-pending cursor sits after the line's last cluster, and so does
	// a cursor parked in trimmed blanks: both take the line's length.
	lines := [][]Cell{nil}
	curLine, curOff := 0, 0
	for y := 0; y < g.rows; y++ {
		row := g.lines[y]
		// The slot a wide cell skipped at this width is a hole, not content:
		// the row autowrapped, its last cell is a default blank and the next
		// row starts with the wide cell that refused the slot. At the new
		// width the cluster may fit, so the hole must not travel as a cell.
		gap := y < g.rows-1 && g.wrapped[y+1] && len(row) > 0 &&
			row[len(row)-1].IsBlank() && row[len(row)-1].Style == DefaultStyle() &&
			g.lines[y+1][0].IsWide()
		line := lines[len(lines)-1]
		base := len(line)
		if y == old.Y {
			cx := old.X
			if cx > 0 && row[cx].IsContinuation() {
				cx--
			}
			curLine = len(lines) - 1
			curOff = base
			for x := 0; x < cx; x++ {
				if !row[x].IsContinuation() {
					curOff++
				}
			}
		}
		last := len(row) - 1
		for x := 0; x < len(row); x++ {
			if row[x].IsContinuation() {
				continue
			}
			if gap && x == last {
				break
			}
			line = append(line, row[x])
		}
		if y < g.rows-1 && g.wrapped[y+1] {
			lines[len(lines)-1] = line
			continue
		}
		// The row ends the line: trim its tail and open the next line.
		trimLine(&line)
		lines[len(lines)-1] = line
		if y == old.Y {
			if curOff > len(line) || old.WrapNext {
				curOff = len(line)
			}
		}
		lines = append(lines, nil)
	}
	lines = lines[:len(lines)-1]

	// Blank rows below the last line with content are blank space, not empty
	// lines: they fold into the padding the new shape adds at the bottom,
	// instead of claiming rows that push real content off the top.
	lastContent := -1
	for i, line := range lines {
		if len(line) > 0 {
			lastContent = i
		}
	}
	curFree := curLine > lastContent
	lines = lines[:lastContent+1]

	// Wrap each logical line again at the new width, packing clusters the way
	// place() does: a wide cluster that does not fit in the row's last column
	// moves to the next row and leaves that column blank. A logical line
	// always starts on a fresh row that is not marked as a continuation; a row
	// an overflow opens is. While wrapping the cursor's line, record the cell
	// the cursor's offset lands on.
	out := make([][]Cell, 0, g.rows+8)
	flags := make([]bool, 0, g.rows+8)
	var row []Cell
	rowOpen, rowWrapped := false, false
	x, y := 0, 0
	curX, curY, curWrap := -1, -1, false
	closeRow := func() {
		padded := newLine(cols, DefaultStyle())
		copy(padded, row)
		out = append(out, padded)
		flags = append(flags, rowWrapped)
		row = row[:0]
		rowOpen, rowWrapped = false, false
		x, y = 0, y+1
	}
	for i, line := range lines {
		if rowOpen {
			closeRow()
		}
		rowOpen, rowWrapped = true, false
		for j := 0; j <= len(line); j++ {
			if j == len(line) {
				// The cursor at the line's end sits after the last cluster. A
				// row the line filled exactly parks it on the last column
				// with the wrap pending, the way place() leaves it; anywhere
				// else the pending state belonged to the old shape.
				if i == curLine && !curFree && curX < 0 {
					if x >= cols {
						curX, curY, curWrap = cols-1, y, true
					} else {
						curX, curY, curWrap = x, y, false
					}
				}
				break
			}
			c := line[j]
			w := int(c.Width)
			if x+w > cols && x > 0 {
				closeRow()
				rowOpen, rowWrapped = true, true
			}
			if i == curLine && !curFree && j == curOff && curX < 0 {
				curX, curY, curWrap = x, y, false
			}
			row = append(row, c)
			if c.IsWide() {
				row = append(row, Continuation())
			}
			x += w
		}
	}
	if rowOpen {
		closeRow()
	}

	// Fit the wrapped rows into the viewport. Too many: rows leave at the top,
	// and the first kept row keeps its continuation mark, so a line cut in
	// half still reads as one line. Too few: blank rows fill the bottom.
	drop := len(out) - rows
	if drop > 0 {
		out, flags = out[drop:], flags[drop:]
		curY -= drop
		g.scrolls += drop
	}
	for len(out) < rows {
		out = append(out, newLine(cols, DefaultStyle()))
		flags = append(flags, false)
	}
	if curY < 0 {
		curY = 0
	}
	if curY > rows-1 {
		curY = rows - 1
	}
	if curFree || curX < 0 {
		curX, curY, curWrap = min(old.X, cols-1), min(old.Y, rows-1), false
	} else if curX > cols-1 {
		curX = cols - 1
	}

	g.cols, g.rows = cols, rows
	g.lines = out
	g.wrapped = flags
	g.cursor = Cursor{X: curX, Y: curY, Visible: old.Visible, WrapNext: curWrap}
	g.tabs = defaultTabs(cols)
	g.resetScrollRegion()
	g.modes.ColumnMode = cols > 80
	return true
}

// trimLine drops the trailing cells that paint nothing: a blank in the default
// style with no link and no protection mark. A styled blank paints background,
// so it stays.
func trimLine(line *[]Cell) {
	for len(*line) > 0 {
		c := (*line)[len(*line)-1]
		if !c.IsBlank() || c.Style != DefaultStyle() || c.Link != "" || c.Protected || c.ISOProtected {
			return
		}
		*line = (*line)[:len(*line)-1]
	}
}
