package client

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"dm/internal/layout"
	"dm/internal/testutil"
	"dm/internal/vt"
	"dm/internal/wire"
)

// The scrollback search: `/` opens a query row, enter accepts, n and N step
// through the matches, and the band highlights every match row with the
// current one in reverse video.

// searchTUIconn is a one-pane client in scrollback with a filled ring, plus the
// channel of what it sends to the server.
func searchTUIconn(t *testing.T, rows int) (*tui, *wire.PaneInfo, chan wire.Msg) {
	t.Helper()
	tui, sent := clientTUI(t, nil)
	p := tui.paneByID("a")
	var ring strings.Builder
	for i := 0; i < rows; i++ {
		fmt.Fprintf(&ring, "ROW-%03d\r\n", i)
	}
	tui.hist = map[string][]byte{"a": []byte(ring.String())}
	tui.histMeta = map[string]histCache{"a": {at: p.Rect, got: true}}
	live := vt.NewTerm(p.Rect.W, p.Rect.H)
	live.Write([]byte(ring.String()))
	live.Flush()
	tui.vts["a"] = live
	tui.startScrollOnFocus()
	if tui.mode != "scroll" || tui.scrollVT == nil {
		t.Fatalf("did not enter scrollback: mode=%q vt=%v", tui.mode, tui.scrollVT != nil)
	}
	return tui, p, sent
}

// searchTUI is searchTUIconn without the send channel.
func searchTUI(t *testing.T, rows int) (*tui, *wire.PaneInfo) {
	t.Helper()
	tui, p, _ := searchTUIconn(t, rows)
	return tui, p
}

// press feeds keys to the client in its current mode. A lone esc is held as a
// possible paste marker, so the helper flushes the hold: the test sees the key
// the operator pressed instead of waiting out the clock.
func press(t *testing.T, tui *tui, keys string) {
	t.Helper()
	tui.inbuf = append(tui.inbuf, keys...)
	tui.processInput()
	if !tui.pasteDeadline.IsZero() {
		tui.expirePaste(time.Now().Add(pasteTimeout))
	}
	// A lone esc in scroll mode could still be the head of an arrow sequence,
	// so the mode keymap waits out the escape timeout. Expire it here.
	if tui.pending && tui.mode != "" {
		tui.expireMode(time.Now().Add(escTimeout))
	}
}

// tallRowOf returns the tall terminal row whose text holds want, or -1.
func tallRowOf(t *testing.T, tui *tui, want string) int {
	t.Helper()
	for i, row := range termRows(tui.scrollVT) {
		if strings.Contains(row, want) {
			return i
		}
	}
	return -1
}

// renderFrame draws one frame and replays it into a terminal, so a test reads
// the cells the operator would see.
func renderFrame(t *testing.T, tui *tui) *vt.Term {
	t.Helper()
	raw := testutil.CaptureStdout(t, func() { tui.draw() })
	term := vt.NewTerm(tui.snap.Cols, tui.snap.Rows)
	term.Write([]byte(raw))
	term.Flush()
	return term
}

// screenRowOf maps a tall terminal row onto its screen row in the scrolled
// pane.
func screenRowOf(t *testing.T, tui *tui, p *wire.PaneInfo, tallRow int) int {
	t.Helper()
	row0, _ := scrollStart(tui.scrollVT, p.Rect.H, tui.offset)
	return p.Rect.Y + tallRow - row0
}

// TestSearchPromptKeys pins the query row's editing keys: printable bytes
// append, backspace drops the last rune, ctrl+u clears and a lone esc cancels.
func TestSearchPromptKeys(t *testing.T) {
	t.Parallel()
	tui, _ := searchTUI(t, 60)

	press(t, tui, "/")
	if tui.mode != "search" {
		t.Fatalf("mode = %q, want search", tui.mode)
	}
	if tui.search == nil {
		t.Fatal("the query row opened without a draft")
	}
	press(t, tui, "ROW")
	if tui.search.draft != "ROW" {
		t.Fatalf("draft = %q, want ROW", tui.search.draft)
	}
	press(t, tui, "\x7f")
	if tui.search.draft != "RO" {
		t.Fatalf("after backspace draft = %q, want RO", tui.search.draft)
	}
	press(t, tui, "\x15")
	if tui.search.draft != "" {
		t.Fatalf("after ctrl+u draft = %q, want empty", tui.search.draft)
	}
	press(t, tui, "zz")
	press(t, tui, "\x1b")
	if tui.mode != "scroll" {
		t.Fatalf("esc left mode = %q, want scroll", tui.mode)
	}
	if tui.search == nil || tui.search.draft != "" {
		t.Fatalf("esc kept the draft: %+v", tui.search)
	}
	if tui.search.model != nil {
		t.Fatal("esc accepted a query")
	}
}

