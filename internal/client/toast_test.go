package client

import (
	"bytes"
	"dm/internal/cells"
	"dm/internal/rail"
	"dm/internal/theme"
	"dm/internal/wire"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
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

// The stack draws both texts, newest nearest the bar.
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
	if newRow != snap.Rows-1 {
		t.Fatalf("newest row = %d, want %d just above the bar", newRow, snap.Rows-1)
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
	row, col := toastRow(t, out, "a toast mu")
	if row != snap.Rows-1 {
		t.Fatalf("row = %d, want %d", row, snap.Rows-1)
	}
	// Left border, cols-2 inner cells, right border.
	if col != 1 {
		t.Fatalf("col = %d, want the box flush at 1", col)
	}
	if strings.Contains(out, "longer") {
		t.Fatalf("overflowing text was not truncated: %q", out)
	}
}

// The bar keeps its content and no longer carries the toast suffix.
func TestBarDropsToastSuffix(t *testing.T) {
	snap := &wire.Snapshot{
		Session: "main", Workspace: "main", Windows: []string{"1"}, Focus: "w1p1",
		Cols: 60, Rows: 24,
		Panes: []wire.PaneInfo{{ID: "w1p1", Status: "working"}},
	}
	info := "SCROLL w1p1 +1/9"
	glyph, _ := rail.RailGlyph(theme.DefaultTheme(), "working", rail.NerdFonts())
	want := []string{"main", " ", "main", " ", "▸ ", "1 1", " ", glyph,
		" w1p1", " working", info}
	got := barTexts(barSegments(theme.DefaultTheme(), snap, false, info))
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("bar spans = %q, want %q", got, want)
	}

	var b bytes.Buffer
	drawBar(&b, theme.DefaultTheme(), snap, false, info)
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
	tui.vts["a"] = testVT(t, p.Rect.W, p.Rect.H, "hello world")
	captureStdout(t, func() {
		tui.handleMouse(mouseEvent{btn: 0, x: p.Rect.X + 1, y: p.Rect.Y + 1,
			raw: []byte("\x1b[<0;1;1M")})
		tui.handleMouse(mouseEvent{btn: 32, x: p.Rect.X + 5, y: p.Rect.Y + 1,
			raw: []byte("\x1b[<32;5;1M")})
		tui.handleMouse(mouseEvent{btn: 0, release: true, x: p.Rect.X + 5, y: p.Rect.Y + 1,
			raw: []byte("\x1b[<0;5;1m")})
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
		{"joined family", "edit " + family + " now"},
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
			if row != snap.Rows-1 {
				t.Fatalf("row = %d, want %d", row, snap.Rows-1)
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
