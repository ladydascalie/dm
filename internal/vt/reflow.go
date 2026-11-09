package vt

import "bytes"

// Reflow rebuilds a pane's screen at cols by rows by replaying the tail of its
// raw output ring into a fresh terminal. The ring's stream decides the screen:
// it is the same bytes the app wrote at the old width, so its cursor moves,
// clears and wraps land as they did live.
//
// A size below 1 is clamped to 1. The alternate screen is not inspected: when
// the replay ends on it, Grid().Modes().AltScreen reports it and the caller
// decides whether to adopt the result. A pane whose app owns that screen is not
// reflowed; the app repaints itself on SIGWINCH.
//
// ReplayRows is the history path and is not affected: it counts scrolls to
// extend the scrollback and re-wraps its rows at the pane width.
//
// The replay is bounded to the tail that can still be on screen. The screen
// holds rows*cols cells, so a window of twice that many printable bytes is
// enough: a cell can cost several bytes (three for a CJK glyph, more with
// combining marks) and escape sequences between the text cost bytes without
// occupying a cell. Two bytes per cell covers that slack with room to spare,
// and the window cuts 512 KiB of ordinary shell output down to a few KiB.
//
// The window starts on a line boundary. Text that wraps depends on the column
// the line began at, so a replay that starts mid-line would place the rest of
// that line at the wrong columns. When the next newline after the cutoff still
// leaves a screenful of text, the cut is moved forward past the partial line:
// it is above the screen anyway and need not be replayed. When that boundary
// leaves too little (a line longer than the window followed by a few short
// ones), the replay starts at the beginning of the line that holds the cutoff,
// so the wrap alignment is kept. When the stream ends inside a line, with no
// newline after the cutoff, the start is clamped to the cutoff and the partial
// first line is accepted: replaying back to its beginning could walk the whole
// ring. A ring no longer than the window is replayed whole, byte for byte.
//
// A ring that switches screens forces a whole-ring replay, whether the switch
// is in the skipped prefix or in the slice the window would replay. A pager
// saves the primary screen on ?1049h and restores it on ?1049l. If the window
// starts inside the cycle, the primary screen it restores lost the rows above
// the window. If the cycle sits wholly inside the window, the window saves a
// partial primary screen and the restore leaves the top blank and the prompt
// too high. The pane sees primary mode again after ?1049l, so it cannot refuse
// either result. The cycle is rare and its window is a pager dump, so the
// trade is one slow replay for correctness: this fires only when one of the
// eight sequences appears, anywhere and for any pane output, and the whole
// ring costs a few hundred milliseconds then.
//
// The window assumes the dropped prefix does not change the screen state that
// survives it: a style or a mode set before the window is not replayed. The
// visible rows and the cursor are what it must reproduce, and it does.
func Reflow(ring []byte, cols, rows int) *Term {
	cols, rows = reflowSize(cols, rows)
	return reflowFrom(ring, cols, rows, reflowStart(ring, cols, rows))
}

// reflowSize clamps a screen size to at least one cell each way.
func reflowSize(cols, rows int) (int, int) {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	return cols, rows
}

// reflowFrom replays the ring from offset from. from 0 is the whole ring, the
// form a caller uses when it cannot bound the stream.
func reflowFrom(ring []byte, cols, rows, from int) *Term {
	if from < 0 {
		from = 0
	} else if from > len(ring) {
		from = len(ring)
	}
	term := NewTerm(cols, rows)
	term.Write(ring[from:])
	// The last cluster of a Write stays pending until the next rune proves it
	// complete, so flush it to place it and advance the cursor.
	term.Flush()
	return term
}

