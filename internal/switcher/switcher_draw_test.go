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
	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/rail"
	"dm/internal/testutil"
	"dm/internal/theme"
	"dm/internal/ui"
	"dm/internal/vt"
	"dm/internal/wire"
)

// switcherMoveRe finds every absolute cursor move a float writes.
var switcherMoveRe = regexp.MustCompile("\x1b\\[([0-9]+);([0-9]+)H")

// switcherSizes are the floats the dashboard must fit: a palette-sized box on
// a screen of each shape, then the whole screen of the same shapes.
var switcherSizes = [][2]int{
	{200, 60},
	{120, 40},
	{80, 24},
	{40, 12},
	{20, 8},
	{12, 5},
	{160, 30},
	{61, 10},
	{60, 9},
	{40, 8},
}

// drawFloat draws the dashboard in the ASCII glyph set and replays its bytes
// into a fresh screen.
func drawFloat(t *testing.T, s *Switcher, snap *wire.Snapshot, vts map[string]*vt.Term) (string, int, int, *vt.Term) {
	t.Helper()
	var b bytes.Buffer
	cx, cy := DrawSwitcher(&b, theme.DefaultTheme(), s, snap, vts, testPanePainter, theme.GlyphASCII, keymap.LoadDefault())
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
	// This frame is the right split: a fifth of the body to the preview
	// leaves the table its natural widths, so it shows the columns rather
	// than their elisions.
	s.SetPeekDown(false)
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
	wantLeft := "enter pick | esc close | 1-9 jump | i copy id | d diff | " +
		"/ filter | s sort | p peek | +/- size | ctrl+u/ctrl+d scroll | D diff split"
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
	wantLeft := "enter pick | esc close | 1-9 jump | i copy id | d diff | / filter"
	wantRight := "recent 60%"
	want := wantLeft + strings.Repeat(" ", l.inner.W-cells.Cells(wantLeft)-cells.Cells(wantRight)) + wantRight
	if foot != want {
		t.Fatalf("footer = %q, want %q", foot, want)
	}
	for _, gone := range []string{"s sort", "p peek", "+/- size", "ctrl+u/ctrl+d scroll"} {
		if strings.Contains(foot, gone) {
			t.Fatalf("footer kept %q: %q", gone, foot)
		}
	}
}

