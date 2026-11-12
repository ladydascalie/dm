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
