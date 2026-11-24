package client

// The ask float driven through a tui: the float opens on the focused pane's
// ask, takes its keys, and sends the answer the reader gives it.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"dm/internal/ask"
	"dm/internal/keymap"
	"dm/internal/switcher"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"
)

// askQuestions is one single-select question with a description and a preview,
// so the float has every block to draw.
func askQuestions() []wire.AskQuestion {
	return []wire.AskQuestion{{
		Question: "Which base branch?",
		Header:   "Branch",
		Options: []wire.AskOption{
			{Label: "main", Description: "the trunk", Preview: "a1b2c3\nline two"},
			{Label: "develop", Description: "integration"},
		},
	}}
}

// askTUI is a client with one ask listed on the focused pane, its float open.
func askTUI(t *testing.T, questions []wire.AskQuestion) (*tui, chan wire.Msg) {
	t.Helper()
	tui, got := switcherTUI(t)
	tui.snap.Ask = []wire.Ask{{
		ID: "ask1", Pane: "w1p1", ToolCallID: "tc1", Questions: questions,
	}}
	tui.syncAsk()
	return tui, got
}

// askAnswer read the body of an ask-answer request.
func askAnswer(t *testing.T, m wire.Msg) ask.AnswerReq {
	t.Helper()
	if m.Type != "req" || m.Action != "ask-answer" {
		t.Fatalf("request = %+v, want ask-answer", m)
	}
	var req ask.AnswerReq
	if err := json.Unmarshal(m.Res, &req); err != nil {
		t.Fatalf("answer body: %v", err)
	}
	return req
}

// askFloatScreen replays the float's bytes into a fresh screen, so a test reads
// what a terminal would show.
func askFloatScreen(t *testing.T, v *askView, cols, rows int) *vt.Term {
	t.Helper()
	var buf bytes.Buffer
	v.float.Draw(&buf, theme.DefaultTheme())
	screen := vt.NewTerm(cols, rows)
	screen.Write(buf.Bytes())
	screen.Flush()
	return screen
}

// askScreenText is every row of the float's interior, trimmed, one string.
func askScreenText(t *testing.T, v *askView, cols, rows int) string {
	t.Helper()
	screen := askFloatScreen(t, v, cols, rows)
	r := v.float.Rect
	var rowsOut []string
	for y := r.Y; y < r.Y+r.H; y++ {
		rowsOut = append(rowsOut, boxScreenRow(screen, y, r.X, r.W))
	}
	return strings.Join(rowsOut, "\n")
}

// TestAskFloatOpensOnFocusedAsk: a listed ask on the focused pane puts the
// float on the desk by itself and gives it the keyboard. A listed ask on
// another pane opens nothing, so a question elsewhere cannot steal the desk.
func TestAskFloatOpensOnFocusedAsk(t *testing.T) {
	t.Parallel()
	tui, _ := askTUI(t, askQuestions())
	if tui.ask == nil || tui.mode != "ask" {
		t.Fatalf("ask = %v, mode = %q, want an open float in ask mode", tui.ask, tui.mode)
	}
	if len(tui.floats) != 1 || tui.floats[0].ID != askFloatID {
		t.Fatalf("floats = %d, want one ask float", len(tui.floats))
	}
	if !strings.Contains(tui.ask.float.Title, "ask w1p1") {
		t.Fatalf("title = %q, want the pane named", tui.ask.float.Title)
	}

	other, _ := switcherTUI(t)
	other.snap.Ask = []wire.Ask{{
		ID: "ask2", Pane: "w1p2", ToolCallID: "tc2", Questions: askQuestions(),
	}}
	other.syncAsk()
	if other.ask != nil || len(other.floats) != 0 {
		t.Fatalf("an ask on another pane opened a float: %v", other.ask)
	}
}

// TestAskSnapshotOpensAndClosesTheFloat: the snapshot is the door. A snapshot
// that lists the focused pane's ask opens the float, and a snapshot that drops
// it closes the float, without any other call.
func TestAskSnapshotOpensAndClosesTheFloat(t *testing.T) {
	t.Parallel()
	tui, _ := switcherTUI(t)
	tui.snap.Ask = []wire.Ask{{
		ID: "ask1", Pane: "w1p1", ToolCallID: "tc1", Questions: askQuestions(),
	}}
	tui.apply(wire.Msg{Type: "snapshot", Snap: tui.snap})
	if tui.ask == nil || tui.mode != "ask" {
		t.Fatalf("the snapshot did not open the float: %v mode %q", tui.ask, tui.mode)
	}
	tui.snap.Ask = nil
	tui.apply(wire.Msg{Type: "snapshot", Snap: tui.snap})
	if tui.ask != nil || tui.mode != "" || len(tui.floats) != 0 {
		t.Fatalf("the snapshot without the ask kept the float: %v mode %q", tui.ask, tui.mode)
	}
}

// TestAskFloatArrowsMove: the arrow keys move the cursor the way j and k do.
func TestAskFloatArrowsMove(t *testing.T) {
	t.Parallel()
	tui, _ := askTUI(t, askQuestions())
	feed(tui, "\x1b[B")
	if tui.ask.sel[0] != 1 {
		t.Fatalf("down left the cursor at %d, want 1", tui.ask.sel[0])
	}
	feed(tui, "\x1b[A")
	if tui.ask.sel[0] != 0 {
		t.Fatalf("up left the cursor at %d, want 0", tui.ask.sel[0])
	}
}

// TestAskFloatDrawsQuestion: the interior carries the header, the question, the
// options with their description and preview, and the typed row.
func TestAskFloatDrawsQuestion(t *testing.T) {
	t.Parallel()
	tui, _ := askTUI(t, askQuestions())
	text := askScreenText(t, tui.ask, tui.snap.Cols, tui.snap.Rows)
	for _, want := range []string{
		"Branch", "Which base branch?", "1  main",
		"a1b2c3", "line two", "2  develop", "the trunk", "integration", "Type something.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the float does not draw %q:\n%s", want, text)
		}
	}
}

// TestAskFramePaintsTheFloat: the float reaches the frame a session draws, so
// the picture is the desk the operator sees.
func TestAskFramePaintsTheFloat(t *testing.T) {
	t.Parallel()
	tui, _ := switcherTUI(t)
	tui.snap.Ask = []wire.Ask{{
		ID: "ask1", Pane: "w1p1", ToolCallID: "tc1", Questions: askQuestions(),
	}}
	tui.apply(wire.Msg{Type: "snapshot", Snap: tui.snap})
	tui.draw()
	out := tui.out.(*bytes.Buffer).String()
	for _, want := range []string{"ask w1p1", "Which base branch?", "Type something."} {
		if !strings.Contains(out, want) {
			t.Fatalf("the frame does not paint %q", want)
		}
	}
}

// TestAskFloatDigitPicksAndSubmitSends: a digit picks an option, enter sends
// the items, and the float waits for the daemon instead of closing on hope.
func TestAskFloatDigitPicksAndSubmitSends(t *testing.T) {
	t.Parallel()
	tui, got := askTUI(t, askQuestions())
	feed(tui, "2")
	if tui.ask.pick[0] != 1 {
		t.Fatalf("pick = %d, want 1", tui.ask.pick[0])
	}
	feed(tui, "\r")
	m := nextBoxMsg(t, got)
	if m.Target != "ask1" {
		t.Fatalf("answer target = %q, want ask1", m.Target)
	}
	req := askAnswer(t, m)
	if req.Cancel || len(req.Items) != 1 || len(req.Items[0].OptionIndexes) != 1 ||
		req.Items[0].OptionIndexes[0] != 1 {
		t.Fatalf("answer = %+v, want option 1 of question 0", req)
	}
	if tui.ask == nil {
		t.Fatal("the float closed before the daemon answered")
	}
	// The daemon pushes the snapshot the answer earned: the ask it lists is
	// answered, so the float closes.
	tui.syncAsk()
	if tui.ask != nil || tui.mode != "" || len(tui.floats) != 0 {
		t.Fatalf("the answered ask kept the float: %v, mode %q", tui.ask, tui.mode)
	}
}

