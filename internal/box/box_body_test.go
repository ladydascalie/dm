package box

// The body tests: the long text under the one-line Msg, its cap and its
// relaxed one-line rule.

import (
	"encoding/json"
	"strings"
	"testing"
)

// CheckBody applies the text rule minus the one-line rule: newlines are
// legal, every other control character stays refused, and the cap is
// MaxBody.
func TestBodyCheckAcceptsNewlinesAndCaps(t *testing.T) {
	if err := CheckBody("body", "line one\nline two\n\nline three"); err != nil {
		t.Fatalf("multi-line body refused: %v", err)
	}
	if err := CheckBody("body", ""); err != nil {
		t.Fatalf("empty body refused: %v", err)
	}
	if err := CheckBody("body", strings.Repeat("x", MaxBody)); err != nil {
		t.Fatalf("body at the cap refused: %v", err)
	}
	if err := CheckBody("body", strings.Repeat("x", MaxBody+1)); err == nil {
		t.Fatal("a body over the cap was accepted")
	}
	for _, bad := range []string{"tab\there", "carriage\rreturn", "\x01bell"} {
		if err := CheckBody("body", bad); err == nil {
			t.Fatalf("body %q was accepted", bad)
		}
	}
	if err := CheckBody("body", "\xff\xfe"); err == nil {
		t.Fatal("an invalid UTF-8 body was accepted")
	}
}

// The one-line rule still holds for Msg, so the two texts differ where the
// brief draws the line.
func TestBodyMsgRuleUnchanged(t *testing.T) {
	if err := CheckText("msg", "line one\nline two", MaxMsg); err == nil {
		t.Fatal("a multi-line msg was accepted")
	}
}

// A post without a body keeps the old shape: the field is absent from the
// request, the empty body travels on the entry, and the schema names body on
// both sides without making it required.
func TestBodyWireShape(t *testing.T) {
	got, err := ParseReq("msg-post", json.RawMessage(`{"key":"lane/a","state":"done","msg":"gate green"}`))
	if err != nil {
		t.Fatalf("post without a body rejected: %v", err)
	}
	if req := got.(*PostReq); req.Body != "" {
		t.Fatalf("post body = %q, want empty", req.Body)
	}
	got, err = ParseReq("msg-post", json.RawMessage(`{"key":"lane/a","state":"done","msg":"gate green","body":"long\ntext"}`))
	if err != nil {
		t.Fatalf("post with a body rejected: %v", err)
	}
	if req := got.(*PostReq); req.Body != "long\ntext" {
		t.Fatalf("post body = %q, want the long text", req.Body)
	}

	// An entry with no body serialises the field, so a reader can tell an
	// empty body from a missing one.
	b, err := json.Marshal(Entry{Seq: 1, Key: "lane/a", State: StateDone, Msg: "m"})
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	if !strings.Contains(string(b), `"body":""`) {
		t.Fatalf("entry json = %s, want an explicit empty body", b)
	}

	// The published schema carries the new field on both sides; the request
	// keeps it optional.
	doc := Schema()
	post := doc["actions"].(map[string]any)["msg-post"].(map[string]any)
	req := post["request"].(map[string]any)
	if _, ok := req["properties"].(map[string]any)["body"]; !ok {
		t.Fatal("post request schema has no body")
	}
	if req["required"] == nil {
		t.Fatal("post request schema lost its required set")
	}
	reply := post["reply"].(map[string]any)
	entryProps := reply["properties"].(map[string]any)["entry"].(map[string]any)["properties"].(map[string]any)
	if entryProps["body"] == nil {
		t.Fatal("entry reply schema has no body")
	}
}