// TestSearchPromptKeepsKeysFromThePane checks no key of the query row reaches
// the pane: the prompt consumes every byte and sends nothing.
func TestSearchPromptKeepsKeysFromThePane(t *testing.T) {
	t.Parallel()
	tui, _, sent := searchTUIconn(t, 60)
	testutil.NoMsg(t, sent)

	press(t, tui, "/")
	press(t, tui, "ROW-02")
	press(t, tui, "\x7f")
	testutil.NoMsg(t, sent)
	if tui.mode != "search" {
		t.Fatalf("mode = %q, want search", tui.mode)
	}
	if tui.search.draft != "ROW-0" {
		t.Fatalf("draft = %q, want ROW-0", tui.search.draft)
	}
}

// TestSearchAcceptJumpsToTheMatch checks enter accepts the query, finds the
// match and puts its row at the top of the band.
func TestSearchAcceptJumpsToTheMatch(t *testing.T) {
	t.Parallel()
	tui, p := searchTUI(t, 60)
	want := tallRowOf(t, tui, "ROW-010")
	if want < 0 {
		t.Fatal("ROW-010 is not in the tall terminal")
	}

	press(t, tui, "/")
	press(t, tui, "ROW-010")
	press(t, tui, "\r")
	if tui.mode != "scroll" {
		t.Fatalf("accept left mode = %q, want scroll", tui.mode)
	}
	if tui.search == nil || tui.search.model == nil {
		t.Fatal("accept kept no query")
	}
	m := tui.search.model
	if m.Count() != 1 || m.CurrentRow() != want {
		t.Fatalf("matches = %d, current row = %d, want one at %d", m.Count(), m.CurrentRow(), want)
	}
	row0, _ := scrollStart(tui.scrollVT, p.Rect.H, tui.offset)
	if row0 != want {
		t.Fatalf("band starts at row %d, want the match row %d", row0, want)
	}
	if got := tui.barInfo(); !strings.Contains(got, "SEARCH ROW-010 1/1") {
		t.Fatalf("bar info = %q, want the search block", got)
	}
}

// TestSearchCancelKeepsOffset checks esc in the prompt leaves the picture where
// it was and accepts nothing.
func TestSearchCancelKeepsOffset(t *testing.T) {
	t.Parallel()
	tui, _ := searchTUI(t, 60)
	tui.offset = 5
	press(t, tui, "/")
	press(t, tui, "ROW-01")
	press(t, tui, "\x1b")
	if tui.search == nil || tui.search.model != nil {
		t.Fatal("cancel accepted a query")
	}
	if tui.offset != 5 {
		t.Fatalf("offset = %d after cancel, want 5", tui.offset)
	}
}

// TestSearchNoMatchToasts checks a query with no match raises the toast and
// leaves the offset alone.
func TestSearchNoMatchToasts(t *testing.T) {
	t.Parallel()
	tui, _ := searchTUI(t, 60)
	tui.offset = 4
	press(t, tui, "/")
	press(t, tui, "zzz")
	press(t, tui, "\r")
	if tui.offset != 4 {
		t.Fatalf("offset = %d, want the untouched 4", tui.offset)
	}
	if tui.search == nil || tui.search.model != nil {
		t.Fatal("a query with no match was accepted")
	}
	if len(tui.toasts) == 0 || tui.toasts[len(tui.toasts)-1].text != "no match: zzz" {
		t.Fatalf("toasts = %+v, want no match: zzz", tui.toasts)
	}
}

