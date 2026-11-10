package client

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"dm/internal/cells"
	"dm/internal/rail"
	"dm/internal/testutil"
	"dm/internal/theme"
	"dm/internal/wire"
)

func toastTexts(t *tui) []string {
	out := make([]string, 0, len(t.toasts))
	for _, x := range t.toasts {
		out = append(out, x.text)
	}
	return out
}

// A repeat of the newest text inside the dedupe window is one toast; the same
// text after the window is a second one.
func TestToastDedupesRepeats(t *testing.T) {
	tui := &tui{}
	tui.notify("busy")
	tui.notify("busy")
	if got := toastTexts(tui); len(got) != 1 {
		t.Fatalf("toasts = %q, want one", got)
	}
	// Age the toast past the window: the next repeat is a new toast.
	tui.toasts[0].at = time.Now().Add(-2 * time.Second)
	tui.notify("busy")
	if got := toastTexts(tui); len(got) != 2 {
		t.Fatalf("toasts = %q, want two", got)
	}
	// Only consecutive repeats join; a different text in between splits them.
	tui.notify("other")
	tui.notify("busy")
	if got := toastTexts(tui); len(got) != 3 {
		t.Fatalf("toasts = %q, want three", got)
	}
}

// The stack keeps the newest three and drops the oldest.
func TestToastCapsAtThree(t *testing.T) {
	tui := &tui{}
	for _, s := range []string{"one", "two", "three", "four"} {
		tui.notify(s)
	}
	got := strings.Join(toastTexts(tui), ",")
	if want := "two,three,four"; got != want {
		t.Fatalf("toasts = %q, want %q", got, want)
	}
}

// A toast lives three seconds and no longer.
func TestToastExpires(t *testing.T) {
	tui := &tui{}
	tui.notify("short")
	raised := tui.toasts[0].at
	tui.expireToasts(raised.Add(2 * time.Second))
	if len(tui.toasts) != 1 {
		t.Fatalf("toast expired early: %+v", tui.toasts)
	}
	tui.expireToasts(raised.Add(4 * time.Second))
	if len(tui.toasts) != 0 {
		t.Fatalf("toast outlived its life: %+v", tui.toasts)
	}
}

var cursorAt = regexp.MustCompile("\x1b\\[([0-9]+);([0-9]+)H")

// toastRow returns the screen row a text was drawn at, from the cursor position
// printed just before it.
func toastRow(t *testing.T, out, text string) (row, col int) {
	t.Helper()
	at := strings.Index(out, text)
	if at < 0 {
		t.Fatalf("toast %q missing from drawing", text)
	}
	best := -1
	row, col = 0, 0
	for _, m := range cursorAt.FindAllStringSubmatchIndex(out, -1) {
		if m[1] > at {
			break
		}
		if m[1] > best {
			best = m[1]
			row, _ = strconv.Atoi(out[m[2]:m[3]])
			col, _ = strconv.Atoi(out[m[4]:m[5]])
		}
	}
	if best < 0 {
		t.Fatalf("toast %q has no cursor position before it", text)
	}
	return row, col
}

// The stack draws both texts, newest nearest the bar, one row apart between
// the boxes.
func TestDrawToastsStack(t *testing.T) {
	snap := &wire.Snapshot{Cols: 40, Rows: 12}
	tui := &tui{}
	tui.notify("older")
	tui.notify("newest")
	var b bytes.Buffer
	drawToasts(&b, theme.DefaultTheme(), snap, tui.toasts)
	out := b.String()
	oldRow, _ := toastRow(t, out, "older")
	newRow, _ := toastRow(t, out, "newest")
	if !(oldRow < newRow) {
		t.Fatalf("rows older=%d newest=%d, want newest below", oldRow, newRow)
	}
	if newRow != snap.Rows-2 {
		t.Fatalf("newest row = %d, want %d inside the box just above the bar", newRow, snap.Rows-2)
	}
}

