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

// drawRename fills one row, never past Cols, cuts the head of a long draft and
// keeps the caret. A wide rune is never split.
func TestRenamePromptDrawFitsRow(t *testing.T) {
	long := strings.Repeat("a very long workspace name ", 4)
	wide := strings.Repeat("名前", 12)

	for _, cols := range []int{80, 30, 20, 12, 1} {
		snap := testSnapshot(&Node{Pane: "a"}, cols, 8, nil)
		var b bytes.Buffer
		drawRename(&b, snap, "s1", long)
		out := b.String()
		if want := fmt.Sprintf("\x1b[%d;1H", snap.Rows-1); !strings.Contains(out, want) {
			t.Fatalf("%d cols: not on row %d: %q", cols, snap.Rows-2, out)
		}
		if !strings.Contains(out, railBG.bg()) {
			t.Fatalf("%d cols: row is not on railBG: %q", cols, out)
		}
		vis := renamePromptVisible(out)
		if n := runeCells(vis); n > cols {
			t.Fatalf("%d cols: painted %d cells: %q", cols, n, vis)
		}
		if !strings.HasSuffix(vis, "▏") && cols > 1 {
			t.Fatalf("%d cols: caret is not last: %q", cols, vis)
		}
	}

	// A long draft on a narrow row drops its head and says so.
	snap := testSnapshot(&Node{Pane: "a"}, 30, 8, nil)
	var b bytes.Buffer
	drawRename(&b, snap, "s1", long)
	vis := renamePromptVisible(b.String())
	if !strings.HasPrefix(vis, " rename workspace s1: ") {
		t.Fatalf("label lost: %q", vis)
	}
	if !strings.Contains(vis, "…") {
		t.Fatalf("head not elided: %q", vis)
	}
	if !strings.Contains(vis, "name ") {
		t.Fatalf("tail of the draft lost: %q", vis)
	}

	// A wide rune is cut whole, never half painted.
	var wb bytes.Buffer
	drawRename(&wb, snap, "s1", wide)
	if n := runeCells(renamePromptVisible(wb.String())); n > snap.Cols {
		t.Fatalf("wide draft painted %d cells, want <= %d", n, snap.Cols)
	}
}

// The prompt is a paint band: a steady frame writes nothing, the row is
// recorded, and closing the prompt repaints the row it covered.
func TestRenamePromptBandRepaints(t *testing.T) {
	f := newFrameTUI(t, 60, 12, 20)
	f.snap.Workspace = "s1"
	f.tui.openRenamePrompt()

	first := renamePromptVisible(f.frame())
	if !strings.Contains(first, " rename workspace s1: ") || !strings.Contains(first, "main") {
		t.Fatalf("prompt row not painted: %q", first)
	}
	if rows := f.tui.last.rename.rows; len(rows) != 1 || rows[0] != f.snap.Rows-2 {
		t.Fatalf("band rows = %v, want [%d]", rows, f.snap.Rows-2)
	}
	if got := f.frame(); got != "" {
		t.Fatalf("steady frame wrote %d bytes: %q", len(got), got)
	}

	f.tui.closeRename()
	raw := f.frame()
	if closed := renamePromptVisible(raw); strings.Contains(closed, "rename workspace") {
		t.Fatalf("closed prompt still painted: %q", closed)
	}
	if want := fmt.Sprintf("\x1b[%d;", f.snap.Rows-1); !strings.Contains(raw, want) {
		t.Fatalf("row %d was not repainted: %q", f.snap.Rows-2, raw)
	}
	if got := f.frame(); got != "" {
		t.Fatalf("steady frame after close wrote %d bytes: %q", len(got), got)
	}
}