// TestSearchNextPrevWrap checks n steps to the older match, N to the newer one,
// both wrapping, and that each step lands the match at the top of the band.
func TestSearchNextPrevWrap(t *testing.T) {
	t.Parallel()
	tui, p := searchTUI(t, 60)
	press(t, tui, "/")
	press(t, tui, "ROW-01")
	press(t, tui, "\r")
	m := tui.search.model
	if m == nil || m.Count() < 3 {
		t.Fatalf("matches = %v, want several", m)
	}
	cur := m.Current()

	press(t, tui, "n")
	if want := (cur - 1 + m.Count()) % m.Count(); m.Current() != want {
		t.Fatalf("n moved to %d, want the older match %d", m.Current(), want)
	}
	row0, _ := scrollStart(tui.scrollVT, p.Rect.H, tui.offset)
	if row0 != m.CurrentRow() {
		t.Fatalf("band starts at row %d, want the match row %d", row0, m.CurrentRow())
	}

	press(t, tui, "N")
	if m.Current() != cur {
		t.Fatalf("N moved to %d, want back to %d", m.Current(), cur)
	}

	// n wraps past the first match to the last one.
	for m.Current() != 0 {
		press(t, tui, "n")
	}
	press(t, tui, "n")
	if m.Current() != m.Count()-1 {
		t.Fatalf("n from the first match went to %d, want the last %d", m.Current(), m.Count()-1)
	}
	// N wraps past the last match to the first one.
	press(t, tui, "N")
	if m.Current() != 0 {
		t.Fatalf("N from the last match went to %d, want the first 0", m.Current())
	}
}

// TestSearchEscOrder checks the first esc clears an accepted query and keeps
// scrollback, and the next one leaves.
func TestSearchEscOrder(t *testing.T) {
	t.Parallel()
	tui, _ := searchTUI(t, 60)
	press(t, tui, "/")
	press(t, tui, "ROW-01")
	press(t, tui, "\r")
	if tui.search == nil || tui.search.model == nil {
		t.Fatal("the query was not accepted")
	}

	press(t, tui, "\x1b")
	if tui.mode != "scroll" || tui.scroll == "" {
		t.Fatalf("esc with a query left scrollback: mode=%q scroll=%q", tui.mode, tui.scroll)
	}
	if tui.search != nil {
		t.Fatal("esc kept the query and its matches")
	}

	press(t, tui, "\x1b")
	if tui.mode != "" || tui.scroll != "" {
		t.Fatalf("the second esc left mode=%q scroll=%q, want live", tui.mode, tui.scroll)
	}
}

// TestSearchHighlightsMatches checks the frame: every visible match row takes
// the highlight, the current one is reverse video, and a row without a match
// keeps its own background. It is serial because it captures stdout.
func TestSearchHighlightsMatches(t *testing.T) {
	tui, p := searchTUI(t, 60)
	press(t, tui, "/")
	press(t, tui, "ROW-01")
	press(t, tui, "\r")
	m := tui.search.model
	if m == nil || m.Count() < 2 {
		t.Fatalf("matches = %v, want several", m)
	}
	// One step older leaves a newer match inside the band, below the current
	// row, so both a highlight and a plain row are on screen.
	press(t, tui, "n")
	screen := renderFrame(t, tui)
	highlight := tui.searchBG()
	curRow := m.CurrentRow()
	other := -1
	for _, row := range m.Matches() {
		if row == curRow {
			continue
		}
		if y := screenRowOf(t, tui, p, row); y > 0 && y < p.Rect.H {
			other = row
			break
		}
	}
	if other < 0 {
		t.Fatal("no other match row is on screen")
	}

	curCell := screen.Grid().Cell(1, screenRowOf(t, tui, p, curRow))
	if curCell.Style.Attrs&vt.Reverse == 0 {
		t.Fatalf("the current match row is not reverse video: %+v", curCell.Style)
	}
	otherCell := screen.Grid().Cell(1, screenRowOf(t, tui, p, other))
	if otherCell.Style.Bg != highlight {
		t.Fatalf("a match row background = %+v, want %+v", otherCell.Style.Bg, highlight)
	}
	// A row in the band with no match keeps the default background.
	row0, _ := scrollStart(tui.scrollVT, p.Rect.H, tui.offset)
	plain := -1
	for row := row0; row < row0+p.Rect.H; row++ {
		if !m.HasMatch(row) {
			plain = row
			break
		}
	}
	if plain < 0 {
		t.Fatal("every row of the band holds a match")
	}
	plainCell := screen.Grid().Cell(1, screenRowOf(t, tui, p, plain))
	if plainCell.Style.Bg == highlight || plainCell.Style.Attrs&vt.Reverse != 0 {
		t.Fatalf("a row without a match is marked: %+v", plainCell.Style)
	}
}

