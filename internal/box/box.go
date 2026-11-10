package box

// The message box wire: one append-only log per session that any pane can post
// into and a caller can wait on. This file holds the types and their proof.
// The server and the CLI both read these shapes, so the frozen contract is
// written here once.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Box limits. They bound one entry's text and the whole log, so a runaway
// poster cannot grow the state file without limit.
const (
	MaxMsg     = 2000      // bytes in one entry's msg
	MaxBody    = 16 << 10  // bytes in one entry's body
	MaxKey     = 128       // bytes in one entry's key
	MaxEntries = 1000      // entries kept in one box
	MaxBytes   = 256 << 10 // bytes kept in one box
	MaxWaitMs  = 3600000   // longest wait, one hour
)

// NotifyPrefix marks the topic branch the server renders as desktop
// notifications. An entry under it keeps every box behaviour: validation, caps,
// seq, trim, watchers. The prefix only adds the render.
const NotifyPrefix = "notify/"

// IsNotifyTopic reports whether key names the notify topic itself or anything
// under it, so a caller decides on the key alone.
func IsNotifyTopic(key string) bool {
	return key == NotifyPrefix || strings.HasPrefix(key, NotifyPrefix)
}

// Entry is one line of the box, exactly as it travels in Res. Every field is
// always present, so the tags carry no omitempty: a reader can tell an empty
// key from a missing one. The server owns seq and at.
type Entry struct {
	Seq   int       `json:"seq"`
	At    time.Time `json:"at"`
	From  string    `json:"from"`
	Key   string    `json:"key"`
	State State     `json:"state"`
	Msg   string    `json:"msg"`
	// Body is the long text under the one-line Msg. Optional: an empty body
	// is the normal case, and it still travels, so a reader can tell an
	// empty body from a missing one.
	Body string `json:"body"`
}

// Watch is one reader's place in the log: the pane, its filter and the highest
// seq served under it. Filter is an exact key or a branch, never both. Parked
// is true while a msg-wait on the filter is outstanding. Every field is always
// present, so a reader can tell an empty filter from a missing one.
type Watch struct {
	Pane      string    `json:"pane"`
	Key       string    `json:"key"`
	KeyPrefix string    `json:"keyPrefix"`
	Cursor    int       `json:"cursor"`
	Parked    bool      `json:"parked"`
	At        time.Time `json:"at"`
}

// State is the closed set of states an entry can carry. "clear" is a pane
// status, not a box state, and ParseState rejects it.
type State string

const (
	StateNote    State = "note"
	StateWorking State = "working"
	StateBlocked State = "blocked"
	StateDone    State = "done"
	StateError   State = "error"
)

// States is the one enum. ParseState, its error text and Schema all read it,
// so there is no second list to keep in step.
var States = []State{StateNote, StateWorking, StateBlocked, StateDone, StateError}

var StateType = reflect.TypeOf(State(""))

// StateNames is the valid set in published order.
func StateNames() []string {
	out := make([]string, len(States))
	for i, st := range States {
		out[i] = string(st)
	}
	return out
}

// ParseState turns a wire state into a State. Everything outside States
// fails with one error that names the valid set, so a caller can print it.
func ParseState(s string) (State, error) {
	for _, st := range States {
		if string(st) == s {
			return st, nil
		}
	}
	return "", fmt.Errorf("box: bad state %q: want one of %s", s, strings.Join(StateNames(), ", "))
}

// PostReq is the msg-post request.
type PostReq struct {
	Key   string `json:"key"`
	State State  `json:"state"`
	Msg   string `json:"msg"`
	From  string `json:"from"`
	// Body is the optional long text; empty is the normal case.
	Body string `json:"body"`
}

// ListReq is the msg-list request. Limit zero or less means the whole box.
// Key is an exact topic and KeyPrefix a branch of the topic tree; naming both
// is ambiguous and fails. AfterSeq, when the request carries it, is a cursor:
// only entries past it are listed, so a reader can catch up without repeats.
type ListReq struct {
	Key       string `json:"key"`
	KeyPrefix string `json:"keyPrefix"`
	Limit     int    `json:"limit"`
	AfterSeq  *int   `json:"afterSeq"`
	// From is the reader pane. Empty means the caller is not a pane and records
	// nothing; a non-empty id is shape-checked here and the server looks it up.
	From string `json:"from"`
}

// ClearReq is the msg-clear request. An empty key and an empty keyPrefix
// clear every entry. Key is an exact topic and KeyPrefix a branch of the
// topic tree; naming both is ambiguous and fails.
type ClearReq struct {
	Key       string `json:"key"`
	KeyPrefix string `json:"keyPrefix"`
}

