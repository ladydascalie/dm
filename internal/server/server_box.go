package server

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"dm/internal/box"
	"dm/internal/notify"
	"dm/internal/wire"
)

// boxPost appends the request as one entry, trims the log and saves state under
// one lock. The server owns seq and at: the request carries neither, and the
// strict decoder rejects the fields if a caller tries to send them.
func (s *server) boxPost(req *box.PostReq) (box.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, err := s.boxStoreLocked(req)
	// A notify/ entry renders after it is logged, so the log is never
	// suppressed. SystemNotify runs async and never blocks the post.
	if box.IsNotifyTopic(e.Key) {
		pane := strings.TrimPrefix(e.Key, box.NotifyPrefix)
		s.notifyBoxEntryLocked(e, pane != "" && s.operatorSeesLocked(pane))
	}
	return e, err
}

// boxStoreLocked appends the request as one entry, trims the log and saves
// state. The caller holds s.mu and renders any notify/ entry itself through
// notifyBoxEntryLocked, so a caller that already made the suppression decision
// can keep it.
func (s *server) boxStoreLocked(req *box.PostReq) (box.Entry, error) {
	if req.From != "" && !s.paneInTreeLocked(req.From) {
		return box.Entry{}, fmt.Errorf("box: no pane %q", req.From)
	}
	s.st.BoxSeq++
	e := box.Entry{
		Seq:   s.st.BoxSeq,
		At:    time.Now().UTC().Truncate(time.Second),
		From:  req.From,
		Key:   req.Key,
		State: req.State,
		Msg:   req.Msg,
		Body:  req.Body,
	}
	s.st.Box = append(s.st.Box, e)
	s.st.TrimBox()
	s.boxWakeLocked()
	// The entry stays in memory even when the save fails, so the running
	// session keeps a consistent log. The error says the state file is stale.
	err := s.st.Save()
	// Every attached client learns the log moved. An open box view asks for
	// the whole log again, so its picture is never a guess.
	s.broadcastLocked(wire.Msg{Type: "box"})
	return e, err
}

// notifyBoxEntryLocked renders one notify/ entry as a desktop notification:
// the topic and state as the title, the message as the body. A true suppress
// drops only the pop; the entry is logged either way. The caller holds s.mu.
func (s *server) notifyBoxEntryLocked(e box.Entry, suppress bool) {
	if suppress {
		return
	}
	notify.SystemNotify(e.Key+" "+string(e.State), e.Msg)
}

// boxList returns the entries newest first that pass the request's topic and
// cursor, capped at the clamped limit. Trimmed reports a cursor that names a
// row the box has already dropped. A read that names its pane leaves a watch
// rooted at the highest seq it served.
func (s *server) boxList(req *box.ListReq) ([]box.Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.boxReadLocked(req.From); err != nil {
		return nil, false, err
	}
	out := make([]box.Entry, 0, req.Limit)
	highest := 0
	for i := len(s.st.Box) - 1; i >= 0 && len(out) < req.Limit; i-- {
		if !box.Match(s.st.Box[i], req.Key, req.KeyPrefix, req.AfterSeq) {
			continue
		}
		out = append(out, s.st.Box[i])
		highest = max(highest, s.st.Box[i].Seq)
	}
	if req.From != "" {
		s.boxSetWatchLocked(boxWatchKeyOf(req.From, req.Key, req.KeyPrefix), highest, false)
	}
	return out, s.boxTrimmedLocked(req.AfterSeq), nil
}

// boxTrimmedLocked reports whether the cursor sits behind the oldest row the
// box still holds, so entries between the two are gone. The caller holds s.mu.
func (s *server) boxTrimmedLocked(after *int) bool {
	if after == nil || *after < 0 || len(s.st.Box) == 0 {
		return false
	}
	return *after+1 < s.st.Box[0].Seq
}

