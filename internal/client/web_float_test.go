package client

// The web URL float, driven through the client: the key that opens it, the two
// answers it paints, its three ways out and the way a resize refits it.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"dm/internal/layout"
	"dm/internal/testutil"
	"dm/internal/vt"
	"dm/internal/wire"
)

// webSnap is a one-pane session at the size a test asks for, so a narrow screen
// can prove the box drops the QR instead of painting half of one.
func webSnap(cols, rows int) *wire.Snapshot {
	snap := testutil.TestSnapshot(&layout.Node{Pane: "a"}, cols, rows, nil)
	snap.Focus = "a"
	return snap
}

// webTUI is a client on a blank session of the size the test names.
func webTUI(t *testing.T, cols, rows int) (*tui, chan wire.Msg) {
	t.Helper()
	tui, got := clientTUI(t, nil)
	tui.snap = webSnap(cols, rows)
	tui.out = &bytes.Buffer{}
	return tui, got
}

// openWeb presses the default web-url key and returns the request id the box
// sent. It fails the test when the key sends anything else.
func openWeb(t *testing.T, tui *tui, got chan wire.Msg) int {
	t.Helper()
	feed(tui, "\x02U")
	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "web-info" || m.ID == 0 {
		t.Fatalf("the key sent %+v, want one web-info request", m)
	}
	if tui.web == nil {
		t.Fatal("the key opened no web box")
	}
	if m.ID != tui.web.id {
		t.Fatalf("request id = %d, box waits for %d", m.ID, tui.web.id)
	}
	return m.ID
}

// webAnswer delivers the daemon's reply, the way the socket delivers it.
func webAnswer(tui *tui, id int, body string) {
	tui.apply(wire.Msg{Type: "resp", Action: "web-info", ID: id, OK: true, Res: json.RawMessage(body)})
}

// webScreen replays one frame into a fresh terminal, so a test reads what the
// box painted and where.
func webScreen(tui *tui) *vt.Term {
	term := vt.NewTerm(tui.snap.Cols, tui.snap.Rows)
	buf := tui.out.(*bytes.Buffer)
	buf.Reset()
	tui.draw()
	term.Write(buf.Bytes())
	term.Flush()
	return term
}

// webRowText reads width cells of a screen row as text, zero based.
func webRowText(term *vt.Term, y, x, width int) string {
	var b strings.Builder
	for i := 0; i < width; i++ {
		c := termCell(term, x+i, y)
		if c.Char == 0 {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(c.Char)
	}
	return strings.TrimRight(b.String(), " ")
}

// webQRRows counts the interior rows that carry QR art: any row holding a cell
// on the code's black or bright white module ground. The quiet zone is all
// light, so a row of one colour alone still counts; the blank row between the
// link and the code, and the box ground, are neither.
func webQRRows(term *vt.Term, r layout.Rect) int {
	n := 0
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			switch termCell(term, x, y).BG {
			case vt.PaletteColor(0), vt.PaletteColor(15):
				n++
				x = r.X + r.W
			}
		}
	}
	return n
}

// TestWebURLKeyOpensTheFloat proves the key opens one temporary float, sends
// one web-info request, and leaves the focus and the mode alone.
func TestWebURLKeyOpensTheFloat(t *testing.T) {
	t.Parallel()
	tui, got := webTUI(t, 100, 30)
	openWeb(t, tui, got)
	if len(tui.floats) != 1 || tui.floats[0].ID != webFloatID {
		t.Fatalf("floats = %+v, want the one web box", tui.floats)
	}
	if tui.mode != "" {
		t.Fatalf("the box opened mode %q, want none", tui.mode)
	}
	// The box fits the screen and stands inside it.
	r := tui.web.float.Rect
	if r.X < 1 || r.Y < 1 || r.X+r.W > tui.snap.Cols || r.Y+r.H > tui.snap.Rows-1 {
		t.Fatalf("box rect = %+v on a %dx%d screen", r, tui.snap.Cols, tui.snap.Rows)
	}
	testutil.NoMsg(t, got)
}

