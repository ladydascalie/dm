package ui

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"dm/internal/box"
	"dm/internal/cells"
	"dm/internal/layout"
	"dm/internal/theme"
)

// MessageList is the list behind the message view: the entries, the topic
// filter, the selection window, the read mark and the row painter. It composes
// Viewport for the selection and owns no float, no border and no key byte: the
// caller sets the window height and paints the chrome around the rows.
//
// The value holds only what the reader did. The filtered rows, the unread
// marks and the ages are derived on every call, so a resize or a filter change
// never leaves a stale copy behind.
type MessageList struct {
	Entries []box.Entry // Entries is the log, newest first, as the box serves it.
	Filter  string      // Filter is a topic prefix; empty shows every entry.
	View    Viewport    // View is the selection and the window over the filtered rows.
	Read    int         // Read is the highest seq the reader has seen; an entry is unread when its seq is above it.
}

// MessageListProps is one frame of the list: the rect it paints into, the
// clock, the row colours, the focus flag and the receipt count per entry. The
// caller builds it, Draw reads it, and nothing in it survives the frame.
type MessageListProps struct {
	Rect     layout.Rect // Rect is the rows the list owns; W clips a row and H is the window height.
	Now      time.Time   // Now is the clock the age column reads; zero means time.Now().
	Dim      theme.RGB   // Dim is an unselected row, and every row while the list is not Active.
	Text     theme.RGB   // Text is the selected row while the list is Active.
	Active   bool        // Active says whether the selected row keeps its full intensity.
	Receipts map[int]int // Receipts is the receipt count per entry seq; a missing seq counts zero.
}

// The columns of one row, in order: the unread mark, the receipt count, the
// seq, the state, the key, the from pane, the age and the msg. The fixed
// widths let a column read down the list.
const (
	messageReceiptW = 3 // the two-cell count and the space after it
	messageSeqW     = 4
	messageStateW   = 7
	messageKeyW     = 14
	messageFromW    = 6
	messageAgeW     = 4
	// messageReceiptMinW is the narrowest rect that carries the receipt
	// column. Below it the column is dropped whole, so the msg keeps its
	// cells.
	messageReceiptMinW = 60
)

// Set replaces the entries and keeps the selection on the same seq when the
// filter still shows it. A selection whose seq is gone keeps its row, clamped,
// and an empty list keeps no selection. A list that had none selects the
// newest row once the filter shows one.
func (l *MessageList) Set(entries []box.Entry) {
	seq, ok := l.selectedSeq()
	l.Entries = entries
	l.reselect(seq, ok)
}

// SetFilter narrows the list to a topic prefix; an empty prefix shows every
// entry. The selection stays on the same seq when the new filter shows it, and
// keeps its row, clamped, when it does not. A filter that widens an empty view
// selects its newest row.
func (l *MessageList) SetFilter(prefix string) {
	if prefix == l.Filter {
		return
	}
	seq, ok := l.selectedSeq()
	l.Filter = prefix
	l.reselect(seq, ok)
}

// ToggleFilter swaps between the whole log and the notify topic. A list that
// already shows a narrower filter opens to every entry.
func (l *MessageList) ToggleFilter() {
	if l.Filter == "" {
		l.SetFilter(box.NotifyPrefix)
		return
	}
	l.SetFilter("")
}

// Move steps the selection n rows, positive toward older entries, and stops at
// either end of the filtered list. A step of zero leaves the selection alone.
func (l *MessageList) Move(n int) {
	l.fit()
	l.View.Move(n)
}

// Top selects the newest visible entry, which also scrolls the window to the
// top. An empty list selects nothing.
func (l *MessageList) Top() {
	l.fit()
	l.View.Top()
}

// Bottom selects the oldest visible entry, which also scrolls the window to the
// end. An empty list selects nothing.
func (l *MessageList) Bottom() {
	l.fit()
	l.View.Bottom()
}

// Selected is the entry the selection sits on. An empty list, or a selection
// the view no longer shows, reports false.
func (l *MessageList) Selected() (box.Entry, bool) {
	rows := l.rows()
	if l.View.Sel < 0 || l.View.Sel >= len(rows) {
		return box.Entry{}, false
	}
	return rows[l.View.Sel], true
}

// Len is the number of rows the filter shows.
func (l *MessageList) Len() int {
	return len(l.rows())
}

// Unread counts the filtered entries past the read mark.
func (l *MessageList) Unread() int {
	n := 0
	for _, e := range l.rows() {
		if e.Seq > l.Read {
			n++
		}
	}
	return n
}

// MarkRead raises the read mark to seq. A seq at or below the mark changes
// nothing, so the mark never walks back over an entry that was read.
func (l *MessageList) MarkRead(seq int) {
	if seq > l.Read {
		l.Read = seq
	}
}

// MarkReadAll marks every entry the filter shows read.
func (l *MessageList) MarkReadAll() {
	for _, e := range l.rows() {
		if e.Seq > l.Read {
			l.Read = e.Seq
		}
	}
}

// Draw paints one row per visible entry into buf, each at its own absolute
// cursor address starting on Rect's first row and column, and reads nothing
// back. The value is passed by value, so a Draw cannot mutate it. An empty
// list paints nothing: the caller's chrome carries its own note.
func (l MessageList) Draw(buf *bytes.Buffer, th theme.Theme, p MessageListProps) {
	r := p.Rect
	if r.W <= 0 || r.H <= 0 {
		return
	}
	rows := l.rows()
	if len(rows) == 0 {
		return
	}
	now := p.Now
	if now.IsZero() {
		now = time.Now()
	}
	receiptW := r.W >= messageReceiptMinW
	for row := 0; row < r.H; row++ {
		i := l.View.Off + row
		if i >= len(rows) {
			break
		}
		e := rows[i]
		receipt := -1
		if receiptW {
			receipt = p.Receipts[e.Seq]
		}
		fmt.Fprintf(buf, "\x1b[%d;%dH", r.Y+row+1, r.X+1)
		base := p.Dim
		if i == l.View.Sel && p.Active {
			base = p.Text
		}
		paintMessageRow(buf, th, e, r.W, receipt, e.Seq > l.Read, base, messageAge(e.At, now))
	}
}