// boxClear removes every entry the request matches: the exact key, or every
// key under the prefix, or the whole log when the request names neither, and
// reports how many went. BoxSeq is untouched, so the next post continues the
// sequence instead of reusing a number. The removal stays applied in memory;
// a failed save returns its error with the count.
func (s *server) boxClear(req *box.ClearReq) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := make([]box.Entry, 0, len(s.st.Box))
	for _, e := range s.st.Box {
		if box.Match(e, req.Key, req.KeyPrefix, nil) {
			continue
		}
		kept = append(kept, e)
	}
	cleared := len(s.st.Box) - len(kept)
	if cleared == 0 {
		return 0, nil
	}
	if len(kept) == 0 {
		kept = nil
	}
	s.st.Box = kept
	s.boxWakeLocked()
	// The clear stays applied in memory even when the save fails. The count
	// reports what went and the error says the state file is stale.
	err := s.st.Save()
	s.broadcastLocked(wire.Msg{Type: "box"})
	return cleared, err
}

// boxWaiter is one msg-wait parked on s.boxWaiters. The buffered channel holds
// one nudge, so a wakeup that arrives between two checks is not lost.
type boxWaiter struct {
	wake chan struct{}
}

// boxPollWait bounds how long a wait sleeps before it looks at the client's
// socket for a hangup. The client's read loop is inside the wait and cannot see
// the EOF itself, so the wait polls for it. A missed hangup costs one timeout,
// never a live waiter past this interval.
const boxPollWait = 100 * time.Millisecond

// boxWatchLocked registers a waiter. The caller holds s.mu and registers
// before it unlocks, so a post cannot slip between the last check and the
// registration.
func (s *server) boxWatchLocked(w *boxWaiter) {
	if s.boxWaiters == nil {
		s.boxWaiters = map[*boxWaiter]bool{}
	}
	s.boxWaiters[w] = true
}

// boxUnwatch removes a waiter, on match, timeout or disconnect.
func (s *server) boxUnwatch(w *boxWaiter) {
	s.mu.Lock()
	delete(s.boxWaiters, w)
	s.mu.Unlock()
}

// boxWakeLocked nudges every waiter. The caller holds s.mu, and the non
// blocking send means a waiter that is already running keeps its one pending
// nudge.
func (s *server) boxWakeLocked() {
	for w := range s.boxWaiters {
		select {
		case w.wake <- struct{}{}:
		default:
		}
	}
}

// boxWatchKey is the identity of one watch: the reader pane and its filter.
// The filter is an exact key or a branch prefix, never both, so the three
// fields name one watch.
type boxWatchKey struct {
	pane      string
	key       string
	keyPrefix string
}

// boxWatchTTL is how long an unparked watch survives without a fresh read. A
// parked watch is a live wait, so it never ages out.
const boxWatchTTL = 10 * time.Minute

// boxWatchKeyOf is the registry key a read leaves behind.
func boxWatchKeyOf(from, key, prefix string) boxWatchKey {
	return boxWatchKey{pane: from, key: key, keyPrefix: prefix}
}

// boxReadLocked prepares a read: it drops the expired watches, then checks the
// reader pane against the session tree. A read without a pane records nothing
// and passes. The caller holds s.mu.
func (s *server) boxReadLocked(from string) error {
	s.boxExpireLocked(time.Now())
	if from != "" && !s.paneInTreeLocked(from) {
		return fmt.Errorf("box: no pane %q", from)
	}
	return nil
}

// boxExpireLocked drops the watches whose pane left the tree and the unparked
// watches older than boxWatchTTL. A parked watch stays whatever its age. One
// push covers the drop, because a watcher page asks for the whole list again.
// The caller holds s.mu.
func (s *server) boxExpireLocked(now time.Time) {
	dropped := false
	for k, w := range s.watches {
		if !s.paneInTreeLocked(k.pane) || (!w.Parked && now.Sub(w.At) > boxWatchTTL) {
			delete(s.watches, k)
			dropped = true
		}
	}
	if dropped {
		s.broadcastLocked(wire.Msg{Type: "watch"})
	}
}

// boxSetWatchLocked records one read. The cursor only grows, parked follows the
// caller and at is now in UTC to the second. It pushes when the watch appears,
// advances or changes its parked state, which are the changes the page shows.
// The caller holds s.mu.
func (s *server) boxSetWatchLocked(k boxWatchKey, cursor int, parked bool) {
	old, existed := s.watches[k]
	w := box.Watch{
		Pane: k.pane, Key: k.key, KeyPrefix: k.keyPrefix,
		Cursor: max(old.Cursor, cursor), Parked: parked,
		At: time.Now().UTC().Truncate(time.Second),
	}
	s.watches[k] = w
	if !existed || w.Cursor != old.Cursor || w.Parked != old.Parked {
		s.broadcastLocked(wire.Msg{Type: "watch"})
	}
}

