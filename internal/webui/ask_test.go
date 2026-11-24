package webui

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"dm/internal/ask"
	"dm/internal/box"
	"dm/internal/paths"
	"dm/internal/wire"
)

// askAt is the moment the tests say an ask was put to the reader.
var askAt = time.Date(2026, 5, 4, 9, 30, 0, 0, time.UTC)

// openAsk is one ask with two questions: a single select with a description and
// a preview, and a multi select.
func openAsk() ask.Request {
	return ask.Request{
		ID:         "a1",
		Pane:       "w1p1",
		ToolCallID: "toolu_1",
		At:         askAt,
		Questions: []ask.Question{
			{
				Question: "Which base should the branch take?",
				Header:   "base",
				Options: []ask.Option{
					{Label: "main", Description: "the trunk", Preview: "a1b2c3d head -> main"},
					{Label: "poc/web", Description: "the last release"},
				},
			},
			{
				Question:    "What should the change touch?",
				Header:      "scope",
				MultiSelect: true,
				Options: []ask.Option{
					{Label: "server"}, {Label: "client"},
				},
			},
		},
	}
}

// wireAsk flattens one stored ask the way the daemon's snapshot does.
func wireAsk(r ask.Request) wire.Ask {
	out := wire.Ask{ID: r.ID, Pane: r.Pane, ToolCallID: r.ToolCallID, At: r.At}
	for _, q := range r.Questions {
		wq := wire.AskQuestion{Question: q.Question, Header: q.Header, MultiSelect: q.MultiSelect}
		for _, o := range q.Options {
			wq.Options = append(wq.Options, wire.AskOption{Label: o.Label, Description: o.Description, Preview: o.Preview})
		}
		out.Questions = append(out.Questions, wq)
	}
	return out
}

// askSession serves one session that holds one ask and answers ask-answer the
// way the daemon does: the ask package's own decoder and rules. Every request
// is logged, so a test can read what reached the socket.
func askSession(t *testing.T, dir, session string, open ask.Request) *sessionLog {
	t.Helper()
	log := &sessionLog{}
	listenUnix(t, paths.ServerSockAt(dir, session), func(c net.Conn) {
		for {
			m, err := wire.ReadMsg(c)
			if err != nil {
				return
			}
			log.add(m)
			out := wire.Msg{Type: "resp", Action: m.Action, ID: m.ID, OK: true}
			switch m.Action {
			case "list":
				out.Res, _ = json.Marshal(map[string]any{"snapshot": wire.Snapshot{
					Session: session,
					Panes:   []wire.PaneInfo{{ID: open.Pane}},
					All:     []wire.AllPane{{ID: open.Pane, Status: "blocked"}, {ID: "w1p2", Status: "working"}},
					Ask:     []wire.Ask{wireAsk(open)},
				}})
			case "msg-list":
				out.Res, _ = json.Marshal(box.ListReply{Entries: []box.Entry{}})
			case "ask-answer":
				req, err := ask.ParseAnswer(m.Res)
				if err == nil {
					err = ask.ValidateAnswer(open.Questions, req)
				}
				if err != nil {
					out.OK, out.Error = false, err.Error()
					break
				}
				out.Res, _ = json.Marshal(ask.Answer{At: askAt, Cancel: req.Cancel, Items: req.Items, Note: req.Note})
			default:
				out.OK, out.Error = false, "test: no action "+m.Action
			}
			if err := wire.WriteMsg(c, out); err != nil {
				return
			}
		}
	})
	return log
}

// all returns every request the session received, so a test can prove what did
// not reach the socket.
func (l *sessionLog) all() []wire.Msg {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]wire.Msg{}, l.got...)
}

