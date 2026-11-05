package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

// renamePromptTUI is a client on a snapshot whose active workspace is s1 with
// the label main, plus a second workspace the prompt must never pick. The
// snapshot carries the label in Workspace, as the server sends it, and the
// prompt resolves the id from the rail entries.
func renamePromptTUI(t *testing.T) (*tui, chan Msg) {
	t.Helper()
	tr, got := clientTUI(t, nil)
	tr.snap.Workspace = "main"
	tr.snap.All = []AllPane{
		{ID: "a", Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one", Focus: true},
		{ID: "z", Workspace: "s2", WorkspaceName: "work", Window: "w1", WindowName: "one"},
	}
	return tr, got
}

// renamePromptKeys feeds bytes through the normal input path.
func renamePromptKeys(tr *tui, keys string) {
	tr.inbuf = append(tr.inbuf, keys...)
	tr.processInput()
}

// renamePromptVisible is the text a paint leaves on screen: its escape
// sequences removed.
func renamePromptVisible(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			i++
			continue
		}
		i++
		if i < len(s) && s[i] == '[' {
			i++
			for i < len(s) && (s[i] < 0x40 || s[i] > 0x7e) {
				i++
			}
			if i < len(s) {
				i++
			}
			continue
		}
		if i < len(s) {
			i++
		}
	}
	return b.String()
}

// Prefix r opens the prompt with the active workspace's label in the draft and
// sends nothing. The sibling workspace's label must not leak in.
func TestRenamePromptOpensOnPrefixR(t *testing.T) {
	tr, got := renamePromptTUI(t)
	renamePromptKeys(tr, "\x02r")
	if tr.mode != "rename" {
		t.Fatalf("mode = %q, want rename", tr.mode)
	}
	if tr.renameDraft != "main" {
		t.Fatalf("draft = %q, want %q", tr.renameDraft, "main")
	}
	noMsg(t, got)

	// A snapshot that lists no workspace for the active id opens an empty
	// draft, not some other workspace's name.
	tr.snap.All = nil
	tr.closeRename()
	renamePromptKeys(tr, "\x02r")
	if tr.mode != "rename" || tr.renameDraft != "" {
		t.Fatalf("no-match open: mode = %q draft = %q", tr.mode, tr.renameDraft)
	}
	noMsg(t, got)
}

// The right-click menu offers the same prompt and closes itself first.
func TestRenamePromptMenuRow(t *testing.T) {
	tr, got := renamePromptTUI(t)
	tr.openMenu(tr.paneByID("a"), 10, 10)
	found := false
	for _, it := range tr.menu.items {
		if it.label != "Rename workspace" {
			continue
		}
		found = true
		if it.key != "r" {
			t.Fatalf("menu key = %q, want r", it.key)
		}
		it.run()
	}
	if !found {
		t.Fatal("the menu has no rename row")
	}
	if tr.menu != nil {
		t.Fatalf("menu is still open: %+v", tr.menu)
	}
	if tr.mode != "rename" || tr.renameDraft != "main" {
		t.Fatalf("menu row gave mode %q draft %q", tr.mode, tr.renameDraft)
	}
	noMsg(t, got)
}

// Prefix b cycles the rail and prefix r no longer does.
func TestRenamePromptRailMovedToB(t *testing.T) {
	tr, got := renamePromptTUI(t)
	tr.snap.Rail = 0
	renamePromptKeys(tr, "\x02b")
	if m := nextMsg(t, got); m.Action != "set-rail" || m.Index != defaultRail {
		t.Fatalf("prefix b sent %+v, want set-rail %d", m, defaultRail)
	}

	tr.snap.Rail = defaultRail
	tr.snap.RailWant = defaultRail
	renamePromptKeys(tr, "\x02r")
	if tr.mode != "rename" {
		t.Fatalf("prefix r opened %q, want rename", tr.mode)
	}
	noMsg(t, got)
}

