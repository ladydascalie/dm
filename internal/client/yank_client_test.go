package client

import (
	"encoding/json"
	"strings"
	"testing"

	"dm/internal/keymap"
	"dm/internal/testutil"
	"dm/internal/wire"
)

// The default main keymap binds both copy actions.
func TestCopyKeysAreBound(t *testing.T) {
	cfg := keymap.LoadDefault()
	if got := cfg.Key("main", "copy-output"); got != "y" {
		t.Fatalf("copy-output key = %q, want y", got)
	}
	if got := cfg.Key("main", "copy-screen"); got != "Y" {
		t.Fatalf("copy-screen key = %q, want Y", got)
	}
}

// The y key reaches copy-output and sends a yank request for the focused pane.
func TestCopyOutputKeySendsYank(t *testing.T) {
	tui, got := clientTUI(t, nil)
	tui.armed = true
	tui.key('y')
	m := testutil.NextMsg(t, got)
	if m.Type != "req" || m.Action != "yank" || m.Target != "a" {
		t.Fatalf("y sent %+v, want a yank request for a", m)
	}
}

// The Y key reaches copy-screen and writes the visible screen to the
// clipboard.
func TestCopyScreenKeyCopiesTheScreen(t *testing.T) {
	tui, _ := clientTUI(t, nil)
	tui.vts["a"] = testutil.TestVT(t, 20, 3, "one\r\ntwo\r\n")
	tui.armed = true
	out := testutil.CaptureStdout(t, func() { tui.key('Y') })
	want := "one\ntwo"
	if !strings.Contains(out, osc52(want)) {
		t.Fatalf("clipboard = %q, want %q", out, want)
	}
	if len(tui.toasts) != 1 || tui.toasts[0].text != "copied 7 chars" {
		t.Fatalf("toasts = %+v, want copied 7 chars", tui.toasts)
	}
}

// A yank reply writes the rows the server served, not the live screen.
func TestCopyOutputWritesTheServedRows(t *testing.T) {
	tui, got := clientTUI(t, nil)
	tui.vts["a"] = testutil.TestVT(t, 20, 3, "live screen")
	tui.runAction("copy-output")
	m := testutil.NextMsg(t, got)
	res, _ := json.Marshal(map[string]any{
		"lines": []string{"old", "rows", "wiped", "after", "the", "enter", ""},
		"start": 3, "rows": 7, "anchor": true,
	})
	out := testutil.CaptureStdout(t, func() {
		tui.apply(wire.Msg{Type: "resp", Action: "yank", ID: m.ID, OK: true, Res: res})
	})
	want := "after\nthe\nenter"
	if !strings.Contains(out, osc52(want)) {
		t.Fatalf("clipboard = %q, want %q", out, want)
	}
	if len(tui.toasts) != 1 || tui.toasts[0].text != "copied 15 chars" {
		t.Fatalf("toasts = %+v, want copied 15 chars", tui.toasts)
	}
}

// A pane with no Enter has no anchor, so copy-output falls back to the visible
// screen and the toast says so.
func TestCopyOutputWithoutAnchorFallsBackToScreen(t *testing.T) {
	tui, got := clientTUI(t, nil)
	tui.vts["a"] = testutil.TestVT(t, 20, 3, "one\r\ntwo\r\n")
	tui.runAction("copy-output")
	m := testutil.NextMsg(t, got)
	res, _ := json.Marshal(map[string]any{
		"lines": []string{}, "start": 0, "rows": 3, "anchor": false,
	})
	out := testutil.CaptureStdout(t, func() {
		tui.apply(wire.Msg{Type: "resp", Action: "yank", ID: m.ID, OK: true, Res: res})
	})
	want := "one\ntwo"
	if !strings.Contains(out, osc52(want)) {
		t.Fatalf("clipboard = %q, want the screen %q", out, want)
	}
	if len(tui.toasts) != 1 || !strings.Contains(tui.toasts[0].text, "no Enter yet") {
		t.Fatalf("toasts = %+v, want the no-Enter fallback", tui.toasts)
	}
}

// The i key copies the focused pane's id, the handle every other dm surface
// names a pane by.
func TestCopyPaneIDKeyCopiesTheFocus(t *testing.T) {
	tui, _ := clientTUI(t, nil)
	tui.armed = true
	out := testutil.CaptureStdout(t, func() { tui.key('i') })
	if !strings.Contains(out, osc52("a")) {
		t.Fatalf("clipboard = %q, want the pane id a", out)
	}
	if len(tui.toasts) != 1 || tui.toasts[0].text != "copied a" {
		t.Fatalf("toasts = %+v, want copied a", tui.toasts)
	}
}