// boxWatchers returns every live watch, sorted by pane, key and keyPrefix. The
// reply is a whole-list read, so it drops the expired watches first.
func (s *server) boxWatchers() []box.Watch {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.boxExpireLocked(time.Now())
	out := make([]box.Watch, 0, len(s.watches))
	for _, w := range s.watches {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pane != out[j].Pane {
			return out[i].Pane < out[j].Pane
		}
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].KeyPrefix < out[j].KeyPrefix
	})
	return out
}

// boxNewestLocked returns the newest entry under the topic filter: the whole
// log when both fields are empty. The caller holds s.mu.
func (s *server) boxNewestLocked(key, prefix string) (box.Entry, bool) {
	for i := len(s.st.Box) - 1; i >= 0; i-- {
		if box.Match(s.st.Box[i], key, prefix, nil) {
			return s.st.Box[i], true
		}
	}
	return box.Entry{}, false
}

// boxFindLocked is the read a wait blocks on. With a cursor it returns the
// oldest entry past it that matches the topic and the state, so a subscriber
// sees every message in order and no message is skipped. Without a cursor it
// returns the newest matching entry and reports whether its state satisfies
// the wait, which is the status board. The caller holds s.mu.
func (s *server) boxFindLocked(req *box.WaitReq) (box.Entry, bool, bool) {
	trimmed := s.boxTrimmedLocked(req.AfterSeq)
	if req.AfterSeq == nil {
		e, ok := s.boxNewestLocked(req.Key, req.KeyPrefix)
		if !ok {
			return box.Entry{}, false, false
		}
		return e, req.State == "" || e.State == req.State, false
	}
	for _, e := range s.st.Box {
		if !box.Match(e, req.Key, req.KeyPrefix, req.AfterSeq) {
			continue
		}
		if req.State != "" && e.State != req.State {
			continue
		}
		return e, true, trimmed
	}
	return box.Entry{}, false, trimmed
}

// boxWait blocks until the read finds an entry. Without a cursor it waits for
// the newest entry under the topic to carry the state; with one it waits for
// the oldest entry past the cursor, state-filtered when a state is named. It
// holds no lock while blocked: it checks and registers under s.mu, sleeps
// outside it, and re-checks after every wakeup. It returns the entry and
// whether the cursor named a dropped row. On timeout it returns the error
// "box: wait timed out" plus the newest entry it saw. A disconnected client
// releases the waiter instead of holding it to the timeout.
//
// A wait that names its pane is a watch: it registers parked before the first
// check, so it shows up before any post, advances to the entry it serves, and
// unparks on return, timeout or a gone client.
func (s *server) boxWait(cl *client, req *box.WaitReq) (box.Entry, bool, error) {
	deadline := time.Now().Add(time.Duration(req.TimeoutMs) * time.Millisecond)
	w := &boxWaiter{wake: make(chan struct{}, 1)}
	k := boxWatchKeyOf(req.From, req.Key, req.KeyPrefix)

	s.mu.Lock()
	if err := s.boxReadLocked(req.From); err != nil {
		s.mu.Unlock()
		return box.Entry{}, false, err
	}
	if req.From != "" {
		s.boxSetWatchLocked(k, 0, true)
	}
	s.mu.Unlock()

	// unpark clears the parked mark of the watch this wait left behind.
	unpark := func() {
		if req.From == "" {
			return
		}
		s.mu.Lock()
		s.boxSetWatchLocked(k, 0, false)
		s.mu.Unlock()
	}

	for {
		s.mu.Lock()
		e, ok, trimmed := s.boxFindLocked(req)
		if ok {
			if req.From != "" {
				s.boxSetWatchLocked(k, e.Seq, false)
			}
			s.mu.Unlock()
			return e, trimmed, nil
		}
		last, _ := s.boxNewestLocked(req.Key, req.KeyPrefix)
		s.boxWatchLocked(w)
		s.mu.Unlock()

		wait := time.Until(deadline)
		if wait <= 0 {
			s.boxUnwatch(w)
			unpark()
			return last, trimmed, fmt.Errorf("box: wait timed out")
		}
		if wait > boxPollWait {
			wait = boxPollWait
		}
		timer := time.NewTimer(wait)
		select {
		case <-w.wake:
			timer.Stop()
		case <-timer.C:
		}
		s.boxUnwatch(w)
		if connGone(cl.c) {
			unpark()
			return box.Entry{}, trimmed, fmt.Errorf("box: client gone")
		}
	}
}

