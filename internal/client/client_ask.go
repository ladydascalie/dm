package client

// The ask float: the questions an agent put to the reader on a pane, drawn as a
// floating pane over the desk. The store is the daemon's (internal/ask) and the
// list rides every snapshot, so the client paints what session state already
// carries and writes nothing back but the reader's answer.
//
// One question at a time: the header, the question, its options with their
// description and preview, and on a single-select question a last row that
// opens the client's input prompt. The float opens by itself on the focused
// pane's open ask and closes when the ask is answered or gone. A pane that is
// not focused gets a rail badge instead, so a question elsewhere cannot steal
// the desk.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dm/internal/ask"
	"dm/internal/boxview"
	"dm/internal/cells"
	"dm/internal/layout"
	"dm/internal/panel"
	"dm/internal/segment"
	"dm/internal/theme"
	"dm/internal/ui"
	"dm/internal/wire"
)

// askFloatID is the float's id in the client's float stack.
const askFloatID = "ask"

// The answer prompt's kind: the client's input prompt, opened by "t" or by the
// "Type something." row, carries this kind so its draft lands on the open
// question instead of a workspace.
const askPromptKind = "ask"

// askView is the open ask float: the listed ask it shows, the cursor and the
// answers so far, and the float that draws them.
type askView struct {
	id   string
	pane string
	ask  wire.Ask

	// q is the question on screen. sel is that question's cursor over its
	// choices: an option index, or the trailing typed row on a single-select
	// question.
	q   int
	sel []int
	// pick is a single-select question's chosen option, -1 while it has none.
	// multi is a multi-select question's picks, one flag per option. texts is
	// the custom answer typed for a question, space or option alike.
	pick  []int
	multi [][]bool
	texts []string

	// top is the first content row on screen, and note is the status row: an
	// answer in flight or a refusal's code.
	top  int
	note string
	// pending is true once an answer is on the wire and no snapshot has
	// closed the ask yet, so a listed ask means the daemon took it.
	pending bool
	// readOnly hides send and decline: a read-only session never answers.
	readOnly bool
	// float draws the view.
	float *panel.FloatingPane
}

// askOnPane returns the ask the snapshot lists on pane, nil when the pane has
// none. The list is oldest first, so a pane with two questions shows the first.
func askOnPane(snap *wire.Snapshot, pane string) *wire.Ask {
	if snap == nil || pane == "" {
		return nil
	}
	for i := range snap.Ask {
		if snap.Ask[i].Pane == pane {
			return &snap.Ask[i]
		}
	}
	return nil
}

// syncAsk opens the float on the focused pane's ask, refreshes the one it
// shows, and closes it when the ask is answered, gone or no longer focused. It
// defers while another overlay owns the keyboard: the float opens when that
// overlay closes, so a question never yanks the desk out from under the reader.
func (t *tui) syncAsk() {
	if t.snap == nil {
		return
	}
	a := askOnPane(t.snap, t.snap.Focus)
	if t.ask == nil {
		if a == nil {
			t.askDeferred = false
			return
		}
		if t.overlayBusy() {
			// Another overlay owns the keys. The tick opens the float once it
			// is gone.
			t.askDeferred = true
			return
		}
		t.askDeferred = false
		t.openAskFloat(a)
		return
	}
	if a == nil || a.ID != t.ask.id || t.ask.pending {
		// Gone, closed, or answered behind the float's back: a listed ask the
		// client already answered still carries no answer in the snapshot, so
		// its presence after a send is the daemon's verdict.
		t.closeAskFloat()
		return
	}
	t.ask.ask = *a
	t.ask.fit(t.snap, t.theme())
	t.dirty = true
}

// openDeferredAsk opens an ask an overlay held back, once no overlay is up.
func (t *tui) openDeferredAsk() {
	if t.askDeferred && !t.overlayBusy() {
		t.syncAsk()
	}
}

// overlayBusy reports whether an overlay other than the ask float owns the
// keyboard: a mode, the switcher in either shape, or the command palette. The
// ask float waits for it instead of painting over a desk the reader is using.
func (t *tui) overlayBusy() bool {
	return t.mode != "" || t.sw != nil || t.cp != nil
}

// openAskFloat puts the float on the desk and gives it the keyboard.
func (t *tui) openAskFloat(a *wire.Ask) {
	v := &askView{id: a.ID, pane: a.Pane, ask: *a, readOnly: t.readOnly}
	v.reset()
	v.float = &panel.FloatingPane{ID: askFloatID, Active: true}
	v.float.Paint = func(b *bytes.Buffer, r layout.Rect) { v.paint(b, r, t.theme()) }
	t.ask = v
	t.AddFloatingPane(v.float)
	t.mode = "ask"
	t.armed = false
	t.inbuf = nil
	t.pending = false
	v.fit(t.snap, t.theme())
	t.dirty = true
}