// TestAskFloatRefusalKeepsTheFloat: a refused answer shows the daemon's code
// and leaves the float where it was.
func TestAskFloatRefusalKeepsTheFloat(t *testing.T) {
	t.Parallel()
	tui, got := askTUI(t, askQuestions())
	feed(tui, "1\r")
	m := nextBoxMsg(t, got)
	tui.apply(wire.Msg{
		Type: "resp", Action: "ask-answer", ID: m.ID, OK: false,
		Error: "ask: invalid_answer: question 0 wants one option or one custom text",
	})
	if tui.ask == nil {
		t.Fatal("a refusal closed the float")
	}
	if !strings.Contains(tui.ask.note, "invalid_answer") {
		t.Fatalf("note = %q, want the code", tui.ask.note)
	}
	if tui.ask.pending {
		t.Fatal("a refusal left the float waiting")
	}
}

// TestAskFloatOnlyOnTheScreen: a question on a pane of another window stays in
// the rail and does not paint over the window the reader is in.
func TestAskFloatOnlyOnTheScreen(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	// w9p1 is not a pane of the active window, so it is not on screen.
	tui.snap.Ask = []wire.Ask{{
		ID: "ask9", Pane: "w9p1", ToolCallID: "tc9", Questions: askQuestions(),
	}}
	tui.syncAsk()
	if tui.ask != nil || tui.mode != "" {
		t.Fatalf("an off-screen ask opened the float: %v, mode %q", tui.ask, tui.mode)
	}
	// The focused pane of the active window still opens it.
	tui.snap.Ask = []wire.Ask{{
		ID: "ask1", Pane: "w1p1", ToolCallID: "tc1", Questions: askQuestions(),
	}}
	tui.syncAsk()
	if tui.ask == nil {
		t.Fatal("an on-screen ask did not open the float")
	}
	_ = got
}

// TestAskFloatSettledElsewhereLetsGo: an ask answered on another surface is
// refused with E_EXISTS, and that must free the desk instead of pinning a code
// the reader cannot clear. The ask stays listed, so the float must also refuse
// to reopen on the next snapshot.
func TestAskFloatSettledElsewhereLetsGo(t *testing.T) {
	t.Parallel()
	tui, got := askTUI(t, askQuestions())
	feed(tui, "1\r")
	m := nextBoxMsg(t, got)
	tui.apply(wire.Msg{
		Type: "resp", Action: "ask-answer", ID: m.ID, OK: false,
		Error: `ask: E_EXISTS: ask "a1" is already answered`,
	})
	if tui.ask != nil || tui.mode != "" {
		t.Fatalf("an ask answered elsewhere kept the float: %v, mode %q", tui.ask, tui.mode)
	}
	tui.syncAsk()
	if tui.ask != nil {
		t.Fatal("the dismissed ask reopened on the next snapshot")
	}
}

// TestAskFloatHidesAndComesBack: the toggle key takes the float off the desk,
// leaves the ask listed, gives the keys back to the pane so the reader can
// scroll, and brings the same form back with the picks it held. A snapshot that
// arrives while it is hidden does not reopen it.
func TestAskFloatHidesAndComesBack(t *testing.T) {
	t.Parallel()
	tui, got := askTUI(t, askQuestions())
	feed(tui, "1")
	if tui.ask.pick[0] != 0 {
		t.Fatalf("pick = %d, want the first option chosen", tui.ask.pick[0])
	}
	feed(tui, "A")
	if tui.ask == nil {
		t.Fatal("the hidden ask lost its view")
	}
	if tui.mode != "" {
		t.Fatalf("mode = %q, want the desk to have the keys back", tui.mode)
	}
	if tui.askHidden != "ask1" {
		t.Fatalf("askHidden = %q, want the ask remembered", tui.askHidden)
	}
	if len(tui.floats) != 0 {
		t.Fatalf("the hidden ask still draws %d floats", len(tui.floats))
	}
	// The ask is still listed, and the snapshot that lists it does not reopen
	// the float: the reader put it away on purpose.
	tui.syncAsk()
	if tui.mode != "" || len(tui.floats) != 0 {
		t.Fatalf("a snapshot reopened a hidden ask: mode %q floats %d", tui.mode, len(tui.floats))
	}
	// The desk is usable: a key now belongs to the focused pane, not the ask.
	select {
	case <-got:
	default:
	}

	// The same key brings it back, picks intact.
	tui.runAction("toggle-ask")
	if tui.ask == nil || tui.mode != "ask" || len(tui.floats) != 1 {
		t.Fatalf("the toggle did not bring the ask back: mode %q floats %d", tui.mode, len(tui.floats))
	}
	if tui.askHidden != "" {
		t.Fatalf("askHidden = %q after the ask came back", tui.askHidden)
	}
	if tui.ask.pick[0] != 0 {
		t.Fatalf("pick = %d after the ask came back, want the reader's choice kept", tui.ask.pick[0])
	}
	// The action hides it again, and the float's own A key does the same.
	tui.runAction("toggle-ask")
	if tui.mode != "" || tui.askHidden != "ask1" {
		t.Fatalf("the second toggle left mode %q hidden %q", tui.mode, tui.askHidden)
	}
}

// TestAskFloatHiddenIsOnTheStatusStrip: while the float is up the box is the
// only notice the reader needs. Once it is hidden the strip pins the pane, the
// place in the ask and the key that brings it back, so a question out of sight
// is never a question forgotten.
func TestAskFloatHiddenIsOnTheStatusStrip(t *testing.T) {
	t.Parallel()
	questions := []wire.AskQuestion{
		{Question: "One?", Header: "First", Options: []wire.AskOption{{Label: "a"}}},
		{Question: "Two?", Header: "Second", Options: []wire.AskOption{{Label: "b"}}},
	}
	tui, _ := askTUI(t, questions)
	th := theme.DefaultTheme()
	set := theme.GlyphNerd
	strip := func() string {
		badge := tui.barBadgeOf(th, set)
		badge.ask = tui.barAskOf()
		var b bytes.Buffer
		drawBar(&b, th, &wire.Snapshot{Cols: 100, Rows: 24}, false, "", set, badge)
		return b.String()
	}
	if got := strip(); strings.Contains(got, " ask ") {
		t.Fatalf("the strip names the ask while its float is up: %q", got)
	}
	feed(tui, "\t") // stand on the second question
	feed(tui, "A")
	got := strip()
	for _, want := range []string{"ask w1p1", "2/2", "A"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the strip = %q, want %q in it", got, want)
		}
	}
	// The chip is pinned: a narrow strip keeps it and gives up the left block.
	narrow := func() string {
		badge := tui.barBadgeOf(th, set)
		badge.ask = tui.barAskOf()
		var b bytes.Buffer
		drawBar(&b, th, &wire.Snapshot{Cols: 24, Rows: 24}, false, "", set, badge)
		return b.String()
	}()
	if !strings.Contains(narrow, "ask w1p1") {
		t.Fatalf("a 24-cell strip dropped the question: %q", narrow)
	}
	// Bringing it back takes the chip away again.
	tui.runAction("toggle-ask")
	if got := strip(); strings.Contains(got, " ask ") {
		t.Fatalf("the strip still names the ask after the float came back: %q", got)
	}
}

