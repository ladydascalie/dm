package vt

// Reflow rebuilds a pane's screen at cols by rows by replaying its raw output
// ring into a fresh terminal. The ring's stream decides the screen: it is the
// same bytes the app wrote at the old width, so its cursor moves, clears and
// wraps land as they did live.
//
// A size below 1 is clamped to 1. The alternate screen is not inspected: when
// the replay ends on it, Grid().Modes().AltScreen reports it and the caller
// decides whether to adopt the result. A pane whose app owns that screen is not
// reflowed; the app repaints itself on SIGWINCH.
//
// ReplayRows is the history path and is not affected: it counts scrolls to
// extend the scrollback and re-wraps its rows at the pane width.
func Reflow(ring []byte, cols, rows int) *Term {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	term := NewTerm(cols, rows)
	term.Write(ring)
	// The last cluster of a Write stays pending until the next rune proves it
	// complete, so flush it to place it and advance the cursor.
	term.Flush()
	return term
}