// closeAskFloat takes the float down and gives the keyboard back to the panes.
// A prompt opened from the float closes with it.
func (t *tui) closeAskFloat() {
	// The answer prompt closes with the float. It runs first and finds no
	// view, so it leaves mode empty instead of handing the keys back to an ask
	// that is gone.
	if t.mode == "rename" && t.renameKind == askPromptKind {
		t.ask = nil
		t.closeRename()
	}
	t.RemoveFloatingPane(askFloatID)
	t.ask = nil
	t.askDeferred = false
	t.mode = ""
	// The close key stays in the buffer while the pending timer waits. Drop
	// the whole buffer, so a later keypress cannot forward a stray escape to
	// a pane.
	t.inbuf = nil
	t.pending = false
	t.dirty = true
}

// reset sizes the per-question state to the ask's questions and clears every
// answer, so a fresh ask never inherits another ask's picks.
func (v *askView) reset() {
	n := len(v.ask.Questions)
	v.q = 0
	v.sel = make([]int, n)
	v.pick = make([]int, n)
	v.multi = make([][]bool, n)
	v.texts = make([]string, n)
	for i, q := range v.ask.Questions {
		v.pick[i] = -1
		v.multi[i] = make([]bool, len(q.Options))
	}
	v.top = 0
	v.note = ""
	v.pending = false
}

// question is the question on screen.
func (v *askView) question() wire.AskQuestion {
	if v.q < 0 || v.q >= len(v.ask.Questions) {
		return wire.AskQuestion{}
	}
	return v.ask.Questions[v.q]
}

// choices is how many rows the cursor can sit on: every option, plus the
// trailing typed row on a single-select question.
func (v *askView) choices(q wire.AskQuestion) int {
	n := len(q.Options)
	if !q.MultiSelect {
		n++
	}
	return n
}

// answered reports whether a question carries a complete answer. A
// multi-select question needs one pick or custom text; a single-select one
// needs exactly one of them.
func (v *askView) answered(i int) bool {
	if i < 0 || i >= len(v.ask.Questions) {
		return false
	}
	if v.texts[i] != "" {
		return true
	}
	q := v.ask.Questions[i]
	if q.MultiSelect {
		for _, on := range v.multi[i] {
			if on {
				return true
			}
		}
		return false
	}
	return v.pick[i] >= 0
}

// allAnswered reports whether every question carries an answer, the daemon's
// all-or-nothing rule said before the send.
func (v *askView) allAnswered() bool {
	for i := range v.ask.Questions {
		if !v.answered(i) {
			return false
		}
	}
	return true
}

// move steps the cursor by d rows and keeps the screen on it.
func (v *askView) move(d int) {
	q := v.question()
	n := v.choices(q)
	if n == 0 {
		return
	}
	v.sel[v.q] += d
	if v.sel[v.q] < 0 {
		v.sel[v.q] = 0
	}
	if v.sel[v.q] >= n {
		v.sel[v.q] = n - 1
	}
}

// askChoose is the pick key: it sets a single-select answer, or toggles one row
// of a multi-select answer. On the typed row it opens the answer prompt.
func (t *tui) askChoose(row int) {
	v := t.ask
	if v == nil {
		return
	}
	q := v.question()
	if row < 0 || row >= v.choices(q) {
		return
	}
	if row == len(q.Options) && !q.MultiSelect {
		t.openAskPrompt()
		return
	}
	if q.MultiSelect {
		v.multi[v.q][row] = !v.multi[v.q][row]
		return
	}
	v.pick[v.q] = row
	v.texts[v.q] = ""
	t.dirty = true
}

// askPick is the digit key: it picks the option the digit names, on a
// single-select question or a multi-select one alike.
func (t *tui) askPick(n int) {
	v := t.ask
	if v == nil {
		return
	}
	q := v.question()
	if n < 0 || n >= len(q.Options) {
		return
	}
	v.sel[v.q] = n
	t.askChoose(n)
}

// openAskPrompt opens the client's input prompt on the current question, so a
// custom answer is typed with the same editor every other prompt uses.
func (t *tui) openAskPrompt() {
	v := t.ask
	if v == nil || t.readOnly {
		return
	}
	t.openRenamePromptFor(askPromptKind, v.id, v.texts[v.q])
}

