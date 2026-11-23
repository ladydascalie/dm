package cli

// The ask verbs: the wire each one sends, the rows it prints, the JSON switch
// and the refusals that never reach the server.

import (
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"dm/internal/ask"
	"dm/internal/testutil"
	"dm/internal/wire"
)

// askOpenInput is the body a bridge pipes into dm ask open.
const askOpenInput = `{"questions":[{"question":"Which base?","header":"base","options":[{"label":"main"},{"label":"next"}]}]}`

// askGoodInput is the body a bridge pipes into dm ask answer.
const askGoodInput = `{"items":[{"questionIndex":0,"optionIndexes":[1]}],"globalNote":"ship it"}`

// withStdin runs fn with stdin reading body. Stays serial: stdin is a process
// global.
func withStdin(t *testing.T, body string, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(body); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	old := os.Stdin
	os.Stdin = r
	defer func() {
		os.Stdin = old
		_ = r.Close()
	}()
	fn()
}

// askReply answers one ask action with body, after recording the request.
func askReply(rec *recorder, body string) fakeHandler {
	return func(_ net.Conn, req wire.Msg) (wire.Msg, bool) {
		rec.mu.Lock()
		rec.got = append(rec.got, req)
		rec.mu.Unlock()
		return wire.Msg{OK: true, Res: json.RawMessage(body)}, true
	}
}

// TestAskOpenSendsTheFlagsAndTheBody proves open puts the pane in target, the
// tool call and the piped questions in res, and prints the id.
func TestAskOpenSendsTheFlagsAndTheBody(t *testing.T) {
	rec := &recorder{}
	startFakeServer(t, "main", askReply(rec, `{"id":"a1"}`))
	out := testutil.CaptureStdout(t, func() {
		withStdin(t, askOpenInput, func() {
			if err := Run([]string{"ask", "open", "--pane", "w1p1", "--tool-call", "toolu_1"}); err != nil {
				t.Fatalf("dm ask open: %v", err)
			}
		})
	})
	if out != "a1\n" {
		t.Fatalf("dm ask open printed %q, want the id", out)
	}
	sent := rec.last()
	if sent.Action != "ask-open" || sent.Target != "w1p1" {
		t.Fatalf("sent %+v", sent)
	}
	var req ask.OpenReq
	if err := json.Unmarshal(sent.Res, &req); err != nil {
		t.Fatalf("res: %v", err)
	}
	if req.ToolCallID != "toolu_1" {
		t.Errorf("toolCallId = %q", req.ToolCallID)
	}
	if len(req.Questions) != 1 || req.Questions[0].Options[1].Label != "next" {
		t.Errorf("questions = %+v", req.Questions)
	}
}

// TestAskOpenRefusesBadInput proves the verb refuses a missing flag, a bad tool
// call id and a malformed body with a non-zero exit, and never dials.
func TestAskOpenRefusesBadInput(t *testing.T) {
	cases := []struct {
		name string
		args []string
		body string
		want string
	}{
		{"no pane", []string{"ask", "open", "--tool-call", "toolu_1"}, askOpenInput, "--pane"},
		{"no tool call", []string{"ask", "open", "--pane", "w1p1"}, askOpenInput, "--tool-call"},
		{"bad tool call", []string{"ask", "open", "--pane", "w1p1", "--tool-call", "toolu 1"}, askOpenInput, "toolCallId"},
		{"bad body", []string{"ask", "open", "--pane", "w1p1", "--tool-call", "toolu_1"}, `{"questions":[]}`, "no questions"},
		{"not json", []string{"ask", "open", "--pane", "w1p1", "--tool-call", "toolu_1"}, `questions`, "not an object"},
		{"empty", []string{"ask", "open", "--pane", "w1p1", "--tool-call", "toolu_1"}, "  \n", "no body on stdin"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withStdin(t, c.body, func() {
				err := Run(c.args)
				if err == nil || !strings.Contains(err.Error(), c.want) {
					t.Fatalf("err = %v, want %q", err, c.want)
				}
			})
		})
	}
}

