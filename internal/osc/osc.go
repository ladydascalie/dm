package osc

import (
	"bytes"
	"dm/internal/wire"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// Filter strips OSC 7501 status reports out of the PTY stream and passes
// everything else through untouched. Partial sequences are held between reads.
// Query is called when an application asks whether the terminal supports
// program status; the pane answers by writing the reply back into the PTY.
type Filter struct {
	buf    []byte
	Status func(wire.Status)
	// Session is called with the decoded OSC 7502 report: the file a pane's
	// agent writes its structured session to, and the session id. A clear
	// calls it with the zero Session, so the pane drops what it kept.
	Session func(Session)
	Query   func()
	Reply   func(string)
	Cursor  func() (row, col int)
	FG, BG  string
	// CursorFG is the cursor colour OSC 12 reports. Empty falls back to
	// TermCursor, so a future cursor-colour track only fills this field.
	CursorFG string
}

// Terminal queries an application makes at startup and then waits on. termenv
// waits five seconds for the OSC 11 reply, so an unanswered query is a five
// second stall in every TUI it fronts.
//
// TermFG and TermBG are the fallback pair, in the xterm form OSC 10 and OSC 11
// define, until a client reports the outer terminal's colours. A pane that has
// not been told the pair answers these, and termenv parses them.
const (
	TermFG = "rgb:ffff/ffff/ffff"
	TermBG = "rgb:0000/0000/0000"
	// TermCursor is the fallback cursor colour OSC 12 reports. The pane does
	// not track a cursor colour, so the query answers this default until one
	// does.
	TermCursor = "rgb:ffff/ffff/ffff"
	replyDA1   = "\x1b[?1;2c"
	replyDA2   = "\x1b[>0;10;1c"
)

var (
	oscPrefix        = []byte("\x1b]7501;")
	oscSessionPrefix = []byte("\x1b]7502;")
)

func (f *Filter) Feed(p []byte, out *bytes.Buffer) {
	f.buf = append(f.buf, p...)
	for len(f.buf) > 0 {
		i := bytes.IndexByte(f.buf, 0x1b)
		if i < 0 {
			out.Write(f.buf)
			f.buf = f.buf[:0]
			return
		}
		if i > 0 {
			out.Write(f.buf[:i])
			f.buf = f.buf[i:]
			continue
		}
		if len(f.buf) < 2 {
			return // need the byte after ESC
		}
		switch f.buf[1] {
		case ']':
			end, complete := oscEnd(f.buf)
			if !complete {
				if len(f.buf) > 8192 { // runaway, do not buffer forever
					out.Write(f.buf)
					f.buf = f.buf[:0]
				}
				return
			}
			seq := f.buf[:end]
			if !f.handleOSC(seq) {
				out.Write(seq)
			}
			f.buf = f.buf[end:]
		case '[':
			end, complete := csiEnd(f.buf)
			if !complete {
				if len(f.buf) > 64 {
					out.Write(f.buf[:2])
					f.buf = f.buf[2:]
					continue
				}
				return
			}
			seq := f.buf[:end]
			if !f.handleCSI(seq) {
				out.Write(seq)
			}
			f.buf = f.buf[end:]
		default:
			out.Write(f.buf[:2])
			f.buf = f.buf[2:]
		}
	}
}

// handleOSC answers terminal colour and identity queries and consumes the
// 7501 status protocol. It reports whether the sequence was handled; an
// unhandled sequence is passed to the VT untouched.
func (f *Filter) handleOSC(seq []byte) bool {
	body := seq[2:]
	body = bytes.TrimSuffix(body, []byte("\x1b\\"))
	body = bytes.TrimSuffix(body, []byte{0x07})
	text := string(body)
	if strings.HasPrefix(text, "10;?") {
		f.send("\x1b]10;" + f.FG + "\x1b\\")
		return true
	}
	if strings.HasPrefix(text, "11;?") {
		f.send("\x1b]11;" + f.BG + "\x1b\\")
		return true
	}
	if strings.HasPrefix(text, "12;?") {
		colour := f.CursorFG
		if colour == "" {
			colour = TermCursor
		}
		f.send("\x1b]12;" + colour + "\x1b\\")
		return true
	}
	if strings.HasPrefix(text, "4;") && strings.HasSuffix(text, ";?") {
		parts := strings.Split(strings.TrimSuffix(text, ";?"), ";")
		if len(parts) == 2 {
			if n, err := parseIndex(parts[1]); err == nil {
				f.send(fmt.Sprintf("\x1b]4;%d;%s\x1b\\", n, xtermColor(n)))
				return true
			}
		}
	}
	if bytes.HasPrefix(seq, oscSessionPrefix) {
		payload := seq[len(oscSessionPrefix):]
		payload = bytes.TrimSuffix(payload, []byte("\x1b\\"))
		payload = bytes.TrimSuffix(payload, []byte{0x07})
		if s, outcome := ParseSession(string(payload)); outcome != SessionNone && f.Session != nil {
			f.Session(s)
		}
		// The frame names the session and nothing else: it is consumed even
		// when it does not decode, so the VT never sees it.
		return true
	}
	if bytes.HasPrefix(seq, oscPrefix) {
		payload := seq[len(oscPrefix):]
		payload = bytes.TrimSuffix(payload, []byte("\x1b\\"))
		payload = bytes.TrimSuffix(payload, []byte{0x07})
		payloadText := strings.TrimSpace(string(payload))
		if strings.HasPrefix(payloadText, "?") {
			if f.Query != nil {
				f.Query()
			}
		} else if f.Status != nil {
			f.Status(ParseStatus(payloadText))
		}
		return true
	}
	return false
}

// handleCSI answers the identity and cursor queries a terminal is expected to.
func (f *Filter) handleCSI(seq []byte) bool {
	switch string(seq) {
	case "\x1b[6n":
		if f.Cursor != nil {
			row, col := f.Cursor()
			f.send(fmt.Sprintf("\x1b[%d;%dR", row+1, col+1))
		}
		return true
	case "\x1b[c", "\x1b[0c":
		f.send(replyDA1)
		return true
	case "\x1b[>c", "\x1b[>0c":
		f.send(replyDA2)
		return true
	case "\x1b[5n":
		f.send("\x1b[0n")
		return true
	}
	return false
}

func (f *Filter) send(s string) {
	if f.Reply != nil {
		f.Reply(s)
	}
}

func parseIndex(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, fmt.Errorf("no index")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("bad index")
		}
		n = n*10 + int(c-'0')
	}
	if n > 255 {
		return 0, fmt.Errorf("index out of range")
	}
	return n, nil
}

