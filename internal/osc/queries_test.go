package osc

import (
	"bytes"
	"dm/internal/wire"
	"strings"
	"testing"
)

// TestPaletteQueryAnswersXtermColour feeds the OSC 4 palette query and checks
// the reply carries the colour the 256-colour table defines for that index.
func TestPaletteQueryAnswersXtermColour(t *testing.T) {
	var replies []string
	f := &Filter{Reply: func(r string) { replies = append(replies, r) }}
	var out bytes.Buffer
	f.Feed([]byte("\x1b]4;2;?\x07n"), &out)
	if out.String() != "n" {
		t.Fatalf("palette query leaked: %q", out.String())
	}
	if len(replies) != 1 {
		t.Fatalf("replies = %q", replies)
	}
	// Index 2 is green: 0, 205, 0, doubled per channel.
	if want := "\x1b]4;2;theme.RGB:0000/cdcd/0000\x1b\\"; replies[0] != want {
		t.Fatalf("reply = %q, want %q", replies[0], want)
	}
}

// TestPaletteQueryBadIndex tests an index the table has no answer for. A query
// with no index, a non-numeric one, or one past 255 gets no reply, and dm
// passes the raw sequence to the VT, which answers nothing.
func TestPaletteQueryBadIndex(t *testing.T) {
	cases := []string{
		"\x1b]4;;?\x07",       // no index
		"\x1b]4;x;?\x07",      // not a number
		"\x1b]4;12a;?\x07",    // not a number
		"\x1b]4;256;?\x07",    // past the table
		"\x1b]4;999999;?\x07", // overflow
	}
	for _, seq := range cases {
		var replies []string
		f := &Filter{Reply: func(r string) { replies = append(replies, r) }}
		var out bytes.Buffer
		f.Feed([]byte(seq), &out)
		if len(replies) != 0 {
			t.Errorf("%q replied %q", seq, replies)
		}
		if out.String() != seq {
			t.Errorf("%q came back as %q, want the raw sequence", seq, out.String())
		}
	}
}

// TestParseIndex pins the index parser: empty, non-numeric and out-of-range
// input is an error, and 255 is the last legal index.
func TestParseIndex(t *testing.T) {
	for _, s := range []string{"", "x", "1x", "256", "00000000000000300"} {
		if _, err := parseIndex(s); err == nil {
			t.Errorf("parseIndex(%q) accepted", s)
		}
	}
	if n, err := parseIndex("255"); err != nil || n != 255 {
		t.Errorf("parseIndex(255) = %d, %v", n, err)
	}
}

// TestXtermColorRoundsThePalette checks one colour from each band of the
// 256-colour table: the 16 base colours, the 6x6x6 cube, and the grayscale ramp.
func TestXtermColorRoundsThePalette(t *testing.T) {
	cases := map[int]string{
		0:   "theme.RGB:0000/0000/0000", // black
		1:   "theme.RGB:cdcd/0000/0000", // red, doubled per channel
		16:  "theme.RGB:0000/0000/0000", // cube corner 0,0,0
		231: "theme.RGB:ffff/ffff/ffff", // cube corner 5,5,5
		232: "theme.RGB:0808/0808/0808", // grayscale start
		255: "theme.RGB:eeee/eeee/eeee", // grayscale end
	}
	for n, want := range cases {
		if got := xtermColor(n); got != want {
			t.Errorf("xtermColor(%d) = %q, want %q", n, got, want)
		}
	}
}

// TestUnhandledQueriesPassThrough checks a CSI and an OSC dm does not answer
// reach the VT untouched. Stripping them would break the program that sent them.
func TestUnhandledQueriesPassThrough(t *testing.T) {
	f := &Filter{}
	var out bytes.Buffer
	in := "\x1b]52;c;aGk=\x07\x1b[2Jtail"
	f.Feed([]byte(in), &out)
	if out.String() != in {
		t.Fatalf("got %q, want %q", out.String(), in)
	}
}

// TestAnsweredQueriesNeedNoHandlers pins that the identity, status-report and
// colour queries are consumed and replied to even with the callbacks unset,
// so a pane that wires no handlers still answers termenv and hangs nothing.
func TestAnsweredQueriesNeedNoHandlers(t *testing.T) {
	f := &Filter{FG: TermFG, BG: TermBG}
	var out bytes.Buffer
	var replies []string
	f.Reply = func(r string) { replies = append(replies, r) }
	f.Feed([]byte("\x1b[6n\x1b[c\x1b[0c\x1b[>c\x1b[>0c\x1b[5n\x1b]10;?\x07\x1b]11;?\x07\x1b]12;?\x07"), &out)
	if out.String() != "" {
		t.Fatalf("queries leaked: %q", out.String())
	}
	want := []string{
		// A nil Cursor answers nothing: the query is consumed silently.
		"\x1b[?1;2c",
		"\x1b[?1;2c",
		"\x1b[>0;10;1c",
		"\x1b[>0;10;1c",
		"\x1b[0n",
		"\x1b]10;" + TermFG + "\x1b\\",
		"\x1b]11;" + TermBG + "\x1b\\",
		"\x1b]12;" + TermFG + "\x1b\\",
	}
	if len(replies) != len(want) {
		t.Fatalf("replies = %q, want %d", replies, len(want))
	}
	for i := range want {
		if replies[i] != want[i] {
			t.Errorf("reply %d = %q, want %q", i, replies[i], want[i])
		}
	}
}

