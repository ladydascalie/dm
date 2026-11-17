package pane

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// The scrub stage sits at the end of the pane's output filter chain, after
// the OSC filter and before publish. It replaces every occurrence of a
// secret value with the same number of redaction bytes, so the screen, the
// scroll ring, the subscriber broadcasts and every read-back draw redacted
// output and no ring or snapshot ever holds the secret.
//
// Replacement is length preserving on purpose: produced counts published
// bytes, and the anchor and yank offsets are positions in that same stream.
// A stage that shortened or lengthened chunks would move every mark.

const (
	// redactByte replaces every byte of a matched secret.
	redactByte = '*'
)

// The scrub bounds are shared policy: the server reads them to warn the
// operator when a value cannot be redacted, so a short pin never echoes
// unredacted while the surface reports success.
const (
	// MinSecretLen keeps a value out of the list when it is too short to
	// redact safely: a one or two byte value would blot most of the pane's
	// ordinary output.
	MinSecretLen = 4
	// MaxSecretLen bounds one value's length, so a bad push cannot make the
	// pane scan unbounded data per chunk.
	MaxSecretLen = 4096
	// MaxSecrets bounds the list.
	MaxSecrets = 256
)

// scrubber holds the pane's secret values and redacts them from the output
// stream. set and toggle are called from the serve loop; feed and flush run
// only on the pty reader goroutine, which owns carry. vals and off are
// guarded by mu.
type scrubber struct {
	mu     sync.Mutex
	vals   [][]byte
	maxLen int
	carry  []byte
	// off is the redaction switch. The zero value keeps the stage scrubbing,
	// so a respawned daemon and a fresh scrubber start in the safe state:
	// output already carries secrets the list must catch.
	off bool
}

// set replaces the secret list. Empty and too short values are dropped, a
// value longer than MaxSecretLen is dropped, and the list is capped at
// MaxSecrets values, counted before the derived forms so a multi-line value
// costs one of the cap's places and not two. An empty list turns the stage
// into a pass-through that still drains any bytes a previous list held back.
func (s *scrubber) set(values []string) {
	vals := make([][]byte, 0, len(values))
	maxLen := 0
	used := 0
	for _, v := range values {
		if used == MaxSecrets {
			break
		}
		if len(v) < MinSecretLen || len(v) > MaxSecretLen {
			continue
		}
		used++
		for _, form := range valueForms(v) {
			vals = append(vals, []byte(form))
			maxLen = max(maxLen, len(form))
		}
	}
	s.mu.Lock()
	s.vals, s.maxLen = vals, maxLen
	s.mu.Unlock()
}

// valueForms lists the spellings a value can take in the output stream. The
// pty rewrites every LF to CRLF on output, so a value holding a newline is
// never matched byte for byte by the raw form: the CRLF form is what the
// stream actually carries. Both are returned, because the raw form costs
// nothing when the value has no newline and covers a stream the pty did not
// translate; the two forms of one value are matched independently and the
// longest match at an offset wins, so an overlap redacts the larger span.
//
// The derived form is at most twice its value, so a list at MaxSecretLen
// keeps the scan and the hold-back bounded by twice the shared cap.
func valueForms(v string) []string {
	if !strings.Contains(v, "\n") {
		return []string{v}
	}
	return []string{v, strings.ReplaceAll(v, "\n", "\r\n")}
}

// toggle flips the redaction switch and reports the new state: true when
// the stage scrubs again. The value list survives both ways, so a toggle
// off and back on needs no fresh set-secrets push.
func (s *scrubber) toggle() bool {
	s.mu.Lock()
	s.off = !s.off
	on := !s.off
	s.mu.Unlock()
	return on
}

