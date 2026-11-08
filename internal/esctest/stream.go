package esctest

import (
	"strconv"
	"strings"
)

// seq is one escape sequence from the stream.
type seq struct {
	kind   byte   // 'c' CSI, ']' OSC, 'P' DCS, 'Z' DECID, 0 other
	priv   byte   // '?', '>', '<', '=', or 0
	params []int  // -1 for an empty parameter
	inter  string // intermediate bytes
	final  byte
	body   string // OSC or DCS payload
}

// scan parses the sequence at the start of b. ok is false when b holds only
// part of a sequence; the caller must wait for more bytes.
func scan(b []byte) (seq, int, bool) {
	if len(b) == 0 {
		return seq{}, 0, false
	}
	if b[0] != 0x1b {
		switch b[0] {
		case 0x9b:
			return scanCSI(b, 1)
		case 0x90:
			return scanDCS(b, 1)
		case 0x9d:
			return scanOSC(b, 1)
		case 0x9a:
			return seq{kind: 'Z'}, 1, true
		default:
			return seq{}, 1, true
		}
	}
	if len(b) < 2 {
		return seq{}, 0, false
	}
	switch b[1] {
	case '[':
		return scanCSI(b, 2)
	case ']':
		return scanOSC(b, 2)
	case 'P':
		return scanDCS(b, 2)
	case 'Z':
		return seq{kind: 'Z'}, 2, true
	default:
		return seq{}, 2, true
	}
}

func scanCSI(b []byte, start int) (seq, int, bool) {
	i := start
	for i < len(b) && b[i] >= 0x30 && b[i] <= 0x3f {
		i++
	}
	pstart, pend := start, i
	for i < len(b) && b[i] >= 0x20 && b[i] <= 0x2f {
		i++
	}
	istart, iend := pend, i
	if i >= len(b) {
		return seq{}, 0, false
	}
	if b[i] < 0x40 || b[i] > 0x7e {
		return seq{}, 0, false
	}
	s := seq{kind: 'c', final: b[i], inter: string(b[istart:iend])}
	p := b[pstart:pend]
	if len(p) > 0 && (p[0] == '?' || p[0] == '>' || p[0] == '<' || p[0] == '=') {
		s.priv = p[0]
		p = p[1:]
	}
	s.params = parseParams(p)
	return s, i + 1, true
}

func scanOSC(b []byte, start int) (seq, int, bool) {
	for i := start; i < len(b); i++ {
		if b[i] == 0x07 {
			return seq{kind: ']', body: string(b[start:i])}, i + 1, true
		}
		if b[i] == 0x1b {
			if i+1 >= len(b) {
				return seq{}, 0, false
			}
			if b[i+1] == '\\' {
				return seq{kind: ']', body: string(b[start:i])}, i + 2, true
			}
		}
	}
	return seq{}, 0, false
}

func scanDCS(b []byte, start int) (seq, int, bool) {
	for i := start; i < len(b); i++ {
		if b[i] == 0x1b {
			if i+1 >= len(b) {
				return seq{}, 0, false
			}
			if b[i+1] == '\\' {
				return seq{kind: 'P', body: string(b[start:i])}, i + 2, true
			}
		}
	}
	return seq{}, 0, false
}

func parseParams(p []byte) []int {
	if len(p) == 0 {
		return nil
	}
	parts := strings.Split(string(p), ";")
	out := make([]int, len(parts))
	for i, s := range parts {
		if s == "" {
			out[i] = -1
			continue
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			out[i] = -1
			continue
		}
		out[i] = n
	}
	return out
}

// handled reports whether the harness answers or acts on the sequence. A
// handled sequence is not written to the terminal.
func handled(s seq) bool {
	switch s.kind {
	case 'Z':
		return true
	case ']':
		return oscIsQuery(s.body)
	case 'P':
		return strings.HasPrefix(s.body, "$q") || strings.HasPrefix(s.body, "+q")
	case 'c':
		switch s.final {
		case 'c':
			return s.priv == 0 || s.priv == '>'
		case 'n':
			// A plain DSR 6 is answered by the terminal under test; the
			// private form, DECXCPR, is handled here. Both are routed to the
			// report terminal's answer.
			return s.priv == '?' || (s.priv == 0 && firstParam(s.params, 0) == 6)
		case 't':
			return s.priv == 0
		case 'p':
			return s.inter == "$"
		case 'y':
			return s.inter == "*"
		case 'q':
			return s.priv == '>'
		}
	}
	return false
}

// oscIsQuery reports whether an OSC body asks for a reply.
func oscIsQuery(body string) bool {
	fields := strings.Split(body, ";")
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "4", "5":
		for i := 2; i < len(fields); i += 2 {
			if fields[i] == "?" {
				return true
			}
		}
	case "10", "11", "12":
		for i := 1; i < len(fields); i++ {
			if fields[i] == "?" {
				return true
			}
		}
	case "52":
		for i := 1; i < len(fields); i++ {
			if fields[i] == "?" {
				return true
			}
		}
	}
	return false
}

// translateC1 rewrites 8-bit C1 controls as their 7-bit forms. The harness does not
// recognise C1 bytes, and esctest2 sends them after S8C1T.
func translateC1(b []byte) []byte {
	has := false
	for _, c := range b {
		if c >= 0x80 && c <= 0x9f {
			has = true
			break
		}
	}
	if !has {
		return b
	}
	out := make([]byte, 0, len(b)+8)
	for _, c := range b {
		switch c {
		case 0x84:
			out = append(out, 0x1b, 'D')
		case 0x85:
			out = append(out, 0x1b, 'E')
		case 0x88:
			out = append(out, 0x1b, 'H')
		case 0x8d:
			out = append(out, 0x1b, 'M')
		case 0x90:
			out = append(out, 0x1b, 'P')
		case 0x9a:
			out = append(out, 0x1b, 'Z')
		case 0x9b:
			out = append(out, 0x1b, '[')
		case 0x9c:
			out = append(out, 0x1b, '\\')
		case 0x9d:
			out = append(out, 0x1b, ']')
		case 0x9e:
			out = append(out, 0x1b, '^')
		case 0x9f:
			out = append(out, 0x1b, '_')
		default:
			out = append(out, c)
		}
	}
	return out
}

func firstParam(params []int, def int) int {
	return paramAt(params, 0, def)
}

func paramAt(params []int, i, def int) int {
	if i < 0 || i >= len(params) || params[i] < 0 {
		return def
	}
	return params[i]
}
