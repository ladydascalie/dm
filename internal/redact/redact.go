// Package redact scrubs registered secret values out of a byte stream.
//
// A Scrubber holds a registry of named secret values. Feed gives it chunks of
// an arbitrary stream; it writes the stream to the output buffer with every
// registered value replaced by a marker naming the secret. Values that arrive
// split across chunk boundaries are held back and matched once complete, the
// same way internal/osc Filter holds partial sequences. The stream is treated
// as raw bytes, so binary data passes through untouched apart from matches.
//
// Matching is leftmost-longest: at each position the longest registered value
// that the bytes could still grow into decides. A position stays held while
// some longer value could still complete there, so a value that is a prefix
// of another never fires early.
package redact

import "bytes"

// Scrubber replaces registered secret values in a byte stream. It is not
// safe for concurrent use.
type Scrubber struct {
	buf     []byte
	secrets []entry
}

type entry struct {
	value  []byte
	marker []byte
}

// New returns an empty scrubber. With nothing registered, Feed passes
// everything straight through.
func New() *Scrubber {
	return &Scrubber{}
}

// Register adds a named secret value. Later registrations of the same value
// replace the earlier name. Empty values are ignored: they match nothing.
func (s *Scrubber) Register(name, value string) {
	if value == "" {
		return
	}
	e := entry{value: []byte(value), marker: []byte("[" + name + " redacted]")}
	for i := range s.secrets {
		if bytes.Equal(s.secrets[i].value, e.value) {
			s.secrets[i] = e
			return
		}
	}
	s.secrets = append(s.secrets, e)
}

// Feed appends p to the stream and writes the part that can no longer grow
// into a match to out. A trailing slice that a registered value could still
// complete inside is held until more bytes arrive. Call Flush at end of
// stream to release it.
func (s *Scrubber) Feed(p []byte, out *bytes.Buffer) {
	s.buf = append(s.buf, p...)
	s.run(out, false)
}

// Flush settles the stream. Held bytes that now form a complete match are
// replaced; the rest go out as they arrived.
func (s *Scrubber) Flush(out *bytes.Buffer) {
	s.run(out, true)
}

// run consumes s.buf from the front, writing settled output to out. A false
// final holds a tail a future chunk could still turn into a match; a true
// final forces a decision on every byte.
func (s *Scrubber) run(out *bytes.Buffer, final bool) {
	buf, pos := s.buf, 0
	for pos < len(buf) {
		rel, n, e := s.scan(buf[pos:])
		if rel < 0 {
			out.Write(buf[pos:])
			pos = len(buf)
			break
		}
		j := pos + rel
		if n > len(buf)-j {
			// The longest value anchored at j extends past the buffer.
			if !final {
				out.Write(buf[pos:j])
				pos = j
				break
			}
			// End of stream: settle for the longest complete match here.
			n, e = s.completeAt(buf[j:])
			if e == nil {
				out.Write(buf[pos : j+1])
				pos = j + 1
				continue
			}
		}
		out.Write(buf[pos:j])
		out.Write(e.marker)
		pos = j + n
	}
	tail := len(s.buf) - pos
	if tail > 0 {
		copy(s.buf, s.buf[pos:])
	}
	s.buf = s.buf[:tail]
}

// scan finds the first position where a match could start and reports the
// longest value anchored there: the largest secret whose bytes agree with
// buf[j:] up to the shorter of the two. A nil entry means no position in buf
// can start a match.
func (s *Scrubber) scan(buf []byte) (int, int, *entry) {
	for j := 0; j < len(buf); j++ {
		var best *entry
		n := 0
		for i := range s.secrets {
			e := &s.secrets[i]
			shared := len(e.value)
			if len(buf)-j < shared {
				shared = len(buf) - j
			}
			if shared > 0 && bytes.HasPrefix(e.value, buf[j:j+shared]) && len(e.value) > n {
				best, n = e, len(e.value)
			}
		}
		if best != nil {
			return j, n, best
		}
	}
	return -1, 0, nil
}

// completeAt reports the longest registered value that fully matches the
// head of buf, for use at end of stream when no longer value can arrive.
func (s *Scrubber) completeAt(buf []byte) (int, *entry) {
	var best *entry
	n := 0
	for i := range s.secrets {
		e := &s.secrets[i]
		if len(e.value) <= len(buf) && bytes.HasPrefix(buf, e.value) && len(e.value) > n {
			best, n = e, len(e.value)
		}
	}
	return n, best
}

// Marker returns the replacement text Register produces for a name.
func Marker(name string) string {
	return "[" + name + " redacted]"
}
