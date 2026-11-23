package ask

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// openBody is a well-formed open body with two questions, for the cases that
// only change one part of it.
const openBody = `{"toolCallId":"toolu_1","questions":[
	{"question":"Which base?","header":"base","options":[{"label":"main"},{"label":"next"}]},
	{"question":"Anything else?","header":"notes","multiSelect":true,
	 "options":[{"label":"tests","description":"run the suite"},{"label":"docs","preview":"a diff"}]}
]}`

// answerBody is a well-formed answer to openBody.
const answerBody = `{"items":[
	{"questionIndex":0,"optionIndexes":[1]},
	{"questionIndex":1,"optionIndexes":[0,1],"text":"and a changelog","notes":"quick"}
],"globalNote":"ship it"}`

func mustOpen(t *testing.T) *OpenReq {
	t.Helper()
	req, err := ParseOpen(json.RawMessage(openBody))
	if err != nil {
		t.Fatalf("ParseOpen: %v", err)
	}
	return req
}

func mustAnswer(t *testing.T) *AnswerReq {
	t.Helper()
	req, err := ParseAnswer(json.RawMessage(answerBody))
	if err != nil {
		t.Fatalf("ParseAnswer: %v", err)
	}
	return req
}

// TestRefusalCarriesTheCode proves the code is machine-readable: it sits first
// after the package prefix and CodeOf reads it back.
func TestRefusalCarriesTheCode(t *testing.T) {
	err := Refuse(CodeInvalidAnswer, "question 0 wants one option or one custom text")
	if got := err.Error(); got != "ask: invalid_answer: question 0 wants one option or one custom text" {
		t.Errorf("Error() = %q", got)
	}
	if !CodeIs(err, CodeInvalidAnswer) {
		t.Error("CodeIs does not read the code")
	}
	if !strings.HasPrefix(err.Error(), "ask: "+CodeInvalidAnswer+": ") {
		t.Error("the code does not follow the ask prefix")
	}
}

// TestCodeOfIsEmptyForAPlainError proves a non-refusal carries no code, so a
// caller can tell a refusal from an I/O failure.
func TestCodeOfIsEmptyForAPlainError(t *testing.T) {
	if got := CodeOf(jsonError()); got != "" {
		t.Errorf("CodeOf(plain) = %q, want empty", got)
	}
}

func jsonError() error { return &json.SyntaxError{Offset: 1} }

// TestParseOpenRejectsBadBodies proves the decoder is strict: a non-object, an
// unknown field, trailing data and a blank question all fail with
// invalid_payload.
func TestParseOpenRejectsBadBodies(t *testing.T) {
	cases := map[string]string{
		"array":         `[]`,
		"unknown field": `{"toolCallId":"t","questions":[{"question":"q","header":"h","options":[]}],"extra":1}`,
		"trailing":      `{"toolCallId":"t","questions":[{"question":"q","header":"h","options":[]}]} {}`,
		"no tool call":  `{"questions":[{"question":"q","header":"h","options":[]}]}`,
		"no questions":  `{"toolCallId":"t","questions":[]}`,
		"blank":         `{"toolCallId":"t","questions":[{"question":"   ","header":"h","options":[]}]}`,
		"blank option":  `{"toolCallId":"t","questions":[{"question":"q","header":"h","options":[{"label":" "}]}]}`,
	}
	for name, body := range cases {
		if _, err := ParseOpen(json.RawMessage(body)); !CodeIs(err, CodeInvalidPayload) {
			t.Errorf("%s: err = %v, want %s", name, err, CodeInvalidPayload)
		}
	}
}

// TestParseOpenAcceptsAFreeformQuestion proves a question with no options is
// legal: the reader answers it with text alone.
func TestParseOpenAcceptsAFreeformQuestion(t *testing.T) {
	body := `{"toolCallId":"toolu_2","questions":[{"question":"Name it","header":"name","options":[]}]}`
	req, err := ParseOpen(json.RawMessage(body))
	if err != nil {
		t.Fatalf("ParseOpen: %v", err)
	}
	if len(req.Questions) != 1 || len(req.Questions[0].Options) != 0 {
		t.Fatalf("questions = %+v", req.Questions)
	}
}

// TestValidateAnswerAcceptsTheWellFormedBody proves a single select of one
// option, a multi select of two options plus text, and a global note all pass.
func TestValidateAnswerAcceptsTheWellFormedBody(t *testing.T) {
	open := mustOpen(t)
	if err := ValidateAnswer(open.Questions, mustAnswer(t)); err != nil {
		t.Fatalf("ValidateAnswer: %v", err)
	}
}