// feed appends one chunk to the output. It writes every byte it can swear
// to into out and holds back only the bytes a next chunk could complete in
// carry, because an occurrence may straddle the chunk boundary and only a
// hold-back keeps its first half from already being on the wire.
func (s *scrubber) feed(b []byte, out *bytes.Buffer) {
	s.mu.Lock()
	vals, maxLen, off := s.vals, s.maxLen, s.off
	s.mu.Unlock()

	// The switch is off: every byte flows through untouched, in order,
	// while the value list waits for the switch back. Anything an earlier
	// list held back goes out first, so the stream keeps its order.
	if off {
		out.Write(s.carry)
		s.carry = s.carry[:0]
		out.Write(b)
		return
	}

	// The steady state of a pane with no secrets: nothing held, nothing to
	// scan, the chunk passes through untouched.
	if maxLen == 0 && len(s.carry) == 0 {
		out.Write(b)
		return
	}
	data := append(s.carry, b...)
	s.carry = s.carry[:0]
	// A match is not final while the data stops inside a longer value that
	// starts at the same offset: the scan gives those bytes back, and the
	// hold-back below takes them from the match start. That is what keeps a
	// short value that is a proper prefix of a long one from finalising at a
	// chunk boundary.
	pos := redactInto(out, vals, data, false)
	// Only the bytes a next chunk could complete wait in carry: the longest
	// suffix of the unemitted data that is a proper prefix of some value.
	// A tail that cannot begin a match flows with zero delay, the same rule
	// the OSC filter applies to partial sequences.
	hold := 0
	if maxLen > 1 {
		hold = holdBack(vals, maxLen, data[pos:])
	}
	cut := len(data) - hold
	if cut < 0 {
		cut = 0
	}
	if cut < pos {
		cut = pos
	}
	out.Write(data[pos:cut])
	s.carry = append(s.carry, data[cut:]...)
}

// holdBack returns the length of the longest suffix of data that is a
// proper prefix of some value in vals, capped at maxLen-1 because a proper
// prefix is never a whole value. Zero means nothing is held back.
func holdBack(vals [][]byte, maxLen int, data []byte) int {
	lim := len(data)
	if lim > maxLen-1 {
		lim = maxLen - 1
	}
	for k := lim; k > 0; k-- {
		tail := data[len(data)-k:]
		for _, v := range vals {
			if len(v) > k && bytes.HasPrefix(v, tail) {
				return k
			}
		}
	}
	return 0
}

// flush drains the held-back bytes at the end of the stream. No chunk
// follows, so every remaining byte can be examined whole and nothing stays
// behind in the stage.
func (s *scrubber) flush(out *bytes.Buffer) {
	s.mu.Lock()
	vals, off := s.vals, s.off
	s.mu.Unlock()
	data := s.carry
	s.carry = nil
	if len(data) == 0 {
		return
	}
	if off || len(vals) == 0 {
		out.Write(data)
		return
	}
	pos := redactInto(out, vals, data, true)
	out.Write(data[pos:])
}

// redactText replaces every value in text with the same number of redaction
// bytes and returns the result. It is the metadata path: a status report is
// decoded out of the stream before the stage runs, so it has to be scrubbed by
// the same matcher and the same value list. There is no hold-back here: the
// text is whole, and a caller that held a prefix would have to wait for a next
// report that may never come.
//
// The switch does not apply. Off is a human's window into the pane's output
// bytes, which the stage feeds; the text this scrubs is the status a report
// carries to the box, the desktop notification and the state file, and none of
// those is a surface the toggle reaches.
func (s *scrubber) redactText(text string) string {
	if text == "" {
		return text
	}
	s.mu.Lock()
	vals, maxLen := s.vals, s.maxLen
	s.mu.Unlock()
	if maxLen == 0 || len(vals) == 0 {
		return text
	}
	data := []byte(text)
	var out bytes.Buffer
	out.Grow(len(data))
	pos := redactInto(&out, vals, data, true)
	out.Write(data[pos:])
	return out.String()
}

