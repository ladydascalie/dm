package switcher

import (
	"bytes"
	"fmt"
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

// switcherSizes are the floats the dashboard must fit: a palette-sized box on
// a screen of each shape, then the whole screen of the same shapes.
var switcherSizes = [][2]int{
	{200, 60}, {120, 40}, {80, 24}, {40, 12}, {20, 8}, {12, 5},
	{160, 30}, {61, 10}, {60, 9}, {40, 8},
}

// drawFloat draws the dashboard in the ASCII glyph set and replays its bytes
// into a fresh screen.
func drawFloat(t *testing.T, s *Switcher, snap *wire.Snapshot, vts map[string]*vt.Term) (string, int, int, *vt.Term) {
	t.Helper()
	var b bytes.Buffer
	cx, cy := DrawSwitcher(&b, theme.DefaultTheme(), s, snap, vts, testPanePainter, theme.GlyphASCII)
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

// switcherText reads one row of a replayed screen as a string, blanks dropped
// from the end.
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

// switcherOn opens the dashboard full screen on a cols by rows screen.
func switcherOn(cols, rows int) (*Switcher, *wire.Snapshot) {
	snap := switcherSnap()
	snap.Cols, snap.Rows = cols, rows
	return NewSwitcher(layout.Rect{W: cols, H: rows}, snap), snap
}

// switcherTableLine is the table's line for one row: the columns padded to the
// widths the table gives them at a ratio of 20 percent on a 160-column screen,
// with the trailing blanks dropped.
func switcherTableLine(jump, project, agent, git, status, clock, title string) string {
	line := fmt.Sprintf("%-1s %-10s %-14s %-8s %-10s %-4s %s",
		jump, project, agent, git, status, clock, title)
	return strings.TrimRight(line, " ")
}

// TestSwitcherRectRule pins the rule PaletteRect moved here with: a wide screen
// takes the 132 by 40 cap, a tiny screen takes the whole of it.
func TestSwitcherRectRule(t *testing.T) {
	if r := PaletteRect(200, 60); r != (layout.Rect{X: 34, Y: 10, W: 132, H: 40}) {
		t.Fatalf("PaletteRect(200,60) = %+v, want 34,10 132x40", r)
	}
	if r := PaletteRect(12, 5); r != (layout.Rect{X: 0, Y: 0, W: 12, H: 5}) {
		t.Fatalf("PaletteRect(12,5) = %+v, want the whole screen", r)
	}
}

// TestSwitcherDrawInsideRect draws the dashboard at every size and checks every
// move and every written cell stays inside the rect, that nothing clears the
// screen, and that the cursor lands inside the rect.
func TestSwitcherDrawInsideRect(t *testing.T) {
	for _, size := range switcherSizes {
		cols, rows := size[0], size[1]
		snap := switcherSnap()
		snap.Cols, snap.Rows = cols, rows
		for _, full := range []bool{true, false} {
			rect := layout.Rect{W: cols, H: rows}
			if !full {
				rect = PaletteRect(cols, rows)
			}
			s := NewSwitcher(rect, snap)
			out, cx, cy, screen := drawFloat(t, s, snap, nil)
			r := s.Rect
			if r.X < 0 || r.Y < 0 || r.X+r.W > cols || r.Y+r.H > rows {
				t.Fatalf("%dx%d full %v: rect %+v is off screen", cols, rows, full, r)
			}
			if strings.Contains(out, "\x1b[2J") || strings.Contains(out, "\x1b[3J") {
				t.Fatalf("%dx%d full %v: the float clears the screen", cols, rows, full)
			}
			moveRows, moveCols := switcherMoves(out)
			for i := range moveRows {
				y, x := moveRows[i], moveCols[i]
				if y < r.Y || y >= r.Y+r.H || x < r.X || x >= r.X+r.W {
					t.Fatalf("%dx%d full %v: move lands at %d,%d outside %+v", cols, rows, full, x, y, r)
				}
			}
			for _, y := range bandRows([]byte(out)) {
				if y < r.Y || y >= r.Y+r.H {
					t.Fatalf("%dx%d full %v: band row %d outside %+v", cols, rows, full, y, r)
				}
			}
			for y := 0; y < rows; y++ {
				for x := 0; x < cols; x++ {
					if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
						continue
					}
					if c := switcherCell(screen, x, y); c != "" && c != " " {
						t.Fatalf("%dx%d full %v: cell %d,%d = %q outside %+v", cols, rows, full, x, y, c, r)
					}
				}
			}
			if cx < r.X || cx >= r.X+r.W || cy < r.Y || cy >= r.Y+r.H {
				t.Fatalf("%dx%d full %v: cursor %d,%d outside %+v", cols, rows, full, cx, cy, r)
			}
		}
	}
}

// TestSwitcherDrawFrame draws one full-screen frame and reads it row by row:
// the query line, the column names, the five panes in the recent order with
// every column filled, and the footer hints.
func TestSwitcherDrawFrame(t *testing.T) {
	freezeSwitcherClock(t)
	cols, rows := 160, 30
	snap := switcherSnap()
	snap.Cols, snap.Rows = cols, rows
	s := NewSwitcher(layout.Rect{W: cols, H: rows}, snap)
	// A fifth of the body to the preview leaves the table its natural widths,
	// so the frame shows the columns rather than their elisions.
	s.ResizePreview(previewRatioMin - previewRatioDefault)
	_, cx, cy, screen := drawFloat(t, s, snap, nil)
	l := switcherLayoutOf(s, snap)
	if !l.preview || l.listW != 126 || l.prevW != 31 {
		t.Fatalf("layout table %d preview %d (%v), want 126 and 31", l.listW, l.prevW, l.preview)
	}
	if cy != l.queryY || cx != l.inner.X {
		t.Fatalf("cursor %d,%d, want %d,%d", cx, cy, l.inner.X, l.queryY)
	}
	want := []string{
		"press / to filter",
		switcherTableLine("#", "proj", "agent", "git", "status", "time", "title"),
		switcherTableLine("1", "dm", "serve", "+3 -12", "* working", "30s", "dm server"),
		switcherTableLine("2", "web", "build", "clean", "! blocked", "4m", "make all"),
		switcherTableLine("3", "dm", "nvim", "+7", "+ done", "2h", "api.go"),
		switcherTableLine("4", "web", "docs", "-", "o idle", "3d", ""),
		switcherTableLine("5", "-", "w4p1", "-", "> shell", "-", "notes"),
	}
	got := []string{switcherText(screen, l.inner.X, l.queryY, l.listW)}
	got = append(got, switcherText(screen, l.inner.X, l.headY, l.listW))
	for i := 0; i < s.RowCount(); i++ {
		got = append(got, switcherText(screen, l.inner.X, l.bodyY+i, l.listW))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %q, want %q", i, got[i], want[i])
		}
	}
	// The rows under the last pane are blank, and the rule holds the split.
	for i := s.RowCount(); i < l.bodyH; i++ {
		if text := switcherText(screen, l.inner.X, l.bodyY+i, l.listW); text != "" {
			t.Fatalf("blank row %d reads %q", i, text)
		}
	}
	for y := l.queryY; y < l.bodyY+l.bodyH; y++ {
		if c := switcherCell(screen, l.ruleX, y); c != "|" {
			t.Fatalf("rule cell %d,%d = %q, want |", l.ruleX, y, c)
		}
	}
	// The footer names every key the dashboard takes, then the ordering and the
	// preview's share at the right edge.
	wantLeft := "enter pick  esc close  1-9 jump  i copy id  d diff  " +
		"/ filter  s sort  p peek  +/- size  ^u/^d scroll"
	wantRight := "recent 20%"
	wantFoot := wantLeft + strings.Repeat(" ", l.inner.W-cells.Cells(wantLeft)-cells.Cells(wantRight)) + wantRight
	if foot := switcherText(screen, l.inner.X, l.footY, l.inner.W); foot != wantFoot {
		t.Fatalf("footer = %q, want %q", foot, wantFoot)
	}
}

