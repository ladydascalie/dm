package switcher

import (
	"bytes"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"dm/internal/cells"
	"dm/internal/layout"
	"dm/internal/testutil"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"
)

// switcherMoveRe finds every absolute cursor move a float writes.
var switcherMoveRe = regexp.MustCompile("\x1b\\[([0-9]+);([0-9]+)H")

// switcherSizes are the screens the float must fit.
var switcherSizes = [][2]int{
	{200, 60}, {120, 40}, {80, 24}, {40, 12}, {20, 8}, {12, 5},
}

// switcherAt opens the switcher over the fixture on a cols by rows screen.
func switcherAt(cols, rows int) (*Switcher, *wire.Snapshot) {
	snap := switcherSnap()
	snap.Cols, snap.Rows = cols, rows
	return NewSwitcher(PaletteRect(cols, rows), snap), snap
}

// drawFloat draws the switcher and replays its bytes into a fresh screen.
func drawFloat(t *testing.T, s *Switcher, snap *wire.Snapshot, vts map[string]*vt.Term) (string, int, int, *vt.Term) {
	t.Helper()
	var b bytes.Buffer
	cx, cy := DrawSwitcher(&b, theme.DefaultTheme(), s, snap, vts, testPanePainter)
	screen := vt.NewTerm(snap.Cols, snap.Rows)
	screen.Write(b.Bytes())
	screen.Flush()
	return b.String(), cx, cy, screen
}

// testPanePainter paints a pane's rows the way the client's drawPaneFrom does
// with no selection or hover, so the preview test reads the real SGR output.
func testPanePainter(buf *bytes.Buffer, term *vt.Term, r layout.Rect, row0 int) {
	testutil.PaintTerm(buf, term, r, row0)
}

// bandRows returns the rows a band's bytes address, in screen order. It is a
// copy of the client's helper: the moved test cannot reach package main.
func bandRows(buf []byte) []int {
	var rows []int
	seen := map[int]bool{}
	for i := 0; i+2 < len(buf); i++ {
		if buf[i] != 0x1b || buf[i+1] != '[' {
			continue
		}
		j := i + 2
		n := 0
		for j < len(buf) && (buf[j] >= '0' && buf[j] <= '9') {
			n = n*10 + int(buf[j]-'0')
			j++
		}
		for j < len(buf) && (buf[j] == ';' || (buf[j] >= '0' && buf[j] <= '9')) {
			j++
		}
		if j >= len(buf) || (buf[j] != 'H' && buf[j] != 'f') {
			continue
		}
		y := 0
		if n > 1 {
			y = n - 1
		}
		if !seen[y] {
			seen[y] = true
			rows = append(rows, y)
		}
	}
	sort.Ints(rows)
	return rows
}

// switcherCell reads one cell of a replayed screen.
func switcherCell(screen *vt.Term, x, y int) string {
	return screen.Grid().Cell(x, y).Content
}

// switcherText reads one row of a replayed screen as a string, blanks dropped.
func switcherText(screen *vt.Term, x, y, w int) string {
	var b strings.Builder
	for i := 0; i < w; i++ {
		c := switcherCell(screen, x+i, y)
		if c == "" {
			c = " "
		}
		b.WriteString(c)
	}
	return strings.TrimRight(b.String(), " ")
}

// switcherMoves lists the cursor moves a float wrote as zero-based row, col.
func switcherMoves(out string) (rows, cols []int) {
	for _, m := range switcherMoveRe.FindAllStringSubmatch(out, -1) {
		y, _ := strconv.Atoi(m[1])
		x, _ := strconv.Atoi(m[2])
		rows = append(rows, y-1)
		cols = append(cols, x-1)
	}
	return rows, cols
}

// switcherFocusRows lists the rows whose bytes carry the focus background.
func switcherFocusRows(out string) []int {
	idx := switcherMoveRe.FindAllStringSubmatchIndex(out, -1)
	var rows []int
	for k, m := range idx {
		end := len(out)
		if k+1 < len(idx) {
			end = idx[k+1][0]
		}
		if strings.Contains(out[m[0]:end], theme.DefaultTheme().Palette.RailFocusBG.BG()) {
			y, _ := strconv.Atoi(out[m[2]:m[3]])
			rows = append(rows, y-1)
		}
	}
	return rows
}

// TestSwitcherRectRule pins the rule PaletteRect moved here with: a wide screen
// takes the 132 by 40 cap, a tiny screen takes the whole of it. pal_test.go
// already holds the fit loop over the same sizes.
func TestSwitcherRectRule(t *testing.T) {
	if r := PaletteRect(200, 60); r != (layout.Rect{X: 34, Y: 10, W: 132, H: 40}) {
		t.Fatalf("PaletteRect(200,60) = %+v, want 34,10 132x40", r)
	}
	if r := PaletteRect(12, 5); r != (layout.Rect{X: 0, Y: 0, W: 12, H: 5}) {
		t.Fatalf("PaletteRect(12,5) = %+v, want the whole screen", r)
	}
}

