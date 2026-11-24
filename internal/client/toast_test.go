package client

import (
	"bytes"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"dm/internal/box"
	"dm/internal/cells"
	"dm/internal/rail"
	"dm/internal/testutil"
	"dm/internal/theme"
	"dm/internal/wire"
)

// toastParts renders the whole announcement a toast carries: its state, its
// topic and its message, so a test reads a notify toast at a glance.
func toastParts(x toast) string {
	return strings.TrimSpace(strings.Join([]string{x.state, x.topic, x.text}, " "))
}

// The default theme's escapes the toast box paints with: the dim border, the
// bright message, and the panel ground every toast sits on.
var (
	dimBorder  = theme.DefaultTheme().Palette.RailDim.FG()
	brightText = theme.DefaultTheme().Palette.RailText.FG()
	panelBG    = theme.DefaultTheme().Palette.RailFocusBG.BG()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	snap := &wire.Snapshot{Cols: 40, Rows: 12}
	tui := &tui{}
	tui.notify("older")
	tui.notify("newest")
	var b bytes.Buffer
	drawToasts(&b, theme.DefaultTheme(), snap, tui.toasts)
	out := b.String()
	oldRow, _ := toastRow(t, out, "older")
	newRow, _ := toastRow(t, out, "newest")
	if oldRow >= newRow {
		t.Fatalf("rows older=%d newest=%d, want newest below", oldRow, newRow)
	}
	if newRow != snap.Rows-2 {
		t.Fatalf("newest row = %d, want %d inside the box just above the bar", newRow, snap.Rows-2)
	}
}

// A text wider than the screen is clamped and truncated, not wrapped.
func TestDrawToastsClampsToScreen(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	cases := []struct {
		name string
		text string
	}{
		{"wide runes", "日日"},
		{"joined family", "edit " + testutil.Family + " now"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			snap := &wire.Snapshot{Cols: 100, Rows: 8}
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
			// cells wide: every toast is the same width.
			if w, want := snap.Cols-col+1, toastWidth+2; w != want {
				t.Fatalf("box width = %d, want the fixed %d", w, want)
			}
			// The interior is the text padded out to the fixed width: no
			// dropped glyph, no doubled one.
			want := panelBG + brightText + cells.Pad(c.text, toastWidth) + "\x1b[0m" + panelBG + dimBorder + theme.DefaultTheme().Glyphs.BorderVertical
			if !strings.Contains(out, want) {
				t.Fatalf("interior is not the whole text %q: %q", c.text, out)
			}
		})
	}
}

// A message wider than the box is cut with an ellipsis, and the box never grows
// past its fixed width.
func TestDrawToastsCapsWidth(t *testing.T) {
	t.Parallel()
	snap := &wire.Snapshot{Cols: 100, Rows: 8}
	tui := &tui{}
	tui.notify(strings.Repeat("x", 60))
	var b bytes.Buffer
	drawToasts(&b, theme.DefaultTheme(), snap, tui.toasts)
	out := b.String()
	// The ellipsis is the last cell of the interior, inside the box.
	head := strings.Repeat("x", toastWidth-1) + cells.Ellipsis
	if !strings.Contains(out, head) {
		t.Fatalf("toast is not truncated with an ellipsis at the cap: %q", out)
	}
	if strings.Contains(out, strings.Repeat("x", toastWidth)) {
		t.Fatalf("toast kept more than the cap: %q", out)
	}
	_, col := toastRow(t, out, head)
	if w := snap.Cols - col + 1; w != toastWidth+2 {
		t.Fatalf("box width = %d, want %d", w, toastWidth+2)
	}
}

// A toast under the cap renders whole, padded out to the fixed width.
func TestDrawToastsKeepsShortToastWhole(t *testing.T) {
	t.Parallel()
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
	if w := snap.Cols - col + 1; w != toastWidth+2 {
		t.Fatalf("box width = %d, want the fixed %d", w, toastWidth+2)
	}
}

