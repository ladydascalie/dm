package box

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
	Items                *schemaProp           `json:"items"`
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
	b, err := json.Marshal(Schema())
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	var doc schemaDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	return doc
}

// emptyReqActions are the actions whose request carries no field.
var emptyReqActions = map[string]bool{"msg-schema": true, "msg-watchers": true}

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
	want := `{"seq":7,"at":"2026-11-03T14:40:12Z","from":"w3p1","key":"lane/a","state":"done","msg":"gate green","body":""}`
	e := Entry{
		Seq:   7,
		At:    time.Date(2026, time.November, 3, 14, 40, 12, 0, time.UTC),
		From:  "w3p1",
		Key:   "lane/a",
		State: StateDone,
		Msg:   "gate green",
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	if string(b) != want {
		t.Fatalf("entry json =\n %s\nwant\n %s", b, want)
	}
	var back Entry
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("decode entry: %v", err)
	}
	if back.Seq != e.Seq || !back.At.Equal(e.At) || back.From != e.From ||
		back.Key != e.Key || back.State != e.State || back.Msg != e.Msg || back.Body != e.Body {
		t.Fatalf("entry round trip = %+v", back)
	}
	if err := e.Validate(); err != nil {
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
		_, err := ParseReq(c.action, json.RawMessage(c.raw))
		if err == nil {
			t.Fatalf("%s: %s accepted, want an error", c.name, c.raw)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: error %q does not mention %q", c.name, err, c.want)
		}
	}

	got, err := ParseReq("msg-post", json.RawMessage(`{"key":"lane/a","state":"done","msg":"gate green","from":"w3p1"}`))
	if err != nil {
		t.Fatalf("valid post rejected: %v", err)
	}
	req, ok := got.(*PostReq)
	if !ok {
		t.Fatalf("valid post decoded to %T", got)
	}
	if req.Key != "lane/a" || req.State != StateDone || req.Msg != "gate green" || req.From != "w3p1" {
		t.Fatalf("valid post = %+v", req)
	}
	if _, err := ParseReq("msg-schema", nil); err != nil {
		t.Fatalf("schema with an empty request rejected: %v", err)
	}
}

// TestMessageBoxUnknownState proves parseState accepts exactly the enum and
// names the valid set when it refuses.
func TestMessageBoxUnknownState(t *testing.T) {
	for _, name := range StateNames() {
		if _, err := ParseState(name); err != nil {
			t.Fatalf("state %q rejected: %v", name, err)
		}
	}
	for _, bad := range []string{"sleepy", "", "Done", "clear"} {
		_, err := ParseState(bad)
		if err == nil {
			t.Fatalf("state %q accepted, want an error", bad)
		}
		for _, name := range StateNames() {
			if !strings.Contains(err.Error(), name) {
				t.Fatalf("state %q error %q does not name %q", bad, err, name)
			}
		}
	}

	// The request path refuses the same values. A wait alone admits an empty
	// state, which asks for any state, so a subscriber can read a stream.
	for _, raw := range []string{`{"state":"sleepy"}`, `{"state":""}`} {
		if _, err := ParseReq("msg-post", json.RawMessage(raw)); err == nil {
			t.Fatalf("post %s accepted, want a bad state error", raw)
		}
		if raw == `{"state":"sleepy"}` {
			if _, err := ParseReq("msg-wait", json.RawMessage(`{"state":"sleepy","timeoutMs":1000}`)); err == nil {
				t.Fatalf("wait %s accepted, want a bad state error", raw)
			}
		}
	}
	if _, err := ParseReq("msg-wait", json.RawMessage(`{"state":"","timeoutMs":1000}`)); err != nil {
		t.Fatalf("a wait with an empty state rejected: %v", err)
	}
}

