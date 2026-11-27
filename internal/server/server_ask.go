package server

import (
	"errors"
	"log"
	"time"

	"dm/internal/ask"
	"dm/internal/secrets"
	"dm/internal/wire"
)

// The ask store: the questions an agent puts to the reader on a pane. The
// requests live in the session state file the way box entries do, so a client
// that attaches mid-question sees the open ones. These actions are the only
// door: no keystroke path, no float and no endpoint reaches the store.

// askPollWait bounds how long an await sleeps before it looks at the client's
// socket for a hangup. The client's read loop is inside the wait and cannot see
// the EOF itself, so the wait polls for it. A missed hangup costs one timeout,
// never a live waiter past this interval.
const askPollWait = 100 * time.Millisecond

// errAskGone reports an await whose connection went. A reply has nowhere to go,
// so the handler stops without one and the waiter is released.
var errAskGone = errors.New("ask: client gone")

// askWaiter is one ask-await parked on s.askWaiters. The buffered channel holds
// one nudge, so a wakeup that arrives between two checks is not lost.
type askWaiter struct {
	wake chan struct{}
}

// askWatchLocked registers a waiter. The caller holds s.mu and registers before
// it unlocks, so an answer cannot slip between the last check and the
// registration.
func (s *server) askWatchLocked(w *askWaiter) {
	if s.askWaiters == nil {
		s.askWaiters = map[*askWaiter]bool{}
	}
	s.askWaiters[w] = true
}

// askUnwatch removes a waiter, on answer, close, timeout or disconnect.
func (s *server) askUnwatch(w *askWaiter) {
	s.mu.Lock()
	delete(s.askWaiters, w)
	s.mu.Unlock()
}

// askWakeLocked nudges every parked await. The caller holds s.mu, and the
// non-blocking send means a waiter that is already running keeps its one
// pending nudge.
func (s *server) askWakeLocked() {
	for w := range s.askWaiters {
		select {
		case w.wake <- struct{}{}:
		default:
		}
	}
}

// askOpen lists one ask on a pane, then gives that pane the focus: the desk
// stands the question box only on the focused pane of the active window, so a
// question on any other pane would arrive invisible while the agent blocked on
// an await nobody saw.
func (s *server) askOpen(pane string, req *ask.OpenReq) (string, error) {
	id, err := s.askStore(pane, req)
	if err != nil {
		return "", err
	}
	s.askFocus(pane)
	return id, nil
}

// askFocus moves the focus to the pane a question sits on, then pushes the
// snapshot that shows it. A reader already standing on an open ask keeps their
// place: the box in front of them carries picks they typed, and stealing it
// would throw them away. That leaves a second question queued behind the first,
// which the rail badge and dm ask list still name.
//
// The zoom is one view of one focus, so a focus that moves drops it. Dropping
// it changes every rect in the old window, which owes the panes their new size
// and the client a rebuilt screen: the resync carries that snapshot instead.
func (s *server) askFocus(pane string) {
	s.mu.Lock()
	win := s.st.ActiveWindow()
	moved := win.Focus != pane && !s.openAskOnLocked(win.Focus) &&
		s.setFocusLocked(pane) == nil
	zoomed := moved && s.zoom != ""
	if moved {
		s.zoom = ""
		_ = s.st.Save()
	}
	s.mu.Unlock()
	if zoomed {
		s.resync()
		return
	}
	if moved {
		s.pushSnapshot()
	}
}

// openAskOnLocked reports whether a pane carries an unanswered ask, the one the
// desk is showing. The caller holds s.mu.
func (s *server) openAskOnLocked(pane string) bool {
	if pane == "" {
		return false
	}
	for i := range s.st.Ask {
		if s.st.Ask[i].Answer == nil && s.st.Ask[i].Pane == pane {
			return true
		}
	}
	return false
}

// askStore lists one ask on a pane and returns the id the server gave it. A
// tool call already covered by a listed ask is refused as E_EXISTS: an open ask
// replaces nothing, and a retried tool call must not stack a second question
// set on the reader.
func (s *server) askStore(pane string, req *ask.OpenReq) (string, error) {
	if pane == "" {
		return "", ask.Refuse(ask.CodeInvalidPayload, "ask-open needs a pane")
	}
	if err := ask.ValidateOpen(req); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.paneInTreeLocked(pane) {
		return "", ask.Refuse(ask.CodeInvalidPayload, "no pane %q", pane)
	}
	for i := range s.st.Ask {
		if s.st.Ask[i].ToolCallID == req.ToolCallID {
			return "", ask.Refuse(ask.CodeExists, "ask %q already covers tool call %q", s.st.Ask[i].ID, req.ToolCallID)
		}
	}
	// The questions are scrubbed before the cost check, so the cap measures
	// what is stored and a value never reaches state.json or a snapshot. The
	// scrub keeps every byte length, so the cost is the same either way.
	needles := s.scrubNeedlesLocked()
	questions := scrubQuestions(req.Questions, needles)
	if err := ask.CheckRoom(s.st.Ask, &ask.OpenReq{ToolCallID: req.ToolCallID, Questions: questions}); err != nil {
		return "", err
	}
	id := s.st.NewAskID()
	s.st.Ask = append(s.st.Ask, ask.Request{
		ID:         id,
		Pane:       pane,
		ToolCallID: req.ToolCallID,
		At:         askTime(),
		Questions:  questions,
	})
	err := s.st.Save()
	s.pushAskLocked()
	return id, err
}