// TestAskFloatHideKeepsThePane: hiding drops the ask's claim on the keyboard
// and nothing else. No answer left the client, and the store still lists the
// question.
func TestAskFloatHideKeepsThePane(t *testing.T) {
	t.Parallel()
	tui, got := askTUI(t, askQuestions())
	feed(tui, "A")
	select {
	case m := <-got:
		t.Fatalf("hiding sent %+v", m)
	default:
	}
	if len(tui.snap.Ask) != 1 {
		t.Fatalf("the ask left the snapshot: %+v", tui.snap.Ask)
	}
	// A dismissal is final; a hide is not. Pressing A on a settled ask does
	// not resurrect it.
	tui.dismissAsk()
	tui.askHidden = "ask1"
	tui.runAction("toggle-ask")
	if tui.ask != nil {
		t.Fatal("the toggle reopened an ask the reader dismissed")
	}
}

// TestAskFloatDeclineSendsCancel: esc sends the cancel answer, the same call
// the terminal dialog makes, and the float stays until the store closes the
// ask.
func TestAskFloatDeclineSendsCancel(t *testing.T) {
	t.Parallel()
	tui, got := askTUI(t, askQuestions())
	feed(tui, "\x1b")
	m := nextBoxMsg(t, got)
	if req := askAnswer(t, m); !req.Cancel {
		t.Fatalf("answer = %+v, want cancel", req)
	}
	if tui.ask == nil {
		t.Fatal("the float closed on the send")
	}
	tui.snap.Ask = nil
	tui.syncAsk()
	if tui.ask != nil || tui.mode != "" {
		t.Fatalf("a closed ask kept the float: %v", tui.ask)
	}
}

// TestAskFloatTypeOpensThePrompt: the typed row and t both open the client's
// input prompt, and its draft lands on the question as custom text.
func TestAskFloatTypeOpensThePrompt(t *testing.T) {
	t.Parallel()
	tui, got := askTUI(t, askQuestions())
	// j twice walks past both options onto the typed row.
	feed(tui, "jj")
	if tui.ask.sel[0] != 2 {
		t.Fatalf("cursor = %d, want the typed row", tui.ask.sel[0])
	}
	feed(tui, " ")
	if tui.mode != "rename" || tui.renameKind != askPromptKind {
		t.Fatalf("mode = %q kind = %q, want the answer prompt", tui.mode, tui.renameKind)
	}
	feed(tui, "café")
	feed(tui, "\r")
	if tui.mode != "ask" {
		t.Fatalf("mode = %q, want the ask float back", tui.mode)
	}
	if tui.ask.texts[0] != "café" || tui.ask.pick[0] != -1 {
		t.Fatalf("text = %q pick = %d, want the draft and no pick", tui.ask.texts[0], tui.ask.pick[0])
	}
	feed(tui, "\r")
	req := askAnswer(t, nextBoxMsg(t, got))
	if len(req.Items) != 1 || req.Items[0].Text != "café" || len(req.Items[0].OptionIndexes) != 0 {
		t.Fatalf("answer = %+v, want the custom text alone", req)
	}
}

// TestAskFloatEnterNeedsEveryAnswer: enter before the questions are answered
// sends nothing and says why.
func TestAskFloatEnterNeedsEveryAnswer(t *testing.T) {
	t.Parallel()
	tui, got := askTUI(t, askQuestions())
	feed(tui, "\r")
	if tui.ask == nil || tui.ask.pending {
		t.Fatal("enter sent an incomplete answer")
	}
	if tui.ask.note == "" {
		t.Fatal("enter said nothing about the missing answer")
	}
	select {
	case m := <-got:
		t.Fatalf("an unanswered ask sent %+v", m)
	default:
	}
}

// TestAskFloatMultiSelectToggles: space toggles a multi-select option, digits
// pick too, and both rides the answer.
func TestAskFloatMultiSelectToggles(t *testing.T) {
	t.Parallel()
	questions := []wire.AskQuestion{{
		Question: "Which checks?", Header: "Checks", MultiSelect: true,
		Options: []wire.AskOption{{Label: "unit"}, {Label: "lint"}},
	}}
	tui, got := askTUI(t, questions)
	feed(tui, " ")
	if !tui.ask.multi[0][0] {
		t.Fatal("space did not toggle the first option")
	}
	feed(tui, "j ")
	if !tui.ask.multi[0][1] {
		t.Fatal("space did not toggle the second option")
	}
	// A multi-select question draws no typed row, so the cursor stops at the
	// last option.
	if tui.ask.choices(tui.ask.question()) != 2 {
		t.Fatalf("choices = %d, want 2", tui.ask.choices(tui.ask.question()))
	}
	feed(tui, "\r")
	req := askAnswer(t, nextBoxMsg(t, got))
	if len(req.Items) != 1 || len(req.Items[0].OptionIndexes) != 2 {
		t.Fatalf("answer = %+v, want both options", req)
	}
}

// TestAskFloatQuestionStep: tab and shift-tab move between questions and keep
// each question's cursor.
func TestAskFloatQuestionStep(t *testing.T) {
	t.Parallel()
	questions := []wire.AskQuestion{
		{Question: "One?", Header: "First", Options: []wire.AskOption{{Label: "a"}}},
		{Question: "Two?", Header: "Second", Options: []wire.AskOption{{Label: "b"}}},
	}
	tui, _ := askTUI(t, questions)
	feed(tui, "\t")
	if tui.ask.q != 1 {
		t.Fatalf("q = %d, want the second question", tui.ask.q)
	}
	if !strings.Contains(tui.ask.float.Title, "2/2") {
		t.Fatalf("title = %q, want the question count", tui.ask.float.Title)
	}
	feed(tui, "\x1b[Z")
	if tui.ask.q != 0 {
		t.Fatalf("q = %d, want the first question", tui.ask.q)
	}
}

// TestAskFloatKeysWrapToTheWidth: the key rows hold every binding a narrow float
// can take, wrapping onto as many rows as the width needs. Nothing drops, so a
// key the reader is told about is a key that is on screen.
func TestAskFloatKeysWrapToTheWidth(t *testing.T) {
	t.Parallel()
	tui, _ := askTUI(t, askQuestions())
	tui.snap.Rail = 0
	tui.snap.Cols = 44
	tui.askFit()
	text := askScreenText(t, tui.ask, tui.snap.Cols, tui.snap.Rows)
	for _, want := range []string{"j/k", "move", "J/K", "detail", "1-9", "pick",
		"space", "t", "type", "A", "hide", "n", "note", "enter", "send", "esc", "decline"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the box misses %q on a narrow float:\n%s", want, text)
		}
	}
	// The rows wrap: one row cannot hold them at 44 cells.
	if rows := tui.ask.hint().Rows(tui.ask.float.Rect.W-2, theme.DefaultTheme()); len(rows) < 2 {
		t.Fatalf("the keys fit one row at %d cells, want them wrapped", tui.ask.float.Rect.W)
	}
}

// TestAskFloatReadOnlyOffersNoAnswer: a read-only client never offers send or
// decline, and neither key sends anything.
func TestAskFloatReadOnlyOffersNoAnswer(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	tui.readOnly = true
	tui.snap.Ask = []wire.Ask{{
		ID: "ask1", Pane: "w1p1", ToolCallID: "tc1", Questions: askQuestions(),
	}}
	tui.syncAsk()
	if tui.ask == nil {
		t.Fatal("a read-only client drew no ask")
	}
	text := askScreenText(t, tui.ask, tui.snap.Cols, tui.snap.Rows)
	if strings.Contains(text, "send") || strings.Contains(text, "decline") {
		t.Fatalf("the box = %q, want no send and no decline", text)
	}
	if !strings.Contains(text, "move") || !strings.Contains(text, "pick") {
		t.Fatalf("the box = %q, want the keys a read-only session still takes", text)
	}
	feed(tui, "1\r")
	feed(tui, "\x1b")
	feed(tui, "n")
	select {
	case m := <-got:
		t.Fatalf("a read-only client sent %+v", m)
	default:
	}
	if tui.ask == nil {
		t.Fatal("esc closed a read-only ask")
	}
	if tui.mode != "ask" || tui.ask.notes[0] != "" {
		t.Fatalf("a read-only client opened the note prompt: mode %q", tui.mode)
	}
}