// TestMessageBoxClearIsNotState proves the pane status "clear" is not a box
// state.
func TestMessageBoxClearIsNotState(t *testing.T) {
	if _, err := ParseState("clear"); err == nil {
		t.Fatal(`"clear" parsed as a box state`)
	}
	_, err := ParseReq("msg-post", json.RawMessage(`{"state":"clear"}`))
	if err == nil {
		t.Fatal(`post with state "clear" accepted`)
	}
	_, set, ok := strings.Cut(err.Error(), "want one of ")
	if !ok {
		t.Fatalf("error %q does not name the valid set", err)
	}
	if !sameStrings(strings.Split(set, ", "), StateNames()) {
		t.Fatalf("error names %q, want %v", set, StateNames())
	}
	for _, name := range StateNames() {
		if name == "clear" {
			t.Fatal(`the enum carries "clear"`)
		}
	}
}

// TestMessageBoxSchemaMatchesEnum proves the published schema and the Go enum
// are one list, in one order.
func TestMessageBoxSchemaMatchesEnum(t *testing.T) {
	doc := readBoxSchema(t)
	if len(doc.Actions) != len(Wire) {
		t.Fatalf("schema has %d actions, want %d", len(doc.Actions), len(Wire))
	}
	for _, action := range ActionNames() {
		shape, ok := doc.Actions[action]
		if !ok {
			t.Fatalf("schema has no action %q", action)
		}
		if shape.Request.Type != "object" || shape.Reply.Type != "object" {
			t.Fatalf("%s shapes = %+v / %+v", action, shape.Request, shape.Reply)
		}
		if len(shape.Request.Properties) == 0 && !emptyReqActions[action] {
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

	want := StateNames()
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
	if want := []string{"seq", "at", "from", "key", "state", "msg", "body"}; !sameStrings(e.Required, want) {
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
	base := Entry{
		Seq:   1,
		At:    time.Date(2026, time.November, 3, 14, 40, 12, 0, time.UTC),
		From:  "w1p1",
		Key:   "lane/a",
		State: StateDone,
		Msg:   "gate green",
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid entry rejected: %v", err)
	}
	cases := []struct {
		name string
		make func(Entry) Entry
	}{
		{"nul in msg", func(e Entry) Entry { e.Msg = "gate\x00green"; return e }},
		{"newline in msg", func(e Entry) Entry { e.Msg = "gate\ngreen"; return e }},
		{"escape in msg", func(e Entry) Entry { e.Msg = "gate\x1bgreen"; return e }},
		{"nul in key", func(e Entry) Entry { e.Key = "lane\x00a"; return e }},
		{"invalid utf-8", func(e Entry) Entry { e.Msg = "gate\xffgreen"; return e }},
		{"bad state", func(e Entry) Entry { e.State = "clear"; return e }},
	}
	for _, c := range cases {
		if err := c.make(base).Validate(); err == nil {
			t.Fatalf("%s: entry accepted, want an error", c.name)
		}
	}
	if _, err := ParseReq("msg-post", json.RawMessage(`{"state":"note","msg":"a\u0000b"}`)); err == nil {
		t.Fatal("a post with a control character in msg was accepted")
	}
}

// TestMessageBoxTextCaps proves the cap is inclusive and one byte over is not.
func TestMessageBoxTextCaps(t *testing.T) {
	atCap := strings.Repeat("a", MaxMsg)
	over := atCap + "a"
	cases := []struct {
		name string
		raw  json.RawMessage
		ok   bool
	}{
		{"msg at the cap", json.RawMessage(`{"state":"note","msg":"` + atCap + `"}`), true},
		{"msg one byte over", json.RawMessage(`{"state":"note","msg":"` + over + `"}`), false},
		{"key at the cap", json.RawMessage(`{"state":"note","key":"` + strings.Repeat("k", MaxKey) + `"}`), true},
		{"key one byte over", json.RawMessage(`{"state":"note","key":"` + strings.Repeat("k", MaxKey+1) + `"}`), false},
	}
	for _, c := range cases {
		_, err := ParseReq("msg-post", c.raw)
		if c.ok && err != nil {
			t.Fatalf("%s rejected: %v", c.name, err)
		}
		if !c.ok && err == nil {
			t.Fatalf("%s accepted, want an error", c.name)
		}
	}
	// A multibyte rune counts as its bytes, not its runes.
	wide := strings.Repeat("é", MaxMsg/2)
	if _, err := ParseReq("msg-post", json.RawMessage(`{"state":"note","msg":"`+wide+`"}`)); err != nil {
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
		_, err := ParseReq("msg-post", raw)
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
		{0, MaxEntries},
		{-5, MaxEntries},
		{1, 1},
		{7, 7},
		{MaxEntries, MaxEntries},
		{MaxEntries + 1, MaxEntries},
		{1 << 20, MaxEntries},
	}
	for _, c := range cases {
		raw := json.RawMessage(`{"limit":` + strconv.Itoa(c.in) + `}`)
		got, err := ParseReq("msg-list", raw)
		if err != nil {
			t.Fatalf("limit %d rejected: %v", c.in, err)
		}
		if req := got.(*ListReq); req.Limit != c.want {
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
		_, err := ParseReq("msg-wait", json.RawMessage(c.raw))
		if c.ok && err != nil {
			t.Fatalf("%s rejected: %v", c.name, err)
		}
		if !c.ok && err == nil {
			t.Fatalf("%s accepted, want an error", c.name)
		}
	}
}

// TestMessageBoxGoldenWatch pins the frozen watch JSON, field order included.
func TestMessageBoxGoldenWatch(t *testing.T) {
	want := `{"pane":"w1p2","key":"lane/a","keyPrefix":"","cursor":7,"parked":true,"at":"2026-11-03T14:40:12Z"}`
	w := Watch{
		Pane:   "w1p2",
		Key:    "lane/a",
		Cursor: 7,
		Parked: true,
		At:     time.Date(2026, time.November, 3, 14, 40, 12, 0, time.UTC),
	}
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("marshal watch: %v", err)
	}
	if string(b) != want {
		t.Fatalf("watch json =\n %s\nwant\n %s", b, want)
	}
	var back Watch
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("decode watch: %v", err)
	}
	if back.Pane != w.Pane || back.Key != w.Key || back.KeyPrefix != w.KeyPrefix ||
		back.Cursor != w.Cursor || back.Parked != w.Parked || !back.At.Equal(w.At) {
		t.Fatalf("watch round trip = %+v", back)
	}

	reply, err := json.Marshal(WatchersReply{Watchers: []Watch{w}})
	if err != nil {
		t.Fatalf("marshal watchers reply: %v", err)
	}
	if got, want := string(reply), `{"watchers":[`+want+`]}`; got != want {
		t.Fatalf("watchers reply json =\n %s\nwant\n %s", got, want)
	}
}

// TestMessageBoxSchemaReadsAndWatchers proves the derived schema already names
// from on both reads and the six watcher fields, and that neither read makes
// from required.
func TestMessageBoxSchemaReadsAndWatchers(t *testing.T) {
	doc := readBoxSchema(t)
	for _, action := range []string{"msg-list", "msg-wait"} {
		req := doc.Actions[action].Request
		if req.Properties["from"].Type != "string" {
			t.Fatalf("%s from schema = %+v", action, req.Properties["from"])
		}
		for _, name := range req.Required {
			if name == "from" {
				t.Fatalf("%s marks from required: %v", action, req.Required)
			}
		}
	}
	if got := doc.Actions["msg-list"].Request.Required; len(got) != 0 {
		t.Fatalf("list required = %v, want none", got)
	}
	if got := doc.Actions["msg-wait"].Request.Required; !sameStrings(got, []string{"timeoutMs"}) {
		t.Fatalf("wait required = %v, want [timeoutMs]", got)
	}

	watchers, ok := doc.Actions["msg-watchers"]
	if !ok {
		t.Fatal("schema has no msg-watchers action")
	}
	if got := watchers.Request.Required; len(got) != 0 {
		t.Fatalf("watchers required = %v, want none", got)
	}
	list := watchers.Reply.Properties["watchers"]
	if list.Type != "array" || list.Items == nil {
		t.Fatalf("watchers reply schema = %+v", list)
	}
	w := *list.Items
	if w.Type != "object" {
		t.Fatalf("watch schema type = %q", w.Type)
	}
	if want := []string{"pane", "key", "keyPrefix", "cursor", "parked", "at"}; !sameStrings(w.Required, want) {
		t.Fatalf("watch required = %v, want %v", w.Required, want)
	}
	for name, want := range map[string]string{"pane": "string", "key": "string", "keyPrefix": "string", "cursor": "integer", "parked": "boolean"} {
		if got := w.Properties[name].Type; got != want {
			t.Fatalf("watch %s type = %q, want %q", name, got, want)
		}
	}
	if at := w.Properties["at"]; at.Type != "string" || at.Format != "date-time" {
		t.Fatalf("watch at schema = %+v", at)
	}
}

// TestMessageBoxReadsFrom proves an explicit empty from is accepted on both
// reads, a malformed one is refused, and a pane id is kept.
func TestMessageBoxReadsFrom(t *testing.T) {
	cases := []struct {
		name   string
		action string
		raw    string
		ok     bool
	}{
		{"list empty from", "msg-list", `{"from":""}`, true},
		{"list pane from", "msg-list", `{"from":"w3p1"}`, true},
		{"list bad from", "msg-list", `{"from":"lane"}`, false},
		{"list pane id without a pane", "msg-list", `{"from":"w3p"}`, false},
		{"wait empty from", "msg-wait", `{"timeoutMs":1000,"from":""}`, true},
		{"wait pane from", "msg-wait", `{"timeoutMs":1000,"from":"w12p34"}`, true},
		{"wait bad from", "msg-wait", `{"timeoutMs":1000,"from":"3p1"}`, false},
	}
	for _, c := range cases {
		got, err := ParseReq(c.action, json.RawMessage(c.raw))
		if c.ok && err != nil {
			t.Fatalf("%s rejected: %v", c.name, err)
		}
		if !c.ok && err == nil {
			t.Fatalf("%s accepted, want an error", c.name)
		}
		if !c.ok {
			continue
		}
		switch req := got.(type) {
		case *ListReq:
			if req.From != "w3p1" && req.From != "" {
				t.Fatalf("%s from = %q", c.name, req.From)
			}
		case *WaitReq:
			if req.From != "w12p34" && req.From != "" {
				t.Fatalf("%s from = %q", c.name, req.From)
			}
		default:
			t.Fatalf("%s decoded to %T", c.name, got)
		}
	}
}

// TestMessageBoxWatchersRequest proves msg-watchers decodes an empty request
// and refuses a field it does not name.
func TestMessageBoxWatchersRequest(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`{}`), json.RawMessage(` { } `)} {
		got, err := ParseReq("msg-watchers", raw)
		if err != nil {
			t.Fatalf("msg-watchers %q rejected: %v", raw, err)
		}
		if _, ok := got.(*WatchersReq); !ok {
			t.Fatalf("msg-watchers %q decoded to %T", raw, got)
		}
	}
	if _, err := ParseReq("msg-watchers", json.RawMessage(`{"pane":"w1p1"}`)); err == nil {
		t.Fatal("msg-watchers accepted a field, want an error")
	}
}