// Typing appends, both delete bytes drop one rune, and ctrl+u clears.
func TestRenamePromptEditsDraft(t *testing.T) {
	tr, got := renamePromptTUI(t)
	renamePromptKeys(tr, "\x02r")

	renamePromptKeys(tr, "api")
	if tr.renameDraft != "mainapi" {
		t.Fatalf("after typing draft = %q", tr.renameDraft)
	}
	renamePromptKeys(tr, "\x7f")
	if tr.renameDraft != "mainap" {
		t.Fatalf("after 0x7f draft = %q", tr.renameDraft)
	}
	renamePromptKeys(tr, "\x08")
	if tr.renameDraft != "maina" {
		t.Fatalf("after 0x08 draft = %q", tr.renameDraft)
	}
	// A rune, not a byte: a wide label keeps its head intact.
	tr.renameDraft = "naïve"
	renamePromptKeys(tr, "\x7f")
	if tr.renameDraft != "naïv" {
		t.Fatalf("rune delete draft = %q, want %q", tr.renameDraft, "naïv")
	}
	renamePromptKeys(tr, "\x15")
	if tr.renameDraft != "" {
		t.Fatalf("ctrl+u draft = %q, want empty", tr.renameDraft)
	}
	noMsg(t, got)
}

// Enter sends one req with the trimmed draft and the active workspace id, and
// closes the prompt.
func TestRenamePromptEnterSendsReq(t *testing.T) {
	tr, got := renamePromptTUI(t)
	renamePromptKeys(tr, "\x02r")
	tr.renameDraft = ""
	renamePromptKeys(tr, "  api work  \r")
	if tr.mode != "" {
		t.Fatalf("mode = %q after enter, want empty", tr.mode)
	}
	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "rename-workspace" {
		t.Fatalf("sent %+v, want req rename-workspace", m)
	}
	if m.Target != "s1" {
		t.Fatalf("target = %q, want s1", m.Target)
	}
	if m.Name != "api work" {
		t.Fatalf("name = %q, want %q", m.Name, "api work")
	}
	noMsg(t, got)
}

// An empty or spaces-only draft cancels: the prompt closes and nothing is sent.
func TestRenamePromptEmptyDraftCancels(t *testing.T) {
	tr, got := renamePromptTUI(t)
	for _, keys := range []string{"\x15\r", "\x15   \r", "\x15\r"} {
		renamePromptKeys(tr, "\x02r")
		renamePromptKeys(tr, keys)
		if tr.mode != "" {
			t.Fatalf("keys %q left mode %q", keys, tr.mode)
		}
		noMsg(t, got)
	}
}

// Esc, an arrow key and a bracketed paste each close the prompt and send
// nothing. A paste never reaches the draft.
func TestRenamePromptEscArrowAndPasteCancel(t *testing.T) {
	tr, got := renamePromptTUI(t)

	// A lone esc is held for a moment as a possible paste start, like every
	// other lone escape. The ticker flushes it, and the prompt cancels on its
	// first byte instead of waiting for an arrow key's tail.
	renamePromptKeys(tr, "\x02r")
	renamePromptKeys(tr, "\x1b")
	tr.pasteDeadline = time.Now().Add(-escTimeout)
	if !tr.expirePaste(time.Now()) {
		t.Fatal("the stalled escape was not flushed")
	}
	if tr.mode != "" {
		t.Fatalf("lone esc left mode %q", tr.mode)
	}
	noMsg(t, got)

	// The arrow key is one escape sequence: it cancels on its first byte and
	// its tail never lands in the draft.
	renamePromptKeys(tr, "\x02r")
	renamePromptKeys(tr, "\x1b[A")
	if tr.mode != "" {
		t.Fatalf("arrow left mode %q", tr.mode)
	}
	if strings.Contains(tr.renameDraft, "[") || strings.Contains(tr.renameDraft, "A") {
		t.Fatalf("arrow corrupted the draft: %q", tr.renameDraft)
	}
	noMsg(t, got)

	renamePromptKeys(tr, "\x02r")
	renamePromptKeys(tr, "keep")
	renamePromptKeys(tr, "\x1b[200~pasted name\x1b[201~")
	if tr.mode != "" {
		t.Fatalf("paste left mode %q", tr.mode)
	}
	if strings.Contains(tr.renameDraft, "pasted") {
		t.Fatalf("paste reached the draft: %q", tr.renameDraft)
	}
	noMsg(t, got)
}

