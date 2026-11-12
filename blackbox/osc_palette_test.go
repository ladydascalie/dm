package blackbox

import (
	"strings"
	"testing"
	"time"
)

// TestPaletteReplyCarriesDetectedBackground proves the whole colour chain
// end to end: the client's term-colours pair reaches the pane, and a program
// inside the pane that queries OSC 11 gets the detected background back in
// the xterm rgb:RRRR/GGGG/BBBB form.
//
// The probe runs in the pane's shell. It puts the tty in raw mode, asks for
// the background, and reads exactly the reply's length with dd: the reply is
// ESC ] 11 ; <colour> ESC \, 25 bytes for a four-digit rgb triple. A first
// query is read and discarded so stray startup output cannot shift the reply
// the assertion reads. The escapes are stripped before echoing, so the screen
// carries the framing and the value as plain text.
func TestPaletteReplyCarriesDetectedBackground(t *testing.T) {
	h := newHarness(t)
	s := "osccolor"
	h.run("new", "-s", s)
	pane := h.focus(s)
	h.waitPanes(s, 1)

	// The detected pair, in the xterm form the server normalises to.
	res := h.rpc(s, map[string]any{
		"action": "term-colours",
		"fg":     "rgb:1111/2222/3333",
		"bg":     "rgb:4444/5555/6666",
	})
	if res["error"] != nil {
		t.Fatalf("term-colours: %v", res["error"])
	}
	// The fan-out lands on the pane's socket in microseconds; a beat keeps
	// the probe's query behind it.
	time.Sleep(300 * time.Millisecond)

	probe := `stty raw -echo; printf '\033]11;?\033\\'; dd bs=1 count=25 2>/dev/null >/dev/null; printf '\033]11;?\033\\'; bg=$(dd bs=1 count=25 2>/dev/null | tr -d '\033\\'); stty sane; echo "PROBE-BG=$bg"`
	h.run("write-to", pane, "-s", s, "--msg", probe, "--enter")
	out := h.waitFor(s, pane, "PROBE-BG=]11;rgb:4444/5555/6666")

	// The rgb: prefix and the value sit inside the OSC framing on screen, so
	// the match can only be the reply: the typed command never spells the
	// value.
	if !strings.Contains(out, "]11;rgb:4444/5555/6666") {
		t.Fatalf("reply framing lost: %q", out)
	}
}
