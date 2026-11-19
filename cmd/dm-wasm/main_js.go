//go:build js && wasm

// Command dm-wasm runs the real dm client in a browser tab. It is the spike
// for a guided tour: proof that internal/client, which owns a terminal on a
// real machine, runs with none.
//
// The client is the shipped one. The session on the other end of the pipe is
// internal/websession: workspaces, windows and panes with a pretend shell in
// each, no pty, no pane daemon and no server. See README.md for what that means
// and what is left.
//
// This file is the page bridge only: the frame sink, the key channel, the
// resize signal, the hub, the saved progress and the lesson. The session itself
// has no build tags, so it is tested natively, and the tour's progress rules are
// pure functions in progress.go, which is tested natively too.
//
// The page talks to it through one global, set before the program starts:
//
//	dm.onOutput(fn)        fn(Uint8Array) receives the frame bytes
//	dm.input(text)         keys, as a terminal would send them
//	dm.resize(cols, rows)  the renderer's size in cells
//	dm.onClipboard(fn)     fn(text) bool receives a copy; false refuses it
//	dm.onEvent(fn)         fn(event) receives one event to draw
//	dm.tracks()            every track, the one being played marked
//	dm.pick(id)            play that track from its first step
//	dm.restart()           play the current track from its first step
//	dm.showMe()            press the current step's keys
//	dm.last()              the last request the fake session handled
package main

import (
	"net"
	"os"
	"sync"
	"syscall/js"
	"time"

	"dm/internal/client"
	"dm/internal/lesson"
	"dm/internal/websession"
)

// The fast forward and show me leave this long between keystrokes, so each read
// reaches the session on its own, as a person's would.
const keyGap = 250 * time.Millisecond

// stepWait is how long one fast forward step gets to be proved before the
// handover gives up on it.
const stepWait = 5 * time.Second

// bridge is the page's side of the program: the frame sink the client writes
// to, and the queues the page fills. Output written before the page registers
// a callback is held and delivered to it, so a slow page never loses a frame.
type bridge struct {
	mu      sync.Mutex
	out     js.Value
	events  js.Value
	clip    js.Value
	pending [][]byte
	held    []map[string]any
}

// Write sends one frame to the page.
func (b *bridge) Write(p []byte) (int, error) {
	n := len(p)
	b.mu.Lock()
	fn := b.out
	if fn.IsUndefined() || fn.IsNull() {
		b.pending = append(b.pending, append([]byte(nil), p...))
		b.mu.Unlock()
		return n, nil
	}
	b.mu.Unlock()
	b.send(fn, p)
	return n, nil
}

// send copies b into a Uint8Array and calls fn with it.
func (b *bridge) send(fn js.Value, p []byte) {
	buf := js.Global().Get("Uint8Array").New(len(p))
	js.CopyBytesToJS(buf, p)
	fn.Invoke(buf)
}

// handover is the boot's fast forward. While it runs the lesson still advances,
// but no step and no hint is drawn: the card does not flash through steps the
// person already did, and it is drawn once, at the step the handover reached.
// A halted handover is a page on its way out: nothing it saves may write the old
// step back over the record the reload boots from.
type handover struct {
	mu     sync.Mutex
	busy   bool
	halted bool
}

// running reports whether the fast forward still owns the card.
func (h *handover) running() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.busy
}

// done hands the card back to the lesson.
func (h *handover) done() {
	h.mu.Lock()
	h.busy = false
	h.mu.Unlock()
}

// halt stops the fast forward and its saves. A page that is about to reload with
// a fresh record calls it first.
func (h *handover) halt() {
	h.mu.Lock()
	h.busy, h.halted = false, true
	h.mu.Unlock()
}

// live reports whether a save may still land.
func (h *handover) live() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return !h.halted
}

// report sends one event to the page and folds it into the lesson, so a step
// advances on what the session did and the page only draws the result.
func report(events chan map[string]any, l *lesson.Lesson, hand *handover, ev map[string]any) {
	pushEvent(events, ev)
	if kind, _ := ev["type"].(string); kind == "session" {
		// The session's own shape is the coach: the page draws it, and the
		// lesson never sees it.
		return
	}
	if !l.Feed(asLessonEvent(ev)) {
		return
	}
	if hand.running() {
		// The fast forward draws the card when it hands over.
		return
	}
	if at, step, ok := l.Current(); ok {
		if hand.live() {
			saveProgress(progress{Track: l.Track().ID, Step: at})
		}
		pushEvent(events, stepEvent("lesson.step", l, step))
		return
	}
	if hand.live() {
		clearStep(l)
	}
	pushEvent(events, map[string]any{"type": "lesson.finish", "title": l.Track().Title})
}