// redactInto writes data with every value replaced by the same number of
// redaction bytes and returns the offset it stopped emitting at: every byte
// before it is on the wire, redacted or not, and the bytes from it on are not.
// That is the end of the last finalised match, the start of a match the data
// cannot yet settle, or zero when no value matched.
//
// Matching is leftmost-longest: at each position the match that starts first
// wins, and among values that start at the same offset the longest wins. The
// longest rule is load bearing, not a tie-break: the server pushes values in
// name order, so a value that is a proper prefix of another one arrives
// first, and redacting only the prefix would publish the longer value's tail
// in the clear.
//
// The longest rule only holds over the bytes in hand. A chunk can end inside a
// longer value that starts at the same offset, and then the shorter value is
// the only complete match: finalising it would put its redaction on the wire
// and flush the longer value's tail after it. So a match whose bytes are a
// proper prefix of a longer value is not finalised while more data can come:
// the scan stops before it and the caller holds the bytes from the match start
// for the next chunk. final says no more data can come (a flush, or a whole
// status text), so the longest complete match is the answer and the hold ends.
func redactInto(out *bytes.Buffer, vals [][]byte, data []byte, final bool) int {
	pos := 0
	for {
		at, n := nextMatch(vals, data, pos)
		if at < 0 {
			return pos
		}
		if !final && extendsPast(vals, data[at:]) {
			return pos
		}
		out.Write(data[pos:at])
		writeRedacted(out, n)
		pos = at + n
	}
}

// extendsPast reports whether data is a proper prefix of a registered value,
// so a next chunk could still complete a longer match at the same offset.
func extendsPast(vals [][]byte, data []byte) bool {
	for _, v := range vals {
		if len(v) > len(data) && bytes.HasPrefix(v, data) {
			return true
		}
	}
	return false
}

// nextMatch returns the offset and the length of the leftmost, longest value
// match at or after pos, or -1 when no value matches.
func nextMatch(vals [][]byte, data []byte, pos int) (at, n int) {
	at, n = -1, 0
	for _, v := range vals {
		i := bytes.Index(data[pos:], v)
		if i < 0 {
			continue
		}
		got := pos + i
		if at < 0 || got < at || (got == at && len(v) > n) {
			at, n = got, len(v)
		}
	}
	return at, n
}

// writeRedacted appends n redaction bytes, one per secret byte, so the
// output length stays the stream's length.
func writeRedacted(out *bytes.Buffer, n int) {
	for range n {
		out.WriteByte(redactByte)
	}
}

// secretValues reads a set-secrets request body:
// {"valuesB64":["<base64>",...]}. The payload rides Res because it is
// server-only and the envelope has no field for a byte list.
//
// The list is base64 and not a JSON string list because the registry holds
// raw bytes. json.Marshal replaces every byte of a Go string that is not
// valid UTF-8 with U+FFFD, so a plain string list hands the pane a different
// byte string than the value and the stage never matches the value's real
// bytes. A []byte field marshals as base64, so the pane's list is
// byte-identical to the registry's.
//
// The legacy plain "values" list is still read when "valuesB64" is absent.
// It exists for a pane daemon older than this encoding, which a running
// server can outlive: the old daemon keeps the redaction it had instead of
// answering the push with an error and redacting nothing. A pane that reads
// valuesB64 never looks at the plain list.
func secretValues(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("set-secrets needs a {\"valuesB64\":[...]} body")
	}
	var payload struct {
		ValuesB64 [][]byte `json:"valuesB64"`
		Values    []string `json:"values"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("set-secrets body: %w", err)
	}
	if payload.ValuesB64 != nil {
		values := make([]string, len(payload.ValuesB64))
		for i, v := range payload.ValuesB64 {
			values[i] = string(v)
		}
		return values, nil
	}
	if payload.Values == nil {
		return nil, fmt.Errorf("set-secrets needs a {\"valuesB64\":[...]} body")
	}
	return payload.Values, nil
}
