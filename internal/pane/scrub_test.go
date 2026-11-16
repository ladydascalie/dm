package pane

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"dm/internal/wire"
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

// TestScrubEmitsUnmatchedChunkInFull proves a chunk whose tail cannot begin
// a match leaves the stage in the same feed: the hold-back keeps only the
// bytes a next chunk could complete, never a fixed window. A 50 byte secret
// once delayed every chunk by 49 bytes; this test fails on that behaviour.
func TestScrubEmitsUnmatchedChunkInFull(t *testing.T) {
	t.Parallel()
	secret := strings.Repeat("qwerty", 8) + "qr"
	if len(secret) != 50 {
		t.Fatalf("secret is %d bytes, want 50", len(secret))
	}
	// A prompt redraw: title set, cursor home, erased line, cursor shown,
	// newline. No suffix of it is a proper prefix of the secret.
	chunk := "\x1b]0;shell\x07\x1b[1;1H$ \x1b[K\x1b[?25h\r\n"
	var s scrubber
	s.set([]string{secret})
	var out bytes.Buffer
	s.feed([]byte(chunk), &out)
	if got := out.String(); got != chunk {
		t.Fatalf("scrub emitted %q, want the full chunk in one feed", got)
	}
}

// TestScrubToggleOffPassesThrough proves the switch off lets matched and
// unmatched bytes through untouched, in order, and that bytes an earlier
// feed held back go out first rather than vanishing.
func TestScrubToggleOffPassesThrough(t *testing.T) {
	t.Parallel()
	var s scrubber
	s.set([]string{"token1234"})
	// The hold-back takes four bytes; the switch goes off before the rest
	// of the occurrence arrives, and those bytes must still come out whole.
	var out bytes.Buffer
	s.feed([]byte("axtok"), &out)
	if got := out.String(); got != "ax" {
		t.Fatalf("pre-toggle feed emitted %q, want %q", got, "ax")
	}
	if s.toggle() {
		t.Fatal("fresh scrubber toggled to enabled, want disabled")
	}
	out.Reset()
	s.feed([]byte("en1234 mid plain\n"), &out)
	s.flush(&out)
	if got := out.String(); got != "token1234 mid plain\n" {
		t.Fatalf("disabled scrubber emitted %q, want the bytes back untouched", got)
	}
}

// TestScrubRetainsValuesAcrossToggle proves the switch off keeps the value
// list: switching back on resumes redaction with no fresh push.
func TestScrubRetainsValuesAcrossToggle(t *testing.T) {
	t.Parallel()
	var s scrubber
	s.set([]string{"token1234"})
	s.toggle()
	if !s.toggle() {
		t.Fatal("second toggle left the scrubber disabled, want enabled")
	}
	got := runScrubFeed(t, &s, "see token1234 here")
	want := "see ********* here"
	if got != want {
		t.Fatalf("re-enabled scrubber emitted %q, want %q", got, want)
	}
}

// TestScrubSetWhileDisabledStaysDisabled proves a set-secrets push while the
// switch is off updates the values and leaves the stage passing through:
// the human's off outlives a data update.
func TestScrubSetWhileDisabledStaysDisabled(t *testing.T) {
	t.Parallel()
	var s scrubber
	s.set([]string{"oldsecret"})
	s.toggle()
	s.set([]string{"newsecret"})
	got := runScrubFeed(t, &s, "newsecret plain")
	if got != "newsecret plain" {
		t.Fatalf("disabled scrubber after a set emitted %q, want the bytes back", got)
	}
	if !s.toggle() {
		t.Fatal("toggle after a disabled set did not re-enable")
	}
	// set replaced the list, so the old value scrubs no more and the new
	// one does: the push landed while the switch was off.
	got = runScrubFeed(t, &s, "newsecret oldsecret")
	want := "********* oldsecret"
	if got != want {
		t.Fatalf("re-enabled scrubber emitted %q, want %q", got, want)
	}
}

// runScrubFeed feeds one chunk through an existing scrubber and flushes,
// for tests that drive set and toggle themselves.
func runScrubFeed(t *testing.T, s *scrubber, chunk string) string {
	t.Helper()
	var out bytes.Buffer
	s.feed([]byte(chunk), &out)
	s.flush(&out)
	return out.String()
}

// TestToggleSecretsActionReportsState proves the daemon action flips the
// stage and answers with the state it landed on: enabled false first, since
// a daemon starts enabled, then true.
func TestToggleSecretsActionReportsState(t *testing.T) {
	t.Parallel()
	_, cli, msgs := yankPane(t, 20, 5)
	reply := paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "toggle-secrets", ID: 1})
	if !reply.OK {
		t.Fatalf("toggle-secrets reply = %+v, want ok", reply)
	}
	if enabled, ok := replyEnabled(t, reply); ok && enabled {
		t.Fatalf("first toggle reported enabled %v, want false", enabled)
	}
	reply = paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "toggle-secrets", ID: 2})
	if enabled, ok := replyEnabled(t, reply); !ok || !enabled {
		t.Fatalf("second toggle reported enabled %v ok %v, want true", enabled, ok)
	}
}

// replyEnabled reads the toggle-secrets reply's {"enabled":bool} body.
func replyEnabled(t *testing.T, m wire.Msg) (bool, bool) {
	t.Helper()
	var res struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(m.Res, &res); err != nil {
		t.Fatalf("toggle-secrets reply body %s: %v", m.Res, err)
	}
	return res.Enabled, len(m.Res) > 0
}

// TestToggleSecretsServesAnyConnWithoutHandshake proves the guard is gone:
// the daemon serves the toggle from a plain conn, and the first frame that
// conn receives is the reply to its own request. A handshake frame would
// have arrived first, so no nonce state exists on the daemon side.
func TestToggleSecretsServesAnyConnWithoutHandshake(t *testing.T) {
	t.Parallel()
	_, cli, msgs := yankPane(t, 20, 5)
	paneSend(t, cli, wire.Msg{Type: "req", Action: "toggle-secrets", ID: 1})
	m := <-msgs
	if m.Type != "resp" || m.ID != 1 {
		t.Fatalf("first frame %+v, want the toggle's own reply: a handshake frame exists", m)
	}
	if enabled, ok := replyEnabled(t, m); !ok || enabled {
		t.Fatalf("plain-conn toggle reported enabled %v ok %v, want false", enabled, ok)
	}
}
