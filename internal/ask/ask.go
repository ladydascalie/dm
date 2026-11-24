// Package ask holds the questions one session's agents put to the reader: the
// store's shapes, the rules an answer must meet and the error codes a caller
// passes through. A request lives in the session state file the way a box entry
// does, so a client that attaches mid-question sees the open ones.
//
// Nothing here paints. The renderers read these types and the daemon actions
// that drive them; the store itself has no keystroke path and no endpoint.
package ask

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Request is one ask: the questions an agent put to the reader on a pane, and
// the answer once the reader gives one. Answer is nil while the ask is open.
type Request struct {
	ID         string     `json:"id"`
	Pane       string     `json:"pane"`
	ToolCallID string     `json:"toolCallId"`
	At         time.Time  `json:"at"`
	Questions  []Question `json:"questions"`
	Answer     *Answer    `json:"answer,omitempty"`
}

// Question is one question of an ask: its text, its short header, whether the
// reader may pick several options, and the options on offer. An empty Options
// list is a freeform question, answered with custom text alone.
type Question struct {
	Question    string   `json:"question"`
	Header      string   `json:"header"`
	MultiSelect bool     `json:"multiSelect,omitempty"`
	Options     []Option `json:"options"`
}

// Option is one choice: a label the reader picks, and the description and
// preview the renderer may show beside it.
type Option struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Preview     string `json:"preview,omitempty"`
}

// Answer is the reader's reply to a whole ask. Cancel means the reader declined
// the ask and Items is empty; otherwise Items carries one item per question.
type Answer struct {
	At     time.Time `json:"at"`
	Cancel bool      `json:"cancel,omitempty"`
	Items  []Item    `json:"items,omitempty"`
	Note   string    `json:"globalNote,omitempty"`
}

// Item is one question's answer: the option indexes the reader picked, custom
// text, or both on a multi-select question, plus an optional note.
type Item struct {
	QuestionIndex int    `json:"questionIndex"`
	OptionIndexes []int  `json:"optionIndexes,omitempty"`
	Text          string `json:"text,omitempty"`
	Notes         string `json:"notes,omitempty"`
}

// OpenReq is the ask-open request body, the part the caller supplies: the tool
// call the questions belong to and the questions themselves. The server owns
// the id, the pane and the time.
type OpenReq struct {
	ToolCallID string     `json:"toolCallId"`
	Questions  []Question `json:"questions"`
}

// AnswerReq is the ask-answer request body. The server owns the time.
type AnswerReq struct {
	Cancel bool   `json:"cancel,omitempty"`
	Items  []Item `json:"items,omitempty"`
	Note   string `json:"globalNote,omitempty"`
}

// AwaitReq is the ask-await request body. A zero TimeoutMs waits DefaultAwait.
// A longer request is clamped to DefaultAwait, so a caller can shorten the wait
// but never park past the ask's own bound.
type AwaitReq struct {
	TimeoutMs int `json:"timeoutMs,omitempty"`
}

// OpenReply carries the id the server gave the ask.
type OpenReply struct {
	ID string `json:"id"`
}

// AwaitReply is the ask-await reply: pending when the wait passed and nobody
// answered, else the answer.
type AwaitReply struct {
	Pending bool    `json:"pending,omitempty"`
	Answer  *Answer `json:"answer,omitempty"`
}

// ListReply carries every listed ask, oldest first. An answered ask stays
// listed until it is closed, so Answer is how a reader tells the two apart.
type ListReply struct {
	Asks []Request `json:"asks"`
}

// ClosedReply carries whether this close removed an ask. A close that removed
// nothing is still a success: the verb is idempotent.
type ClosedReply struct {
	Closed bool `json:"closed"`
}

// The error codes. A pi bridge passes them through, so they are the contract
// and never change. The text of an Error puts the code first after the ask
// prefix, so a caller that parses the error string alone still reads it.
const (
	// CodeExists is a duplicate: an ask already covers this tool call, or the
	// ask is already answered.
	CodeExists = "E_EXISTS"
	// CodeNotFound is an id that names no listed ask.
	CodeNotFound = "not_found"
	// CodeInvalidPayload is a request body that does not parse or that breaks
	// the open rules.
	CodeInvalidPayload = "invalid_payload"
	// CodeInvalidAnswer is a well-formed answer that does not satisfy the
	// questions.
	CodeInvalidAnswer = "invalid_answer"
)