// paintMessageRow writes one entry row as runs: the plain text in base, the
// state word in the colour the rail gives that state. A state column the clip
// cut in half keeps the base colour, so a clip never paints a half-coloured
// word.
func paintMessageRow(buf *bytes.Buffer, th theme.Theme, e box.Entry, w, receipt int, unread bool, base theme.RGB, age string) {
	row, start := messageRowText(e, w, receipt, unread, age)
	state := string(e.State)
	buf.WriteString(theme.Style{Fg: styleColor(base)}.SGR())
	if state == "" || start+len(state) > cells.Cells(row) {
		buf.WriteString(row)
		return
	}
	buf.WriteString(row[:start])
	buf.WriteString(theme.Style{Fg: styleColor(messageStateColour(th, e.State))}.SGR())
	buf.WriteString(row[start : start+len(state)])
	buf.WriteString(theme.Style{Fg: styleColor(base)}.SGR())
	buf.WriteString(row[start+len(state):])
}

// messageRowText is the plain text of one entry row, columns padded and the
// whole clipped to w cells. It also returns the cell offset of the state
// column: a seq past its four-cell column prints whole and shifts the later
// columns right, so the painter derives the highlight from the rendered
// prefix. A negative receipt drops the receipt column.
func messageRowText(e box.Entry, w, receipt int, unread bool, age string) (string, int) {
	mark := " "
	if unread {
		mark = "*"
	}
	count := ""
	if receipt >= 0 {
		if receipt > 99 {
			receipt = 99
		}
		count = fmt.Sprintf("%2d ", receipt)
	}
	seq := fmt.Sprintf("%*d", messageSeqW, e.Seq)
	key := cells.Pad(cells.Clip(e.Key, messageKeyW), messageKeyW)
	from := e.From
	if from == "" {
		from = "-"
	}
	from = cells.Pad(cells.Clip(from, messageFromW), messageFromW)
	prefix := " " + mark + count + seq + " "
	start := cells.Cells(prefix)
	return cells.Clip(prefix+cells.Pad(string(e.State), messageStateW)+" "+key+" "+from+
		" "+messageAgeCell(age)+"  "+e.Msg, w), start
}

// messageAgeCell right-aligns an age in its column, clipped to that width.
func messageAgeCell(age string) string {
	age = cells.Clip(age, messageAgeW)
	return strings.Repeat(" ", messageAgeW-cells.Cells(age)) + age
}

// messageAge is the entry's age at now, short: 42s, 7m, 3h, 2d. A zero time
// prints a dash, and a future time reads 0s.
func messageAge(at, now time.Time) string {
	if at.IsZero() {
		return "-"
	}
	d := now.Sub(at)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// messageStateColour is the state's hue. It is the rail's status colour, so a
// lane reads the same in the list as on the rail. note carries no progress
// meaning, so it is dim text.
func messageStateColour(th theme.Theme, st box.State) theme.RGB {
	if st == box.StateNote {
		return th.Palette.RailDim
	}
	if s, ok := th.States[string(st)]; ok {
		return s.Colour
	}
	if s, ok := th.States["unknown"]; ok {
		return s.Colour
	}
	return theme.DefaultTheme().States["unknown"].Colour
}

// rows is the entries the filter admits, newest first. An empty filter admits
// every entry without a copy.
func (l MessageList) rows() []box.Entry {
	if l.Filter == "" {
		return l.Entries
	}
	out := make([]box.Entry, 0, len(l.Entries))
	for _, e := range l.Entries {
		if strings.HasPrefix(e.Key, l.Filter) {
			out = append(out, e)
		}
	}
	return out
}

// selectedSeq is the seq of the selected entry, when one is selected.
func (l *MessageList) selectedSeq() (int, bool) {
	e, ok := l.Selected()
	if !ok {
		return 0, false
	}
	return e.Seq, true
}

// reselect fits the window to the filtered rows and puts the selection back on
// seq when the filter still shows it, or on the row it had, clamped. A list
// that showed nothing selects its newest row once the filter shows one.
func (l *MessageList) reselect(seq int, ok bool) {
	l.View.N = len(l.rows())
	switch {
	case ok:
		if i := l.Index(seq); i >= 0 {
			l.View.Sel = i
		}
	case l.View.N > 0:
		l.View.Sel = 0
	}
	l.View.Ensure()
}

// fit sets the row count from the filter and holds the selection and the
// window inside it. The window height is the caller's: it comes from the rect
// the caller paints into.
func (l *MessageList) fit() {
	l.View.N = len(l.rows())
	l.View.Ensure()
}

// Index is the filtered row of the entry with seq, or -1 when the filter hides
// it. A caller that holds a filtered index, View.Off for one, reads its seq
// with SeqAt and finds the row back here after the entries change.
func (l MessageList) Index(seq int) int {
	for i, e := range l.rows() {
		if e.Seq == seq {
			return i
		}
	}
	return -1
}

// SeqAt is the seq of the filtered row i, or 0 when i is outside the rows the
// filter shows. Entry seqs start at 1, so 0 is never a real row.
func (l MessageList) SeqAt(i int) int {
	rows := l.rows()
	if i < 0 || i >= len(rows) {
		return 0
	}
	return rows[i].Seq
}
