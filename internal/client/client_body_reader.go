package client

// The body reader: the scrolling overlay the box list page opens for one
// entry's body. The overlay is client-owned, so it rides on top of the view
// the boxview package paints without touching it.

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"dm/internal/box"
	"dm/internal/cells"
	"dm/internal/layout"
	"dm/internal/panel"
	"dm/internal/theme"
	"dm/internal/vt"
)

// bodyReader is the open reader overlay: the entry it shows, its lines with
// the summary on top and the body below, and the first line in view. The text
// is plain, unwrapped, and clipped to the float's width.
type bodyReader struct {
	e     box.Entry
	lines []string
	off   int
	Float *panel.FloatingPane
}

// newBodyReader builds the overlay for one entry at the box float's rect. The
// summary line comes first and the body renders inline below it; a summary
// the entry does not carry is skipped, and one trailing empty line from the
// body's final newline is dropped.
func newBodyReader(e box.Entry, r layout.Rect, th theme.Theme) *bodyReader {
	lines := make([]string, 0, strings.Count(e.Body, "\n")+2)
	if e.Msg != "" {
		lines = append(lines, e.Msg)
	}
	lines = append(lines, strings.Split(e.Body, "\n")...)
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	b := &bodyReader{
		e:     e,
		lines: lines,
		Float: &panel.FloatingPane{
			ID:     "body",
			Active: true,
			Rect:   r,
			Title:  " " + e.Key + "  @" + strconv.Itoa(e.Seq) + " " + string(e.State) + " ",
			Hint:   " j/k scroll  esc back ",
		},
	}
	b.paint(th)
	return b
}

// paint rewrites the overlay's terminal from the window of lines. Every row
// is placed absolutely, so a scrolled reader paints its window whole. The
// text sits in the theme's dim colour, the way the box paints its rows.
func (r *bodyReader) paint(th theme.Theme) {
	rect := r.Float.Rect
	if rect.W < 1 || rect.H < 1 {
		r.Float.VT = vt.NewTerm(1, 1)
		return
	}
	term := vt.NewTerm(rect.W, rect.H)
	var buf bytes.Buffer
	for row := 0; row < rect.H; row++ {
		i := r.off + row
		if i >= len(r.lines) {
			break
		}
		fmt.Fprintf(&buf, "\x1b[%d;1H\x1b[0m%s %s", row+1, th.Palette.RailDim.FG(), cells.Clip(r.lines[i], rect.W-2))
	}
	term.Write(buf.Bytes())
	term.Flush()
	r.Float.VT = term
}

// Scroll moves the window by n rows, positive toward later lines.
func (r *bodyReader) Scroll(n int, th theme.Theme) {
	r.off += n
	r.clamp()
	r.paint(th)
}

// Sync fits the overlay to the float's rect after a resize and repaints.
func (r *bodyReader) Sync(rect layout.Rect, th theme.Theme) {
	r.Float.Rect = rect
	r.clamp()
	r.paint(th)
}

// clamp keeps the window on the lines: the offset never passes the last row
// that still fills the pane.
func (r *bodyReader) clamp() {
	rect := r.Float.Rect
	max := len(r.lines) - rect.H
	if max < 0 {
		max = 0
	}
	if r.off > max {
		r.off = max
	}
	if r.off < 0 {
		r.off = 0
	}
}

// The client side of the reader: the window mirror the list keeps, the key
// that opens the overlay, and the affordance the hint carries.

// openBodyReader opens the overlay for the entry at the top of the list
// window. An entry without a body opens nothing, which is why the hint only
// offers the key when the entry carries one.
func openBodyReader(t *tui) {
	e, ok := t.boxTopEntry()
	if !ok || e.Body == "" {
		return
	}
	t.reader = newBodyReader(e, t.box.Float.Rect, t.theme())
	t.AddFloatingPane(t.reader.Float)
}

// closeBodyReader takes the overlay down and leaves the list page where it
// was.
func closeBodyReader(t *tui) {
	if t.reader == nil {
		return
	}
	t.RemoveFloatingPane(t.reader.Float.ID)
	t.reader = nil
}

// boxTopEntry is the entry the list page's window shows at its top row: the
// one the reader opens on. The watcher page has no entry.
func (t *tui) boxTopEntry() (box.Entry, bool) {
	if t.box == nil || t.boxPage || t.boxTop < 0 || t.boxTop >= len(t.boxLog) {
		return box.Entry{}, false
	}
	return t.boxLog[t.boxTop], true
}

// boxScroll moves the list window and mirrors the move, so the mirror's top
// row is the view's top row.
func (t *tui) boxScroll(n int) {
	t.box.Scroll(n, t.theme())
	if t.boxPage {
		return
	}
	t.boxTop += n
	t.boxClampTop()
}

// boxClampTop keeps the mirror's top row where the view's clamp would hold
// it: inside the log, never past the oldest row that still fills the pane.
func (t *tui) boxClampTop() {
	max := len(t.boxLog) - t.box.Float.Rect.H
	if max < 0 {
		max = 0
	}
	if t.boxTop > max {
		t.boxTop = max
	}
	if t.boxTop < 0 {
		t.boxTop = 0
	}
}

// boxMirrorSet replaces the mirrored log the way the view's Set keeps a
// scrolled-back window: the top entry stays by seq, and a window at the top
// follows the newest entry.
func (t *tui) boxMirrorSet(entries []box.Entry) {
	top := 0
	if t.boxTop > 0 && t.boxTop < len(t.boxLog) {
		top = t.boxLog[t.boxTop].Seq
	}
	t.boxLog = entries
	if top != 0 {
		for i, e := range entries {
			if e.Seq == top {
				t.boxTop = i
				break
			}
		}
	}
	t.boxClampTop()
}

// boxBodyHint amends the hint the view painted with the reader key when the
// entry at the top of the window carries a body. An entry without a body
// leaves the hint as the view wrote it, so the affordance names nothing.
func (t *tui) boxBodyHint() {
	if t.box == nil {
		return
	}
	h := strings.Replace(t.box.Float.Hint, "  v body", "", 1)
	if !t.boxPage {
		if _, ok := t.boxTopEntry(); ok && t.boxLog[t.boxTop].Body != "" {
			h += "  v body"
		}
	}
	t.box.Float.Hint = h
}

// readerAction runs one box-keymap action on the reader, whichever float
// opened it: the scroll keys scroll, and the close keys hand the keyboard
// back to the float beneath.
func (t *tui) readerAction(action string) {
	th := t.theme()
	switch action {
	case "up", "up-arrow":
		t.reader.Scroll(-1, th)
	case "down", "down-arrow":
		t.reader.Scroll(1, th)
	case "page-up":
		t.reader.Scroll(-t.reader.Float.Rect.H, th)
	case "page-down":
		t.reader.Scroll(t.reader.Float.Rect.H, th)
	case "top":
		t.reader.Scroll(-t.reader.off-1, th)
	case "bottom":
		t.reader.Scroll(len(t.reader.lines), th)
	default:
		closeBodyReader(t)
	}
}
