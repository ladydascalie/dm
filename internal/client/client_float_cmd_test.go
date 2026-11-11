package client

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"dm/internal/testutil"
	"dm/internal/wire"
)

// askFloat opens the dashboard over the two-pane fixture and presses d, so the
// client sends a float-open request for the selected pane. It returns the
// request.
func askFloat(t *testing.T, tui *tui, got chan wire.Msg) wire.Msg {
	t.Helper()
	feed(tui, "\x02a")
	feed(tui, "d")
	if tui.sw != nil {
		t.Fatal("d left the dashboard open")
	}
	select {
	case m := <-got:
		if m.Type != "req" || m.Action != "float-open" || m.Target != "w1p1" {
			t.Fatalf("d sent %+v, want a float-open request for w1p1", m)
		}
		return m
	case <-time.After(time.Second):
		t.Fatal("d sent no float-open request")
	}
	return wire.Msg{}
}

// floatReply is the server's answer to a float-open request.
func floatReply(t *testing.T, req wire.Msg, command string) wire.Msg {
	t.Helper()
	res, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	return wire.Msg{
		Type: "resp", Action: "float-open", ID: req.ID, Float: req.Float,
		OK: true, Res: res,
	}
}

// openFloat opens the float and proves it came up, for the tests that need one.
func openFloat(t *testing.T, tui *tui, got chan wire.Msg, command string) wire.Msg {
	t.Helper()
	req := askFloat(t, tui, got)
	tui.apply(floatReply(t, req, command))
	if tui.float == nil || tui.mode != "float" {
		t.Fatalf("the reply left float %v and mode %q", tui.float, tui.mode)
	}
	return req
}

// TestFloatKeyRequests: d closes the dashboard and asks for the command on the
// selected pane's folder, at the box's inner size, under an id the client
// picked.
func TestFloatKeyRequests(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	r := floatRect(tui.snap)
	req := askFloat(t, tui, got)
	if req.Float == "" {
		t.Fatal("the request carries no float id")
	}
	if req.ID <= 0 {
		t.Fatalf("request id = %d, want a positive tag", req.ID)
	}
	if req.Cols != r.W || req.Rows != r.H {
		t.Fatalf("request size = %dx%d, want the box's inner %dx%d", req.Cols, req.Rows, r.W, r.H)
	}
	if tui.float == nil || tui.float.vt == nil {
		t.Fatal("the client did not build the terminal before sending")
	}
	if c, rw := tui.float.vt.Grid().Size(); c != r.W || rw != r.H {
		t.Fatalf("terminal = %dx%d, want %dx%d", c, rw, r.W, r.H)
	}
	testutil.NoMsg(t, got)
}

// TestFloatReplyOpens: the reply puts the float on screen in mode "float" with
// the command in its title, and a reply to an older request opens nothing.
func TestFloatReplyOpens(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	req := askFloat(t, tui, got)

	// A reply to an older request is dropped: the pending float keeps waiting
	// for its own, and nothing lands on the screen.
	tui.apply(floatReply(t, wire.Msg{Type: "resp", Action: "float-open", ID: req.ID + 1, Float: req.Float}, "git diff"))
	if tui.mode != "float" || len(tui.floats) != 0 || tui.float.opened {
		t.Fatalf("a stale reply left mode %q, %d floats and opened %v", tui.mode, len(tui.floats), tui.float.opened)
	}

	tui.apply(floatReply(t, req, "git diff --stat"))
	if tui.float == nil || tui.mode != "float" {
		t.Fatalf("the reply left float %v and mode %q", tui.float, tui.mode)
	}
	if len(tui.floats) != 1 {
		t.Fatalf("floats = %d, want the one float", len(tui.floats))
	}
	if got := tui.float.float.Title; !strings.Contains(got, "git diff --stat") {
		t.Fatalf("title = %q, want the command", got)
	}
	if got := tui.float.float.Hint; !strings.Contains(got, "ctrl+b kills the float") {
		t.Fatalf("hint = %q, want the kill key", got)
	}
}

// TestFloatBytesPaint: the child's bytes land in the float's own terminal, and
// no other target touches it.
func TestFloatBytesPaint(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	req := openFloat(t, tui, got, "git diff")
	tui.apply(wire.Msg{Type: "bytes", Target: req.Float, Data: []byte("+added line")})
	tui.flushIdle(time.Now().Add(idleFlush))
	if out := tui.float.vt.Grid().Text(0); !strings.Contains(out, "+added line") {
		t.Fatalf("row 0 = %q, want the child's output", out)
	}
	tui.apply(wire.Msg{Type: "bytes", Target: "w1p1", Data: []byte("pane bytes")})
	if out := tui.float.vt.Grid().Text(1); strings.Contains(out, "pane bytes") {
		t.Fatal("a pane's bytes landed in the float")
	}
}