// TestAskListPrintsRows proves one row per ask, with the outcome as a word, and
// that --json passes the reply through untouched.
func TestAskListPrintsRows(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	rep := ask.ListReply{Asks: []ask.Request{
		{ID: "a1", Pane: "w1p1", ToolCallID: "toolu_1", At: at,
			Questions: []ask.Question{{Question: "Which base?", Header: "base"}}},
		{ID: "a2", Pane: "w1p2", ToolCallID: "toolu_2", At: at,
			Questions: []ask.Question{{Question: "Anything else?", Header: "notes"}},
			Answer:    &ask.Answer{At: at, Items: []ask.Item{{QuestionIndex: 0, Text: "no"}}}},
	}}
	raw, _ := json.Marshal(rep)
	startFakeServer(t, "main", okHandler(string(raw)))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"ask", "list"}); err != nil {
			t.Fatalf("dm ask list: %v", err)
		}
	})
	for _, want := range []string{"a1", "w1p1", "open", "toolu_1", "Which base?", "a2", "answered"} {
		if !strings.Contains(out, want) {
			t.Errorf("dm ask list printed %q, want %q", out, want)
		}
	}
	if strings.Contains(out, "{") {
		t.Errorf("dm ask list printed JSON without --json: %q", out)
	}

	out = testutil.CaptureStdout(t, func() {
		if err := Run([]string{"ask", "list", "--json"}); err != nil {
			t.Fatalf("dm ask list --json: %v", err)
		}
	})
	if !strings.Contains(out, `"toolCallId":"toolu_1"`) {
		t.Fatalf("dm ask list --json printed %q", out)
	}
}

// TestAskAwaitPrintsPending proves a pending reply prints one word and exits
// zero, and that the request carries the id and the timeout.
func TestAskAwaitPrintsPending(t *testing.T) {
	rec := &recorder{}
	startFakeServer(t, "main", askReply(rec, `{"pending":true}`))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"ask", "await", "a1", "--timeout", "2s"}); err != nil {
			t.Fatalf("dm ask await: %v", err)
		}
	})
	if out != "pending\n" {
		t.Fatalf("dm ask await printed %q", out)
	}
	sent := rec.last()
	if sent.Action != "ask-await" || sent.Target != "a1" {
		t.Fatalf("sent %+v", sent)
	}
	var req ask.AwaitReq
	if err := json.Unmarshal(sent.Res, &req); err != nil {
		t.Fatalf("res: %v", err)
	}
	if req.TimeoutMs != 2000 {
		t.Errorf("timeoutMs = %d, want 2000", req.TimeoutMs)
	}
}

// TestAskAwaitPrintsTheAnswer proves a settled await prints the answer's rows
// and its global note, and nothing about pending.
func TestAskAwaitPrintsTheAnswer(t *testing.T) {
	answer := ask.Answer{
		At:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Items: []ask.Item{{QuestionIndex: 0, OptionIndexes: []int{1}, Notes: "quick"}},
		Note:  "ship it",
	}
	raw, _ := json.Marshal(ask.AwaitReply{Answer: &answer})
	startFakeServer(t, "main", okHandler(string(raw)))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"ask", "await", "a1"}); err != nil {
			t.Fatalf("dm ask await: %v", err)
		}
	})
	if out != "0 1 quick\nship it\n" {
		t.Fatalf("dm ask await printed %q", out)
	}
}

// TestAskAwaitPrintsCancelled proves a cancelled answer prints one word.
func TestAskAwaitPrintsCancelled(t *testing.T) {
	raw, _ := json.Marshal(ask.AwaitReply{Answer: &ask.Answer{Cancel: true}})
	startFakeServer(t, "main", okHandler(string(raw)))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"ask", "await", "a1"}); err != nil {
			t.Fatalf("dm ask await: %v", err)
		}
	})
	if out != "cancelled\n" {
		t.Fatalf("dm ask await printed %q", out)
	}
}