// TestSwitcherSetFullScreen: the dashboard takes the whole screen or the
// palette rect, and a resize keeps the shape it has.
func TestSwitcherSetFullScreen(t *testing.T) {
	s, snap := switcherOn(160, 40)
	if got, want := s.Rect, (layout.Rect{W: 160, H: 40}); got != want {
		t.Fatalf("full rect = %+v, want %+v", got, want)
	}
	s.SetFullScreen(false)
	if got, want := s.Rect, PaletteRect(160, 40); got != want {
		t.Fatalf("palette rect = %+v, want %+v", got, want)
	}
	// A palette keeps its shape through a resize, and so does a full screen.
	snap.Cols, snap.Rows = 100, 24
	s.Sync(snap)
	if got, want := s.Rect, PaletteRect(100, 24); got != want {
		t.Fatalf("palette rect after resize = %+v, want %+v", got, want)
	}
	s.SetFullScreen(true)
	if got, want := s.Rect, (layout.Rect{W: 100, H: 24}); got != want {
		t.Fatalf("full rect after resize = %+v, want %+v", got, want)
	}
	// The palette stays inside a screen too small for its share.
	s.SetFullScreen(false)
	snap.Cols, snap.Rows = 20, 6
	s.Sync(snap)
	r := s.Rect
	if r.X < 0 || r.Y < 0 || r.X+r.W > 20 || r.Y+r.H > 6 {
		t.Fatalf("palette rect %+v misses the 20x6 screen", r)
	}
}