// TestFloatForwardsKeys: a printable key and enter go to the float target as
// raw bytes, and the client keeps the mode.
func TestFloatForwardsKeys(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	req := openFloat(t, tui, got, "cat")
	feed(tui, "a")
	m := <-got
	if m.Type != "keys" || m.Target != req.Float || string(m.Keys) != "a" {
		t.Fatalf("a sent %+v, want the key to the float", m)
	}
	feed(tui, "\r")
	m = <-got
	if m.Type != "keys" || m.Target != req.Float || string(m.Keys) != "\r" {
		t.Fatalf("enter sent %+v, want the key to the float", m)
	}
	if tui.mode != "float" {
		t.Fatalf("mode = %q, want the float still up", tui.mode)
	}
	testutil.NoMsg(t, got)
}

// TestFloatPrefixCloses: the prefix alone kills the float and the command
// under it in one press, with no chord and no clock, and the command never sees
// the prefix byte, so a prefix followed by any key kills it as well.
func TestFloatPrefixCloses(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	req := openFloat(t, tui, got, "cat")

	feed(tui, "\x02")
	m := <-got
	if m.Type != "req" || m.Action != "float-close" || m.Float != req.Float {
		t.Fatalf("the prefix sent %+v, want a float-close for the float", m)
	}
	if tui.float != nil || tui.mode != "" || len(tui.floats) != 0 {
		t.Fatalf("the prefix left float %v, mode %q and %d floats", tui.float, tui.mode, len(tui.floats))
	}
	if tui.pending {
		t.Fatal("the prefix armed the escape timer")
	}
	testutil.NoMsg(t, got)

	// A prefix followed by another key kills it too: the command gets none of
	// the bytes that carry the prefix.
	tui2, got2 := switcherTUI(t)
	req2 := openFloat(t, tui2, got2, "cat")
	feed(tui2, "\x02k")
	m = <-got2
	if m.Type != "req" || m.Action != "float-close" || m.Float != req2.Float {
		t.Fatalf("prefix k sent %+v, want a float-close", m)
	}
	if tui2.float != nil || tui2.mode != "" {
		t.Fatalf("prefix k left float %v and mode %q", tui2.float, tui2.mode)
	}
}

// TestFloatExitKeepsScreen: the exit frame keeps the last screen and says so;
// the next key closes the float and forwards nothing.
func TestFloatExitKeepsScreen(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	req := openFloat(t, tui, got, "echo hi")
	tui.apply(wire.Msg{Type: "bytes", Target: req.Float, Data: []byte("hi")})
	tui.flushIdle(time.Now().Add(idleFlush))
	tui.apply(wire.Msg{Type: "exit", Target: req.Float})
	if tui.float == nil || !tui.float.exited {
		t.Fatalf("the exit frame left float %v", tui.float)
	}
	if got := tui.float.float.Title; !strings.Contains(got, "exited") {
		t.Fatalf("title = %q, want the exited note", got)
	}
	if got := tui.float.float.Hint; !strings.Contains(got, "any key closes") {
		t.Fatalf("hint = %q, want the any key note", got)
	}
	if out := tui.float.vt.Grid().Text(0); !strings.Contains(out, "hi") {
		t.Fatalf("row 0 = %q, want the last screen", out)
	}
	feed(tui, "x")
	if tui.float != nil || tui.mode != "" || len(tui.floats) != 0 {
		t.Fatalf("a key after the exit left float %v and mode %q", tui.float, tui.mode)
	}
	// Nothing went to the child, and an exited float is not closed again.
	testutil.NoMsg(t, got)
}

// TestFloatRefusedReply: a refusal toasts and opens nothing.
func TestFloatRefusedReply(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	req := askFloat(t, tui, got)
	tui.apply(wire.Msg{
		Type: "resp", Action: "float-open", ID: req.ID, Float: req.Float,
		Error: "pane w1p1 has no working directory",
	})
	if tui.float != nil || tui.mode != "" || len(tui.floats) != 0 {
		t.Fatalf("the refusal left float %v, mode %q and %d floats", tui.float, tui.mode, len(tui.floats))
	}
	texts := toastTexts(tui)
	if len(texts) == 0 || !strings.Contains(texts[len(texts)-1], "no working directory") {
		t.Fatalf("toasts = %q, want the server's reason", texts)
	}
}

