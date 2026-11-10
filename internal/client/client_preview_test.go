package client

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"dm/internal/testutil"
	"dm/internal/vt"
	"dm/internal/wire"
)

// farPane is a pane outside the active window: the dashboard lists it, and the
// client holds no local terminal for it.
const farPane = "w2p1"

// switcherWithFar adds that pane to the dashboard fixture and returns the
// client with the socket its requests land on.
func switcherWithFar(t *testing.T) (*tui, chan wire.Msg) {
	t.Helper()
	tui, got := switcherTUI(t)
	tui.snap.All = append(tui.snap.All, wire.AllPane{
		ID: farPane, Name: "far", Workspace: "s1", Window: "w2",
	})
	return tui, got
}

// screenRequest reads one screen request from the socket and returns it.
func screenRequest(t *testing.T, got chan wire.Msg) wire.Msg {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case m := <-got:
			if m.Type == "req" && m.Action == "screen" {
				return m
			}
		case <-deadline:
			t.Fatal("no screen request")
		}
	}
}

// frameHas replays a drawn frame and reports whether the screen holds want.
func frameHas(t *testing.T, tui *tui, want string) bool {
	t.Helper()
	out := tui.out.(*bytes.Buffer)
	screen := vt.NewTerm(tui.snap.Cols, tui.snap.Rows)
	screen.Write(out.Bytes())
	screen.Flush()
	for y := 0; y < tui.snap.Rows; y++ {
		var b strings.Builder
		for x := 0; x < tui.snap.Cols; x++ {
			c := screen.Grid().Cell(x, y).Content
			if c == "" {
				c = " "
			}
			b.WriteString(c)
		}
		if strings.Contains(b.String(), want) {
			return true
		}
	}
	return false
}

// TestDashboardPreviewScreenLands proves the dashboard asks for a pane outside
// the active window, restores the reply into its own terminal, and paints it.
func TestDashboardPreviewScreenLands(t *testing.T) {
	tui, got := switcherWithFar(t)
	feed(tui, "\x02a")
	if !tui.sw.Jump(3) {
		t.Fatalf("the fixture has %d rows, want three", tui.sw.RowCount())
	}
	tui.previewTick(time.Now())
	req := screenRequest(t, got)
	if req.Target != farPane || req.ID <= 0 {
		t.Fatalf("screen request = %+v, want a tagged request for %s", req, farPane)
	}

	src := vt.NewTerm(20, 4)
	src.Write([]byte("LIVE-FAR"))
	src.Flush()
	tui.apply(wire.Msg{Type: "screen", Target: farPane, VTSnap: src.Snapshot()})
	term := tui.swVT[farPane]
	if term == nil {
		t.Fatal("the screen reply left no dashboard terminal")
	}
	if got := term.Grid().Text(0); got != "LIVE-FAR" {
		t.Fatalf("dashboard terminal row 0 = %q, want LIVE-FAR", got)
	}
	// The painter reads the dashboard terminal: one drawn frame carries it.
	out := tui.out.(*bytes.Buffer)
	out.Reset()
	tui.draw()
	if !frameHas(t, tui, "LIVE-FAR") {
		t.Fatal("the drawn frame does not carry the pane's live screen")
	}
	// A selection move drops the terminal on the next tick.
	tui.sw.Jump(1)
	tui.previewTick(time.Now())
	if len(tui.swVT) != 0 {
		t.Fatalf("the selection move kept %d preview terminals", len(tui.swVT))
	}
	// A reply for the pane it left is not kept either.
	stale := vt.NewTerm(20, 4)
	stale.Write([]byte("STALE-FAR"))
	stale.Flush()
	tui.apply(wire.Msg{Type: "screen", Target: farPane, VTSnap: stale.Snapshot()})
	if len(tui.swVT) != 0 {
		t.Fatalf("a reply for an unselected pane left %d terminals", len(tui.swVT))
	}
}

// TestDashboardPreviewCadenceStops proves the refresh rides on the cadence and
// stops when the dashboard closes.
func TestDashboardPreviewCadenceStops(t *testing.T) {
	tui, got := switcherWithFar(t)
	feed(tui, "\x02a")
	tui.sw.Jump(3)
	now := time.Now()
	tui.previewTick(now)
	screenRequest(t, got)
	// Inside the cadence nothing is asked.
	tui.previewTick(now.Add(previewEvery / 2))
	testutil.NoMsg(t, got)
	// Past it, one more request.
	tui.previewTick(now.Add(previewEvery))
	screenRequest(t, got)
	// Closed, nothing is asked however long the client runs.
	closeSwitcher(tui)
	tui.previewTick(now.Add(time.Minute))
	testutil.NoMsg(t, got)
	if len(tui.swVT) != 0 {
		t.Fatalf("close kept %d preview terminals", len(tui.swVT))
	}
}

// TestDashboardInputAsksOnce proves one key in input mode sends the key and one
// screen request, so an answer appears without waiting for the cadence.
func TestDashboardInputAsksOnce(t *testing.T) {
	tui, got := switcherWithFar(t)
	feed(tui, "\x02a")
	tui.sw.Jump(3)
	feed(tui, "i")
	if !tui.swInput {
		t.Fatal("i did not enter input mode")
	}
	feed(tui, "x")
	select {
	case m := <-got:
		if m.Type != "keys" || m.Target != farPane || string(m.Keys) != "x" {
			t.Fatalf("input mode sent %+v, want keys x to %s", m, farPane)
		}
	case <-time.After(time.Second):
		t.Fatal("input mode sent nothing")
	}
	if req := screenRequest(t, got); req.Target != farPane {
		t.Fatalf("screen request = %+v, want %s", req, farPane)
	}
	testutil.NoMsg(t, got)
}
