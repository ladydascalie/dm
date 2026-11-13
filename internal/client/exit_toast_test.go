package client

import (
	"encoding/json"
	"testing"

	"dm/internal/box"
	"dm/internal/wire"
)

// A pane's exit is told once: the notify/ entry the server logs before the
// teardown rides the catch-up and toasts, and the exit broadcast itself adds
// no second toast saying the same thing.
func TestPaneExitToastsOnce(t *testing.T) {
	t.Parallel()
	tui := &tui{snap: &wire.Snapshot{Focus: "w1p1", Cols: 60, Rows: 24}}

	tui.apply(wire.Msg{Type: "exit", Target: "w1p2"})
	if n := len(tui.toasts); n != 0 {
		t.Fatalf("toasts = %d, want none from the exit broadcast alone", n)
	}

	rep := box.ListReply{Entries: []box.Entry{
		{Seq: 1, Key: box.NotifyPrefix + "w1p2", State: box.StateNote, Msg: "exited"},
	}}
	body, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	tui.takeNotifyList(wire.Msg{Type: "resp", OK: true, Action: "msg-list", Res: body})

	if n := len(tui.toasts); n != 1 {
		t.Fatalf("toasts = %d, want the one the entry raised", n)
	}
	if got, want := tui.toasts[0].text, "notify/w1p2 note: exited"; got != want {
		t.Fatalf("toast = %q, want %q", got, want)
	}
}