// TestFloatResizeFollowsScreen: a snapshot that changes the float's rect resizes
// the terminal and tells the server.
func TestFloatResizeFollowsScreen(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	req := openFloat(t, tui, got, "git diff")
	before := tui.float.float.Rect

	big := *tui.snap
	big.Cols += 20
	big.Rows += 10
	tui.apply(wire.Msg{Type: "snapshot", Snap: &big})
	after := floatRect(&big)
	if tui.float.float.Rect != after {
		t.Fatalf("float rect = %+v, want %+v", tui.float.float.Rect, after)
	}
	if c, r := tui.float.vt.Grid().Size(); c != after.W || r != after.H {
		t.Fatalf("terminal = %dx%d, want %dx%d", c, r, after.W, after.H)
	}
	select {
	case m := <-got:
		if m.Type != "resize" || m.Target != req.Float || m.Cols != after.W || m.Rows != after.H {
			t.Fatalf("snapshot sent %+v, want a resize for the float at %dx%d", m, after.W, after.H)
		}
	case <-time.After(time.Second):
		t.Fatal("a changed rect sent no resize")
	}
	testutil.NoMsg(t, got)

	// The same picture again sends nothing.
	tui.apply(wire.Msg{Type: "snapshot", Snap: &big})
	testutil.NoMsg(t, got)
	if before == after {
		t.Fatal("the test's snapshot did not change the rect")
	}
}

// TestFloatPendingHoldsKeys: keys typed while the open reply is in flight go to
// neither the pane nor the child, and the reply then lets them through.
func TestFloatPendingHoldsKeys(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	req := askFloat(t, tui, got)
	if tui.mode != "float" {
		t.Fatalf("mode = %q while the reply is in flight, want float", tui.mode)
	}
	feed(tui, "x")
	testutil.NoMsg(t, got)
	if tui.mode != "float" {
		t.Fatalf("a held key left mode %q", tui.mode)
	}
	tui.apply(floatReply(t, req, "cat"))
	feed(tui, "y")
	m := <-got
	if m.Type != "keys" || m.Target != req.Float || string(m.Keys) != "y" {
		t.Fatalf("after the reply y sent %+v, want the key to the float", m)
	}
}

// TestFloatExitBeatsReply: an exit frame that lands before the open reply still
// ends in a float that says the command exited, and any key closes it.
func TestFloatExitBeatsReply(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	req := askFloat(t, tui, got)
	tui.apply(wire.Msg{Type: "exit", Target: req.Float})
	if tui.float == nil || !tui.float.exited {
		t.Fatalf("the early exit left float %v", tui.float)
	}
	tui.apply(floatReply(t, req, "git diff"))
	if tui.mode != "float" || len(tui.floats) != 1 {
		t.Fatalf("the reply left mode %q and %d floats", tui.mode, len(tui.floats))
	}
	if got := tui.float.float.Title; !strings.Contains(got, "exited") {
		t.Fatalf("title = %q, want the exited note", got)
	}
	feed(tui, "x")
	if tui.float != nil || tui.mode != "" || len(tui.floats) != 0 {
		t.Fatalf("a key after the early exit left float %v and mode %q", tui.float, tui.mode)
	}
	testutil.NoMsg(t, got)
}

// TestFloatClosedFloatSwallowsExit: the exit frame that follows a client close
// does not come back as a pane toast naming the float.
func TestFloatClosedFloatSwallowsExit(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	req := openFloat(t, tui, got, "sleep 30")
	feed(tui, "\x02")
	m := <-got
	if m.Type != "req" || m.Action != "float-close" || m.Float != req.Float {
		t.Fatalf("the prefix sent %+v, want a float-close", m)
	}
	testutil.NoMsg(t, got)
	tui.apply(wire.Msg{Type: "exit", Target: req.Float})
	if texts := toastTexts(tui); len(texts) != 0 {
		t.Fatalf("the exit came back as toasts %q, want none", texts)
	}
}

// TestFloatErrorRacingExitIsSwallowed: an error reply that names the float is a
// key that raced the child's exit, not pane news, both while the float is up
// and after the client closed it. A pane error still toasts.
func TestFloatErrorRacingExitIsSwallowed(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	req := openFloat(t, tui, got, "sleep 30")
	tui.apply(wire.Msg{Type: "resp", Action: "keys", Error: `no pane "` + req.Float + `"`})
	if texts := toastTexts(tui); len(texts) != 0 {
		t.Fatalf("a racing key toasted %q, want nothing", texts)
	}
	feed(tui, "\x02")
	<-got
	tui.apply(wire.Msg{Type: "resp", Action: "keys", Error: `no pane "` + req.Float + `"`})
	if texts := toastTexts(tui); len(texts) != 0 {
		t.Fatalf("a key after the close toasted %q, want nothing", texts)
	}
	tui.apply(wire.Msg{Type: "resp", Action: "keys", Error: `no pane "w1p1"`})
	texts := toastTexts(tui)
	if len(texts) == 0 || !strings.Contains(texts[len(texts)-1], "w1p1") {
		t.Fatalf("toasts = %q, want the pane error", texts)
	}
}
