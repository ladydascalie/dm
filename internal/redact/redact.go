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
//
// Register refuses a value that collides with marker text. Markers are the
// only text the scrubber injects, so the invariant is: no registered value is
// a substring of any marker, own or another's. Otherwise a marker could carry
// a secret value into the output.
package redact

import (
	"bytes"
	"fmt"
)

// Scrubber replaces registered secret values in a byte stream. It is not
// safe for concurrent use.
type Scrubber struct {
	buf     []byte
	secrets []entry
	starts  [256][]int // secret indices by first byte
	held    int        // len(buf) held as a partial prefix, 0 when none
	heldIdx int        // index of the secret the held tail is a prefix of
}

type entry struct {
	name   string
	value  []byte
	marker []byte
}

// New returns an empty scrubber. With nothing registered, Feed passes
// everything straight through.
func New() *Scrubber {
	return &Scrubber{heldIdx: -1}
}

// Register adds a named secret value. Later registrations of the same value
// replace the earlier name. Empty values are ignored: they match nothing.
//
// Registration is refused when the value would sit inside any marker, or any
// registered value would sit inside the new marker. In both cases the marker
// would carry a secret value into the output.
func (s *Scrubber) Register(name, value string) error {
	if value == "" {
		return nil
	}
	valueB := []byte(value)
	marker := []byte("[" + name + " redacted]")
	for i := range s.secrets {
		e := &s.secrets[i]
		if bytes.Contains(marker, e.value) {
			return fmt.Errorf("redact: marker for %q would contain the value of %q", name, e.name)
		}
		if bytes.Contains(e.marker, valueB) {
			return fmt.Errorf("redact: value %q is contained in the marker for %q", value, e.name)
		}
	}
	if bytes.Contains(marker, valueB) {
		return fmt.Errorf("redact: value %q is contained in its own marker", value)
	}
	e := entry{name: name, value: valueB, marker: marker}
	for i := range s.secrets {
		if bytes.Equal(s.secrets[i].value, e.value) {
			s.secrets[i] = e
			s.held = 0
			return nil
		}
	}
	s.secrets = append(s.secrets, e)
	s.starts[valueB[0]] = append(s.starts[valueB[0]], len(s.secrets)-1)
	s.held = 0
	return nil
}

// Feed appends p to the stream and writes the part that can no longer grow
// into a match to out. A trailing slice that a registered value could still
// complete inside is held until more bytes arrive. Call Flush at end of
// stream to release it.
func (s *Scrubber) Feed(p []byte, out *bytes.Buffer) {
	if len(s.secrets) == 0 {
		out.Write(p)
		return
	}
	s.buf = append(s.buf, p...)
	if s.held > 0 && len(s.buf) > s.held {
		// buf is the tail held last time, still a prefix of one secret.
		// Extend that match instead of rescanning the tail.
		e := &s.secrets[s.heldIdx]
		m, k := len(e.value), len(s.buf)
		end := m
		if k < end {
			end = k
		}
		grew := bytes.Equal(e.value[s.held:end], s.buf[s.held:end])
		s.held = 0
		switch {
		case k < m && grew:
			s.held = k // still a growing partial; hold everything
			return
		case k >= m && grew:
			out.Write(e.marker) // the remembered prefix just completed
			s.consume(m)
		}
		// Divergence or completion: the memo is spent, scan the rest.
	}
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
		rel, n, idx := s.scan(buf[pos:])
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
				s.held = len(buf) - j
				s.heldIdx = idx
				break
			}
			// End of stream: settle for the longest complete match here.
			n, idx = s.completeAt(buf[j:])
			if idx < 0 {
				out.Write(buf[pos : j+1])
				pos = j + 1
				continue
			}
		}
		out.Write(buf[pos:j])
		out.Write(s.secrets[idx].marker)
		pos = j + n
	}
	tail := len(s.buf) - pos
	if tail > 0 {
		copy(s.buf, s.buf[pos:])
	} else {
		s.held = 0
	}
	s.buf = s.buf[:tail]
}

func (s *Scrubber) consume(n int) {
	m := copy(s.buf, s.buf[n:])
	s.buf = s.buf[:m]
}

// scan finds the first position where a match could start and reports the
// longest value anchored there: the largest secret whose bytes agree with
// buf[j:] up to the shorter of the two, plus that secret's full length. A
// negative index means no position in buf can start a match.
func (s *Scrubber) scan(buf []byte) (int, int, int) {
	for j := 0; j < len(buf); j++ {
		n, idx := s.longestAt(buf, j)
		if idx >= 0 {
			return j, n, idx
		}
	}
	return -1, 0, -1
}

// longestAt reports the longest secret anchored at buf[j] and its index.
func (s *Scrubber) longestAt(buf []byte, j int) (int, int) {
	n, idx := 0, -1
	for _, i := range s.starts[buf[j]] {
		e := &s.secrets[i]
		shared := len(e.value)
		if avail := len(buf) - j; avail < shared {
			shared = avail
		}
		if bytes.HasPrefix(e.value, buf[j:j+shared]) && len(e.value) > n {
			n, idx = len(e.value), i
		}
	}
	return n, idx
}

// completeAt reports the longest registered value that fully matches the
// head of buf, for use at end of stream when no longer value can arrive.
func (s *Scrubber) completeAt(buf []byte) (int, int) {
	n, idx := 0, -1
	for _, i := range s.starts[buf[0]] {
		e := &s.secrets[i]
		if len(e.value) <= len(buf) && bytes.HasPrefix(buf, e.value) && len(e.value) > n {
			n, idx = len(e.value), i
		}
	}
	return n, idx
}

// Marker returns the replacement text Register produces for a name.
func Marker(name string) string {
	return "[" + name + " redacted]"
}
