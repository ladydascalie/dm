package client

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"dm/internal/testutil"
	"dm/internal/wire"
)

// askDiff opens the dashboard over the two-pane fixture and presses d, so the
// client sends a diff request for the selected pane. It returns the request.
func askDiff(t *testing.T, tui *tui, got chan wire.Msg) wire.Msg {
	t.Helper()
	feed(tui, "\x02a")
	feed(tui, "d")
	if tui.sw != nil {
		t.Fatal("d left the dashboard open")
	}
	select {
	case m := <-got:
		if m.Type != "req" || m.Action != "diff" || m.Target != "w1p1" || m.ID <= 0 {
			t.Fatalf("d sent %+v, want a diff request for w1p1 with an id", m)
		}
		return m
	case <-time.After(time.Second):
		t.Fatal("d sent no diff request")
	}
	return wire.Msg{}
}

// diffLines is a diff long enough to scroll.
func diffLines(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("+line %d", i)
	}
	return lines
}

// TestDiffKeyRequests: d closes the dashboard and asks for the selected pane's
// diff.
func TestDiffKeyRequests(t *testing.T) {
	tui, got := switcherTUI(t)
	askDiff(t, tui, got)
	testutil.NoMsg(t, got)
}

// TestDiffReplyOpensPager: the reply opens the pager, its title names the
// worktree, and a reply to another request opens nothing.
func TestDiffReplyOpensPager(t *testing.T) {
	tui, got := switcherTUI(t)
	tui.snap.All[0].Worktree = "web"
	req := askDiff(t, tui, got)

	// A reply to an older request is not this pager.
	tui.apply(wire.Msg{Type: "resp", Action: "diff", ID: req.ID + 1, OK: true, Lines: diffLines(3)})
	if tui.diff != nil {
		t.Fatal("a stale diff reply opened the pager")
	}

	tui.apply(wire.Msg{Type: "resp", Action: "diff", ID: req.ID, OK: true, Lines: diffLines(100)})
	if tui.diff == nil || tui.mode != "diff" {
		t.Fatalf("the reply left diff %v and mode %q", tui.diff, tui.mode)
	}
	if got := tui.diff.float.Title; !strings.Contains(got, "web") {
		t.Fatalf("pager title = %q, want the worktree name", got)
	}
	testutil.NoMsg(t, got)
}

// TestDiffPagerScroll: j/k step a row, pgup/pgdn a page, G the end and g the
// top, and the offset never leaves the lines.
func TestDiffPagerScroll(t *testing.T) {
	tui, got := switcherTUI(t)
	req := askDiff(t, tui, got)
	tui.apply(wire.Msg{Type: "resp", Action: "diff", ID: req.ID, OK: true, Lines: diffLines(100)})
	if tui.diff == nil {
		t.Fatal("the reply did not open the pager")
	}
	h := tui.diff.float.Rect.H
	if h < 3 {
		t.Fatalf("pager body is %d rows, want a scrollable page", h)
	}
	feed(tui, "j")
	if n := tui.diff.Offset(); n != 1 {
		t.Fatalf("j scrolled to %d, want 1", n)
	}
	feed(tui, "k")
	if n := tui.diff.Offset(); n != 0 {
		t.Fatalf("k scrolled to %d, want 0", n)
	}
	feed(tui, "\x1b[6~") // pgdn
	if n := tui.diff.Offset(); n != h {
		t.Fatalf("pgdn scrolled to %d, want one page %d", n, h)
	}
	feed(tui, "\x1b[5~") // pgup
	if n := tui.diff.Offset(); n != 0 {
		t.Fatalf("pgup scrolled to %d, want 0", n)
	}
	feed(tui, "G")
	if n := tui.diff.Offset(); n != len(tui.diff.lines)-h {
		t.Fatalf("G scrolled to %d, want the last page %d", n, len(tui.diff.lines)-h)
	}
	feed(tui, "g")
	if n := tui.diff.Offset(); n != 0 {
		t.Fatalf("g scrolled to %d, want the top", n)
	}
	// Nothing reached a pane.
	testutil.NoMsg(t, got)
}

// TestDiffPagerCloses: q and esc both close the pager and give the keys back.
func TestDiffPagerCloses(t *testing.T) {
	for _, key := range []string{"q", "\x1b"} {
		tui, got := switcherTUI(t)
		req := askDiff(t, tui, got)
		tui.apply(wire.Msg{Type: "resp", Action: "diff", ID: req.ID, OK: true, Lines: diffLines(10)})
		if tui.diff == nil {
			t.Fatalf("%q: the reply did not open the pager", key)
		}
		feed(tui, key)
		if tui.pending {
			// A lone esc is the head of every arrow binding, so it waits for
			// the pending timer to prove it never finished arriving.
			tui.escAt = time.Now().Add(-time.Second)
			tui.expireMode(time.Now())
		}
		if tui.diff != nil || tui.mode != "" {
			t.Fatalf("%q left diff %v and mode %q", key, tui.diff, tui.mode)
		}
		if len(tui.floats) != 0 {
			t.Fatalf("%q left %d floats behind", key, len(tui.floats))
		}
		testutil.NoMsg(t, got)
	}
}

// TestDiffPagerDraws: a drawn frame carries the border title, the hint row and
// the first visible line.
func TestDiffPagerDraws(t *testing.T) {
	tui, got := switcherTUI(t)
	req := askDiff(t, tui, got)
	tui.apply(wire.Msg{Type: "resp", Action: "diff", ID: req.ID, OK: true, Lines: diffLines(100)})
	if tui.diff == nil {
		t.Fatal("the reply did not open the pager")
	}
	out := tui.out.(*bytes.Buffer)
	out.Reset()
	tui.draw()
	frame := out.String()
	for _, want := range []string{"diff w1p1", "j/k scroll", "+line 0"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("the frame does not carry %q", want)
		}
	}
	// A scrolled page starts at the offset row.
	feed(tui, "j")
	out.Reset()
	tui.draw()
	if !strings.Contains(out.String(), "+line 1") {
		t.Fatal("a scrolled frame does not show the second row")
	}
}