// TestAskFloatDefersToTheSwitcher: the palette-shaped switcher owns the keys
// without a mode, so the ask waits for it too.
func TestAskFloatDefersToTheSwitcher(t *testing.T) {
	t.Parallel()
	tui, _ := switcherTUI(t)
	tui.sw = &switcher.Switcher{}
	tui.snap.Ask = []wire.Ask{{
		ID: "ask1", Pane: "w1p1", ToolCallID: "tc1", Questions: askQuestions(),
	}}
	tui.syncAsk()
	if tui.ask != nil {
		t.Fatal("the ask float painted over an open switcher")
	}
	closeSwitcher(tui)
	if tui.ask == nil || tui.mode != "ask" {
		t.Fatalf("the ask did not open after the switcher closed: %v", tui.ask)
	}
}

// TestAskPromptCancelKeepsTheAsk: esc in the answer prompt drops the draft and
// hands the keys back to the float, which stays up.
func TestAskPromptCancelKeepsTheAsk(t *testing.T) {
	t.Parallel()
	tui, _ := askTUI(t, askQuestions())
	feed(tui, "t")
	if tui.mode != "rename" {
		t.Fatalf("mode = %q, want the answer prompt", tui.mode)
	}
	feed(tui, "abc")
	// A lone esc is held a moment as a possible paste start, the same as in
	// every other prompt. The ticker flushes it and the prompt cancels.
	feed(tui, "\x1b")
	tui.pasteDeadline = time.Now().Add(-escTimeout)
	if !tui.expirePaste(time.Now()) {
		t.Fatal("the stalled escape was not flushed")
	}
	if tui.mode != "ask" || tui.ask == nil {
		t.Fatalf("mode = %q, want the ask float back", tui.mode)
	}
	if tui.ask.texts[0] != "" {
		t.Fatalf("text = %q, want the draft dropped", tui.ask.texts[0])
	}
}

// TestAskFloatDefersToAnotherOverlay: an ask that lands while another overlay
// owns the keys opens once that overlay closes, so it never yanks the desk.
func TestAskFloatDefersToAnotherOverlay(t *testing.T) {
	t.Parallel()
	tui, _ := switcherTUI(t)
	tui.mode = "layout"
	tui.snap.Ask = []wire.Ask{{
		ID: "ask1", Pane: "w1p1", ToolCallID: "tc1", Questions: askQuestions(),
	}}
	tui.syncAsk()
	if tui.ask != nil {
		t.Fatal("the ask float took the desk from an open overlay")
	}
	if !tui.askDeferred {
		t.Fatal("the ask was not held for a later open")
	}
	closeLayoutSwitcher(tui)
	if tui.ask == nil || tui.mode != "ask" {
		t.Fatalf("the ask did not open after the overlay closed: %v", tui.ask)
	}
}

// askSizedTUI is askTUI on a screen of the given width, refitted, so a test
// picks the wide layout or the narrow one by the width it asks for.
func askSizedTUI(t *testing.T, questions []wire.AskQuestion, cols int) (*tui, chan wire.Msg) {
	t.Helper()
	tui, got := askTUI(t, questions)
	tui.snap.Cols = cols
	tui.askFit()
	return tui, got
}

// askBorderRow is the ask float's bottom border: the row the keys ride.
func askBorderRow(t *testing.T, v *askView, cols, rows int) string {
	t.Helper()
	screen := askFloatScreen(t, v, cols, rows)
	r := v.float.Rect
	return boxScreenRow(screen, r.Y+r.H, r.X-1, r.W+2)
}

// askPanes reads the float's interior as three blocks: the option column, the
// context column and the rule between them. leftW is the option column's width;
// the rule is the cell after it.
func askPanes(t *testing.T, v *askView, cols, rows, leftW int) (left, right, rule string) {
	t.Helper()
	screen := askFloatScreen(t, v, cols, rows)
	r := v.float.Rect
	var l, g, o strings.Builder
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			ch := switcherCell(screen, x, y)
			if ch == 0 {
				ch = ' '
			}
			switch {
			case x-r.X < leftW:
				l.WriteRune(ch)
			case x-r.X == leftW:
				g.WriteRune(ch)
			default:
				o.WriteRune(ch)
			}
		}
		l.WriteString("\n")
		g.WriteString("\n")
		o.WriteString("\n")
	}
	return l.String(), o.String(), g.String()
}

// TestAskFloatWideDrawsBothPanes: a float of askWideMin or more splits into the
// option column and a context column behind a vertical rule. The right pane
// carries the hovered option's label, description and preview; the left one
// carries no preview at all.
func TestAskFloatWideDrawsBothPanes(t *testing.T) {
	t.Parallel()
	tui, _ := askSizedTUI(t, askQuestions(), 140)
	v := tui.ask
	r := v.float.Rect
	if r.W < askWideMin {
		t.Fatalf("float width = %d, want at least %d for the wide layout", r.W, askWideMin)
	}
	sh := v.shape(r.W)
	if sh.rightW <= 0 {
		t.Fatalf("shape(%d) = %+v, want a context column", r.W, sh)
	}
	if sh.rightW < askContextMin {
		t.Fatalf("context column = %d, want at least %d", sh.rightW, askContextMin)
	}
	left, right, rule := askPanes(t, v, tui.snap.Cols, tui.snap.Rows, sh.leftW)
	for _, want := range []string{"Which base branch?", "1  main", "2  develop"} {
		if !strings.Contains(left, want) {
			t.Fatalf("the option column does not draw %q:\n%s", want, left)
		}
	}
	if strings.Contains(left, "the trunk") {
		t.Fatalf("the option column draws the description in the wide layout:\n%s", left)
	}
	if strings.Contains(left, "a1b2c3") {
		t.Fatalf("the option column draws the preview in the wide layout:\n%s", left)
	}
	for _, want := range []string{"main", "the trunk", "a1b2c3", "line two"} {
		if !strings.Contains(right, want) {
			t.Fatalf("the context column does not draw %q:\n%s", want, right)
		}
	}
	glyph := theme.DefaultTheme().Glyphs.BorderVertical
	ruled, gap := 0, 0
	for _, row := range strings.Split(rule, "\n") {
		switch row {
		case glyph:
			if gap > 0 {
				t.Fatalf("the rule between the panes has a hole in it")
			}
			ruled++
		default:
			if ruled > 0 {
				gap++
			}
		}
	}
	if ruled < 4 {
		t.Fatalf("the rule between the panes covers %d rows, want the pane's rows", ruled)
	}

	// The cursor on the option with no preview keeps the column and says so,
	// so the box does not jump width as the cursor moves.
	feed(tui, "j")
	if v.float.Rect.W != r.W {
		t.Fatalf("the float moved width to %d when the cursor moved", v.float.Rect.W)
	}
	_, right, _ = askPanes(t, v, tui.snap.Cols, tui.snap.Rows, sh.leftW)
	for _, want := range []string{"develop", "integration", "no preview"} {
		if !strings.Contains(right, want) {
			t.Fatalf("the context column does not draw %q for an option with no preview:\n%s", want, right)
		}
	}
}