// TestWebInfoReplyPaintsURLAndQR proves a reply with a URL paints the link on
// the box's first row and every row of the QR art under it. 120x50 is tall
// enough for the whole code, so the count is exact: a clipped code would fail
// here, not pass a floor.
func TestWebInfoReplyPaintsURLAndQR(t *testing.T) {
	t.Parallel()
	tui, got := webTUI(t, 120, 50)
	id := openWeb(t, tui, got)
	const url = "http://127.0.0.1:8099/?t=c33beba64055b013"
	webAnswer(tui, id, `{"auto":true,"url":"`+url+`","addr":"127.0.0.1:8099"}`)

	term := webScreen(tui)
	r := tui.web.float.Rect
	if got := webRowText(term, r.Y, r.X, r.W); got != url {
		t.Fatalf("first row = %q, want the URL %q", got, url)
	}
	want := tui.web.content.qrH
	if want == 0 {
		t.Fatal("the reply has no QR to paint")
	}
	if n := webQRRows(term, r); n != want {
		t.Fatalf("the box painted %d of %d QR rows, want the whole code", n, want)
	}
	if !tui.web.qrOn {
		t.Fatal("the box reports the code off on a screen that holds it")
	}
	if r.X+r.W > tui.snap.Cols || r.Y+r.H > tui.snap.Rows-1 {
		t.Fatalf("box rect = %+v runs off the screen", r)
	}
}

// TestWebInfoOffPaintsTheReason proves an auto-off reply names the node and the
// command that starts the listener by hand.
func TestWebInfoOffPaintsTheReason(t *testing.T) {
	t.Parallel()
	tui, got := webTUI(t, 100, 30)
	id := openWeb(t, tui, got)
	webAnswer(tui, id, `{"auto":false,"url":"","addr":"","error":""}`)

	screen := screenString(webScreen(tui))
	for _, want := range []string{"web-auto", "dm web"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("the box does not name %q: %q", want, screen)
		}
	}
}

// TestWebInfoBindErrorPaints proves a bind failure paints the daemon's error
// text rather than an empty link.
func TestWebInfoBindErrorPaints(t *testing.T) {
	t.Parallel()
	tui, got := webTUI(t, 100, 30)
	id := openWeb(t, tui, got)
	webAnswer(tui, id, `{"auto":true,"url":"","addr":"","error":"listen tcp 127.0.0.1:8099: bind: address already in use"}`)

	// The error wraps at the box's own width, so the test reads its words, not
	// one unbroken line.
	screen := screenString(webScreen(tui))
	for _, want := range []string{"could not start", "listen tcp", "address already", "in use"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("the box does not paint %q of the bind failure: %q", want, screen)
		}
	}
}

// TestWebFloatEscCloses proves esc closes the box and goes no further: no byte
// reaches a pane.
func TestWebFloatEscCloses(t *testing.T) {
	t.Parallel()
	tui, got := webTUI(t, 100, 30)
	openWeb(t, tui, got)
	feed(tui, "\x1b")
	if tui.web != nil || len(tui.floats) != 0 {
		t.Fatalf("esc left web = %+v floats = %+v", tui.web, tui.floats)
	}
	testutil.NoMsg(t, got)
}

// TestWebFloatQCloses proves q closes the box, the second key every float uses.
func TestWebFloatQCloses(t *testing.T) {
	t.Parallel()
	tui, got := webTUI(t, 100, 30)
	openWeb(t, tui, got)
	feed(tui, "q")
	if tui.web != nil || len(tui.floats) != 0 {
		t.Fatalf("q left web = %+v floats = %+v", tui.web, tui.floats)
	}
	testutil.NoMsg(t, got)
}

// TestWebURLKeyCloses proves the action key toggles the box: a second press
// takes it down and sends no second request.
func TestWebURLKeyCloses(t *testing.T) {
	t.Parallel()
	tui, got := webTUI(t, 100, 30)
	openWeb(t, tui, got)
	feed(tui, "\x02U")
	if tui.web != nil || len(tui.floats) != 0 {
		t.Fatalf("second press left web = %+v floats = %+v", tui.web, tui.floats)
	}
	testutil.NoMsg(t, got)
}

// TestWebFloatDropsTheQRInsteadOfClippingIt proves the whole-or-nothing rule:
// a screen too short or too narrow for every row of the code paints none of it
// and keeps the link. 100x30 and 80x24 are the sizes the defect clipped; 40x20
// is the width rule.
func TestWebFloatDropsTheQRInsteadOfClippingIt(t *testing.T) {
	t.Parallel()
	const url = "http://127.0.0.1:8099/?t=c33beba64055b013"
	for _, size := range []struct{ cols, rows int }{{100, 30}, {80, 24}, {40, 20}} {
		t.Run(fmt.Sprintf("%dx%d", size.cols, size.rows), func(t *testing.T) {
			t.Parallel()
			tui, got := webTUI(t, size.cols, size.rows)
			id := openWeb(t, tui, got)
			webAnswer(tui, id, `{"auto":true,"url":"`+url+`","addr":"127.0.0.1:8099"}`)

			if tui.web.content.qrH == 0 {
				t.Fatal("the reply has no QR, so the test proves nothing")
			}
			if tui.web.qrOn {
				t.Fatalf("the box kept the code on a %dx%d screen it does not fit", size.cols, size.rows)
			}
			term := webScreen(tui)
			r := tui.web.float.Rect
			if r.X < 1 || r.Y < 1 || r.X+r.W > size.cols || r.Y+r.H > size.rows-1 {
				t.Fatalf("box rect = %+v on a %dx%d screen", r, size.cols, size.rows)
			}
			if n := webQRRows(term, r); n != 0 {
				t.Fatalf("the box painted %d QR rows, want none", n)
			}
			// The link is what the box keeps: a narrow box elides it, it never
			// wraps, and it is never painted over by a code.
			if got := webRowText(term, r.Y, r.X, r.W); !strings.HasPrefix(got, "http://127.0.0.1:8099/") {
				t.Fatalf("first row = %q, want the start of the URL", got)
			}
		})
	}
}

