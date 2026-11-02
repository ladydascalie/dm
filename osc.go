package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
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
}

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
		if f.buf[1] != ']' {
			out.Write(f.buf[:2])
			f.buf = f.buf[2:]
			continue
		}
		end, complete := oscEnd(f.buf)
		if !complete {
			if len(f.buf) > 8192 { // runaway, do not buffer forever
				out.Write(f.buf)
				f.buf = f.buf[:0]
			}
			return
		}
		seq := f.buf[:end]
		if bytes.HasPrefix(seq, oscPrefix) {
			payload := seq[len(oscPrefix):]
			payload = bytes.TrimSuffix(payload, []byte("\x1b\\"))
			payload = bytes.TrimSuffix(payload, []byte{0x07})
			body := strings.TrimSpace(string(payload))
			if strings.HasPrefix(body, "?") {
				if f.query != nil {
					f.query()
				}
			} else if f.status != nil {
				f.status(parseStatus(body))
			}
		} else {
			out.Write(seq)
		}
		f.buf = f.buf[end:]
	}
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