// TestFleetCarriesTheAsks proves the fleet payload carries the questions a pane
// put to the reader, the pane row says it is waiting, and no answer rides with
// them.
func TestFleetCarriesTheAsks(t *testing.T) {
	dir := root(t, "main")
	open := openAsk()
	askSession(t, dir, "main", open)
	srv := httptest.NewServer(handler(Config{Session: "main", Root: dir, Token: "t", ReadOnly: true}))
	defer srv.Close()

	res := get(t, srv.URL, "/api/fleet?t=t")
	raw := body(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	// The snapshot publishes no answer, and the page's payload adds none: a
	// reply to one ask can never appear beside another pane's row.
	if strings.Contains(raw, `"answer"`) {
		t.Fatalf("the fleet carries an answer field:\n%s", raw)
	}
	var got fleetReply
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got.Error != "" {
		t.Fatalf("error %q", got.Error)
	}
	if len(got.Ask) != 1 {
		t.Fatalf("%d asks, want 1", len(got.Ask))
	}
	a := got.Ask[0]
	if a.ID != open.ID || a.Pane != open.Pane || !a.At.Equal(askAt) {
		t.Fatalf("ask %+v", a)
	}
	if len(a.Questions) != 2 {
		t.Fatalf("%d questions, want 2", len(a.Questions))
	}
	if q := a.Questions[0]; q.Question != open.Questions[0].Question || q.Header != "base" || q.MultiSelect {
		t.Fatalf("first question %+v", q)
	}
	if len(a.Questions[0].Options) != 2 {
		t.Fatalf("first question options %+v", a.Questions[0].Options)
	}
	if o := a.Questions[0].Options[0]; o.Label != "main" || o.Description != "the trunk" || o.Preview != "a1b2c3d head -> main" {
		t.Fatalf("first option %+v", o)
	}
	if !a.Questions[1].MultiSelect {
		t.Fatalf("multiSelect dropped: %+v", a.Questions[1])
	}

	// The row waiting on an answer says so, and the pane with no ask does not.
	if len(got.Panes) != 2 {
		t.Fatalf("%d panes, want 2", len(got.Panes))
	}
	for _, p := range got.Panes {
		want := p.ID == open.Pane
		if p.Ask != want {
			t.Fatalf("pane %s ask=%v, want %v", p.ID, p.Ask, want)
		}
	}
}

// TestAskAnswerReachesTheDaemon proves a form's answer posted through the action
// endpoint reaches the daemon as the store's own answer body, and the daemon's
// reply is what the page reads back.
func TestAskAnswerReachesTheDaemon(t *testing.T) {
	dir := root(t, "main")
	askSession(t, dir, "main", openAsk())
	srv := httptest.NewServer(handler(Config{Session: "main", Root: dir, Token: "t"}))
	defer srv.Close()

	res := post(t, srv.URL+"/api/action?t=t", map[string]any{
		"id":     "ask-answer",
		"target": "a1",
		"answer": map[string]any{"items": []map[string]any{
			{"questionIndex": 0, "optionIndexes": []int{1}},
			{"questionIndex": 1, "optionIndexes": []int{0, 1}, "notes": "both halves"},
		}},
	})
	raw := body(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	var got ask.Answer
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("the page's reply is not an answer: %v: %s", err, raw)
	}
	if got.Cancel || len(got.Items) != 2 {
		t.Fatalf("reply %+v", got)
	}
	if it := got.Items[0]; it.QuestionIndex != 0 || len(it.OptionIndexes) != 1 || it.OptionIndexes[0] != 1 {
		t.Fatalf("first item %+v", it)
	}
	if it := got.Items[1]; len(it.OptionIndexes) != 2 || it.Notes != "both halves" {
		t.Fatalf("second item %+v", it)
	}
}

// appScript is the page's script as the build carries it. The ask form is the
// page's own, so the rules it keeps stay in the page, and a test reads them
// where they live.
func appScript(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("read the page's script: %v", err)
	}
	return string(b)
}

// funcSource is one top level function's source, from its name to the closing
// brace at column zero.
func funcSource(t *testing.T, src, name string) string {
	t.Helper()
	at := strings.Index(src, "function "+name+"(")
	if at < 0 {
		t.Fatalf("no function %s in the page's script", name)
	}
	end := strings.Index(src[at:], "\n}")
	if end < 0 {
		t.Fatalf("function %s has no closing brace", name)
	}
	return src[at : at+end+2]
}