// WaitReq is the msg-wait request. Without AfterSeq it waits for the newest
// entry of the topic to carry the state, which is a status board. With
// AfterSeq it returns the oldest entry past the cursor that matches, so a
// subscriber sees every message in order. State is optional, and empty means
// any state.
type WaitReq struct {
	Key       string `json:"key"`
	KeyPrefix string `json:"keyPrefix"`
	// State is omitted when empty, so an any-state subscriber's request matches
	// the published schema. The decoder still accepts an explicit empty state.
	State     State `json:"state,omitempty"`
	TimeoutMs int   `json:"timeoutMs"`
	AfterSeq  *int  `json:"afterSeq"`
	// From is the reader pane, as on list: empty records nothing.
	From string `json:"from"`
}

// SubReq is the msg-sub request: a stream of entries under one topic filter.
// The fields are the read fields minus the timeout, because a subscription runs
// until its connection goes. AfterSeq is the cursor a reconnecting reader
// resumes at; absent means 0.
type SubReq struct {
	// From is the subscribing pane. Empty means the caller is not a pane and
	// records nothing; a non-empty id must name a pane in the session tree.
	From      string `json:"from"`
	Key       string `json:"key"`
	KeyPrefix string `json:"keyPrefix"`
	// State is omitted when empty, so an any-state subscriber's request matches
	// the published schema. The decoder still accepts an explicit empty state.
	State    State `json:"state,omitempty"`
	AfterSeq *int  `json:"afterSeq"`
}

// SchemaReq is the msg-schema request: no fields.
type SchemaReq struct{}

// PostReply carries the stored entry.
type PostReply struct {
	Entry Entry `json:"entry"`
}

// ListReply carries entries newest first. Trimmed is true when the cursor
// named a row the box has already dropped, so the list starts after a gap.
type ListReply struct {
	Entries []Entry `json:"entries"`
	Trimmed bool    `json:"trimmed"`
}

// ClearReply carries how many entries went.
type ClearReply struct {
	Cleared int `json:"cleared"`
}

// WaitReply carries the entry that satisfied the wait. Trimmed is true when
// the cursor named a row the box has already dropped.
type WaitReply struct {
	Entry   Entry `json:"entry"`
	Trimmed bool  `json:"trimmed"`
}

// SubFrame is one msg-sub frame, delivered on the subscribing connection only.
// Trimmed is true on the first frame of a stream whose cursor sat behind the
// oldest kept row, and false on every frame after it.
type SubFrame struct {
	Entry   Entry `json:"entry"`
	Trimmed bool  `json:"trimmed"`
}

// SchemaReply carries the box wire as JSON Schema.
type SchemaReply struct {
	Schema map[string]any `json:"schema"`
}

// WatchersReq is the msg-watchers request: no fields.
type WatchersReq struct{}

// WatchersReply carries every live watch, sorted by pane, key and keyPrefix.
type WatchersReply struct {
	Watchers []Watch `json:"watchers"`
}

// Action is one frozen action: the request the decoder builds, the reply the
// server sends, and the request fields the schema marks required. Validation
// enforces the same required set: ParseState rejects an empty state, and the
// wait bound rejects a zero timeout.
type Action struct {
	request  func() any
	reply    any
	required []string
}

// Wire is the frozen wire, one shape per action. ParseReq and Schema both
// read it, so the decoder and the published schema cannot list different
// actions or different request fields.
var Wire = map[string]Action{
	"msg-post": {
		request:  func() any { return &PostReq{} },
		reply:    PostReply{},
		required: []string{"state"},
	},
	"msg-list": {
		request: func() any { return &ListReq{} },
		reply:   ListReply{},
	},
	"msg-clear": {
		request: func() any { return &ClearReq{} },
		reply:   ClearReply{},
	},
	"msg-wait": {
		request:  func() any { return &WaitReq{} },
		reply:    WaitReply{},
		required: []string{"timeoutMs"},
	},
	"msg-sub": {
		request: func() any { return &SubReq{} },
		reply:   SubFrame{},
	},
	"msg-watchers": {
		request: func() any { return &WatchersReq{} },
		reply:   WatchersReply{},
	},
	"msg-schema": {
		request: func() any { return &SchemaReq{} },
		reply:   SchemaReply{},
	},
}

// ActionNames is the valid action set, sorted so an error reads the same every
// time.
func ActionNames() []string {
	out := make([]string, 0, len(Wire))
	for action := range Wire {
		out = append(out, action)
	}
	sort.Strings(out)
	return out
}

