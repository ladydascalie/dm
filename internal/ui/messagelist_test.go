package ui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"dm/internal/box"
	"dm/internal/layout"
	"dm/internal/theme"
	"dm/internal/vt"
)

// entry is one box entry, with a seq, an age and every column filled. The
// clock the tests read it at is entryNow, so an age is exact.
func entry(seq int, state box.State, key, from, msg string, age time.Duration) box.Entry {
	return box.Entry{
		Seq:   seq,
		At:    entryNow.Add(-age),
		From:  from,
		Key:   key,
		State: state,
		Msg:   msg,
	}
}

// entryNow is the clock every test reads ages from.
var entryNow = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

// noteEntry is the plain entry the selection tests use: a notify topic, a
// known state and a msg that names its seq.
func noteEntry(seq int) box.Entry {
	return entry(seq, box.StateNote, "lane/w1", "w1p1", "message "+itoa(seq), 42*time.Second)
}

// itoa is a local strconv.Itoa: the package already avoids the dependency in
// the test helpers that read seqs.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// seqsDescending builds a newest-first log of n note entries.
func seqsDescending(n int) []box.Entry {
	out := make([]box.Entry, 0, n)
	for i := n; i >= 1; i-- {
		out = append(out, noteEntry(i))
	}
	return out
}

// TestMessageListFilter runs one case per filter rule: the whole log, the
// notify branch, a branch that matches nothing, and the toggle in both
// directions.
func TestMessageListFilter(t *testing.T) {
	entries := []box.Entry{
		noteEntry(4),
		entry(3, box.StateDone, box.NotifyPrefix+"w2p1", "w2p1", "build green", time.Minute),
		noteEntry(2),
		entry(1, box.StateBlocked, box.NotifyPrefix, "w3p1", "needs input", time.Hour),
	}
	cases := []struct {
		name   string
		set    func(*MessageList)
		want   []int
		filter string
	}{
		{
			name: "no filter shows every entry",
			set:  func(l *MessageList) { l.Set(entries) },
			want: []int{4, 3, 2, 1},
		},
		{
			name: "the notify filter shows the branch",
			set: func(l *MessageList) {
				l.Set(entries)
				l.SetFilter(box.NotifyPrefix)
			},
			want:   []int{3, 1},
			filter: box.NotifyPrefix,
		},
		{
			name: "a branch with no rows shows none",
			set: func(l *MessageList) {
				l.Set(entries)
				l.SetFilter("nobody/")
			},
			want:   []int{},
			filter: "nobody/",
		},
		{
			name: "the toggle narrows",
			set: func(l *MessageList) {
				l.Set(entries)
				l.ToggleFilter()
			},
			want:   []int{3, 1},
			filter: box.NotifyPrefix,
		},
		{
			name: "the toggle widens a second time",
			set: func(l *MessageList) {
				l.Set(entries)
				l.ToggleFilter()
				l.ToggleFilter()
			},
			want: []int{4, 3, 2, 1},
		},
		{
			name: "the toggle widens a narrower filter",
			set: func(l *MessageList) {
				l.Set(entries)
				l.SetFilter("lane/")
				l.ToggleFilter()
			},
			want: []int{4, 3, 2, 1},
		},
	}
	for _, c := range cases {
		var l MessageList
		c.set(&l)
		if c.filter != l.Filter {
			t.Fatalf("%s: filter = %q, want %q", c.name, l.Filter, c.filter)
		}
		if got, want := seqsOf(&l), c.want; !sameInts(got, want) {
			t.Fatalf("%s: rows = %v, want %v", c.name, got, want)
		}
		if l.Len() != len(c.want) {
			t.Fatalf("%s: Len = %d, want %d", c.name, l.Len(), len(c.want))
		}
		if l.View.N != len(c.want) {
			t.Fatalf("%s: View.N = %d, want %d", c.name, l.View.N, len(c.want))
		}
	}
}