// TestSearchClearedOnExit checks leaving scrollback drops the query, its
// matches and the highlights. It is serial because it captures stdout.
func TestSearchClearedOnExit(t *testing.T) {
	tui, _ := searchTUI(t, 60)
	press(t, tui, "/")
	press(t, tui, "ROW-01")
	press(t, tui, "\r")
	if tui.search == nil || tui.search.model == nil {
		t.Fatal("the query was not accepted")
	}

	tui.exitScroll()
	if tui.search != nil {
		t.Fatal("leaving scrollback kept the query")
	}
	tui.startScrollOnFocus()
	screen := renderFrame(t, tui)
	highlight := tui.searchBG()
	for y := 0; y < tui.snap.Rows; y++ {
		for x := 0; x < tui.snap.Cols; x++ {
			if c := screen.Grid().Cell(x, y); c.Style.Bg == highlight {
				t.Fatalf("a highlight survived the exit at %d,%d", x, y)
			}
		}
	}
}

// TestSearchOffsetAtBothEnds checks the offset math at the ends of the
// history: the newest match clamps to the live screen instead of scrolling past
// it, and the oldest lands at the top of the band.
func TestSearchOffsetAtBothEnds(t *testing.T) {
	t.Parallel()
	tui, p := searchTUI(t, 60)
	rows := termRows(tui.scrollVT)
	if len(rows) < p.Rect.H+4 {
		t.Fatalf("the tall terminal holds %d rows, too few for the ends", len(rows))
	}
	last := len(rows) - 1
	for last > 0 && strings.TrimSpace(rows[last]) == "" {
		last--
	}

	// From the live screen the newest row is on screen already, so the jump
	// clamps to the live row instead of scrolling past it.
	tui.offset = 0
	press(t, tui, "/")
	press(t, tui, strings.TrimSpace(rows[last]))
	press(t, tui, "\r")
	m := tui.search.model
	if m == nil {
		t.Fatal("the newest row found no match")
	}
	if m.CurrentRow() >= last-p.Rect.H && tui.offset != 0 {
		t.Fatalf("the newest match left offset = %d, want the live screen at 0", tui.offset)
	}
	row0, _ := scrollStart(tui.scrollVT, p.Rect.H, tui.offset)
	if m.CurrentRow() < row0 || m.CurrentRow() >= row0+p.Rect.H {
		t.Fatalf("the current match row %d is outside the band at row %d", m.CurrentRow(), row0)
	}

	// The oldest row takes the whole scroll: its offset is the maximum and its
	// row is the first one drawn.
	press(t, tui, "/")
	press(t, tui, strings.TrimSpace(rows[0]))
	press(t, tui, "\r")
	row0, maxOff := scrollStart(tui.scrollVT, p.Rect.H, tui.offset)
	if row0 != 0 || tui.offset != maxOff {
		t.Fatalf("oldest match: row0=%d offset=%d maxOff=%d, want the top at the maximum", row0, tui.offset, maxOff)
	}
}

// TestSearchPromptRowPaints checks the query row lands on the row above the
// status bar with its label and draft. It is serial because it captures stdout.
func TestSearchPromptRowPaints(t *testing.T) {
	tui, _ := searchTUI(t, 60)
	press(t, tui, "/")
	press(t, tui, "ROW")
	screen := renderFrame(t, tui)

	var row strings.Builder
	for x := 0; x < tui.snap.Cols; x++ {
		c := screen.Grid().Cell(x, tui.snap.Rows-2)
		if c.Content == "" {
			row.WriteByte(' ')
			continue
		}
		row.WriteString(c.Content)
	}
	got := strings.TrimRight(row.String(), " ")
	if !strings.Contains(got, "search: ROW") {
		t.Fatalf("the query row reads %q, want the label and the draft", got)
	}
}