// A wide-rune toast is cut on a cluster boundary: every painted rune is whole
// and the interior never passes the cap.
func TestDrawToastsElidesOnClusterBounds(t *testing.T) {
	t.Parallel()
	snap := &wire.Snapshot{Cols: 100, Rows: 8}
	tui := &tui{}
	tui.notify(strings.Repeat("日", 30)) // 60 cells, twice the cap
	var b bytes.Buffer
	drawToasts(&b, theme.DefaultTheme(), snap, tui.toasts)
	out := b.String()
	at := strings.Index(out, brightText)
	end := strings.Index(out[at:], "\x1b[0m"+panelBG+dimBorder)
	if at < 0 || end < 0 {
		t.Fatalf("no toast interior in %q", out)
	}
	got := strings.TrimRight(out[at+len(brightText):at+end], " ")
	if n := cells.Cells(got); n > toastWidth {
		t.Fatalf("interior paints %d cells, want at most %d", n, toastWidth)
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
	t.Parallel()
	snap := &wire.Snapshot{Cols: 100, Rows: 9}
	th := theme.DefaultTheme()
	g := th.Glyphs
	tui := &tui{}
	tui.notify("saved 3 files")
	var b bytes.Buffer
	drawToasts(&b, th, snap, tui.toasts)
	out := b.String()
	edge := strings.Repeat(g.BorderHorizontal, toastWidth)
	if !strings.Contains(out, g.BorderTopLeft+edge+g.BorderTopRight) {
		t.Fatalf("the box has no closed top border: %q", out)
	}
	textRow, _ := toastRow(t, out, "saved 3 files")
	if got, want := rowGlyphs(t, out, textRow+1), g.BorderBottomLeft+edge+g.BorderBottomRight; got != want {
		t.Fatalf("the bottom border is %q, want %q", got, want)
	}
	want := "\x1b[0m" + panelBG + dimBorder + g.BorderVertical + "\x1b[0m" + panelBG + brightText +
		cells.Pad("saved 3 files", toastWidth) + "\x1b[0m" + panelBG + dimBorder + g.BorderVertical + "\x1b[0m"
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
	t.Parallel()
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
		t.Fatalf("text rows %d and %d, want a bottom, a gap and a top between", oldRow, newRow)
	}
	// The gap row between the boxes carries no glyph.
	for _, m := range cursorAt.FindAllStringSubmatchIndex(out, -1) {
		r, _ := strconv.Atoi(out[m[2]:m[3]])
		if r != oldRow+2 {
			continue
		}
		t.Fatalf("a cursor paints on the gap row %d", r)
	}
	g := theme.DefaultTheme().Glyphs
	// Each box draws its top border and its fresh gauge across the interior,
	// clamped to the screen the two of them share.
	want := 4 * (snap.Cols - 2)
	if got := strings.Count(out, g.BorderHorizontal); got != want {
		t.Fatalf("horizontal glyphs = %d, want %d for two boxes and their gauges: %q", got, want, out)
	}
	if got := strings.Count(out, g.BorderTopLeft); got != 2 {
		t.Fatalf("boxes opened = %d, want two closed boxes: %q", got, out)
	}
}

