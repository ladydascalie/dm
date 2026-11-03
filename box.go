package main

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
	boxMaxMsg     = 2000      // bytes in one entry's msg
	boxMaxKey     = 128       // bytes in one entry's key
	boxMaxEntries = 1000      // entries kept in one box
	boxMaxBytes   = 256 << 10 // bytes kept in one box
	boxMaxWaitMs  = 3600000   // longest wait, one hour
)

// entry is one line of the box, exactly as it travels in Res. Every field is
// always present, so the tags carry no omitempty: a reader can tell an empty
// key from a missing one. The server owns seq and at.
type entry struct {
	Seq   int       `json:"seq"`
	At    time.Time `json:"at"`
	From  string    `json:"from"`
	Key   string    `json:"key"`
	State boxState  `json:"state"`
	Msg   string    `json:"msg"`
}

// boxState is the closed set of states an entry can carry. "clear" is a pane
// status, not a box state, and parseState rejects it.
type boxState string

const (
	stateNote    boxState = "note"
	stateWorking boxState = "working"
	stateBlocked boxState = "blocked"
	stateDone    boxState = "done"
	stateError   boxState = "error"
)

// boxStates is the one enum. parseState, its error text and schema all read it,
// so there is no second list to keep in step.
var boxStates = []boxState{stateNote, stateWorking, stateBlocked, stateDone, stateError}

var stateType = reflect.TypeOf(boxState(""))

// stateNames is the valid set in published order.
func stateNames() []string {
	out := make([]string, len(boxStates))
	for i, st := range boxStates {
		out[i] = string(st)
	}
	return out
}

// parseState turns a wire state into a boxState. Everything outside boxStates
// fails with one error that names the valid set, so a caller can print it.
func parseState(s string) (boxState, error) {
	for _, st := range boxStates {
		if string(st) == s {
			return st, nil
		}
	}
	return "", fmt.Errorf("box: bad state %q: want one of %s", s, strings.Join(stateNames(), ", "))
}

// postReq is the msg-post request.
type postReq struct {
	Key   string   `json:"key"`
	State boxState `json:"state"`
	Msg   string   `json:"msg"`
	From  string   `json:"from"`
}

// listReq is the msg-list request. Limit zero or less means the whole box.
type listReq struct {
	Key   string `json:"key"`
	Limit int    `json:"limit"`
}

// clearReq is the msg-clear request. An empty key clears every entry.
type clearReq struct {
	Key string `json:"key"`
}

// waitReq is the msg-wait request.
type waitReq struct {
	Key       string   `json:"key"`
	State     boxState `json:"state"`
	TimeoutMs int      `json:"timeoutMs"`
}

// schemaReq is the msg-schema request: no fields.
type schemaReq struct{}

// postReply carries the stored entry.
type postReply struct {
	Entry entry `json:"entry"`
}

// listReply carries entries newest first.
type listReply struct {
	Entries []entry `json:"entries"`
}

// clearReply carries how many entries went.
type clearReply struct {
	Cleared int `json:"cleared"`
}

// waitReply carries the entry that satisfied the wait.
type waitReply struct {
	Entry entry `json:"entry"`
}

// schemaReply carries the box wire as JSON Schema.
type schemaReply struct {
	Schema map[string]any `json:"schema"`
}

// boxAction is one frozen action: the request the decoder builds, the reply the
// server sends, and the request fields the schema marks required. Validation
// enforces the same required set: parseState rejects an empty state, and the
// wait bound rejects a zero timeout.
type boxAction struct {
	request  func() any
	reply    any
	required []string
}

// boxWire is the frozen wire, one shape per action. parseBoxReq and schema both
// read it, so the decoder and the published schema cannot list different
// actions or different request fields.
var boxWire = map[string]boxAction{
	"msg-post": {
		request:  func() any { return &postReq{} },
		reply:    postReply{},
		required: []string{"state"},
	},
	"msg-list": {
		request: func() any { return &listReq{} },
		reply:   listReply{},
	},
	"msg-clear": {
		request: func() any { return &clearReq{} },
		reply:   clearReply{},
	},
	"msg-wait": {
		request:  func() any { return &waitReq{} },
		reply:    waitReply{},
		required: []string{"state", "timeoutMs"},
	},
	"msg-schema": {
		request: func() any { return &schemaReq{} },
		reply:   schemaReply{},
	},
}

// actionNames is the valid action set, sorted so an error reads the same every
// time.
func actionNames() []string {
	out := make([]string, 0, len(boxWire))
	for action := range boxWire {
		out = append(out, action)
	}
	sort.Strings(out)
	return out
}