// No byte reaches the pane while the prompt is open, and a stray click leaves
// the draft and the prompt alone.
func TestRenamePromptShieldsPane(t *testing.T) {
	tr, got := renamePromptTUI(t)
	renamePromptKeys(tr, "\x02r")
	renamePromptKeys(tr, "hello")
	noMsg(t, got)
	renamePromptKeys(tr, "\x1b[<0;5;5M")
	if tr.mode != "rename" {
		t.Fatalf("click closed the prompt: mode %q", tr.mode)
	}
	if tr.renameDraft != "mainhello" {
		t.Fatalf("draft = %q, want %q", tr.renameDraft, "mainhello")
	}
	noMsg(t, got)
}

// renameFloatPaint draws the prompt and parses its band into the cells it
// paints, using the help box parser: a wide rune takes the cells cells gives
// it, and any move but an absolute one fails the test.
func renameFloatPaint(t *testing.T, snap *Snapshot, id, draft string, kind ...string) (Rect, helpBoxPainted) {
	t.Helper()
	box, ok := renameBoxGeometry(snap.Cols, snap.Rows, snap.Rail)
	if !ok {
		t.Fatalf("%dx%d: no box", snap.Cols, snap.Rows)
	}
	var b bytes.Buffer
	drawRename(&b, snap, id, draft, kind...)
	return box, helpBoxParse(t, b.String())
}

// renameFloatInside fails when the float painted a cell, or a wide rune's
// second cell, outside the box: the two border rows and the input row between
// them, one border column either side.
func renameFloatInside(t *testing.T, box Rect, p helpBoxPainted) {
	t.Helper()
	for cell, runes := range p.writes {
		y, x := cell[0], cell[1]
		w := cells(string(runes[len(runes)-1]))
		if y < box.Y-1 || y > box.Y+box.H || x < box.X-1 || x+w-1 > box.X+box.W {
			t.Fatalf("cell %d,%d (%d cells) is outside the box %+v", x, y, w, box)
		}
	}
}

// renameFloatRow is one row of the float as the terminal ends up, trailing
// blanks dropped.
func renameFloatRow(p helpBoxPainted, y, x, w int) string {
	return strings.TrimRight(helpBoxRowText(p, y, x, x+w-1), " ")
}

// The prompt is a float of three rows: the title names the kind and the id, the
// input row names the draft and holds the caret, and nothing lands outside it.
func TestRenamePromptDrawsTheBox(t *testing.T) {
	snap := testSnapshot(&Node{Pane: "a"}, 80, 12, nil)
	box, p := renameFloatPaint(t, snap, "s1", "main")
	if box.H != 1 {
		t.Fatalf("box height = %d, want one input row", box.H)
	}
	if box.Y+box.H > snap.Rows-3 {
		t.Fatalf("box %+v touches the toast row or the bar on %d rows", box, snap.Rows)
	}
	if box.X-1 < effectiveRail(snap.Cols, snap.Rail) {
		t.Fatalf("box %+v starts under the rail", box)
	}
	renameFloatInside(t, box, p)

	if got := renameFloatRow(p, box.Y-1, box.X-1, box.W+2); !strings.Contains(got, "rename workspace s1") {
		t.Fatalf("title row = %q, want the kind and the id", got)
	}
	if got := renameFloatRow(p, box.Y+box.H, box.X-1, box.W+2); !strings.Contains(got, "enter  esc") {
		t.Fatalf("hint row = %q, want enter and esc", got)
	}
	row := renameFloatRow(p, box.Y, box.X, box.W)
	if !strings.HasPrefix(row, " name: main") {
		t.Fatalf("input row = %q, want the label and the draft", row)
	}
	if !strings.HasSuffix(row, "▏") {
		t.Fatalf("input row = %q, want the caret last", row)
	}
	// The input row owns every cell inside the box, so no glyph of the frame
	// before shows through.
	for x := box.X; x < box.X+box.W; x++ {
		if _, ok := p.writes[[2]int{box.Y, x}]; !ok {
			t.Fatalf("input row leaves cell %d unpainted", x)
		}
	}
}

