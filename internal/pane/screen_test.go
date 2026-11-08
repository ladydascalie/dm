package pane

import (
	"net"
	"strings"
	"testing"
	"time"

	"dm/internal/vt"
	"dm/internal/wire"
)

// TestScreenReplyCarriesSnapshot proves the screen action sends the visible
// terminal state as a versioned snapshot, not an ANSI replay, and that the
// snapshot decodes and restores.
func TestScreenReplyCarriesSnapshot(t *testing.T) {
	p := &paneProc{
		id:   "w1p1",
		vt:   vt.NewTerm(20, 3),
		ring: &scrollRing{},
		subs: map[*subConn]bool{},
		st:   wire.Status{State: "idle"},
	}
	p.vt.Write([]byte("日本語 e\u0301"))
	p.vt.Write([]byte("\x1b(0"))
	p.vt.Grid().SetScrollRegion(vt.ScrollRegion{Top: 1, Bottom: 2, Left: 0, Right: 19})

	serve, cli := net.Pipe()
	defer cli.Close()
	go p.serve(serve)

	_ = cli.SetDeadline(time.Now().Add(5 * time.Second))
	if err := wire.WriteMsg(cli, wire.Msg{Type: "req", Action: "screen", ID: 1}); err != nil {
		t.Fatalf("send screen request: %v", err)
	}
	reply, err := wire.ReadMsg(cli)
	if err != nil {
		t.Fatalf("read screen reply: %v", err)
	}
	if reply.Type != "screen" {
		t.Fatalf("reply type %q, want screen", reply.Type)
	}
	if reply.Data != nil {
		t.Fatalf("screen reply carried %d replay bytes, want none", len(reply.Data))
	}
	if reply.VTSnap == nil {
		t.Fatal("screen reply carried no snapshot")
	}
	if reply.VTSnap.Version != vt.SnapshotVersion {
		t.Fatalf("snapshot version %d, want %d", reply.VTSnap.Version, vt.SnapshotVersion)
	}
	if reply.VTSnap.Cols != 20 || reply.VTSnap.Rows != 3 {
		t.Fatalf("snapshot size %dx%d, want 20x3", reply.VTSnap.Cols, reply.VTSnap.Rows)
	}

	// The wire form decodes, as the client will decode it.
	b, err := reply.VTSnap.Encode()
	if err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}
	snap, err := vt.DecodeSnapshot(b)
	if err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	restored := vt.NewTerm(1, 1)
	if err := restored.Restore(snap); err != nil {
		t.Fatalf("restore snapshot: %v", err)
	}
	if got := restored.Grid().Text(0); !strings.Contains(got, "日本語") || !strings.Contains(got, "e\u0301") {
		t.Fatalf("restored row 0 = %q, want the CJK and combining text", got)
	}
	if cs := restored.Grid().Charset(); cs.G0 != vt.CharsetDECSpecial {
		t.Fatalf("restored charset = %+v, want DEC special", cs)
	}
	if got := restored.Grid().ScrollRegion(); got != (vt.ScrollRegion{Top: 1, Bottom: 2, Left: 0, Right: 19}) {
		t.Fatalf("restored scroll region = %+v", got)
	}
}