// TestValidateAnswerAcceptsCancel proves a cancel carries no items and is a
// valid answer.
func TestValidateAnswerAcceptsCancel(t *testing.T) {
	if err := ValidateAnswer(mustOpen(t).Questions, &AnswerReq{Cancel: true}); err != nil {
		t.Fatalf("ValidateAnswer(cancel): %v", err)
	}
	if err := ValidateAnswer(mustOpen(t).Questions, &AnswerReq{Cancel: true, Items: []Item{{QuestionIndex: 0}}}); !CodeIs(err, CodeInvalidAnswer) {
		t.Fatalf("cancel with items: err = %v, want %s", err, CodeInvalidAnswer)
	}
}

// TestValidateAnswerRefusesBadAnswers walks the all-or-nothing rule: every
// question answered once, in range and distinct, one option or text on a
// single-select question, and no blank text.
func TestValidateAnswerRefusesBadAnswers(t *testing.T) {
	qs := mustOpen(t).Questions
	cases := map[string]*AnswerReq{
		"missing item": {Items: []Item{{QuestionIndex: 0, OptionIndexes: []int{0}}}},
		"extra item": {Items: []Item{
			{QuestionIndex: 0, OptionIndexes: []int{0}},
			{QuestionIndex: 1, OptionIndexes: []int{0}},
			{QuestionIndex: 1, OptionIndexes: []int{1}},
		}},
		"twice": {Items: []Item{
			{QuestionIndex: 0, OptionIndexes: []int{0}},
			{QuestionIndex: 0, OptionIndexes: []int{1}},
		}},
		"index negative": {Items: []Item{
			{QuestionIndex: -1, OptionIndexes: []int{0}},
			{QuestionIndex: 1, OptionIndexes: []int{0}},
		}},
		"index past end": {Items: []Item{
			{QuestionIndex: 0, OptionIndexes: []int{0}},
			{QuestionIndex: 2, OptionIndexes: []int{0}},
		}},
		"option out of range": {Items: []Item{
			{QuestionIndex: 0, OptionIndexes: []int{2}},
			{QuestionIndex: 1, OptionIndexes: []int{0}},
		}},
		"option negative": {Items: []Item{
			{QuestionIndex: 0, OptionIndexes: []int{-1}},
			{QuestionIndex: 1, OptionIndexes: []int{0}},
		}},
		"option twice": {Items: []Item{
			{QuestionIndex: 0, OptionIndexes: []int{0}},
			{QuestionIndex: 1, OptionIndexes: []int{0, 0}},
		}},
		"single takes two options": {Items: []Item{
			{QuestionIndex: 0, OptionIndexes: []int{0, 1}},
			{QuestionIndex: 1, OptionIndexes: []int{0}},
		}},
		"single takes none": {Items: []Item{
			{QuestionIndex: 0},
			{QuestionIndex: 1, OptionIndexes: []int{0}},
		}},
		"single takes option and text": {Items: []Item{
			{QuestionIndex: 0, OptionIndexes: []int{0}, Text: "both"},
			{QuestionIndex: 1, OptionIndexes: []int{0}},
		}},
		"blank text": {Items: []Item{
			{QuestionIndex: 0, Text: "  "},
			{QuestionIndex: 1, OptionIndexes: []int{0}},
		}},
		"multi takes none": {Items: []Item{
			{QuestionIndex: 0, OptionIndexes: []int{0}},
			{QuestionIndex: 1},
		}},
	}
	for name, req := range cases {
		if err := ValidateAnswer(qs, req); !CodeIs(err, CodeInvalidAnswer) {
			t.Errorf("%s: err = %v, want %s", name, err, CodeInvalidAnswer)
		}
	}
}

// TestValidateAnswerAcceptsFreeformText proves a single-select question with no
// options is answered with text alone.
func TestValidateAnswerAcceptsFreeformText(t *testing.T) {
	qs := []Question{{Question: "Name it", Header: "name"}}
	req := &AnswerReq{Items: []Item{{QuestionIndex: 0, Text: "bison"}}}
	if err := ValidateAnswer(qs, req); err != nil {
		t.Fatalf("ValidateAnswer: %v", err)
	}
}

// TestParseAnswerRejectsBadBodies proves the answer decoder is strict too.
func TestParseAnswerRejectsBadBodies(t *testing.T) {
	cases := map[string]string{
		"array":          `[]`,
		"unknown field":  `{"items":[],"extra":true}`,
		"trailing":       `{} {}`,
		"bad item field": `{"items":[{"questionIndex":0,"selection":1}]}`,
	}
	for name, body := range cases {
		if _, err := ParseAnswer(json.RawMessage(body)); !CodeIs(err, CodeInvalidPayload) {
			t.Errorf("%s: err = %v, want %s", name, err, CodeInvalidPayload)
		}
	}
}

