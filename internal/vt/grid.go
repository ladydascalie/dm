package vt

const defaultTabWidth = 8

// ScrollRegion holds the grid's margins, all inclusive and in grid coordinates.
// Top and Bottom bound vertical scrolling; Left and Right bound DECSLRM.
type ScrollRegion struct {
	Top, Bottom int
	Left, Right int
}

// MouseMode is which mouse events the application asked for.
type MouseMode uint8

const (
	MouseOff MouseMode = iota
	MouseX10
	MouseButton
	MouseMotion
	MouseMany
)

// MouseEncoding is how mouse events are encoded for the application.
type MouseEncoding uint8

const (
	MouseEncodingDefault MouseEncoding = iota
	MouseEncodingSGR
)

// Modes is the terminal state that is not a cell: the modes a snapshot must
// carry and the dispatch table sets.
type Modes struct {
	Wrap           bool // DECAWM
	Origin         bool // DECOM
	Insert         bool // IRM
	AltScreen      bool
	ReverseVideo   bool // DECSCNM
	BracketedPaste bool
	FocusReporting bool
	Mouse          MouseMode
	MouseEncoding  MouseEncoding

	CursorKeysApplication bool // DECCKM
	ApplicationKeypad     bool // DECNKM
	ColumnMode            bool // DECCOLM: 132 columns when set
	Allow80To132          bool // DECSET 40: DECCOLM may resize
	NoClearOnColumnMode   bool // DECNCSM: DECCOLM does not erase
	MoreFix               bool // DECSET 41
	ReverseWrap           bool // DECSET 45: wrap only a wrapped line
	ReverseWrapExtended   bool // DECSET 1045: wrap any line
	LeftRightMargin       bool // DECLRMM
	LineFeedNewLine       bool // LNM
}

// defaultModes returns a fresh primary screen: wrap on, everything else off.
func defaultModes() Modes {
	return Modes{Wrap: true}
}

// CharsetSet is one designated character set.
type CharsetSet uint8

const (
	CharsetASCII CharsetSet = iota
	CharsetDECSpecial
	CharsetUK
)

// Charset is the G0/G1 designation and which one is currently GL.
type Charset struct {
	G0, G1 CharsetSet
	GL     int // 0 or 1
}

// Cursor is the cursor position and its visible state. WrapNext is the pending
// wrap: the last cell of a row was written and the next printable rune starts a
// new row. It is state no cell shows, so it lives here and in the snapshot.
type Cursor struct {
	X, Y     int
	Visible  bool
	WrapNext bool
}

// SavedCursor is what DECSC stores and DECRC restores.
type SavedCursor struct {
	Cursor  Cursor
	Style   Style
	Charset Charset
	Origin  bool
}

// Grid is the visible screen: rows of cells, a cursor, a scroll region, tab
// stops, the saved cursor, the modes and the charset state.
//
// A wide cell occupies two slots. The first carries Content and Width 2; the
// second is a continuation with Width 0 and empty content. Accessors skip a
// continuation: Cell resolves it to the wide cell it belongs to, and Next
// returns the column after the whole cluster.
type Grid struct {
	cols, rows int
	lines      [][]Cell
	// wrapped marks a row that a printable rune reached by autowrap. Mode 45
	// reverse wrap is limited to those rows; mode 1045 ignores the mark.
	wrapped []bool

	cursor Cursor
	// saved holds the DECSC state once per screen: index 0 is the primary
	// screen and index 1 the alternate, because xterm keeps them apart.
	saved   [2]SavedCursor
	scroll  ScrollRegion
	tabs    []bool
	modes   Modes
	charset Charset

	// altLines is the alternate screen. It is nil until the application first
	// enters it, so a terminal that never uses the alternate screen does not
	// pay for a second grid.
	altLines   [][]Cell
	altWrapped []bool
	// savedModes is the XTSAVE store, keyed by DEC private mode number.
	savedModes map[int]bool
}

// NewGrid returns a blank primary screen of cols by rows. Both are clamped to
// at least one.
func NewGrid(cols, rows int) *Grid {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	g := &Grid{
		cols:    cols,
		rows:    rows,
		lines:   make([][]Cell, rows),
		wrapped: make([]bool, rows),
		cursor:  Cursor{Visible: true},
		scroll:  ScrollRegion{Top: 0, Bottom: rows - 1, Left: 0, Right: cols - 1},
		tabs:    defaultTabs(cols),
		modes:   defaultModes(),
		charset: Charset{},
	}
	for y := range g.lines {
		g.lines[y] = newLine(cols, DefaultStyle())
	}
	g.saved[0] = homeSavedCursor()
	g.saved[1] = homeSavedCursor()
	return g
}