// SettleGrace is how long a settled ask stays in the store after its answer.
// The owner reads the answer through ask-await and closes the ask afterwards,
// so this is the room a slow owner gets before the store drops the entry on its
// own. An ask nobody has answered is never dropped: a reader may still take it.
const SettleGrace = 10 * time.Minute

// DefaultAwait is how long ask-await parks before it answers pending. It is
// short enough that a bridge can loop on it without holding a tool call open
// for minutes.
const DefaultAwait = 25 * time.Second

// Caps. They bound one ask and the state file it lands in, so a runaway
// question cannot grow the session without limit.
const (
	MaxID         = 128
	MaxToolCallID = 256
	MaxQuestions  = 64
	MaxOptions    = 64
	MaxQuestion   = 8000
	MaxLine       = 1000
	MaxLong       = 16000
)

// Error is a refusal with a machine-readable code.
type Error struct {
	Code string
	Msg  string
}

// Error renders the refusal as "ask: <code>: <msg>". The code sits first after
// the package prefix, so a caller can split it off without a table.
func (e *Error) Error() string { return "ask: " + e.Code + ": " + e.Msg }

// Refuse builds a refusal with one code.
func Refuse(code, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// CodeOf returns the code an error carries, or empty for an error that is not
// a refusal.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// CodeIs reports whether err is a refusal carrying code.
func CodeIs(err error, code string) bool { return CodeOf(err) == code }

// ParseOpen decodes an ask-open body strictly: an object, no unknown field, no
// trailing data, and a payload that passes ValidateOpen.
func ParseOpen(raw json.RawMessage) (*OpenReq, error) {
	req := &OpenReq{}
	if err := decode("ask-open", raw, req); err != nil {
		return nil, err
	}
	if err := ValidateOpen(req); err != nil {
		return nil, err
	}
	return req, nil
}

// QuestionsReq is the CLI's ask-open body: the questions alone, because the
// pane and the tool call are flags. The daemon's body is OpenReq.
type QuestionsReq struct {
	Questions []Question `json:"questions"`
}

// ParseQuestions decodes the CLI's ask-open body strictly and applies the
// question rules.
func ParseQuestions(raw json.RawMessage) (*QuestionsReq, error) {
	req := &QuestionsReq{}
	if err := decode("ask-open", raw, req); err != nil {
		return nil, err
	}
	if err := ValidateQuestions(req.Questions); err != nil {
		return nil, err
	}
	return req, nil
}

// ParseAnswer decodes an ask-answer body strictly. It checks the body alone;
// the rules that read the questions are ValidateAnswer's.
func ParseAnswer(raw json.RawMessage) (*AnswerReq, error) {
	req := &AnswerReq{}
	if err := decode("ask-answer", raw, req); err != nil {
		return nil, err
	}
	if err := checkLong("globalNote", req.Note, MaxLong); err != nil {
		return nil, err
	}
	return req, nil
}

// ParseAwait decodes an ask-await body strictly. An empty body waits
// DefaultAwait.
func ParseAwait(raw json.RawMessage) (*AwaitReq, error) {
	req := &AwaitReq{}
	if err := decode("ask-await", raw, req); err != nil {
		return nil, err
	}
	if req.TimeoutMs < 0 {
		return nil, Refuse(CodeInvalidPayload, "timeoutMs %d, want 0 or more", req.TimeoutMs)
	}
	return req, nil
}

// Wait is how long an ask-await parks: the request's timeout when it names one,
// clamped to DefaultAwait.
func (r *AwaitReq) Wait() time.Duration {
	if r == nil || r.TimeoutMs <= 0 {
		return DefaultAwait
	}
	d := time.Duration(r.TimeoutMs) * time.Millisecond
	if d > DefaultAwait {
		return DefaultAwait
	}
	return d
}

// decode is the strict decoder every ask request body goes through: an object,
// no unknown field, no trailing data.
func decode(action string, raw json.RawMessage, v any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		trimmed = []byte("{}")
	}
	if trimmed[0] != '{' {
		return Refuse(CodeInvalidPayload, "%s: request is not an object", action)
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return Refuse(CodeInvalidPayload, "%s: %v", action, err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return Refuse(CodeInvalidPayload, "%s: trailing data after the request", action)
	}
	return nil
}

// Validate checks a stored request: its ids, its questions and, when it
// carries one, its answer. A loader drops a request that fails this instead of
// failing the whole session, the way the box drops a bad entry.
func (r *Request) Validate() error {
	if err := CheckID(r.ID); err != nil {
		return err
	}
	if err := CheckToolCallID(r.ToolCallID); err != nil {
		return err
	}
	if r.Pane == "" {
		return Refuse(CodeInvalidPayload, "pane is empty")
	}
	if err := ValidateQuestions(r.Questions); err != nil {
		return err
	}
	if r.Answer == nil {
		return nil
	}
	return ValidateAnswer(r.Questions, &AnswerReq{
		Cancel: r.Answer.Cancel, Items: r.Answer.Items, Note: r.Answer.Note,
	})
}

// ValidateOpen applies the open rules: a tool call id, at least one question,
// and text that a terminal can draw.
func ValidateOpen(req *OpenReq) error {
	if err := CheckToolCallID(req.ToolCallID); err != nil {
		return err
	}
	return ValidateQuestions(req.Questions)
}

// ValidateQuestions applies the question rules: at least one question, each
// with text, and each option with a label.
func ValidateQuestions(qs []Question) error {
	if len(qs) == 0 {
		return Refuse(CodeInvalidPayload, "the ask carries no questions")
	}
	if len(qs) > MaxQuestions {
		return Refuse(CodeInvalidPayload, "the ask carries %d questions, cap is %d", len(qs), MaxQuestions)
	}
	for i, q := range qs {
		if strings.TrimSpace(q.Question) == "" {
			return Refuse(CodeInvalidPayload, "question %d is blank", i)
		}
		if err := checkLong(fmt.Sprintf("question %d", i), q.Question, MaxQuestion); err != nil {
			return err
		}
		if err := checkLine(fmt.Sprintf("question %d header", i), q.Header, MaxLine); err != nil {
			return err
		}
		if len(q.Options) > MaxOptions {
			return Refuse(CodeInvalidPayload, "question %d carries %d options, cap is %d", i, len(q.Options), MaxOptions)
		}
		for j, o := range q.Options {
			if strings.TrimSpace(o.Label) == "" {
				return Refuse(CodeInvalidPayload, "option %d of question %d is blank", j, i)
			}
			if err := checkLine(fmt.Sprintf("option %d of question %d", j, i), o.Label, MaxLine); err != nil {
				return err
			}
			if err := checkLong(fmt.Sprintf("option %d of question %d description", j, i), o.Description, MaxLong); err != nil {
				return err
			}
			if err := checkLong(fmt.Sprintf("option %d of question %d preview", j, i), o.Preview, MaxLong); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidateAnswer applies the all-or-nothing rule: every question answered
// exactly once unless the reader cancelled, indexes in range and distinct,
// custom text non-blank, and a single-select question answered with one option
// or with text, never both.
func ValidateAnswer(qs []Question, req *AnswerReq) error {
	if err := checkLong("globalNote", req.Note, MaxLong); err != nil {
		return Refuse(CodeInvalidAnswer, "%s", message(err))
	}
	if req.Cancel {
		if len(req.Items) > 0 {
			return Refuse(CodeInvalidAnswer, "a cancelled answer carries no items")
		}
		return nil
	}
	if len(req.Items) != len(qs) {
		return Refuse(CodeInvalidAnswer, "the answer carries %d items, the ask has %d questions", len(req.Items), len(qs))
	}
	seen := make([]bool, len(qs))
	for _, it := range req.Items {
		if it.QuestionIndex < 0 || it.QuestionIndex >= len(qs) {
			return Refuse(CodeInvalidAnswer, "questionIndex %d is out of range, the ask has %d questions", it.QuestionIndex, len(qs))
		}
		if seen[it.QuestionIndex] {
			return Refuse(CodeInvalidAnswer, "question %d is answered twice", it.QuestionIndex)
		}
		seen[it.QuestionIndex] = true
		q := qs[it.QuestionIndex]
		if err := checkItem(it, q, it.QuestionIndex); err != nil {
			return err
		}
	}
	return nil
}

// checkItem applies the rules one item must meet against its question.
func checkItem(it Item, q Question, index int) error {
	picked := make([]bool, len(q.Options))
	for _, oi := range it.OptionIndexes {
		if oi < 0 || oi >= len(q.Options) {
			return Refuse(CodeInvalidAnswer, "option index %d is out of range for question %d, which has %d options", oi, index, len(q.Options))
		}
		if picked[oi] {
			return Refuse(CodeInvalidAnswer, "option %d of question %d is picked twice", oi, index)
		}
		picked[oi] = true
	}
	if it.Text != "" && strings.TrimSpace(it.Text) == "" {
		return Refuse(CodeInvalidAnswer, "question %d carries blank custom text", index)
	}
	if err := checkLong(fmt.Sprintf("question %d text", index), it.Text, MaxQuestion); err != nil {
		return Refuse(CodeInvalidAnswer, "%s", message(err))
	}
	if err := checkLong(fmt.Sprintf("question %d notes", index), it.Notes, MaxLong); err != nil {
		return Refuse(CodeInvalidAnswer, "%s", message(err))
	}
	if q.MultiSelect {
		if len(it.OptionIndexes) == 0 && it.Text == "" {
			return Refuse(CodeInvalidAnswer, "question %d has no choice", index)
		}
		return nil
	}
	// A single-select question takes exactly one option or custom text, never
	// both and never neither.
	if (len(it.OptionIndexes) == 1) == (it.Text != "") {
		return Refuse(CodeInvalidAnswer, "question %d wants one option or one custom text", index)
	}
	return nil
}

// CheckID applies the ask id shape: a non-empty id with no space or control
// character and under the id cap. The store owns the id, so this only keeps
// junk out of a request that names one.
func CheckID(id string) error {
	return checkIDShape("id", id, MaxID)
}

// CheckToolCallID applies the tool call id shape: the id an agent's tool call
// carries, which is non-empty and drawn from printable characters.
func CheckToolCallID(id string) error {
	return checkIDShape("toolCallId", id, MaxToolCallID)
}

// checkIDShape is the rule an id meets: non-empty, under the cap, and made of
// printable non-space characters, because an id is a key and never a sentence.
func checkIDShape(field, s string, max int) error {
	if s == "" {
		return Refuse(CodeInvalidPayload, "%s is empty", field)
	}
	if len(s) > max {
		return Refuse(CodeInvalidPayload, "%s is %d bytes, cap is %d", field, len(s), max)
	}
	if !utf8.ValidString(s) {
		return Refuse(CodeInvalidPayload, "%s is not valid UTF-8", field)
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return Refuse(CodeInvalidPayload, "%s has a space or control character %q", field, r)
		}
	}
	return nil
}

// checkLine applies the one-row text rule: a byte cap, valid UTF-8 and no
// control character, because the value is drawn on one line.
func checkLine(field, s string, max int) error {
	return checkText(field, s, max, false)
}

// checkLong applies the long-text rule: the line rule except that a newline and
// a tab are legal, because a preview, a question and an answer note are blocks
// of text, not rows.
func checkLong(field, s string, max int) error {
	return checkText(field, s, max, true)
}

func checkText(field, s string, max int, long bool) error {
	if len(s) > max {
		return Refuse(CodeInvalidPayload, "%s is %d bytes, cap is %d", field, len(s), max)
	}
	if !utf8.ValidString(s) {
		return Refuse(CodeInvalidPayload, "%s is not valid UTF-8", field)
	}
	for _, r := range s {
		if long && (r == '\n' || r == '\t') {
			continue
		}
		if unicode.IsControl(r) {
			return Refuse(CodeInvalidPayload, "%s has a control character %q", field, r)
		}
	}
	return nil
}

// message returns a refusal's message without its code, so a caller can re-code
// the same text as an invalid answer.
func message(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Msg
	}
	return err.Error()
}
