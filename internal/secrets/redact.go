package secrets

import (
	"bytes"
	"strings"
)

// The redaction rule lives here so every surface draws from one place. The
// pane's output stage and the surfaces that hold text another program reads
// unasked (the box store, the ask store) share the same value bounds, the
// same matching order and the same replacement: a value redacted on the
// screen is redacted wherever else it lands, and no second matcher can drift
// from this one.
const (
	// MinRedactLen keeps a value out of the list when it is too short to
	// redact safely: a one or two byte value would blot most of the text it
	// appears in.
	MinRedactLen = 4
	// MaxRedactLen bounds one value's length, so a bad push cannot make a
	// scrub scan unbounded data.
	MaxRedactLen = 4096
	// MaxRedactValues bounds the list.
	MaxRedactValues = 256
	// RedactByte replaces every byte of a matched value. Replacement is
	// length preserving: the pane's byte counts, the box caps and the ask
	// cost stay exact.
	RedactByte = '*'
)

// Needles prepares a value list for redaction. Empty and too short values are
// dropped, a value longer than MaxRedactLen is dropped, and the list is capped
// at MaxRedactValues values, counted before the derived forms so a multi-line
// value costs one of the cap's places and not two. The forms are the
// spellings a value can take in a stream: a pty rewrites every LF to CRLF, so
// a value holding a newline is never matched byte for byte by its raw form.
func Needles(values []string) [][]byte {
	needles := make([][]byte, 0, len(values))
	used := 0
	for _, v := range values {
		if used == MaxRedactValues {
			break
		}
		if len(v) < MinRedactLen || len(v) > MaxRedactLen {
			continue
		}
		used++
		for _, form := range valueForms(v) {
			needles = append(needles, []byte(form))
		}
	}
	return needles
}

// Longest returns the length of the longest needle, zero for an empty list.
// It is the bound a streaming caller uses for its hold-back.
func Longest(needles [][]byte) int {
	maxLen := 0
	for _, v := range needles {
		maxLen = max(maxLen, len(v))
	}
	return maxLen
}

// valueForms lists the spellings a value can take in the output stream. The
// pty rewrites every LF to CRLF on output, so a value holding a newline is
// never matched byte for byte by the raw form: the CRLF form is what the
// stream actually carries. Both are returned, because the raw form costs
// nothing when the value has no newline and covers a stream the pty did not
// translate; the two forms of one value are matched independently and the
// longest match at an offset wins, so an overlap redacts the larger span.
func valueForms(v string) []string {
	if !strings.Contains(v, "\n") {
		return []string{v}
	}
	return []string{v, strings.ReplaceAll(v, "\n", "\r\n")}
}

// RedactText returns text with every value replaced by the same number of
// redaction bytes. The text is whole, so no byte is held back: a caller that
// held a prefix would have to wait for a next report that may never come.
func RedactText(text string, needles [][]byte) string {
	if text == "" || len(needles) == 0 {
		return text
	}
	data := []byte(text)
	var out bytes.Buffer
	out.Grow(len(data))
	pos := RedactInto(&out, needles, data, true)
	out.Write(data[pos:])
	return out.String()
}

// RedactBytes rewrites a whole byte range, the way a pane repairs a ring it
// already published. The range has no next chunk, so every match in it is
// final and nothing is held back.
func RedactBytes(data []byte, needles [][]byte) []byte {
	if len(data) == 0 || len(needles) == 0 {
		return data
	}
	var out bytes.Buffer
	out.Grow(len(data))
	pos := RedactInto(&out, needles, data, true)
	out.Write(data[pos:])
	return out.Bytes()
}

// RedactInto writes data with every value replaced by the same number of
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
// text), so the longest complete match is the answer and the hold ends.
func RedactInto(out *bytes.Buffer, needles [][]byte, data []byte, final bool) int {
	pos := 0
	for {
		at, n := nextMatch(needles, data, pos)
		if at < 0 {
			return pos
		}
		if !final && extendsPast(needles, data[at:]) {
			return pos
		}
		out.Write(data[pos:at])
		writeRedacted(out, n)
		pos = at + n
	}
}

// HoldBack returns the length of the longest suffix of data that is a proper
// prefix of some needle, capped at maxLen-1 because a proper prefix is never a
// whole value. Zero means nothing is held back.
func HoldBack(needles [][]byte, maxLen int, data []byte) int {
	lim := len(data)
	if lim > maxLen-1 {
		lim = maxLen - 1
	}
	for k := lim; k > 0; k-- {
		tail := data[len(data)-k:]
		for _, v := range needles {
			if len(v) > k && bytes.HasPrefix(v, tail) {
				return k
			}
		}
	}
	return 0
}

// extendsPast reports whether data is a proper prefix of a needle, so a next
// chunk could still complete a longer match at the same offset.
func extendsPast(needles [][]byte, data []byte) bool {
	for _, v := range needles {
		if len(v) > len(data) && bytes.HasPrefix(v, data) {
			return true
		}
	}
	return false
}

// nextMatch returns the offset and the length of the leftmost, longest needle
// match at or after pos, or -1 when no needle matches.
func nextMatch(needles [][]byte, data []byte, pos int) (at, n int) {
	at, n = -1, 0
	for _, v := range needles {
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

// writeRedacted appends n redaction bytes, one per value byte, so the output
// length stays the input's length.
func writeRedacted(out *bytes.Buffer, n int) {
	for range n {
		out.WriteByte(RedactByte)
	}
}
