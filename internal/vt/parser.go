package vt

import (
	"unicode"
	"unicode/utf8"
)

// maxString bounds an OSC or DCS body so a runaway sequence cannot grow the
// parser without limit.
const maxString = 4096

// parserState is the byte state machine's position.
type parserState uint8

const (
	stateGround parserState = iota
	stateEscape
	stateCSI
	stateOSC
	stateDCS
	stateCharset
)

const (
	zwj = 0x200d
	esc = 0x1b
)

// CSI is one control sequence, parsed far enough for a dispatch table to key on
// it. Params holds the numeric parameters split on ';', with a missing field
// read as 0. Raw keeps the bytes, so a family that needs colon sub-parameters
// (SGR) can read them.
type CSI struct {
	Private byte
	Params  []int
	Inter   []byte
	Final   byte
	Raw     []byte
}

// parser is the byte state machine and its pending state. Everything here must
// survive a chunk boundary and is part of the snapshot later.
type parser struct {
	state parserState

	// cluster accumulates one grapheme cluster in ground state.
	cluster []byte
	// pending holds the bytes of a rune that did not arrive whole.
	pending []byte

	// esc accumulates escape intermediates. seq accumulates the bytes of a CSI
	// between the introducer and the final byte.
	esc []byte
	seq []byte
	// str accumulates an OSC or DCS body.
	str []byte
	// charsetSlot is the designator that opened stateCharset: '(' or ')'.
	charsetSlot byte
	// strStart is the stream byte offset where the current OSC or DCS
	// sequence began, so a mark names its introducer, not its terminator.
	strStart int64
}

// write consumes b. A rune, an escape sequence and a grapheme cluster may all
// be split across calls; the state that carries them lives in parser.
func (p *parser) write(t *Term, b []byte) {
	if len(p.pending) > 0 {
		b = append(append([]byte(nil), p.pending...), b...)
		p.pending = p.pending[:0]
	}
	for i := 0; i < len(b); {
		c := b[i]
		switch p.state {
		case stateGround:
			switch {
			case c == esc:
				p.flush(t)
				p.startEscape()
				i++
				t.streamPos++
			case c < 0x20:
				p.flush(t)
				t.control(c)
				i++
				t.streamPos++
			case c == 0x7f:
				i++
				t.streamPos++
			default:
				r, size := utf8.DecodeRune(b[i:])
				if r == utf8.RuneError && size == 1 && !utf8.FullRune(b[i:]) {
					// A partial rune waits for the next chunk. The cluster
					// before it stays pending too.
					p.pending = append(p.pending[:0], b[i:]...)
					return
				}
				p.rune(t, r)
				i += size
				t.streamPos += int64(size)
			}
		case stateEscape:
			p.escapeByte(t, c)
			i++
			t.streamPos++
		case stateCSI:
			p.csiByte(t, c)
			i++
			t.streamPos++
		case stateOSC, stateDCS:
			p.stringByte(t, c)
			i++
			t.streamPos++
		case stateCharset:
			p.charsetByte(t, c)
			i++
			t.streamPos++
		}
	}
}

// rune adds r to the cluster under construction. A rune that cannot extend the
// current cluster starts a new one. Whether the cluster could still grow is not
// known until the next rune, so the last cluster of a Write stays pending until
// a rune proves it complete or Flush places it.
func (p *parser) rune(t *Term, r rune) {
	if len(p.cluster) > 0 {
		prev, _ := utf8.DecodeLastRune(p.cluster)
		if !canExtend(prev, r) {
			p.flush(t)
		}
	}
	p.cluster = utf8.AppendRune(p.cluster, r)
}

// flush places the cluster under construction and clears it.
func (p *parser) flush(t *Term) {
	if len(p.cluster) == 0 {
		return
	}
	t.place(string(p.cluster))
	p.cluster = p.cluster[:0]
}

// startEscape enters the escape state.
func (p *parser) startEscape() {
	p.esc = p.esc[:0]
	p.state = stateEscape
}

// escapeByte consumes one byte of an escape sequence.
func (p *parser) escapeByte(t *Term, c byte) {
	switch {
	case c == esc:
		p.esc = p.esc[:0]
	case c == 0x18 || c == 0x1a: // CAN, SUB
		p.state = stateGround
	case c < 0x20:
		t.control(c)
	case c == 0x7f:
	case c >= 0x20 && c <= 0x2f:
		switch c {
		case '(', ')', '*', '+':
			// A character set designator takes one more byte.
			p.charsetSlot = c
			p.esc = p.esc[:0]
			p.state = stateCharset
		default:
			p.esc = append(p.esc, c)
		}
	case c == '[':
		p.seq = p.seq[:0]
		p.state = stateCSI
	case c == ']':
		p.str = p.str[:0]
		// streamPos counts the ESC that opened the sequence; the ']' has not
		// been counted yet.
		p.strStart = t.streamPos - 1
		p.state = stateOSC
	case c == 'P' || c == 'X' || c == '^' || c == '_':
		// DCS, SOS, PM and APC all run to ST.
		p.str = p.str[:0]
		p.strStart = t.streamPos - 1
		p.state = stateDCS
	default:
		t.escape(string(p.esc), c)
		p.state = stateGround
	}
}

