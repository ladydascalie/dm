package server

import (
	"errors"
	"log"
	"time"

	"dm/internal/ask"
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

// askOpen lists one ask on a pane and returns the id the server gave it. A tool
// call already covered by a listed ask is refused as E_EXISTS: an open ask
// replaces nothing, and a retried tool call must not stack a second question
// set on the reader.
func (s *server) askOpen(pane string, req *ask.OpenReq) (string, error) {
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
	id := s.st.NewAskID()
	s.st.Ask = append(s.st.Ask, ask.Request{
		ID:         id,
		Pane:       pane,
		ToolCallID: req.ToolCallID,
		At:         askTime(),
		Questions:  req.Questions,
	})
	err := s.st.Save()
	s.pushAskLocked()
	return id, err
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
	r.Answer = &ask.Answer{
		At:     askTime(),
		Cancel: req.Cancel,
		Items:  req.Items,
		Note:   req.Note,
	}
	s.askWakeLocked()
	err := s.st.Save()
	s.pushAskLocked()
	return r.Answer, err
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