// TestSwitcherDrawFootFoldsControlKeys checks a footer too narrow for the
// spelled-out keys retries with the caret form, so the scroll reminder keeps
// the cells the full key name would spend. A wide footer keeps the full name.
func TestSwitcherDrawFootFoldsControlKeys(t *testing.T) {
	// 139 columns leaves the hints 125 cells once the mode text and its gap
	// are paid for: one cell short of the spelled-out keys, exact for the
	// folded ones.
	s, snap := switcherOn(139, 30)
	l := switcherLayoutOf(s, snap)
	_, _, _, screen := drawFloat(t, s, snap, nil)
	foot := switcherText(screen, l.inner.X, l.footY, l.inner.W)
	if !strings.Contains(foot, "^u/^d scroll") {
		t.Fatalf("narrow footer = %q, want ^u/^d scroll", foot)
	}
	if strings.Contains(foot, "ctrl+u") {
		t.Fatalf("narrow footer kept the full key name: %q", foot)
	}

	wide, wsnap := switcherOn(200, 40)
	wl := switcherLayoutOf(wide, wsnap)
	_, _, _, wscreen := drawFloat(t, wide, wsnap, nil)
	wfoot := switcherText(wscreen, wl.inner.X, wl.footY, wl.inner.W)
	if !strings.Contains(wfoot, "ctrl+u/ctrl+d scroll") {
		t.Fatalf("wide footer = %q, want ctrl+u/ctrl+d scroll", wfoot)
	}
	if strings.Contains(wfoot, "^u/^d") {
		t.Fatalf("wide footer folded a key that fits: %q", wfoot)
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
	want := l.bodyY + (s.vp.Sel - s.vp.Off)
	focus := switcherFocusRows(out)
	if len(focus) != 1 || focus[0] != want {
		t.Fatalf("focus background on rows %v, want only %d", focus, want)
	}
}

// TestSwitcherDrawPreview checks the preview paints the selected pane's
// terminal in the band right of the split.
func TestSwitcherDrawPreview(t *testing.T) {
	s, snap := switcherOn(160, 30)
	s.SetPeekDown(false)
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
	s.SetPeekDown(false)
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
	s.SetPeekDown(false)
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
	DrawSwitcher(&b, theme.DefaultTheme(), s, snap, nil, testPanePainter, theme.GlyphASCII, keymap.LoadDefault())
	screen.Write(b.Bytes())
	screen.Flush()
	if c := switcherCell(screen, l.prevX, l.bodyY+l.bodyH-1); c == " " || c == "" {
		t.Fatalf("the first frame drew no preview lines: %q", c)
	}
	b.Reset()
	s.SetPreview(2, "w1p1")
	DrawSwitcher(&b, theme.DefaultTheme(), s, snap, nil, testPanePainter, theme.GlyphASCII, keymap.LoadDefault())
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
	if s.vp.Off == 0 {
		t.Fatal("the table did not scroll to the selection")
	}
	y := l.bodyY + (s.vp.Sel - s.vp.Off)
	if y < l.bodyY || y >= l.bodyY+l.bodyH {
		t.Fatalf("the selected row sits at %d, outside the body %d..%d", y, l.bodyY, l.bodyY+l.bodyH-1)
	}
	if text := switcherText(screen, l.inner.X, y, l.listW); !strings.Contains(text, "pane30") {
		t.Fatalf("the selected row reads %q, want pane30", text)
	}
	top := s.vp.Off
	_, _, _, _ = drawFloat(t, s, snap, nil)
	if s.vp.Off != top {
		t.Fatalf("a redraw moved the scroll offset from %d to %d", top, s.vp.Off)
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

// TestSwitcherRowPinnedAccentFG: the project column paints the pinned accent
// slot the server carries, the hash when the snapshot predates the field.
func TestSwitcherRowPinnedAccentFG(t *testing.T) {
	th := theme.DefaultTheme()
	p := wire.AllPane{ID: "w1p1", Project: "a", ProjectAccent: 3, ProjectAccentSet: true}
	if got := switcherRowCells(th, theme.GlyphASCII, p, false, 0, 0)[1][0].FG; got != th.Accents[3] {
		t.Fatalf("pinned project FG = %v, want %v", got, th.Accents[3])
	}
	p.ProjectAccentSet = false
	if got := switcherRowCells(th, theme.GlyphASCII, p, false, 0, 0)[1][0].FG; got != th.Accents[rail.RailAccentSlot("a")] {
		t.Fatalf("fallback project FG = %v, want %v", got, th.Accents[rail.RailAccentSlot("a")])
	}
}

// TestSwitcherHintNamesTheKeymap: the footer and the idle query row name the
// keys the switcher keymap binds, so a rebind moves both, and a hint the config
// leaves unbound drops out of the footer whole.
func TestSwitcherHintNamesTheKeymap(t *testing.T) {
	cfg, err := keymap.ParseConfig(`keymap "switcher" {
		"pick" "ctrl+p"
		"close-esc" "ctrl+q"
		"filter" "f"
	}`)
	if err != nil {
		t.Fatalf("parse test keymap: %v", err)
	}
	if got, want := switcherHint(200, cfg), "ctrl+p pick | ctrl+q close | f filter"; got != want {
		t.Fatalf("footer = %q, want %q", got, want)
	}
	if got, want := switcherIdleHint(cfg), "press f to filter"; got != want {
		t.Fatalf("idle query row = %q, want %q", got, want)
	}
	if got := switcherHint(200, nil); got != "" {
		t.Fatalf("footer without a config = %q, want empty", got)
	}
}

// switcherVTColor is a theme colour as the replayed screen keeps it, for
// comparing a cell's style.
func switcherVTColor(c theme.RGB) vt.Color {
	return vt.RGBColor(uint8(c.R), uint8(c.G), uint8(c.B))
}

// TestSwitcherDrawShelfHeader proves the drawer inserts one header row before
// the first shelved pane: the word on the left, the rest of the row a rail-dim
// rule, no jump digit, and the pane after it keeps the number its index gives.
func TestSwitcherDrawShelfHeader(t *testing.T) {
	freezeSwitcherClock(t)
	snap := switcherShelfSnap()
	snap.Cols, snap.Rows = 160, 30
	s := NewSwitcher(layout.Rect{W: 160, H: 30}, snap)
	l := switcherLayoutOf(s, snap)
	_, _, _, screen := drawFloat(t, s, snap, nil)
	start := s.ShelfStart()
	if start != 4 {
		t.Fatalf("ShelfStart = %d, want 4", start)
	}
	y := l.bodyY + start
	text := switcherText(screen, l.inner.X, y, l.listW)
	if got, want := text, "shelf"+strings.Repeat("-", l.listW-5); got != want {
		t.Fatalf("header row = %q, want the word then a rule to the table's edge", got)
	}
	// The header takes no jump digit: the cell before the word is the row's
	// first cell, and the word starts there.
	if c := switcherCell(screen, l.inner.X-1, y); c != "|" && c != "\u2502" {
		// The cell before the inner rect is the box border; it is not a digit.
		t.Fatalf("cell before the header = %q, want the box border", c)
	}
	// The colours: the word in the rail header colour, the rule in the rail
	// dim, both on the rail ground.
	th := theme.DefaultTheme()
	bg, header, dim := switcherVTColor(th.Palette.RailBG), switcherVTColor(th.Palette.RailHeader), switcherVTColor(th.Palette.RailDim)
	for i := 0; i < 5; i++ {
		c := screen.Grid().Cell(l.inner.X+i, y)
		if c.Style.Fg != header || c.Style.Bg != bg {
			t.Fatalf("header word cell %d = %+v, want the rail header on the rail ground", i, c.Style)
		}
	}
	c := screen.Grid().Cell(l.inner.X+5, y)
	if c.Content != "-" || c.Style.Fg != dim || c.Style.Bg != bg {
		t.Fatalf("header rule cell = %q %+v, want a rail-dim rule on the rail ground", c.Content, c.Style)
	}
	// The row after the header is the first shelved pane, and its jump digit
	// is the 1-based index in the pane rows: five, because four live rows
	// precede it.
	if got := switcherCell(screen, l.inner.X, y+1); got != "5" {
		t.Fatalf("jump digit after the header = %q, want 5", got)
	}
	if got := switcherText(screen, l.inner.X, y+1, l.listW); !strings.Contains(got, "serve") {
		t.Fatalf("row after the header = %q, want the first shelved pane", got)
	}
	// The live rows keep their numbers above the header.
	if got := switcherCell(screen, l.inner.X, l.bodyY); got != "1" {
		t.Fatalf("first live row's jump digit = %q, want 1", got)
	}
}

// TestSwitcherDrawNoShelfHeader: a table with nothing shelved paints no header
// row, so every body row is a pane row.
func TestSwitcherDrawNoShelfHeader(t *testing.T) {
	freezeSwitcherClock(t)
	s, snap := switcherOn(160, 30)
	l := switcherLayoutOf(s, snap)
	_, _, _, screen := drawFloat(t, s, snap, nil)
	for i := 0; i < l.bodyH; i++ {
		if text := switcherText(screen, l.inner.X, l.bodyY+i, l.listW); strings.HasPrefix(text, "shelf") {
			t.Fatalf("body row %d painted a shelf header with nothing shelved: %q", i, text)
		}
	}
}

// switcherShelfCrowd is a session long enough that the shelf starts below the
// first window: 40 live panes then three shelved ones.
func switcherShelfCrowd() *wire.Snapshot {
	snap := &wire.Snapshot{Cols: 160, Rows: 30}
	for i := 0; i < 40; i++ {
		snap.All = append(snap.All, wire.AllPane{
			ID: fmt.Sprintf("p%02d", i), Name: fmt.Sprintf("pane%02d", i),
			Cwd: "/tmp", StatusChangedAt: int64(1000 - i),
		})
	}
	for i := 0; i < 3; i++ {
		snap.All = append(snap.All, wire.AllPane{
			ID: fmt.Sprintf("s%02d", i), Name: fmt.Sprintf("kept%02d", i), Cwd: "/tmp",
			Shelf: true,
		})
	}
	return snap
}

// shelfHeaderRow finds the shelf header's screen row in a drawn frame, or -1.
func shelfHeaderRow(screen *vt.Term, l switcherLayout) int {
	for i := 0; i < l.bodyH; i++ {
		y := l.bodyY + i
		if strings.HasPrefix(switcherText(screen, l.inner.X, y, l.listW), "shelf-") {
			return y
		}
	}
	return -1
}

// TestSwitcherDrawShelfScrolled scrolls the table into the shelf: the header
// stays on screen above the rows it names, and the selection stays in the body.
func TestSwitcherDrawShelfScrolled(t *testing.T) {
	snap := switcherShelfCrowd()
	s := NewSwitcher(layout.Rect{W: 160, H: 30}, snap)
	l := switcherLayoutOf(s, snap)
	if l.bodyH >= s.RowCount()+1 {
		t.Fatalf("the body fits the whole list (%d rows, %d height), so it cannot scroll", s.RowCount()+1, l.bodyH)
	}
	// The first shelved row sits right under the header, both on screen.
	if !s.Jump(41) {
		t.Fatal("jump 41 found no row")
	}
	_, _, _, screen := drawFloat(t, s, snap, nil)
	header := shelfHeaderRow(screen, l)
	if header < 0 {
		t.Fatalf("the header is off screen with the first shelved row selected: rows %v", s.vp)
	}
	selY := l.bodyY + (s.vp.Sel - s.vp.Off)
	if selY != header+1 {
		t.Fatalf("the selected row sits at %d, want %d, right under the header", selY, header+1)
	}
	// The last shelved row scrolls the whole shelf into view with the header.
	s.Move(s.RowCount())
	_, _, _, screen = drawFloat(t, s, snap, nil)
	header = shelfHeaderRow(screen, l)
	if header < 0 {
		t.Fatalf("the header scrolled off screen with the shelf in view: %v", s.vp)
	}
	selY = l.bodyY + (s.vp.Sel - s.vp.Off)
	if selY <= header || selY >= l.bodyY+l.bodyH {
		t.Fatalf("the selected row sits at %d, want it on screen below the header at %d", selY, header)
	}
	if text := switcherText(screen, l.inner.X, selY, l.listW); !strings.Contains(text, "kept02") {
		t.Fatalf("the selected row = %q, want the last shelved pane", text)
	}
}

// TestSwitcherDrawPeekDown checks the down split's geometry: the table above a
// horizontal rule and the band below it, full inner width, with the table and
// the band both keeping their floors.
func TestSwitcherDrawPeekDown(t *testing.T) {
	s, snap := switcherOn(160, 30)
	if !s.PeekDown() {
		t.Fatal("the default band is not the down split")
	}
	l := switcherLayoutOf(s, snap)
	if !l.preview || !l.down {
		t.Fatalf("layout %+v did not split down", l)
	}
	if l.prevX != l.inner.X || l.prevW != l.inner.W {
		t.Fatalf("band at %d width %d, want the whole inner width from %d", l.prevX, l.prevW, l.inner.X)
	}
	avail := l.footY - l.bodyY
	if l.bodyH+l.prevH+1 != avail {
		t.Fatalf("table %d + rule 1 + band %d = %d, want the body's %d rows", l.bodyH, l.prevH, l.bodyH+l.prevH+1, avail)
	}
	if l.bodyH < 3 || l.prevH < 3 {
		t.Fatalf("table %d band %d, want at least three rows each", l.bodyH, l.prevH)
	}
	if l.ruleY != l.bodyY+l.bodyH {
		t.Fatalf("rule row = %d, want %d under the table", l.ruleY, l.bodyY+l.bodyH)
	}
	if l.prevY != l.ruleY+1 {
		t.Fatalf("band starts at %d, want %d under the rule", l.prevY, l.ruleY+1)
	}
	// The rule row is a rail-dim rule on the rail ground across the inner
	// width, and nothing else paints there.
	_, _, _, screen := drawFloat(t, s, snap, nil)
	if got, want := switcherText(screen, l.inner.X, l.ruleY, l.inner.W), strings.Repeat("-", l.inner.W); got != want {
		t.Fatalf("rule row = %q, want %q", got, want)
	}
	th := theme.DefaultTheme()
	c := screen.Grid().Cell(l.inner.X, l.ruleY)
	if c.Style.Fg != switcherVTColor(th.Palette.RailDim) || c.Style.Bg != switcherVTColor(th.Palette.RailBG) {
		t.Fatalf("rule cell style = %+v, want the rail dim on the rail ground", c.Style)
	}
	// The preview paints from the same band rect: a line fills the band's row
	// across the inner width.
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = fmt.Sprintf("L%02d", i)
	}
	s.SetPreview(1, "w1p1")
	if !s.AcceptPreview(1, lines) {
		t.Fatal("the preview reply was refused")
	}
	_, _, _, screen = drawFloat(t, s, snap, nil)
	if got := switcherText(screen, l.inner.X, l.prevY+l.prevH-1, l.inner.W); got != "L39" {
		t.Fatalf("band bottom = %q, want the newest line at the band's last row", got)
	}
	// The query row, the column header and the footer keep their places.
	if got := switcherText(screen, l.inner.X, l.queryY, l.inner.W); !strings.HasPrefix(got, "press / to filter") {
		t.Fatalf("query row = %q, want the filter hint in its place", got)
	}
	if got := switcherText(screen, l.inner.X, l.headY, l.inner.W); !strings.HasPrefix(got, "#") {
		t.Fatalf("column header = %q, want it under the query row", got)
	}
	if got := switcherText(screen, l.inner.X, l.footY, l.inner.W); !strings.HasSuffix(got, "recent 60%") {
		t.Fatalf("footer = %q, want the mode text in its place", got)
	}
}

// TestSwitcherDrawPeekDownFloors pins the down split's short-body fallback: a
// body too short for the table floor, the rule and the band floor holds no band
// at all, and a body of exactly seven rows holds the floors.
func TestSwitcherDrawPeekDownFloors(t *testing.T) {
	// Ten rows passes the 60x10 preview floor but leaves the body five rows.
	s, snap := switcherOn(160, 10)
	if !s.PreviewOn() {
		t.Fatal("a 160x10 float dropped the band before the body rule")
	}
	l := switcherLayoutOf(s, snap)
	if l.preview || l.down {
		t.Fatalf("a five-row body kept the band: %+v", l)
	}
	if l.listW != l.inner.W {
		t.Fatalf("the table width = %d, want the whole inner width %d", l.listW, l.inner.W)
	}
	// Twelve rows leaves the body exactly seven: three table rows, the rule,
	// three band rows.
	s, snap = switcherOn(160, 12)
	l = switcherLayoutOf(s, snap)
	if !l.preview || !l.down {
		t.Fatalf("a seven-row body dropped the band: %+v", l)
	}
	if l.bodyH != 3 || l.prevH != 3 {
		t.Fatalf("table %d band %d, want three rows each at the floor", l.bodyH, l.prevH)
	}
}

// TestSwitcherDrawPeekDownScroll scrolls the band under the table: the lines
// move by rows from the newest one, the same way the right split does.
func TestSwitcherDrawPeekDownScroll(t *testing.T) {
	s, snap := switcherOn(160, 30)
	l := switcherLayoutOf(s, snap)
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("L%02d", i)
	}
	s.SetPreview(1, "w1p1")
	if !s.AcceptPreview(1, lines) {
		t.Fatal("the preview reply was refused")
	}
	check := func(first, last string) {
		t.Helper()
		screen := vt.NewTerm(snap.Cols, snap.Rows)
		var b bytes.Buffer
		DrawSwitcher(&b, theme.DefaultTheme(), s, snap, nil, testPanePainter, theme.GlyphASCII, keymap.LoadDefault())
		screen.Write(b.Bytes())
		screen.Flush()
		if got := switcherText(screen, l.inner.X, l.prevY, l.inner.W); got != first {
			t.Fatalf("band top = %q, want %q", got, first)
		}
		if got := switcherText(screen, l.inner.X, l.prevY+l.prevH-1, l.inner.W); got != last {
			t.Fatalf("band bottom = %q, want %q", got, last)
		}
	}
	check("L85", "L99")
	s.ScrollPreview(l.prevH / 2)
	check("L78", "L92")
	s.ScrollPreview(1000)
	check("L00", "L14")
	s.ScrollPreview(-1000)
	check("L85", "L99")
}

// TestSwitcherDrawFootGround paints the whole footer row on the rail hover
// ground and lays the hints out as segments: the keys in the bar accent and
// bold, the labels in the rail text, and the separators in the rail dim.
func TestSwitcherDrawFootGround(t *testing.T) {
	s, snap := switcherOn(80, 24)
	l := switcherLayoutOf(s, snap)
	_, _, _, screen := drawFloat(t, s, snap, nil)
	th := theme.DefaultTheme()
	hover := switcherVTColor(th.Palette.RailHoverBG)
	for x := l.inner.X; x < l.inner.X+l.inner.W; x++ {
		if c := screen.Grid().Cell(x, l.footY); c.Style.Bg != hover {
			t.Fatalf("footer cell %d background = %+v, want the rail hover ground", x, c.Style.Bg)
		}
	}
	// The first hint's key is bold in the bar accent, and its label is the
	// rail text on the same ground.
	key := screen.Grid().Cell(l.inner.X, l.footY)
	if key.Content != "e" || key.Style.Fg != switcherVTColor(th.Palette.BarAccent) || key.Style.Attrs&vt.Bold == 0 {
		t.Fatalf("first key cell = %q %+v, want a bold bar-accent key", key.Content, key.Style)
	}
	label := screen.Grid().Cell(l.inner.X+6, l.footY)
	if label.Content != "p" || label.Style.Fg != switcherVTColor(th.Palette.RailText) || label.Style.Attrs&vt.Bold != 0 {
		t.Fatalf("label cell = %q %+v, want the rail text in the row's weight", label.Content, label.Style)
	}
	// The separator between the first two hints is a rail-dim bar with the
	// spaces around it.
	sep := switcherText(screen, l.inner.X+10, l.footY, 3)
	if sep != " |" {
		t.Fatalf("separator = %q, want a bar between the hints' spaces", sep)
	}
	if c := screen.Grid().Cell(l.inner.X+12, l.footY); c.Style.Fg != switcherVTColor(th.Palette.RailDim) {
		t.Fatalf("separator bar colour = %+v, want the rail dim", c.Style.Fg)
	}
	// The mode text keeps the rail dim on the same ground, and the row ends at
	// the inner rect's last cell.
	right := screen.Grid().Cell(l.inner.X+l.inner.W-1, l.footY)
	if right.Style.Fg != switcherVTColor(th.Palette.RailDim) || right.Style.Bg != hover {
		t.Fatalf("footer right end = %+v, want the rail dim on the hover ground", right.Style)
	}
	if c := screen.Grid().Cell(l.inner.X+l.inner.W, l.footY); c.Content != "|" {
		t.Fatalf("cell past the footer = %q, want the box border", c.Content)
	}
}

// TestSwitcherHintMeasuresSeparators pins the fit rule with the separators in
// it: two hints cost their own cells and the run between them, and one cell
// short of that drops the second hint whole.
func TestSwitcherHintMeasuresSeparators(t *testing.T) {
	km := keymap.LoadDefault()
	hints := switcherHints(km)
	first, second := ui.Hints(hints[:1]), ui.Hints(hints[1:2])
	room := cells.Cells(first) + cells.Cells(switcherHintSep) + cells.Cells(second)
	if got, want := switcherHint(room, km), first+switcherHintSep+second; got != want {
		t.Fatalf("hints at %d cells = %q, want %q", room, got, want)
	}
	if got := switcherHint(room-1, km); got != first {
		t.Fatalf("hints at %d cells = %q, want the first hint alone", room-1, got)
	}
	// The fold retry measures the separators too: the whole list at the folded
	// width keeps every hint, one cell under it drops the last.
	folded := make([]ui.Hint, len(hints))
	for i, h := range hints {
		keys := make([]string, len(h.Keys))
		for j, k := range h.Keys {
			keys[j] = ui.ShortKey(k)
		}
		folded[i] = ui.Hint{Keys: keys, Label: h.Label}
	}
	full := cells.Cells(switcherHint(100000, km))
	foldWidth := full - (cells.Cells("ctrl+u/ctrl+d scroll") - cells.Cells("^u/^d scroll"))
	if got := switcherHint(foldWidth-1, km); !strings.Contains(got, "^u/^d scroll") {
		t.Fatalf("folded hints at %d cells = %q, want the folded scroll reminder", foldWidth-1, got)
	}
}

// TestSwitcherDrawAskMark: a pane the snapshot lists a question on leads its
// agent cell with the ask mark, the same mark the rail paints, and no other
// pane does.
func TestSwitcherDrawAskMark(t *testing.T) {
	freezeSwitcherClock(t)
	snap := switcherSnap()
	snap.Cols, snap.Rows = 160, 30
	snap.Ask = []wire.Ask{{ID: "a1", Pane: "w2p1"}}
	s := NewSwitcher(layout.Rect{W: 160, H: 30}, snap)
	l := switcherLayoutOf(s, snap)
	_, _, _, screen := drawFloat(t, s, snap, nil)
	// The row for w2p1 is the second in the recent order. Its agent column
	// starts after the jump digit and the project column, each with its
	// separator.
	agentX := l.inner.X + 1 + 1 + switcherColumns(l.listW)[1] + 1
	y := l.bodyY + 1
	th := theme.DefaultTheme()
	mark := screen.Grid().Cell(agentX, y)
	if mark.Content != "?" {
		t.Fatalf("agent cell = %q, want the ask mark", mark.Content)
	}
	if mark.Style.Fg != switcherVTColor(th.Palette.BarAccent) || mark.Style.Attrs&vt.Bold == 0 {
		t.Fatalf("ask mark style = %+v, want a bold bar-accent mark", mark.Style)
	}
	if c := screen.Grid().Cell(agentX+1, y); c.Content != " " {
		t.Fatalf("cell after the ask mark = %q, want the space before the name", c.Content)
	}
	if got := switcherText(screen, agentX+2, y, 10); !strings.HasPrefix(got, "build") {
		t.Fatalf("agent cell after the mark = %q, want the pane name", got)
	}
	// A pane with no question leads with its name, so the mark adds no column.
	if got := switcherText(screen, agentX, l.bodyY, 10); !strings.HasPrefix(got, "serve") {
		t.Fatalf("unasked pane's agent cell = %q, want the name at the column's head", got)
	}
	// The mark clears with a snapshot that lists no question.
	s.Sync(switcherSnap())
	_, _, _, screen = drawFloat(t, s, snap, nil)
	if got := switcherText(screen, agentX, y, 10); !strings.HasPrefix(got, "build") {
		t.Fatalf("agent cell after the question closed = %q, want the name alone", got)
	}
}
