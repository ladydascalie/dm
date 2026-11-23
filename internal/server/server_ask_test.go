package server

// The ask store's daemon actions: the four that open, await, answer and close
// one ask, and the snapshot that carries the list.

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"dm/internal/ask"
	"dm/internal/session"
	"dm/internal/testutil"
	"dm/internal/wire"
)

// askServer is an in-process server over a temp state dir with one pane in its
// tree, so an open can name a pane that exists.
func askServer(t *testing.T) (*server, string) {
	t.Helper()
	st := session.NewStateAt(t.TempDir(), "askserver")
	pane := testutil.RootPane(st)
	return railServer(st), pane
}

// askOpenBody is a two-question open body for the tool call name.
func askOpenBody(toolCall string) string {
	return `{"toolCallId":"` + toolCall + `","questions":[
		{"question":"Which base?","header":"base","options":[{"label":"main"},{"label":"next"}]},
		{"question":"Anything else?","header":"notes","options":[{"label":"tests"},{"label":"docs"}]}
	]}`
}

// askGoodAnswer answers both questions of askOpenBody.
const askGoodAnswer = `{"items":[{"questionIndex":0,"optionIndexes":[1]},{"questionIndex":1,"optionIndexes":[0]}],"globalNote":"ship it"}`

// askCall sends one ask action and returns its reply.
func askCall(t *testing.T, s *server, cl *client, conn net.Conn, action, target, res string) wire.Msg {
	t.Helper()
	if cl == nil {
		cl, conn = boxConn(t)
	}
	s.handleReq(cl, wire.Msg{Type: "req", Action: action, Target: target, Res: json.RawMessage(res)})
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	m, err := wire.ReadMsg(conn)
	if err != nil {
		t.Fatalf("%s reply: %v", action, err)
	}
	return m
}

// askOpen opens one ask and returns its id, failing the test on any refusal.
func askOpen(t *testing.T, s *server, pane, toolCall string) string {
	t.Helper()
	m := askCall(t, s, nil, nil, "ask-open", pane, askOpenBody(toolCall))
	if !m.OK {
		t.Fatalf("ask-open: %s", m.Error)
	}
	var rep ask.OpenReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.Fatalf("ask-open reply: %v", err)
	}
	return rep.ID
}

// TestAskOpenListsAndSnapshots proves an open ask lands in the state file and
// in the snapshot, with the pane, the tool call and the questions.
func TestAskOpenListsAndSnapshots(t *testing.T) {
	t.Parallel()
	s, pane := askServer(t)
	id := askOpen(t, s, pane, "toolu_1")
	if id != "a1" {
		t.Errorf("first ask id = %q, want a1", id)
	}
	snap := snapshot(s)
	if len(snap.Ask) != 1 {
		t.Fatalf("snapshot carries %d asks, want 1", len(snap.Ask))
	}
	got := snap.Ask[0]
	if got.ID != id || got.Pane != pane || got.ToolCallID != "toolu_1" {
		t.Errorf("snapshot ask = %+v", got)
	}
	if len(got.Questions) != 2 || got.Questions[0].Question != "Which base?" ||
		len(got.Questions[0].Options) != 2 || got.Questions[0].Options[1].Label != "next" {
		t.Errorf("snapshot questions = %+v", got.Questions)
	}
	st, err := session.LoadStateAt(s.st.Dir(), s.st.Name)
	if err != nil {
		t.Fatalf("reload state: %v", err)
	}
	if len(st.Ask) != 1 || st.Ask[0].ID != id {
		t.Fatalf("state file carries %+v", st.Ask)
	}
	if st.AskSeq != 1 {
		t.Errorf("askSeq = %d, want 1", st.AskSeq)
	}
}

// TestAskOpenRefusesDuplicateToolCall proves an open ask replaces nothing: a
// second open for the same tool call is E_EXISTS, and the first ask stands.
func TestAskOpenRefusesDuplicateToolCall(t *testing.T) {
	t.Parallel()
	s, pane := askServer(t)
	first := askOpen(t, s, pane, "toolu_1")
	m := askCall(t, s, nil, nil, "ask-open", pane, askOpenBody("toolu_1"))
	if m.OK {
		t.Fatal("a duplicate tool call was accepted")
	}
	if code := askCode(m.Error); code != ask.CodeExists {
		t.Errorf("duplicate tool call: %q, want %s", m.Error, ask.CodeExists)
	}
	if list := s.askList(); len(list) != 1 || list[0].ID != first {
		t.Fatalf("store = %+v", list)
	}
}

// TestAskOpenRefusesUnknownPane proves the pane is checked against the session
// tree, so an ask cannot name a pane that is gone.
func TestAskOpenRefusesUnknownPane(t *testing.T) {
	t.Parallel()
	s, _ := askServer(t)
	m := askCall(t, s, nil, nil, "ask-open", "w9p9", askOpenBody("toolu_1"))
	if m.OK || askCode(m.Error) != ask.CodeInvalidPayload {
		t.Fatalf("unknown pane: ok=%v err=%q, want %s", m.OK, m.Error, ask.CodeInvalidPayload)
	}
}

