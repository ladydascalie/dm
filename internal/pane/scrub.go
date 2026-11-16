package pane

import (
	"bytes"
	"encoding/json"
	"fmt"
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
// stream. set is called from the serve loop; feed and flush run only on the
// pty reader goroutine, which owns carry. vals is guarded by mu.
type scrubber struct {
	mu     sync.Mutex
	vals   [][]byte
	maxLen int
	carry  []byte
}

// set replaces the secret list. Empty and too short values are dropped, a
// value longer than secretMax is dropped, and the list is capped at
// secretCap values. An empty list turns the stage into a pass-through that
// still drains any bytes a previous list held back.
func (s *scrubber) set(values []string) {
	vals := make([][]byte, 0, len(values))
	maxLen := 0
	for _, v := range values {
		if len(v) < MinSecretLen || len(v) > MaxSecretLen {
			continue
		}
		vals = append(vals, []byte(v))
		if len(v) > maxLen {
			maxLen = len(v)
		}
		if len(vals) == MaxSecrets {
			break
		}
	}
	s.mu.Lock()
	s.vals, s.maxLen = vals, maxLen
	s.mu.Unlock()
}

// feed appends one chunk to the output. It writes every byte it can swear
// to into out and holds back the trailing maxLen-1 bytes in carry, because
// an occurrence may straddle the chunk boundary and only a hold-back keeps
// its first half from already being on the wire.
func (s *scrubber) feed(b []byte, out *bytes.Buffer) {
	s.mu.Lock()
	vals, maxLen := s.vals, s.maxLen
	s.mu.Unlock()

	// The steady state of a pane with no secrets: nothing held, nothing to
	// scan, the chunk passes through untouched.
	if maxLen == 0 && len(s.carry) == 0 {
		out.Write(b)
		return
	}
	data := append(s.carry, b...)
	s.carry = s.carry[:0]
	pos := 0
	for {
		best, bestLen := -1, 0
		for _, v := range vals {
			if i := bytes.Index(data[pos:], v); i >= 0 && (best < 0 || pos+i < best) {
				best, bestLen = pos+i, len(v)
			}
		}
		if best < 0 {
			break
		}
		out.Write(data[pos:best])
		writeRedacted(out, bestLen)
		pos = best + bestLen
	}
	// Bytes within maxLen-1 of the end may begin an occurrence the next
	// chunk completes, so they wait in carry. A match that reached past the
	// window already consumed its bytes, and they are redacted on the wire.
	hold := maxLen - 1
	if hold < 0 {
		hold = 0
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
	vals := s.vals
	s.mu.Unlock()
	data := s.carry
	s.carry = nil
	if len(data) == 0 {
		return
	}
	if len(vals) == 0 {
		out.Write(data)
		return
	}
	pos := 0
	for {
		best, bestLen := -1, 0
		for _, v := range vals {
			if i := bytes.Index(data[pos:], v); i >= 0 && (best < 0 || pos+i < best) {
				best, bestLen = pos+i, len(v)
			}
		}
		if best < 0 {
			break
		}
		out.Write(data[pos:best])
		writeRedacted(out, bestLen)
		pos = best + bestLen
	}
	out.Write(data[pos:])
}

// writeRedacted appends n redaction bytes, one per secret byte, so the
// output length stays the stream's length.
func writeRedacted(out *bytes.Buffer, n int) {
	for range n {
		out.WriteByte(redactByte)
	}
}

// secretValues reads a set-secrets request body: {"values":["..."]}. The
// payload rides Res because it is server-only and the envelope has no field
// for a string list.
func secretValues(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("set-secrets needs a {\"values\":[...]} body")
	}
	var payload struct {
		Values []string `json:"values"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("set-secrets body: %w", err)
	}
	return payload.Values, nil
}