// parseBoxReq is the one decoder the server calls. It builds the request the
// action names, rejects an unknown field, rejects trailing data, then validates
// the values. Strictness lives here; no caller decodes a box request itself.
func parseBoxReq(action string, raw json.RawMessage) (any, error) {
	wire, ok := boxWire[action]
	if !ok {
		return nil, fmt.Errorf("box: unknown action %q: want one of %s", action, strings.Join(actionNames(), ", "))
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
	if err := validateBoxReq(action, req); err != nil {
		return nil, err
	}
	return req, nil
}

// validateBoxReq is the value half of the contract: the fields a request must
// carry, the pane id shape of from, the text caps on key and msg, the list
// limit clamp, and the wait timeout bound.
func validateBoxReq(action string, req any) error {
	switch r := req.(type) {
	case *postReq:
		if _, err := parseState(string(r.State)); err != nil {
			return err
		}
		if r.From != "" && !paneIDShape(r.From) {
			return fmt.Errorf("box: from %q is not a pane id", r.From)
		}
		if err := checkText("key", r.Key, boxMaxKey); err != nil {
			return err
		}
		return checkText("msg", r.Msg, boxMaxMsg)
	case *listReq:
		if err := checkText("key", r.Key, boxMaxKey); err != nil {
			return err
		}
		r.Limit = clampLimit(r.Limit)
		return nil
	case *clearReq:
		return checkText("key", r.Key, boxMaxKey)
	case *waitReq:
		if _, err := parseState(string(r.State)); err != nil {
			return err
		}
		if r.TimeoutMs <= 0 || r.TimeoutMs > boxMaxWaitMs {
			return fmt.Errorf("box: timeoutMs %d, want 1 to %d", r.TimeoutMs, boxMaxWaitMs)
		}
		return nil
	case *schemaReq:
		return nil
	}
	return fmt.Errorf("box: %s has no validator for %T", action, req)
}

// checkText applies the text rule to a key or a message: a byte cap, valid
// UTF-8 and no control characters, because an entry is printed on one row.
func checkText(field, s string, max int) error {
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

// validate checks the parts of an entry a request can carry: the state and the
// key and message text. The server owns seq and at. A loader drops an entry
// that fails this instead of failing the whole box.
func (e entry) validate() error {
	if _, err := parseState(string(e.State)); err != nil {
		return err
	}
	if err := checkText("key", e.Key, boxMaxKey); err != nil {
		return err
	}
	return checkText("msg", e.Msg, boxMaxMsg)
}

// paneIDShape is the addressable pane id: a window id and a pane number, w3p1.
// The server checks the id exists in the session tree; this is the cheap shape
// check that keeps junk out before that lookup.
func paneIDShape(s string) bool {
	win, pane, ok := strings.Cut(s, "p")
	if !ok || len(win) < 2 || win[0] != 'w' || pane == "" {
		return false
	}
	return allDigits(win[1:]) && allDigits(pane)
}

// clampLimit holds a list limit inside the log: zero or less means the whole
// box, and anything past the entry cap is the cap.
func clampLimit(n int) int {
	if n <= 0 || n > boxMaxEntries {
		return boxMaxEntries
	}
	return n
}

// schema is the box wire as JSON Schema, served by the msg-schema action. The
// actions, property names and types come from boxWire and the Go types the
// decoder fills, and the state enum comes from boxStates, so there is no second
// hand-written wire list to drift.
func schema() map[string]any {
	actions := make(map[string]any, len(boxWire))
	for action, wire := range boxWire {
		actions[action] = map[string]any{
			"request": requestSchema(wire),
			"reply":   typeSchema(reflect.TypeOf(wire.reply)),
		}
	}
	return map[string]any{
		"title":   "dm message box",
		"actions": actions,
	}
}

// requestSchema is the request object with its required set taken from the
// action table.
func requestSchema(wire boxAction) map[string]any {
	s := typeSchema(reflect.TypeOf(wire.request()))
	required := make([]string, len(wire.required))
	copy(required, wire.required)
	s["required"] = required
	return s
}

// typeSchema describes one Go type with the JSON Schema keywords dm publishes:
// object, array, string and integer, plus the state enum. Property names come
// from the json tags, so the schema cannot name a field the decoder rejects.
func typeSchema(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == reflect.TypeOf(time.Time{}) {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	switch t.Kind() {
	case reflect.String:
		s := map[string]any{"type": "string"}
		if t == stateType {
			s["enum"] = stateNames()
		}
		return s
	case reflect.Int:
		return map[string]any{"type": "integer"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": typeSchema(t.Elem())}
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
			props[name] = typeSchema(f.Type)
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
