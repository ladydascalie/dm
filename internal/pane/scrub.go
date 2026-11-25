package pane

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"dm/internal/secrets"
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
//
// The rule itself lives in secrets, which holds the one matcher the box and
// ask stores use too: the bounds, the value forms, the leftmost-longest order
// and the redaction byte. This type adds only what a stream needs: the
// carry between chunks and the reveal switch.

// The scrub bounds are shared policy: the server reads them to warn the
// operator when a value cannot be redacted, and the box and ask stores hold
// the same list, so a short pin never echoes unredacted on one surface while
// another redacts it.
const (
	// MinSecretLen keeps a value out of the list when it is too short to
	// redact safely: a one or two byte value would blot most of the pane's
	// ordinary output.
	MinSecretLen = secrets.MinRedactLen
	// MaxSecretLen bounds one value's length, so a bad push cannot make the
	// pane scan unbounded data per chunk.
	MaxSecretLen = secrets.MaxRedactLen
	// MaxSecrets bounds the list.
	MaxSecrets = secrets.MaxRedactValues
)

// scrubber holds the pane's secret values and redacts them from the output
// stream. set and toggle are called from the serve loop; feed and flush run
// only on the pty reader goroutine, which owns carry. vals and off are
// guarded by mu.
//
// The lock order with the pane is the pane's mu then this one: the status
// writer and the set path scrub while they hold p.mu, so a report can never
// scrub on one list and store under another. This type never calls back into
// the pane, so nothing takes them the other way.
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
	vals := secrets.Needles(values)
	maxLen := secrets.Longest(vals)
	s.mu.Lock()
	s.vals, s.maxLen = vals, maxLen
	s.mu.Unlock()
}

// screenValues returns the needles a pane's screen is rewritten with when the
// switch returns on. They are one value per entry, not the CRLF form the
// stream stage needs: a newline is a row break in the grid, never a cell run,
// so a multi-line value is split into the lines the terminal actually drew.
// A line shorter than MinSecretLen is left out for the reason set drops a
// short value: it would blot ordinary text without protecting a secret.
//
// The list is copied out under the scrubber's lock: the pane takes it while
// it holds its own lock, and the documented order is the pane then this one.
func (s *scrubber) screenValues() []string {
	s.mu.Lock()
	vals := s.vals
	s.mu.Unlock()
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		text := string(v)
		if !strings.Contains(text, "\n") {
			out = append(out, text)
			continue
		}
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSuffix(line, "\r")
			if len(line) >= MinSecretLen {
				out = append(out, line)
			}
		}
	}
	return out
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
	pos := secrets.RedactInto(out, vals, data, false)
	// Only the bytes a next chunk could complete wait in carry: the longest
	// suffix of the unemitted data that is a proper prefix of some value.
	// A tail that cannot begin a match flows with zero delay, the same rule
	// the OSC filter applies to partial sequences.
	hold := 0
	if maxLen > 1 {
		hold = secrets.HoldBack(vals, maxLen, data[pos:])
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
	pos := secrets.RedactInto(out, vals, data, true)
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
	vals := s.vals
	s.mu.Unlock()
	return secrets.RedactText(text, vals)
}

// scrubBytes rewrites the bytes a pane already published. It is the toggle's
// repair path: while the switch was off the stage passed the value through
// untouched, so the ring and every read-back built from it hold it. The ring
// is a whole stream with no next chunk, so every match in it is final and no
// byte is held back.
//
// The switch does not apply. This runs on the way back on, where the caller
// has already decided that the history the reveal built must not survive.
func (s *scrubber) scrubBytes(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	s.mu.Lock()
	vals := s.vals
	s.mu.Unlock()
	return secrets.RedactBytes(data, vals)
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