// setAskText lands the answer prompt's draft on the open question and gives the
// keyboard back to the float.
func (t *tui) setAskText(i int, text string) {
	v := t.ask
	if v == nil || i < 0 || i >= len(v.texts) {
		return
	}
	text = strings.TrimSpace(text)
	v.texts[i] = text
	if text != "" {
		// A single-select question takes one option or custom text, never
		// both, so typing replaces the pick.
		v.pick[i] = -1
	}
	v.note = ""
	if t.snap != nil {
		v.fit(t.snap, t.theme())
	}
	t.dirty = true
}

// askSubmit sends the answers and waits for the daemon's verdict. A refused
// answer keeps the float open with the code on it; the daemon pushes a snapshot
// on success and the next sync closes the float.
func (t *tui) askSubmit() {
	v := t.ask
	if v == nil || v.pending || t.readOnly {
		return
	}
	if !v.allAnswered() {
		v.note = "answer every question first"
		t.dirty = true
		return
	}
	items := make([]ask.Item, len(v.ask.Questions))
	for i, q := range v.ask.Questions {
		it := ask.Item{QuestionIndex: i}
		if q.MultiSelect {
			for j, on := range v.multi[i] {
				if on {
					it.OptionIndexes = append(it.OptionIndexes, j)
				}
			}
			it.Text = v.texts[i]
		} else if v.texts[i] != "" {
			it.Text = v.texts[i]
		} else {
			it.OptionIndexes = []int{v.pick[i]}
		}
		items[i] = it
	}
	t.sendAskAnswer(ask.AnswerReq{Items: items})
}

// askDecline answers the ask with cancel, the same call Esc makes in the
// terminal dialog this replaces.
func (t *tui) askDecline() {
	if t.ask == nil || t.ask.pending || t.readOnly {
		return
	}
	t.sendAskAnswer(ask.AnswerReq{Cancel: true})
}

// sendAskAnswer puts one answer on the wire and marks the float as waiting. A
// session with no connection keeps the float and says so.
func (t *tui) sendAskAnswer(req ask.AnswerReq) {
	v := t.ask
	if v == nil {
		return
	}
	body, err := json.Marshal(req)
	if err != nil {
		v.note = err.Error()
		t.dirty = true
		return
	}
	v.pending = true
	v.note = "sending"
	t.dirty = true
	if t.conn == nil {
		v.pending = false
		v.note = "not connected"
		return
	}
	t.askAnswerID++
	_ = t.conn.Send(wire.Msg{
		Type: "req", Action: "ask-answer", Target: v.id,
		ID: t.askAnswerID, Res: body,
	})
}

// takeAskAnswer reads the answer's reply. A refusal keeps the float open and
// shows the daemon's code; a success leaves the closing to the snapshot the
// daemon pushes.
func (t *tui) takeAskAnswer(m wire.Msg) {
	v := t.ask
	if v == nil || m.ID != t.askAnswerID {
		return
	}
	if m.OK {
		return
	}
	v.pending = false
	v.note = m.Error
	t.dirty = true
}

// askKey consumes one read while the ask float is up. Every key belongs to the
// float: nothing reaches a pane. It returns false when it needs more bytes for
// an escape sequence.
func (t *tui) askKey() bool {
	v := t.ask
	if v == nil || len(t.inbuf) == 0 || len(v.ask.Questions) == 0 {
		return true
	}
	if t.inbuf[0] == 0x1b {
		if len(t.inbuf) == 1 {
			// A lone esc declines, at once: the float binds no other escape.
			t.inbuf = nil
			t.pending = false
			t.askDecline()
			return true
		}
		if len(t.inbuf) < 3 && (bytes.HasPrefix([]byte("\x1b[A"), t.inbuf) ||
			bytes.HasPrefix([]byte("\x1b[B"), t.inbuf)) {
			// An arrow key is still arriving.
			if !t.pending {
				t.pending = true
				t.escAt = time.Now()
			}
			return false
		}
		n := escapeRun(t.inbuf)
		if n == 0 {
			n = len(t.inbuf)
		}
		head := string(t.inbuf[:n])
		t.inbuf = append([]byte(nil), t.inbuf[n:]...)
		t.pending = false
		switch head {
		case "\x1b[A":
			v.move(-1)
		case "\x1b[B":
			v.move(1)
		}
		t.askFit()
		return true
	}
	if t.inbuf[0] == '\r' || t.inbuf[0] == '\n' {
		// Enter is a control byte, not text: it submits the whole ask once
		// every question carries an answer.
		t.inbuf = t.inbuf[1:]
		t.pending = false
		t.askSubmit()
		return true
	}
	r, size, ok, wait := textRune(t.inbuf)
	if wait {
		if !t.pending {
			t.pending = true
			t.escAt = time.Now()
		}
		return false
	}
	t.inbuf = t.inbuf[size:]
	t.pending = false
	if !ok {
		return true
	}
	switch r {
	case 'j':
		v.move(1)
	case 'k':
		v.move(-1)
	case ' ':
		t.askChoose(v.sel[v.q])
	case 't':
		t.openAskPrompt()
		return true
	case 'n':
		t.askStepQuestion(1)
	case 'p':
		t.askStepQuestion(-1)
	default:
		if r >= '1' && r <= '9' {
			t.askPick(int(r - '1'))
		}
	}
	t.askFit()
	return true
}

