package osc

import (
	"bytes"
	"dm/internal/wire"
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
)

func TestOSCFilter(t *testing.T) {
	var got []wire.Status
	f := &Filter{Status: func(s wire.Status) { got = append(got, s) }}
	var out bytes.Buffer

	f.Feed([]byte("hello \x1b]7501;{\"state\":\"work"), &out)
	if out.String() != "hello " {
		t.Fatalf("partial sequence leaked: %q", out.String())
	}
	f.Feed([]byte("ing\",\"message\":\"build\"}\x07world"), &out)
	if out.String() != "hello world" {
		t.Fatalf("got %q", out.String())
	}
	if len(got) != 1 || got[0].State != "working" || got[0].Message != "build" {
		t.Fatalf("status %+v", got)
	}

	out.Reset()
	f.Feed([]byte("\x1b]7501;idle\x1b\\ok"), &out)
	if out.String() != "ok" || len(got) != 2 || got[1].State != "idle" {
		t.Fatalf("ST terminator: out=%q got=%+v", out.String(), got)
	}

	out.Reset()
	f.Feed([]byte("\x1b]0;title\x07kept"), &out)
	if out.String() != "\x1b]0;title\x07kept" {
		t.Fatalf("other OSC stripped: %q", out.String())
	}
}

func TestParseStatusWireFormat(t *testing.T) {
	msg := base64.StdEncoding.EncodeToString([]byte("needs review"))
	st := ParseStatus("state=blocked:app=pi:kind=confirm:msg=" + msg)
	if st.State != "blocked" || st.Message != "needs review" || st.App != "pi" {
		t.Fatalf("pairs parsed as %+v", st)
	}
	if st := ParseStatus("state=working"); st.State != "working" || st.Message != "" {
		t.Fatalf("plain pair: %+v", st)
	}
	if st := ParseStatus("state=clear"); st.State != "unknown" || st.Message != "" {
		t.Fatalf("clear: %+v", st)
	}
	if st := ParseStatus("blocked"); st.State != "blocked" {
		t.Fatalf("bare state: %+v", st)
	}
}

func TestTerminalQueriesAnswered(t *testing.T) {
	var replies []string
	f := &Filter{
		Reply:  func(r string) { replies = append(replies, r) },
		Cursor: func() (int, int) { return 4, 9 },
		FG:     TermFG,
		BG:     TermBG,
	}
	var out bytes.Buffer
	// A colour query, a CPR, a DA1, and a normal SGR that must pass through.
	f.Feed([]byte("a\x1b]11;?\x1b\\b\x1b[6nc\x1b[cd\x1b[1me"), &out)
	if out.String() != "abcd\x1b[1me" {
		t.Fatalf("queries were not stripped: %q", out.String())
	}
	want := []string{
		"\x1b]11;rgb:0000/0000/0000\x1b\\",
		"\x1b[5;10R",
		"\x1b[?1;2c",
	}
	if len(replies) != len(want) {
		t.Fatalf("replies = %q", replies)
	}
	for i := range want {
		if replies[i] != want[i] {
			t.Fatalf("reply %d = %q, want %q", i, replies[i], want[i])
		}
	}

	// A query split across reads is still answered.
	replies = nil
	f.Feed([]byte("\x1b]10"), &out)
	f.Feed([]byte(";?\x07"), &out)
	if len(replies) != 1 || !strings.HasPrefix(replies[0], "\x1b]10;") {
		t.Fatalf("split OSC reply = %q", replies)
	}
}

func TestOSC7501QueryReply(t *testing.T) {
	queries := 0
	f := &Filter{
		Status: func(wire.Status) { t.Fatal("query must not become a status") },
		Query:  func() { queries++ },
	}
	var out bytes.Buffer
	f.Feed([]byte("a\x1b]7501;?\x1b\\b"), &out)
	if queries != 1 || out.String() != "ab" {
		t.Fatalf("queries=%d out=%q", queries, out.String())
	}
}

// legalTermColour is the two forms the server normalises: rgb:RRRR/GGGG/BBBB
// and #rrggbb.
var legalTermColour = regexp.MustCompile(`^(rgb:[0-9a-fA-F]{4}/[0-9a-fA-F]{4}/[0-9a-fA-F]{4}|#[0-9a-fA-F]{6})$`)

