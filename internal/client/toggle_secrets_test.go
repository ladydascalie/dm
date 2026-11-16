package client

import (
	"testing"

	"dm/internal/keymap"
	"dm/internal/wire"
)

// TestKeymapDispatchToggleSecrets proves the built-in binding sends the
// toggle request for the focused pane with a request id, and that the armed
// state clears after the key runs. The default binds ctrl+g, a key the shell
// and the pane traffic never carry in normal flow.
func TestKeymapDispatchToggleSecrets(t *testing.T) {
	t.Parallel()
	action, n, state := keymap.LoadDefault().Match("main", []byte{0x07})
	if state != keymap.Match || action != "toggle-secrets" || n != 1 {
		t.Fatalf("Match(main, ctrl+g) = %q, %d, %v; want toggle-secrets, 1, Match", action, n, state)
	}

	tui, got := clientTUI(t, nil)
	tui.arm()
	feed(tui, "\x07")
	m := <-got
	if m.Action != "toggle-secrets" || m.Target != "a" || m.ID == 0 {
		t.Fatalf("toggle-secrets sent %+v, want the focused pane and a request id", m)
	}
	if tui.armed {
		t.Fatal("toggle-secrets left the keymap armed")
	}
}

// TestTakeToggleToastsState proves the reply toast names the pane and the
// state it landed on: an unprotected pane must never read as protected. A
// failure toasts the error instead.
func TestTakeToggleToastsState(t *testing.T) {
	t.Parallel()
	tui, _ := clientTUI(t, nil)

	tui.takeToggle(wire.Msg{OK: true, Res: []byte(`{"pane":"w1p1","enabled":false}`)})
	if last := tui.toasts[len(tui.toasts)-1].text; last != "redaction off: w1p1" {
		t.Fatalf("off toast = %q, want the pane and its real state", last)
	}
	tui.takeToggle(wire.Msg{OK: true, Res: []byte(`{"pane":"w1p1","enabled":true}`)})
	if last := tui.toasts[len(tui.toasts)-1].text; last != "redaction on: w1p1" {
		t.Fatalf("on toast = %q, want the pane and its real state", last)
	}
	tui.takeToggle(wire.Msg{OK: false, Error: "no pane"})
	if last := tui.toasts[len(tui.toasts)-1].text; last != "no pane" {
		t.Fatalf("failure toast = %q, want the error", last)
	}
}