// ParseReq is the one decoder the server calls. It builds the request the
// action names, rejects an unknown field, rejects trailing data, then validates
// the values. Strictness lives here; no caller decodes a box request itself.
func ParseReq(action string, raw json.RawMessage) (any, error) {
	wire, ok := Wire[action]
	if !ok {
		return nil, fmt.Errorf("box: unknown action %q: want one of %s", action, strings.Join(ActionNames(), ", "))
	}
	req := wire.request()
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		trimmed = []byte("{}")
	}
	if trimmed[0] != '{' {
		return nil, fmt.Errorf("box: %s: request is not an object", action)
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(req); err != nil {
		return nil, fmt.Errorf("box: %s: %w", action, err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("box: %s: trailing data after the request", action)
	}
	if err := ValidateReq(action, req); err != nil {
		return nil, err
	}
	return req, nil
}

// ValidateReq is the value half of the contract: the fields a request must
// carry, the pane id shape of from, the text caps on key and msg, the list
// limit clamp, and the wait timeout bound.
func ValidateReq(action string, req any) error {
	switch r := req.(type) {
	case *PostReq:
		if _, err := ParseState(string(r.State)); err != nil {
			return err
		}
		if err := CheckFrom(r.From); err != nil {
			return err
		}
		if err := CheckText("key", r.Key, MaxKey); err != nil {
			return err
		}
		if err := CheckText("msg", r.Msg, MaxMsg); err != nil {
			return err
		}
		return CheckBody("body", r.Body)
	case *ListReq:
		if err := CheckFrom(r.From); err != nil {
			return err
		}
		if err := CheckTopic(r.Key, r.KeyPrefix); err != nil {
			return err
		}
		if err := CheckAfter(r.AfterSeq); err != nil {
			return err
		}
		r.Limit = ClampLimit(r.Limit)
		return nil
	case *ClearReq:
		return CheckTopic(r.Key, r.KeyPrefix)
	case *WaitReq:
		// An empty state asks for any state, which a subscriber needs.
		if r.State != "" {
			if _, err := ParseState(string(r.State)); err != nil {
				return err
			}
		}
		if err := CheckFrom(r.From); err != nil {
			return err
		}
		if err := CheckTopic(r.Key, r.KeyPrefix); err != nil {
			return err
		}
		if err := CheckAfter(r.AfterSeq); err != nil {
			return err
		}
		if r.TimeoutMs <= 0 || r.TimeoutMs > MaxWaitMs {
			return fmt.Errorf("box: timeoutMs %d, want 1 to %d", r.TimeoutMs, MaxWaitMs)
		}
		return nil
	case *SubReq:
		// An empty state asks for any state, which a subscriber needs.
		if r.State != "" {
			if _, err := ParseState(string(r.State)); err != nil {
				return err
			}
		}
		if err := CheckFrom(r.From); err != nil {
			return err
		}
		if err := CheckTopic(r.Key, r.KeyPrefix); err != nil {
			return err
		}
		return CheckAfter(r.AfterSeq)
	case *SchemaReq, *WatchersReq:
		return nil
	}
	return fmt.Errorf("box: %s has no validator for %T", action, req)
}

// CheckTopic applies the text rule to the two topic fields and refuses a
// request that names both: an exact topic and a branch are different reads.
func CheckTopic(key, prefix string) error {
	if key != "" && prefix != "" {
		return fmt.Errorf("box: key and keyPrefix are exclusive")
	}
	if err := CheckText("key", key, MaxKey); err != nil {
		return err
	}
	return CheckText("keyPrefix", prefix, MaxKey)
}

// CheckAfter refuses a cursor below zero. Absent is not zero: absent means no
// cursor, which is the status board read.
func CheckAfter(after *int) error {
	if after != nil && *after < 0 {
		return fmt.Errorf("box: afterSeq %d, want 0 or more", *after)
	}
	return nil
}

// Match reports whether an entry passes one read: an exact topic, a topic
// prefix and a cursor. An empty filter field admits everything.
func Match(e Entry, key, prefix string, after *int) bool {
	if key != "" && e.Key != key {
		return false
	}
	if prefix != "" && !strings.HasPrefix(e.Key, prefix) {
		return false
	}
	if after != nil && e.Seq <= *after {
		return false
	}
	return true
}

// CheckText applies the text rule to a key or a message: a byte cap, valid
// UTF-8 and no control characters, because an entry is printed on one row.
func CheckText(field, s string, max int) error {
	if len(s) > max {
		return fmt.Errorf("box: %s is %d bytes, cap is %d", field, len(s), max)
	}
	if !utf8.ValidString(s) {
		return fmt.Errorf("box: %s is not valid UTF-8", field)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return fmt.Errorf("box: %s has a control character %q", field, r)
		}
	}
	return nil
}

