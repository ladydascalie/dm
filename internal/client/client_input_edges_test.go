package client

import (
	"testing"
	"time"

	"dm/internal/testutil"
	"dm/internal/vt"
	"dm/internal/wire"
)

// farScreen puts a screen on the far pane's dashboard terminal, with bracketed
// paste on when bracketed is true. The modes ride the screen snapshot, which is
// how the client learns them for a pane outside the active window.
func farScreen(t *testing.T, tui *tui, bracketed bool) {
	t.Helper()
	src := vt.NewTerm(20, 4)
	if bracketed {
		src.Write([]byte("\x1b[?2004h"))
	}
	src.Write([]byte("far pane"))
	src.Flush()
	tui.apply(wire.Msg{Type: "screen", Target: farPane, VTSnap: src.Snapshot()})
	if tui.swVT[farPane] == nil {
		t.Fatal("the screen reply left no dashboard terminal")
	}
}

// TestDashboardInputPasteFramesForTarget proves a paste in input mode is framed
// for the pane the dashboard sends to, not for the focused pane behind it.
func TestDashboardInputPasteFramesForTarget(t *testing.T) {
	paste := append(append(append([]byte(nil), pasteStart...), []byte("hello")...), pasteEnd...)
	for _, c := range []struct {
		name      string
		bracketed bool
		want      []byte
	}{
		{name: "wants markers", bracketed: true, want: paste},
		{name: "plain text", bracketed: false, want: []byte("hello")},
	} {
		tui, got := switcherWithFar(t)
		// The focused pane keeps bracketed paste off, so a focus-based frame
		// would strip the markers and fail the first case.
		feed(tui, "\x02a")
		if !tui.sw.Jump(3) {
			t.Fatalf("%s: the fixture has %d rows, want three", c.name, tui.sw.RowCount())
		}
		feed(tui, "i")
		if !tui.swInput {
			t.Fatalf("%s: i did not enter input mode", c.name)
		}
		farScreen(t, tui, c.bracketed)
		feed(tui, string(paste))
		select {
		case m := <-got:
			if m.Type != "keys" || m.Target != farPane {
				t.Fatalf("%s: paste sent %+v, want keys to %s", c.name, m, farPane)
			}
			if string(m.Keys) != string(c.want) {
				t.Fatalf("%s: paste keys = %q, want %q", c.name, m.Keys, c.want)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s: the paste sent nothing", c.name)
		}
		// The paste also asks for the pane's screen, so the answer shows.
		if req := screenRequest(t, got); req.Target != farPane {
			t.Fatalf("%s: screen request = %+v, want %s", c.name, req, farPane)
		}
		testutil.NoMsg(t, got)
	}
}

// TestDashboardInputCoalescedEsc proves an esc that arrives in the same read as
// another key leaves input mode and never reaches the pane.
func TestDashboardInputCoalescedEsc(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	feed(tui, "i")
	if !tui.swInput {
		t.Fatal("i did not enter input mode")
	}
	feed(tui, "\x1bx") // one read: esc and a letter
	if tui.swInput {
		t.Fatal("esc and a letter in one read left input mode on")
	}
	if tui.sw == nil {
		t.Fatal("esc closed the dashboard")
	}
	select {
	case m := <-got:
		if m.Type != "keys" || m.Target != "w1p1" || string(m.Keys) != "x" {
			t.Fatalf("coalesced esc sent %+v, want only the letter", m)
		}
	case <-time.After(time.Second):
		t.Fatal("coalesced esc sent nothing")
	}
	// The key also asks for the pane's screen; nothing else follows.
	if req := screenRequest(t, got); req.Target != "w1p1" {
		t.Fatalf("screen request = %+v, want w1p1", req)
	}
	testutil.NoMsg(t, got)
	// The dashboard takes the keys again.
	feed(tui, "j")
	if it, ok := tui.sw.Selected(); !ok || it.Pane != "w1p2" {
		t.Fatalf("after the mode left, j selects %+v", it)
	}
	testutil.NoMsg(t, got)

	// A lone esc waits for a sequence's final byte; the pending timer then
	// leaves the mode and sends nothing at all.
	lonely, quiet := switcherTUI(t)
	feed(lonely, "\x02a")
	feed(lonely, "i")
	feed(lonely, "\x1b")
	if !lonely.pending {
		t.Fatal("a lone esc did not wait for the rest of a sequence")
	}
	lonely.escAt = time.Now().Add(-time.Second)
	lonely.expireSwitcher(time.Now())
	if lonely.swInput || lonely.sw == nil {
		t.Fatalf("a lone esc left input %v with the dashboard %v", lonely.swInput, lonely.sw != nil)
	}
	testutil.NoMsg(t, quiet)
}

// forwardedKeys reads one keys message for target, so a test can assert the
// pane got exactly the bytes it should. The screen request behind it is the
// test's to check: the preview ask rides the cadence, so a second key inside
// the interval asks for nothing.
func forwardedKeys(t *testing.T, got chan wire.Msg, target, keys string) {
	t.Helper()
	select {
	case m := <-got:
		if m.Type != "keys" || m.Target != target || string(m.Keys) != keys {
			t.Fatalf("forwarded %+v, want keys %q to %s", m, keys, target)
		}
	case <-time.After(time.Second):
		t.Fatalf("no keys %q forwarded to %s", keys, target)
	}
}

// TestDashboardInputForwardsArrows proves an arrow is a key the program asked
// for: it goes to the pane whole and input mode stays on. A sequence split
// across reads waits for its final byte, then forwards.
func TestDashboardInputForwardsArrows(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	feed(tui, "i")
	feed(tui, "\x1b[A") // up arrow, one read
	if !tui.swInput {
		t.Fatal("an up arrow left input mode")
	}
	forwardedKeys(t, got, "w1p1", "\x1b[A")
	if req := screenRequest(t, got); req.Target != "w1p1" {
		t.Fatalf("screen request = %+v, want w1p1", req)
	}
	testutil.NoMsg(t, got)

	// The head of a sequence waits for the final byte.
	feed(tui, "\x1b[1")
	if !tui.pending {
		t.Fatal("the head of a sequence did not wait")
	}
	testutil.NoMsg(t, got)
	feed(tui, "A")
	if !tui.swInput {
		t.Fatal("the sequence's final byte left input mode")
	}
	// The arrow that just asked holds the next ask back: the keys go, the
	// screen request waits for the cadence.
	forwardedKeys(t, got, "w1p1", "\x1b[1A")
	testutil.NoMsg(t, got)
}
