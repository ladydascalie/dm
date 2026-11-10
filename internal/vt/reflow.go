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
// The window is sound only for a plain tail. Only printable text, CRLF, TAB,
// SGR and the OSC 0, 1, 2 and 7 reports are replayed from the window; anything
// else means the live rows were drawn from output above the window, and a
// fresh terminal cannot reproduce them. A line break must be CRLF: a bare LF
// keeps the column and a bare CR keeps the row, so the window could start at a
// phase the replay does not know. Reflow then replays the whole ring, a
// few hundred milliseconds instead of a few milliseconds. A progress line, a
// status block or a pager is the common trigger; the settle runs once per
// gesture, so the trade is accepted. plainTail lists the four allowed shapes
// and nothing else.
//
// The window says nothing about screen state set before it that survives: a
// style or a mode is not replayed. For a plain tail, the visible rows and the
// cursor are what it must reproduce, and it does.
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
	// The window is sound only for a plain tail. Anything that moves the cursor,
	// saves or restores it, sets a scroll region or switches the screen means
	// the live rows were drawn from output above the window, and a fresh
	// terminal cannot reproduce them: it would start at row 0 and paint over
	// them. The whole ring is replayed then, a few hundred milliseconds instead
	// of a few milliseconds. The settle runs once per gesture, so the trade is
	// accepted.
	if !plainTail(ring[start:]) {
		return 0
	}
	return start
}

// plainTail reports whether b is plain text the window may replay on its own.
// Four shapes are allowed, and nothing else is:
//
//   - printable text: any byte at or above 0x20, UTF-8 continuation bytes
//     included (the test is permissive, not a UTF-8 validation);
//   - a CRLF pair and TAB. A bare LF keeps the column and a bare CR keeps the
//     row, so either one can start the window at a column or row phase the
//     replay does not know; only a CRLF returns to both the left margin and the
//     next row;
//   - SGR, CSI <digits and semicolons> m;
//   - an OSC 0, 1, 2 or 7 body terminated by BEL or ST, with no ESC inside.
//
// Everything else returns false: a lone CR, backspace and every other C0
// control, every other CSI final byte, every other escape, every other OSC. A
// A screen switch is just another byte outside this list, so the older
// alternate-screen rule is covered here too.
//
// The allow-list is deliberate. The set of controls that can move a cursor is
// unbounded (backspace, DECSC/DECRC, CSI s/u and more), so a control that is
// not named here must not silently inherit the window: it takes the whole-ring
// path until someone adds it, with a test.
func plainTail(b []byte) bool {
	for i := 0; i < len(b); {
		c := b[i]
		switch c {
		case '\r':
			// CR is plain only as the first half of CRLF. A lone CR is the
			// progress-line rewrite.
			if i+1 >= len(b) || b[i+1] != '\n' {
				return false
			}
			i++
		case '\n':
			// LF is plain only as the second half of CRLF. A bare LF keeps the
			// column, which is the staircase a program with ONLCR off writes.
			if i == 0 || b[i-1] != '\r' {
				return false
			}
			i++
		case '\t':
			i++
		case 0x1b:
			n, ok := plainEscape(b[i:])
			if !ok {
				return false
			}
			i += n
		default:
			if c < 0x20 {
				return false
			}
			i++
		}
	}
	return true
}

// plainEscape parses one escape sequence from the start of b, which begins
// with ESC, and returns its length. It accepts only SGR and the OSC 0, 1, 2
// and 7 reports. A truncated sequence is not accepted.
func plainEscape(b []byte) (int, bool) {
	if len(b) < 2 {
		return 0, false
	}
	switch b[1] {
	case '[':
		i := 2
		for i < len(b) && (b[i] >= '0' && b[i] <= '9' || b[i] == ';') {
			i++
		}
		if i < len(b) && b[i] == 'm' {
			return i + 1, true
		}
		return 0, false
	case ']':
		i := 2
		code, digits := 0, 0
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			code = code*10 + int(b[i]-'0')
			digits++
			i++
		}
		if digits == 0 || i >= len(b) || b[i] != ';' {
			return 0, false
		}
		switch code {
		case 0, 1, 2, 7:
		default:
			return 0, false
		}
		i++
		for i < len(b) {
			switch b[i] {
			case 0x07: // BEL
				return i + 1, true
			case 0x1b:
				if i+1 < len(b) && b[i+1] == '\\' {
					return i + 2, true
				}
				return 0, false
			}
			i++
		}
		return 0, false
	default:
		return 0, false
	}
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
			switch c {
			case 0x1b: // ST is ESC \, re-entered as an escape
				state = scanEscape
			case 0x9c, 0x07, 0x18, 0x1a:
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
