package wire_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"dm/internal/testutil"
	"dm/internal/wire"
)

// A pane daemon outlives the server, so the read reply must keep its shape.
func TestReadReplyStaysBackwardCompatible(t *testing.T) {
	old, err := wire.ReadMsg(bytes.NewReader(testutil.Frame(t, `{"type":"resp","lines":["a","b"]}`)))
	if err != nil || len(old.Lines) != 2 || old.Lines[1] != "b" {
		t.Fatalf("old read reply: %+v %v", old, err)
	}
	req, err := wire.ReadMsg(bytes.NewReader(testutil.Frame(t, `{"type":"req","action":"history","count":7}`)))
	if err != nil || req.Count != 7 || req.Lines != nil {
		t.Fatalf("history request: %+v %v", req, err)
	}
}

func TestAllPaneWorkspaceUnfoldedRoundTrip(t *testing.T) {
	p := wire.AllPane{WorkspaceUnfolded: true}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var p2 wire.AllPane
	if err := json.Unmarshal(b, &p2); err != nil {
		t.Fatal(err)
	}
	if !p2.WorkspaceUnfolded {
		t.Fatal("WorkspaceUnfolded lost in round trip")
	}

	p.WorkspaceUnfolded = false
	b, err = json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("workspaceUnfolded")) {
		t.Fatal("workspaceUnfolded key present when false")
	}
}