// TestSwitcherSyncFollowsScreen syncs the dashboard through a grow and a
// shrink and checks the rect and the drawn frame take the new size each time.
func TestSwitcherSyncFollowsScreen(t *testing.T) {
	s, snap := switcherOn(80, 24)
	for _, size := range [][2]int{{80, 24}, {120, 40}, {60, 20}} {
		cols, rows := size[0], size[1]
		snap.Cols, snap.Rows = cols, rows
		s.Sync(snap)
		if want := (layout.Rect{W: cols, H: rows}); s.Rect != want {
			t.Fatalf("after a sync at %dx%d rect = %+v, want %+v", cols, rows, s.Rect, want)
		}
		l := switcherLayoutOf(s, snap)
		if l.r != s.Rect {
			t.Fatalf("%dx%d: layout rect %+v, want the screen %+v", cols, rows, l.r, s.Rect)
		}
		_, _, _, screen := drawFloat(t, s, snap, nil)
		// The box corners sit on the screen corners, so the frame took the
		// whole screen rather than a leftover size.
		if c := switcherCell(screen, 0, 0); c != "+" {
			t.Fatalf("%dx%d: top left = %q, want the box corner", cols, rows, c)
		}
		if c := switcherCell(screen, cols-1, rows-1); c != "+" {
			t.Fatalf("%dx%d: bottom right = %q, want the box corner", cols, rows, c)
		}
		// The footer keeps to the new width.
		foot := switcherText(screen, 1, rows-2, cols-2)
		if !strings.HasSuffix(foot, "recent 60%") {
			t.Fatalf("%dx%d: footer = %q, want the ordering at its end", cols, rows, foot)
		}
	}
}

// TestSwitcherDrawFootDropsHints checks a narrow float keeps the hints that
// fit and drops the rest whole: it loses the scroll reminder before it loses
// the jump keys, and it never names half a key.
func TestSwitcherDrawFootDropsHints(t *testing.T) {
	s, snap := switcherOn(80, 24)
	l := switcherLayoutOf(s, snap)
	_, _, _, screen := drawFloat(t, s, snap, nil)
	foot := switcherText(screen, l.inner.X, l.footY, l.inner.W)
	wantLeft := "enter pick  esc close  1-9 jump  i copy id  d diff  / filter"
	wantRight := "recent 60%"
	want := wantLeft + strings.Repeat(" ", l.inner.W-cells.Cells(wantLeft)-cells.Cells(wantRight)) + wantRight
	if foot != want {
		t.Fatalf("footer = %q, want %q", foot, want)
	}
	for _, gone := range []string{"s sort", "p peek", "+/- size", "^u/^d scroll"} {
		if strings.Contains(foot, gone) {
			t.Fatalf("footer kept %q: %q", gone, foot)
		}
	}
}

