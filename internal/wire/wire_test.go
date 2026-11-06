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
