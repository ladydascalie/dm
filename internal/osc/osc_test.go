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