// A text wider than the screen is clamped and truncated, not wrapped.
func TestDrawToastsClampsToScreen(t *testing.T) {
	cols := 12
	snap := &wire.Snapshot{Cols: cols, Rows: 6}
	tui := &tui{}
	tui.notify("a toast much longer than the screen")
	var b bytes.Buffer
	drawToasts(&b, theme.DefaultTheme(), snap, tui.toasts)
	out := b.String()
	row, col := toastRow(t, out, "a toast m"+cells.Ellipsis)
	if row != snap.Rows-2 {
		t.Fatalf("row = %d, want %d", row, snap.Rows-2)
	}
	// Left border, cols-2 inner cells, right border.
	if col != 1 {
		t.Fatalf("col = %d, want the box flush at 1", col)
	}
	if !strings.Contains(out, "a toast m"+cells.Ellipsis) {
		t.Fatalf("overflowing text was not truncated with an ellipsis: %q", out)
	}
}

// The bar keeps its content and no longer carries the toast suffix.
func TestBarDropsToastSuffix(t *testing.T) {
	snap := &wire.Snapshot{
		Session: "main", Workspace: "main", Windows: testWindows("1"), Focus: "w1p1",
		Cols: 60, Rows: 24,
		Panes: []wire.PaneInfo{{ID: "w1p1", Status: "working"}},
	}
	info := "SCROLL w1p1 +1/9"
	glyph, _ := rail.RailGlyph(theme.DefaultTheme(), "working", theme.GlyphNerd)
	want := []string{
		"main", " ", "main", " ", "▸ ", "1 1", " ", glyph,
		" w1p1", " working", info,
	}
	got := testutil.BarTexts(barSegments(theme.DefaultTheme(), snap, false, info, theme.GlyphNerd))
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("bar spans = %q, want %q", got, want)
	}

	var b bytes.Buffer
	drawBar(&b, theme.DefaultTheme(), snap, false, info, theme.GlyphNerd)
	out := b.String()
	if !strings.Contains(out, info) {
		t.Fatalf("bar lost its scroll segment: %q", out)
	}
	if strings.Contains(out, " | ") {
		t.Fatalf("bar still draws the inline string: %q", out)
	}
	if strings.Contains(out, "! ") {
		t.Fatalf("bar still draws a toast suffix: %q", out)
	}
}

// Copying a selection raises one toast, through the same path the mouse drag
// uses.
func TestCopySelectionRaisesToast(t *testing.T) {
	tui, _ := clientTUI(t, map[string]bool{"a": true})
	p := tui.paneByID("a")
	tui.vts["a"] = testutil.TestVT(t, p.Rect.W, p.Rect.H, "hello world")
	testutil.CaptureStdout(t, func() {
		tui.handleMouse(mouseEvent{
			btn: 0, x: p.Rect.X + 1, y: p.Rect.Y + 1,
			raw: []byte("\x1b[<0;1;1M"),
		})
		tui.handleMouse(mouseEvent{
			btn: 32, x: p.Rect.X + 5, y: p.Rect.Y + 1,
			raw: []byte("\x1b[<32;5;1M"),
		})
		tui.handleMouse(mouseEvent{
			btn: 0, release: true, x: p.Rect.X + 5, y: p.Rect.Y + 1,
			raw: []byte("\x1b[<0;5;1m"),
		})
	})
	got := toastTexts(tui)
	if len(got) != 1 || got[0] != "copied 5 chars" {
		t.Fatalf("toasts = %q, want one copied 5 chars", got)
	}
}

// A toast with wide runes or a joined glyph sizes its box in cells, so the
// interior is exactly the text and no glyph is dropped or doubled.
func TestDrawToastsSizesByCells(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"wide runes", "日日"},
		{"joined family", "edit " + testutil.Family + " now"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			snap := &wire.Snapshot{Cols: 40, Rows: 8}
			tui := &tui{}
			tui.notify(c.text)
			var b bytes.Buffer
			drawToasts(&b, theme.DefaultTheme(), snap, tui.toasts)
			out := b.String()
			row, col := toastRow(t, out, c.text)
			if row != snap.Rows-2 {
				t.Fatalf("row = %d, want %d", row, snap.Rows-2)
			}
			// The cursor sits on the left border, so the box is Cols-col+1
			// cells wide: the text's cells plus its two border cells.
			if w, want := snap.Cols-col+1, cells.Cells(c.text)+2; w != want {
				t.Fatalf("box width = %d, want %d for %d cells of text",
					w, want, cells.Cells(c.text))
			}
			// The interior is the text alone: bright text, then the right
			// border, with no padding and no dropped glyph.
			want := "\x1b[97m" + c.text + "\x1b[0m\x1b[90m" + theme.DefaultTheme().Glyphs.BorderVertical
			if !strings.Contains(out, want) {
				t.Fatalf("interior is not the whole text %q: %q", c.text, out)
			}
		})
	}
}