// dropPaneAsksLocked drops every ask a pane owns, answered or not, and reports
// whether it removed one. A closed or lost pane can no longer answer, so an ask
// left behind would be a ghost: still listed, still in the state file, still
// answerable from the phone. The caller holds s.mu and saves the state; the
// snapshot is the caller's push.
func (s *server) dropPaneAsksLocked(pane string) bool {
	if pane == "" || len(s.st.Ask) == 0 {
		return false
	}
	kept := s.st.Ask[:0]
	dropped := false
	for _, a := range s.st.Ask {
		if a.Pane == pane {
			dropped = true
			continue
		}
		kept = append(kept, a)
	}
	if !dropped {
		return false
	}
	s.st.Ask = kept
	if len(s.st.Ask) == 0 {
		s.st.Ask = nil
	}
	s.askWakeLocked()
	return true
}

// askAwait parks until the ask is answered, closed or the wait passes. It holds
// no lock while blocked: it checks and registers under s.mu, sleeps outside it,
// and re-checks after every wakeup. A timeout answers pending, never an error,
// because a bridge loops on it. An ask that closes under the wait is gone, so it
// answers not_found; a disconnected client releases the waiter.
func (s *server) askAwait(cl *client, id string, wait time.Duration) (*ask.AwaitReply, error) {
	deadline := time.Now().Add(wait)
	w := &askWaiter{wake: make(chan struct{}, 1)}
	for {
		s.mu.Lock()
		i := s.st.AskIndex(id)
		if i < 0 {
			s.mu.Unlock()
			return nil, ask.Refuse(ask.CodeNotFound, "no ask %q", id)
		}
		if a := s.st.Ask[i].Answer; a != nil {
			answer := *a
			s.mu.Unlock()
			return &ask.AwaitReply{Answer: &answer}, nil
		}
		s.askWatchLocked(w)
		s.mu.Unlock()

		left := time.Until(deadline)
		if left <= 0 {
			s.askUnwatch(w)
			return &ask.AwaitReply{Pending: true}, nil
		}
		if left > askPollWait {
			left = askPollWait
		}
		timer := time.NewTimer(left)
		select {
		case <-w.wake:
			timer.Stop()
		case <-timer.C:
		}
		s.askUnwatch(w)
		if cl != nil && connGone(cl.c) {
			return nil, errAskGone
		}
	}
}

// askAnswer settles one ask. It is all or nothing: the rules live in the ask
// package, and a refusal changes nothing. An ask already answered is refused as
// E_EXISTS, because a second answer would overwrite the outcome a late client
// still reads.
func (s *server) askAnswer(id string, req *ask.AnswerReq) (*ask.Answer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.st.AskIndex(id)
	if i < 0 {
		return nil, ask.Refuse(ask.CodeNotFound, "no ask %q", id)
	}
	r := &s.st.Ask[i]
	if r.Answer != nil {
		return nil, ask.Refuse(ask.CodeExists, "ask %q is already answered", id)
	}
	if err := ask.ValidateAnswer(r.Questions, req); err != nil {
		return nil, err
	}
	// The reader's own text is scrubbed the same way: an answer that quotes a
	// value is stored in state.json and rides every snapshot, and the ask store
	// is a surface another program reads unasked. The scrub keeps every byte
	// length, so the rules that validated the text still hold.
	needles := s.scrubNeedlesLocked()
	r.Answer = &ask.Answer{
		At:     askTime(),
		Cancel: req.Cancel,
		Items:  scrubAnswerItems(req.Items, needles),
		Note:   secrets.RedactText(req.Note, needles),
	}
	s.askWakeLocked()
	err := s.st.Save()
	s.pushAskLocked()
	return r.Answer, err
}

// scrubQuestions returns a copy of qs with every operator-visible string
// scrubbed: the question, the header, and each option's label, description and
// preview. The copy is what enters the store, so the request's own slices are
// never aliased into state. A nil option list stays nil; an empty list stays
// empty.
func scrubQuestions(qs []ask.Question, needles [][]byte) []ask.Question {
	out := make([]ask.Question, len(qs))
	for i, q := range qs {
		out[i] = q
		out[i].Question = secrets.RedactText(q.Question, needles)
		out[i].Header = secrets.RedactText(q.Header, needles)
		if q.Options == nil {
			continue
		}
		opts := make([]ask.Option, len(q.Options))
		for j, o := range q.Options {
			opts[j] = ask.Option{
				Label:       secrets.RedactText(o.Label, needles),
				Description: secrets.RedactText(o.Description, needles),
				Preview:     secrets.RedactText(o.Preview, needles),
			}
		}
		out[i].Options = opts
	}
	return out
}