// TestSearchDroppedOnResize checks a resize that moves the pane closes the
// search with the scrollback, so the next frame carries no mark over the new
// rows. It is serial because it captures stdout.
func TestSearchDroppedOnResize(t *testing.T) {
	tui, _ := searchTUI(t, 60)
	press(t, tui, "/")
	press(t, tui, "ROW-01")
	press(t, tui, "\r")
	if tui.search == nil || tui.search.model == nil {
		t.Fatal("the query was not accepted")
	}

	small := testutil.TestSnapshot(&layout.Node{Pane: "a"}, 80, 24, nil)
	small.Focus = "a"
	tui.apply(wire.Msg{Type: "snapshot", Snap: small})
	if tui.search != nil {
		t.Fatal("the resize kept the query over rows that are gone")
	}
	if tui.mode != "" || tui.scroll != "" {
		t.Fatalf("the resize left scrollback open: mode=%q scroll=%q", tui.mode, tui.scroll)
	}

	// The frame the operator sees carries no highlight and no reverse row.
	screen := renderFrame(t, tui)
	highlight := tui.searchBG()
	for y := 0; y < tui.snap.Rows; y++ {
		for x := 0; x < tui.snap.Cols; x++ {
			c := screen.Grid().Cell(x, y)
			if c.Style.Bg == highlight {
				t.Fatalf("a highlight survived the resize at %d,%d", x, y)
			}
			if c.Style.Attrs&vt.Reverse != 0 {
				t.Fatalf("a reverse row survived the resize at %d,%d", x, y)
			}
		}
	}
}

// TestSearchDroppedOnPaneChange checks a wheel to another pane drops the query
// built over the pane the operator left, so the old model never paints or steps
// over the new pane's rows.
func TestSearchDroppedOnPaneChange(t *testing.T) {
	t.Parallel()
	tui, p := searchTUI(t, 60)
	press(t, tui, "/")
	press(t, tui, "ROW-01")
	press(t, tui, "\r")
	if tui.search == nil || tui.search.model == nil {
		t.Fatal("the query was not accepted")
	}

	b := &wire.PaneInfo{ID: "b", Rect: p.Rect}
	tui.hist["b"] = tui.hist["a"]
	tui.histMeta["b"] = histCache{at: b.Rect, got: true}
	tui.startScroll(b)
	if tui.search != nil {
		t.Fatal("the model followed the scroll to another pane")
	}
	if tui.scroll != "b" || tui.mode != "scroll" {
		t.Fatalf("scroll target = %q mode = %q, want b in scroll", tui.scroll, tui.mode)
	}
	// n and N do nothing without a model, and scrollback stays open.
	press(t, tui, "n")
	press(t, tui, "N")
	if tui.search != nil || tui.scroll != "b" {
		t.Fatalf("a step recreated the model: search=%v scroll=%q", tui.search != nil, tui.scroll)
	}
}

// TestSearchPromptTakesPaste checks a bracketed paste over the query row is
// query text: it never reaches the pane, and the esc order still holds after
// it.
func TestSearchPromptTakesPaste(t *testing.T) {
	t.Parallel()
	tui, _, sent := searchTUIconn(t, 60)
	press(t, tui, "/")
	press(t, tui, "\x1b[200~W-01\x1b[201~")
	if tui.mode != "search" {
		t.Fatalf("the paste left mode = %q, want search", tui.mode)
	}
	if tui.search == nil || tui.search.draft != "W-01" {
		t.Fatalf("draft = %+v, want the pasted W-01", tui.search)
	}
	testutil.NoMsg(t, sent)

	press(t, tui, "\r")
	if tui.search == nil || tui.search.model == nil {
		t.Fatal("the pasted query was not accepted")
	}
	// The esc order holds after a paste.
	press(t, tui, "\x1b")
	if tui.search != nil {
		t.Fatal("esc kept the pasted query")
	}
	if tui.mode != "scroll" || tui.scroll == "" {
		t.Fatalf("the first esc left scrollback: mode=%q scroll=%q", tui.mode, tui.scroll)
	}
	press(t, tui, "\x1b")
	if tui.mode != "" || tui.scroll != "" {
		t.Fatalf("the second esc left mode=%q scroll=%q, want live", tui.mode, tui.scroll)
	}
}

// TestSearchPromptKeepsBurstAfterSlash checks the bytes that share one read
// with the key that opens the query row are the start of the draft.
func TestSearchPromptKeepsBurstAfterSlash(t *testing.T) {
	t.Parallel()
	tui, _ := searchTUI(t, 60)
	press(t, tui, "/ROW-01")
	if tui.mode != "search" {
		t.Fatalf("mode = %q, want search", tui.mode)
	}
	if tui.search == nil || tui.search.draft != "ROW-01" {
		t.Fatalf("draft = %+v, want ROW-01 from the same read", tui.search)
	}
	press(t, tui, "\r")
	if tui.search == nil || tui.search.model == nil {
		t.Fatal("the burst query was not accepted")
	}
}