// xtermColor is the standard 256-colour palette as an OSC rgb string.
func xtermColor(n int) string {
	base := [16][3]int{
		{0, 0, 0}, {205, 0, 0}, {0, 205, 0}, {205, 205, 0},
		{0, 0, 238}, {205, 0, 205}, {0, 205, 205}, {229, 229, 229},
		{127, 127, 127}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0},
		{92, 92, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
	}
	var r, g, b int
	switch {
	case n < 16:
		r, g, b = base[n][0], base[n][1], base[n][2]
	case n < 232:
		i := n - 16
		level := [6]int{0, 95, 135, 175, 215, 255}
		r, g, b = level[i/36], level[(i/6)%6], level[i%6]
	default:
		v := 8 + (n-232)*10
		r, g, b = v, v, v
	}
	// The 16-bit xterm form: each channel doubled, as OSC 4 expects.
	return fmt.Sprintf("rgb:%02x%02x/%02x%02x/%02x%02x", r, r, g, g, b, b)
}

// oscEnd returns the byte length of the sequence starting at ESC ] and whether
// it is terminated (BEL or ESC \).
func oscEnd(b []byte) (int, bool) {
	for i := 2; i < len(b); i++ {
		if b[i] == 0x07 {
			return i + 1, true
		}
		if b[i] == 0x1b && i+1 < len(b) && b[i+1] == '\\' {
			return i + 2, true
		}
	}
	return 0, false
}

// csiEnd returns the byte length of the sequence starting at ESC [ and whether
// its final byte has arrived.
func csiEnd(b []byte) (int, bool) {
	for i := 2; i < len(b); i++ {
		c := b[i]
		if c >= 0x40 && c <= 0x7e {
			return i + 1, true
		}
		if c < 0x20 || c > 0x7e {
			return 0, false
		}
	}
	return 0, false
}