// A narrow box keeps every painted cell inside it, cuts a wide rune whole and
// keeps the caret on screen.
func TestRenamePromptDrawFitsNarrowBox(t *testing.T) {
	wide := strings.Repeat("名前", 12)
	for _, cols := range []int{80, 30, 20, 12} {
		snap := testSnapshot(&Node{Pane: "a"}, cols, 8, nil)
		box, p := renameFloatPaint(t, snap, "s1", wide)
		renameFloatInside(t, box, p)
		if got := renameFloatRow(p, box.Y, box.X, box.W); !strings.HasSuffix(got, "▏") {
			t.Fatalf("%d cols: input row = %q, want the caret last", cols, got)
		}
	}
}

// A long draft drops its head and says so, so the end of the name stays
// readable beside the caret.
func TestRenamePromptElidesLongDraftHead(t *testing.T) {
	long := strings.Repeat("a very long workspace name ", 4)
	snap := testSnapshot(&Node{Pane: "a"}, 30, 8, nil)
	box, p := renameFloatPaint(t, snap, "s1", long)
	row := renameFloatRow(p, box.Y, box.X, box.W)
	if !strings.HasPrefix(row, " name: ") {
		t.Fatalf("label lost: %q", row)
	}
	if !strings.Contains(row, "…") {
		t.Fatalf("head not elided: %q", row)
	}
	if !strings.Contains(row, "name ") {
		t.Fatalf("tail of the draft lost: %q", row)
	}
	if !strings.HasSuffix(row, "▏") {
		t.Fatalf("caret lost: %q", row)
	}
}

// A screen too small to hold the box paints nothing.
func TestRenamePromptDrawsNothingWhenTooSmall(t *testing.T) {
	for _, c := range []struct{ cols, rows int }{{1, 8}, {20, 4}} {
		snap := testSnapshot(&Node{Pane: "a"}, c.cols, c.rows, nil)
		var b bytes.Buffer
		drawRename(&b, snap, "s1", "main")
		if b.Len() != 0 {
			t.Fatalf("%dx%d: painted %q", c.cols, c.rows, b.String())
		}
	}
}

// The prompt is a paint band: a steady frame writes nothing, the band records
// the float's rows, and closing the prompt repaints the rows it covered.
func TestRenamePromptBandRepaints(t *testing.T) {
	f := newFrameTUI(t, 60, 12, 20)
	f.snap.Workspace = "s1"
	f.tui.openRenamePrompt()
	box, ok := renameBoxGeometry(f.snap.Cols, f.snap.Rows, f.snap.Rail)
	if !ok {
		t.Fatal("no box")
	}

	first := renamePromptVisible(f.frame())
	for _, want := range []string{"rename workspace s1", " name: ", "main", "enter  esc"} {
		if !strings.Contains(first, want) {
			t.Fatalf("prompt is missing %q: %q", want, first)
		}
	}
	want := []int{box.Y - 1, box.Y, box.Y + box.H}
	rows := f.tui.last.rename.rows
	if len(rows) != len(want) {
		t.Fatalf("band rows = %v, want %v", rows, want)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Fatalf("band rows = %v, want %v", rows, want)
		}
	}
	if got := f.frame(); got != "" {
		t.Fatalf("steady frame wrote %d bytes: %q", len(got), got)
	}

	// Leaving rename mode repaints whole by itself. Set the layout signature to
	// the closed one, so this frame shows what the band's own recorded rows do.
	f.tui.closeRename()
	f.tui.laysig = f.tui.layoutSig(f.snap)
	raw := f.frame()
	if strings.Contains(raw, "\x1b[2J") {
		t.Fatalf("the close frame cleared the screen: %q", raw)
	}
	if closed := renamePromptVisible(raw); strings.Contains(closed, "rename workspace") {
		t.Fatalf("closed prompt still painted: %q", closed)
	}
	for _, y := range want {
		if move := fmt.Sprintf("\x1b[%d;", y+1); !strings.Contains(raw, move) {
			t.Fatalf("row %d was not repainted: %q", y, raw)
		}
	}
	if rows := f.tui.last.rename.rows; len(rows) != 0 {
		t.Fatalf("closed band still records rows %v", rows)
	}
	if got := f.frame(); got != "" {
		t.Fatalf("steady frame after close wrote %d bytes: %q", len(got), got)
	}
}