// TestAllPaneShelfRoundTrip proves the shelf mark survives the fleet list, and
// that a live pane leaves the key out: a page reading an old server and a page
// reading a live row must both see no shelf.
func TestAllPaneShelfRoundTrip(t *testing.T) {
	b, err := json.Marshal(wire.AllPane{ID: "w1p1", Shelf: true})
	if err != nil {
		t.Fatal(err)
	}
	var p wire.AllPane
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if !p.Shelf || p.ID != "w1p1" {
		t.Fatalf("round trip = %+v, want the shelf mark kept", p)
	}
	if p.Workspace != "" || p.Window != "" {
		t.Fatalf("a shelved row gained a place: %+v", p)
	}

	b, err = json.Marshal(wire.AllPane{ID: "w1p1"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("shelf")) {
		t.Fatalf("a live row carries the shelf key: %s", b)
	}
}

// TestTermColoursRoundTrip proves the detected pair survives the framing with
// its exact strings, and that an unset pair leaves both keys out of the JSON.
func TestTermColoursRoundTrip(t *testing.T) {
	in := wire.Msg{Type: "req", Action: "term-colours",
		FG: "rgb:ffff/0000/1234", BG: "#aabbcc"}
	var buf bytes.Buffer
	if err := wire.WriteMsg(&buf, in); err != nil {
		t.Fatal(err)
	}
	out, err := wire.ReadMsg(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if out.FG != in.FG || out.BG != in.BG {
		t.Fatalf("round trip = %q/%q, want %q/%q", out.FG, out.BG, in.FG, in.BG)
	}

	b, err := json.Marshal(wire.Msg{Type: "req", Action: "term-colours"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["fg"]; ok {
		t.Fatalf("unset pair kept the fg key: %s", b)
	}
	if _, ok := m["bg"]; ok {
		t.Fatalf("unset pair kept the bg key: %s", b)
	}
}

// TestSnapshotPadRoundTrip proves the padding the server publishes survives
// the frame with its value, and that 0 travels as a real value: the client
// must draw with it, not fall back to a default.
func TestSnapshotPadRoundTrip(t *testing.T) {
	for _, pad := range []int{0, 2} {
		in := wire.Snapshot{Session: "s", Cols: 80, Rows: 24, Pad: pad}
		var buf bytes.Buffer
		if err := wire.WriteMsg(&buf, wire.Msg{Type: "snapshot", Snap: &in}); err != nil {
			t.Fatal(err)
		}
		out, err := wire.ReadMsg(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if out.Snap == nil || out.Snap.Pad != pad {
			t.Fatalf("pad %d round trip = %+v, want %d", pad, out.Snap, pad)
		}
		if out.Snap.Cols != 80 || out.Snap.Rows != 24 {
			t.Fatalf("snapshot fields lost beside pad: %+v", out.Snap)
		}
	}
}

// TestFloatRoundTrip proves the float id survives the frame in both directions
// and that an unset id leaves the key out.
func TestFloatRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	in := wire.Msg{Type: "bytes", Target: "f1", Float: "f1", Data: []byte("hi")}
	if err := wire.WriteMsg(&buf, in); err != nil {
		t.Fatal(err)
	}
	out, err := wire.ReadMsg(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if out.Float != "f1" || out.Target != "f1" {
		t.Fatalf("round trip float/target = %q/%q, want f1/f1", out.Float, out.Target)
	}

	b, err := json.Marshal(wire.Msg{Type: "keys", Target: "f1"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["float"]; ok {
		t.Fatalf("unset float kept the key: %s", b)
	}
}

// TestSetHistoryRoundTrip proves the ring budget survives the frame and that an
// unset budget leaves the key out, so a zero never reaches a pane as a size.
func TestSetHistoryRoundTrip(t *testing.T) {
	in := wire.Msg{Type: "req", Action: "set-history", History: 2 << 20}
	var buf bytes.Buffer
	if err := wire.WriteMsg(&buf, in); err != nil {
		t.Fatal(err)
	}
	out, err := wire.ReadMsg(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if out.Action != "set-history" || out.History != 2<<20 {
		t.Fatalf("round trip = %q/%d, want set-history/%d", out.Action, out.History, 2<<20)
	}

	b, err := json.Marshal(wire.Msg{Type: "req", Action: "set-history"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["history"]; ok {
		t.Fatalf("unset history kept the key: %s", b)
	}
}

// TestSelectLayoutRoundTrip proves the preset name survives the frame and that
// an unset name leaves the key out.
func TestSelectLayoutRoundTrip(t *testing.T) {
	in := wire.Msg{Type: "req", Action: "select-layout", Layout: "main-vertical"}
	var buf bytes.Buffer
	if err := wire.WriteMsg(&buf, in); err != nil {
		t.Fatal(err)
	}
	out, err := wire.ReadMsg(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if out.Action != "select-layout" || out.Layout != "main-vertical" {
		t.Fatalf("round trip = %q/%q, want select-layout/main-vertical", out.Action, out.Layout)
	}

	b, err := json.Marshal(wire.Msg{Type: "req", Action: "select-layout"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["layout"]; ok {
		t.Fatalf("unset layout kept the key: %s", b)
	}
}

// TestSynchronizeOnRoundTrip proves the optional on field travels true and
// false as real values, and that an absent value leaves the key out so nil
// can toggle.
func TestSynchronizeOnRoundTrip(t *testing.T) {
	for _, v := range []bool{true, false} {
		on := v
		in := wire.Msg{Type: "req", Action: "synchronize-panes", On: &on}
		var buf bytes.Buffer
		if err := wire.WriteMsg(&buf, in); err != nil {
			t.Fatal(err)
		}
		out, err := wire.ReadMsg(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if out.On == nil || *out.On != v {
			t.Fatalf("on %v round trip = %+v, want %v", v, out.On, v)
		}
	}

	b, err := json.Marshal(wire.Msg{Type: "req", Action: "synchronize-panes"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["on"]; ok {
		t.Fatalf("absent on kept the key: %s", b)
	}
}

// TestSnapshotSyncRoundTrip proves the active window's sync flag survives the
// frame when set and stays out of the JSON when off.
func TestSnapshotSyncRoundTrip(t *testing.T) {
	in := wire.Snapshot{Session: "s", Cols: 80, Rows: 24, Sync: true}
	var buf bytes.Buffer
	if err := wire.WriteMsg(&buf, wire.Msg{Type: "snapshot", Snap: &in}); err != nil {
		t.Fatal(err)
	}
	out, err := wire.ReadMsg(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if out.Snap == nil || !out.Snap.Sync {
		t.Fatalf("sync round trip = %+v, want on", out.Snap)
	}

	b, err := json.Marshal(wire.Snapshot{Session: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("sync")) {
		t.Fatalf("unset sync kept the key: %s", b)
	}
}

// TestWindowInfoLayoutRoundTrip proves the stored preset name survives the
// frame and that an empty name, the custom-tree case, leaves the key out.
func TestWindowInfoLayoutRoundTrip(t *testing.T) {
	in := wire.Snapshot{Session: "s", Windows: []wire.WindowInfo{
		{ID: "w1", Name: "one", Layout: "main-vertical"},
		{ID: "w2", Name: "two"},
	}}
	var buf bytes.Buffer
	if err := wire.WriteMsg(&buf, wire.Msg{Type: "snapshot", Snap: &in}); err != nil {
		t.Fatal(err)
	}
	out, err := wire.ReadMsg(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if out.Snap == nil || len(out.Snap.Windows) != 2 {
		t.Fatalf("windows round trip = %+v", out.Snap)
	}
	if got := out.Snap.Windows[0].Layout; got != "main-vertical" {
		t.Fatalf("layout round trip = %q, want main-vertical", got)
	}
	if got := out.Snap.Windows[1].Layout; got != "" {
		t.Fatalf("empty layout = %q, want empty", got)
	}

	b, err := json.Marshal(wire.WindowInfo{ID: "w2", Name: "two"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["layout"]; ok {
		t.Fatalf("empty layout kept the key: %s", b)
	}
}

func TestWriteMsgRejectsOversize(t *testing.T) {
	// MaxFrame is the read-side bound; the writer must refuse a payload the
	// peer would drop the connection over.
	m := wire.Msg{Type: "bytes", Data: make([]byte, wire.MaxFrame)}
	var buf bytes.Buffer
	if err := wire.WriteMsg(&buf, m); err == nil {
		t.Fatal("WriteMsg oversize = nil error, want an error naming MaxFrame")
	}
	if buf.Len() != 0 {
		t.Errorf("WriteMsg oversize wrote %d bytes before failing, want none", buf.Len())
	}
}

// TestMsgCarriesNoNonce proves the envelope carries no nonce state: the
// nonce guard came off, and no field may smuggle it back.
func TestMsgCarriesNoNonce(t *testing.T) {
	b, err := json.Marshal(wire.Msg{Type: "req", Action: "toggle-secrets", Target: "w1p1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "nonce") {
		t.Fatalf("envelope carries a nonce field: %s", b)
	}
}

// TestTrustFieldDistinguishesAbsentFromFalse proves the sync verdict is a
// pointer on the wire: absent (the dm exec route) is nil, and false is an
// explicit refusal that must survive the frame.
func TestTrustFieldDistinguishesAbsentFromFalse(t *testing.T) {
	cases := []struct {
		body string
		want *bool
	}{
		{`{"action":"secret-sync","path":"/m"}`, nil},
		{`{"action":"secret-sync","path":"/m","trust":true}`, boolPtr(true)},
		{`{"action":"secret-sync","path":"/m","trust":false}`, boolPtr(false)},
	}
	for _, tc := range cases {
		var m wire.Msg
		if err := json.Unmarshal([]byte(tc.body), &m); err != nil {
			t.Fatalf("%s: %v", tc.body, err)
		}
		switch {
		case tc.want == nil && m.Trust != nil:
			t.Fatalf("%s: trust = %v, want absent", tc.body, *m.Trust)
		case tc.want != nil && (m.Trust == nil || *m.Trust != *tc.want):
			t.Fatalf("%s: trust = %v, want %v", tc.body, m.Trust, *tc.want)
		}
		// The absent verdict stays out of the JSON, so an old peer never
		// reads a decision the sender did not make.
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if tc.want == nil && strings.Contains(string(b), `"trust"`) {
			t.Fatalf("absent trust kept the key: %s", b)
		}
	}
}

func boolPtr(v bool) *bool { return &v }

// TestSnapshotAskRoundTrip proves the snapshot carries the flattened asks with
// the published field names, and that an empty store leaves the key out.
func TestSnapshotAskRoundTrip(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	in := wire.Snapshot{Session: "s", Ask: []wire.Ask{{
		ID: "a1", Pane: "w1p1", ToolCallID: "toolu_1", At: at,
		Questions: []wire.AskQuestion{{
			Question: "Which base?", Header: "base", MultiSelect: true,
			Options: []wire.AskOption{{Label: "main", Description: "the trunk", Preview: "a diff"}},
		}},
	}}}
	var buf bytes.Buffer
	if err := wire.WriteMsg(&buf, wire.Msg{Type: "snapshot", Snap: &in}); err != nil {
		t.Fatal(err)
	}
	out, err := wire.ReadMsg(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if out.Snap == nil || len(out.Snap.Ask) != 1 {
		t.Fatalf("ask round trip = %+v, want one ask", out.Snap)
	}
	got := out.Snap.Ask[0]
	if got.ID != "a1" || got.Pane != "w1p1" || got.ToolCallID != "toolu_1" || !got.At.Equal(at) {
		t.Errorf("ask = %+v", got)
	}
	if len(got.Questions) != 1 || !got.Questions[0].MultiSelect ||
		len(got.Questions[0].Options) != 1 || got.Questions[0].Options[0].Preview != "a diff" {
		t.Errorf("questions = %+v", got.Questions)
	}

	b, err := json.Marshal(wire.Snapshot{Session: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(`"ask"`)) {
		t.Fatalf("an empty store kept the key: %s", b)
	}
}