// homeSavedCursor is the state a saved cursor has before any DECSC: the home
// position, the default style and the default charset. A DECRC without a DECSC
// must land here, not on a hidden cursor at the origin.
func homeSavedCursor() SavedCursor {
	return SavedCursor{Cursor: Cursor{Visible: true}, Style: DefaultStyle()}
}

// blankLines returns rows of blank cells for the current size.
func (g *Grid) blankLines() [][]Cell {
	lines := make([][]Cell, g.rows)
	for y := range lines {
		lines[y] = newLine(g.cols, DefaultStyle())
	}
	return lines
}

// resetScrollRegion makes the whole grid the scroll region.
func (g *Grid) resetScrollRegion() {
	g.scroll = ScrollRegion{Top: 0, Bottom: g.rows - 1, Left: 0, Right: g.cols - 1}
}

// screen returns the index of the visible screen into saved.
func (g *Grid) screen() int {
	if g.modes.AltScreen {
		return 1
	}
	return 0
}

// newLine returns a row of cols blank cells.
func newLine(cols int, st Style) []Cell {
	line := make([]Cell, cols)
	for x := range line {
		line[x] = blankCell(st)
	}
	return line
}

// defaultTabs places a tab stop every defaultTabWidth columns.
func defaultTabs(cols int) []bool {
	tabs := make([]bool, cols)
	for x := defaultTabWidth; x < cols; x += defaultTabWidth {
		tabs[x] = true
	}
	return tabs
}

// Size returns the grid's width and height in cells.
func (g *Grid) Size() (cols, rows int) {
	return g.cols, g.rows
}

// Cell returns the cell that covers column x of row y. A continuation slot
// resolves to the wide cell it belongs to, so a caller never sees half a
// cluster. Out of range coordinates return a blank cell.
func (g *Grid) Cell(x, y int) Cell {
	if x < 0 || x >= g.cols || y < 0 || y >= g.rows {
		return blankCell(DefaultStyle())
	}
	line := g.lines[y]
	for x > 0 && line[x].Width == WidthContinuation {
		x--
	}
	return line[x]
}

// Next returns the column after the cluster covering x. A caller walking a row
// skips a wide cell's continuation with this.
func (g *Grid) Next(x, y int) int {
	if x < 0 || x >= g.cols || y < 0 || y >= g.rows {
		return x + 1
	}
	// Walk back to the cluster's first slot, so x on a continuation advances
	// past the whole cluster too.
	for x > 0 && g.lines[y][x].Width == WidthContinuation {
		x--
	}
	c := g.lines[y][x]
	if c.Width < WidthNormal {
		return x + 1
	}
	return x + int(c.Width)
}

// Text returns row y as plain text, with each cluster written once and trailing
// blanks trimmed. Continuation slots are not written, so a wide cell appears
// once.
func (g *Grid) Text(y int) string {
	if y < 0 || y >= g.rows {
		return ""
	}
	line := g.lines[y]
	buf := make([]byte, 0, g.cols)
	end := 0
	for x := 0; x < g.cols; x = g.Next(x, y) {
		c := line[x]
		if c.IsContinuation() {
			continue
		}
		if !c.IsBlank() {
			end = len(buf) + len(c.Content)
		}
		buf = append(buf, c.Content...)
	}
	return string(buf[:end])
}