// TestSwitcherDrawIdleQueryRow checks the query row says how to start filtering
// while the letters are keys, and that the row is blank but for that hint.
func TestSwitcherDrawIdleQueryRow(t *testing.T) {
	s, snap := switcherOn(80, 24)
	_, cx, cy, screen := drawFloat(t, s, snap, nil)
	l := switcherLayoutOf(s, snap)
	if cy != l.queryY || cx != l.inner.X {
		t.Fatalf("cursor %d,%d, want the query row's first cell %d,%d", cx, cy, l.inner.X, l.queryY)
	}
	line := switcherText(screen, l.inner.X, l.queryY, l.listW)
	if !strings.HasPrefix(line, "press / to filter") {
		t.Fatalf("idle query row = %q, want the filter hint", line)
	}
	// Entering the mode swaps the hint for the prompt.
	s.StartFilter()
	_, _, _, screen = drawFloat(t, s, snap, nil)
	if line := switcherText(screen, l.inner.X, l.queryY, l.listW); line != ">" {
		t.Fatalf("filter row = %q, want the bare > prompt", line)
	}
}

// TestSwitcherDrawQueryRow checks the query row reads "> " and the query in
// filter mode, and the returned cursor sits one past the query's last cell.
func TestSwitcherDrawQueryRow(t *testing.T) {
	s, snap := switcherOn(80, 24)
	switcherType(s, "ap")
	_, cx, cy, screen := drawFloat(t, s, snap, nil)
	l := switcherLayoutOf(s, snap)
	r := s.Rect
	if cy != l.queryY {
		t.Fatalf("cursor row %d, want the query row %d", cy, l.queryY)
	}
	line := switcherText(screen, r.X+1, cy, l.listW)
	if !strings.HasPrefix(line, "> ap") {
		t.Fatalf("query row = %q, want the > ap prefix", line)
	}
	if want := r.X + 1 + 2 + cells.Cells("ap"); cx != want {
		t.Fatalf("cursor col %d, want %d one past the query", cx, want)
	}
	// A query wider than the table is clipped and the cursor is clamped inside.
	s.ClearQuery()
	switcherType(s, strings.Repeat("x", 200))
	_, cx, cy, _ = drawFloat(t, s, snap, nil)
	if max := r.X + l.listW; cx != max {
		t.Fatalf("clipped cursor col %d, want the table's last cell %d", cx, max)
	}
	if cy != l.queryY {
		t.Fatalf("clipped cursor row %d, want %d", cy, l.queryY)
	}
}

// TestSwitcherDrawFocusRow checks only the selected row carries the focus
// background.
func TestSwitcherDrawFocusRow(t *testing.T) {
	s, snap := switcherOn(80, 24)
	s.Move(2)
	out, _, _, _ := drawFloat(t, s, snap, nil)
	l := switcherLayoutOf(s, snap)
	want := l.bodyY + (s.sel - s.top)
	focus := switcherFocusRows(out)
	if len(focus) != 1 || focus[0] != want {
		t.Fatalf("focus background on rows %v, want only %d", focus, want)
	}
}

// TestSwitcherDrawPreview checks the preview paints the selected pane's
// terminal in the band right of the split.
func TestSwitcherDrawPreview(t *testing.T) {
	s, snap := switcherOn(160, 30)
	l := switcherLayoutOf(s, snap)
	if !l.preview {
		t.Fatalf("rect %+v dropped the preview", l.r)
	}
	pane := vt.NewTerm(20, 4)
	pane.Write([]byte("hello"))
	pane.Flush()
	vts := map[string]*vt.Term{"w1p1": pane}
	_, _, _, screen := drawFloat(t, s, snap, vts)
	// The pane's screen is bottom aligned in the band: four rows at the bottom.
	start := l.bodyY + l.bodyH - 4
	for i := 0; i < 5; i++ {
		if c := switcherCell(screen, l.prevX+i, start); c != string("hello"[i]) {
			t.Fatalf("preview cell %d,%d = %q, want %q", l.prevX+i, start, c, string("hello"[i]))
		}
	}
}

