package client

import (
	"testing"
	"time"

	"dm/internal/testutil"
	"dm/internal/wire"
)

// TestCloseWindowActionSendsWindowID proves X resolves the focused pane to its
// window and never sends the pane id.
func TestCloseWindowActionSendsWindowID(t *testing.T) {
	t.Parallel()
	tui, got := clientTUI(t, nil)
	tui.snap.Focus = "w1p1"
	tui.snap.All = []wire.AllPane{
		{ID: "w1p1", Window: "w1"},
		{ID: "w2p1", Window: "w2"},
	}
	tui.armed = true

	tui.runAction("close-window")

	m := testutil.NextMsg(t, got)
	if m.Type != "req" || m.Action != "close-window" || m.Target != "w1" {
		t.Fatalf("close-window sent %+v, want a req for w1", m)
	}
	if m.Target == "w1p1" {
		t.Fatal("close-window sent the pane id")
	}
	if tui.armed {
		t.Fatal("close-window left the client armed")
	}
}

// TestCloseWindowActionUnknownPaneSendsNothing proves a focused pane outside
// the snapshot sends no request and raises a toast.
func TestCloseWindowActionUnknownPaneSendsNothing(t *testing.T) {
	t.Parallel()
	tui, got := clientTUI(t, nil)
	tui.snap.Focus = "w9p9"
	tui.snap.All = []wire.AllPane{{ID: "w1p1", Window: "w1"}}

	tui.runAction("close-window")

	select {
	case m := <-got:
		t.Fatalf("close-window sent %+v for an unknown pane", m)
	case <-time.After(50 * time.Millisecond):
	}
	if n := len(tui.toasts); n == 0 || tui.toasts[n-1].text != "no window for w9p9" {
		t.Fatalf("toasts = %+v, want no window for w9p9", tui.toasts)
	}
}