// TestAskAnswerCarriesOneQuestionsNote proves a note typed on one question
// rides that question's item and no other: the socket sees the note once, on
// the item the reader typed it under.
func TestAskAnswerCarriesOneQuestionsNote(t *testing.T) {
	dir := root(t, "main")
	log := askSession(t, dir, "main", openAsk())
	srv := httptest.NewServer(handler(Config{Session: "main", Root: dir, Token: "t"}))
	defer srv.Close()

	const note = "the last release, not the trunk"
	res := post(t, srv.URL+"/api/action?t=t", map[string]any{
		"id":     "ask-answer",
		"target": "a1",
		"answer": map[string]any{"items": []map[string]any{
			{"questionIndex": 0, "optionIndexes": []int{1}, "notes": note},
			{"questionIndex": 1, "optionIndexes": []int{0, 1}},
		}},
	})
	raw := body(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	var got ask.Answer
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("the page's reply is not an answer: %v: %s", err, raw)
	}
	if len(got.Items) != 2 {
		t.Fatalf("%d items, want 2", len(got.Items))
	}
	if it := got.Items[0]; it.Notes != note || len(it.OptionIndexes) != 1 {
		t.Fatalf("first item %+v", it)
	}
	if it := got.Items[1]; it.Notes != "" {
		t.Fatalf("second item grew a note: %+v", it)
	}

	// The body the socket saw carries one notes field, the first item's.
	var sent []wire.Msg
	for _, m := range log.all() {
		if m.Action == "ask-answer" {
			sent = append(sent, m)
		}
	}
	if len(sent) != 1 {
		t.Fatalf("%d answers reached the socket, want 1", len(sent))
	}
	if n := strings.Count(string(sent[0].Res), `"notes":`); n != 1 {
		t.Fatalf("the answer body carries %d notes fields, want 1: %s", n, sent[0].Res)
	}
	if !strings.Contains(string(sent[0].Res), `"notes":"`+note+`"`) {
		t.Fatalf("the note missing from the answer body: %s", sent[0].Res)
	}
}

// TestAskNoteIsTrimmedAndBlankIsOmitted proves the form sends the trimmed note
// and leaves a blank one off the item whole. The daemon checks a note's length
// and nothing else, so the page is where a blank note is stopped.
func TestAskNoteIsTrimmedAndBlankIsOmitted(t *testing.T) {
	body := funcSource(t, appScript(t), "entriesOf")
	if !strings.Contains(body, "e.note.trim()") {
		t.Fatalf("entriesOf does not trim the note:\n%s", body)
	}
	if !strings.Contains(body, "if (note) item.notes = note;") {
		t.Fatalf("entriesOf puts a blank note on the item:\n%s", body)
	}
	field := funcSource(t, appScript(t), "askNote")
	if !strings.Contains(field, `placeholder = "note for this question"`) {
		t.Fatalf("the note field has no placeholder of its own:\n%s", field)
	}
}

// TestAskNoteIsNotASendGate proves a note never answers a question: the gate
// reads the picks and the text only, and typing a note never refreshes the send
// control.
func TestAskNoteIsNotASendGate(t *testing.T) {
	src := appScript(t)
	for _, name := range []string{"askAnswered", "askComplete"} {
		if body := funcSource(t, src, name); strings.Contains(strings.ToLower(body), "note") {
			t.Fatalf("%s reads a note, so a note would answer a question:\n%s", name, body)
		}
	}
	field := funcSource(t, src, "askNote")
	if strings.Contains(field, "refreshAskSend") {
		t.Fatalf("typing a note touches the send control:\n%s", field)
	}
	// The daemon's own rule is the gate the page mirrors: an item that carries
	// a note and no pick or text does not answer its question.
	req := &ask.AnswerReq{Items: []ask.Item{
		{QuestionIndex: 0, Notes: "a note"},
		{QuestionIndex: 1, OptionIndexes: []int{0}},
	}}
	err := ask.ValidateAnswer(openAsk().Questions, req)
	if err == nil {
		t.Fatal("the daemon accepted a note as an answer")
	}
	if !strings.Contains(err.Error(), ask.CodeInvalidAnswer) {
		t.Fatalf("refusal %q", err)
	}
}

// TestAskNoteHidesInReadOnly proves a read-only page carries the question with
// no note field: the field is appended under the read-only guard, so a page
// that may not answer cannot take a note either. A settled form is guarded the
// same way, and keeps the note the reader typed as a line of its own.
func TestAskNoteHidesInReadOnly(t *testing.T) {
	q := funcSource(t, appScript(t), "askQuestion")
	if !strings.Contains(q, "if (!askState.ro && !askState.settled) block.append(askNote(i));") {
		t.Fatalf("the note field is not behind the read-only guard:\n%s", q)
	}
	if !strings.Contains(q, `else if (askState.settled && e.note.trim() !== "")`) {
		t.Fatalf("a settled form drops the note the reader typed:\n%s", q)
	}
}

