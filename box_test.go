package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

// schemaProp is the part of the published box schema the tests read.
type schemaProp struct {
	Type                 string                `json:"type"`
	Format               string                `json:"format"`
	Enum                 []string              `json:"enum"`
	Properties           map[string]schemaProp `json:"properties"`
	Required             []string              `json:"required"`
	AdditionalProperties *bool                 `json:"additionalProperties"`
}

type schemaDoc struct {
	Actions map[string]struct {
		Request schemaProp `json:"request"`
		Reply   schemaProp `json:"reply"`
	} `json:"actions"`
}

// readBoxSchema marshals schema() and decodes it into the shape the tests
// assert on.
func readBoxSchema(t *testing.T) schemaDoc {
	t.Helper()
	b, err := json.Marshal(schema())
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	var doc schemaDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	return doc
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestMessageBoxGoldenEntry pins the frozen entry JSON, field order included.
func TestMessageBoxGoldenEntry(t *testing.T) {
	want := `{"seq":7,"at":"2026-11-03T14:40:12Z","from":"w3p1","key":"lane/a","state":"done","msg":"gate green"}`
	e := entry{
		Seq:   7,
		At:    time.Date(2026, time.November, 3, 14, 40, 12, 0, time.UTC),
		From:  "w3p1",
		Key:   "lane/a",
		State: stateDone,
		Msg:   "gate green",
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	if string(b) != want {
		t.Fatalf("entry json =\n %s\nwant\n %s", b, want)
	}
	var back entry
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("decode entry: %v", err)
	}
	if back.Seq != e.Seq || !back.At.Equal(e.At) || back.From != e.From ||
		back.Key != e.Key || back.State != e.State || back.Msg != e.Msg {
		t.Fatalf("entry round trip = %+v", back)
	}
	if err := e.validate(); err != nil {
		t.Fatalf("the frozen entry is not valid: %v", err)
	}
}

// TestMessageBoxDecodeStrict proves the one helper rejects what the contract
// does not name: an unknown field, trailing data and an unknown action.
func TestMessageBoxDecodeStrict(t *testing.T) {
	cases := []struct {
		name   string
		action string
		raw    string
		want   string
	}{
		{"unknown field", "msg-post", `{"state":"done","extra":1}`, "unknown field"},
		{"trailing data", "msg-post", `{"state":"done"}{"state":"done"}`, "trailing data"},
		{"unknown action", "msg-nope", `{}`, "unknown action"},
		{"not an object", "msg-post", `["done"]`, "not an object"},
		{"null request", "msg-list", `null`, "not an object"},
	}
	for _, c := range cases {
		_, err := parseBoxReq(c.action, json.RawMessage(c.raw))
		if err == nil {
			t.Fatalf("%s: %s accepted, want an error", c.name, c.raw)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: error %q does not mention %q", c.name, err, c.want)
		}
	}

	got, err := parseBoxReq("msg-post", json.RawMessage(`{"key":"lane/a","state":"done","msg":"gate green","from":"w3p1"}`))
	if err != nil {
		t.Fatalf("valid post rejected: %v", err)
	}
	req, ok := got.(*postReq)
	if !ok {
		t.Fatalf("valid post decoded to %T", got)
	}
	if req.Key != "lane/a" || req.State != stateDone || req.Msg != "gate green" || req.From != "w3p1" {
		t.Fatalf("valid post = %+v", req)
	}
	if _, err := parseBoxReq("msg-schema", nil); err != nil {
		t.Fatalf("schema with an empty request rejected: %v", err)
	}
}

// TestMessageBoxUnknownState proves parseState accepts exactly the enum and
// names the valid set when it refuses.
func TestMessageBoxUnknownState(t *testing.T) {
	for _, name := range stateNames() {
		if _, err := parseState(name); err != nil {
			t.Fatalf("state %q rejected: %v", name, err)
		}
	}
	for _, bad := range []string{"sleepy", "", "Done", "clear"} {
		_, err := parseState(bad)
		if err == nil {
			t.Fatalf("state %q accepted, want an error", bad)
		}
		for _, name := range stateNames() {
			if !strings.Contains(err.Error(), name) {
				t.Fatalf("state %q error %q does not name %q", bad, err, name)
			}
		}
	}

	// The request path refuses the same values. A wait alone admits an empty
	// state, which asks for any state, so a subscriber can read a stream.
	for _, raw := range []string{`{"state":"sleepy"}`, `{"state":""}`} {
		if _, err := parseBoxReq("msg-post", json.RawMessage(raw)); err == nil {
			t.Fatalf("post %s accepted, want a bad state error", raw)
		}
		if raw == `{"state":"sleepy"}` {
			if _, err := parseBoxReq("msg-wait", json.RawMessage(`{"state":"sleepy","timeoutMs":1000}`)); err == nil {
				t.Fatalf("wait %s accepted, want a bad state error", raw)
			}
		}
	}
	if _, err := parseBoxReq("msg-wait", json.RawMessage(`{"state":"","timeoutMs":1000}`)); err != nil {
		t.Fatalf("a wait with an empty state rejected: %v", err)
	}
}

// TestMessageBoxClearIsNotState proves the pane status "clear" is not a box
// state.
func TestMessageBoxClearIsNotState(t *testing.T) {
	if _, err := parseState("clear"); err == nil {
		t.Fatal(`"clear" parsed as a box state`)
	}
	_, err := parseBoxReq("msg-post", json.RawMessage(`{"state":"clear"}`))
	if err == nil {
		t.Fatal(`post with state "clear" accepted`)
	}
	_, set, ok := strings.Cut(err.Error(), "want one of ")
	if !ok {
		t.Fatalf("error %q does not name the valid set", err)
	}
	if !sameStrings(strings.Split(set, ", "), stateNames()) {
		t.Fatalf("error names %q, want %v", set, stateNames())
	}
	for _, name := range stateNames() {
		if name == "clear" {
			t.Fatal(`the enum carries "clear"`)
		}
	}
}

// TestMessageBoxSchemaMatchesEnum proves the published schema and the Go enum
// are one list, in one order.
func TestMessageBoxSchemaMatchesEnum(t *testing.T) {
	doc := readBoxSchema(t)
	if len(doc.Actions) != len(boxWire) {
		t.Fatalf("schema has %d actions, want %d", len(doc.Actions), len(boxWire))
	}
	for _, action := range actionNames() {
		shape, ok := doc.Actions[action]
		if !ok {
			t.Fatalf("schema has no action %q", action)
		}
		if shape.Request.Type != "object" || shape.Reply.Type != "object" {
			t.Fatalf("%s shapes = %+v / %+v", action, shape.Request, shape.Reply)
		}
		if len(shape.Request.Properties) == 0 && action != "msg-schema" {
			t.Fatalf("%s request schema = %+v", action, shape.Request)
		}
		if len(shape.Reply.Properties) == 0 {
			t.Fatalf("%s reply schema = %+v", action, shape.Reply)
		}
		for name, prop := range map[string]schemaProp{"request": shape.Request, "reply": shape.Reply} {
			if prop.AdditionalProperties == nil || *prop.AdditionalProperties {
				t.Fatalf("%s %s admits unknown fields: %+v", action, name, prop)
			}
		}
	}

	want := stateNames()
	post := doc.Actions["msg-post"]
	if got := post.Request.Properties["state"].Enum; !sameStrings(got, want) {
		t.Fatalf("post state enum = %v, want %v", got, want)
	}
	if !sameStrings(post.Request.Required, []string{"state"}) {
		t.Fatalf("post required = %v, want [state]", post.Request.Required)
	}
	if got := doc.Actions["msg-wait"].Request.Properties["state"].Enum; !sameStrings(got, want) {
		t.Fatalf("wait state enum = %v, want %v", got, want)
	}
	if got := doc.Actions["msg-wait"].Request.Required; !sameStrings(got, []string{"timeoutMs"}) {
		t.Fatalf("wait required = %v, want [timeoutMs]: state is optional, empty means any", got)
	}
	// The cursor and the branch are additions to both reads.
	for _, action := range []string{"msg-list", "msg-wait"} {
		req := doc.Actions[action].Request
		if req.Properties["afterSeq"].Type != "integer" {
			t.Fatalf("%s afterSeq schema = %+v", action, req.Properties["afterSeq"])
		}
		if req.Properties["keyPrefix"].Type != "string" {
			t.Fatalf("%s keyPrefix schema = %+v", action, req.Properties["keyPrefix"])
		}
	}
	if got := doc.Actions["msg-list"].Request.Required; len(got) != 0 {
		t.Fatalf("list required = %v, want none", got)
	}

	// The entry the reply carries keeps the frozen field order and the enum.
	e := post.Reply.Properties["entry"]
	if e.Type != "object" {
		t.Fatalf("entry schema type = %q", e.Type)
	}
	if want := []string{"seq", "at", "from", "key", "state", "msg"}; !sameStrings(e.Required, want) {
		t.Fatalf("entry required = %v, want %v", e.Required, want)
	}
	if got := e.Properties["state"].Enum; !sameStrings(got, want) {
		t.Fatalf("entry state enum = %v, want %v", got, want)
	}
	if e.Properties["at"].Type != "string" || e.Properties["at"].Format != "date-time" {
		t.Fatalf("entry at schema = %+v", e.Properties["at"])
	}
	if e.Properties["seq"].Type != "integer" {
		t.Fatalf("entry seq schema = %+v", e.Properties["seq"])
	}
}

// TestMessageBoxEntryRejectsBadText proves a stored entry obeys the same text
// rule a request does.
func TestMessageBoxEntryRejectsBadText(t *testing.T) {
	base := entry{
		Seq:   1,
		At:    time.Date(2026, time.November, 3, 14, 40, 12, 0, time.UTC),
		From:  "w1p1",
		Key:   "lane/a",
		State: stateDone,
		Msg:   "gate green",
	}
	if err := base.validate(); err != nil {
		t.Fatalf("valid entry rejected: %v", err)
	}
	cases := []struct {
		name string
		make func(entry) entry
	}{
		{"nul in msg", func(e entry) entry { e.Msg = "gate\x00green"; return e }},
		{"newline in msg", func(e entry) entry { e.Msg = "gate\ngreen"; return e }},
		{"escape in msg", func(e entry) entry { e.Msg = "gate\x1bgreen"; return e }},
		{"nul in key", func(e entry) entry { e.Key = "lane\x00a"; return e }},
		{"invalid utf-8", func(e entry) entry { e.Msg = "gate\xffgreen"; return e }},
		{"bad state", func(e entry) entry { e.State = "clear"; return e }},
	}
	for _, c := range cases {
		if err := c.make(base).validate(); err == nil {
			t.Fatalf("%s: entry accepted, want an error", c.name)
		}
	}
	if _, err := parseBoxReq("msg-post", json.RawMessage(`{"state":"note","msg":"a\u0000b"}`)); err == nil {
		t.Fatal("a post with a control character in msg was accepted")
	}
}

// TestMessageBoxTextCaps proves the cap is inclusive and one byte over is not.
func TestMessageBoxTextCaps(t *testing.T) {
	atCap := strings.Repeat("a", boxMaxMsg)
	over := atCap + "a"
	cases := []struct {
		name string
		raw  json.RawMessage
		ok   bool
	}{
		{"msg at the cap", json.RawMessage(`{"state":"note","msg":"` + atCap + `"}`), true},
		{"msg one byte over", json.RawMessage(`{"state":"note","msg":"` + over + `"}`), false},
		{"key at the cap", json.RawMessage(`{"state":"note","key":"` + strings.Repeat("k", boxMaxKey) + `"}`), true},
		{"key one byte over", json.RawMessage(`{"state":"note","key":"` + strings.Repeat("k", boxMaxKey+1) + `"}`), false},
	}
	for _, c := range cases {
		_, err := parseBoxReq("msg-post", c.raw)
		if c.ok && err != nil {
			t.Fatalf("%s rejected: %v", c.name, err)
		}
		if !c.ok && err == nil {
			t.Fatalf("%s accepted, want an error", c.name)
		}
	}
	// A multibyte rune counts as its bytes, not its runes.
	wide := strings.Repeat("é", boxMaxMsg/2)
	if _, err := parseBoxReq("msg-post", json.RawMessage(`{"state":"note","msg":"`+wide+`"}`)); err != nil {
		t.Fatalf("msg of exactly %d bytes rejected: %v", len(wide), err)
	}
}

// TestMessageBoxFromShape proves from is empty or a pane id.
func TestMessageBoxFromShape(t *testing.T) {
	cases := []struct {
		from string
		ok   bool
	}{
		{"", true},
		{"w1p1", true},
		{"w12p34", true},
		{"lane", false},
		{"w1", false},
		{"1p2", false},
		{"w1p", false},
		{"w1p2p3", false},
		{"W1P2", false},
		{"w1p2 ", false},
	}
	for _, c := range cases {
		raw := json.RawMessage(`{"state":"note","from":"` + c.from + `"}`)
		_, err := parseBoxReq("msg-post", raw)
		if c.ok && err != nil {
			t.Fatalf("from %q rejected: %v", c.from, err)
		}
		if !c.ok && err == nil {
			t.Fatalf("from %q accepted, want an error", c.from)
		}
	}
}

// TestMessageBoxLimitClamp proves a list limit lands inside the log.
func TestMessageBoxLimitClamp(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, boxMaxEntries},
		{-5, boxMaxEntries},
		{1, 1},
		{7, 7},
		{boxMaxEntries, boxMaxEntries},
		{boxMaxEntries + 1, boxMaxEntries},
		{1 << 20, boxMaxEntries},
	}
	for _, c := range cases {
		raw := json.RawMessage(`{"limit":` + strconv.Itoa(c.in) + `}`)
		got, err := parseBoxReq("msg-list", raw)
		if err != nil {
			t.Fatalf("limit %d rejected: %v", c.in, err)
		}
		if req := got.(*listReq); req.Limit != c.want {
			t.Fatalf("limit %d clamped to %d, want %d", c.in, req.Limit, c.want)
		}
	}
}