// asLessonEvent is one session event as the engine sees it. The engine reads
// four fields and the page's session event carries none of its business.
func asLessonEvent(ev map[string]any) lesson.Event {
	le := lesson.Event{}
	if v, ok := ev["type"].(string); ok {
		le.Type = v
	}
	if v, ok := ev["name"].(string); ok {
		le.Name = v
	}
	if v, ok := ev["line"].(string); ok {
		le.Line = v
	}
	if v, ok := ev["panes"].(int); ok {
		le.Panes = v
	}
	return le
}

// stepEvent is one step as the page draws it.
func stepEvent(kind string, l *lesson.Lesson, step lesson.Step) map[string]any {
	keys := make([]any, 0, len(step.Keys))
	for _, k := range step.Keys {
		keys = append(keys, k.Label)
	}
	at, _, _ := l.Current()
	return map[string]any{
		"type": kind, "track": l.Track().Title, "index": at + 1,
		"total": len(l.Track().Steps), "title": step.Title,
		"note": step.Note, "hint": step.Hint, "keys": keys,
	}
}

// pushEvent queues one event for the page, dropping it when nobody is reading:
// a lesson must never stall the session behind a slow card.
func pushEvent(events chan map[string]any, ev map[string]any) {
	select {
	case events <- ev:
	default:
	}
}

// drainEvents hands events to the page's callback as they arrive. Events that
// came before the page registered are held and delivered in order.
func drainEvents(b *bridge, events chan map[string]any) {
	for ev := range events {
		b.mu.Lock()
		fn := b.events
		if fn.IsUndefined() || fn.IsNull() {
			b.held = append(b.held, ev)
			b.mu.Unlock()
			continue
		}
		b.mu.Unlock()
		fn.Invoke(js.ValueOf(ev))
	}
}

// sizer is the renderer's size in cells, written by the page and read by the
// session and the client.
type sizer struct {
	mu   sync.Mutex
	cols int
	rows int
}

// get reads the size.
func (s *sizer) get() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cols, s.rows
}

// set writes the size, refusing one too small to lay out. It asks the session
// package, so the page and the fake hold one gate.
func (s *sizer) set(cols, rows int) {
	if !websession.SizeOK(cols, rows) {
		return
	}
	s.mu.Lock()
	s.cols, s.rows = cols, rows
	s.mu.Unlock()
}

// resizeSignal is the winch the page sends. A browser has no SIGWINCH, and the
// client only reads the channel, so the signal is a marker with no meaning.
type resizeSignal struct{}

func (resizeSignal) String() string { return "resize" }
func (resizeSignal) Signal()        {}

// storage is the browser's localStorage, or an empty value when the page has
// none to offer.
func storage() js.Value {
	ls := js.Global().Get("localStorage")
	if ls.Type() != js.TypeObject {
		return js.Value{}
	}
	return ls
}

// savedRaw reads the saved record, empty when it cannot be read. The wasm side
// is the only writer; the page only draws what it is told.
func savedRaw() (raw string) {
	ls := storage()
	if ls.IsUndefined() || ls.IsNull() {
		return ""
	}
	defer func() { _ = recover() }()
	return ls.Call("getItem", progressKey).String()
}

// saveProgress writes the record. A browser that refuses storage leaves the
// tour running: progress is a convenience, not a rule.
func saveProgress(p progress) {
	ls := storage()
	if ls.IsUndefined() || ls.IsNull() {
		return
	}
	defer func() { _ = recover() }()
	ls.Call("setItem", progressKey, encodeProgress(p))
}

// clearStep drops the saved step and keeps the chosen track, so a finished,
// restarted or switched track plays from its first step next time.
func clearStep(l *lesson.Lesson) {
	saveProgress(progress{Track: l.Track().ID})
}

// reload asks the browser for a fresh session. A track switch boots a new
// lesson, a new session and a new client, so nothing of the old track is left
// behind in either.
func reload() {
	loc := js.Global().Get("location")
	if loc.Type() == js.TypeObject {
		loc.Call("reload")
	}
}