// TestAskFloatChromeIsABandAndABorder: the box reads top to bottom as the
// status band, a rule, the question, a rule, the list beside the help pane, a
// closing rule and the key rows. The keys take whole interior rows, so every
// binding is painted whatever the box width, and the border stays a plain rule.
func TestAskFloatChromeIsABandAndABorder(t *testing.T) {
	t.Parallel()
	tui, _ := askSizedTUI(t, askQuestions(), 140)
	text := askScreenText(t, tui.ask, tui.snap.Cols, tui.snap.Rows)
	t.Logf("the ask float:\n%s", text)
	rows := strings.Split(text, "\n")
	if len(rows) < 7 {
		t.Fatalf("the float draws %d rows, want the band, the question, the list and the tail", len(rows))
	}
	if !strings.Contains(rows[0], "Branch") || !strings.Contains(rows[0], "1 of 1") {
		t.Fatalf("the first row = %q, want the header and the count on one band", rows[0])
	}
	rule := strings.Repeat(theme.DefaultTheme().Glyphs.BorderHorizontal, 8)
	if !strings.Contains(rows[1], rule) {
		t.Fatalf("the second row = %q, want the rule under the band", rows[1])
	}
	// The closing rule sits above the key rows, which end the interior.
	closing := -1
	for i, row := range rows {
		if strings.Contains(row, rule) {
			closing = i
		}
	}
	if closing < 0 {
		t.Fatalf("no closing rule in the box:\n%s", text)
	}
	footer := strings.Join(rows[closing+1:], "\n")
	for _, want := range []string{" j/k ", " enter ", " esc "} {
		if !strings.Contains(footer, want) {
			t.Fatalf("the key rows = %q, want the cap %q", footer, want)
		}
	}
	if strings.Contains(footer, rule) {
		t.Fatalf("the key rows = %q, want them under the closing rule", footer)
	}
	// The border is a plain rule: no hint rides it any more.
	r := tui.ask.float.Rect
	screen := askFloatScreen(t, tui.ask, tui.snap.Cols, tui.snap.Rows)
	border := boxScreenRow(screen, r.Y+r.H, r.X-1, r.W+2)
	if strings.Contains(border, "j/k") {
		t.Fatalf("the bottom border = %q, want a plain rule", border)
	}
}

// TestAskFloatOneSizeForTheWholeAsk: the box reserves its height from the
// tallest question and the tallest help it can show, so a tab or a cursor move
// never resizes it, and the count is drawn on one band only.
func TestAskFloatOneSizeForTheWholeAsk(t *testing.T) {
	t.Parallel()
	questions := []wire.AskQuestion{
		{Question: "Short?", Header: "One", Options: []wire.AskOption{{Label: "a"}}},
		{Question: "Long?", Header: "Two", Options: []wire.AskOption{
			{Label: "b", Description: "a description", Preview: "line one\nline two"},
			{Label: "c", Description: "another description"},
			{Label: "d"},
		}},
	}
	tui, _ := askSizedTUI(t, questions, 140)
	// A screen tall enough for the floor, so the screen is not the cap under
	// test here.
	tui.snap.Rows = 60
	tui.askFit()
	h := tui.ask.float.Rect.H
	if floor := tui.km().AskRows(); h < floor {
		t.Fatalf("float height = %d, want at least the reserved %d", h, floor)
	}
	check := func(what string) {
		t.Helper()
		if got := tui.ask.float.Rect.H; got != h {
			t.Fatalf("%s resized the box: %d, want %d", what, got, h)
		}
	}
	for range 3 {
		feed(tui, "j")
		check("a cursor move")
	}
	feed(tui, "\t")
	check("tab")
	text := askScreenText(t, tui.ask, tui.snap.Cols, tui.snap.Rows)
	t.Logf("the second question:\n%s", text)
	if n := strings.Count(text, "2 of 2"); n != 1 {
		t.Fatalf("the count appears %d times on the second question, want one:\n%s", n, text)
	}
	if strings.Contains(text, "Two") && strings.Count(text, "Two") != 1 {
		// The header names the question once, in its tab.
		t.Fatalf("the header is repeated outside its tab:\n%s", text)
	}
	feed(tui, "\t")
	check("tab back")
	if n := strings.Count(askScreenText(t, tui.ask, tui.snap.Cols, tui.snap.Rows), "1 of 2"); n != 1 {
		t.Fatalf("the count appears %d times on the first question, want one", n)
	}
}

// TestAskFloatSingleSelectShowsItsPick: a single-select option wears its mark
// like a multi-select one, so a pick left behind the cursor still reads as a
// pick. The band says where the cursor is; the box says what is chosen.
func TestAskFloatSingleSelectShowsItsPick(t *testing.T) {
	t.Parallel()
	tui, _ := askTUI(t, askQuestions())
	text := func() string { return askScreenText(t, tui.ask, tui.snap.Cols, tui.snap.Rows) }
	if got := text(); !strings.Contains(got, "[ ] 1  main") || !strings.Contains(got, "[ ] 2  develop") {
		t.Fatalf("an unpicked single select does not show its empty marks:\n%s", got)
	}
	feed(tui, "2")
	got := text()
	t.Logf("after picking the second option:\n%s", got)
	if !strings.Contains(got, "[●] 2  develop") {
		t.Fatalf("the pick does not show a filled mark:\n%s", got)
	}
	if strings.Contains(got, "[●] 1  main") {
		t.Fatalf("a pick leaked onto the other row:\n%s", got)
	}
	// The cursor can leave the pick without changing it.
	feed(tui, "k")
	got = text()
	if !strings.Contains(got, "[●] 2  develop") {
		t.Fatalf("moving the cursor dropped the pick:\n%s", got)
	}
	if !strings.Contains(got, "[ ] 1  main") {
		t.Fatalf("the cursor row lost its empty mark:\n%s", got)
	}
}

// TestAskFloatPickWearsTheSettledColour: a chosen option is coloured, not just
// boxed, so a pick reads as an answer at a glance. The cursor keeps the text
// colour, so the two states never look the same.
func TestAskFloatPickWearsTheSettledColour(t *testing.T) {
	t.Parallel()
	th := theme.DefaultTheme()
	tui, _ := askTUI(t, askQuestions())
	feed(tui, "2")
	lines := tui.ask.listLines(th, 60, false)
	fg := func(i int) (box, label theme.RGB) {
		t.Helper()
		for _, ln := range lines {
			if ln.choice != i {
				continue
			}
			return ln.segs[1].FG, ln.segs[2].FG
		}
		t.Fatalf("no row for option %d", i)
		return theme.RGB{}, theme.RGB{}
	}
	if box, label := fg(1); box != th.Palette.RailDiffAdd || label != th.Palette.RailDiffAdd {
		t.Fatalf("the pick is not in the settled colour: box %v label %v", box, label)
	}
	// The colour stays while the cursor stands on the pick; the weight is what
	// the cursor adds.
	for _, ln := range lines {
		if ln.choice == 1 && !ln.segs[2].Bold {
			t.Fatal("the cursor is on the pick and the label is not bold")
		}
	}
	if box, label := fg(0); box != th.Palette.RailDim || label != th.Palette.RailDim {
		t.Fatalf("the unpicked row carries the pick's colour: box %v label %v", box, label)
	}
	// The cursor alone does not take the settled colour: move onto the empty
	// row and it reads as the cursor, not as an answer.
	feed(tui, "k")
	lines = tui.ask.listLines(th, 60, false)
	for _, ln := range lines {
		if ln.choice != 0 {
			continue
		}
		if ln.segs[1].FG != th.Palette.RailDim {
			t.Fatalf("the cursor row's mark took the pick colour: %v", ln.segs[1].FG)
		}
		if ln.segs[2].FG != th.Palette.RailText {
			t.Fatalf("the cursor row's label is not the text colour: %v", ln.segs[2].FG)
		}
	}
}