// knownState reports whether s is one of the states the protocol defines, the
// set the README's OSC 7501 section lists. A state is a wire word, not text:
// `dm status`, the rail, `dm exec read` and `dm exec list` print it unscrubbed,
// so a report must not be able to put a value there.
func knownState(s string) bool {
	switch s {
	case "idle", "working", "blocked", "done", "error", "unknown":
		return true
	}
	return false
}

// normaliseState maps a reported state to the wire set. clear is the
// protocol's removal, shown as unknown; a word the set does not name is
// unknown too, so no report can invent a state.
func normaliseState(s string) string {
	if knownState(s) {
		return s
	}
	return "unknown"
}

// Session is the decoded OSC 7502 frame: the file a pane's agent writes its
// structured session to, and the id that session holds. The file is the path
// itself, decoded, not the base64 the frame carries.
type Session struct {
	File string
	ID   string
}

// SessionOutcome is what an OSC 7502 frame says. A frame names a session, clears
// one, or says nothing the pane should act on.
type SessionOutcome int

const (
	// SessionNone is a frame with no session key, or a path that does not
	// decode. The pane ignores it and keeps what it had.
	SessionNone SessionOutcome = iota
	// SessionSet is a frame with a decoded path. ID may be empty.
	SessionSet
	// SessionClear is the session key with an empty value: the agent that
	// reported it is gone, so the pane drops the session it kept.
	SessionClear
)

// ParseSession accepts the OSC 7502 wire format, `session=<b64 path>;id=<id>`.
// The path is base64 because a path holds characters the frame cannot carry.
// Three outcomes, all distinct:
//   - SessionSet: the session key holds a decoded path.
//   - SessionClear: the session key is present with an empty value.
//   - SessionNone: no session key at all, or a path that does not decode. The
//     caller drops the frame and changes nothing.
func ParseSession(s string) (Session, SessionOutcome) {
	var out Session
	keyed := false
	for _, pair := range strings.Split(strings.TrimSpace(s), ";") {
		k, v, found := strings.Cut(strings.TrimSpace(pair), "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(k) {
		case "session":
			keyed = true
			v = strings.TrimSpace(v)
			if v == "" {
				out = Session{}
				continue
			}
			b, err := base64.StdEncoding.DecodeString(v)
			if err != nil {
				return Session{}, SessionNone
			}
			out.File = string(b)
		case "id":
			out.ID = strings.TrimSpace(v)
		}
	}
	if !keyed {
		return Session{}, SessionNone
	}
	if out.File == "" {
		return Session{}, SessionClear
	}
	return out, SessionSet
}

// ParseStatus accepts the OSC 7501 wire format, `state=working:app=pi:msg=<b64>`,
// plus the JSON and bare-state forms dm accepted before. The state is validated
// against the protocol's set: every read verb prints it, and a state is never a
// place for free text.
func ParseStatus(s string) wire.Status {
	s = strings.TrimSpace(s)
	var st wire.Status
	if strings.HasPrefix(s, "{") && json.Unmarshal([]byte(s), &st) == nil {
		st.State = normaliseState(st.State)
		return st
	}
	if strings.Contains(s, "=") {
		for _, pair := range strings.Split(s, ":") {
			k, v, ok := strings.Cut(pair, "=")
			if !ok {
				continue
			}
			switch k {
			case "state":
				st.State = v
			case "app":
				st.App = v
			case "msg":
				if b, err := base64.StdEncoding.DecodeString(v); err == nil {
					st.Message = string(b)
				}
			}
		}
		if st.State == "" || st.State == "clear" {
			// clear is the protocol's removal: nothing is left of the
			// report, the message included. An empty state is the same
			// report with a word missing.
			st.State = "unknown"
			st.Message = ""
			return st
		}
		st.State = normaliseState(st.State)
		return st
	}
	// The keyed form normalises clear to unknown; the bare form gets the
	// same treatment, so one state never renders two ways.
	return wire.Status{State: normaliseState(s)}
}