// seqsOf reads the filtered entries newest first through the value alone: the
// test walks the rows by moving the selection, so it sees what the view shows.
func seqsOf(l *MessageList) []int {
	out := make([]int, 0, l.Len())
	saved := l.View
	defer func() { l.View = saved }()
	l.View.Sel = -1
	for i := 0; i < l.Len(); i++ {
		l.View.Sel = i
		e, ok := l.Selected()
		if !ok {
			break
		}
		out = append(out, e.Seq)
	}
	return out
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestMessageListSelection runs one case per selection rule: the selection
// kept by seq across a Set, a seq that left the log, the clamp at both ends,
// the empty list, and the selection kept across a filter toggle.
func TestMessageListSelection(t *testing.T) {
	cases := []struct {
		name string
		run  func(*MessageList)
		want int // the selected seq, 0 when nothing is selected
		sel  int
	}{
		{
			name: "a fresh list selects the newest row",
			run:  func(l *MessageList) { l.Set(seqsDescending(3)) },
			want: 3,
			sel:  0,
		},
		{
			name: "move down",
			run: func(l *MessageList) {
				l.Set(seqsDescending(3))
				l.Move(1)
			},
			want: 2,
			sel:  1,
		},
		{
			name: "move clamps at the oldest end",
			run: func(l *MessageList) {
				l.Set(seqsDescending(3))
				l.Move(99)
			},
			want: 1,
			sel:  2,
		},
		{
			name: "move clamps at the newest end",
			run: func(l *MessageList) {
				l.Set(seqsDescending(3))
				l.Move(99)
				l.Move(-99)
			},
			want: 3,
			sel:  0,
		},
		{
			name: "move of zero leaves the selection",
			run: func(l *MessageList) {
				l.Set(seqsDescending(3))
				l.Move(1)
				l.Move(0)
			},
			want: 2,
			sel:  1,
		},
		{
			name: "top and bottom",
			run: func(l *MessageList) {
				l.Set(seqsDescending(3))
				l.Bottom()
				l.Top()
			},
			want: 3,
			sel:  0,
		},
		{
			name: "bottom selects the oldest",
			run: func(l *MessageList) {
				l.Set(seqsDescending(3))
				l.Bottom()
			},
			want: 1,
			sel:  2,
		},
		{
			name: "the selection stays on its seq across a set",
			run: func(l *MessageList) {
				l.Set(seqsDescending(3))
				l.Move(1) // seq 2
				l.Set([]box.Entry{noteEntry(5), noteEntry(4), noteEntry(3), noteEntry(2), noteEntry(1)})
			},
			want: 2,
			sel:  3,
		},
		{
			name: "a selection whose seq left keeps its row, clamped",
			run: func(l *MessageList) {
				l.Set(seqsDescending(4))
				l.Move(3) // seq 1
				l.Set([]box.Entry{noteEntry(9), noteEntry(8)})
			},
			want: 8,
			sel:  1,
		},
		{
			name: "an empty list keeps no selection",
			run:  func(l *MessageList) { l.Set(nil) },
			want: 0,
			sel:  -1,
		},
		{
			name: "an emptied list keeps no selection",
			run: func(l *MessageList) {
				l.Set(seqsDescending(3))
				l.Set(nil)
			},
			want: 0,
			sel:  -1,
		},
		{
			name: "top and bottom on an empty list select nothing",
			run: func(l *MessageList) {
				l.Set(nil)
				l.Top()
				l.Bottom()
			},
			want: 0,
			sel:  -1,
		},
		{
			name: "the selection survives the filter toggle",
			run: func(l *MessageList) {
				l.Set([]box.Entry{
					noteEntry(4),
					entry(3, box.StateDone, box.NotifyPrefix+"w2p1", "w2p1", "green", time.Minute),
					noteEntry(2),
					entry(1, box.StateBlocked, box.NotifyPrefix, "w3p1", "input", time.Hour),
				})
				l.Move(1) // seq 3, a notify entry
				l.ToggleFilter()
			},
			want: 3,
			sel:  0,
		},
		{
			name: "a selection the filter hides keeps its row",
			run: func(l *MessageList) {
				l.Set([]box.Entry{
					entry(4, box.StateDone, box.NotifyPrefix+"w2p1", "w2p1", "green", time.Minute),
					noteEntry(3),
					entry(2, box.StateBlocked, box.NotifyPrefix, "w3p1", "input", time.Hour),
					noteEntry(1),
				})
				l.SetFilter(box.NotifyPrefix) // rows seq 4, seq 2
				l.Move(1)                     // seq 2
				l.SetFilter("lane/")          // seq 2 is hidden; row 1 is seq 1
			},
			want: 1,
			sel:  1,
		},
	}
	for _, c := range cases {
		var l MessageList
		c.run(&l)
		if l.View.Sel != c.sel {
			t.Fatalf("%s: Sel = %d, want %d", c.name, l.View.Sel, c.sel)
		}
		e, ok := l.Selected()
		if c.want == 0 {
			if ok {
				t.Fatalf("%s: selected %d, want nothing", c.name, e.Seq)
			}
			continue
		}
		if !ok {
			t.Fatalf("%s: nothing selected, want seq %d", c.name, c.want)
		}
		if e.Seq != c.want {
			t.Fatalf("%s: selected seq %d, want %d", c.name, e.Seq, c.want)
		}
	}
}

// TestMessageListUnread counts the filtered entries past the read mark, and
// pins that a mark never walks back and that MarkReadAll covers the view.
func TestMessageListUnread(t *testing.T) {
	entries := []box.Entry{
		entry(6, box.StateDone, box.NotifyPrefix+"w2p1", "w2p1", "green", time.Minute),
		noteEntry(5),
		entry(4, box.StateWorking, box.NotifyPrefix, "w3p1", "busy", time.Hour),
		noteEntry(3),
	}
	var l MessageList
	l.Set(entries)
	if got := l.Unread(); got != 4 {
		t.Fatalf("unread with no mark = %d, want 4", got)
	}
	l.MarkRead(4)
	if got := l.Unread(); got != 2 {
		t.Fatalf("unread after marking 4 = %d, want 2", got)
	}
	l.MarkRead(3)
	if got := l.Unread(); got != 2 {
		t.Fatalf("unread after a mark below the mark = %d, want 2", got)
	}
	l.SetFilter(box.NotifyPrefix)
	if got := l.Unread(); got != 1 {
		t.Fatalf("unread under the notify filter = %d, want 1", got)
	}
	l.MarkReadAll()
	if got := l.Unread(); got != 0 {
		t.Fatalf("unread after MarkReadAll = %d, want 0", got)
	}
	if l.Read != 6 {
		t.Fatalf("read mark = %d, want 6", l.Read)
	}
	l.SetFilter("")
	if got := l.Unread(); got != 0 {
		t.Fatalf("unread over the whole log = %d, want 0", got)
	}
}

// TestMessageListUnreadAllMarksTheView pins that MarkReadAll covers the
// filtered view only: a filter hides rows and the mark does not reach them.
func TestMessageListUnreadAllMarksTheView(t *testing.T) {
	var l MessageList
	l.Set([]box.Entry{noteEntry(9), entry(8, box.StateDone, box.NotifyPrefix, "w1p1", "ok", time.Minute)})
	l.SetFilter(box.NotifyPrefix)
	l.MarkReadAll()
	if l.Read != 8 {
		t.Fatalf("read mark = %d, want 8 under the filter", l.Read)
	}
	if got := l.Unread(); got != 0 {
		t.Fatalf("unread under the filter = %d, want 0", got)
	}
	l.SetFilter("")
	if got := l.Unread(); got != 1 {
		t.Fatalf("unread over the whole log = %d, want 1: seq 9 stayed unread", got)
	}
}

// TestMessageAge runs one case per age branch: the four short units and the
// two edges a clock can hand the column.
func TestMessageAge(t *testing.T) {
	cases := []struct {
		name string
		at   time.Time
		want string
	}{
		{"seconds", entryNow.Add(-42 * time.Second), "42s"},
		{"a whole minute", entryNow.Add(-time.Minute), "1m"},
		{"minutes", entryNow.Add(-7 * time.Minute), "7m"},
		{"a whole hour", entryNow.Add(-time.Hour), "1h"},
		{"hours", entryNow.Add(-3 * time.Hour), "3h"},
		{"a whole day", entryNow.Add(-24 * time.Hour), "1d"},
		{"days", entryNow.Add(-48 * time.Hour), "2d"},
		{"a future time reads 0s", entryNow.Add(time.Hour), "0s"},
		{"no time reads a dash", time.Time{}, "-"},
	}
	for _, c := range cases {
		if got := messageAge(c.at, entryNow); got != c.want {
			t.Fatalf("%s: age = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestMessageRowText pins the exact text of one row: every column, the unread
// mark, the receipt count, and the state column's cell offset that the painter
// highlights.
func TestMessageRowText(t *testing.T) {
	e := box.Entry{
		Seq:   42,
		At:    entryNow.Add(-7 * time.Minute),
		From:  "w1p1",
		Key:   "lane/w1",
		State: box.StateWorking,
		Msg:   "tests green",
	}
	row, start := messageRowText(e, 80, 3, true, messageAge(e.At, entryNow))
	want := " * 3   42 working lane/w1        w1p1     7m  tests green"
	if row != want {
		t.Fatalf("row = %q, want %q", row, want)
	}
	if state := string(e.State); row[start:start+len(state)] != state {
		t.Fatalf("state column starts at %d, reads %q", start, row[start:])
	}
}

// TestMessageRowTextColumns pins the per-column rules the row text carries: a
// read row, an empty from, a seq that outgrows its column, the receipt clamp
// and the drop, and an oversize key clipped rather than allowed to shift the
// later columns.
func TestMessageRowTextColumns(t *testing.T) {
	cases := []struct {
		name    string
		entry   box.Entry
		w       int
		receipt int
		unread  bool
		age     string
		want    string
	}{
		{
			name:    "a read row carries a blank mark and a zero receipt",
			entry:   box.Entry{Seq: 1, Key: "k", From: "w1p1", State: box.StateDone, Msg: "ok"},
			w:       80,
			receipt: 0,
			age:     "2d",
			want:    "   0    1 done    k              w1p1     2d  ok",
		},
		{
			name:    "an empty from reads a dash",
			entry:   box.Entry{Seq: 7, Key: "k", State: box.StateNote, Msg: "m"},
			w:       80,
			receipt: 0,
			age:     "1m",
			want:    "   0    7 note    k              -        1m  m",
		},
		{
			name:    "a receipt past two digits clamps",
			entry:   box.Entry{Seq: 7, Key: "k", From: "f", State: box.StateNote, Msg: "m"},
			w:       80,
			receipt: 120,
			age:     "1m",
			want:    "  99    7 note    k              f        1m  m",
		},
		{
			name:    "a negative receipt drops the column",
			entry:   box.Entry{Seq: 7, Key: "k", From: "f", State: box.StateNote, Msg: "m"},
			w:       80,
			receipt: -1,
			age:     "1m",
			want:    "     7 note    k              f        1m  m",
		},
		{
			name:    "a seq past four cells shifts the columns",
			entry:   box.Entry{Seq: 12345, Key: "k", From: "f", State: box.StateNote, Msg: "m"},
			w:       80,
			receipt: -1,
			age:     "1m",
			want:    "  12345 note    k              f        1m  m",
		},
		{
			name:    "a long key clips to its column",
			entry:   box.Entry{Seq: 2, Key: "lane/a-very-long-topic", From: "f", State: box.StateNote, Msg: "m"},
			w:       80,
			receipt: -1,
			age:     "1m",
			want:    "     2 note    lane/a-very-lo f        1m  m",
		},
		{
			name:    "a narrow row clips the msg",
			entry:   box.Entry{Seq: 2, Key: "k", From: "f", State: box.StateNote, Msg: "a long message"},
			w:       50,
			receipt: -1,
			age:     "1m",
			want:    "     2 note    k              f        1m  a long ",
		},
		{
			name:    "a row narrower than the fixed columns clips the msg away",
			entry:   box.Entry{Seq: 2, Key: "k", From: "f", State: box.StateNote, Msg: "a long message"},
			w:       45,
			receipt: -1,
			age:     "1m",
			want:    "     2 note    k              f        1m  a ",
		},
	}
	for _, c := range cases {
		row, _ := messageRowText(c.entry, c.w, c.receipt, c.unread, c.age)
		if row != c.want {
			t.Fatalf("%s: row = %q, want %q", c.name, row, c.want)
		}
	}
}

// TestMessageListDrawReceiptBoundary pins the receipt column rule: at 60
// columns and above every row carries the count, and below 60 the column is
// dropped whole so the msg keeps its cells.
func TestMessageListDrawReceiptBoundary(t *testing.T) {
	e := box.Entry{Seq: 3, At: entryNow.Add(-time.Minute), From: "w1p1", Key: "lane/w1", State: box.StateDone, Msg: "green"}
	cases := []struct {
		name string
		w    int
		want string
	}{
		{
			name: "at the boundary the count shows",
			w:    messageReceiptMinW,
			want: " * 2    3 done    lane/w1        w1p1     1m  green",
		},
		{
			name: "one cell under the boundary drops the column",
			w:    messageReceiptMinW - 1,
			want: " *   3 done    lane/w1        w1p1     1m  green",
		},
		{
			name: "a wide rect carries the count",
			w:    100,
			want: " * 2    3 done    lane/w1        w1p1     1m  green",
		},
	}
	for _, c := range cases {
		var l MessageList
		l.Set([]box.Entry{e})
		rows := drawMessageList(t, MessageListProps{
			Rect:     layout.Rect{W: c.w, H: 1},
			Now:      entryNow,
			Dim:      theme.DefaultTheme().Palette.RailDim,
			Text:     theme.DefaultTheme().Palette.RailText,
			Active:   true,
			Receipts: map[int]int{3: 2},
		}, l)
		if got := strings.TrimRight(rows[0], " "); got != c.want {
			t.Fatalf("%s: row = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestMessageListDrawWindow pins the window: Draw paints the rows from Off to
// Off+H, absolutely placed, and the value is untouched by the paint.
func TestMessageListDrawWindow(t *testing.T) {
	var l MessageList
	l.Set(seqsDescending(5))
	l.View.H = 3
	l.Move(1)
	l.View.Ensure()
	if l.View.Off != 0 {
		t.Fatalf("Off = %d, want 0", l.View.Off)
	}
	before := l
	rows := drawMessageList(t, MessageListProps{
		Rect:   layout.Rect{W: 59, H: 4},
		Now:    entryNow,
		Dim:    theme.DefaultTheme().Palette.RailDim,
		Text:   theme.DefaultTheme().Palette.RailText,
		Active: true,
	}, l)
	wantMsgs := []string{"message 5", "message 4", "message 3", "message 2"}
	for i, want := range wantMsgs {
		if !strings.Contains(rows[i], want) {
			t.Fatalf("row %d = %q, want %q", i, rows[i], want)
		}
	}
	if !strings.HasPrefix(strings.TrimRight(rows[0], " "), " *   5") {
		t.Fatalf("row 0 = %q, want the newest seq at the top", rows[0])
	}

	// A window taller than the log leaves the rows past it blank.
	var short MessageList
	short.Set(seqsDescending(2))
	rows = drawMessageList(t, MessageListProps{
		Rect:   layout.Rect{W: 59, H: 4},
		Now:    entryNow,
		Dim:    theme.DefaultTheme().Palette.RailDim,
		Text:   theme.DefaultTheme().Palette.RailText,
		Active: true,
	}, short)
	for i := 2; i < 4; i++ {
		if rows[i] != strings.Repeat(" ", 59) {
			t.Fatalf("row %d past the log painted %q", i, rows[i])
		}
	}
	if l.View != before.View || l.Read != before.Read || l.Filter != before.Filter || len(l.Entries) != len(before.Entries) {
		t.Fatalf("Draw mutated the value")
	}
}

// TestMessageListDrawEmpty pins that an empty list paints nothing: the caller
// paints its own note.
func TestMessageListDrawEmpty(t *testing.T) {
	var l MessageList
	rows := drawMessageList(t, MessageListProps{
		Rect: layout.Rect{W: 40, H: 2},
		Now:  entryNow,
	}, l)
	for i, row := range rows {
		if row != strings.Repeat(" ", 40) {
			t.Fatalf("row %d = %q, want blank", i, row)
		}
	}
}

// TestMessageListDrawStateColour pins that the state word alone takes the
// rail's colour for that state, and that a row the list does not focus paints
// every run in the dim colour.
func TestMessageListDrawStateColour(t *testing.T) {
	th := theme.DefaultTheme()
	e := box.Entry{Seq: 1, At: entryNow, From: "w1p1", Key: "lane/w1", State: box.StateBlocked, Msg: "input"}
	var l MessageList
	l.Set([]box.Entry{e})

	cases := []struct {
		name   string
		active bool
		want   theme.RGB
	}{
		{"focused paints the selected row in Text", true, th.Palette.RailText},
		{"unfocused paints every row in Dim", false, th.Palette.RailDim},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		l.Draw(&buf, th, MessageListProps{
			Rect:   layout.Rect{W: 60, H: 1},
			Now:    entryNow,
			Dim:    th.Palette.RailDim,
			Text:   th.Palette.RailText,
			Active: c.active,
		})
		got := buf.String()
		base := theme.Style{Fg: styleColor(c.want)}.SGR()
		state := theme.Style{Fg: styleColor(messageStateColour(th, box.StateBlocked))}.SGR()
		if !strings.Contains(got, base) {
			t.Fatalf("%s: row never takes the base colour %q: %q", c.name, base, got)
		}
		if !strings.Contains(got, state+"blocked") {
			t.Fatalf("%s: the state word does not take the rail colour: %q", c.name, got)
		}
	}
}

// drawMessageList paints the list into a blank terminal of the rect's size and
// reads every row of the rect back, one row per slice entry.
func drawMessageList(t *testing.T, p MessageListProps, l MessageList) []string {
	t.Helper()
	var buf bytes.Buffer
	th := theme.DefaultTheme()
	l.Draw(&buf, th, p)
	term := vt.NewTerm(p.Rect.X+p.Rect.W, p.Rect.Y+p.Rect.H)
	term.Write(buf.Bytes())
	term.Flush()
	g := term.Grid()
	rows := make([]string, p.Rect.H)
	for i := range rows {
		rows[i] = rowText(g, p.Rect.Y+i, p.Rect.X, p.Rect.W)
	}
	return rows
}

// TestMessageListEmptyFilterWidensToARow: a list the filter emptied has no
// selection, and the filter that shows a row again selects it, so the actions
// that read the selection work without a move first.
func TestMessageListEmptyFilterWidensToARow(t *testing.T) {
	var l MessageList
	l.Set([]box.Entry{
		{Seq: 2, Key: "lane/a", State: box.StateNote, Msg: "one"},
		{Seq: 1, Key: "lane/a", State: box.StateNote, Msg: "two"},
	})
	l.SetFilter(box.NotifyPrefix)
	if l.Len() != 0 {
		t.Fatalf("rows = %d, want none under the notify filter", l.Len())
	}
	if e, ok := l.Selected(); ok {
		t.Fatalf("selected = %+v, want nothing under the filter", e)
	}
	l.ToggleFilter()
	if got, want := l.Len(), 2; got != want {
		t.Fatalf("rows = %d, want %d", got, want)
	}
	if got, want := l.View.Sel, 0; got != want {
		t.Fatalf("selection = %d, want the newest row %d", got, want)
	}
	if e, ok := l.Selected(); !ok || e.Seq != 2 {
		t.Fatalf("selected = %+v ok %v, want the newest entry", e, ok)
	}
}

// TestMessageListIndexAndSeqAt read the filtered rows: a caller holding an
// index reads the seq, and a caller holding the seq finds the row back after
// the filter or the entries change.
func TestMessageListIndexAndSeqAt(t *testing.T) {
	var l MessageList
	l.Set([]box.Entry{
		{Seq: 5, Key: "notify/w1p1", State: box.StateNote, Msg: "notify"},
		{Seq: 4, Key: "lane/a", State: box.StateNote, Msg: "lane"},
		{Seq: 3, Key: "notify/w1p2", State: box.StateNote, Msg: "notify"},
	})
	l.SetFilter(box.NotifyPrefix)
	cases := []struct {
		row, seq int
	}{
		{0, 5},
		{1, 3},
		{2, 0},
		{-1, 0},
	}
	for _, c := range cases {
		if got := l.SeqAt(c.row); got != c.seq {
			t.Fatalf("SeqAt(%d) = %d, want %d", c.row, got, c.seq)
		}
	}
	if got := l.Index(3); got != 1 {
		t.Fatalf("Index(3) = %d, want the second filtered row", got)
	}
	if got := l.Index(4); got != -1 {
		t.Fatalf("Index(4) = %d, want -1 under the filter", got)
	}
	// The same seq keeps its row when a filtered-out entry arrives above it.
	l.Set([]box.Entry{
		{Seq: 6, Key: "lane/a", State: box.StateNote, Msg: "lane"},
		{Seq: 5, Key: "notify/w1p1", State: box.StateNote, Msg: "notify"},
		{Seq: 4, Key: "lane/a", State: box.StateNote, Msg: "lane"},
		{Seq: 3, Key: "notify/w1p2", State: box.StateNote, Msg: "notify"},
	})
	if got := l.Index(3); got != 1 {
		t.Fatalf("Index(3) after a hidden arrival = %d, want 1", got)
	}
}