// errBoxGone reports a stream whose connection went. A stream has no reply, so
// the handler stops without one and the watch it left behind ages out like any
// other.
var errBoxGone = errors.New("box: client gone")

// boxSub streams the log to one connection: the oldest entry past the cursor
// that matches the topic and the state, then the next, for as long as the
// connection lives. It scans and registers under s.mu and delivers outside it,
// so a post never waits on a socket. Nothing matching parks the handler on a
// boxWaiter with the wake channel a post already nudges. The watch it leaves is
// parked from the first frame to the last, its cursor follows the stream, and a
// disconnected client releases the handler through boxPollWait.
func (s *server) boxSub(cl *client, req *box.SubReq) error {
	cursor := 0
	if req.AfterSeq != nil {
		cursor = *req.AfterSeq
	}
	k := boxWatchKeyOf(req.From, req.Key, req.KeyPrefix)
	w := &boxWaiter{wake: make(chan struct{}, 1)}

	// unpark clears the parked mark of the watch this stream left behind. It
	// takes s.mu itself, so no caller holds it.
	unpark := func() {
		if req.From == "" {
			return
		}
		s.mu.Lock()
		s.boxSetWatchLocked(k, cursor, false)
		s.mu.Unlock()
	}

	s.mu.Lock()
	if err := s.boxReadLocked(req.From); err != nil {
		s.mu.Unlock()
		return err
	}
	// The gap is decided once, at the start: an empty log has no gap, so the
	// first entry that arrives later carries trimmed false.
	trimmed := s.boxTrimmedLocked(req.AfterSeq)
	if req.From != "" {
		s.boxSetWatchLocked(k, cursor, true)
	}
	s.mu.Unlock()

	for {
		s.mu.Lock()
		before := cursor
		e, ok := s.boxSubFindLocked(req, &cursor)
		if req.From != "" && cursor != before {
			// The watch stays parked while the stream runs; only its cursor
			// moves, so a view shows the reader where it is.
			s.boxSetWatchLocked(k, cursor, true)
		}
		if !ok {
			s.boxWatchLocked(w)
		}
		s.mu.Unlock()

		if ok {
			frame := box.SubFrame{Entry: e, Trimmed: trimmed}
			if !cl.deliver(wire.Msg{Type: "msg", Res: jsonRaw(frame)}) {
				// The frame never reached the queue, so the cursor must not
				// pass it: a reconnect resumes at this entry.
				s.mu.Lock()
				cursor = before
				if req.From != "" {
					s.boxSetWatchLocked(k, cursor, true)
				}
				s.mu.Unlock()
				unpark()
				return errBoxGone
			}
			trimmed = false
			continue
		}

		timer := time.NewTimer(boxPollWait)
		select {
		case <-w.wake:
			timer.Stop()
		case <-timer.C:
		}
		s.boxUnwatch(w)
		if connGone(cl.c) {
			unpark()
			return errBoxGone
		}
	}
}

// boxSubFindLocked is the read a stream blocks on: the oldest entry past the
// cursor that matches the topic and, when one is named, the state. It advances
// the cursor over every entry it scans, the state-skipped ones included, so a
// reconnect never rescans them. The caller holds s.mu.
func (s *server) boxSubFindLocked(req *box.SubReq, cursor *int) (box.Entry, bool) {
	for _, e := range s.st.Box {
		if e.Seq <= *cursor {
			continue
		}
		*cursor = e.Seq
		if !box.Match(e, req.Key, req.KeyPrefix, nil) {
			continue
		}
		if req.State != "" && e.State != req.State {
			continue
		}
		return e, true
	}
	return box.Entry{}, false
}