// TestWebFloatRefitsOnResize proves the box follows the terminal: a frame on a
// shorter screen keeps the box inside it.
func TestWebFloatRefitsOnResize(t *testing.T) {
	t.Parallel()
	tui, got := webTUI(t, 100, 30)
	id := openWeb(t, tui, got)
	webAnswer(tui, id, `{"auto":true,"url":"http://127.0.0.1:8099/?t=c33beba64055b013","addr":"127.0.0.1:8099"}`)
	before := tui.web.float.Rect
	webScreen(tui)

	tui.snap = webSnap(60, 14)
	webScreen(tui)
	r := tui.web.float.Rect
	if r == before {
		t.Fatalf("box rect %+v did not follow the resize", r)
	}
	if r.X < 1 || r.Y < 1 || r.X+r.W > 60 || r.Y+r.H > 13 {
		t.Fatalf("box rect = %+v on a 60x14 screen", r)
	}
}

// TestWebFloatStandsDownForADeskSurface proves an overlay that owns the desk
// closes the box, so two floats never fight for the frame.
func TestWebFloatStandsDownForADeskSurface(t *testing.T) {
	t.Parallel()
	tui, got := webTUI(t, 100, 30)
	openWeb(t, tui, got)
	tui.takeDesk()
	if tui.web != nil || len(tui.floats) != 0 {
		t.Fatalf("takeDesk left web = %+v floats = %+v", tui.web, tui.floats)
	}
}

// TestWebFloatCloseKeepsTypedKeys proves a close that is not a swallowed key
// leaves the input buffer alone: the desk and a snapshot close the box, and the
// operator's half-typed line must still reach the pane. The two close keys drop
// themselves.
func TestWebFloatCloseKeepsTypedKeys(t *testing.T) {
	t.Parallel()
	tui, got := webTUI(t, 100, 30)
	id := openWeb(t, tui, got)
	webAnswer(tui, id, `{"auto":false}`)
	tui.inbuf = []byte("ls -la")
	tui.pending = true
	tui.takeDesk()
	if tui.web != nil {
		t.Fatal("takeDesk left the web box up")
	}
	if string(tui.inbuf) != "ls -la" || !tui.pending {
		t.Fatalf("takeDesk dropped the buffer: %q pending=%v", tui.inbuf, tui.pending)
	}

	// Reopen and close with esc: the key that closes is consumed, not kept.
	tui.openWebFloat()
	_ = nextMsg(t, got)
	tui.inbuf = []byte{0x1b}
	tui.processInput()
	if tui.web != nil {
		t.Fatal("esc left the web box up")
	}
	if len(tui.inbuf) != 0 || tui.pending {
		t.Fatalf("esc left %q in the buffer, pending=%v", tui.inbuf, tui.pending)
	}
}

// TestWebFloatHintNamesTheBoundKey proves the border reads the keymap: a rebind
// of web-url labels itself, and the literal U appears nowhere.
func TestWebFloatHintNamesTheBoundKey(t *testing.T) {
	t.Parallel()
	tui, got := webTUI(t, 100, 30)
	tui.keymap = testutil.KeymapTestLoad(t, "prefix \"ctrl+a\"\nkeymap \"main\" { \"web-url\" \"W\" }")
	tui.openWebFloat()
	_ = nextMsg(t, got)
	if tui.web == nil || tui.web.float.Hint == nil {
		t.Fatal("the box opened no hint bar")
	}
	hints := tui.web.float.Hint.Hints
	last := hints[len(hints)-1]
	if len(last.Keys) != 1 || last.Keys[0] != "ctrl+a W" {
		t.Fatalf("the hint names %v, want the bound ctrl+a W", last.Keys)
	}
}
