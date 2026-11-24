package client

// The ask float: the questions an agent put to the reader on a pane, drawn as a
// floating pane over the desk. The store is the daemon's (internal/ask) and the
// list rides every snapshot, so the client paints what session state already
// carries and writes nothing back but the reader's answer.
//
// The box reads top to bottom as bands and rules: a filled band carrying the
// tabs and the count, a rule, the question, a rule, the list, a rule, then the
// keys as chips on a footer row inside the border. A wide float splits the
// middle: the options on the left, one row each, and a help pane on the right
// holding the hovered option's name, description and preview. The pane grows at
// most askHelpExtra rows past the list, so a long preview elides instead of
// filling the terminal. Below askWideMin the help content folds under the list
// and the descriptions sit under their options.
//
// A note rides the answer item the question already sends (ask.Item.Notes), so
// the float offers no store of its own: the draft is typed in the client's
// input prompt and the send carries it.
//
// The float opens by itself on the focused pane's open ask and closes when the
// ask is answered or gone. A pane that is not focused gets a rail badge
// instead, so a question elsewhere cannot steal the desk.

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
	"dm/internal/wire"
)

// askFloatID is the float's id in the client's float stack.
const askFloatID = "ask"

// The answer prompt's kind: the client's input prompt, opened by "t" or by the
// "Type something." row, carries this kind so its draft lands on the open
// question instead of a workspace.
const askPromptKind = "ask"

// askNoteKind is the same prompt carrying a note for the option under the
// cursor. Its draft lands on the question's answer item, so the daemon stores
// the note with the answer.
const askNoteKind = "ask-note"

const (
	// askWideMin is the narrowest float that carries the context column.
	// Below it the preview sits under its option, one column.
	askWideMin = 90
	// askContextMin is the narrowest context column, and askQuestionMin the
	// narrowest option column. The context column is about 40% of the box
	// and never squeezes the options below askQuestionMin.
	askContextMin  = 30
	askQuestionMin = 24
	// askHelpExtra is how many rows taller than the option list the help pane
	// may be. A long preview elides there instead of filling the terminal.
	askHelpExtra = 2
	// askMinRows is the interior the float reserves whatever the ask holds, so
	// the box keeps one size from the first question to the last.
	askMinRows = 13
	// askPreviewMax caps the narrow layout's preview block. A longer preview
	// ends on a count of the rows it hid.
	askPreviewMax = 5
)

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
	// notes is the note carried on each question's answer item, and noteRow
	// the option it is drawn under, -1 while the question has none.
	notes   []string
	noteRow []int

	// top is the first content row on screen, and note is the status row: an
	// answer in flight or a refusal's code.
	top  int
	note string
	// pending is true once an answer is on the wire and no snapshot has
	// closed the ask yet, so a listed ask means the daemon took it.
	pending bool
	// readOnly hides notes, send and decline: a read-only session never
	// answers.
	readOnly bool
	// float draws the view.
	float *panel.FloatingPane
}

