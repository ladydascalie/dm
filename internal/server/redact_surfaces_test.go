package server

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dm/internal/wire"
)

// liveSecret registers and activates one secret on a fresh server, so the
// value sits in the session's live list and every pane heard the scrub set.
func liveSecret(t *testing.T, name, value string) (*server, *client, net.Conn) {
	t.Helper()
	s, cl, conn, _ := secretServer(t, "redact", value, nil)
	reg := wire.Msg{Action: "secret-register", Name: name, Tool: "op", Ref: "op://Personal/gh/token"}
	if rep := secretCall(t, s, cl, conn, reg); !rep.OK {
		t.Fatalf("register: %s", rep.Error)
	}
	if rep := secretCall(t, s, cl, conn, wire.Msg{Action: "secret-use", Name: name}); !rep.OK {
		t.Fatalf("use: %s", rep.Error)
	}
	return s, cl, conn
}

// stateText reads the session's state file as it sits on disk.
func stateText(t *testing.T, s *server) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(s.st.SessionDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// snapshotText marshals the live snapshot, the frame every attached client and
// the web fleet draw their picture from.
func snapshotText(t *testing.T, s *server) string {
	t.Helper()
	b, err := json.Marshal(snapshot(s))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// noValue fails when a surface carries the value, and proves the redaction
// landed rather than the text being lost: a length-preserving run of one
// redaction byte per value byte has to be there.
func noValue(t *testing.T, surface, text, value string) {
	t.Helper()
	if strings.Contains(text, value) {
		t.Fatalf("%s holds the value %q:\n%s", surface, value, text)
	}
	if want := strings.Repeat("*", len(value)); !strings.Contains(text, want) {
		t.Fatalf("%s carries no redaction run %q:\n%s", surface, want, text)
	}
}

// TestBoxPostRedactsALiveValue proves a value posted into the box never enters
// the state file, the box listing the rail and the phone draw, or a snapshot.
func TestBoxPostRedactsALiveValue(t *testing.T) {
	t.Parallel()
	const value = "poc-box-9f2c-secret"
	s, cl, conn := liveSecret(t, "gh", value)

	body := fmt.Sprintf(
		`{"from":"w1p1","key":"lane/a","state":"note","msg":"the token %s was echoed","body":"raw: %s"}`,
		value, value,
	)
	post := wire.Msg{Type: "req", Action: "msg-post", Res: json.RawMessage(body)}
	if rep := rpcCall(t, s, cl, conn, post); !rep.OK {
		t.Fatalf("msg-post: %s", rep.Error)
	}
	noValue(t, "state.json", stateText(t, s), value)

	list := wire.Msg{Type: "req", Action: "msg-list", Res: json.RawMessage(`{"limit":10}`)}
	rep := rpcCall(t, s, cl, conn, list)
	if !rep.OK {
		t.Fatalf("msg-list: %s", rep.Error)
	}
	noValue(t, "the box listing", string(rep.Res), value)

	// The snapshot carries no box entries at all, so the only claim here is
	// that the value is nowhere in it.
	if snap := snapshotText(t, s); strings.Contains(snap, value) {
		t.Fatalf("the snapshot holds the value:\n%s", snap)
	}
}

// TestAskStoreRedactsALiveValue proves a value carried by a question, a header
// or an option never enters the store: not the state file, not the list, not a
// snapshot. It also proves the reader's own text is scrubbed on the answer.
func TestAskStoreRedactsALiveValue(t *testing.T) {
	t.Parallel()
	const value = "poc-ask-9f2c-secret"
	s, cl, conn := liveSecret(t, "gh", value)

	body := fmt.Sprintf(`{"toolCallId":"tc-redact-1","questions":[`+
		`{"question":"which %s do you mean","header":"%s","options":[`+
		`{"label":"the %s one","description":"about %s","preview":"line one\n%s is here"}]},`+
		`{"question":"free text for %s","header":"notes","options":[]}]}`,
		value, value, value, value, value, value,
	)
	open := wire.Msg{Type: "req", Action: "ask-open", Target: "w1p1", Res: json.RawMessage(body)}
	rep := rpcCall(t, s, cl, conn, open)
	if !rep.OK {
		t.Fatalf("ask-open: %s", rep.Error)
	}
	var opened struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rep.Res, &opened); err != nil {
		t.Fatal(err)
	}
	if opened.ID == "" {
		t.Fatalf("ask-open reply carries no id: %s", rep.Res)
	}

	noValue(t, "state.json after the open", stateText(t, s), value)
	list := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "ask-list"})
	if !list.OK {
		t.Fatalf("ask-list: %s", list.Error)
	}
	noValue(t, "the ask list", string(list.Res), value)
	noValue(t, "the snapshot after the open", snapshotText(t, s), value)

	answer := fmt.Sprintf(`{"items":[`+
		`{"questionIndex":0,"optionIndexes":[0],"notes":"picked %s"},`+
		`{"questionIndex":1,"text":"typed %s"}],"globalNote":"note %s"}`,
		value, value, value,
	)
	settle := wire.Msg{Type: "req", Action: "ask-answer", Target: opened.ID, Res: json.RawMessage(answer)}
	if rep := rpcCall(t, s, cl, conn, settle); !rep.OK {
		t.Fatalf("ask-answer: %s", rep.Error)
	}
	noValue(t, "state.json after the answer", stateText(t, s), value)
	list = rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "ask-list"})
	if !list.OK {
		t.Fatalf("ask-list: %s", list.Error)
	}
	noValue(t, "the ask list after the answer", string(list.Res), value)
}