// Write stores c at column x of row y.
//
// A wide cell takes the slot to its right as its continuation. A write that
// lands on a continuation blanks the wide cell that owns it, because half a
// cluster is never valid. A wide cell with no room at the right edge is stored
// as a normal cell, so the cluster is not lost.
func (g *Grid) Write(x, y int, c Cell) {
	if x < 0 || x >= g.cols || y < 0 || y >= g.rows {
		return
	}
	line := g.lines[y]
	blank := blankCell(DefaultStyle())

	// A continuation written directly is not a cluster.
	if c.Width == WidthContinuation {
		c = blank
	}

	// Blank the wide cell x is the continuation of.
	if line[x].Width == WidthContinuation && x > 0 {
		line[x-1] = blank
	}
	// Blank a wide cell to the left that lost its continuation to this write.
	if x > 0 && line[x-1].Width == WidthWide {
		line[x-1] = blank
	}
	// Blank the continuation of the wide cell this write replaces.
	if line[x].Width == WidthWide && x+1 < g.cols && line[x+1].Width == WidthContinuation {
		line[x+1] = blank
	}

	if c.Width == WidthWide && x+1 < g.cols {
		// Blank a wide cell at x+1 and its continuation before taking both
		// slots.
		if line[x+1].Width == WidthWide && x+2 < g.cols && line[x+2].Width == WidthContinuation {
			line[x+2] = blank
		}
		line[x] = c
		line[x+1] = Continuation()
		return
	}

	if c.Width == WidthWide {
		c.Width = WidthNormal
	}
	line[x] = c
}

// Erase blanks column x of row y. A wide cell loses both slots.
func (g *Grid) Erase(x, y int) {
	g.Write(x, y, blankCell(DefaultStyle()))
}

// InsertCells shifts row y right from column x by n and blanks the n slots that
// open. The shift stops at column right, so the cells outside the left and
// right margins do not move. Cells pushed past right are dropped, and a wide
// cell split by the shift is blanked.
func (g *Grid) InsertCells(x, y, right, n int) {
	if x < 0 || x >= g.cols || y < 0 || y >= g.rows || n <= 0 {
		return
	}
	if right >= g.cols {
		right = g.cols - 1
	}
	if x > right {
		return
	}
	if n > right-x+1 {
		n = right - x + 1
	}
	line := g.lines[y]
	copy(line[x+n:right+1], line[x:right+1-n])
	blank := blankCell(DefaultStyle())
	for i := x; i < x+n; i++ {
		line[i] = blank
	}
	g.repairLine(y)
}

// DeleteCells shifts row y left from column x, dropping n cells, and blanks the
// n slots that open at column right. The shift stops at right, so the cells
// outside the left and right margins do not move. A wide cell split by the
// shift is blanked.
func (g *Grid) DeleteCells(x, y, right, n int) {
	if x < 0 || x >= g.cols || y < 0 || y >= g.rows || n <= 0 {
		return
	}
	if right >= g.cols {
		right = g.cols - 1
	}
	if x > right {
		return
	}
	if n > right-x+1 {
		n = right - x + 1
	}
	line := g.lines[y]
	copy(line[x:right+1-n], line[x+n:right+1])
	blank := blankCell(DefaultStyle())
	for i := right - n + 1; i <= right; i++ {
		line[i] = blank
	}
	g.repairLine(y)
}

// InsertColumns inserts n blank columns at column x on every row from top to
// bottom, between the left and right margins. The cells to the left and right
// of the margins and the rows outside top to bottom do not move.
func (g *Grid) InsertColumns(x, top, bottom, left, right, n int) {
	if x < 0 || x >= g.cols || n <= 0 {
		return
	}
	top = clamp(top, 0, g.rows-1)
	bottom = clamp(bottom, 0, g.rows-1)
	left = clamp(left, 0, g.cols-1)
	right = clamp(right, 0, g.cols-1)
	if top > bottom || left > right || x < left || x > right {
		return
	}
	if n > right-x+1 {
		n = right - x + 1
	}
	blank := blankCell(DefaultStyle())
	for y := top; y <= bottom; y++ {
		line := g.lines[y]
		copy(line[x+n:right+1], line[x:right+1-n])
		for i := x; i < x+n; i++ {
			line[i] = blank
		}
		g.repairLine(y)
	}
}

// ScrollColumnsRight shifts the scroll region's columns right by n and blanks
// the columns that enter at the left. Used by DECBI.
func (g *Grid) ScrollColumnsRight(n int) {
	r := g.scroll
	width := r.Right - r.Left + 1
	if n <= 0 || width <= 0 {
		return
	}
	if n > width {
		n = width
	}
	blank := blankCell(DefaultStyle())
	for y := r.Top; y <= r.Bottom; y++ {
		line := g.lines[y]
		copy(line[r.Left+n:r.Right+1], line[r.Left:r.Right+1-n])
		for x := r.Left; x < r.Left+n; x++ {
			line[x] = blank
		}
		g.repairLine(y)
	}
}