// csiByte consumes one byte of a control sequence.
func (p *parser) csiByte(t *Term, c byte) {
	switch {
	case c == esc:
		p.startEscape()
	case c == 0x18 || c == 0x1a: // CAN, SUB
		p.state = stateGround
	case c < 0x20:
		t.control(c)
	case c == 0x7f:
	case c >= 0x40 && c <= 0x7e:
		t.csi(p.parseCSI(c))
		p.state = stateGround
	default: // parameter and intermediate bytes 0x20..0x3f
		p.seq = append(p.seq, c)
	}
}

// parseCSI turns the accumulated bytes and the final byte into a CSI. The
// slices are fresh, so the sequence outlives the parser's buffers.
func (p *parser) parseCSI(final byte) CSI {
	raw := append([]byte(nil), p.seq...)
	seq := CSI{Final: final, Raw: raw}
	body := raw
	if len(body) > 0 {
		switch body[0] {
		case '<', '>', '?', '=':
			seq.Private = body[0]
			body = body[1:]
		}
	}
	var params []byte
	for _, c := range body {
		if c >= 0x20 && c <= 0x2f {
			seq.Inter = append(seq.Inter, c)
			continue
		}
		params = append(params, c)
	}
	if len(params) > 0 {
		seq.Params = parseParams(params)
	}
	return seq
}

// parseParams reads the numeric parameters. A ':' sub-parameter keeps the
// leading number of its field and is otherwise left in Raw.
func parseParams(b []byte) []int {
	var out []int
	n := 0
	for i := 0; i < len(b); i++ {
		switch c := b[i]; {
		case c >= '0' && c <= '9':
			n = n*10 + int(c-'0')
		case c == ';':
			out = append(out, n)
			n = 0
		case c == ':':
			for i+1 < len(b) && b[i+1] != ';' {
				i++
			}
			out = append(out, n)
			n = 0
		}
	}
	return append(out, n)
}

// stringByte consumes one byte of an OSC or DCS body. An OSC ends at BEL, ST
// or CAN; a DCS ends at ST only. ESC ends either and is re-entered as an
// escape, so a following '\' is the ST final byte.
func (p *parser) stringByte(t *Term, c byte) {
	switch {
	case c == esc:
		p.endString(t)
		p.startEscape()
	case c == 0x9c: // 8-bit ST
		p.endString(t)
		p.state = stateGround
	case c == 0x07 && p.state == stateOSC:
		p.endString(t)
		p.state = stateGround
	case (c == 0x18 || c == 0x1a):
		p.str = p.str[:0]
		p.state = stateGround
	default:
		if len(p.str) < maxString {
			p.str = append(p.str, c)
		}
	}
}

// endString delivers a completed OSC or DCS body.
func (p *parser) endString(t *Term) {
	switch p.state {
	case stateOSC:
		t.markStart = p.strStart
		t.osc(string(p.str))
	case stateDCS:
		t.dcs(string(p.str))
	}
	p.str = p.str[:0]
}

// charsetByte consumes the designator of a G0 or G1 designation.
func (p *parser) charsetByte(t *Term, c byte) {
	switch {
	case c == esc:
		p.startEscape()
	case c == 0x18 || c == 0x1a:
		p.state = stateGround
	case c >= 0x20 && c <= 0x2f:
		// An intermediate such as '%' in the UTF-8 designation.
		p.esc = append(p.esc, c)
	case c >= 0x30 && c <= 0x7e:
		p.designate(t, c)
		p.state = stateGround
	}
}

// designate applies one character set designator to G0 or G1.
func (p *parser) designate(t *Term, final byte) {
	var set CharsetSet
	switch final {
	case 'B':
		set = CharsetASCII
	case '0':
		set = CharsetDECSpecial
	case 'A':
		set = CharsetUK
	default:
		return
	}
	cs := t.grid.Charset()
	switch p.charsetSlot {
	case '(':
		cs.G0 = set
	case ')':
		cs.G1 = set
	}
	t.grid.SetCharset(cs)
}

// extendRune reports whether r joins a cluster rather than starting one: a
// combining mark, a variation selector, an emoji modifier or a tag.
func extendRune(r rune) bool {
	return unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r) ||
		(r >= 0xfe00 && r <= 0xfe0f) ||
		(r >= 0xe0100 && r <= 0xe01ef) ||
		(r >= 0x1f3fb && r <= 0x1f3ff) ||
		(r >= 0xe0020 && r <= 0xe007f)
}

// canExtend reports whether r continues the cluster whose last rune is prev.
func canExtend(prev, r rune) bool {
	return prev == zwj || r == zwj || extendRune(r) ||
		(isRegionalIndicator(prev) && isRegionalIndicator(r))
}

// isRegionalIndicator reports whether r is an indicator that pairs with another
// to form a flag.
func isRegionalIndicator(r rune) bool {
	return r >= 0x1f1e6 && r <= 0x1f1ff
}
