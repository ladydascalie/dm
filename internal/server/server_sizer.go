package server

import (
	"dm/internal/wire"
)

// A sizing client owns the size of every layout pane's pty. A TUI draws the
// panes as cells and takes the layout's rectangles; a pixel client such as
// insaniterm draws them as glyphs, so only it knows how many cells each pane's
// font fits, and it claims them one resize frame at a time. The client that
// attached last is the sizing client. A client that never claims leaves every
// pane on its layout rectangle, which is what a TUI sees.
//
// The claims are memory only. A restart drops them and the attached client
// claims again, exactly like a fresh attach. A claim survives until the sizing
// client changes: it is that client's picture, and nobody else can repair it.

// paneSize is one pane's claimed pty size in cells.
type paneSize struct {
	cols, rows int
}

// setSizerLocked makes cl the sizing client and reports whether the sizing
// client changed. A nil cl means no client sizes the panes, so every pane falls
// back to its layout rectangle. Every claim belongs to the outgoing client: a
// new sizing client starts from the layout, never from sizes it never asked
// for. The caller holds s.mu.
func (s *server) setSizerLocked(cl *client) bool {
	if s.sizer == cl {
		return false
	}
	s.sizer = cl
	s.claims = nil
	return true
}

// newestClientLocked is the client that attached last: the next sizing client
// when the current one goes. Anonymous clients count with seq zero, so a caller
// that registered a client without an attach frame still gets an answer. The
// caller holds s.mu.
func (s *server) newestClientLocked() *client {
	var best *client
	for cl := range s.clients {
		if best == nil || cl.seq > best.seq {
			best = cl
		}
	}
	return best
}

// pushSizer tells every attached client whether it sizes the panes. A client is
// told once on attach and every client is told again on a change, so the old
// sizing client stops fitting its terminals to the layout rect.
func (s *server) pushSizer() {
	type tell struct {
		cl     *client
		sizing bool
	}
	s.mu.Lock()
	tells := make([]tell, 0, len(s.clients))
	for cl := range s.clients {
		tells = append(tells, tell{cl: cl, sizing: cl == s.sizer})
	}
	s.mu.Unlock()
	for _, t := range tells {
		sizing := t.sizing
		t.cl.deliver(wire.Msg{Type: "sizer", Sizing: &sizing})
	}
}

// claimPaneSize records one layout pane's claimed size from the sizing client
// and pushes it into the pane's pty. A claim from any other client is refused:
// it changes no claim, no pty and no client screen. The frame named a pane, so
// it is never a client's own screen resize, and it is consumed either way.
func (s *server) claimPaneSize(cl *client, id string, cols, rows int) {
	if cols < 1 || rows < 1 {
		return
	}
	// Clamp before the pty's uint16 winsize sees it: a wrap would size the
	// pane to something the client never asked for.
	if cols > maxWinCols {
		cols = maxWinCols
	}
	if rows > maxWinRows {
		rows = maxWinRows
	}
	s.mu.Lock()
	if s.sizer != cl {
		s.mu.Unlock()
		return
	}
	// Only a pane the layout draws keeps a claim. A shelved pane and a float
	// are sized by their own frames; an id the layout does not draw is a
	// client talking about a pane that is not there.
	if _, ok := s.paneRectsLocked()[id]; !ok {
		s.mu.Unlock()
		return
	}
	if s.claims == nil {
		s.claims = map[string]paneSize{}
	}
	s.claims[id] = paneSize{cols: cols, rows: rows}
	s.mu.Unlock()
	// syncSizes sends a resize only to the pane whose size moved, so a claim
	// never touches a sibling's pty.
	s.syncSizes()
}
