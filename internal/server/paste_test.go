package server

import (
	"testing"

	"dm/internal/wire"
)

// TestWriteToCarriesThePasteFlag proves the server is a pass-through on the
// paste path: the block reaches the pane socket marked as a paste, so the pane
// can frame it for a program that asked for bracketed paste, and no enter
// follows it.
func TestWriteToCarriesThePasteFlag(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, _ := rpcServer(t, "rpcpaste")
	spy := spyPane(t, s, a)

	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "write-to", Target: a,
		Msg: "one\ntwo", Paste: true, ID: 1}); !m.OK {
		t.Fatalf("write-to: %s", m.Error)
	}
	if m := readSpy(t, spy); m.Action != "write" || m.Msg != "one\ntwo" || !m.Paste || m.Enter {
		t.Fatalf("write frame = %+v, want the block kept as a paste with no enter", m)
	}
}

// TestWriteToTypedStaysUnmarked proves a typed write keeps its shape on the way
// to the pane: the text, the enter and no paste flag, so a program that never
// asked for bracketed paste sees exactly the bytes it always did.
func TestWriteToTypedStaysUnmarked(t *testing.T) {
	t.Parallel()
	s, cl, conn, a, _ := rpcServer(t, "rpcpastetyped")
	spy := spyPane(t, s, a)

	if m := rpcCall(t, s, cl, conn, wire.Msg{Type: "req", Action: "write-to", Target: a,
		Msg: "ls", Enter: true, ID: 1}); !m.OK {
		t.Fatalf("write-to: %s", m.Error)
	}
	if m := readSpy(t, spy); m.Msg != "ls" || !m.Enter || m.Paste {
		t.Fatalf("write frame = %+v, want the typed line unchanged", m)
	}
}