// TestSwitcherDrawPreviewScroll checks the preview scrolls by rows from the
// newest one and paints the lines the model holds.
func TestSwitcherDrawPreviewScroll(t *testing.T) {
	s, snap := switcherOn(160, 30)
	s.ResizePreview(previewRatioMin - previewRatioDefault)
	l := switcherLayoutOf(s, snap)
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("L%02d", i)
	}
	s.SetPreview(1, "w1p1")
	s.AcceptPreview(1, lines)
	check := func(first, last string) {
		t.Helper()
		_, _, _, screen := drawFloat(t, s, snap, nil)
		if got := switcherText(screen, l.prevX, l.bodyY, l.prevW); got != first {
			t.Fatalf("preview top = %q, want %q", got, first)
		}
		if got := switcherText(screen, l.prevX, l.bodyY+l.bodyH-1, l.prevW); got != last {
			t.Fatalf("preview bottom = %q, want %q", got, last)
		}
	}
	// The newest rows fill the band first.
	check("L75", "L99")
	s.ScrollPreview(s.bodyRows() / 2)
	check("L63", "L87")
	s.ScrollPreview(1000)
	check("L00", "L24")
	s.ScrollPreview(-1000)
	check("L75", "L99")
}

// TestSwitcherDrawNoPreviewData checks a band with no preview data is blank and
// never keeps the frame before it.
func TestSwitcherDrawNoPreviewData(t *testing.T) {
	s, snap := switcherOn(160, 30)
	l := switcherLayoutOf(s, snap)
	// A first frame with lines, then a request that drops them. Both frames are
	// replayed into one screen, so a cell the second frame leaves alone shows
	// the first frame's glyph: that is the stale cell this test hunts.
	s.SetPreview(1, "w1p1")
	if !s.AcceptPreview(1, []string{"old", "screen"}) {
		t.Fatal("the preview reply was refused")
	}
	screen := vt.NewTerm(snap.Cols, snap.Rows)
	var b bytes.Buffer
	DrawSwitcher(&b, theme.DefaultTheme(), s, snap, nil, testPanePainter, theme.GlyphASCII)
	screen.Write(b.Bytes())
	screen.Flush()
	if c := switcherCell(screen, l.prevX, l.bodyY+l.bodyH-1); c == " " || c == "" {
		t.Fatalf("the first frame drew no preview lines: %q", c)
	}
	b.Reset()
	s.SetPreview(2, "w1p1")
	DrawSwitcher(&b, theme.DefaultTheme(), s, snap, nil, testPanePainter, theme.GlyphASCII)
	screen.Write(b.Bytes())
	screen.Flush()
	for y := l.bodyY; y < l.bodyY+l.bodyH; y++ {
		for x := l.prevX; x < l.prevX+l.prevW; x++ {
			if c := switcherCell(screen, x, y); c != "" && c != " " {
				t.Fatalf("stale preview cell %d,%d = %q", x, y, c)
			}
		}
	}
}

// TestSwitcherDrawNarrow checks a narrow float drops the preview and gives the
// table the whole width.
func TestSwitcherDrawNarrow(t *testing.T) {
	s, snap := switcherOn(40, 12)
	l := switcherLayoutOf(s, snap)
	if l.preview {
		t.Fatalf("rect %+v kept the preview", l.r)
	}
	if l.listW != l.inner.W {
		t.Fatalf("table width %d, want the whole inner width %d", l.listW, l.inner.W)
	}
	_, _, _, screen := drawFloat(t, s, snap, nil)
	for y := l.queryY; y < l.bodyY+l.bodyH; y++ {
		for x := l.inner.X; x < l.inner.X+l.inner.W; x++ {
			if c := switcherCell(screen, x, y); c == "|" {
				t.Fatalf("narrow float drew a rule at %d,%d", x, y)
			}
		}
	}
}