// TestAskAnswerCarriesADecline proves the decline control sends the cancel the
// store expects, with no items.
func TestAskAnswerCarriesADecline(t *testing.T) {
	dir := root(t, "main")
	askSession(t, dir, "main", openAsk())
	srv := httptest.NewServer(handler(Config{Session: "main", Root: dir, Token: "t"}))
	defer srv.Close()

	res := post(t, srv.URL+"/api/action?t=t", map[string]any{
		"id": "ask-answer", "target": "a1",
		"answer": map[string]any{"cancel": true},
	})
	raw := body(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	var got ask.Answer
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Cancel || len(got.Items) != 0 {
		t.Fatalf("reply %+v", got)
	}
}

// TestAskAnswerRefusals proves the catalogue row checks its own fields before
// the socket sees them, and that the daemon's refusal keeps its code in the
// text the page paints.
func TestAskAnswerRefusals(t *testing.T) {
	dir := root(t, "main")
	log := askSession(t, dir, "main", openAsk())
	srv := httptest.NewServer(handler(Config{Session: "main", Root: dir, Token: "t"}))
	defer srv.Close()

	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"no answer", map[string]any{"id": "ask-answer", "target": "a1"},
			"ask-answer needs an answer"},
		{"answer is not an object", map[string]any{"id": "ask-answer", "target": "a1", "answer": "yes"},
			"not an object"},
		{"bad ask id", map[string]any{"id": "ask-answer", "target": "w1p1",
			"answer": map[string]any{"cancel": true}}, "bad ask id"},
		{"field outside the row", map[string]any{"id": "ask-answer", "name": "x"},
			"ask-answer takes no name"},
		{"the daemon's refusal", map[string]any{"id": "ask-answer", "target": "a1",
			"answer": map[string]any{"items": []map[string]any{
				{"questionIndex": 0, "optionIndexes": []int{9}},
			}}}, ask.CodeInvalidAnswer},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := post(t, srv.URL+"/api/action?t=t", tc.body)
			got := body(t, res)
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("status %d: %s", res.StatusCode, got)
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("body %q does not carry %q", got, tc.want)
			}
		})
	}
	// The one answer that reached the socket is the well formed body the daemon
	// itself refused; the four this server refused never left it.
	reached := 0
	for _, m := range log.all() {
		if m.Action != "ask-answer" {
			continue
		}
		reached++
		if !strings.Contains(string(m.Res), `"optionIndexes":[9]`) {
			t.Fatalf("an unvalidated answer reached the socket: %s", m.Res)
		}
	}
	if reached != 1 {
		t.Fatalf("%d answers reached the socket, want the one the daemon refused", reached)
	}
}

// TestAskReadOnlyRefusesTheAnswer proves a read-only page still lists the
// question and refuses the answer twice over: the catalogue is not served and
// the action endpoint declines.
func TestAskReadOnlyRefusesTheAnswer(t *testing.T) {
	dir := root(t, "main")
	log := askSession(t, dir, "main", openAsk())
	srv := httptest.NewServer(handler(Config{Session: "main", Root: dir, Token: "t", ReadOnly: true}))
	defer srv.Close()

	// Reading keeps working: the fleet carries the ask.
	res := get(t, srv.URL, "/api/fleet?t=t")
	raw := body(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	if !strings.Contains(raw, `"a1"`) {
		t.Fatalf("the read-only fleet dropped the ask: %s", raw)
	}

	res = get(t, srv.URL, "/api/commands?t=t")
	if got := body(t, res); res.StatusCode != http.StatusForbidden {
		t.Fatalf("GET /api/commands: status %d: %s", res.StatusCode, got)
	}
	res = post(t, srv.URL+"/api/action?t=t", map[string]any{
		"id": "ask-answer", "target": "a1", "answer": map[string]any{"cancel": true},
	})
	if got := body(t, res); res.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /api/action: status %d: %s", res.StatusCode, got)
	}
	for _, m := range log.all() {
		if m.Action == "ask-answer" {
			t.Fatalf("a read-only page answered an ask: %+v", m)
		}
	}
}

// TestCatalogueRowForAskAnswer proves the row the page builds its menu from:
// one ask-answer command, targeting an ask, carrying the answer body.
func TestCatalogueRowForAskAnswer(t *testing.T) {
	cmd, ok := commandByID("ask-answer")
	if !ok {
		t.Fatal("no ask-answer row in the catalogue")
	}
	if cmd.Target != targetAsk {
		t.Fatalf("target %q, want %q", cmd.Target, targetAsk)
	}
	if got := strings.Join(cmd.Fields, ","); got != "target,answer" {
		t.Fatalf("fields %q, want the target and the answer", got)
	}
	for _, id := range []string{"a1", "a12"} {
		if err := cmd.checkTarget(id); err != nil {
			t.Fatalf("id %q refused: %v", id, err)
		}
	}
	for _, id := range []string{"1", "a", "a1x", "w1p1", ""} {
		if id == "" {
			continue
		}
		if err := cmd.checkTarget(id); err == nil {
			t.Fatalf("id %q accepted", id)
		}
	}
}
