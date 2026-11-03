package main

import (
	"bytes"
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
	snap := &Snapshot{Cols: 40, Rows: 12}
	tui := &tui{}
	tui.notify("older")
	tui.notify("newest")
	var b bytes.Buffer
	drawToasts(&b, snap, tui.toasts)
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
	snap := &Snapshot{Cols: cols, Rows: 6}
	tui := &tui{}
	tui.notify("a toast much longer than the screen")
	var b bytes.Buffer
	drawToasts(&b, snap, tui.toasts)
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
	snap := &Snapshot{
		Session: "main", Workspace: "main", Windows: []string{"1"}, Focus: "w1p1",
		Cols: 60, Rows: 24,
		Panes: []PaneInfo{{ID: "w1p1", Status: "working"}},
	}
	var b bytes.Buffer
	drawBar(&b, snap, false, "SCROLL w1p1 +1/9")
	out := b.String()
	if !strings.Contains(out, "main | main | win 1/1 1") {
		t.Fatalf("bar lost its content: %q", out)
	}
	if !strings.Contains(out, "SCROLL w1p1 +1/9") {
		t.Fatalf("bar lost its scroll segment: %q", out)
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
