package wire_test

import (
	"bytes"
	"encoding/json"
	"testing"

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