// TestAskAnswerSettlesAndAwaitReturns proves an await parks until the answer
// lands, and returns the answer once it does.
func TestAskAnswerSettlesAndAwaitReturns(t *testing.T) {
	t.Parallel()
	s, pane := askServer(t)
	id := askOpen(t, s, pane, "toolu_1")

	type reply struct{ m wire.Msg }
	got := make(chan reply, 1)
	cl, conn := boxConn(t)
	go func() {
		got <- reply{askCall(t, s, cl, conn, "ask-await", id, `{"timeoutMs":5000}`)}
	}()
	// Give the await a moment to park before the answer lands.
	waitFor(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.askWaiters) == 1
	})

	ans := askCall(t, s, nil, nil, "ask-answer", id, askGoodAnswer)
	if !ans.OK {
		t.Fatalf("ask-answer: %s", ans.Error)
	}
	m := (<-got).m
	if !m.OK {
		t.Fatalf("ask-await: %s", m.Error)
	}
	var rep ask.AwaitReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.Fatalf("ask-await reply: %v", err)
	}
	if rep.Pending || rep.Answer == nil {
		t.Fatalf("ask-await reply = %+v", rep)
	}
	if rep.Answer.Cancel || len(rep.Answer.Items) != 2 || rep.Answer.Note != "ship it" {
		t.Errorf("answer = %+v", rep.Answer)
	}
}

// TestAskAwaitAnswersAnAnsweredAskAtOnce proves a settled ask needs no park.
func TestAskAwaitAnswersAnAnsweredAskAtOnce(t *testing.T) {
	t.Parallel()
	s, pane := askServer(t)
	id := askOpen(t, s, pane, "toolu_1")
	if m := askCall(t, s, nil, nil, "ask-answer", id, askGoodAnswer); !m.OK {
		t.Fatalf("ask-answer: %s", m.Error)
	}
	m := askCall(t, s, nil, nil, "ask-await", id, `{"timeoutMs":5000}`)
	if !m.OK {
		t.Fatalf("ask-await: %s", m.Error)
	}
	var rep ask.AwaitReply
	_ = json.Unmarshal(m.Res, &rep)
	if rep.Answer == nil || rep.Pending {
		t.Fatalf("ask-await reply = %+v", rep)
	}
}

// TestAskAwaitTimesOutPending proves a wait that nobody answers answers
// pending, not an error.
func TestAskAwaitTimesOutPending(t *testing.T) {
	t.Parallel()
	s, pane := askServer(t)
	id := askOpen(t, s, pane, "toolu_1")
	m := askCall(t, s, nil, nil, "ask-await", id, `{"timeoutMs":20}`)
	if !m.OK {
		t.Fatalf("ask-await: %s", m.Error)
	}
	var rep ask.AwaitReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.Fatalf("ask-await reply: %v", err)
	}
	if !rep.Pending || rep.Answer != nil {
		t.Fatalf("ask-await reply = %+v, want pending", rep)
	}
}

// TestAskAwaitRefusesAnUnknownID proves a missing ask is not_found.
func TestAskAwaitRefusesAnUnknownID(t *testing.T) {
	t.Parallel()
	s, _ := askServer(t)
	m := askCall(t, s, nil, nil, "ask-await", "a9", `{}`)
	if m.OK || askCode(m.Error) != ask.CodeNotFound {
		t.Fatalf("unknown id: ok=%v err=%q, want %s", m.OK, m.Error, ask.CodeNotFound)
	}
}

// TestAskAnswerRefusals proves the three refusals the bridge passes through: a
// missing ask is not_found, an answer that breaks the rules is invalid_answer
// and leaves the ask open, and a second answer is E_EXISTS.
func TestAskAnswerRefusals(t *testing.T) {
	t.Parallel()
	s, pane := askServer(t)
	id := askOpen(t, s, pane, "toolu_1")

	if m := askCall(t, s, nil, nil, "ask-answer", "a9", askGoodAnswer); askCode(m.Error) != ask.CodeNotFound {
		t.Errorf("unknown id: %q, want %s", m.Error, ask.CodeNotFound)
	}
	bad := `{"items":[{"questionIndex":0,"optionIndexes":[0]}]}`
	if m := askCall(t, s, nil, nil, "ask-answer", id, bad); askCode(m.Error) != ask.CodeInvalidAnswer {
		t.Errorf("short answer: %q, want %s", m.Error, ask.CodeInvalidAnswer)
	}
	s.mu.Lock()
	open := s.st.Ask[0].Answer == nil
	s.mu.Unlock()
	if !open {
		t.Fatal("a refused answer settled the ask")
	}
	if m := askCall(t, s, nil, nil, "ask-answer", id, askGoodAnswer); !m.OK {
		t.Fatalf("ask-answer: %s", m.Error)
	}
	if m := askCall(t, s, nil, nil, "ask-answer", id, askGoodAnswer); askCode(m.Error) != ask.CodeExists {
		t.Errorf("second answer: %q, want %s", m.Error, ask.CodeExists)
	}
}

