package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// oscFilter strips OSC 7501 status reports out of the PTY stream and passes
// everything else through untouched. Partial sequences are held between reads.
// query is called when an application asks whether the terminal supports
// program status; the pane answers by writing the reply back into the PTY.
type oscFilter struct {
	buf    []byte
	status func(Status)
	query  func()
	reply  func(string)
	cursor func() (row, col int)
	fg, bg string
}

// Terminal queries an application makes at startup and then waits on. termenv
// waits five seconds for the OSC 11 reply, so an unanswered query is a five
// second stall in every TUI it fronts.
const (
	termFG   = "rgb:ffff/ffff/ffff"
	termBG   = "rgb:0000/0000/0000"
	replyDA1 = "\x1b[?1;2c"
	replyDA2 = "\x1b[>0;10;1c"
)

var oscPrefix = []byte("\x1b]7501;")

func (f *oscFilter) feed(p []byte, out *bytes.Buffer) {
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
func (f *oscFilter) handleOSC(seq []byte) bool {
	body := seq[2:]
	body = bytes.TrimSuffix(body, []byte("\x1b\\"))
	body = bytes.TrimSuffix(body, []byte{0x07})
	text := string(body)
	if strings.HasPrefix(text, "10;?") {
		f.send("\x1b]10;" + f.fg + "\x1b\\")
		return true
	}
	if strings.HasPrefix(text, "11;?") {
		f.send("\x1b]11;" + f.bg + "\x1b\\")
		return true
	}
	if strings.HasPrefix(text, "12;?") {
		f.send("\x1b]12;" + f.fg + "\x1b\\")
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
	if bytes.HasPrefix(seq, oscPrefix) {
		payload := seq[len(oscPrefix):]
		payload = bytes.TrimSuffix(payload, []byte("\x1b\\"))
		payload = bytes.TrimSuffix(payload, []byte{0x07})
		payloadText := strings.TrimSpace(string(payload))
		if strings.HasPrefix(payloadText, "?") {
			if f.query != nil {
				f.query()
			}
		} else if f.status != nil {
			f.status(parseStatus(payloadText))
		}
		return true
	}
	return false
}

// handleCSI answers the identity and cursor queries a terminal is expected to.
func (f *oscFilter) handleCSI(seq []byte) bool {
	switch string(seq) {
	case "\x1b[6n":
		if f.cursor != nil {
			row, col := f.cursor()
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

func (f *oscFilter) send(s string) {
	if f.reply != nil {
		f.reply(s)
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

// parseStatus accepts the OSC 7501 wire format, `state=working:app=pi:msg=<b64>`,
// plus the JSON and bare-state forms dm accepted before.
func parseStatus(s string) Status {
	s = strings.TrimSpace(s)
	var st Status
	if strings.HasPrefix(s, "{") && json.Unmarshal([]byte(s), &st) == nil {
		if st.State == "" {
			st.State = "unknown"
		}
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
			case "msg":
				if b, err := base64.StdEncoding.DecodeString(v); err == nil {
					st.Message = string(b)
				}
			}
		}
		if st.State == "clear" || st.State == "" {
			st.State = "unknown"
			st.Message = ""
		}
		return st
	}
	if s == "" {
		s = "unknown"
	}
	return Status{State: s}
}