// A toast longer than the cap is cut to the cap with an ellipsis, and the box
// never grows past the cap.
func TestDrawToastsCapsWidth(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 8}
	tui := &tui{}
	tui.notify(strings.Repeat("x", 60))
	var b bytes.Buffer
	drawToasts(&b, theme.DefaultTheme(), snap, tui.toasts)
	out := b.String()
	// The ellipsis is the last cell of the interior, inside the box.
	head := strings.Repeat("x", toastMaxWidth-1) + cells.Ellipsis
	if !strings.Contains(out, head) {
		t.Fatalf("toast is not truncated with an ellipsis at the cap: %q", out)
	}
	if strings.Contains(out, strings.Repeat("x", toastMaxWidth)) {
		t.Fatalf("toast kept more than the cap: %q", out)
	}
	_, col := toastRow(t, out, head)
	if w := snap.Cols - col + 1; w != toastMaxWidth+2 {
		t.Fatalf("box width = %d, want %d", w, toastMaxWidth+2)
	}
}

// A toast under the cap renders whole, and its box is the text plus borders.
func TestDrawToastsKeepsShortToastWhole(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 8}
	tui := &tui{}
	tui.notify("saved 3 files")
	var b bytes.Buffer
	drawToasts(&b, theme.DefaultTheme(), snap, tui.toasts)
	out := b.String()
	if !strings.Contains(out, "saved 3 files") {
		t.Fatalf("short toast was truncated: %q", out)
	}
	if strings.Contains(out, cells.Ellipsis) {
		t.Fatalf("short toast gained an ellipsis: %q", out)
	}
	_, col := toastRow(t, out, "saved 3 files")
	if w := snap.Cols - col + 1; w != cells.Cells("saved 3 files")+2 {
		t.Fatalf("box width = %d, want %d", w, cells.Cells("saved 3 files")+2)
	}
}

// A wide-rune toast is cut on a cluster boundary: every painted rune is whole
// and the interior never passes the cap.
func TestDrawToastsElidesOnClusterBounds(t *testing.T) {
	snap := &wire.Snapshot{Cols: 100, Rows: 8}
	tui := &tui{}
	tui.notify(strings.Repeat("日", 30)) // 60 cells, twice the cap
	var b bytes.Buffer
	drawToasts(&b, theme.DefaultTheme(), snap, tui.toasts)
	out := b.String()
	at := strings.Index(out, "\x1b[97m")
	end := strings.Index(out[at:], "\x1b[0m\x1b[90m")
	if at < 0 || end < 0 {
		t.Fatalf("no toast interior in %q", out)
	}
	got := strings.TrimRight(out[at+len("\x1b[97m"):at+end], " ")
	if n := cells.Cells(got); n > toastMaxWidth {
		t.Fatalf("interior paints %d cells, want at most %d", n, toastMaxWidth)
	}
	if !strings.HasSuffix(got, cells.Ellipsis) {
		t.Fatalf("interior %q does not end with the ellipsis", got)
	}
	for _, r := range got {
		if r != '日' && r != '…' {
			t.Fatalf("interior %q holds a split rune %q", got, string(r))
		}
	}
}