// TestSwitcherDrawInsideRect draws the float at every size and checks every move
// and every written cell stays inside the rect, that nothing clears the screen,
// and that the cursor lands inside the rect.
func TestSwitcherDrawInsideRect(t *testing.T) {
	for _, size := range switcherSizes {
		cols, rows := size[0], size[1]
		s, snap := switcherAt(cols, rows)
		out, cx, cy, screen := drawFloat(t, s, snap, nil)
		r := s.Rect
		if r.X < 0 || r.Y < 0 || r.X+r.W > cols || r.Y+r.H > rows {
			t.Fatalf("%dx%d: rect %+v is off screen", cols, rows, r)
		}
		if strings.Contains(out, "\x1b[2J") || strings.Contains(out, "\x1b[3J") {
			t.Fatalf("%dx%d: the float clears the screen", cols, rows)
		}
		moveRows, moveCols := switcherMoves(out)
		for i := range moveRows {
			y, x := moveRows[i], moveCols[i]
			if y < r.Y || y >= r.Y+r.H || x < r.X || x >= r.X+r.W {
				t.Fatalf("%dx%d: move lands at %d,%d outside %+v", cols, rows, x, y, r)
			}
		}
		for _, y := range bandRows([]byte(out)) {
			if y < r.Y || y >= r.Y+r.H {
				t.Fatalf("%dx%d: band row %d outside %+v", cols, rows, y, r)
			}
		}
		for y := 0; y < rows; y++ {
			for x := 0; x < cols; x++ {
				if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
					continue
				}
				if c := switcherCell(screen, x, y); c != "" && c != " " {
					t.Fatalf("%dx%d: cell %d,%d = %q outside %+v", cols, rows, x, y, c, r)
				}
			}
		}
		if cx < r.X || cx >= r.X+r.W || cy < r.Y || cy >= r.Y+r.H {
			t.Fatalf("%dx%d: cursor %d,%d outside %+v", cols, rows, cx, cy, r)
		}
	}
}

// TestSwitcherDrawQueryRow checks the query row reads "> " and the query, and
// the returned cursor sits one past the query's last cell.
func TestSwitcherDrawQueryRow(t *testing.T) {
	cols, rows := 80, 24
	s, snap := switcherAt(cols, rows)
	switcherType(s, "ap")
	_, cx, cy, screen := drawFloat(t, s, snap, nil)
	l := switcherLayoutOf(s, snap)
	r := s.Rect
	if cy != r.Y+1 {
		t.Fatalf("cursor row %d, want the first row inside the box %d", cy, r.Y+1)
	}
	line := switcherText(screen, r.X+1, cy, l.listW)
	if !strings.HasPrefix(line, "> ap") {
		t.Fatalf("query row = %q, want the > ap prefix", line)
	}
	if want := r.X + 1 + 2 + cells.Cells("ap"); cx != want {
		t.Fatalf("cursor col %d, want %d one past the query", cx, want)
	}
	// A query wider than the band is clipped and the cursor is clamped inside.
	s.ClearQuery()
	switcherType(s, strings.Repeat("x", 200))
	_, cx, cy, _ = drawFloat(t, s, snap, nil)
	if max := r.X + l.listW; cx != max {
		t.Fatalf("clipped cursor col %d, want the band's last cell %d", cx, max)
	}
	if cy != r.Y+1 {
		t.Fatalf("clipped cursor row %d, want %d", cy, r.Y+1)
	}
}

// TestSwitcherDrawFocusRow checks only the selected row carries the focus
// background.
func TestSwitcherDrawFocusRow(t *testing.T) {
	cols, rows := 40, 12
	s, snap := switcherAt(cols, rows)
	s.Move(2)
	_, _, _, _ = drawFloat(t, s, snap, nil)
	out, _, _, _ := drawFloat(t, s, snap, nil)
	r := s.Rect
	want := r.Y + 2 + (s.sel - s.top)
	rows_ := switcherFocusRows(out)
	if len(rows_) != 1 || rows_[0] != want {
		t.Fatalf("focus background on rows %v, want only %d", rows_, want)
	}
}

