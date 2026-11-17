package pane

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/creack/pty"

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

// TestScrubPrefersLongestAtEqualOffset proves two values where one is a
// proper prefix of the other redact whole. The server pushes values in name
// order, so the short value arrives first: a first-match rule would redact
// the prefix and publish the longer value's tail in the clear.
func TestScrubPrefersLongestAtEqualOffset(t *testing.T) {
	t.Parallel()
	got := runScrub(t, []string{"poc-", "poc-token-1234"}, "key: poc-token-1234\n")
	want := "key: " + strings.Repeat("*", len("poc-token-1234")) + "\n"
	if got != want {
		t.Fatalf("scrubbed %q, want %q", got, want)
	}
	if len(got) != len("key: poc-token-1234\n") {
		t.Fatalf("scrub changed the stream length: %q", got)
	}
	// The same list with the long value first is the control: the ordering
	// is what decides which value the scan keeps.
	got = runScrub(t, []string{"poc-token-1234", "poc-"}, "key: poc-token-1234\n")
	if got != want {
		t.Fatalf("long value first scrubbed %q, want %q", got, want)
	}
}

// TestScrubPrefersLongestAtEverySplit proves the prefix rule survives every
// chunk boundary. A cut inside the longer value leaves the shorter one as the
// only complete match in hand, and a scan that finalises it puts "****" on the
// wire and lets the longer value's tail follow raw. Both name orders, every
// split point.
func TestScrubPrefersLongestAtEverySplit(t *testing.T) {
	t.Parallel()
	const long = "poc-token-1234"
	stream := "key: " + long + "\n"
	want := "key: " + strings.Repeat("*", len(long)) + "\n"
	for _, vals := range [][]string{{"poc-", long}, {long, "poc-"}} {
		for cut := 1; cut < len(stream); cut++ {
			got := runScrub(t, vals, stream[:cut], stream[cut:])
			if got != want {
				t.Fatalf("values %v, split at %d: %q, want %q", vals, cut, got, want)
			}
			if len(got) != len(stream) {
				t.Fatalf("values %v, split at %d changed the length: %q", vals, cut, got)
			}
		}
	}
}

// TestScrubPrefersLongestByteAtATime is the worst case for the hold-back: one
// byte per chunk, so the longer value is never in the scan at the moment its
// prefix completes.
func TestScrubPrefersLongestByteAtATime(t *testing.T) {
	t.Parallel()
	const long = "poc-token-1234"
	stream := "key: " + long + "\n"
	want := "key: " + strings.Repeat("*", len(long)) + "\n"
	for _, vals := range [][]string{{"poc-", long}, {long, "poc-"}} {
		chunks := make([]string, 0, len(stream))
		for i := 0; i < len(stream); i++ {
			chunks = append(chunks, stream[i:i+1])
		}
		got := runScrub(t, vals, chunks...)
		if got != want {
			t.Fatalf("values %v, byte at a time: %q, want %q", vals, got, want)
		}
	}
}

// TestScrubFlushFinalisesAHeldPrefix proves the end of the stream settles a
// held match: the longer value can no longer complete, so the shorter one is
// redacted whole and its tail follows.
func TestScrubFlushFinalisesAHeldPrefix(t *testing.T) {
	t.Parallel()
	var s scrubber
	s.set([]string{"poc-", "poc-token-1234"})
	var out bytes.Buffer
	s.feed([]byte("poc-tok"), &out)
	if out.Len() != 0 {
		t.Fatalf("the unsettled match was on the wire already: %q", out.String())
	}
	s.flush(&out)
	if got, want := out.String(), "****tok"; got != want {
		t.Fatalf("flush emitted %q, want %q", got, want)
	}
	if out.Len() != len("poc-tok") {
		t.Fatalf("flush changed the length: %q", out.String())
	}
}

// TestScrubRedactsMultiLineValue proves a value holding a newline is
// redacted from the finished read surfaces. The pty rewrites every LF to
// CRLF on output, so the byte-exact LF value never appears in the stream:
// the stage has to hold the CRLF spelling too. The stream here comes from a
// real pty, so the translation is the terminal's and not a mock.
func TestScrubRedactsMultiLineValue(t *testing.T) {
	t.Parallel()
	value := "poc-pem-5678\nsecond-line-9999"
	stream := writeToPty(t, []byte(value+"\n"))
	if !strings.Contains(stream, strings.ReplaceAll(value, "\n", "\r\n")) {
		t.Fatalf("the pty did not translate the value's newline: %q", stream)
	}
	got := runScrub(t, []string{value}, stream)
	if strings.Contains(got, "poc-pem-5678") || strings.Contains(got, "second-line-9999") {
		t.Fatalf("the multi-line value reached the read surface: %q", got)
	}
	if want := strings.Repeat("*", len(value)+1) + "\r\n"; got != want {
		t.Fatalf("scrubbed %q, want the value redacted whole", got)
	}
	if len(got) != len(stream) {
		t.Fatalf("scrub changed the stream length: %q", got)
	}
}