// askStepQuestion moves to the next or previous question and keeps the cursor
// of each where the reader left it.
func (t *tui) askStepQuestion(d int) {
	v := t.ask
	if v == nil {
		return
	}
	v.q += d
	if v.q < 0 {
		v.q = 0
	}
	if v.q >= len(v.ask.Questions) {
		v.q = len(v.ask.Questions) - 1
	}
	if v.q < 0 {
		v.q = 0
	}
	v.note = ""
}

// askFit refits the float to the current question's rows and repaints.
func (t *tui) askFit() {
	v := t.ask
	if v == nil || t.snap == nil {
		t.dirty = true
		return
	}
	v.fit(t.snap, t.theme())
	t.dirty = true
}

// fit sizes the float to the current question, capped by the screen. The
// content decides the height, so a short question reads in a short box and a
// tall preview scrolls.
func (v *askView) fit(snap *wire.Snapshot, th theme.Theme) {
	roomy := boxview.BoxViewRect(snap, 1<<8)
	r := boxview.BoxViewRect(snap, len(v.lines(th, roomy.W)))
	if r.W != roomy.W {
		// A narrower box wraps more, so the height is recounted at the width
		// the placement actually gives.
		r = boxview.BoxViewRect(snap, len(v.lines(th, r.W)))
	}
	v.float.Rect = r
	v.float.Title = v.title()
	v.float.Hint = v.hint()
}

// title is the top border: the pane the ask came from and how far through the
// questions the reader is.
func (v *askView) title() string {
	return fmt.Sprintf(" ask %s  %d/%d ", v.pane, v.q+1, len(v.ask.Questions))
}

// hint is the bottom border: every key the float takes, cut down to the cells
// the border has. Send and decline survive whatever happens.
func (v *askView) hint() string {
	q := v.question()
	space := ui.Hint{Keys: []string{"space"}, Label: "pick"}
	if q.MultiSelect {
		space = ui.Hint{Keys: []string{"space"}, Label: "toggle"}
	}
	hints := []ui.Hint{
		{Keys: []string{"j", "k"}, Label: "move"},
		{Keys: []string{"1-9"}, Label: "pick"},
		space,
		{Keys: []string{"t"}, Label: "type"},
		{Keys: []string{"n", "p"}, Label: "question"},
	}
	if v.readOnly {
		return ui.Hints(hints)
	}
	hints = append(hints,
		ui.Hint{Keys: []string{"enter"}, Label: "send"},
		ui.Hint{Keys: []string{"esc"}, Label: "decline"},
	)
	limit := v.float.Rect.W - 2
	for _, drop := range []string{"question", "type", "toggle", "pick", "move"} {
		if limit < 0 || cells.Cells(ui.Hints(hints)) <= limit {
			break
		}
		kept := make([]ui.Hint, 0, len(hints))
		for _, h := range hints {
			// The pick label names two hints: the digit row and the space
			// key. The space key goes first, because the digits still name
			// every option on the cramped border.
			if h.Label == drop && !(drop == "pick" && len(h.Keys) > 0 && h.Keys[0] != "space") {
				continue
			}
			kept = append(kept, h)
		}
		hints = kept
	}
	return ui.Hints(hints)
}

// askLine is one painted row: a styled run and the choice row it belongs to,
// -1 for prose.
type askLine struct {
	segs   []segment.Seg
	choice int
}