// TestSwitcherDrawPeek checks the peek key gives the table the whole width and
// the footer stops claiming a preview share.
func TestSwitcherDrawPeek(t *testing.T) {
	s, snap := switcherOn(160, 30)
	s.TogglePreview()
	l := switcherLayoutOf(s, snap)
	if l.preview || l.listW != l.inner.W {
		t.Fatalf("peek off keeps preview %v with table width %d", l.preview, l.listW)
	}
	_, _, _, screen := drawFloat(t, s, snap, nil)
	foot := switcherText(screen, l.inner.X, l.footY, l.inner.W)
	if !strings.HasSuffix(foot, "recent") {
		t.Fatalf("footer = %q, want the sort alone while the preview is hidden", foot)
	}
}

// TestSwitcherDrawScroll checks a long table scrolls to keep the selection in
// the body and that a redraw does not move it.
func TestSwitcherDrawScroll(t *testing.T) {
	snap := switcherCrowd(40)
	snap.Cols, snap.Rows = 160, 30
	s := NewSwitcher(layout.Rect{W: 160, H: 30}, snap)
	s.Move(30)
	l := switcherLayoutOf(s, snap)
	if l.bodyH >= len(s.rows) {
		t.Fatalf("the body fits every row (%d >= %d), so it cannot scroll", l.bodyH, len(s.rows))
	}
	_, _, _, screen := drawFloat(t, s, snap, nil)
	if s.top == 0 {
		t.Fatal("the table did not scroll to the selection")
	}
	y := l.bodyY + (s.sel - s.top)
	if y < l.bodyY || y >= l.bodyY+l.bodyH {
		t.Fatalf("the selected row sits at %d, outside the body %d..%d", y, l.bodyY, l.bodyY+l.bodyH-1)
	}
	if text := switcherText(screen, l.inner.X, y, l.listW); !strings.Contains(text, "pane30") {
		t.Fatalf("the selected row reads %q, want pane30", text)
	}
	top := s.top
	_, _, _, _ = drawFloat(t, s, snap, nil)
	if s.top != top {
		t.Fatalf("a redraw moved the scroll offset from %d to %d", top, s.top)
	}
}

// switcherCrowd is a session with n plain panes, for the scroll test.
func switcherCrowd(n int) *wire.Snapshot {
	snap := &wire.Snapshot{}
	for i := 0; i < n; i++ {
		snap.All = append(snap.All, wire.AllPane{
			ID: fmt.Sprintf("p%02d", i), Name: fmt.Sprintf("pane%02d", i), Cwd: "/tmp",
		})
	}
	return snap
}

// TestSwitcherColumns checks the table's column widths: the natural widths when
// they fit, the title's floor, and the floors that hold on a narrow table.
func TestSwitcherColumns(t *testing.T) {
	if got, want := switcherColumns(126), [7]int{1, 10, 14, 8, 10, 4, 73}; got != want {
		t.Fatalf("columns at 126 = %v, want %v", got, want)
	}
	got := switcherColumns(63)
	for i, w := range got {
		if w < 1 {
			t.Fatalf("column %d is %d cells wide", i, w)
		}
	}
	if sum := sum6([6]int{got[0], got[1], got[2], got[3], got[4], got[5]}) + 6 + got[6]; sum != 63 {
		t.Fatalf("columns at 63 add up to %d, want 63: %v", sum, got)
	}
	if got[6] < switcherTitleFloor {
		t.Fatalf("title column = %d, want at least %d", got[6], switcherTitleFloor)
	}
	small := switcherColumns(20)
	total := sum6([6]int{small[0], small[1], small[2], small[3], small[4], small[5]}) + 6 + small[6]
	if total != 20 {
		t.Fatalf("columns at 20 add up to %d, want 20: %v", total, small)
	}
}