// TestScrubRedactsMultiLineValueAcrossChunks proves the hold-back covers the
// derived CRLF form at every boundary: the pty's two inserted bytes can be
// split anywhere, and no prefix of the value may reach the wire first.
func TestScrubRedactsMultiLineValueAcrossChunks(t *testing.T) {
	t.Parallel()
	value := "poc-pem-5678\nsecond-line-9999"
	stream := "\x1b[0m" + writeToPty(t, []byte(value+"\n"))
	for cut := 1; cut < len(stream); cut++ {
		got := runScrub(t, []string{value}, stream[:cut], stream[cut:])
		if strings.Contains(got, "1234") || strings.Contains(got, "poc-pem") {
			t.Fatalf("split at %d leaked the value: %q", cut, got)
		}
		if len(got) != len(stream) {
			t.Fatalf("split at %d changed the stream length: %q", cut, got)
		}
	}
}

// TestScrubRedactsCRLFValue proves a value that already carries CRLF still
// redacts whole. The pty leaves the CR alone and adds one to the LF, so the
// stream holds CR CR LF and the stage's derived form is built from the same
// rule: the raw form would match only the tail and leave the CR behind.
func TestScrubRedactsCRLFValue(t *testing.T) {
	t.Parallel()
	value := "poc-crlf-5678\r\nsecond-line-9999"
	stream := writeToPty(t, []byte(value+"\n"))
	got := runScrub(t, []string{value}, stream)
	if strings.Contains(got, "5678") || strings.Contains(got, "9999") {
		t.Fatalf("the CRLF value reached the read surface: %q", got)
	}
	if want := strings.Repeat("*", len(value)+1) + "\r\n"; got != want {
		t.Fatalf("scrubbed %q, want %q", got, want)
	}
}

// TestPaneReadDrawsRedactedMultiLineValue proves the multi-line redaction
// reaches a read surface: the pane takes the server's set, the stage scrubs
// the pty's CRLF stream, and `read` draws the redaction and no part of the
// value. The stream is a real pty's output, so the newline translation is
// the terminal driver's and not the test's.
func TestPaneReadDrawsRedactedMultiLineValue(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 60, 10)
	// The adopted signal is the daemon's, and this harness builds the pane
	// by hand: the push would close a nil channel.
	p.adopted = make(chan struct{})
	value := "poc-pem-5678\nsecond-line-9999"
	body := jsonRaw(map[string]any{
		"values":    []string{value},
		"valuesB64": [][]byte{[]byte(value)},
	})
	if reply := paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "set-secrets", ID: 1, Res: body}); !reply.OK {
		t.Fatalf("set-secrets: %s", reply.Error)
	}
	var out bytes.Buffer
	p.scrub.feed([]byte(writeToPty(t, []byte(value+"\n"))), &out)
	p.mu.Lock()
	p.publishLocked(out.Bytes())
	p.mu.Unlock()
	reply := paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "read", ID: 2})
	drawn := strings.Join(reply.Lines, "\n")
	if strings.Contains(drawn, "poc-pem") || strings.Contains(drawn, "second-line") {
		t.Fatalf("the read surface drew the value: %q", drawn)
	}
	if !strings.Contains(drawn, strings.Repeat("*", len(value)+1)) {
		t.Fatalf("the read surface drew %q, want the value redacted whole", drawn)
	}
}

// TestScrubRedactsBareLFValue proves the raw LF form stays registered: a
// stream the pty did not translate still redacts whole.
func TestScrubRedactsBareLFValue(t *testing.T) {
	t.Parallel()
	value := "poc-pem-5678\nsecond-line-9999"
	got := runScrub(t, []string{value}, "x"+value+"y")
	if want := "x" + strings.Repeat("*", len(value)) + "y"; got != want {
		t.Fatalf("scrubbed %q, want %q", got, want)
	}
}