// TestMessageBoxSubFields proves the sub request takes the read fields without
// a timeout: one topic, an optional state and pane, and a cursor that may be 0.
func TestMessageBoxSubFields(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		ok   bool
	}{
		{"no fields", `{}`, true},
		{"cursor only", `{"afterSeq":0}`, true},
		{"cursor and state", `{"afterSeq":3,"state":"done"}`, true},
		{"empty state", `{"state":""}`, true},
		{"unknown state", `{"state":"clear"}`, false},
		{"negative cursor", `{"afterSeq":-1}`, false},
		{"branch and exact topic", `{"key":"a","keyPrefix":"b"}`, false},
		{"bad from", `{"from":"lane"}`, false},
		{"pane from", `{"from":"w3p1"}`, true},
	}
	for _, c := range cases {
		_, err := ParseReq("msg-sub", json.RawMessage(c.raw))
		if c.ok && err != nil {
			t.Fatalf("%s rejected: %v", c.name, err)
		}
		if !c.ok && err == nil {
			t.Fatalf("%s accepted, want an error", c.name)
		}
	}

	// A subscription has no timeout: it runs until its connection goes.
	if _, err := ParseReq("msg-sub", json.RawMessage(`{"timeoutMs":1000}`)); err == nil {
		t.Fatal("msg-sub accepted timeoutMs")
	}
	// The cursor stays a pointer, so 0 is a cursor and absent is not one.
	got, err := ParseReq("msg-sub", json.RawMessage(`{"afterSeq":7,"from":"w1p2"}`))
	if err != nil {
		t.Fatalf("sub rejected: %v", err)
	}
	req, ok := got.(*SubReq)
	if !ok {
		t.Fatalf("sub decoded to %T", got)
	}
	if req.AfterSeq == nil || *req.AfterSeq != 7 || req.From != "w1p2" {
		t.Fatalf("sub = %+v, want the cursor 7 from w1p2", req)
	}
	absent, err := ParseReq("msg-sub", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("empty sub rejected: %v", err)
	}
	if req := absent.(*SubReq); req.AfterSeq != nil {
		t.Fatalf("empty sub carries a cursor %d, want none", *req.AfterSeq)
	}
}