// ScrollColumnsLeft shifts the scroll region's columns left by n and blanks the
// columns that enter at the right. Used by DECFI.
func (g *Grid) ScrollColumnsLeft(n int) {
	r := g.scroll
	width := r.Right - r.Left + 1
	if n <= 0 || width <= 0 {
		return
	}
	if n > width {
		n = width
	}
	blank := blankCell(DefaultStyle())
	for y := r.Top; y <= r.Bottom; y++ {
		line := g.lines[y]
		copy(line[r.Left:r.Right+1-n], line[r.Left+n:r.Right+1])
		for x := r.Right - n + 1; x <= r.Right; x++ {
			line[x] = blank
		}
		g.repairLine(y)
	}
}

// DeleteColumns deletes n columns at column x on every row from top to bottom,
// between the left and right margins. The cells to the left and right of the
// margins and the rows outside top to bottom do not move.
func (g *Grid) DeleteColumns(x, top, bottom, left, right, n int) {
	if x < 0 || x >= g.cols || n <= 0 {
		return
	}
	top = clamp(top, 0, g.rows-1)
	bottom = clamp(bottom, 0, g.rows-1)
	left = clamp(left, 0, g.cols-1)
	right = clamp(right, 0, g.cols-1)
	if top > bottom || left > right || x < left || x > right {
		return
	}
	if n > right-x+1 {
		n = right - x + 1
	}
	blank := blankCell(DefaultStyle())
	for y := top; y <= bottom; y++ {
		line := g.lines[y]
		copy(line[x:right+1-n], line[x+n:right+1])
		for i := right - n + 1; i <= right; i++ {
			line[i] = blank
		}
		g.repairLine(y)
	}
}

// InsertLines opens n blank rows at row y inside the scroll region, pushing the
// rows below down and dropping the ones that leave the bottom. Only the columns
// between the left and right margins move; the cells outside them stay put. A
// row outside the region does nothing.
func (g *Grid) InsertLines(y, n int) {
	r := g.scroll
	if y < r.Top || y > r.Bottom || n <= 0 {
		return
	}
	if n > r.Bottom-y+1 {
		n = r.Bottom - y + 1
	}
	for row := r.Bottom; row >= y+n; row-- {
		copy(g.lines[row][r.Left:r.Right+1], g.lines[row-n][r.Left:r.Right+1])
	}
	blank := blankCell(DefaultStyle())
	for row := y; row < y+n; row++ {
		for x := r.Left; x <= r.Right; x++ {
			g.lines[row][x] = blank
		}
	}
	copy(g.wrapped[y+n:r.Bottom+1], g.wrapped[y:r.Bottom-n+1])
	for row := y; row < y+n; row++ {
		g.wrapped[row] = false
	}
}

// DeleteLines removes n rows at row y inside the scroll region, pulling the
// rows below up and blanking the ones that enter at the bottom. Only the
// columns between the left and right margins move; the cells outside them stay
// put. A row outside the region does nothing.
func (g *Grid) DeleteLines(y, n int) {
	r := g.scroll
	if y < r.Top || y > r.Bottom || n <= 0 {
		return
	}
	if n > r.Bottom-y+1 {
		n = r.Bottom - y + 1
	}
	for row := y; row <= r.Bottom-n; row++ {
		copy(g.lines[row][r.Left:r.Right+1], g.lines[row+n][r.Left:r.Right+1])
	}
	blank := blankCell(DefaultStyle())
	for row := r.Bottom - n + 1; row <= r.Bottom; row++ {
		for x := r.Left; x <= r.Right; x++ {
			g.lines[row][x] = blank
		}
	}
	copy(g.wrapped[y:r.Bottom-n+1], g.wrapped[y+n:r.Bottom+1])
	for row := r.Bottom - n + 1; row <= r.Bottom; row++ {
		g.wrapped[row] = false
	}
}

// repairLine blanks every half cluster in row y: a continuation with no wide
// cell to its left and a wide cell with no continuation to its right.
func (g *Grid) repairLine(y int) {
	line := g.lines[y]
	blank := blankCell(DefaultStyle())
	for x := 0; x < g.cols; x++ {
		switch line[x].Width {
		case WidthContinuation:
			if x == 0 || line[x-1].Width != WidthWide {
				line[x] = blank
			}
		case WidthWide:
			if x+1 >= g.cols || line[x+1].Width != WidthContinuation {
				line[x] = blank
			}
		}
	}
}