// TestEscapedOtherSequencePassesThrough checks an escape sequence that is
// neither OSC nor CSI, such as the reverse-index ESC M, reaches the VT.
func TestEscapedOtherSequencePassesThrough(t *testing.T) {
	f := &Filter{}
	var out bytes.Buffer
	f.Feed([]byte("a\x1bMb"), &out)
	if out.String() != "a\x1bMb" {
		t.Fatalf("got %q", out.String())
	}
}

// TestSplitEscapeByteHolds checks a lone ESC at the end of a read waits for
// the byte that decides what it starts, instead of leaking.
func TestSplitEscapeByteHolds(t *testing.T) {
	f := &Filter{}
	var out bytes.Buffer
	f.Feed([]byte("ok\x1b"), &out)
	if out.String() != "ok" {
		t.Fatalf("lone ESC leaked: %q", out.String())
	}
	f.Feed([]byte("M"), &out)
	if out.String() != "ok\x1bM" {
		t.Fatalf("held ESC lost: %q", out.String())
	}
}

// TestRunawayOSCDropped checks an unterminated OSC longer than the buffer cap
// is flushed rather than buffered forever, and cannot come back as a status
// when the terminator arrives in a later read.
func TestRunawayOSCDropped(t *testing.T) {
	statuses := 0
	f := &Filter{Status: func(wire.Status) { statuses++ }}
	var out bytes.Buffer
	f.Feed([]byte("\x1b]7501;"+strings.Repeat("a", 9000)), &out)
	if !strings.HasPrefix(out.String(), "\x1b]7501;") {
		t.Fatalf("runaway sequence not flushed: %d bytes, prefix %q", out.Len(), out.String()[:12])
	}
	if out.Len() < 9000 {
		t.Fatalf("runaway sequence truncated: %d bytes", out.Len())
	}
	out.Reset()
	after := "}\x07plain"
	f.Feed([]byte(after), &out)
	if out.String() != after {
		t.Fatalf("terminator after drop mis-parsed: %q", out.String())
	}
	if statuses != 0 {
		t.Fatalf("dropped sequence became a status")
	}
}

// TestRunawayCSIFlushed checks an unterminated CSI longer than its cap
// unblocks the stream: the ESC [ goes out, the parameters stay buffered until
// a final byte arrives.
func TestRunawayCSIFlushed(t *testing.T) {
	f := &Filter{}
	var out bytes.Buffer
	in := "\x1b[" + strings.Repeat("0", 63)
	f.Feed([]byte(in), &out)
	// A CSI of 65 bytes is not a query dm answers, so the whole run passes
	// through: the cap flushes it, and nothing is buffered for a final byte.
	if out.String() != in {
		t.Fatalf("got %q, want the whole run flushed", out.String())
	}
	f.Feed([]byte("m"), &out)
	if !strings.HasSuffix(out.String(), "m") {
		t.Fatalf("trailing final byte lost: %q", out.String())
	}
}

// TestCSIControlByteHolds checks a CSI carrying a control byte before its
// final byte is held incomplete, not leaked half-parsed.
func TestCSIControlByteHolds(t *testing.T) {
	f := &Filter{}
	var out bytes.Buffer
	f.Feed([]byte("x\x1b[\x07m"), &out)
	if out.String() != "x" {
		t.Fatalf("malformed CSI leaked: %q", out.String())
	}
}

// TestParseStatusJSONForm tests the JSON form dm accepted before the wire
// format, and the shapes that fall through it.
func TestParseStatusJSONForm(t *testing.T) {
	st := ParseStatus(`{"state":"working","app":"ci","message":"build"}`)
	if st.State != "working" || st.App != "ci" || st.Message != "build" {
		t.Fatalf("JSON: %+v", st)
	}
	// The JSON form spells the message "message", in plain text; the wire
	// format's "msg" key is not a JSON field and carries nothing.
	if st := ParseStatus(`{"state":"working","msg":"build"}`); st.State != "working" || st.Message != "" {
		t.Fatalf("JSON with wire-style msg: %+v", st)
	}
	if st := ParseStatus(`{"app":"ci"}`); st.State != "unknown" || st.App != "ci" {
		t.Fatalf("JSON without state: %+v", st)
	}
	// A message that is not base64 is dropped, not garbled.
	if st := ParseStatus("state=working:msg=!!!not base64"); st.Message != "" || st.State != "working" {
		t.Fatalf("bad base64: %+v", st)
	}
	// Whitespace around the payload does not change the parse.
	if st := ParseStatus("  state=working  "); st.State != "working" {
		t.Fatalf("padded: %+v", st)
	}
	// clear clears the message too.
	if st := ParseStatus("state=clear:msg="); st.State != "unknown" || st.Message != "" {
		t.Fatalf("clear with pairs: %+v", st)
	}
}