// TestMessageBoxGoldenSubFrame pins the frame JSON the stream sends, field
// order included.
func TestMessageBoxGoldenSubFrame(t *testing.T) {
	e := Entry{
		Seq:   7,
		At:    time.Date(2026, time.November, 3, 14, 40, 12, 0, time.UTC),
		From:  "w3p1",
		Key:   "lane/a",
		State: StateDone,
		Msg:   "gate green",
	}
	want := `{"entry":{"seq":7,"at":"2026-11-03T14:40:12Z","from":"w3p1","key":"lane/a","state":"done","msg":"gate green","body":""},"trimmed":true}`
	b, err := json.Marshal(SubFrame{Entry: e, Trimmed: true})
	if err != nil {
		t.Fatalf("marshal sub frame: %v", err)
	}
	if string(b) != want {
		t.Fatalf("sub frame json =\n %s\nwant\n %s", b, want)
	}
	var back SubFrame
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("decode sub frame: %v", err)
	}
	if back.Entry != e || !back.Trimmed {
		t.Fatalf("sub frame round trip = %+v", back)
	}
}

// TestMessageBoxSchemaSub proves the published schema carries the action, the
// request fields with an optional state, and the frame shape.
func TestMessageBoxSchemaSub(t *testing.T) {
	doc := readBoxSchema(t)
	sub, ok := doc.Actions["msg-sub"]
	if !ok {
		t.Fatal("schema has no msg-sub action")
	}

	req := sub.Request
	for name, want := range map[string]string{
		"from": "string", "key": "string", "keyPrefix": "string", "afterSeq": "integer",
	} {
		if got := req.Properties[name].Type; got != want {
			t.Fatalf("sub %s type = %q, want %q", name, got, want)
		}
	}
	if got := req.Properties["state"].Enum; !sameStrings(got, StateNames()) {
		t.Fatalf("sub state enum = %v, want %v", got, StateNames())
	}
	if got := req.Required; len(got) != 0 {
		t.Fatalf("sub required = %v, want none", got)
	}
	if _, ok := req.Properties["timeoutMs"]; ok {
		t.Fatal("sub request schema carries a timeout")
	}

	reply := sub.Reply
	if want := []string{"entry", "trimmed"}; !sameStrings(reply.Required, want) {
		t.Fatalf("sub frame required = %v, want %v", reply.Required, want)
	}
	if got := reply.Properties["trimmed"].Type; got != "boolean" {
		t.Fatalf("sub trimmed type = %q, want boolean", got)
	}
	e := reply.Properties["entry"]
	if e.Type != "object" {
		t.Fatalf("sub entry type = %q", e.Type)
	}
	if want := []string{"seq", "at", "from", "key", "state", "msg", "body"}; !sameStrings(e.Required, want) {
		t.Fatalf("sub entry required = %v, want %v", e.Required, want)
	}
	if got := e.Properties["state"].Enum; !sameStrings(got, StateNames()) {
		t.Fatalf("sub entry state enum = %v, want %v", got, StateNames())
	}
}