// Clear blanks every cell. Cursor, modes and tab stops are untouched.
func (g *Grid) Clear() {
	for y := range g.lines {
		g.lines[y] = newLine(g.cols, DefaultStyle())
	}
	g.wrapped = make([]bool, g.rows)
}

// ScrollUp moves the scroll region's rows up by n and fills the rows that enter
// at the bottom with blanks. Only the columns between the left and right
// margins move; the cells outside them and the rows outside the region stay
// put.
func (g *Grid) ScrollUp(n int) {
	r := g.scroll
	height := r.Bottom - r.Top + 1
	if n <= 0 || height <= 0 {
		return
	}
	if n > height {
		n = height
	}
	for y := r.Top; y <= r.Bottom-n; y++ {
		copy(g.lines[y][r.Left:r.Right+1], g.lines[y+n][r.Left:r.Right+1])
	}
	blank := blankCell(DefaultStyle())
	for y := r.Bottom - n + 1; y <= r.Bottom; y++ {
		for x := r.Left; x <= r.Right; x++ {
			g.lines[y][x] = blank
		}
	}
	copy(g.wrapped[r.Top:r.Bottom-n+1], g.wrapped[r.Top+n:r.Bottom+1])
	for y := r.Bottom - n + 1; y <= r.Bottom; y++ {
		g.wrapped[y] = false
	}
}

// ScrollDown moves the scroll region's rows down by n and fills the rows that
// enter at the top with blanks. Only the columns between the left and right
// margins move; the cells outside them and the rows outside the region stay
// put.
func (g *Grid) ScrollDown(n int) {
	r := g.scroll
	height := r.Bottom - r.Top + 1
	if n <= 0 || height <= 0 {
		return
	}
	if n > height {
		n = height
	}
	for y := r.Bottom; y >= r.Top+n; y-- {
		copy(g.lines[y][r.Left:r.Right+1], g.lines[y-n][r.Left:r.Right+1])
	}
	blank := blankCell(DefaultStyle())
	for y := r.Top; y < r.Top+n; y++ {
		for x := r.Left; x <= r.Right; x++ {
			g.lines[y][x] = blank
		}
	}
	copy(g.wrapped[r.Top+n:r.Bottom+1], g.wrapped[r.Top:r.Bottom-n+1])
	for y := r.Top; y < r.Top+n; y++ {
		g.wrapped[y] = false
	}
}

// Cursor returns the cursor state.
func (g *Grid) Cursor() Cursor {
	return g.cursor
}

// SetCursor moves the cursor, clamped to the grid.
func (g *Grid) SetCursor(x, y int) {
	g.cursor.X = clamp(x, 0, g.cols-1)
	g.cursor.Y = clamp(y, 0, g.rows-1)
	g.cursor.WrapNext = false
}

// SetCursorVisible shows or hides the cursor.
func (g *Grid) SetCursorVisible(v bool) {
	g.cursor.Visible = v
}

// SetWrapNext sets or clears the pending wrap on the cursor.
func (g *Grid) SetWrapNext(v bool) {
	g.cursor.WrapNext = v
}

// Modes returns the terminal modes.
func (g *Grid) Modes() Modes {
	return g.modes
}

// SetModes replaces the terminal modes.
func (g *Grid) SetModes(m Modes) {
	g.modes = m
}

// ScrollRegion returns the margins.
func (g *Grid) ScrollRegion() ScrollRegion {
	return g.scroll
}

// SetScrollRegion sets the margins, clamped to the grid. A top that is not
// above the bottom is ignored.
func (g *Grid) SetScrollRegion(r ScrollRegion) {
	r.Top = clamp(r.Top, 0, g.rows-1)
	r.Bottom = clamp(r.Bottom, 0, g.rows-1)
	r.Left = clamp(r.Left, 0, g.cols-1)
	r.Right = clamp(r.Right, 0, g.cols-1)
	if r.Top > r.Bottom || r.Left > r.Right {
		return
	}
	g.scroll = r
}

// TabStops returns a copy of the tab stop map, one entry per column.
func (g *Grid) TabStops() []bool {
	return append([]bool(nil), g.tabs...)
}