// TestAskFloatLongListKeepsTheKeys: a list taller than the screen windows under
// a border that stays put, so the keys and the count are never the rows that
// get cut. The cursor's row rides the window.
func TestAskFloatLongListKeepsTheFooter(t *testing.T) {
	t.Parallel()
	var options []wire.AskOption
	for i := 1; i <= 14; i++ {
		options = append(options, wire.AskOption{Label: fmt.Sprintf("option %02d", i)})
	}
	tui, _ := askTUI(t, []wire.AskQuestion{{
		Question: "Which one?", Header: "Pick", Options: options,
	}})
	tui.snap.Cols, tui.snap.Rows = 100, 18
	tui.askFit()
	r := tui.ask.float.Rect
	if r.Y+r.H > tui.snap.Rows {
		t.Fatalf("the float reaches row %d on a %d-row screen", r.Y+r.H, tui.snap.Rows)
	}
	last := func() string {
		rows := strings.Split(askScreenText(t, tui.ask, tui.snap.Cols, tui.snap.Rows), "\n")
		return strings.TrimSpace(rows[len(rows)-1])
	}
	keys := func() string {
		return askScreenText(t, tui.ask, tui.snap.Cols, tui.snap.Rows)
	}
	if text := keys(); !strings.Contains(text, "enter") || !strings.Contains(text, "decline") {
		t.Fatalf("the box = %q, want the keys on its last rows", text)
	}
	if foot := last(); strings.Contains(foot, "enter") || strings.Contains(foot, "send") {
		t.Fatalf("the last interior row = %q, want a key row", foot)
	}
	for range 13 {
		feed(tui, "j")
	}
	if !strings.Contains(askScreenText(t, tui.ask, tui.snap.Cols, tui.snap.Rows), "option 14") {
		t.Fatalf("the cursor row left the window")
	}
	if text := keys(); !strings.Contains(text, "enter") || !strings.Contains(text, "j/k") {
		t.Fatalf("the box = %q after the list scrolled, want its keys in place", text)
	}
}

// TestAskFloatHelpPaneFillsTheFloor: the box reserves the configured floor and
// the help pane takes every row it leaves, so a long preview reads in the tall
// box instead of sitting beside blank rows. A preview that still does not fit
// ends on the count of rows it hid.
func TestAskFloatHelpPaneFillsTheFloor(t *testing.T) {
	t.Parallel()
	tui, _ := askSizedTUI(t, []wire.AskQuestion{{
		Question: "Pick one", Header: "Pick",
		Options: []wire.AskOption{
			{Label: "alpha", Description: "the first", Preview: askLongPreview(60)},
			{Label: "beta"},
		},
	}}, 140)
	// A screen tall enough for the floor, so the floor is what gets drawn.
	tui.snap.Rows = 60
	tui.askFit()
	v := tui.ask
	th := theme.DefaultTheme()
	floor := v.floor()
	if got := v.float.Rect.H; got != floor {
		t.Fatalf("float height = %d, want the floor %d", got, floor)
	}
	w := v.float.Rect.W
	room := floor - len(v.headLines(th, w)) - len(v.questionLines(th, w)) - len(v.tailLines(th, w))
	if room <= len(v.listLines(th, v.shape(w).leftW, false)) {
		t.Fatalf("the floor leaves %d rows for the detail, no more than the list", room)
	}
	if help := v.helpLines(th, v.shape(w).rightW, room); len(help) != room {
		t.Fatalf("the help pane carries %d rows, want the %d the floor leaves", len(help), room)
	}
	text := askScreenText(t, v, tui.snap.Cols, tui.snap.Rows)
	if !strings.Contains(text, "more lines") {
		t.Fatalf("the help pane hides the preview without a count:\n%s", text)
	}
	if strings.Contains(text, "line 60") {
		t.Fatalf("the help pane drew past the floor:\n%s", text)
	}
}

// TestAskFloatOptionsOutrankTheFloor: the floor is a floor, not a cap. A
// question with more options than the floor has rows grows the box and keeps
// every option row, so the detail's budget never cuts the list.
func TestAskFloatOptionsOutrankTheFloor(t *testing.T) {
	t.Parallel()
	var options []wire.AskOption
	for i := 1; i <= 26; i++ {
		options = append(options, wire.AskOption{Label: fmt.Sprintf("option %02d", i)})
	}
	tui, _ := askTUI(t, []wire.AskQuestion{{
		Question: "Which one?", Header: "Pick", Options: options,
	}})
	tui.snap.Cols, tui.snap.Rows = 140, 60
	tui.askFit()
	floor := tui.ask.floor()
	if got := tui.ask.float.Rect.H; got <= floor {
		t.Fatalf("float height = %d, want more than the floor %d for 26 options", got, floor)
	}
	if got := askScreenText(t, tui.ask, tui.snap.Cols, tui.snap.Rows); !strings.Contains(got, "option 26") {
		t.Fatalf("the last option is not on the first screen:\n%s", got)
	}
}

// TestAskFloatRowsFollowTheConfig: ask-rows sets the floor, and a taller floor
// shows more of a long preview, so the box's height is the operator's choice.
func TestAskFloatRowsFollowTheConfig(t *testing.T) {
	t.Parallel()
	questions := []wire.AskQuestion{{
		Question: "Pick one", Header: "Pick",
		Options: []wire.AskOption{{Label: "alpha", Preview: askLongPreview(60)}},
	}}
	seen := func(src string) (height int, text string) {
		tui, _ := askSizedTUI(t, questions, 140)
		// A tall screen, so the box shows the floor rather than the screen's.
		tui.snap.Rows = 60
		tui.askFit()
		cfg, err := keymap.ParseConfig(src)
		if err != nil {
			t.Fatalf("ParseConfig: %v", err)
		}
		tui.keymap = cfg
		// A reload lands on the open float through the next snapshot.
		tui.syncAsk()
		return tui.ask.float.Rect.H, askScreenText(t, tui.ask, tui.snap.Cols, tui.snap.Rows)
	}
	shortH, shortText := seen(`ask-rows 10`)
	tallH, tallText := seen(`ask-rows 30`)
	if shortH != 10 || tallH != 30 {
		t.Fatalf("float heights = %d and %d, want 10 and 30", shortH, tallH)
	}
	count := func(s string) int { return strings.Count(s, "line ") }
	if count(tallText) <= count(shortText) {
		t.Fatalf("the taller box shows %d preview rows, the shorter %d", count(tallText), count(shortText))
	}
}

// askLongPreview is n numbered preview rows, one line each.
func askLongPreview(n int) string {
	var lines []string
	for i := 1; i <= n; i++ {
		lines = append(lines, fmt.Sprintf("line %02d", i))
	}
	return strings.Join(lines, "\n")
}