// resume replays the steps already done and then hands the card over. It presses
// each step's keys through the same channel show me uses and waits for the
// lesson to move on before the next step, so the session ends in the shape steps
// 1..N built. A step that does not advance in time ends the fast forward where
// the lesson really reached: it never loops, and it never presses a step twice.
func resume(keys chan []byte, l *lesson.Lesson, hand *handover, events chan map[string]any, at int) {
	tr := l.Track()
	stalled := false
	for step := 0; step < at; step++ {
		for _, k := range stepKeys(tr, step) {
			select {
			case keys <- []byte(k):
			case <-time.After(time.Second):
			}
			time.Sleep(keyGap)
		}
		if !waitPast(l, step) {
			// The session did not do what the saved step promised. The saved
			// step is cleared and the card is the step the lesson really
			// reached.
			stalled = true
			break
		}
	}
	hand.done()
	handOver(events, l, hand, !stalled)
}

// waitPast waits for the lesson to move beyond step i, reporting whether it did.
func waitPast(l *lesson.Lesson, i int) bool {
	end := time.Now().Add(stepWait)
	for {
		if now, _, _ := l.Current(); now > i {
			return true
		}
		if time.Now().After(end) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// handOver draws the step the fast forward reached and writes the one record the
// handover owns. One decision, one writer: handoverSave says whether there is a
// record at all, so no branch can write after a pick has halted the page.
func handOver(events chan map[string]any, l *lesson.Lesson, hand *handover, kept bool) {
	at, step, ok := l.Current()
	if p, write := handoverSave(l.Track(), at, kept && ok, hand.live()); write {
		saveProgress(p)
	}
	if ok {
		pushEvent(events, stepEvent("lesson.step", l, step))
		return
	}
	pushEvent(events, map[string]any{"type": "lesson.finish", "title": l.Track().Title})
}

// clipboard hands text to the page's clipboard and reports whether the page
// took it. A page that registered none has no clipboard, so the copy did not
// happen and the client must not say it did.
func (b *bridge) clipboard(text string) bool {
	b.mu.Lock()
	fn := b.clip
	b.mu.Unlock()
	if fn.IsUndefined() || fn.IsNull() {
		return false
	}
	return fn.Invoke(js.ValueOf(text)).Truthy()
}

func main() {
	size := &sizer{cols: 100, rows: 30}
	b := &bridge{}
	keys := make(chan []byte, 64)
	winch := make(chan os.Signal, 1)
	events := make(chan map[string]any, 64)

	// The saved record decides the track and the step. It is hostile input:
	// resumePlan answers the first track's first step for anything it cannot
	// trust.
	track, resumeAt := resumePlan(savedRaw())
	l := lesson.Start(track)
	hand := &handover{busy: resumeAt > 0}
	forward := func(ev map[string]any) { report(events, l, hand, ev) }

	// The hub first, so the page can name the track it plays and say it is
	// resuming before any step is drawn. A boot that resumes draws no step:
	// the fast forward draws the one the person reloaded on.
	pushEvent(events, map[string]any{
		"type": "lesson.tracks", "tracks": trackList(track.ID),
		"resumeAt": resumeAt, "resuming": resumeAt > 0,
	})
	if resumeAt == 0 {
		if _, step, ok := l.Current(); ok {
			pushEvent(events, stepEvent("lesson.step", l, step))
		}
	}

	// The prologue Run writes on a real machine: the alternate screen, a
	// hidden cursor, a clear, and the mouse modes, without which the rail is
	// never sent a click. Attach owns no terminal, so the harness opens the
	// screen it draws on.
	_, _ = b.Write([]byte("\x1b[?1049h\x1b[?25l\x1b[2J\x1b[H"))
	_, _ = b.Write([]byte("\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1006h\x1b[?2004h"))

	clientConn, serverConn := net.Pipe()
	sess := websession.New(size.get, forward, serverConn)
	go sess.Serve()
	go func() {
		h := client.Harness{
			Conn: clientConn, Out: b, Keys: keys, Winch: winch,
			Size: size.get, Session: "spike",
			Clipboard: b.clipboard,
			OnEvent: func(e client.Event) {
				ev := map[string]any{"type": e.Type}
				if e.Name != "" {
					ev["name"] = e.Name
				}
				if e.Type == "state" {
					ev["panes"] = e.Panes
					ev["focus"] = e.Focus
					ev["rail"] = e.Rail
					ev["zoom"] = e.Zoom
					ev["sync"] = e.Sync
				}
				forward(ev)
			},
		}
		_, _ = client.Attach(h)
	}()
	go drainEvents(b, events)
	if resumeAt > 0 {
		// The keys queue while the client attaches, so the first read loses
		// nothing.
		go resume(keys, l, hand, events, resumeAt)
	}

	// The ladder: one timer for the whole tour, reporting each rung once per
	// step. The page draws what it hears. It waits while the fast forward owns
	// the card, so a step it is replaying is not offered help.
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		last := lesson.NoHelp
		lastStep := -1
		for now := range t.C {
			if hand.running() {
				continue
			}
			level := l.Ladder(now)
			at, step, ok := l.Current()
			if !ok {
				continue
			}
			if at != lastStep {
				// A new step climbs on its own clock: the rung the last step
				// reached says nothing about this one.
				lastStep, last = at, lesson.NoHelp
			}
			if level == last {
				continue
			}
			last = level
			if level < lesson.Hint {
				// The bottom rung is not help: a step that has just arrived has
				// not stalled, so its hint stays back.
				continue
			}
			pushEvent(events, map[string]any{
				"type": "lesson.hint", "index": at + 1, "level": int(level),
				"hint": step.Hint,
			})
		}
	}()

	api := map[string]any{
		"showMe": js.FuncOf(func(_ js.Value, args []js.Value) any {
			// Show me presses the keys through the same channel the page types
			// on, one at a time, so the caps light and the step advances the
			// way it does for a person.
			stepKeys := l.Keys()
			go func() {
				for _, k := range stepKeys {
					select {
					case keys <- []byte(k.Bytes):
					case <-time.After(time.Second):
					}
					time.Sleep(keyGap)
				}
			}()
			return nil
		}),
		"tracks": js.FuncOf(func(_ js.Value, args []js.Value) any {
			// The hub's content, from the same list the boot event carries, so
			// the API and the page cannot disagree about a track.
			return js.ValueOf(trackList(l.Track().ID))
		}),
		"pick": js.FuncOf(func(_ js.Value, args []js.Value) any {
			// A track switch starts from a clean session: the choice is saved
			// with no step, and the reload boots the new track at its first
			// step. A name no track answers changes nothing.
			if len(args) == 0 {
				return nil
			}
			id := args[0].String()
			if _, ok := lookupTrack(id); !ok {
				return nil
			}
			// Nothing still running may write a step back: the reload boots
			// the track the record names.
			hand.halt()
			saveProgress(progress{Track: id})
			reload()
			return nil
		}),
		"restart": js.FuncOf(func(_ js.Value, args []js.Value) any {
			// A restart keeps the track and clears its step.
			hand.halt()
			clearStep(l)
			reload()
			return nil
		}),
		"onOutput": js.FuncOf(func(_ js.Value, args []js.Value) any {
			b.mu.Lock()
			b.out = args[0]
			held := b.pending
			b.pending = nil
			b.mu.Unlock()
			for _, p := range held {
				b.send(b.out, p)
			}
			return nil
		}),
		"input": js.FuncOf(func(_ js.Value, args []js.Value) any {
			select {
			case keys <- []byte(args[0].String()):
			default: // a page that types faster than the client reads
			}
			return nil
		}),
		"onClipboard": js.FuncOf(func(_ js.Value, args []js.Value) any {
			b.mu.Lock()
			b.clip = args[0]
			b.mu.Unlock()
			return nil
		}),
		"onEvent": js.FuncOf(func(_ js.Value, args []js.Value) any {
			b.mu.Lock()
			b.events = args[0]
			held := b.held
			b.held = nil
			b.mu.Unlock()
			for _, ev := range held {
				b.events.Invoke(js.ValueOf(ev))
			}
			return nil
		}),
		"last": js.FuncOf(func(_ js.Value, args []js.Value) any {
			return sess.LastRequest()
		}),
		"resize": js.FuncOf(func(_ js.Value, args []js.Value) any {
			size.set(args[0].Int(), args[1].Int())
			select {
			case winch <- resizeSignal{}:
			default:
			}
			return nil
		}),
	}
	js.Global().Set("dm", js.ValueOf(api))
	if ready := js.Global().Get("onDmReady"); ready.Type() == js.TypeFunction {
		ready.Invoke(js.Global().Get("dm"))
	}
	select {}
}