// A stack taller than the space above the bar drops the oldest boxes instead
// of painting over the bar.
func TestDrawToastsDropsBoxesThatDoNotFit(t *testing.T) {
	t.Parallel()
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

// drawRows lists the distinct screen rows a drawing placed a cursor on, in
// first-seen order, so a test can count a box's rows.
func drawRows(t *testing.T, out string) []int {
	t.Helper()
	seen := map[int]bool{}
	rows := []int{}
	for _, m := range cursorAt.FindAllStringSubmatchIndex(out, -1) {
		r, _ := strconv.Atoi(out[m[2]:m[3]])
		if !seen[r] {
			seen[r] = true
			rows = append(rows, r)
		}
	}
	return rows
}

// A notify toast names its state in the top border: the theme's state mark and
// the state word, in the state's colour, inside the corners.
func TestDrawToastsNamesStateInBorder(t *testing.T) {
	t.Parallel()
	th := theme.DefaultTheme()
	g := th.Glyphs
	done := th.States["done"]
	snap := &wire.Snapshot{Cols: 40, Rows: 8}
	tui := &tui{}
	tui.notifyEntry(box.Entry{Key: "notify/demo", State: box.StateDone, Msg: "gate green"})
	var b bytes.Buffer
	drawToasts(&b, th, snap, tui.toasts)
	out := b.String()
	head := g.BorderTopLeft + " " + done.Glyph + " done "
	if !strings.Contains(out, head) {
		t.Fatalf("the top border does not name the state %q: %q", head, out)
	}
	if !strings.Contains(out, done.Colour.FG()+head) {
		t.Fatalf("the state name is not in the state's colour: %q", out)
	}
	if strings.Contains(out, "done:") {
		t.Fatalf("the state name carries a colon: %q", out)
	}
}

// An entry with no message still takes the one interior row, empty: the box
// keeps the size every other toast has, so the stack never moves.
func TestDrawToastsEmptyMessageKeepsTheRow(t *testing.T) {
	t.Parallel()
	th := theme.DefaultTheme()
	snap := &wire.Snapshot{Cols: 40, Rows: 10}
	tui := &tui{}
	tui.notifyEntry(box.Entry{Key: "notify/demo", State: box.StateDone})
	var b bytes.Buffer
	drawToasts(&b, th, snap, tui.toasts)
	out := b.String()
	if want := th.States["done"].Glyph + " done notify/demo"; !strings.Contains(out, want) {
		t.Fatalf("the top border does not name %q: %q", want, out)
	}
	if strings.Contains(out, ":") {
		t.Fatalf("an empty message left punctuation behind: %q", out)
	}
	// The box is three rows: the border, the empty message row and the gauge.
	if rows := drawRows(t, out); len(rows) != 3 {
		t.Fatalf("the box paints %d rows, want the border, the row and the gauge: %v", len(rows), rows)
	}
	// The bottom border sits on the newest box's row, just above the bar.
	if want := snap.Rows - 1; !strings.Contains(out, "\x1b["+strconv.Itoa(want)+";") {
		t.Fatalf("the box does not end on row %d: %q", want, out)
	}
}

// A notify toast names the state and the topic in the top border, in the state's
// colour, and holds the message bright inside the box on the float ground.
func TestDrawToastsNamesTopicInBorder(t *testing.T) {
	t.Parallel()
	th := theme.DefaultTheme()
	g := th.Glyphs
	snap := &wire.Snapshot{Cols: 40, Rows: 10}
	tui := &tui{}
	tui.notifyEntry(box.Entry{Key: "notify/demo", State: box.StateBlocked, Msg: "waiting on tests"})
	var b bytes.Buffer
	drawToasts(&b, th, snap, tui.toasts)
	out := b.String()
	border := th.States["blocked"].Colour.FG()
	title := " " + th.States["blocked"].Glyph + " blocked notify/demo "
	if !strings.Contains(out, border+g.BorderTopLeft+title) {
		t.Fatalf("the top border does not name the state and topic in the border's colour: %q", out)
	}
	msgRow := "\x1b[0m" + panelBG + border + g.BorderVertical + "\x1b[0m" + panelBG + brightText + "waiting on tests"
	if !strings.Contains(out, msgRow) {
		t.Fatalf("the message does not sit bright inside the border: %q", out)
	}
	// Three rows: the border, the message, the gauge.
	if rows := drawRows(t, out); len(rows) != 3 {
		t.Fatalf("the box paints %d rows, want the border, the message and the gauge: %v", len(rows), rows)
	}
}

// A plain toast keeps the bare border it had: no state word, the text alone.
func TestDrawToastsPlainHasNoTitle(t *testing.T) {
	t.Parallel()
	th := theme.DefaultTheme()
	g := th.Glyphs
	snap := &wire.Snapshot{Cols: 40, Rows: 8}
	tui := &tui{}
	tui.notify("copied 5 chars")
	var b bytes.Buffer
	drawToasts(&b, th, snap, tui.toasts)
	out := b.String()
	edge := strings.Repeat(g.BorderHorizontal, snap.Cols-2)
	if !strings.Contains(out, g.BorderTopLeft+edge+g.BorderTopRight) {
		t.Fatalf("a plain toast grew a title bar: %q", out)
	}
}

// A box on a screen narrower than its state name keeps a closed border and
// stays inside the screen: the title elides rather than pushing the corner off
// the right edge.
func TestDrawToastsNarrowScreenClosesBox(t *testing.T) {
	t.Parallel()
	th := theme.DefaultTheme()
	g := th.Glyphs
	snap := &wire.Snapshot{Cols: 8, Rows: 8}
	tui := &tui{}
	tui.notifyEntry(box.Entry{Key: "notify/demo", State: box.StateDone, Msg: "gate green"})
	var b bytes.Buffer
	drawToasts(&b, th, snap, tui.toasts)
	out := b.String()
	title := th.States["done"].Glyph + " done notify/demo"
	want := g.BorderTopLeft + " " + cells.Elide(title, snap.Cols-4) + " " + g.BorderTopRight
	if !strings.Contains(out, want) {
		t.Fatalf("the top border is not closed inside the screen %q: %q", want, out)
	}
	// The box fills the screen: its left border sits in column 1.
	if head := ";1H\x1b[0m" + panelBG + th.States["done"].Colour.FG() + g.BorderTopLeft; !strings.Contains(out, head) {
		t.Fatalf("the box does not start in column 1: %q", out)
	}
	if got, want := rowGlyphs(t, out, snap.Rows-1), g.BorderBottomLeft+strings.Repeat(g.BorderHorizontal, snap.Cols-2)+g.BorderBottomRight; got != want {
		t.Fatalf("the bottom border is %q, want %q", got, want)
	}
}

// sgr matches one SGR escape, so a test can read a row's glyphs alone.
var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

// rowGlyphs returns the plain glyphs one drawn row carries, found by the cursor
// position that places it. Every row of a toast is placed by its own cursor.
func rowGlyphs(t *testing.T, out string, row int) string {
	t.Helper()
	at := -1
	for _, m := range cursorAt.FindAllStringSubmatchIndex(out, -1) {
		r, _ := strconv.Atoi(out[m[2]:m[3]])
		if r == row {
			at = m[1]
			break
		}
	}
	if at < 0 {
		t.Fatalf("no cursor places row %d in %q", row, out)
	}
	rest := out[at:]
	if next := cursorAt.FindStringIndex(rest); next != nil {
		rest = rest[:next[0]]
	}
	return sgr.ReplaceAllString(rest, "")
}

// gaugeRuns finds the newest toast's bottom border and returns its screen row,
// the cells still painted in the gauge colour and the cells left in the border's.
func gaugeRuns(t *testing.T, out, gauge, border, track string) (row, lit, dim int) {
	t.Helper()
	g := theme.DefaultTheme().Glyphs
	corner := "\x1b[0m" + panelBG + border + g.BorderBottomLeft
	litHead := "\x1b[0m" + panelBG + gauge
	dimHead := "\x1b[0m" + panelBG + track
	for _, m := range cursorAt.FindAllStringSubmatchIndex(out, -1) {
		rest := out[m[1]:]
		if !strings.HasPrefix(rest, corner) {
			continue
		}
		row, _ = strconv.Atoi(out[m[2]:m[3]])
		rest = rest[len(corner):]
		if gauge == track {
			// One run covers the whole track: the toast has no state colour.
			if !strings.HasPrefix(rest, litHead) {
				t.Fatalf("the gauge does not open with its colour: %q", rest)
			}
			rest = rest[len(litHead):]
			stop := strings.Index(rest, "\x1b[0m")
			if stop < 0 {
				t.Fatalf("the gauge track has no terminator: %q", rest)
			}
			return row, cells.Cells(strings.TrimSuffix(rest[:stop], g.BorderBottomRight)), 0
		}
		if !strings.HasPrefix(rest, litHead) {
			t.Fatalf("the gauge does not open with its colour: %q", rest)
		}
		rest = rest[len(litHead):]
		stop := strings.Index(rest, "\x1b[0m")
		if stop < 0 {
			t.Fatalf("the gauge has no end: %q", rest)
		}
		lit = cells.Cells(rest[:stop])
		rest = rest[stop:]
		if n := len(dimHead); strings.HasPrefix(rest, dimHead) {
			rest = rest[n:]
			stop := strings.Index(rest, "\x1b[0m")
			if stop < 0 {
				t.Fatalf("the gauge track has no terminator: %q", rest)
			}
			dim = cells.Cells(rest[:stop])
		}
		return row, lit, dim
	}
	t.Fatalf("no gauge in %q", out)
	return 0, 0, 0
}

// A fresh toast colours its whole gauge, and the gauge is the bottom border: the
// track spans the interior, so the fraction is readable at any width.
func TestDrawToastsGaugeStartsFull(t *testing.T) {
	t.Parallel()
	th := theme.DefaultTheme()
	snap := &wire.Snapshot{Cols: 40, Rows: 9}
	tui := &tui{}
	tui.notifyEntry(box.Entry{Key: "notify/demo", State: box.StateDone, Msg: "gate green"})
	var b bytes.Buffer
	drawToasts(&b, th, snap, tui.toasts)
	out := b.String()
	boxWidth := newToastBox(th, tui.toasts[0], snap.Cols).width
	row, lit, dim := gaugeRuns(t, out, th.States["done"].Colour.FG(), th.States["done"].Colour.FG(), dimBorder)
	if lit != boxWidth || dim != 0 {
		t.Fatalf("gauge = %d lit, %d dim, want the whole track of %d lit", lit, dim, boxWidth)
	}
	// The gauge is the row under the message, and the newest box ends on the row
	// just above the bar.
	textRow, _ := toastRow(t, out, "gate green")
	if row != textRow+1 {
		t.Fatalf("gauge row = %d, want the row under the message %d", row, textRow+1)
	}
	if row != snap.Rows-1 {
		t.Fatalf("the newest gauge row = %d, want %d", row, snap.Rows-1)
	}
}

// A toast half through its life drains half its gauge, from the right: the left
// cells keep the state's colour and the rest falls back to the border's.
func TestDrawToastsGaugeDrainsFromTheRight(t *testing.T) {
	t.Parallel()
	th := theme.DefaultTheme()
	snap := &wire.Snapshot{Cols: 40, Rows: 9}
	full := newToastBox(th, toast{text: "saved 3 files", left: 1}, snap.Cols)
	half := newToastBox(th, toast{text: "saved 3 files", left: 0.5}, snap.Cols)
	if got, want := full.filled(), full.width; got != want {
		t.Fatalf("a fresh gauge colours %d cells, want %d", got, want)
	}
	if got, want := half.filled(), int(math.Round(0.5*float64(half.width))); got != want {
		t.Fatalf("a half-drained gauge colours %d cells, want %d", got, want)
	}
	// One cell of colour at the very end of the life, so the gauge never blinks
	// out before the box does.
	if got := (toastBox{width: 20, left: 0.001}).filled(); got != 1 {
		t.Fatalf("a nearly spent gauge colours %d cells, want 1", got)
	}
	if got := (toastBox{width: 20, left: 0}).filled(); got != 0 {
		t.Fatalf("a spent gauge colours %d cells, want none", got)
	}
	// A toast with a state has two gauge colours, so the drawn split is
	// readable: the life left, then the spent track.
	var b bytes.Buffer
	drawToasts(&b, th, snap, []toast{{text: "gate green", state: "done", left: 0.5}})
	drawn := newToastBox(th, toast{text: "gate green", state: "done", left: 0.5}, snap.Cols)
	_, lit, dim := gaugeRuns(t, b.String(), th.States["done"].Colour.FG(), th.States["done"].Colour.FG(), dimBorder)
	if lit != drawn.filled() {
		t.Fatalf("the drawn gauge colours %d cells, want %d", lit, drawn.filled())
	}
	if lit+dim != drawn.width {
		t.Fatalf("gauge = %d lit, %d dim, want the whole track of %d", lit, dim, drawn.width)
	}
}

// Every toast sits on the float ground, whatever its state: a stack of three is
// three quiet panels, and the state's colour stays in the border and the gauge.
func TestDrawToastsSitOnOneGround(t *testing.T) {
	t.Parallel()
	th := theme.DefaultTheme()
	snap := &wire.Snapshot{Cols: 40, Rows: 12}
	tui := &tui{}
	tui.notify("copied 5 chars")
	tui.notifyEntry(box.Entry{Key: "notify/demo", State: box.StateBlocked, Msg: "waiting on tests"})
	tui.notifyEntry(box.Entry{Key: "notify/demo", State: box.StateDone, Msg: "gate green"})
	var b bytes.Buffer
	drawToasts(&b, th, snap, tui.toasts)
	out := b.String()
	// Every row of every box opens with the panel ground, and no state colour
	// reaches a background: the tint is gone.
	for _, m := range cursorAt.FindAllStringSubmatchIndex(out, -1) {
		if !strings.HasPrefix(out[m[1]:], "\x1b[0m"+panelBG) {
			t.Fatalf("a box row does not sit on the panel ground: %q", out[m[1]:])
		}
	}
	for state := range th.States {
		if bg := th.States[state].Colour.BG(); strings.Contains(out, bg) {
			t.Fatalf("the %s toast paints a tinted body: %q", state, out)
		}
	}
}

// The expiry clock advances the drain fraction every drip and marks the frame
// dirty only when a drip lands, so a tick inside the same step leaves the
// picture alone.
func TestExpireToastsDrainsBandByDrip(t *testing.T) {
	t.Parallel()
	tui := &tui{}
	tui.notify("saved 3 files")
	raised := tui.toasts[0].at
	tui.dirty = false
	tui.expireToasts(raised.Add(toastLifetime / 2))
	if got := tui.toasts[0].left; math.Abs(got-0.5) > 0.01 {
		t.Fatalf("band left = %.3f halfway through the life, want 0.5", got)
	}
	if !tui.dirty {
		t.Fatal("a drip that moved the band marked nothing dirty")
	}
	// A tick inside the same drip changes nothing, so it draws nothing.
	tui.dirty = false
	tui.expireToasts(raised.Add(toastLifetime/2 + toastDrip/2))
	if tui.dirty {
		t.Fatal("a tick inside one drip marked the frame dirty")
	}
	// The next drip moves the band again.
	tui.expireToasts(raised.Add(toastLifetime/2 + toastDrip))
	if !tui.dirty {
		t.Fatal("the next drip marked nothing dirty")
	}
}