// lines builds the content rows for the question on screen at width cells.
func (v *askView) lines(th theme.Theme, width int) []askLine {
	if len(v.ask.Questions) == 0 {
		return nil
	}
	p := th.Palette
	q := v.question()
	text := func(s string, c theme.RGB) segment.Seg { return segment.Seg{Text: s, FG: c, BG: p.RailBG} }
	var out []askLine
	prose := func(segs ...segment.Seg) { out = append(out, askLine{segs: segs, choice: -1}) }
	blank := func() { prose(text("", p.RailBG)) }
	// Rows of a block: every line of s at the indent, dim.
	block := func(s string, indent int, prefix string) {
		for _, row := range askWrap(s, width-indent-cells.Cells(prefix)) {
			prose(text(strings.Repeat(" ", indent), p.RailDim),
				text(prefix+row, p.RailDim))
		}
	}
	if q.Header != "" {
		prose(segment.Seg{Text: " " + q.Header, FG: p.BarAccent, BG: p.RailBG, Bold: true})
	}
	for _, row := range askWrap(q.Question, width-1) {
		prose(text(" "+row, p.RailText))
	}
	blank()
	for i, o := range q.Options {
		cur := v.sel[v.q] == i
		marker := text("  ", p.RailDim)
		if cur {
			marker = text("> ", p.BarAccent)
		}
		pick := text("( ) ", p.RailDim)
		if q.MultiSelect {
			pick = text("[ ] ", p.RailDim)
			if v.multi[v.q][i] {
				pick = text("[x] ", p.RailText)
			}
		} else if v.pick[v.q] == i {
			pick = text("(x) ", p.RailText)
		}
		label := text(fmt.Sprintf("%d. %s", i+1, o.Label), p.RailDim)
		if cur || v.pick[v.q] == i {
			label = text(fmt.Sprintf("%d. %s", i+1, o.Label), p.RailText)
			if cur {
				label.Bold = true
			}
		}
		out = append(out, askLine{segs: []segment.Seg{marker, pick, label}, choice: i})
		if o.Description != "" {
			block(o.Description, 6, "")
		}
		if o.Preview != "" {
			block(o.Preview, 6, th.Glyphs.BorderVertical+" ")
		}
	}
	if !q.MultiSelect {
		// The last row opens the input prompt, and shows the text once typed.
		i := len(q.Options)
		cur := v.sel[v.q] == i
		marker := text("  ", p.RailDim)
		if cur {
			marker = text("> ", p.BarAccent)
		}
		label := segment.Seg{Text: "Type something.", FG: p.RailDim, BG: p.RailBG}
		if cur {
			label.FG = p.RailText
			label.Bold = true
		}
		segs := []segment.Seg{marker, label}
		if txt := v.texts[v.q]; txt != "" && v.pick[v.q] < 0 {
			segs = append(segs, text("  "+txt, p.RailDim))
		}
		out = append(out, askLine{segs: segs, choice: i})
	}
	if len(q.Options) == 0 && q.MultiSelect {
		prose(text(" no options: t types the answer", p.RailDim))
	}
	if v.note != "" {
		blank()
		for _, row := range askWrap(v.note, width-1) {
			prose(text(" "+row, p.RailDiffDel))
		}
	}
	return out
}

// askWrap is wrapLine as a row list: one row for text that fits, and a whole
// row for an empty block, so a blank source line keeps its height.
func askWrap(s string, width int) []string {
	if s == "" {
		return []string{""}
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		out = append(out, wrapLine(line, width)...)
	}
	return out
}

// paint draws the content window. Rows are placed absolutely inside the float,
// so a scrolled view paints its new window whole.
func (v *askView) paint(b *bytes.Buffer, r layout.Rect, th theme.Theme) {
	if r.W < 1 || r.H < 1 {
		return
	}
	lines := v.lines(th, r.W)
	// Keep the cursor row on screen. The float refits on every key, so this
	// only ever moves the window onto a row the content grew past.
	for i, ln := range lines {
		if ln.choice >= 0 && ln.choice == v.sel[v.q] {
			if i < v.top {
				v.top = i
			}
			if i >= v.top+r.H {
				v.top = i - r.H + 1
			}
			break
		}
	}
	maxTop := len(lines) - r.H
	if maxTop < 0 {
		maxTop = 0
	}
	if v.top > maxTop {
		v.top = maxTop
	}
	if v.top < 0 {
		v.top = 0
	}
	for row := 0; row < r.H; row++ {
		i := v.top + row
		if i >= len(lines) {
			break
		}
		fmt.Fprintf(b, "\x1b[%d;%dH", r.Y+row+1, r.X+1)
		segs := segment.ClipSegs(lines[i].segs, r.W)
		for _, s := range segs {
			segment.Write(b, s)
		}
		if n := r.W - segment.SegCells(segs); n > 0 {
			segment.Write(b, segment.Seg{Text: strings.Repeat(" ", n), FG: th.Palette.RailBG, BG: th.Palette.RailBG})
		}
	}
}