// TestTermColoursAreLegalOSC proves the fallback pair is a colour OSC 10 and
// OSC 11 define, so a pane that has not been told the outer terminal's pair
// still answers something termenv can parse.
func TestTermColoursAreLegalOSC(t *testing.T) {
	for _, c := range []string{TermFG, TermBG} {
		if !legalTermColour.MatchString(c) {
			t.Fatalf("%q is not rgb:RRRR/GGGG/BBBB or #rrggbb", c)
		}
	}
}

// TestParseStatusKeepsTheKnownStates proves the protocol's states survive the
// validation unchanged, in every form the parser accepts.
func TestParseStatusKeepsTheKnownStates(t *testing.T) {
	for _, state := range []string{"idle", "working", "blocked", "done", "error", "unknown"} {
		forms := []string{
			"state=" + state,
			"state=" + state + ":app=pi",
			state,
			`{"state":"` + state + `"}`,
		}
		for _, in := range forms {
			if got := ParseStatus(in).State; got != state {
				t.Fatalf("ParseStatus(%q).State = %q, want %q", in, got, state)
			}
		}
	}
}

// TestParseStatusRefusesAnUnknownState proves the state field cannot carry
// free text. Every read verb prints the state unscrubbed, so a word the
// protocol does not name must not survive the parse: a value reported as the
// state would reach `dm status` and `dm exec` in the clear.
func TestParseStatusRefusesAnUnknownState(t *testing.T) {
	const value = "poc-state-9911"
	forms := []string{
		"state=" + value,
		"state=" + value + ":app=pi",
		value,
		`{"state":"` + value + `"}`,
		"state=" + value + ":msg=" + base64.StdEncoding.EncodeToString([]byte("later")),
	}
	for _, in := range forms {
		st := ParseStatus(in)
		if st.State != "unknown" {
			t.Fatalf("ParseStatus(%q).State = %q, want unknown", in, st.State)
		}
		if strings.Contains(st.State, value) {
			t.Fatalf("ParseStatus(%q) kept the value in the state", in)
		}
	}
}

// TestParseStatusClearDropsTheMessage proves the normalisation clear always
// had still holds after validation: clear, and an empty state, leave nothing of
// the report but the app name.
func TestParseStatusClearDropsTheMessage(t *testing.T) {
	msg := base64.StdEncoding.EncodeToString([]byte("gone"))
	for _, in := range []string{"state=clear:app=pi:msg=" + msg, "state=:app=pi:msg=" + msg} {
		st := ParseStatus(in)
		if st.State != "unknown" || st.Message != "" || st.App != "pi" {
			t.Fatalf("ParseStatus(%q) = %+v, want unknown, no message, app kept", in, st)
		}
	}
}

// TestParseStatusUnknownStateKeepsTheMessage proves the state validation only
// touches the state: a report whose state is wrong still carries its message,
// which the scrub handles on its own path.
func TestParseStatusUnknownStateKeepsTheMessage(t *testing.T) {
	in := "state=poc-state-9911:msg=" + base64.StdEncoding.EncodeToString([]byte("needs review"))
	st := ParseStatus(in)
	if st.State != "unknown" || st.Message != "needs review" {
		t.Fatalf("ParseStatus(%q) = %+v, want unknown with the message", in, st)
	}
}

func TestParseStatusClearNormalised(t *testing.T) {
	// One state, one render: the bare form normalises like the keyed form.
	for _, in := range []string{"clear", "state=clear"} {
		if got := ParseStatus(in).State; got != "unknown" {
			t.Fatalf("ParseStatus(%q).State = %q, want unknown", in, got)
		}
	}
}

// wireSession renders the OSC 7502 frame: session=<b64 path>;id=<id>.
func wireSession(file, id string) string {
	return "\x1b]7502;session=" + base64.StdEncoding.EncodeToString([]byte(file)) +
		";id=" + id + "\x1b\\"
}