// TestMessageBoxWaitFields proves a wait needs a bounded timeout, takes an
// optional state, and takes a cursor only when it is a whole number of rows
// already seen.
func TestMessageBoxWaitFields(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		ok   bool
	}{
		{"state and timeout", `{"state":"done","timeoutMs":1000}`, true},
		{"no state", `{"timeoutMs":1000}`, true},
		{"empty state", `{"state":"","timeoutMs":1000}`, true},
		{"no timeout", `{"state":"done"}`, false},
		{"zero timeout", `{"state":"done","timeoutMs":0}`, false},
		{"negative timeout", `{"state":"done","timeoutMs":-1}`, false},
		{"timeout at the bound", `{"state":"done","timeoutMs":3600000}`, true},
		{"timeout over the bound", `{"state":"done","timeoutMs":3600001}`, false},
		{"cursor zero", `{"timeoutMs":1000,"afterSeq":0}`, true},
		{"negative cursor", `{"timeoutMs":1000,"afterSeq":-1}`, false},
		{"branch and exact topic", `{"timeoutMs":1000,"key":"a","keyPrefix":"b"}`, false},
	}
	for _, c := range cases {
		_, err := parseBoxReq("msg-wait", json.RawMessage(c.raw))
		if c.ok && err != nil {
			t.Fatalf("%s rejected: %v", c.name, err)
		}
		if !c.ok && err == nil {
			t.Fatalf("%s accepted, want an error", c.name)
		}
	}
}