// TestAskFloatDetailScrollsTheWidePane: a preview the help pane cannot hold is
// reachable with J and K instead of lost, and the box keeps its height while
// the pane scrolls.
func TestAskFloatDetailScrollsTheWidePane(t *testing.T) {
	t.Parallel()
	tui, _ := askSizedTUI(t, []wire.AskQuestion{{
		Question: "Pick one", Header: "Pick",
		Options: []wire.AskOption{{Label: "alpha", Description: "the first", Preview: askLongPreview(20)}},
	}}, 140)
	v := tui.ask
	if sh := v.shape(v.float.Rect.W); sh.rightW == 0 {
		t.Fatalf("float width %d, want the wide layout", v.float.Rect.W)
	}
	start := v.float.Rect
	text := func() string { return askScreenText(t, v, tui.snap.Cols, tui.snap.Rows) }
	if !strings.Contains(text(), "more lines") {
		t.Fatalf("the pane does not count the rows it hides:\n%s", text())
	}
	if strings.Contains(text(), "line 20") {
		t.Fatalf("the whole preview fitted, so there is nothing to scroll:\n%s", text())
	}
	if got := text(); !strings.Contains(got, "detail") {
		t.Fatalf("the key rows = %q, want the key that scrolls the detail", got)
	}
	feed(tui, "J")
	if v.helpTop != 1 {
		t.Fatalf("helpTop = %d after one J, want 1", v.helpTop)
	}
	if got := text(); !strings.Contains(got, "1 above") {
		t.Fatalf("the pane does not count the row it scrolled past:\n%s", got)
	}
	if v.float.Rect != start {
		t.Fatalf("the float resized under the scroll: %+v, want %+v", v.float.Rect, start)
	}
	for range 40 {
		feed(tui, "J")
	}
	if got := text(); !strings.Contains(got, "line 20") {
		t.Fatalf("the last row of the preview is not reachable:\n%s", got)
	}
	if got := text(); !strings.Contains(got, "more lines above") {
		t.Fatalf("the last window does not count the rows above it:\n%s", got)
	}
	if v.float.Rect != start {
		t.Fatalf("the float resized at the end of the scroll: %+v, want %+v", v.float.Rect, start)
	}
	for range 40 {
		feed(tui, "K")
	}
	if v.helpTop != 0 {
		t.Fatalf("helpTop = %d after K past the top, want 0", v.helpTop)
	}
	if got := text(); !strings.Contains(got, "line 01") {
		t.Fatalf("the first row of the preview is not back:\n%s", got)
	}
}

// TestAskFloatDetailScrollsTheNarrowPreview: the narrow float's preview block
// scrolls under the same keys, so neither layout loses the rows it hides.
func TestAskFloatDetailScrollsTheNarrowPreview(t *testing.T) {
	t.Parallel()
	tui, _ := askSizedTUI(t, []wire.AskQuestion{{
		Question: "Pick one", Header: "Pick",
		Options: []wire.AskOption{{Label: "alpha", Preview: askLongPreview(40)}},
	}}, 100)
	v := tui.ask
	if sh := v.shape(v.float.Rect.W); sh.rightW != 0 {
		t.Fatalf("float width %d, want the narrow layout", v.float.Rect.W)
	}
	start := v.float.Rect
	text := func() string { return askScreenText(t, v, tui.snap.Cols, tui.snap.Rows) }
	if !strings.Contains(text(), "more lines") {
		t.Fatalf("the block does not count the rows it hides:\n%s", text())
	}
	feed(tui, "J")
	if got := text(); !strings.Contains(got, "line 02") || !strings.Contains(got, "1 above") {
		t.Fatalf("the block did not scroll under J:\n%s", got)
	}
	if v.float.Rect != start {
		t.Fatalf("the float resized under the scroll: %+v, want %+v", v.float.Rect, start)
	}
	for range 40 {
		feed(tui, "J")
	}
	if got := text(); !strings.Contains(got, "line 40") {
		t.Fatalf("the last row of the preview is not reachable:\n%s", got)
	}
	if v.float.Rect != start {
		t.Fatalf("the float resized at the end of the scroll: %+v, want %+v", v.float.Rect, start)
	}
	for range 40 {
		feed(tui, "K")
	}
	if got := text(); !strings.Contains(got, "line 01") {
		t.Fatalf("the first row of the preview is not back:\n%s", got)
	}
}

// TestAskFloatDetailStartsAtTheOptionUnderTheCursor: the scroll belongs to the
// option it was made on, so a cursor move puts the pane back to the top of the
// next option's detail.
func TestAskFloatDetailStartsAtTheOptionUnderTheCursor(t *testing.T) {
	t.Parallel()
	tui, _ := askSizedTUI(t, []wire.AskQuestion{{
		Question: "Pick one", Header: "Pick",
		Options: []wire.AskOption{
			{Label: "alpha", Preview: askLongPreview(20)},
			{Label: "beta"},
		},
	}}, 140)
	feed(tui, "J")
	if tui.ask.helpTop != 1 {
		t.Fatalf("helpTop = %d after one J, want 1", tui.ask.helpTop)
	}
	feed(tui, "j")
	if tui.ask.helpTop != 0 {
		t.Fatalf("helpTop = %d on the next option, want 0", tui.ask.helpTop)
	}
	if got := askScreenText(t, tui.ask, tui.snap.Cols, tui.snap.Rows); !strings.Contains(got, "no preview") {
		t.Fatalf("the pane does not read the option under the cursor:\n%s", got)
	}
	feed(tui, "k")
	if strings.Contains(askScreenText(t, tui.ask, tui.snap.Cols, tui.snap.Rows), "1 above") {
		t.Fatalf("the scroll survived the cursor coming back")
	}
}

// TestAskFloatSplitFollowsTheConfig: the detail column takes ask-split of the
// box, 60% by default, and the options keep their floor beside it.
func TestAskFloatSplitFollowsTheConfig(t *testing.T) {
	t.Parallel()
	questions := []wire.AskQuestion{{
		Question: "Pick one", Header: "Pick",
		Options: []wire.AskOption{{Label: "alpha", Description: "the first"}},
	}}
	shapeAt := func(src string, cols int) askShape {
		tui, _ := askTUI(t, questions)
		tui.snap.Cols, tui.snap.Rows = cols, 40
		cfg, err := keymap.ParseConfig(src)
		if err != nil {
			t.Fatalf("ParseConfig: %v", err)
		}
		tui.keymap = cfg
		// A reload lands on the open float through the next snapshot.
		tui.syncAsk()
		return tui.ask.shape(tui.ask.float.Rect.W)
	}
	// 96 columns: the detail takes 57 and the options 38, the mockup's split.
	if got := shapeAt(`prefix "ctrl+b"`, 200); got.rightW != 57 || got.leftW != 38 {
		t.Fatalf("shape = %+v, want the detail 57 and the options 38", got)
	}
	if got := shapeAt(`ask-split 70`, 200); got.rightW != 67 || got.leftW != 28 {
		t.Fatalf("shape = %+v at ask-split 70, want the detail 67 and the options 28", got)
	}
	// The option column keeps its floor however far the detail is pushed.
	if got := shapeAt(`ask-split 80`, 200); got.leftW < askQuestionMin {
		t.Fatalf("shape = %+v, want the options at least %d cells", got, askQuestionMin)
	}
	// And the detail keeps its own floor when the box is narrow.
	if got := shapeAt("ask-cols 90\nask-split 20", 200); got.rightW != askContextMin {
		t.Fatalf("shape = %+v, want the detail on its floor %d", got, askContextMin)
	}
}

// TestAskFloatNarrowKeepsOneColumn: below askWideMin the float keeps today's
// layout, the preview under its option in one column.
func TestAskFloatNarrowKeepsOneColumn(t *testing.T) {
	t.Parallel()
	tui, _ := askSizedTUI(t, askQuestions(), 100)
	v := tui.ask
	r := v.float.Rect
	if r.W >= askWideMin {
		t.Fatalf("float width = %d, want the narrow layout", r.W)
	}
	if sh := v.shape(r.W); sh.rightW != 0 {
		t.Fatalf("shape(%d) = %+v, want no context column", r.W, sh)
	}
	text := askScreenText(t, v, tui.snap.Cols, tui.snap.Rows)
	for _, want := range []string{"1  main", "the trunk", "2  develop", "integration"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the narrow float does not draw %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "a1b2c3") || !strings.Contains(text, "line two") {
		t.Fatalf("the narrow float does not draw the preview under the list:\n%s", text)
	}
	// The preview sits after the option it belongs to, not in a pane of its
	// own.
	if strings.Index(text, "a1b2c3") < strings.Index(text, "the trunk") {
		t.Fatalf("the preview is drawn above the option it belongs to:\n%s", text)
	}
}