// reflowStart returns the offset whose tail can still cover the screen. The
// walk is a byte pass over the ring with no terminal work, so it costs a
// fraction of a millisecond even on a full 512 KiB ring.
func reflowStart(ring []byte, cols, rows int) int {
	budget := 2 * rows * cols
	if budget < 1 {
		return 0
	}
	total := 0
	scanPrintable(ring, func(int) bool {
		total++
		return true
	})
	if total <= budget {
		return 0
	}
	// Keep the last budget printable bytes: cut at the first of them.
	drop := total - budget
	seen := 0
	cut := 0
	scanPrintable(ring, func(i int) bool {
		seen++
		if seen == drop+1 {
			cut = i
			return false
		}
		return true
	})
	start := cut
	if n := bytes.IndexByte(ring[cut:], '\n'); n >= 0 {
		next := cut + n + 1
		if scanCount(ring[next:], rows*cols) >= rows*cols {
			// The next boundary still leaves a screenful: the partial line
			// above it is above the screen and need not be replayed.
			start = next
		} else if n := bytes.LastIndexByte(ring[:cut], '\n'); n >= 0 {
			// The line holding the cutoff is still on screen. Start at its
			// beginning so its wrapped text keeps the same columns.
			start = n + 1
		}
	}
	if start == 0 {
		return 0
	}
	// A screen switch anywhere in the ring forces the whole ring. In the
	// skipped prefix it means the window starts inside the cycle, so the
	// primary screen it restores is partial. In the replayed slice it means
	// the window saves its own partial primary screen on ?1049h and restores
	// that on ?1049l, which leaves the top blank and the prompt too high. Both
	// halves of the ring are checked, so either kind of cycle is caught.
	if hasAltScreenCycle(ring) {
		return 0
	}
	return start
}

// altScreenModes are the paired set and reset sequences of a screen switch:
// the alternate screen (47, 1047, 1049) and the cursor save and restore around
// it (1048). A pager's entry and exit are two of these.
var altScreenModes = [...][2]string{
	{"?47h", "?47l"},
	{"?1047h", "?1047l"},
	{"?1048h", "?1048l"},
	{"?1049h", "?1049l"},
}

// hasAltScreenCycle reports whether prefix holds an alternate-screen entry or
// exit. A false positive only costs a whole-ring replay.
func hasAltScreenCycle(prefix []byte) bool {
	for _, pair := range altScreenModes {
		if bytes.Contains(prefix, []byte(pair[0])) || bytes.Contains(prefix, []byte(pair[1])) {
			return true
		}
	}
	return false
}

// scanCount returns how many printable bytes b holds, stopping at limit.
func scanCount(b []byte, limit int) int {
	n := 0
	scanPrintable(b, func(int) bool {
		n++
		return n < limit
	})
	return n
}

// scanState is the byte state machine's position. It mirrors the parser's
// states so the printable count matches what reaches the screen, without a
// grid to write to.
type scanState uint8

const (
	scanGround scanState = iota
	scanEscape
	scanCSI
	scanString
	scanCharset
)

// scanPrintable walks b and calls text with the index of every byte that
// reaches the screen as text: a ground-state byte that is not a control. text
// returns false to stop the walk. Escape sequences are skipped byte by byte,
// including one cut off at the end of the ring.
func scanPrintable(b []byte, text func(i int) bool) {
	state := scanGround
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch state {
		case scanGround:
			switch {
			case c == 0x1b:
				state = scanEscape
			case c < 0x20 || c == 0x7f:
				// A C0 control or DEL moves the cursor or does nothing; it is
				// not text.
			default:
				if !text(i) {
					return
				}
			}
		case scanEscape:
			switch {
			case c == 0x1b:
			case c == 0x18 || c == 0x1a: // CAN, SUB
				state = scanGround
			case c < 0x20 || c == 0x7f:
			case c >= 0x20 && c <= 0x2f:
				switch c {
				case '(', ')', '*', '+':
					state = scanCharset
				}
			case c == '[':
				state = scanCSI
			case c == ']' || c == 'P' || c == 'X' || c == '^' || c == '_':
				state = scanString
			default:
				state = scanGround
			}
		case scanCSI:
			switch {
			case c == 0x1b:
				state = scanEscape
			case c == 0x18 || c == 0x1a:
				state = scanGround
			case c < 0x20 || c == 0x7f:
			case c >= 0x40 && c <= 0x7e:
				state = scanGround
			}
		case scanString:
			switch {
			case c == 0x1b: // ST is ESC \, re-entered as an escape
				state = scanEscape
			case c == 0x9c || c == 0x07 || c == 0x18 || c == 0x1a:
				state = scanGround
			}
		case scanCharset:
			switch {
			case c == 0x1b:
				state = scanEscape
			case c == 0x18 || c == 0x1a:
				state = scanGround
			case c >= 0x30 && c <= 0x7e:
				state = scanGround
			}
		}
	}
}