// TestAskAwaitRefusalAndBadID proves a server refusal surfaces, a bad id is
// refused before the dial, and a missing id is named.
func TestAskAwaitRefusalAndBadID(t *testing.T) {
	if err := Run([]string{"ask", "await", "a 1"}); err == nil || !strings.Contains(err.Error(), "id") {
		t.Fatalf("bad id = %v", err)
	}
	if err := Run([]string{"ask", "await"}); err == nil || !strings.Contains(err.Error(), "needs an id") {
		t.Fatalf("missing id = %v", err)
	}
	if err := Run([]string{"ask", "await", "a1", "extra"}); err == nil || !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("extra argument = %v", err)
	}
	startFakeServer(t, "main", refuseHandler("ask: not_found: no ask \"a9\""))
	if err := Run([]string{"ask", "await", "a9"}); err == nil || !strings.Contains(err.Error(), "not_found") {
		t.Fatalf("refusal = %v", err)
	}
}

// TestAskAnswerSendsTheBody proves answer sends the piped answer under the id
// in target, and prints the stored answer.
func TestAskAnswerSendsTheBody(t *testing.T) {
	rec := &recorder{}
	stored, _ := json.Marshal(ask.Answer{
		At:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Items: []ask.Item{{QuestionIndex: 0, OptionIndexes: []int{1}}},
		Note:  "ship it",
	})
	startFakeServer(t, "main", askReply(rec, string(stored)))
	out := testutil.CaptureStdout(t, func() {
		withStdin(t, askGoodInput, func() {
			if err := Run([]string{"ask", "answer", "a1"}); err != nil {
				t.Fatalf("dm ask answer: %v", err)
			}
		})
	})
	if out != "0 1\nship it\n" {
		t.Fatalf("dm ask answer printed %q", out)
	}
	sent := rec.last()
	if sent.Action != "ask-answer" || sent.Target != "a1" {
		t.Fatalf("sent %+v", sent)
	}
	var req ask.AnswerReq
	if err := json.Unmarshal(sent.Res, &req); err != nil {
		t.Fatalf("res: %v", err)
	}
	if len(req.Items) != 1 || req.Note != "ship it" {
		t.Errorf("answer = %+v", req)
	}
}

// TestAskAnswerRefusesBadInput proves an empty body, a malformed body and an
// unknown field all exit non-zero before the request goes out.
func TestAskAnswerRefusesBadInput(t *testing.T) {
	cases := map[string]struct {
		body string
		want string
	}{
		"empty":    {"", "no body on stdin"},
		"array":    {`[]`, "not an object"},
		"unknown":  {`{"items":[],"selection":1}`, "unknown field"},
		"trailing": {`{} {}`, "trailing data"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			withStdin(t, c.body, func() {
				err := Run([]string{"ask", "answer", "a1"})
				if err == nil || !strings.Contains(err.Error(), c.want) {
					t.Fatalf("err = %v, want %q", err, c.want)
				}
			})
		})
	}
}

// TestAskClosePrintsTheReplyOnJSON proves close is idempotent: a reply that
// removed nothing is still a success, and --json prints it.
func TestAskClosePrintsTheReplyOnJSON(t *testing.T) {
	startFakeServer(t, "main", okHandler(`{"closed":false}`))
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"ask", "close", "a9", "--json"}); err != nil {
			t.Fatalf("dm ask close: %v", err)
		}
	})
	if !strings.Contains(out, `"closed":false`) {
		t.Fatalf("dm ask close --json printed %q", out)
	}
	out = testutil.CaptureStdout(t, func() {
		if err := Run([]string{"ask", "close", "a9"}); err != nil {
			t.Fatalf("dm ask close: %v", err)
		}
	})
	if out != "" {
		t.Fatalf("dm ask close printed %q without --json", out)
	}
}

// TestAskUnknownVerbAndBareAsk proves the group names its verbs and refuses one
// it does not carry.
func TestAskUnknownVerbAndBareAsk(t *testing.T) {
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"ask"}); err != nil {
			t.Fatalf("dm ask: %v", err)
		}
	})
	for _, want := range []string{"dm ask list", "dm ask open", "dm ask await", "dm ask answer", "dm ask close"} {
		if !strings.Contains(out, want) {
			t.Errorf("dm ask does not name %q", want)
		}
	}
	if err := Run([]string{"ask", "poll"}); err == nil || !strings.Contains(err.Error(), "unknown verb") {
		t.Fatalf("unknown verb = %v", err)
	}
}