// TestSwitcherDrawPreview checks the preview paints the selected pane's terminal
// and the rule sits at the split.
func TestSwitcherDrawPreview(t *testing.T) {
	cols, rows := 120, 40
	s, snap := switcherAt(cols, rows)
	l := switcherLayoutOf(s, snap)
	if !l.preview {
		t.Fatalf("%dx%d: rect %+v dropped the preview", cols, rows, l.r)
	}
	pane := vt.NewTerm(20, 4)
	pane.Write([]byte("hello"))
	pane.Flush()
	vts := map[string]*vt.Term{"w1p1": pane}
	_, _, _, screen := drawFloat(t, s, snap, vts)
	// The pane's screen is bottom aligned in the band: four rows at the bottom.
	start := l.bandY + l.bandH - 4
	for i := 0; i < 5; i++ {
		if c := switcherCell(screen, l.prevX+i, start); c != string("hello"[i]) {
			t.Fatalf("preview cell %d,%d = %q, want %q", l.prevX+i, start, c, string("hello"[i]))
		}
	}
	// The rule runs down the split from the query row to the last band row.
	for y := l.queryY; y < l.bandY+l.bandH; y++ {
		if c := switcherCell(screen, l.ruleX, y); c != "│" {
			t.Fatalf("rule cell %d,%d = %q, want │", l.ruleX, y, c)
		}
	}
	// The split is where the list band ends.
	if want := l.inner.X + l.listW; l.ruleX != want {
		t.Fatalf("rule column %d, want the list band's end %d", l.ruleX, want)
	}
}

// TestSwitcherDrawNoPreviewData checks a band with no preview data is blank and
// never keeps the frame before it.
func TestSwitcherDrawNoPreviewData(t *testing.T) {
	cols, rows := 120, 40
	s, snap := switcherAt(cols, rows)
	l := switcherLayoutOf(s, snap)
	// A first frame with lines, then a request that drops them. Both frames are
	// replayed into one screen, so a cell the second frame leaves alone shows
	// the first frame's glyph: that is the stale cell this test hunts.
	s.SetPreview(1, "w1p1")
	if !s.AcceptPreview(1, []string{"old", "screen"}) {
		t.Fatal("the preview reply was refused")
	}
	screen := vt.NewTerm(cols, rows)
	var b bytes.Buffer
	DrawSwitcher(&b, theme.DefaultTheme(), s, snap, nil, testPanePainter)
	screen.Write(b.Bytes())
	screen.Flush()
	if c := switcherCell(screen, l.prevX, l.bandY+l.bandH-1); c == " " || c == "" {
		t.Fatalf("the first frame drew no preview lines: %q", c)
	}
	b.Reset()
	s.SetPreview(2, "w1p1")
	DrawSwitcher(&b, theme.DefaultTheme(), s, snap, nil, testPanePainter)
	screen.Write(b.Bytes())
	screen.Flush()
	for y := l.bandY; y < l.bandY+l.bandH; y++ {
		for x := l.prevX; x < l.prevX+l.prevW; x++ {
			if c := switcherCell(screen, x, y); c != "" && c != " " {
				t.Fatalf("stale preview cell %d,%d = %q", x, y, c)
			}
		}
	}
}

// TestSwitcherDrawNarrow checks a narrow rect drops the preview and gives the
// rows the whole width.
func TestSwitcherDrawNarrow(t *testing.T) {
	cols, rows := 40, 12
	s, snap := switcherAt(cols, rows)
	l := switcherLayoutOf(s, snap)
	if l.preview {
		t.Fatalf("%dx%d: rect %+v kept the preview", cols, rows, l.r)
	}
	_, _, _, screen := drawFloat(t, s, snap, nil)
	for y := l.queryY; y < l.bandY+l.bandH; y++ {
		for x := l.inner.X; x < l.inner.X+l.inner.W; x++ {
			if c := switcherCell(screen, x, y); c == "│" {
				t.Fatalf("narrow float drew a rule at %d,%d", x, y)
			}
		}
	}
	// The first band rows are the workspace header and the window line. The
	// pane row below them has no git stats to reach the right edge, so it still
	// fills the whole width.
	if c := switcherCell(screen, l.inner.X+l.inner.W-1, l.bandY+2); c != " " && c != "" {
		t.Fatalf("the narrow row did not fill the whole width: %q", c)
	}
}

// TestSwitcherDrawScroll checks a long list scrolls to keep the selection in the
// band and that a redraw does not move it.
func TestSwitcherDrawScroll(t *testing.T) {
	cols, rows := 40, 12
	s, snap := switcherAt(cols, rows)
	s.Move(100)
	l := switcherLayoutOf(s, snap)
	if l.bandH >= len(s.rows) {
		t.Fatalf("the band fits every row (%d >= %d), so it cannot scroll", l.bandH, len(s.rows))
	}
	_, _, _, screen := drawFloat(t, s, snap, nil)
	if s.top == 0 {
		t.Fatal("the list did not scroll to the selection")
	}
	y := l.bandY + (s.sel - s.top)
	if y < l.bandY || y >= l.bandY+l.bandH {
		t.Fatalf("the selected row sits at %d, outside the band %d..%d", y, l.bandY, l.bandY+l.bandH-1)
	}
	if text := switcherText(screen, l.inner.X, y, l.listW); !strings.Contains(text, "notes") {
		t.Fatalf("the selected row reads %q, want the last pane", text)
	}
	top := s.top
	_, _, _, _ = drawFloat(t, s, snap, nil)
	if s.top != top {
		t.Fatalf("a redraw moved the scroll offset from %d to %d", top, s.top)
	}
}