// SetTabStop makes column x a tab stop.
func (g *Grid) SetTabStop(x int) {
	if x >= 0 && x < len(g.tabs) {
		g.tabs[x] = true
	}
}

// ClearTabStop removes the tab stop at column x.
func (g *Grid) ClearTabStop(x int) {
	if x >= 0 && x < len(g.tabs) {
		g.tabs[x] = false
	}
}

// ClearTabStops removes every tab stop.
func (g *Grid) ClearTabStops() {
	for x := range g.tabs {
		g.tabs[x] = false
	}
}

// NextTabStop returns the first tab stop right of x, or the last column when
// there is none.
func (g *Grid) NextTabStop(x int) int {
	for n := x + 1; n < len(g.tabs); n++ {
		if g.tabs[n] {
			return n
		}
	}
	return g.cols - 1
}

// PrevTabStop returns the first tab stop left of x, or column 0 when there is
// none.
func (g *Grid) PrevTabStop(x int) int {
	for n := x - 1; n >= 0; n-- {
		if g.tabs[n] {
			return n
		}
	}
	return 0
}

// SaveCursor stores the cursor, its style, the charset and origin mode, as
// DECSC does. Each screen has its own slot, so a save on the alternate screen is
// not what a restore on the primary screen finds.
func (g *Grid) SaveCursor(style Style) {
	g.saved[g.screen()] = SavedCursor{
		Cursor:  g.cursor,
		Style:   style,
		Charset: g.charset,
		Origin:  g.modes.Origin,
	}
}

// SavedCursor returns what the last DECSC on the visible screen stored.
func (g *Grid) SavedCursor() SavedCursor {
	return g.saved[g.screen()]
}

// RestoreCursor restores the saved cursor, charset and origin mode, and returns
// the saved style for the caller to make current.
func (g *Grid) RestoreCursor() Style {
	sc := g.saved[g.screen()]
	c := sc.Cursor
	g.cursor = Cursor{X: clamp(c.X, 0, g.cols-1), Y: clamp(c.Y, 0, g.rows-1), Visible: c.Visible, WrapNext: c.WrapNext}
	g.charset = sc.Charset
	g.modes.Origin = sc.Origin
	return sc.Style
}

// Charset returns the G0/G1 designation and the active GL.
func (g *Grid) Charset() Charset {
	return g.charset
}

// SetCharset replaces the charset state.
func (g *Grid) SetCharset(c Charset) {
	g.charset = c
}

// Resize changes the grid to cols by rows and keeps every cell that still fits
// in the top-left corner. A wide cell cut by the new right edge is blanked,
// because it cannot keep its continuation. The cursor, the scroll region and
// the tab stops are clamped to the new size.
func (g *Grid) Resize(cols, rows int) {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	lines := resizeLines(g.lines, cols, rows)
	altLines := g.altLines
	if altLines != nil {
		altLines = resizeLines(altLines, cols, rows)
	}
	wrapped := resizeBools(g.wrapped, rows)
	altWrapped := g.altWrapped
	if altWrapped != nil {
		altWrapped = resizeBools(altWrapped, rows)
	}
	tabs := make([]bool, cols)
	copy(tabs, g.tabs)
	for x := len(g.tabs); x < cols; x++ {
		if x > 0 && x%defaultTabWidth == 0 {
			tabs[x] = true
		}
	}

	g.cols, g.rows = cols, rows
	g.lines, g.altLines, g.tabs = lines, altLines, tabs
	g.wrapped, g.altWrapped = wrapped, altWrapped
	g.scroll.Top = clamp(g.scroll.Top, 0, rows-1)
	g.scroll.Bottom = clamp(g.scroll.Bottom, 0, rows-1)
	g.scroll.Left = clamp(g.scroll.Left, 0, cols-1)
	g.scroll.Right = clamp(g.scroll.Right, 0, cols-1)
	g.cursor.X = clamp(g.cursor.X, 0, cols-1)
	g.cursor.Y = clamp(g.cursor.Y, 0, rows-1)
	g.cursor.WrapNext = false
}

