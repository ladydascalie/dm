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
// resize signal and the lesson. The session itself has no build tags, so it is
// tested natively.
//
// The page talks to it through one global, set before the program starts:
//
//	dm.onOutput(fn)        fn(Uint8Array) receives the frame bytes
//	dm.input(text)         keys, as a terminal would send them
//	dm.resize(cols, rows)  the renderer's size in cells
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

// bridge is the page's side of the program: the frame sink the client writes
// to, and the queues the page fills. Output written before the page registers
// a callback is held and delivered to it, so a slow page never loses a frame.
type bridge struct {
	mu      sync.Mutex
	out     js.Value
	events  js.Value
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

// report sends one session event to the page and folds it into the lesson, so a
// step advances on what the session did and the page only draws the result.
func report(events chan map[string]any, l *lesson.Lesson, ev map[string]any) {
	pushEvent(events, ev)
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
	if !l.Feed(le) {
		return
	}
	if _, step, ok := l.Current(); ok {
		pushEvent(events, lessonEvent("lesson.step", l, step))
		return
	}
	pushEvent(events, map[string]any{"type": "lesson.finish", "title": l.Track().Title})
}

// lessonEvent is one step as the page draws it.
func lessonEvent(kind string, l *lesson.Lesson, step lesson.Step) map[string]any {
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

func main() {
	size := &sizer{cols: 100, rows: 30}
	b := &bridge{}
	keys := make(chan []byte, 64)
	winch := make(chan os.Signal, 1)

	// The prologue Run writes on a real machine: the alternate screen, a
	// hidden cursor, a clear, and the mouse modes, without which the rail is
	// never sent a click. Attach owns no terminal, so the harness opens the
	// screen it draws on.
	_, _ = b.Write([]byte("\x1b[?1049h\x1b[?25l\x1b[2J\x1b[H"))
	_, _ = b.Write([]byte("\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1006h\x1b[?2004h"))

	clientConn, serverConn := net.Pipe()
	events := make(chan map[string]any, 64)
	track := lesson.Tracks[0]
	l := lesson.Start(track)
	if _, step, ok := l.Current(); ok {
		pushEvent(events, lessonEvent("lesson.step", l, step))
	}
	forward := func(ev map[string]any) { report(events, l, ev) }

	sess := websession.New(size.get, forward, serverConn)
	go sess.Serve()
	go func() {
		h := client.Harness{
			Conn: clientConn, Out: b, Keys: keys, Winch: winch,
			Size: size.get, Session: "spike",
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

	// The ladder: one timer for the whole tour, reporting each rung once per
	// step. The page draws what it hears.
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		last := lesson.NoHelp
		for now := range t.C {
			level := l.Ladder(now)
			if level == last {
				continue
			}
			last = level
			if at, step, ok := l.Current(); ok {
				pushEvent(events, map[string]any{
					"type": "lesson.hint", "index": at + 1, "level": int(level),
					"hint": step.Hint,
				})
			}
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
					time.Sleep(250 * time.Millisecond)
				}
			}()
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
