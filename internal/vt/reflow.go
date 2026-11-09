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
// it is above the screen anyway and need not be replayed. When it does not (a
// line longer than the window, or the stream ends inside one), the replay
// starts at the beginning of the line that holds the cutoff, so the wrap
// alignment is kept. A ring no longer than the window is replayed whole, byte
// for byte.
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
	// Prefer the next line boundary: the partial line above it is not needed.
	if n := bytes.IndexByte(ring[cut:], '\n'); n >= 0 {
		start := cut + n + 1
		if scanCount(ring[start:], rows*cols) >= rows*cols {
			return start
		}
	}
	// Too little text after that boundary, so the line holding the cutoff is
	// still on screen. Start at its beginning to keep the wrap alignment.
	if n := bytes.LastIndexByte(ring[:cut], '\n'); n >= 0 {
		return n + 1
	}
	return 0
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