// CheckBody applies the body rule: the same text rule as CheckText under the
// body cap, except newlines are legal because a body is long text, not a row.
func CheckBody(field, s string) error {
	if len(s) > MaxBody {
		return fmt.Errorf("box: %s is %d bytes, cap is %d", field, len(s), MaxBody)
	}
	if !utf8.ValidString(s) {
		return fmt.Errorf("box: %s is not valid UTF-8", field)
	}
	for _, r := range s {
		if r == '\n' {
			continue
		}
		if unicode.IsControl(r) {
			return fmt.Errorf("box: %s has a control character %q", field, r)
		}
	}
	return nil
}

// Validate checks the parts of an entry a request can carry: the state, the
// key and message text, and the optional body. The server owns seq and at. A
// loader drops an entry that fails this instead of failing the whole box.
func (e Entry) Validate() error {
	if _, err := ParseState(string(e.State)); err != nil {
		return err
	}
	if err := CheckText("key", e.Key, MaxKey); err != nil {
		return err
	}
	if err := CheckText("msg", e.Msg, MaxMsg); err != nil {
		return err
	}
	return CheckBody("body", e.Body)
}

// CheckFrom is the optional reader pane on a read. Empty means the caller is
// not a pane; a non-empty id must look like a pane id. The server checks that
// the id exists in the session tree.
func CheckFrom(from string) error {
	if from != "" && !PaneIDShape(from) {
		return fmt.Errorf("box: from %q is not a pane id", from)
	}
	return nil
}

// PaneIDShape is the addressable pane id: a window id and a pane number, w3p1.
// The server checks the id exists in the session tree; this is the cheap shape
// check that keeps junk out before that lookup.
func PaneIDShape(s string) bool {
	win, pane, ok := strings.Cut(s, "p")
	if !ok || len(win) < 2 || win[0] != 'w' || pane == "" {
		return false
	}
	return allDigits(win[1:]) && allDigits(pane)
}

// allDigits reports whether s is one or more ASCII digits. It is the local
// copy of the cells rule, so this package stays stdlib only.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// ClampLimit holds a list limit inside the log: zero or less means the whole
// box, and anything past the entry cap is the cap.
func ClampLimit(n int) int {
	if n <= 0 || n > MaxEntries {
		return MaxEntries
	}
	return n
}

// Schema is the box wire as JSON Schema, served by the msg-schema action. The
// actions, property names and types come from Wire and the Go types the
// decoder fills, and the state enum comes from States, so there is no second
// hand-written wire list to drift.
func Schema() map[string]any {
	actions := make(map[string]any, len(Wire))
	for action, wire := range Wire {
		actions[action] = map[string]any{
			"request": RequestSchema(wire),
			"reply":   TypeSchema(reflect.TypeOf(wire.reply)),
		}
	}
	return map[string]any{
		"title":   "dm message box",
		"actions": actions,
	}
}

// RequestSchema is the request object with its required set taken from the
// action table.
func RequestSchema(wire Action) map[string]any {
	s := TypeSchema(reflect.TypeOf(wire.request()))
	required := make([]string, len(wire.required))
	copy(required, wire.required)
	s["required"] = required
	return s
}

// TypeSchema describes one Go type with the JSON Schema keywords dm publishes:
// object, array, string and integer, plus the state enum. Property names come
// from the json tags, so the schema cannot name a field the decoder rejects.
func TypeSchema(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == reflect.TypeOf(time.Time{}) {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	switch t.Kind() {
	case reflect.String:
		s := map[string]any{"type": "string"}
		if t == StateType {
			s["enum"] = StateNames()
		}
		return s
	case reflect.Int:
		return map[string]any{"type": "integer"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": TypeSchema(t.Elem())}
	case reflect.Map, reflect.Interface:
		return map[string]any{"type": "object"}
	case reflect.Struct:
		props := make(map[string]any, t.NumField())
		required := make([]string, 0, t.NumField())
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" || name == "-" {
				continue
			}
			props[name] = TypeSchema(f.Type)
			required = append(required, name)
		}
		return map[string]any{
			"type":                 "object",
			"properties":           props,
			"required":             required,
			"additionalProperties": false,
		}
	}
	return map[string]any{}
}