// A toast renders as a closed box: a border on all four sides from the theme
// glyphs, the text row inside, no padding row.
func TestDrawToastsPaintsClosedBox(t *testing.T) {
	snap := &wire.Snapshot{Cols: 40, Rows: 9}
	th := theme.DefaultTheme()
	g := th.Glyphs
	tui := &tui{}
	tui.notify("saved 3 files")
	var b bytes.Buffer
	drawToasts(&b, th, snap, tui.toasts)
	out := b.String()
	w := cells.Cells("saved 3 files") + 2
	edge := strings.Repeat(g.BorderHorizontal, w-2)
	if !strings.Contains(out, g.BorderTopLeft+edge+g.BorderTopRight) {
		t.Fatalf("the box has no closed top border: %q", out)
	}
	if !strings.Contains(out, g.BorderBottomLeft+edge+g.BorderBottomRight) {
		t.Fatalf("the box has no closed bottom border: %q", out)
	}
	want := "\x1b[0m\x1b[90m" + g.BorderVertical + "\x1b[97msaved 3 files\x1b[0m\x1b[90m" + g.BorderVertical + "\x1b[0m"
	if !strings.Contains(out, want) {
		t.Fatalf("the text row is not framed on both sides: %q", out)
	}
	// The three rows are consecutive and share one column: the box is tight.
	rows := map[int]bool{}
	col := -1
	for _, m := range cursorAt.FindAllStringSubmatchIndex(out, -1) {
		r, _ := strconv.Atoi(out[m[2]:m[3]])
		c, _ := strconv.Atoi(out[m[4]:m[5]])
		rows[r] = true
		if col < 0 {
			col = c
		} else if c != col {
			t.Fatalf("box rows do not share column %d: found %d", col, c)
		}
	}
	if len(rows) != 3 {
		t.Fatalf("the box paints %d rows, want the top, the text and the bottom: %v", len(rows), rows)
	}
	_, textCol := toastRow(t, out, "saved 3 files")
	if textCol != col {
		t.Fatalf("text row does not start at the border column %d", col)
	}
}

// The stack separates the boxes by one row, so the boxes read as separate
// popups, not one tall block.
func TestDrawToastsSeparatesBoxes(t *testing.T) {
	snap := &wire.Snapshot{Cols: 40, Rows: 14}
	tui := &tui{}
	tui.notify("older")
	tui.notify("newer")
	var b bytes.Buffer
	drawToasts(&b, theme.DefaultTheme(), snap, tui.toasts)
	out := b.String()
	oldRow, _ := toastRow(t, out, "older")
	newRow, _ := toastRow(t, out, "newer")
	if newRow-oldRow != 4 {
		t.Fatalf("text rows %d and %d, want one box, one gap and one box between", oldRow, newRow)
	}
	// The gap row between the boxes carries no glyph.
	for _, m := range cursorAt.FindAllStringSubmatchIndex(out, -1) {
		r, _ := strconv.Atoi(out[m[2]:m[3]])
		if r != oldRow+2 {
			continue
		}
		t.Fatalf("a cursor paints on the gap row %d", r)
	}
	if strings.Count(out, theme.DefaultTheme().Glyphs.BorderHorizontal) != 2*(cells.Cells("older")+2-2)+2*(cells.Cells("newer")+2-2) {
		t.Fatalf("the horizontal border count does not match two closed boxes: %q", out)
	}
}

// A stack taller than the space above the bar drops the oldest boxes instead
// of painting over the bar.
func TestDrawToastsDropsBoxesThatDoNotFit(t *testing.T) {
	snap := &wire.Snapshot{Cols: 40, Rows: 6} // room for one box above the bar
	tui := &tui{}
	tui.notify("oldest")
	tui.notify("middle")
	tui.notify("newest")
	var b bytes.Buffer
	drawToasts(&b, theme.DefaultTheme(), snap, tui.toasts)
	out := b.String()
	if !strings.Contains(out, "newest") {
		t.Fatalf("the newest box is missing: %q", out)
	}
	for _, gone := range []string{"middle", "oldest"} {
		if strings.Contains(out, gone) {
			t.Fatalf("box %q overflowed the space above the bar: %q", gone, out)
		}
	}
	// The bar row is never painted.
	for _, m := range cursorAt.FindAllStringSubmatchIndex(out, -1) {
		r, _ := strconv.Atoi(out[m[2]:m[3]])
		if r > snap.Rows-1 {
			t.Fatalf("a box painted on the bar row %d", r)
		}
	}
}