// TestAskFloatColsFollowTheConfig: ask-cols sets the width the float may take,
// so a wide terminal reads a wide detail column, and the default keeps the
// message box's 96-column line length.
func TestAskFloatColsFollowTheConfig(t *testing.T) {
	t.Parallel()
	questions := []wire.AskQuestion{{
		Question: "Pick one", Header: "Pick",
		Options: []wire.AskOption{{Label: "alpha", Description: "the first", Preview: askLongPreview(20)}},
	}}
	width := func(src string) int {
		tui, _ := askTUI(t, questions)
		tui.snap.Cols, tui.snap.Rows = 220, 60
		cfg, err := keymap.ParseConfig(src)
		if err != nil {
			t.Fatalf("ParseConfig: %v", err)
		}
		tui.keymap = cfg
		// A reload lands on the open float through the next snapshot.
		tui.syncAsk()
		return tui.ask.float.Rect.W
	}
	if got := width(`prefix "ctrl+b"`); got != 96 {
		t.Fatalf("default float width = %d, want the 96-column cap", got)
	}
	if got := width(`ask-cols 160`); got != 160 {
		t.Fatalf("float width = %d, want ask-cols 160", got)
	}
	// The screen is the last word: six columns stay clear of its edge, and the
	// rail's 34 columns are not the box's to take.
	if got := width(`ask-cols 240`); got != 220-34-6 {
		t.Fatalf("float width = %d, want the screen and rail less six columns", got)
	}
}

// askTabText is the tab strip as one string.
func askTabText(v *askView, th theme.Theme) string {
	var b strings.Builder
	for _, s := range v.tabRow(th, 1<<8) {
		b.WriteString(s.Text)
	}
	return b.String()
}

// TestAskFloatTabsStepAndMarkAnswered: tab and shift-tab move between the
// questions and wrap. A question with an answer keeps a filled marker in its
// tab, so a form's progress reads at a glance.
func TestAskFloatTabsStepAndMarkAnswered(t *testing.T) {
	t.Parallel()
	questions := []wire.AskQuestion{
		{Question: "One?", Header: "First", Options: []wire.AskOption{{Label: "a"}}},
		{Question: "Two?", Header: "Second", Options: []wire.AskOption{{Label: "b"}}},
	}
	tui, _ := askSizedTUI(t, questions, 100)
	feed(tui, "\t")
	if tui.ask.q != 1 {
		t.Fatalf("tab left q = %d, want the second question", tui.ask.q)
	}
	feed(tui, "\t")
	if tui.ask.q != 0 {
		t.Fatalf("tab did not wrap: q = %d", tui.ask.q)
	}
	feed(tui, "\x1b[Z")
	if tui.ask.q != 1 {
		t.Fatalf("shift-tab from the first question did not wrap back: q = %d", tui.ask.q)
	}

	// The tab strip is the float's first interior row.
	screen := askFloatScreen(t, tui.ask, tui.snap.Cols, tui.snap.Rows)
	r := tui.ask.float.Rect
	top := boxScreenRow(screen, r.Y, r.X, r.W)
	if !strings.Contains(top, "First") || !strings.Contains(top, "Second") {
		t.Fatalf("the tab strip is not the top row: %q", top)
	}

	th := theme.DefaultTheme()
	dirty := th.Glyphs.Dirty
	if strings.Contains(askTabText(tui.ask, th), dirty) {
		t.Fatalf("an unanswered form marks a tab as filled: %q", askTabText(tui.ask, th))
	}
	feed(tui, "1")
	if !tui.ask.answered(1) {
		t.Fatal("the pick did not answer the question")
	}
	if n := strings.Count(askTabText(tui.ask, th), dirty); n != 1 {
		t.Fatalf("filled tabs = %d, want one for the answered question: %q", n, askTabText(tui.ask, th))
	}
}

// TestAskFloatNoteAttachesEditsAndClears: the note key opens the input prompt
// on the option under the cursor, the draft lands on the question's answer
// item, a second press edits it, and an empty draft clears it.
func TestAskFloatNoteAttachesEditsAndClears(t *testing.T) {
	t.Parallel()
	tui, _ := askSizedTUI(t, askQuestions(), 100)
	feed(tui, "j")
	if tui.ask.sel[0] != 1 {
		t.Fatalf("cursor = %d, want the option with no preview", tui.ask.sel[0])
	}
	feed(tui, "n")
	if tui.mode != "rename" || tui.renameKind != askNoteKind {
		t.Fatalf("mode = %q kind = %q, want the note prompt", tui.mode, tui.renameKind)
	}
	feed(tui, "needs a rebase")
	feed(tui, "\r")
	if tui.mode != "ask" {
		t.Fatalf("mode = %q, want the ask float back", tui.mode)
	}
	if tui.ask.notes[0] != "needs a rebase" || tui.ask.noteRow[0] != 1 {
		t.Fatalf("note = %q row = %d, want the note on the option under the cursor",
			tui.ask.notes[0], tui.ask.noteRow[0])
	}
	text := askScreenText(t, tui.ask, tui.snap.Cols, tui.snap.Rows)
	noteAt, rowAt := strings.Index(text, "note: needs a rebase"), strings.Index(text, "2  develop")
	if noteAt < 0 {
		t.Fatalf("the float does not draw the note:\n%s", text)
	}
	if noteAt < rowAt {
		t.Fatalf("the note is drawn above the row it belongs to:\n%s", text)
	}
	if line := strings.TrimSpace(strings.Split(text[noteAt:], "\n")[0]); line != "note: needs a rebase" {
		t.Fatalf("the note row = %q, want it on its own row", line)
	}

	// A second press edits the note it already carries.
	feed(tui, "n")
	if tui.renameDraft.String() != "needs a rebase" {
		t.Fatalf("draft = %q, want the note under edit", tui.renameDraft.String())
	}
	feed(tui, "!")
	feed(tui, "\r")
	if tui.ask.notes[0] != "needs a rebase!" {
		t.Fatalf("note = %q, want the edit landed", tui.ask.notes[0])
	}

	// An empty draft clears it, and the row goes with it.
	feed(tui, "n")
	for range "needs a rebase!" {
		feed(tui, "\x7f")
	}
	feed(tui, "\r")
	if tui.ask.notes[0] != "" || tui.ask.noteRow[0] != -1 {
		t.Fatalf("note = %q row = %d, want it cleared", tui.ask.notes[0], tui.ask.noteRow[0])
	}
	if text := askScreenText(t, tui.ask, tui.snap.Cols, tui.snap.Rows); strings.Contains(text, "note: ") {
		t.Fatalf("the cleared note is still drawn:\n%s", text)
	}
}

// TestAskFloatNoteRidesTheAnswer: the note travels in the answer item the
// daemon already accepts, beside the option the reader picked.
func TestAskFloatNoteRidesTheAnswer(t *testing.T) {
	t.Parallel()
	tui, got := askSizedTUI(t, askQuestions(), 100)
	feed(tui, "j")
	feed(tui, "n")
	feed(tui, "keep it")
	feed(tui, "\r")
	feed(tui, "2")
	feed(tui, "\r")
	req := askAnswer(t, nextBoxMsg(t, got))
	if len(req.Items) != 1 {
		t.Fatalf("answer = %+v, want one item", req)
	}
	it := req.Items[0]
	if it.Notes != "keep it" {
		t.Fatalf("notes = %q, want the note on the item", it.Notes)
	}
	if len(it.OptionIndexes) != 1 || it.OptionIndexes[0] != 1 {
		t.Fatalf("item = %+v, want the option beside the note", it)
	}
}
