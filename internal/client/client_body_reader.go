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
	"dm/internal/boxview"
	"dm/internal/cells"
	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/panel"
	"dm/internal/theme"
	"dm/internal/ui"
	"dm/internal/vt"
)

// bodyReader is the open reader overlay: the entry it shows, its lines with
// the summary on top and the body below, and the first line in view. The
// lines are wrapped to the float's inner width; scroll moves over wrapped
// rows.
type bodyReader struct {
	e     box.Entry
	raw   []string
	lines []string
	// win is the window over the wrapped lines: the first line in view. The
	// reader selects nothing, so only the offset moves.
	win   ui.Viewport
	Float *panel.FloatingPane
}

// newBodyReader builds the overlay for one entry at the box float's rect, with
// its hint named from km. The summary line comes first and the body renders
// inline below it; a summary the entry does not carry is skipped, and one
// trailing empty line from the body's final newline is dropped.
func newBodyReader(e box.Entry, r layout.Rect, th theme.Theme, km *keymap.Config) *bodyReader {
	raw := make([]string, 0, strings.Count(e.Body, "\n")+2)
	if e.Msg != "" {
		raw = append(raw, e.Msg)
	}
	raw = append(raw, strings.Split(e.Body, "\n")...)
	if n := len(raw); n > 0 && raw[n-1] == "" {
		raw = raw[:n-1]
	}
	b := &bodyReader{
		e:   e,
		raw: raw,
		Float: &panel.FloatingPane{
			ID:     "body",
			Active: true,
			Rect:   r,
			Title:  " " + e.Key + "  @" + strconv.Itoa(e.Seq) + " " + string(e.State) + " ",
			Hint:   readerHint(km),
		},
	}
	b.rewrap()
	b.paint(th)
	return b
}

// rewrap refits the lines to the float's current inner width, the rect minus
// its two border cells. A resize calls it, so a wider float re-joins what a
// narrower one broke apart.
func (r *bodyReader) rewrap() {
	inner := r.Float.Rect.W - 2
	r.lines = make([]string, 0, len(r.raw))
	for _, line := range r.raw {
		r.lines = append(r.lines, wrapLine(line, inner)...)
	}
}

// wrapLine breaks one line into rows of at most width cells. A line that
// fits comes back alone and whole. A longer line breaks at the last space
// that fits, and a run with no space hard-breaks at the width. Cutting walks
// grapheme clusters, so a wide rune counts its cells and never splits.
func wrapLine(s string, width int) []string {
	if width < 1 || cells.Cells(s) <= width {
		return []string{s}
	}
	var rows []string
	for cells.Cells(s) > width {
		cut := len(cells.Clip(s, width))
		if j := strings.LastIndex(s[:cut], " "); j > 0 {
			rows = append(rows, s[:j])
			s = strings.TrimLeft(s[j:], " ")
			continue
		}
		if cut == 0 { // one cluster wider than the whole row
			f, _ := cells.FirstCluster(s)
			cut = len(f)
		}
		rows = append(rows, s[:cut])
		s = s[cut:]
	}
	if s != "" {
		rows = append(rows, s)
	}
	// A wrapped row never ends in spaces: the break consumed them.
	for i := range rows {
		rows[i] = strings.TrimRight(rows[i], " ")
	}
	return rows
}

// paint rewrites the overlay's terminal from the window of rows. Every row
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
		i := r.win.Off + row
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
	r.win.Off += n
	r.fit()
	r.paint(th)
}

// Sync fits the overlay to the float's rect after a resize, rewraps the
// lines to the new width and repaints.
func (r *bodyReader) Sync(rect layout.Rect, th theme.Theme) {
	r.Float.Rect = rect
	r.rewrap()
	r.fit()
	r.paint(th)
}

// fit sizes the window to the wrapped lines and the float and holds it on the
// rows: the offset never passes the last row that still fills the pane.
func (r *bodyReader) fit() {
	r.win.N, r.win.H, r.win.Sel = len(r.lines), r.Float.Rect.H, -1
	r.win.Clamp()
}

// The client side of the reader: the keys that open the overlay on the box
// view, and the hint the overlay carries.

// openBodyReader opens the overlay for the entry the selection sits on, and
// marks that entry read. An entry carries a summary even when it has no body,
// and the overlay wraps it, so a notification still reads. An entry with
// neither opens nothing.
func openBodyReader(t *tui) {
	e, ok := t.box.Selected()
	if !ok || (e.Msg == "" && e.Body == "") {
		return
	}
	if e.Seq > t.notifyRead {
		t.notifyRead = e.Seq
	}
	t.box.MarkRead(e.Seq, t.theme())
	t.reader = newBodyReader(e, boxview.ReaderRect(t.snap, t.box.ListLen()), t.theme(), t.km())
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

// readerHint is the overlay's bottom border: the box keymap's scroll pair and
// close key, with the reader's own word for the close. A rebind moves it.
func readerHint(km *keymap.Config) string {
	key := func(action string) string {
		if km == nil {
			return ""
		}
		return km.Key("box", action)
	}
	return ui.Hints([]ui.Hint{
		{Keys: []string{key("down"), key("up")}, Label: "scroll"},
		{Keys: []string{key("close-esc")}, Label: "back"},
	})
}

// SetKeymap renames the reader's hint from a new config, so a reload that
// rebinds a scroll or close key moves the border of an open reader.
func (r *bodyReader) SetKeymap(km *keymap.Config) {
	r.Float.Hint = readerHint(km)
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
		t.reader.Scroll(-t.reader.win.Off-1, th)
	case "bottom":
		t.reader.Scroll(len(t.reader.lines), th)
	default:
		closeBodyReader(t)
	}
}