// TestOSCSessionFilter proves a well formed frame reaches the callback with the
// path decoded, is consumed rather than passed to the VT, and survives arriving
// split across reads.
func TestOSCSessionFilter(t *testing.T) {
	var got []Session
	f := &Filter{Session: func(s Session) { got = append(got, s) }}
	var out bytes.Buffer
	frame := wireSession("/home/me/.pi/sessions/9f2.jsonl", "9f2")

	for i := 0; i < len(frame); i += 3 {
		f.Feed([]byte(frame[i:min(i+3, len(frame))]), &out)
	}
	if out.Len() != 0 {
		t.Fatalf("the frame leaked into the stream: %q", out.String())
	}
	if len(got) != 1 || got[0].File != "/home/me/.pi/sessions/9f2.jsonl" || got[0].ID != "9f2" {
		t.Fatalf("session = %+v, want the decoded path and id", got)
	}
}

// TestOSCSessionBadFrameIgnored proves a frame with a missing or badly encoded
// path names no session: nothing reaches the callback, and the frame is still
// consumed so the VT never sees it.
func TestOSCSessionBadFrameIgnored(t *testing.T) {
	called := 0
	f := &Filter{Session: func(Session) { called++ }}
	var out bytes.Buffer
	for _, frame := range []string{
		"\x1b]7502;id=abc\x07",
		"\x1b]7502;session=!!!not-base64!!!;id=abc\x07",
		"\x1b]7502;state=working\x07",
	} {
		f.Feed([]byte(frame), &out)
	}
	if called != 0 {
		t.Fatalf("a frame with no usable path reached the callback %d times", called)
	}
	if out.Len() != 0 {
		t.Fatalf("a bad frame leaked into the stream: %q", out.String())
	}
}

// TestOSCSessionClear proves a frame with the session key and an empty value
// clears the session: the callback receives the zero pair and the frame is
// consumed rather than passed to the VT. An id on the clear frame goes with
// the session, not with it.
func TestOSCSessionClear(t *testing.T) {
	var got []Session
	f := &Filter{Session: func(s Session) { got = append(got, s) }}
	var out bytes.Buffer

	f.Feed([]byte("a\x1b]7502;session=;id=9f2\x07b"), &out)
	if out.String() != "ab" {
		t.Fatalf("the clear leaked into the stream: %q", out.String())
	}
	if len(got) != 1 || got[0] != (Session{}) {
		t.Fatalf("clear = %+v, want the zero session", got)
	}

	// The ST terminator clears the same way.
	f.Feed([]byte("\x1b]7502;session=\x1b\\"), &out)
	if len(got) != 2 || got[1] != (Session{}) {
		t.Fatalf("ST clear = %+v, want the zero session", got)
	}
}

// TestParseSessionOutcomes proves the three outcomes are distinct: a session,
// a clear, and nothing.
func TestParseSessionOutcomes(t *testing.T) {
	cases := []struct {
		in      string
		want    Session
		outcome SessionOutcome
	}{
		{"session=" + base64.StdEncoding.EncodeToString([]byte("/tmp/s.jsonl")) + ";id=9f2", Session{File: "/tmp/s.jsonl", ID: "9f2"}, SessionSet},
		{"session=", Session{}, SessionClear},
		{"session=;id=9f2", Session{}, SessionClear},
		{"session=   ", Session{}, SessionClear},
		{"id=9f2", Session{}, SessionNone},
		{"session=!!!not-base64!!!", Session{}, SessionNone},
		{"state=working", Session{}, SessionNone},
		{"", Session{}, SessionNone},
	}
	for _, tc := range cases {
		got, outcome := ParseSession(tc.in)
		if outcome != tc.outcome || got != tc.want {
			t.Errorf("ParseSession(%q) = %+v %d, want %+v %d", tc.in, got, outcome, tc.want, tc.outcome)
		}
	}
}

// TestOSC7502Consumed proves a 7502 frame is filtered out whether or not it
// decodes, and a neighbouring sequence is untouched.
func TestOSC7502Consumed(t *testing.T) {
	f := &Filter{}
	var out bytes.Buffer
	f.Feed([]byte("a"+wireSession("/tmp/s.jsonl", "s")+"b\x1b]0;title\x07"), &out)
	if out.String() != "ab\x1b]0;title\x07" {
		t.Fatalf("stream = %q", out.String())
	}
}