// TestAwaitWaitClampsToDefault proves a request can shorten the wait but never
// park past DefaultAwait, and that no body waits the default.
func TestAwaitWaitClampsToDefault(t *testing.T) {
	cases := []struct {
		ms   int
		want time.Duration
	}{
		{0, DefaultAwait},
		{-1, DefaultAwait},
		{1000, time.Second},
		{int(DefaultAwait/time.Millisecond) + 1, DefaultAwait},
		{3600000, DefaultAwait},
	}
	for _, c := range cases {
		req := &AwaitReq{TimeoutMs: c.ms}
		if got := req.Wait(); got != c.want {
			t.Errorf("Wait(%d) = %v, want %v", c.ms, got, c.want)
		}
	}
	var nilReq *AwaitReq
	if got := nilReq.Wait(); got != DefaultAwait {
		t.Errorf("Wait(nil) = %v, want %v", got, DefaultAwait)
	}
	if _, err := ParseAwait(json.RawMessage(`{"timeoutMs":-5}`)); !CodeIs(err, CodeInvalidPayload) {
		t.Errorf("negative timeoutMs: err = %v, want %s", err, CodeInvalidPayload)
	}
}

// TestRequestFieldNames proves the published JSON names, because a downstream
// client and the pi extension are coded against them.
func TestRequestFieldNames(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	req := Request{
		ID: "a1", Pane: "w1p1", ToolCallID: "toolu_1", At: at,
		Questions: []Question{{
			Question: "Which base?", Header: "base", MultiSelect: true,
			Options: []Option{{Label: "main", Description: "the trunk", Preview: "a diff"}},
		}},
		Answer: &Answer{
			At: at, Cancel: true, Note: "later",
			Items: []Item{{QuestionIndex: 0, OptionIndexes: []int{0}, Text: "t", Notes: "n"}},
		},
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "pane", "toolCallId", "at", "questions", "answer"} {
		if _, ok := got[key]; !ok {
			t.Errorf("Request has no %q", key)
		}
	}
	q := got["questions"].([]any)[0].(map[string]any)
	for _, key := range []string{"question", "header", "multiSelect", "options"} {
		if _, ok := q[key]; !ok {
			t.Errorf("Question has no %q", key)
		}
	}
	o := q["options"].([]any)[0].(map[string]any)
	for _, key := range []string{"label", "description", "preview"} {
		if _, ok := o[key]; !ok {
			t.Errorf("Option has no %q", key)
		}
	}
	a := got["answer"].(map[string]any)
	for _, key := range []string{"at", "cancel", "items", "globalNote"} {
		if _, ok := a[key]; !ok {
			t.Errorf("Answer has no %q", key)
		}
	}
	it := a["items"].([]any)[0].(map[string]any)
	for _, key := range []string{"questionIndex", "optionIndexes", "text", "notes"} {
		if _, ok := it[key]; !ok {
			t.Errorf("Item has no %q", key)
		}
	}
}

// TestAnswerFieldNamesAreOptional proves an open ask carries no answer key and
// a cancel carries no items key, so a reader can tell absent from empty.
func TestAnswerFieldNamesAreOptional(t *testing.T) {
	b, err := json.Marshal(Request{ID: "a1", Pane: "w1p1", ToolCallID: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"answer"`) {
		t.Errorf("an open ask carries answer: %s", b)
	}
	b, err = json.Marshal(Answer{Cancel: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"items"`) || strings.Contains(string(b), `"globalNote"`) {
		t.Errorf("a bare cancel carries optional fields: %s", b)
	}
}

// TestCapsRefuse proves the question and option caps are enforced, so one ask
// cannot grow the state file without limit.
func TestCapsRefuse(t *testing.T) {
	many := make([]Question, MaxQuestions+1)
	for i := range many {
		many[i] = Question{Question: "q", Header: "h"}
	}
	if err := ValidateQuestions(many); !CodeIs(err, CodeInvalidPayload) {
		t.Errorf("too many questions: err = %v", err)
	}
	opts := make([]Option, MaxOptions+1)
	for i := range opts {
		opts[i] = Option{Label: "o"}
	}
	if err := ValidateQuestions([]Question{{Question: "q", Header: "h", Options: opts}}); !CodeIs(err, CodeInvalidPayload) {
		t.Errorf("too many options: err = %v", err)
	}
}

// TestCheckID proves the id shape refuses an empty id, a space and a control
// character.
func TestCheckID(t *testing.T) {
	if err := CheckID("a1"); err != nil {
		t.Errorf("CheckID(a1) = %v", err)
	}
	for _, id := range []string{"", "a 1", "a\n1", strings.Repeat("a", MaxID+1)} {
		if err := CheckID(id); !CodeIs(err, CodeInvalidPayload) {
			t.Errorf("CheckID(%q) = %v, want %s", id, err, CodeInvalidPayload)
		}
	}
}