// TestAskAnswerRefusesAMalformedBody proves a body the decoder rejects is
// invalid_payload, before the ask is even looked up.
func TestAskAnswerRefusesAMalformedBody(t *testing.T) {
	t.Parallel()
	s, pane := askServer(t)
	id := askOpen(t, s, pane, "toolu_1")
	for _, body := range []string{`[]`, `{"items":[],"extra":1}`, `{"items":[]} {}`} {
		if m := askCall(t, s, nil, nil, "ask-answer", id, body); askCode(m.Error) != ask.CodeInvalidPayload {
			t.Errorf("body %s: %q, want %s", body, m.Error, ask.CodeInvalidPayload)
		}
	}
}

// TestAskAnAnsweredAskStaysListedUntilClosed proves the outcome is readable
// after the answer and gone after the close, and that a close is idempotent.
func TestAskAnAnsweredAskStaysListedUntilClosed(t *testing.T) {
	t.Parallel()
	s, pane := askServer(t)
	id := askOpen(t, s, pane, "toolu_1")
	if m := askCall(t, s, nil, nil, "ask-answer", id, askGoodAnswer); !m.OK {
		t.Fatalf("ask-answer: %s", m.Error)
	}

	m := askCall(t, s, nil, nil, "ask-list", "", `{}`)
	if !m.OK {
		t.Fatalf("ask-list: %s", m.Error)
	}
	var rep ask.ListReply
	if err := json.Unmarshal(m.Res, &rep); err != nil {
		t.Fatalf("ask-list reply: %v", err)
	}
	if len(rep.Asks) != 1 || rep.Asks[0].Answer == nil {
		t.Fatalf("ask-list = %+v, want one answered ask", rep.Asks)
	}
	if snap := snapshot(s); len(snap.Ask) != 1 {
		t.Fatalf("snapshot carries %d asks, want the answered one", len(snap.Ask))
	}

	m = askCall(t, s, nil, nil, "ask-close", id, `{}`)
	if !m.OK {
		t.Fatalf("ask-close: %s", m.Error)
	}
	var closed ask.ClosedReply
	_ = json.Unmarshal(m.Res, &closed)
	if !closed.Closed {
		t.Error("ask-close did not report the removal")
	}
	m = askCall(t, s, nil, nil, "ask-close", id, `{}`)
	if !m.OK {
		t.Fatalf("second ask-close: %s", m.Error)
	}
	closed = ask.ClosedReply{}
	_ = json.Unmarshal(m.Res, &closed)
	if closed.Closed {
		t.Error("a second close removed something")
	}
	if list := s.askList(); len(list) != 0 {
		t.Fatalf("store = %+v, want empty", list)
	}
}

// TestAskAwaitStopsWhenTheAskCloses proves a close releases the waiter with
// not_found, so nothing parks on an ask the caller dropped.
func TestAskAwaitStopsWhenTheAskCloses(t *testing.T) {
	t.Parallel()
	s, pane := askServer(t)
	id := askOpen(t, s, pane, "toolu_1")
	got := make(chan wire.Msg, 1)
	cl, conn := boxConn(t)
	go func() { got <- askCall(t, s, cl, conn, "ask-await", id, `{"timeoutMs":5000}`) }()
	waitFor(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.askWaiters) == 1
	})
	askCall(t, s, nil, nil, "ask-close", id, `{}`)
	m := <-got
	if m.OK || askCode(m.Error) != ask.CodeNotFound {
		t.Fatalf("await after close: ok=%v err=%q, want %s", m.OK, m.Error, ask.CodeNotFound)
	}
}

// TestAskListIsEmptyForAFreshSession proves no asks reads as an empty list, not
// a null that a client must special-case.
func TestAskListIsEmptyForAFreshSession(t *testing.T) {
	t.Parallel()
	s, _ := askServer(t)
	m := askCall(t, s, nil, nil, "ask-list", "", `{}`)
	if !m.OK {
		t.Fatalf("ask-list: %s", m.Error)
	}
	if string(m.Res) != `{"asks":[]}` {
		t.Fatalf("ask-list reply = %s, want an empty array", m.Res)
	}
	var rep ask.ListReply
	if err := json.Unmarshal(m.Res, &rep); err != nil || len(rep.Asks) != 0 {
		t.Fatalf("ask-list = %+v (%v)", rep, err)
	}
}

// askCode reads the code out of a reply's error text, the way a bridge does:
// the code is the first field after the ask prefix.
func askCode(text string) string {
	rest, ok := strings.CutPrefix(text, "ask: ")
	if !ok {
		return ""
	}
	code, _, ok := strings.Cut(rest, ":")
	if !ok {
		return ""
	}
	return code
}

// waitFor polls until cond holds, so a test can wait on a waiter parking
// without a sleep it cannot bound.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition never held")
}