// writeToPty writes b to a real pty's output side and returns the bytes its
// master reads, so the newline translation is the terminal driver's.
//
// The output side is closed after the write, so the master reads the driver's
// whole translation and then hits the end of the stream. One read can stop
// short of what the driver wrote, and a hard coded expansion would stop
// testing the driver, so the end of the stream is the only correct stopping
// rule here.
func writeToPty(t *testing.T, b []byte) string {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty: %v", err)
	}
	t.Cleanup(func() { _ = ptmx.Close(); _ = tty.Close() })
	if _, err := tty.Write(b); err != nil {
		t.Fatalf("write pty: %v", err)
	}
	_ = tty.Close()
	var out []byte
	buf := make([]byte, 4096)
	for {
		n, err := ptmx.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			if len(out) == 0 {
				t.Fatalf("read pty: %v", err)
			}
			return string(out)
		}
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

// TestSecretValuesReadsExactBytes proves the set-secrets body carries a
// value's bytes exactly. A plain JSON string list is not a byte carrier:
// json.Marshal replaces every byte that is not valid UTF-8 with U+FFFD, so
// the pane would hold a list the registry never had and the stage would
// never match the value's real bytes.
func TestSecretValuesReadsExactBytes(t *testing.T) {
	t.Parallel()
	value := "poc-\xff\xfe-1234"
	body := jsonRaw(map[string]any{
		"values":    []string{value},
		"valuesB64": [][]byte{[]byte(value)},
	})
	values, err := secretValues(body)
	if err != nil {
		t.Fatalf("secretValues: %v", err)
	}
	if len(values) != 1 || values[0] != value {
		t.Fatalf("values = %q, want the exact bytes", values)
	}
	// The plain list alone is the legacy fallback, and it is lossy: that is
	// why the exact form exists.
	legacy, err := secretValues(jsonRaw(map[string]any{"values": []string{value}}))
	if err != nil {
		t.Fatalf("legacy secretValues: %v", err)
	}
	if len(legacy) != 1 || legacy[0] == value {
		t.Fatalf("the plain list kept the bytes: the fallback is meant to be lossy")
	}
	// An empty exact list is a clear, and neither form present is a bad body.
	if got, err := secretValues(jsonRaw(map[string]any{"valuesB64": [][]byte{}})); err != nil || len(got) != 0 {
		t.Fatalf("empty set = %q, %v, want a clear", got, err)
	}
	if _, err := secretValues(jsonRaw(map[string]any{})); err == nil {
		t.Fatal("a body with no list was accepted")
	}
}

// TestScrubRedactsWireValueWithInvalidUTF8 proves the whole path: the body
// the server sends, the pane's parse of it, and the stage. A value with two
// bytes that are not valid UTF-8 redacts whole, so neither the bad bytes nor
// the readable ones around them reach a read surface.
func TestScrubRedactsWireValueWithInvalidUTF8(t *testing.T) {
	t.Parallel()
	value := "poc-\xff\xfe-1234"
	values, err := secretValues(jsonRaw(map[string]any{
		"values":    []string{value},
		"valuesB64": [][]byte{[]byte(value)},
	}))
	if err != nil {
		t.Fatalf("secretValues: %v", err)
	}
	got := runScrub(t, values, "file: "+value+"\r\n")
	want := "file: " + strings.Repeat("*", len(value)) + "\r\n"
	if got != want {
		t.Fatalf("scrubbed %q, want %q", got, want)
	}
}

// TestSetSecretsActionHoldsExactBytes proves the pane's action stores the
// list the server sent, byte for byte: the body crosses a socket and a JSON
// frame, and the stage matches whatever the action kept.
func TestSetSecretsActionHoldsExactBytes(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 40, 5)
	// The adopted signal is the daemon's, and this harness builds the pane
	// by hand: the push would close a nil channel.
	p.adopted = make(chan struct{})
	value := "poc-\xff\xfe-1234"
	body := jsonRaw(map[string]any{
		"values":    []string{value},
		"valuesB64": [][]byte{[]byte(value)},
	})
	reply := paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "set-secrets", ID: 1, Res: body})
	if !reply.OK {
		t.Fatalf("set-secrets: %s", reply.Error)
	}
	p.scrub.mu.Lock()
	held := append([][]byte(nil), p.scrub.vals...)
	p.scrub.mu.Unlock()
	if len(held) != 1 || !bytes.Equal(held[0], []byte(value)) {
		t.Fatalf("the pane holds %q, want the value's bytes", held)
	}
	var out bytes.Buffer
	p.scrub.feed([]byte("x"+value+"y"), &out)
	if want := "x" + strings.Repeat("*", len(value)) + "y"; out.String() != want {
		t.Fatalf("the stage emitted %q, want %q", out.String(), want)
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