// scrubAnswerItems returns a copy of items with the reader's text and notes
// scrubbed. The option indexes are positions, not text, and stay as they are.
func scrubAnswerItems(items []ask.Item, needles [][]byte) []ask.Item {
	if items == nil {
		return nil
	}
	out := make([]ask.Item, len(items))
	for i, it := range items {
		out[i] = it
		out[i].Text = secrets.RedactText(it.Text, needles)
		out[i].Notes = secrets.RedactText(it.Notes, needles)
	}
	return out
}

// askClose drops one ask from the store and reports whether it removed one. It
// is idempotent: an id that names nothing is a success that changed nothing. An
// answered ask stays listed until this runs, so a late client still sees the
// outcome.
func (s *server) askClose(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.st.AskIndex(id)
	if i < 0 {
		return false, nil
	}
	s.st.Ask = append(s.st.Ask[:i], s.st.Ask[i+1:]...)
	if len(s.st.Ask) == 0 {
		s.st.Ask = nil
	}
	s.askWakeLocked()
	err := s.st.Save()
	s.pushAskLocked()
	return true, err
}

// askList returns every listed ask, oldest first, as a copy so the reply is
// built outside the lock. An empty store returns an empty slice, so the JSON is
// an empty array and a client never has to special-case null.
func (s *server) askList() []ask.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ask.Request, len(s.st.Ask))
	copy(out, s.st.Ask)
	return out
}

// pushAskLocked pushes the snapshot the store change asks for, so a live client
// sees the new list. The caller holds s.mu.
func (s *server) pushAskLocked() {
	s.broadcastLocked(wire.Msg{Type: "snapshot", Snap: s.snapshotLocked()})
}

// askTime is the moment the server records: UTC, to the second, the way box
// entries are stamped.
func askTime() time.Time { return time.Now().UTC().Truncate(time.Second) }

// askReapInterval is how often the store sweeps the settled asks it no longer
// needs. A sweep that drops nothing costs one lock and no write.
const askReapInterval = time.Minute

// reapAsks drops the settled asks past ask.SettleGrace and reports how many it
// removed. An answered ask is kept for its owner to read through ask-await and
// to close, but an owner that died before the close would otherwise leave the
// entry in the session file for good.
func (s *server) reapAsks(now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var kept []ask.Request
	dropped := 0
	for _, a := range s.st.Ask {
		if a.Answer != nil && now.Sub(a.Answer.At) > ask.SettleGrace {
			dropped++
			continue
		}
		kept = append(kept, a)
	}
	if dropped == 0 {
		return 0, nil
	}
	s.st.Ask = kept
	err := s.st.Save()
	s.pushAskLocked()
	return dropped, err
}

// reapAsksLoop sweeps the store until the process ends.
func (s *server) reapAsksLoop() {
	t := time.NewTicker(askReapInterval)
	defer t.Stop()
	for range t.C {
		if _, err := s.reapAsks(time.Now().UTC()); err != nil {
			log.Printf("ask reap: %v", err)
		}
	}
}

// wireAsks flattens the store's open asks into the snapshot shape. A settled
// ask is not listed: it is kept for its owner to read and to close, and no
// client presents it again.
func wireAsks(asks []ask.Request) []wire.Ask {
	if len(asks) == 0 {
		return nil
	}
	var out []wire.Ask
	for _, a := range asks {
		if a.Answer != nil {
			continue
		}
		out = append(out, wire.Ask{
			ID: a.ID, Pane: a.Pane, ToolCallID: a.ToolCallID, At: a.At,
			Questions: wireQuestions(a.Questions),
		})
	}
	return out
}

// wireQuestions flattens one ask's questions into the wire shape.
func wireQuestions(qs []ask.Question) []wire.AskQuestion {
	if len(qs) == 0 {
		return nil
	}
	out := make([]wire.AskQuestion, len(qs))
	for i, q := range qs {
		out[i] = wire.AskQuestion{
			Question: q.Question, Header: q.Header, MultiSelect: q.MultiSelect,
			Options: wireOptions(q.Options),
		}
	}
	return out
}

// wireOptions flattens one question's options into the wire shape.
func wireOptions(opts []ask.Option) []wire.AskOption {
	if len(opts) == 0 {
		return nil
	}
	out := make([]wire.AskOption, len(opts))
	for i, o := range opts {
		out[i] = wire.AskOption{Label: o.Label, Description: o.Description, Preview: o.Preview}
	}
	return out
}
