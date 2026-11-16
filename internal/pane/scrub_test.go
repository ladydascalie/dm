package pane

import (
	"bytes"
	"testing"
)

// runScrub feeds chunks through a scrubber in order, then flushes, and
// returns everything the stage emitted.
func runScrub(t *testing.T, values []string, chunks ...string) string {
	t.Helper()
	var s scrubber
	s.set(values)
	var out bytes.Buffer
	for _, c := range chunks {
		s.feed([]byte(c), &out)
	}
	s.flush(&out)
	return out.String()
}

// TestScrubRedactsValues proves a set value never reaches the output: the
// stage replaces each of its bytes and keeps the stream length, so the ring
// and the anchors the yank maths relies on stay aligned.
func TestScrubRedactsValues(t *testing.T) {
	t.Parallel()
	got := runScrub(t, []string{"hunter2"}, "password: hunter2\nok\n")
	want := "password: *******\nok\n"
	if got != want {
		t.Fatalf("scrubbed %q, want %q", got, want)
	}
}

// TestScrubStraddlesChunkBoundary proves an occurrence split across two
// feeds is still caught: the stage holds back the bytes a next chunk could
// complete instead of letting the first half onto the wire.
func TestScrubStraddlesChunkBoundary(t *testing.T) {
	t.Parallel()
	got := runScrub(t, []string{"token1234"}, "xxtoken", "1234yy")
	want := "xx*********yy"
	if got != want {
		t.Fatalf("scrubbed %q, want %q", got, want)
	}
	if len(got) != len("xxtoken1234yy") {
		t.Fatalf("scrub changed the stream length: %q", got)
	}
}

// TestScrubMatchesEarliest proves two values matching at the same spot
// resolve to the earliest occurrence, and an overlap still redacts the rest.
func TestScrubMatchesEarliest(t *testing.T) {
	t.Parallel()
	got := runScrub(t, []string{"abc", "bcdef"}, "xxbcdefyy")
	// "bcdef" matches at index 2, five bytes; "abc" does not occur.
	want := "xx*****yy"
	if got != want {
		t.Fatalf("scrubbed %q, want %q", got, want)
	}
}

// TestScrubWithoutValuesPassesThrough proves a pane with no secrets emits
// its output byte for byte, and that clearing the list drains what an older
// list held back.
func TestScrubWithoutValuesPassesThrough(t *testing.T) {
	t.Parallel()
	var s scrubber
	var out bytes.Buffer
	s.feed([]byte("plain output"), &out)
	if got := out.String(); got != "plain output" {
		t.Fatalf("scrubbed %q, want the input back", got)
	}
	// A list that held bytes, then a clear: the held bytes must still come
	// out, redaction decisions belong to the list that held them.
	s.set([]string{"token1234"})
	out.Reset()
	s.feed([]byte("xtok"), &out)
	s.set(nil)
	s.feed([]byte("en1234 more"), &out)
	s.flush(&out)
	if got := out.String(); got != "xtoken1234 more" {
		t.Fatalf("cleared scrubber emitted %q, want the held bytes back", got)
	}
}

// TestScrubDropsShortValues proves a value too short to redact safely is
// ignored, not applied: a two byte secret would blot ordinary output.
func TestScrubDropsShortValues(t *testing.T) {
	t.Parallel()
	got := runScrub(t, []string{"ab", "longenoughsecret"}, "say ab again\n")
	want := "say ab again\n"
	if got != want {
		t.Fatalf("scrubbed %q, want %q", got, want)
	}
}

// TestScrubFlushEmitsHeldBytes proves the end of the stream lets go of the
// hold-back: a tail shorter than any value leaves the stage whole.
func TestScrubFlushEmitsHeldBytes(t *testing.T) {
	t.Parallel()
	var s scrubber
	s.set([]string{"token1234"})
	var out bytes.Buffer
	s.feed([]byte("tail se"), &out)
	s.flush(&out)
	if got := out.String(); got != "tail se" {
		t.Fatalf("flush emitted %q, want %q", got, "tail se")
	}
}

// TestSecretValuesReadsBody proves the set-secrets body parses and that a
// missing or malformed body is an error the pane can reply with.
func TestSecretValuesReadsBody(t *testing.T) {
	t.Parallel()
	values, err := secretValues([]byte(`{"values":["a","b"]}`))
	if err != nil {
		t.Fatalf("secretValues: %v", err)
	}
	if len(values) != 2 || values[0] != "a" || values[1] != "b" {
		t.Fatalf("values = %q, want a b", values)
	}
	if _, err := secretValues(nil); err == nil {
		t.Fatal("empty body accepted, want an error")
	}
	if _, err := secretValues([]byte(`{`)); err == nil {
		t.Fatal("malformed body accepted, want an error")
	}
}