// resizeLines copies old into a cols by rows grid, blanking what does not fit.
// A wide cell cut by the new right edge is blanked.
func resizeLines(old [][]Cell, cols, rows int) [][]Cell {
	lines := make([][]Cell, rows)
	for y := range lines {
		line := newLine(cols, DefaultStyle())
		if y < len(old) {
			copy(line, old[y])
		}
		if line[cols-1].Width == WidthWide {
			line[cols-1] = blankCell(DefaultStyle())
		}
		lines[y] = line
	}
	return lines
}

// resizeBools copies flags into rows entries, false where there is no old one.
func resizeBools(old []bool, rows int) []bool {
	out := make([]bool, rows)
	copy(out, old)
	return out
}

// LineWrapped reports whether row y was reached by autowrap.
func (g *Grid) LineWrapped(y int) bool {
	if y < 0 || y >= len(g.wrapped) {
		return false
	}
	return g.wrapped[y]
}

// SetLineWrapped records that row y was or was not reached by autowrap.
func (g *Grid) SetLineWrapped(y int, v bool) {
	if y >= 0 && y < len(g.wrapped) {
		g.wrapped[y] = v
	}
}

// EnterAltScreen switches to the alternate screen. clear wipes it first, which
// is what DECSET 1049 and 47 do; the cursor stays where it was.
func (g *Grid) EnterAltScreen(clear bool) {
	if g.modes.AltScreen {
		return
	}
	if g.altLines == nil {
		g.altLines = g.blankLines()
		g.altWrapped = make([]bool, g.rows)
	}
	g.lines, g.altLines = g.altLines, g.lines
	g.wrapped, g.altWrapped = g.altWrapped, g.wrapped
	if clear {
		g.lines = g.blankLines()
		g.wrapped = make([]bool, g.rows)
	}
	g.modes.AltScreen = true
}

// ExitAltScreen switches back to the primary screen. clear wipes the alternate
// screen, which is what DECRESET 1047 and 1049 do.
func (g *Grid) ExitAltScreen(clear bool) {
	if !g.modes.AltScreen {
		return
	}
	g.lines, g.altLines = g.altLines, g.lines
	g.wrapped, g.altWrapped = g.altWrapped, g.wrapped
	if clear {
		g.altLines = g.blankLines()
		g.altWrapped = make([]bool, g.rows)
	}
	g.modes.AltScreen = false
}

// Fill stores one cluster in every cell, as DECALN does with the letter E. The
// cursor moves home and the margins return to the whole grid.
func (g *Grid) Fill(content string) {
	c := NewCell(content, DefaultStyle())
	for y := range g.lines {
		for x := range g.lines[y] {
			g.lines[y][x] = c
		}
	}
	g.resetScrollRegion()
	g.cursor = Cursor{Visible: true}
	g.wrapped = make([]bool, g.rows)
}

// Reset returns the grid to its power-on state, as RIS does. The screen is
// blank, the modes are default, the margins are full, the tab stops are the
// default every eight columns, the charset is ASCII and both screens start
// blank with the cursor home.
func (g *Grid) Reset() {
	if g.modes.ColumnMode && g.cols != 80 {
		g.Resize(80, g.rows)
	}
	g.modes = defaultModes()
	g.charset = Charset{}
	g.cursor = Cursor{Visible: true}
	g.resetScrollRegion()
	g.tabs = defaultTabs(g.cols)
	g.lines = g.blankLines()
	g.wrapped = make([]bool, g.rows)
	g.altLines = nil
	g.altWrapped = nil
	g.saved[0] = homeSavedCursor()
	g.saved[1] = homeSavedCursor()
	g.savedModes = nil
}

// SoftReset resets what DECSTR resets and leaves the screen contents, the
// cursor position, the alt screen and the tab stops alone. It is the path that
// stops one test's margins leaking into the next.
func (g *Grid) SoftReset() {
	m := g.modes
	m.Wrap = true
	m.Origin = false
	m.Insert = false
	m.ReverseVideo = false
	m.ReverseWrap = false
	m.ReverseWrapExtended = false
	m.LeftRightMargin = false
	m.CursorKeysApplication = false
	g.modes = m
	g.charset = Charset{}
	g.cursor.Visible = true
	g.cursor.WrapNext = false
	g.resetScrollRegion()
	g.tabs = defaultTabs(g.cols)
	g.saved[0] = homeSavedCursor()
	g.saved[1] = homeSavedCursor()
}

// clamp folds v into the inclusive range lo to hi.
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