// onScreen reports whether a pane is drawn in the active window. A pane of
// another window still lists its ask in the snapshot, and the rail carries the
// badge, but the desk should not paint a question over work the reader is in
// the middle of.
func onScreen(snap *wire.Snapshot, pane string) bool {
	if snap == nil || pane == "" {
		return false
	}
	for i := range snap.Panes {
		if snap.Panes[i].ID == pane {
			return true
		}
	}
	return false
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
	if a != nil && !onScreen(t.snap, a.Pane) {
		// The question belongs to a pane of another window. The rail badge
		// names it and a jump from the rail focuses it; a float over a window
		// the reader is not looking at is a surprise, not a prompt.
		a = nil
	}
	if t.ask == nil {
		if a == nil || a.ID == t.askClosed || a.ID == t.askHidden {
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
	if a.ID == t.askHidden {
		// The reader put the question out of the way. The view keeps its
		// picks and its cursor, so the toggle brings the same form back.
		t.ask.ask = *a
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
	// The answer and note prompts close with the float. They run first and
	// find no view, so they leave mode empty instead of handing the keys back
	// to an ask that is gone.
	if t.mode == "rename" && (t.renameKind == askPromptKind || t.renameKind == askNoteKind) {
		t.ask = nil
		t.closeRename()
	}
	t.RemoveFloatingPane(askFloatID)
	if t.ask != nil && t.askHidden == t.ask.id {
		t.askHidden = ""
	}
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

// barAskOf is the question the status strip pins: the focused pane's open ask
// while its float is out of the way. The box is louder than any chip when it is
// up, and the rail badge names an ask on another pane, so the strip carries the
// one case neither covers.
func (t *tui) barAskOf() barAsk {
	if t.snap == nil {
		return barAsk{}
	}
	if t.ask != nil && t.askHidden != t.ask.id {
		return barAsk{}
	}
	a := askOnPane(t.snap, t.snap.Focus)
	if a == nil || !onScreen(t.snap, a.Pane) {
		return barAsk{}
	}
	index := 1
	if t.ask != nil && t.ask.id == a.ID {
		index = t.ask.q + 1
	}
	key := "A"
	if km := t.km(); km != nil {
		if k := km.Key("main", "toggle-ask"); k != "" {
			key = k
		}
	}
	return barAsk{pane: a.Pane, index: index, total: len(a.Questions), key: key}
}

// toggleAsk puts the question float away, or brings it back. Hiding keeps the
// view, so the picks and the cursor survive, and leaves the desk to the reader:
// the pane takes the keys again, so they can scroll back and reread what the
// question is about. The pane keeps its rail badge while the ask is hidden.
func (t *tui) toggleAsk() {
	// A hidden view is still t.ask, so the hidden mark is what tells the two
	// states apart.
	if t.ask != nil && t.askHidden != t.ask.id {
		t.hideAsk()
		return
	}
	t.showAsk()
}

// hideAsk takes the float off the desk without dropping the ask. Nothing
// reached the store when it appeared, so nothing has to be undone here.
func (t *tui) hideAsk() {
	if t.ask == nil {
		return
	}
	t.askHidden = t.ask.id
	t.RemoveFloatingPane(askFloatID)
	t.askDeferred = false
	t.mode = ""
	// The key that hid it may still be arriving as one sequence: drop the
	// buffer so it cannot leak a byte into the pane.
	t.inbuf = nil
	t.pending = false
	t.dirty = true
}

// showAsk puts a hidden ask back, or opens the focused pane's ask when the
// reader reaches for one that is not up. A settled ask never comes back.
func (t *tui) showAsk() {
	if t.snap == nil {
		return
	}
	a := askOnPane(t.snap, t.snap.Focus)
	if a == nil || a.ID == t.askClosed {
		return
	}
	if !onScreen(t.snap, a.Pane) {
		return
	}
	if t.ask != nil && t.ask.id == a.ID {
		// The view survived the hide: put its float back and keep the picks.
		t.askHidden = ""
		t.ask.ask = *a
		t.AddFloatingPane(t.ask.float)
		t.mode = "ask"
		t.armed = false
		t.inbuf = nil
		t.pending = false
		t.ask.fit(t.snap, t.theme())
		t.dirty = true
		return
	}
	t.askHidden = ""
	t.openAskFloat(a)
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
	v.notes = make([]string, n)
	v.noteRow = make([]int, n)
	for i, q := range v.ask.Questions {
		v.pick[i] = -1
		v.noteRow[i] = -1
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

// askNote opens the input prompt on the note for the option under the cursor.
// The note rides the question's answer item, so nothing here reaches the store
// before the send. n names the next question, so the note key is its shifted
// form.
func (t *tui) askNote() {
	v := t.ask
	if v == nil || t.readOnly || len(v.notes) == 0 {
		return
	}
	if v.sel[v.q] >= len(v.question().Options) {
		// The typed row is no option: a note belongs to one.
		return
	}
	t.openRenamePromptFor(askNoteKind, v.id, v.notes[v.q])
}

// setAskNote lands the note prompt's draft on the question under it. The note
// is drawn under the option the cursor was on, and an empty draft clears it.
func (t *tui) setAskNote(i int, text string) {
	v := t.ask
	if v == nil || i < 0 || i >= len(v.notes) {
		return
	}
	text = strings.TrimSpace(text)
	v.notes[i] = text
	if text == "" {
		v.noteRow[i] = -1
	} else {
		v.noteRow[i] = v.sel[i]
	}
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
		it := ask.Item{QuestionIndex: i, Notes: v.notes[i]}
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

// dismissAsk puts the float down and remembers the id, so a listed ask this
// client has finished with cannot reopen on the next snapshot. The case that
// matters: an ask answered on another surface stays listed until its owner
// closes it, and an owner that died never will.
func (t *tui) dismissAsk() {
	if t.ask == nil {
		return
	}
	t.askClosed = t.ask.id
	t.askHidden = ""
	t.closeAskFloat()
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
	// The ask is settled or gone: there is nothing left to answer, so the
	// float leaves instead of showing a code the reader cannot clear.
	if strings.Contains(m.Error, "E_EXISTS") || strings.Contains(m.Error, "not_found") {
		t.dismissAsk()
		return
	}
	v.pending = false
	v.note = m.Error
	if t.askHidden == v.id {
		t.showAsk()
	}
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
			bytes.HasPrefix([]byte("\x1b[B"), t.inbuf) ||
			bytes.HasPrefix([]byte("\x1b[Z"), t.inbuf)) {
			// An arrow or shift-tab key is still arriving.
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
		case "\x1b[Z":
			// Shift-tab steps back, wrapping like tab.
			t.askStepQuestion(-1)
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
	if t.inbuf[0] == '\t' {
		// Tab is a control byte too: it moves to the next question.
		t.inbuf = t.inbuf[1:]
		t.pending = false
		t.askStepQuestion(1)
		t.askFit()
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
	case 'N':
		t.askNote()
		return true
	case 'A':
		t.toggleAsk()
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

// askStepQuestion moves to the next or previous question, wrapping, and keeps
// the cursor of each where the reader left it.
func (t *tui) askStepQuestion(d int) {
	v := t.ask
	if v == nil || len(v.ask.Questions) == 0 {
		return
	}
	n := len(v.ask.Questions)
	v.q = ((v.q+d)%n + n) % n
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
// tall one is clamped by the layout that draws it.
func (v *askView) fit(snap *wire.Snapshot, th theme.Theme) {
	roomy := boxview.BoxViewRect(snap, 1<<8)
	r := boxview.BoxViewRect(snap, v.needHeight(th, roomy.W))
	if r.W != roomy.W {
		// A narrower box wraps more, so the height is recounted at the width
		// the placement actually gives.
		r = boxview.BoxViewRect(snap, v.needHeight(th, r.W))
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

// hint is the bottom border. The footer inside the box carries every key as a
// chip, so the border is left clean.
func (v *askView) hint() string { return "" }

// askShape is the float's interior split: the option list's width and the help
// pane's. A zero rightW is one column, the narrow layout.
type askShape struct {
	leftW  int
	rightW int
}

// shape splits a width-cell interior. The help pane is about 40% of the box and
// never narrower than askContextMin, and the options keep at least
// askQuestionMin cells beside it. Below askWideMin the float keeps one column,
// so the help content folds under its option.
func (v *askView) shape(width int) askShape {
	if width < askWideMin {
		return askShape{leftW: width}
	}
	right := width * 2 / 5
	if right < askContextMin {
		right = askContextMin
	}
	if max := width - askQuestionMin - 1; right > max {
		right = max
	}
	if right < 1 {
		return askShape{leftW: width}
	}
	return askShape{leftW: width - right - 1, rightW: right}
}

// askLine is one painted row: a styled run set, the choice row it belongs to
// (-1 for prose) and the ground the row pads with. The ground rides on the row
// so a band and a plain row can share one painter.
type askLine struct {
	segs   []segment.Seg
	choice int
	bg     theme.RGB
}

// headLines is every row above the question: the tab band on a form, the header
// band, and the rule under them. One band is always drawn, so the count has a
// home even on a question with no header.
func (v *askView) headLines(th theme.Theme, width int) []askLine {
	p := th.Palette
	var out []askLine
	if len(v.ask.Questions) > 1 {
		// The strip names every question, so the header band would only
		// repeat the active one.
		out = append(out, askLine{segs: v.tabRow(th, width), choice: -1, bg: p.RailFocusBG})
	} else {
		out = append(out, askLine{segs: v.headBand(v.question().Header, width, th), choice: -1, bg: p.RailFocusBG})
	}
	return append(out, v.ruleRow(th, width))
}

// headBand is the question's header on its filled band, with the count at the
// right edge. The band fills to the width, so a short header still reads as a
// band and not as a stray word.
func (v *askView) headBand(header string, width int, th theme.Theme) []segment.Seg {
	p := th.Palette
	band := func(s string, fg theme.RGB, bold bool) segment.Seg {
		return segment.Seg{Text: s, FG: fg, BG: p.RailFocusBG, Bold: bold}
	}
	segs := []segment.Seg{band(" ", p.RailBG, false), band(header, p.RailHeader, true)}
	count := fmt.Sprintf("%d of %d", v.q+1, len(v.ask.Questions))
	if pad := width - 1 - cells.Cells(header) - cells.Cells(count) - 1; pad > 0 {
		segs = append(segs, band(strings.Repeat(" ", pad), p.RailBG, false))
	}
	return append(segs, band(count+" ", p.RailDim, false))
}

// tabRow is the tab strip: one tab per question, the active one pointed at and
// an answered one filled, with the count at the right edge, so a form's
// progress reads at a glance.
func (v *askView) tabRow(th theme.Theme, width int) []segment.Seg {
	p := th.Palette
	band := func(s string, fg theme.RGB, bold bool) segment.Seg {
		return segment.Seg{Text: s, FG: fg, BG: p.RailFocusBG, Bold: bold}
	}
	segs := []segment.Seg{band(" ", p.RailBG, false)}
	for i, q := range v.ask.Questions {
		name := q.Header
		if name == "" {
			name = fmt.Sprintf("Q%d", i+1)
		}
		fg := p.RailDim
		point, fill := " ", " "
		if i == v.q {
			fg, point = p.BarAccent, th.Glyphs.ActiveMark
		}
		if v.answered(i) {
			fill = th.Glyphs.Dirty
		}
		segs = append(segs,
			band(point, fg, i == v.q),
			band(fill, p.RailText, false),
			band(fmt.Sprintf("%d %s  ", i+1, name), fg, i == v.q),
		)
	}
	count := fmt.Sprintf("%d of %d", v.q+1, len(v.ask.Questions))
	if pad := width - segment.SegCells(segs) - cells.Cells(count) - 1; pad > 0 {
		segs = append(segs, band(strings.Repeat(" ", pad), p.RailBG, false))
	}
	return append(segs, band(count+" ", p.RailDim, false))
}

// ruleRow is one hairline across the interior, one cell clear of each border.
func (v *askView) ruleRow(th theme.Theme, width int) askLine {
	p := th.Palette
	n := width - 2
	if n < 0 {
		n = 0
	}
	return askLine{
		segs: []segment.Seg{
			{Text: " ", FG: p.RailBG, BG: p.RailBG},
			{Text: strings.Repeat(th.Glyphs.BorderHorizontal, n), FG: p.RailDim, BG: p.RailBG},
		},
		choice: -1,
		bg:     p.RailBG,
	}
}

// questionLines is the question block: its text wrapped to the width, then the
// rule that opens the list.
func (v *askView) questionLines(th theme.Theme, width int) []askLine {
	p := th.Palette
	var out []askLine
	for _, row := range askWrap(v.question().Question, width-1) {
		out = append(out, askLine{
			segs: []segment.Seg{
				{Text: " ", FG: p.RailBG, BG: p.RailBG},
				{Text: row, FG: p.RailText, BG: p.RailBG},
			},
			choice: -1,
			bg:     p.RailBG,
		})
	}
	return append(out, v.ruleRow(th, width))
}

// optionIndent is the column an option's label starts in: the cursor bar, the
// mark's four cells and the number's three.
const optionIndent = 7

// listLines is the option column: one row per choice, the note under the option
// it belongs to, and, in the narrow layout, the description of each option
// under it. The preview is the help pane's business in the wide layout.
func (v *askView) listLines(th theme.Theme, width int, withDescription bool) []askLine {
	if len(v.ask.Questions) == 0 {
		return nil
	}
	p := th.Palette
	q := v.question()
	var out []askLine
	// row builds one option row: the accent bar's cell, then the runs, then the
	// ground filled to the column width.
	row := func(i int, segs []segment.Seg) askLine {
		bg := p.RailBG
		bar := segment.Seg{Text: " ", FG: p.RailBG, BG: p.RailBG}
		if i == v.sel[v.q] {
			bg = p.RailFocusBG
			bar = segment.Seg{Text: th.Glyphs.WorkspaceBar, FG: p.BarAccent, BG: bg}
		}
		body := make([]segment.Seg, 0, len(segs)+1)
		body = append(body, bar)
		for _, s := range segs {
			if s.BG == p.RailBG {
				s.BG = bg
			}
			body = append(body, s)
		}
		return askLine{segs: body, choice: i, bg: bg}
	}
	// kid builds a row under the option at i: the bar's cell, the text at the
	// given indent, dim.
	kid := func(i, indent int, segs []segment.Seg) askLine {
		bg := p.RailBG
		bar := segment.Seg{Text: " ", FG: p.RailBG, BG: p.RailBG}
		if i == v.sel[v.q] {
			bg = p.RailFocusBG
			bar = segment.Seg{Text: th.Glyphs.WorkspaceBar, FG: p.BarAccent, BG: bg}
		}
		body := []segment.Seg{bar, {Text: strings.Repeat(" ", indent), FG: p.RailBG, BG: bg}}
		for _, s := range segs {
			if s.BG == p.RailBG {
				s.BG = bg
			}
			body = append(body, s)
		}
		return askLine{segs: body, choice: -1, bg: bg}
	}
	for i, o := range q.Options {
		cur := v.sel[v.q] == i
		// Every option carries its mark, single select included: the band and
		// the pointer say where the cursor is, and the box says what is
		// chosen, so a pick left behind the cursor still reads as one.
		box, on := "[ ] ", false
		switch {
		case q.MultiSelect && v.multi[v.q][i]:
			box, on = "[x] ", true
		case !q.MultiSelect && v.pick[v.q] == i:
			box, on = "[●] ", true
		}
		// A pick wears the theme's settled green, so it reads as an answer and
		// not as a cursor: the band and the pointer are where the reader is,
		// the mark and its colour are what they said.
		boxFG := p.RailDim
		if on {
			boxFG = p.RailDiffAdd
		}
		label := segment.Seg{Text: fmt.Sprintf("%d  %s", i+1, o.Label), FG: p.RailDim, BG: p.RailBG}
		switch {
		case on:
			// The colour survives the cursor: what is picked stays green
			// whether the reader is standing on it or has moved on.
			label.FG, label.Bold = p.RailDiffAdd, cur
		case cur:
			label.FG, label.Bold = p.RailText, true
		}
		segs := []segment.Seg{{Text: box, FG: boxFG, BG: p.RailBG}, label}
		out = append(out, row(i, segs))
		if withDescription && o.Description != "" {
			for _, text := range askWrap(o.Description, width-8) {
				out = append(out, kid(i, optionIndent, []segment.Seg{{Text: text, FG: p.RailDim, BG: p.RailBG}}))
			}
		}
		if v.noteRow[v.q] == i && v.notes[v.q] != "" {
			for n, text := range askWrap(v.notes[v.q], width-13) {
				segs := []segment.Seg{{Text: text, FG: p.RailDim, BG: p.RailBG}}
				if n == 0 {
					segs = append([]segment.Seg{{Text: "note: ", FG: p.RailHeader, BG: p.RailBG}}, segs...)
				}
				out = append(out, kid(i, optionIndent, segs))
			}
		}
	}
	if !q.MultiSelect {
		// The last row opens the input prompt, and shows the text once typed.
		i := len(q.Options)
		label := segment.Seg{Text: "+  Type something.", FG: p.RailDim, BG: p.RailBG}
		if v.texts[v.q] != "" {
			label.Text = "+  " + v.texts[v.q]
		}
		if v.sel[v.q] == i {
			label.FG = p.RailText
			label.Bold = true
		}
		out = append(out, row(i, []segment.Seg{
			{Text: strings.Repeat(" ", len("[ ] ")), FG: p.RailBG, BG: p.RailBG},
			label,
		}))
	} else if len(q.Options) == 0 {
		out = append(out, kid(0, 1, []segment.Seg{{Text: "no options: t types the answer", FG: p.RailDim, BG: p.RailBG}}))
	}
	return out
}

// hovered is the option under the cursor, false while the cursor sits on the
// typed row.
func (v *askView) hovered() (wire.AskOption, bool) {
	q := v.question()
	if i := v.sel[v.q]; i >= 0 && i < len(q.Options) {
		return q.Options[i], true
	}
	return wire.AskOption{}, false
}

// helpLines is the help pane: the hovered option's name on its band, its
// description, and its preview. The pane is clamped to height rows; a preview
// longer than the room left ends on a count of the rows it hid. The key hint
// sits on the pane's last row when the room is there.
func (v *askView) helpLines(th theme.Theme, width, height int) []askLine {
	p := th.Palette
	o, ok := v.hovered()
	label := "Type something."
	if ok {
		label = o.Label
	}
	head := []askLine{
		{
			segs: []segment.Seg{
				{Text: " ", FG: p.BarAccent, BG: p.RailHoverBG},
				{Text: label, FG: p.BarAccent, BG: p.RailHoverBG, Bold: true},
			},
			choice: -1,
			bg:     p.RailHoverBG,
		},
	}
	hint := ""
	if q := v.question(); q.MultiSelect {
		hint = "space toggles the pick"
	} else {
		hint = "enter to pick and send"
	}
	hintLine := askLine{
		segs:   []segment.Seg{{Text: " " + hint, FG: p.RailWindow, BG: p.RailBG}},
		choice: -1,
		bg:     p.RailBG,
	}
	var body []askLine
	if ok && o.Description != "" {
		for _, row := range askWrap(o.Description, width-2) {
			body = append(body, askLine{
				segs:   []segment.Seg{{Text: " " + row, FG: p.RailDim, BG: p.RailBG}},
				choice: -1,
				bg:     p.RailBG,
			})
		}
	}
	body = append(body, askLine{
		segs:   []segment.Seg{{Text: " ", FG: p.RailBG, BG: p.RailBG}},
		choice: -1,
		bg:     p.RailBG,
	})
	if !ok || o.Preview == "" {
		body = append(body, askLine{
			segs:   []segment.Seg{{Text: " no preview", FG: p.RailDim, BG: p.RailBG}},
			choice: -1,
			bg:     p.RailBG,
		})
	} else {
		for _, row := range strings.Split(o.Preview, "\n") {
			body = append(body, askLine{
				segs:   []segment.Seg{{Text: " " + row, FG: p.RailDim, BG: p.RailBG}},
				choice: -1,
				bg:     p.RailBG,
			})
		}
	}
	if height <= 0 {
		return append(append(head, body...), hintLine)
	}
	if len(head)+len(body) < height {
		// The hint pins to the pane's bottom row, and the rows between it and
		// the body stay blank.
		out := append(head, body...)
		for len(out) < height-1 {
			out = append(out, askLine{bg: p.RailBG, choice: -1})
		}
		return append(out, hintLine)
	}
	room := height - len(head)
	if room <= 0 {
		return head[:height]
	}
	if room == len(body) {
		return append(head, body...)
	}
	keep := room - 1
	out := append(head, body[:keep]...)
	more := fmt.Sprintf(" %s %d more lines", th.Glyphs.ElideMark(), len(body)-keep)
	return append(out, askLine{
		segs:   []segment.Seg{{Text: more, FG: p.RailDim, BG: p.RailBG}},
		choice: -1,
		bg:     p.RailBG,
	})
}

// previewLines is the narrow layout's preview block: the rule above it and the
// hovered option's preview under it. An option with no preview has no block,
// so a narrow float does not spend rows saying so.
func (v *askView) previewLines(th theme.Theme, width int) []askLine {
	o, ok := v.hovered()
	if !ok || o.Preview == "" {
		return nil
	}
	p := th.Palette
	rows := askWrap(o.Preview, width-1)
	out := []askLine{v.ruleRow(th, width)}
	for i, row := range rows {
		if i == askPreviewMax {
			more := fmt.Sprintf(" %s %d more lines", th.Glyphs.ElideMark(), len(rows)-i)
			return append(out, askLine{
				segs:   []segment.Seg{{Text: more, FG: p.RailDim, BG: p.RailBG}},
				choice: -1,
				bg:     p.RailBG,
			})
		}
		out = append(out, askLine{
			segs:   []segment.Seg{{Text: " " + row, FG: p.RailDim, BG: p.RailBG}},
			choice: -1,
			bg:     p.RailBG,
		})
	}
	return out
}

// tailLines is every row below the list: the rule under it, the status row when
// the answer is in flight or refused, and the footer.
func (v *askView) tailLines(th theme.Theme, width int) []askLine {
	p := th.Palette
	out := []askLine{v.ruleRow(th, width)}
	if v.note != "" {
		for _, row := range askWrap(v.note, width-1) {
			out = append(out, askLine{
				segs:   []segment.Seg{{Text: " " + row, FG: p.RailDiffDel, BG: p.RailBG}},
				choice: -1,
				bg:     p.RailBG,
			})
		}
	}
	return append(out, askLine{segs: v.footRow(th, width), choice: -1, bg: p.RailBG})
}

// askChip is one footer entry: the keys drawn as a filled chip and the word
// that says what they do.
type askChip struct{ keys, label string }

// chips is the footer for the question on screen: every key the float takes,
// send and decline included whenever the session may answer.
func (v *askView) chips() []askChip {
	q := v.question()
	space := "pick"
	if q.MultiSelect {
		space = "toggle"
	}
	out := []askChip{
		{"j k", "move"},
		{"1-9", "pick"},
		{"space", space},
		{"t", "type"},
	}
	if len(v.ask.Questions) > 1 {
		out = append(out, askChip{"tab", "question"})
	}
	out = append(out, askChip{"A", "hide"})
	if v.readOnly {
		return out
	}
	return append(out,
		askChip{"N", "note"},
		askChip{"enter", "send"},
		askChip{"esc", "decline"},
	)
}

// footRow draws the footer, dropping the hints a narrow box cannot hold. Send
// and decline survive whatever happens; the rest go in the order they matter.
func (v *askView) footRow(th theme.Theme, width int) []segment.Seg {
	p := th.Palette
	chips := v.chips()
	build := func(cs []askChip) []segment.Seg {
		segs := []segment.Seg{{Text: " ", FG: p.RailBG, BG: p.RailBG}}
		for i, c := range cs {
			if i > 0 {
				segs = append(segs, segment.Seg{Text: "  ", FG: p.RailBG, BG: p.RailBG})
			}
			segs = append(segs,
				segment.Seg{Text: " " + c.keys + " ", FG: p.RailText, BG: p.RailHoverBG},
				segment.Seg{Text: " " + c.label, FG: p.RailDim, BG: p.RailBG},
			)
		}
		return segs
	}
	segs := build(chips)
	for _, drop := range []string{"question", "note", "hide", "type", "toggle", "pick", "move"} {
		if width < 0 || segment.SegCells(segs) <= width {
			break
		}
		kept := make([]askChip, 0, len(chips))
		for _, c := range chips {
			if c.label == drop {
				continue
			}
			kept = append(kept, c)
		}
		chips = kept
		segs = build(chips)
	}
	return segs
}

// paintLine writes one row at x, y, clipped to width cells and filled to the
// width with the row's own ground.
func paintLine(b *bytes.Buffer, ln askLine, x, y, width int) {
	if width <= 0 {
		return
	}
	fmt.Fprintf(b, "\x1b[%d;%dH", y+1, x+1)
	clipped := segment.ClipSegs(ln.segs, width)
	for _, s := range clipped {
		segment.Write(b, s)
	}
	if n := width - segment.SegCells(clipped); n > 0 {
		segment.Write(b, segment.Seg{Text: strings.Repeat(" ", n), FG: ln.bg, BG: ln.bg})
	}
}

// askWrap is wrapLine as a row list: one row for text that fits, and a whole
// row for an empty block, so a blank source line keeps its height.
func askWrap(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	if s == "" {
		return []string{""}
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		out = append(out, wrapLine(line, width)...)
	}
	return out
}

// fit sizes the float to the current question, capped by the screen. The
// content decides the height, so a short question reads in a short box and a
// tall one is clamped by the layout that draws it.
func (v *askView) height(th theme.Theme, width int) int {
	if len(v.ask.Questions) == 0 {
		return 1
	}
	sh := v.shape(width)
	n := len(v.headLines(th, width)) + len(v.questionLines(th, width))
	list := len(v.listLines(th, sh.leftW, sh.rightW == 0))
	if sh.rightW > 0 {
		help := len(v.helpLines(th, sh.rightW, 0))
		if help > list+askHelpExtra {
			help = list + askHelpExtra
		}
		if help > list {
			list = help
		}
	} else {
		list += len(v.previewLines(th, width))
	}
	return n + list + len(v.tailLines(th, width))
}

// needHeight is the interior the float reserves for the whole ask: the tallest
// question, at its tallest hovered option, and never below askMinRows. The box
// is therefore one size from the first question to the last, and a cursor move
// or a tab never resizes it.
func (v *askView) needHeight(th theme.Theme, width int) int {
	if len(v.ask.Questions) == 0 {
		return askMinRows
	}
	savedQ, savedSel := v.q, append([]int(nil), v.sel...)
	defer func() { v.q, v.sel = savedQ, savedSel }()
	n := 0
	for i := range v.ask.Questions {
		v.q = i
		for j := 0; j <= len(v.ask.Questions[i].Options); j++ {
			v.sel[i] = j
			if h := v.height(th, width); h > n {
				n = h
			}
		}
	}
	if n < askMinRows {
		n = askMinRows
	}
	return n
}

// paint draws the box: the bands and the question from the top, the footer
// pinned to the bottom, and the split taking every row in between. The reserved
// height therefore shows as room in the columns, not as dead rows after the
// footer, and a box the screen clamped short keeps its keys.
func (v *askView) paint(b *bytes.Buffer, r layout.Rect, th theme.Theme) {
	if r.W < 1 || r.H < 1 || len(v.ask.Questions) == 0 {
		return
	}
	sh := v.shape(r.W)
	bottom := r.Y + r.H
	y := r.Y
	write := func(ln askLine) {
		if y < bottom {
			paintLine(b, ln, r.X, y, r.W)
		}
		y++
	}
	for _, ln := range v.headLines(th, r.W) {
		write(ln)
	}
	for _, ln := range v.questionLines(th, r.W) {
		write(ln)
	}
	tail := v.tailLines(th, r.W)
	tailY := bottom - len(tail)
	split := tailY - y
	if split < 0 {
		split = 0
	}
	if sh.rightW == 0 {
		v.paintColumn(b, r, th, y, split, sh)
	} else {
		v.paintPanes(b, r, th, y, split, sh)
	}
	for i, ln := range tail {
		if at := tailY + i; at >= 0 && at < bottom {
			paintLine(b, ln, r.X, at, r.W)
		}
	}
}

// paintColumn draws the narrow layout: the list, then the hovered option's
// preview under a rule. The list windows under the cursor when it outruns the
// room the split leaves.
func (v *askView) paintColumn(b *bytes.Buffer, r layout.Rect, th theme.Theme, y, split int, sh askShape) {
	list := v.listLines(th, sh.leftW, true)
	preview := v.previewLines(th, r.W)
	avail := split - len(preview)
	if avail < 0 {
		avail = 0
	}
	v.scrollTo(list, avail)
	for i := 0; i < avail && v.top+i < len(list); i++ {
		paintLine(b, list[v.top+i], r.X, y+i, r.W)
	}
	at := y + avail
	for _, ln := range preview {
		if at >= y+split {
			break
		}
		paintLine(b, ln, r.X, at, r.W)
		at++
	}
}

// paintPanes draws the wide layout: the option column behind a vertical rule
// and the help pane beside it. The list windows under the cursor; the help pane
// stays on the option it reads and elides what it cannot show.
func (v *askView) paintPanes(b *bytes.Buffer, r layout.Rect, th theme.Theme, y, split int, sh askShape) {
	list := v.listLines(th, sh.leftW, false)
	help := v.helpLines(th, sh.rightW, split)
	v.scrollTo(list, split)
	for i := 0; i < split; i++ {
		at := y + i
		if at >= r.Y+r.H {
			break
		}
		if row := v.top + i; row < len(list) {
			paintLine(b, list[row], r.X, at, sh.leftW)
		} else {
			paintLine(b, askLine{bg: th.Palette.RailBG, choice: -1}, r.X, at, sh.leftW)
		}
		fmt.Fprintf(b, "\x1b[%d;%dH", at+1, r.X+sh.leftW+1)
		segment.Write(b, segment.Seg{
			Text: th.Glyphs.BorderVertical, FG: th.Palette.RailDim, BG: th.Palette.RailBG,
		})
		if i < len(help) {
			paintLine(b, help[i], r.X+sh.leftW+1, at, sh.rightW)
		} else {
			paintLine(b, askLine{bg: th.Palette.RailBG, choice: -1}, r.X+sh.leftW+1, at, sh.rightW)
		}
	}
}

// scrollTo keeps the cursor's row inside a window of h rows. The float refits
// on every key, so this only ever moves the window onto a row the content grew
// past.
func (v *askView) scrollTo(lines []askLine, h int) {
	for i, ln := range lines {
		if ln.choice >= 0 && ln.choice == v.sel[v.q] {
			if i < v.top {
				v.top = i
			}
			if i >= v.top+h {
				v.top = i - h + 1
			}
			break
		}
	}
	maxTop := len(lines) - h
	if maxTop < 0 {
		maxTop = 0
	}
	if v.top > maxTop {
		v.top = maxTop
	}
	if v.top < 0 {
		v.top = 0
	}
}
