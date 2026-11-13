package client

import (
	"reflect"
	"testing"
	"time"

	"dm/internal/testutil"
)

// TestSwitcherDiffSplit: D closes the dashboard and asks the server to split
// the selected pane on the right, with the diff command as the new pane's
// program through sh -c.
func TestSwitcherDiffSplit(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	feed(tui, "D")
	if tui.sw != nil {
		t.Fatal("D left the dashboard open")
	}
	select {
	case m := <-got:
		if m.Type != "req" || m.Action != "split" || m.Target != "w1p1" || m.Dir != "right" {
			t.Fatalf("D sent %+v, want a split to the right of w1p1", m)
		}
		if want := []string{"sh", "-c", "git diff"}; !reflect.DeepEqual(m.Argv, want) {
			t.Fatalf("D sent argv %v, want %v", m.Argv, want)
		}
	case <-time.After(time.Second):
		t.Fatal("D sent no split request")
	}
	testutil.NoMsg(t, got)
}

// TestFloatDiffSplits: D in a diff float closes the float and reruns the same
// command in a pane split off the pane the float was opened on.
func TestFloatDiffSplits(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	req := openFloat(t, tui, got, "git diff")

	feed(tui, "D")
	m := <-got
	if m.Type != "req" || m.Action != "float-close" || m.Float != req.Float {
		t.Fatalf("D sent %+v, want a float-close for the float", m)
	}
	m = <-got
	if m.Type != "req" || m.Action != "split" || m.Target != "w1p1" || m.Dir != "right" {
		t.Fatalf("D sent %+v, want a split to the right of w1p1", m)
	}
	if want := []string{"sh", "-c", "git diff"}; !reflect.DeepEqual(m.Argv, want) {
		t.Fatalf("D sent argv %v, want %v", m.Argv, want)
	}
	if tui.float != nil || tui.mode != "" || len(tui.floats) != 0 {
		t.Fatalf("D left float %v, mode %q and %d floats", tui.float, tui.mode, len(tui.floats))
	}
	testutil.NoMsg(t, got)
}

// TestFloatCommandKeepsD: D in a user command's float belongs to the command.
// Only a diff float owns the split chord.
func TestFloatCommandKeepsD(t *testing.T) {
	t.Parallel()
	tui, got := commandTUI(t)
	// The prefix arms the main keymap; t then dispatches the command.
	feed(tui, "\x02t")
	req := <-got
	if req.Type != "req" || req.Action != "float-open" || req.Target != "w1p1" {
		t.Fatalf("t sent %+v, want a float-open request for the focused pane w1p1", req)
	}
	tui.apply(floatReply(t, req, "go test ./..."))
	if tui.float == nil || tui.mode != "float" {
		t.Fatalf("the reply left float %v and mode %q", tui.float, tui.mode)
	}

	feed(tui, "D")
	m := <-got
	if m.Type != "keys" || m.Target != req.Float || string(m.Keys) != "D" {
		t.Fatalf("D sent %+v, want the key to the command", m)
	}
	if tui.float == nil || tui.mode != "float" {
		t.Fatalf("D left float %v and mode %q", tui.float, tui.mode)
	}
	testutil.NoMsg(t, got)
}
