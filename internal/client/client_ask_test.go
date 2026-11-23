package client

// The ask float driven through a tui: the float opens on the focused pane's
// ask, takes its keys, and sends the answer the reader gives it.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"dm/internal/ask"
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
		"Branch", "Which base branch?", "1. main", "the trunk",
		"a1b2c3", "line two", "2. develop", "integration", "Type something.",
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

// TestAskFloatQuestionStep: n and p move between questions and keep each
// question's cursor.
func TestAskFloatQuestionStep(t *testing.T) {
	t.Parallel()
	questions := []wire.AskQuestion{
		{Question: "One?", Header: "First", Options: []wire.AskOption{{Label: "a"}}},
		{Question: "Two?", Header: "Second", Options: []wire.AskOption{{Label: "b"}}},
	}
	tui, _ := askTUI(t, questions)
	feed(tui, "n")
	if tui.ask.q != 1 {
		t.Fatalf("q = %d, want the second question", tui.ask.q)
	}
	if !strings.Contains(tui.ask.float.Title, "2/2") {
		t.Fatalf("title = %q, want the question count", tui.ask.float.Title)
	}
	feed(tui, "p")
	if tui.ask.q != 0 {
		t.Fatalf("q = %d, want the first question", tui.ask.q)
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
	hint := tui.ask.float.Hint
	if strings.Contains(hint, "send") || strings.Contains(hint, "decline") {
		t.Fatalf("hint = %q, want no send and no decline", hint)
	}
	feed(tui, "1\r")
	feed(tui, "\x1b")
	select {
	case m := <-got:
		t.Fatalf("a read-only client sent %+v", m)
	default:
	}
	if tui.ask == nil {
		t.Fatal("esc closed a read-only ask")
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
