package client

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"dm/internal/browser"
	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/rail"
	"dm/internal/transport"
	"dm/internal/vt"
	"dm/internal/wire"
)

var (
	pasteStart = []byte("\x1b[200~")
	pasteEnd   = []byte("\x1b[201~")
	oscFG      = []byte("\x1b]10;")
	oscBG      = []byte("\x1b]11;")
)

// takePaste reports whether the buffer starts with a bracketed paste. When the
// whole sequence is present it returns it and its length. When the start marker
// has only partly arrived it returns n == 0 so the caller keeps buffering. A
// lone escape counts as a partial marker: a read can split before the paste
// arrives, and the deadline decides whether it was a real escape.
func takePaste(buf []byte) (seq []byte, n int, started bool) {
	if len(buf) < len(pasteStart) {
		return nil, 0, bytes.HasPrefix(pasteStart, buf)
	}
	if !bytes.HasPrefix(buf, pasteStart) {
		return nil, 0, false
	}
	i := bytes.Index(buf[len(pasteStart):], pasteEnd)
	if i < 0 {
		return nil, 0, true
	}
	n = len(pasteStart) + i + len(pasteEnd)
	return buf[:n], n, true
}

func (t *tui) takePaste() (seq []byte, n int, started bool) {
	if t.pasteOff {
		return nil, 0, false
	}
	return takePaste(t.inbuf)
}

// holdPaste arms the paste deadline for a start marker that is still arriving,
// and slides it forward on every read that grows the held prefix.
// pasteDeadline is the expiry instant, not the arming instant. A lone escape
// waits escTimeout, because that is the dismiss key. A longer prefix already
// proves a marker is being assembled, so it waits pasteTimeout: a paste arrives
// in pieces on a slow link, and flushing between two pieces tore the block
// apart and lost the bracketed-paste framing. A buffer that stops growing still
// expires, which is how a real lone escape reaches the pane.
func (t *tui) holdPaste(now time.Time) {
	if len(t.inbuf) == t.pasteHeld && !t.pasteDeadline.IsZero() {
		return
	}
	t.pasteHeld = len(t.inbuf)
	wait := pasteTimeout
	if len(t.inbuf) <= 1 {
		wait = escTimeout
	}
	t.pasteDeadline = now.Add(wait)
}

// expirePaste flushes a start marker that never completed once its deadline
// passes. A real lone escape reaches the pane this way.
func (t *tui) expirePaste(now time.Time) bool {
	if t.pasteDeadline.IsZero() || now.Before(t.pasteDeadline) {
		return false
	}
	t.staleInput()
	return true
}

// staleInput handles bytes that looked like the start of a paste and then
// stalled. They take the normal input path once.
func (t *tui) staleInput() {
	t.pasteDeadline = time.Time{}
	t.pasteHeld = 0
	t.pasteOff = true
	t.processInput()
	t.pasteOff = false
}

// takeTermColour reports whether buf starts with an OSC 10 or OSC 11 reply.
// A complete reply returns its kind (10 or 11), its value and its length. A
// partial prefix returns started true and n 0, so the caller keeps buffering.
// A lone escape is left to the paste and prefix logic: it is not yet a reply.
func takeTermColour(buf []byte) (kind int, value string, n int, started bool) {
	if len(buf) < 2 || buf[0] != 0x1b || buf[1] != ']' {
		return 0, "", 0, false
	}
	var prefix []byte
	switch {
	case bytes.HasPrefix(buf, oscFG):
		prefix, kind = oscFG, 10
	case bytes.HasPrefix(buf, oscBG):
		prefix, kind = oscBG, 11
	case bytes.HasPrefix(oscFG, buf) || bytes.HasPrefix(oscBG, buf):
		return 0, "", 0, true
	default:
		return 0, "", 0, false
	}
	rest := buf[len(prefix):]
	st := bytes.Index(rest, []byte("\x1b\\"))
	bel := bytes.IndexByte(rest, 0x07)
	if bel >= 0 && (st < 0 || bel < st) {
		return kind, string(rest[:bel]), len(prefix) + bel + 1, true
	}
	if st < 0 {
		return 0, "", 0, true
	}
	return kind, string(rest[:st]), len(prefix) + st + 2, true
}

// termColourAt is where the first complete OSC 10 or OSC 11 prefix starts, or
// -1. It lets the bulk input path stop before a reply that shares a read with
// a keystroke, so the reply is handled at the head on the next pass.
func termColourAt(buf []byte) int {
	at := -1
	for _, p := range [][]byte{oscFG, oscBG} {
		if i := bytes.Index(buf, p); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	return at
}

// termColourTail is where a proper, non-empty prefix of oscFG or oscBG starts
// and runs to the end of buf, or -1. It lets the bulk path keep a reply that
// is still arriving after a keystroke instead of forwarding it to a pane.
func termColourTail(buf []byte) int {
	for i := 0; i < len(buf); i++ {
		if buf[i] != 0x1b {
			continue
		}
		tail := buf[i:]
		for _, p := range [][]byte{oscFG, oscBG} {
			if len(tail) < len(p) && bytes.HasPrefix(p, tail) {
				return i
			}
		}
	}
	return -1
}

// noteTermColour records a reply and sends the pair once both are known, or
// the one that arrived after a short grace so the two coalesce.
func (t *tui) noteTermColour(kind int, value string) {
	switch kind {
	case 10:
		t.termFG = value
	case 11:
		t.termBG = value
	}
	if t.termFG != "" && t.termBG != "" {
		t.sendTermColours()
		return
	}
	if t.termGrace.IsZero() {
		t.termGrace = time.Now()
	}
}

// sendTermColours sends the pair the client has, once per change. The server
// normalises the raw reply payloads.
func (t *tui) sendTermColours() {
	t.termGrace = time.Time{}
	if t.termFG == t.termSentFG && t.termBG == t.termSentBG {
		return
	}
	t.termSentFG, t.termSentBG = t.termFG, t.termBG
	if t.conn != nil {
		_ = t.conn.Send(wire.Msg{Type: "req", Action: "term-colours", FG: t.termFG, BG: t.termBG})
	}
}

// expireTermColour sends the one reply that arrived once the grace for the
// second passes, and flushes a partial reply that stalled as ordinary input,
// exactly as expirePaste does.
func (t *tui) expireTermColour(now time.Time) bool {
	changed := false
	if !t.termGrace.IsZero() && now.Sub(t.termGrace) > termGrace {
		t.sendTermColours()
		changed = true
	}
	if !t.termDeadline.IsZero() && now.Sub(t.termDeadline) > escTimeout {
		t.termDeadline = time.Time{}
		t.termOff = true
		t.processInput()
		t.termOff = false
		changed = true
	}
	return changed
}

// forwardPaste sends a bracketed paste to the focused pane as one keys message.
// A paste closes any open menu, picker, prompt or scrollback first and disarms
// the prefix: its bytes are text for the pane, never dm commands. A paste into
// the rename prompt closes it and the bytes go nowhere: a workspace label is
// typed, not dropped in.
func (t *tui) forwardPaste(seq []byte) {
	t.pasteDeadline = time.Time{}
	t.pasteHeld = 0
	t.pending = false
	t.disarm()
	switch t.mode {
	case "rename":
		// A pasted block is not a workspace name: close the prompt and drop
		// its bytes rather than feed them to the draft or the pane.
		t.closeRename()
		return
	case "menu":
		t.closeMenu()
	case "sessions":
		t.closePicker()
	case "scroll":
		t.exitScroll()
	default:
		t.mode = ""
	}
	focus := ""
	if t.snap != nil {
		focus = t.snap.Focus
	}
	_ = t.conn.Send(wire.Msg{Type: "keys", Keys: t.pasteKeys(seq, focus)})
}

// pasteKeys frames the paste only for the pane it is aimed at, and only when
// that pane asked for bracketed paste. A program that never asked, such as a
// line editor without 2004, reads the markers as keys and leaves their tail on
// the line: it gets the text alone.
func (t *tui) pasteKeys(seq []byte, pane string) []byte {
	if t.paneWantsPaste(pane) {
		return seq
	}
	if len(seq) < len(pasteStart)+len(pasteEnd) {
		return nil
	}
	return seq[len(pasteStart) : len(seq)-len(pasteEnd)]
}

// paneWantsPaste reports whether a pane asked for bracketed paste. The pane's
// own terminal is the live answer: the client tracks the mode in every byte
// stream, and a restored screen carries it, so the dashboard's preview
// terminal answers for a pane outside the active window too. The snapshot's
// pane info covers a pane the client has no terminal for yet.
func (t *tui) paneWantsPaste(pane string) bool {
	if pane == "" {
		return false
	}
	if term := t.vts[pane]; term != nil {
		return term.Grid().Modes().BracketedPaste
	}
	if term := t.swVT[pane]; term != nil {
		return term.Grid().Modes().BracketedPaste
	}
	if p := t.paneByID(pane); p != nil {
		return p.Paste
	}
	return false
}

func (t *tui) processInput() {
	for len(t.inbuf) > 0 {
		if !t.termOff {
			if kind, value, n, started := takeTermColour(t.inbuf); started {
				if n == 0 {
					// The reply is still arriving. Keep buffering; the ticker
					// sends these bytes down the normal path if they stall.
					if t.termDeadline.IsZero() {
						t.termDeadline = time.Now()
					}
					return
				}
				t.inbuf = append([]byte(nil), t.inbuf[n:]...)
				t.termDeadline = time.Time{}
				t.noteTermColour(kind, value)
				continue
			}
		}
		if t.sw != nil {
			// While the float is up every key belongs to it: nothing reaches a
			// pane. A mouse event and a paste are swallowed whole. A lone esc is
			// the close key, so it skips the paste marker check: a bare esc is
			// not the start of one.
			if len(t.inbuf) == 1 && t.inbuf[0] == 0x1b {
				if !t.switcherKey() {
					return
				}
				continue
			}
			if ev, n, ok := parseMouse(t.inbuf); ok {
				t.inbuf = t.inbuf[n:]
				_ = ev
				continue
			} else if n == 0 {
				return // partial sequence, wait for the rest
			}
			if _, n, started := t.takePaste(); started {
				if n == 0 {
					t.holdPaste(time.Now())
					return
				}
				t.inbuf = append([]byte(nil), t.inbuf[n:]...)
				// A paste over the dashboard is swallowed whole: the table and
				// its query take typing, not a block, and no pane owns the keys.
				continue
			}
			if !t.switcherKey() {
				return
			}
			continue
		}
		if t.mode == "help" {
			// While the view is up every key belongs to it: nothing reaches a
			// pane. A lone esc is the close key, so it skips the paste marker
			// check: a bare esc is not the start of one. A mouse event scrolls
			// and a paste is swallowed whole.
			if len(t.inbuf) == 1 && t.inbuf[0] == 0x1b {
				if !t.keymapKey() {
					return
				}
				continue
			}
			if ev, n, ok := parseMouse(t.inbuf); ok {
				t.inbuf = t.inbuf[n:]
				t.keymapWheel(ev)
				continue
			} else if n == 0 {
				return // partial sequence, wait for the rest
			}
			if seq, n, started := t.takePaste(); started {
				if n == 0 {
					t.holdPaste(time.Now())
					return
				}
				// A pasted block is text for nobody: the view is read-only.
				t.inbuf = append([]byte(nil), t.inbuf[n:]...)
				_ = seq
				continue
			}
			if !t.keymapKey() {
				return
			}
			continue
		}
		if t.mode == "box" {
			// While the view is up every key belongs to it: nothing reaches a
			// pane. A lone esc is the close key, so it skips the paste marker
			// check: a bare esc is not the start of one. A mouse event scrolls
			// and a paste is swallowed whole.
			if len(t.inbuf) == 1 && t.inbuf[0] == 0x1b {
				if !t.boxKey() {
					return
				}
				continue
			}
			if ev, n, ok := parseMouse(t.inbuf); ok {
				t.inbuf = t.inbuf[n:]
				t.boxWheel(ev)
				continue
			} else if n == 0 {
				return // partial sequence, wait for the rest
			}
			if seq, n, started := t.takePaste(); started {
				if n == 0 {
					t.holdPaste(time.Now())
					return
				}
				// A pasted block is text for nobody: the view is read-only.
				t.inbuf = append([]byte(nil), t.inbuf[n:]...)
				_ = seq
				continue
			}
			if !t.boxKey() {
				return
			}
			continue
		}
		if t.mode == "float" {
			// The float owns the keyboard: the bytes go to the command, and
			// the prefix alone is the client's. A lone esc is the esc key, so
			// it skips the mouse and paste checks. A mouse event and a paste
			// are swallowed: the command asked for neither.
			if len(t.inbuf) == 1 && t.inbuf[0] == 0x1b {
				if !t.floatKey() {
					return
				}
				continue
			}
			if ev, n, ok := parseMouse(t.inbuf); ok {
				t.inbuf = t.inbuf[n:]
				_ = ev
				continue
			} else if n == 0 {
				return // partial sequence, wait for the rest
			}
			if seq, n, started := t.takePaste(); started {
				if n == 0 {
					t.holdPaste(time.Now())
					return
				}
				t.inbuf = append([]byte(nil), t.inbuf[n:]...)
				_ = seq
				continue
			}
			if !t.floatKey() {
				return
			}
			continue
		}
		if seq, n, started := t.takePaste(); started {
			if n == 0 {
				// The marker is still arriving. Keep buffering; the ticker
				// sends these bytes down the normal path if they stall.
				t.holdPaste(time.Now())
				return
			}
			rest := append([]byte(nil), t.inbuf[n:]...)
			t.forwardPaste(seq)
			t.inbuf = rest
			continue
		}
		// These bytes cannot start a paste, so any earlier partial marker is
		// disproved and must not be flushed early.
		t.pasteDeadline = time.Time{}
		t.pasteHeld = 0
		if ev, n, ok := parseMouse(t.inbuf); ok {
			t.inbuf = t.inbuf[n:]
			// The rename prompt owns the keyboard, not the pointer: a stray
			// click must not move the draft or the focus out from under it.
			if t.mode != "rename" {
				t.handleMouse(ev)
			}
			continue
		} else if n == 0 {
			return // partial sequence, wait for the rest
		}
		if t.mode == "rename" {
			if !t.renameKey() {
				return
			}
			continue
		}
		if t.mode == "sessions" {
			if !t.pickerKey() {
				return
			}
			continue
		}
		if t.mode == "menu" {
			if !t.menuKey() {
				return
			}
			continue
		}
		if t.mode == "scroll" {
			if !t.scrollKey() {
				return
			}
			continue
		}
		if !t.armed {
			prefix := t.km().PrefixBytes()
			pi := -1
			if !t.pasteOff {
				pi = bytes.Index(t.inbuf, pasteStart)
			}
			ci := -1
			if len(prefix) > 0 {
				ci = bytes.Index(t.inbuf, prefix)
			}
			if ci >= 0 && (pi < 0 || ci < pi) {
				// The prefix arms. Bytes before it belong to the pane.
				if ci > 0 {
					_ = t.conn.Send(wire.Msg{Type: "keys", Keys: t.inbuf[:ci]})
				}
				t.inbuf = append([]byte(nil), t.inbuf[ci+len(prefix):]...)
				t.arm()
				continue
			}
			if pi > 0 {
				// Plain bytes before a paste are keys; the paste itself is
				// handled at the head of the buffer on the next pass.
				_ = t.conn.Send(wire.Msg{Type: "keys", Keys: t.inbuf[:pi]})
				t.inbuf = append([]byte(nil), t.inbuf[pi:]...)
				continue
			}
			if pi < 0 && partialPrefix(t.inbuf, prefix) {
				// The buffer ends with the head of the prefix key. Wait for the
				// rest; the ticker forwards the bytes if they stall.
				if !t.pending {
					t.pending = true
					t.escAt = time.Now()
				}
				return
			}
			if at := termColourAt(t.inbuf); at > 0 {
				// A colour reply shares this read with a keystroke. Send the
				// keys before it; the reply is handled at the head next pass.
				_ = t.conn.Send(wire.Msg{Type: "keys", Keys: t.inbuf[:at]})
				t.inbuf = append([]byte(nil), t.inbuf[at:]...)
				continue
			}
			if at := termColourTail(t.inbuf); at > 0 {
				// A reply is still arriving after a keystroke. Send the keys
				// before it and keep the tail; the deadline flushes it as
				// ordinary input if it never completes.
				_ = t.conn.Send(wire.Msg{Type: "keys", Keys: t.inbuf[:at]})
				t.inbuf = append([]byte(nil), t.inbuf[at:]...)
				if t.termDeadline.IsZero() {
					t.termDeadline = time.Now()
				}
				return
			}
			_ = t.conn.Send(wire.Msg{Type: "keys", Keys: t.inbuf})
			t.inbuf = nil
			return
		}
		if !t.armedKey() {
			return
		}
	}
}

// partialPrefix reports whether buf ends with the head of the prefix key, so the
// prefix is still arriving. A one-byte prefix has no head and never waits.
func partialPrefix(buf, prefix []byte) bool {
	for k := 1; k < len(prefix) && k <= len(buf); k++ {
		if bytes.HasSuffix(buf, prefix[:k]) {
			return true
		}
	}
	return false
}

// armedKey consumes the head of the input buffer as one main-mode binding. A
// partial sequence waits for the rest of its bytes. An unbound key notifies and
// disarms. It returns false when it needs more bytes.
func (t *tui) armedKey() bool {
	if len(t.inbuf) == 0 {
		return true
	}
	action, n, state := t.km().Match("main", t.inbuf)
	if state == keymap.Match && escapeRun(t.inbuf) > n {
		// The match covered only the first byte of an escape sequence. The
		// sequence as a whole has no binding, so it is one unknown key: the
		// rest of an F1 or a Home must not reach a pane.
		state = keymap.NoMatch
	}
	switch state {
	case keymap.Partial:
		if !t.pending {
			t.pending = true
			t.escAt = time.Now()
		}
		return false
	case keymap.Match:
		t.inbuf = append([]byte(nil), t.inbuf[n:]...)
		t.pending = false
		t.runAction(action)
		return true
	}
	b := t.inbuf[0]
	// An unbound escape sequence is one key too: its tail is not input.
	t.inbuf = append([]byte(nil), t.inbuf[escapeRun(t.inbuf):]...)
	t.pending = false
	t.notify(fmt.Sprintf("no binding for %q", string(b)))
	t.disarm()
	return true
}

// escapeRun returns how many leading bytes belong to one escape sequence. A
// sequence that has not finished yet runs to the end of the buffer. Esc and any
// byte other than '[', 'O' or esc is an Alt+key, so the pair is one sequence
// and the key is not left behind as ordinary input.
func escapeRun(buf []byte) int {
	if len(buf) == 0 {
		return 0
	}
	if buf[0] != 0x1b {
		return 1
	}
	i := 1
	if i < len(buf) && (buf[i] == '[' || buf[i] == 'O') {
		i++
		for i < len(buf) {
			c := buf[i]
			i++
			if c >= 0x40 && c <= 0x7e {
				break
			}
		}
		return i
	}
	if i < len(buf) && buf[i] != 0x1b {
		// Alt+key: esc and the key are one sequence. The tail of a multi-byte
		// character belongs to the same key, so it is not left in the buffer
		// to reach a pane as a stray byte.
		i++
		for i < len(buf) && buf[i]&0xc0 == 0x80 {
			i++
		}
	}
	return i
}

// expireArmed resolves an armed sequence that never finished arriving. A lone
// esc is the dismiss key, so a stalled sequence dismisses.
func (t *tui) expireArmed(now time.Time) bool {
	if !t.armed || !t.pending || now.Sub(t.escAt) <= escTimeout {
		return false
	}
	t.pending = false
	t.inbuf = t.inbuf[:0]
	t.disarm()
	return true
}

// expireMode closes an overlay whose lone esc never finished arriving. A bare
// esc is the close key in every overlay, so a stalled one means close.
func (t *tui) expireMode(now time.Time) bool {
	if t.mode == "" || !t.pending || now.Sub(t.escAt) <= escTimeout {
		return false
	}
	t.pending = false
	// The lone esc was consumed by the mode's key handler but left in the
	// buffer. Drop it with the mode, so the next keypress does not forward a
	// stray escape to a pane. The float never gets here: its lone prefix is
	// the close chord's head, and that wait does not expire, so expireMode
	// never sees a float pending.
	t.inbuf = t.inbuf[:0]
	switch t.mode {
	case "menu":
		t.closeMenu()
	case "sessions":
		t.closePicker()
	case "scroll":
		t.exitScroll()
	case "box":
		closeBoxView(t)
	case "help":
		closeKeymapView(t)
	case "rename":
		t.closeRename()
	}
	return true
}

// expirePrefix sends a prefix key that never finished arriving to the pane as
// plain input.
func (t *tui) expirePrefix(now time.Time) bool {
	if t.armed || t.mode != "" || t.sw != nil || !t.pending || now.Sub(t.escAt) <= escTimeout {
		return false
	}
	t.pending = false
	if len(t.inbuf) > 0 && t.conn != nil {
		_ = t.conn.Send(wire.Msg{Type: "keys", Keys: append([]byte(nil), t.inbuf...)})
	}
	t.inbuf = t.inbuf[:0]
	return true
}

// askPreview sends a read request for the selected pane when the float shows a
// preview the client does not already have. It asks at most once per pane per
// 120 ms and never for a pane in the active window, whose screen is in t.vts.
func (t *tui) askPreview() {
	if t.sw == nil || t.snap == nil || !t.sw.PreviewDue() {
		return
	}
	it, ok := t.sw.Selected()
	if !ok || it.Kind != rail.Pane || it.Pane == "" {
		return
	}
	if t.vts[it.Pane] != nil {
		return
	}
	if it.Pane == t.swPane && time.Since(t.swAt) < 120*time.Millisecond {
		return
	}
	t.swID++
	t.swAt = time.Now()
	t.swPane = it.Pane
	t.sw.SetPreview(t.swID, it.Pane)
	_ = t.conn.Send(wire.Msg{Type: "req", Action: "read", Target: it.Pane, ID: t.swID})
}

// previewEvery is the cadence the dashboard's live preview rides on: one screen
// request per interval while a pane outside the active window is selected. The
// pacer wakes at this ceiling even when idle, so the cadence costs no extra
// wake.
const previewEvery = 250 * time.Millisecond

// previewTick refreshes the dashboard's preview. A pane in the active window
// has a local terminal, so nothing is asked for it; every other pane gets one
// screen request per cadence, and a closed dashboard asks for nothing. One
// preview lives at a time: a terminal for a pane the selection left goes.
func (t *tui) previewTick(now time.Time) {
	if t.sw == nil || t.snap == nil || t.conn == nil {
		return
	}
	pane := t.sw.Target()
	for id := range t.swVT {
		if id != pane {
			delete(t.swVT, id)
		}
	}
	if pane == "" || t.vts[pane] != nil {
		return
	}
	if now.Sub(t.swScreenAt) < previewEvery {
		return
	}
	t.askScreen(pane, now)
}

// askScreen asks the server for one pane's visible screen and restores the
// reply into the dashboard's own terminal. The request carries an id, so a
// reply the server refuses lands in the ordinary reply path and stays silent:
// a read keeps the band usable until a screen arrives.
func (t *tui) askScreen(pane string, now time.Time) {
	t.swScreen++
	t.swScreenAt = now
	_ = t.conn.Send(wire.Msg{Type: "req", Action: "screen", Target: pane, ID: t.swScreen})
}

// applyPreviewScreen restores one pane's screen into the dashboard's terminal.
// The terminal keeps the pane's own size, not the band's: the painter draws the
// pane's grid into the band, the way it draws any pane row.
func (t *tui) applyPreviewScreen(id string, snap *vt.Snapshot) bool {
	if t.sw == nil || snap == nil || id == "" || id != t.sw.Target() {
		return false
	}
	term := t.swVT[id]
	if term == nil {
		term = vt.NewTerm(snap.Cols, snap.Rows)
	}
	if err := term.Restore(snap); err != nil {
		return false
	}
	if c, r := term.Grid().Size(); c != snap.Cols || r != snap.Rows {
		resizeTerm(term, snap.Cols, snap.Rows)
	}
	if t.swVT == nil {
		t.swVT = map[string]*vt.Term{}
	}
	t.swVT[id] = term
	return true
}

// previewLines pulls the lines out of a read reply. The reply carries them in
// Res as an object with a lines array, so a malformed one yields no lines and
// the band stays blank.
func previewLines(m wire.Msg) []string {
	if len(m.Res) == 0 {
		return nil
	}
	var res struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(m.Res, &res); err != nil {
		return nil
	}
	return res.Lines
}

// scrollKey consumes one key in scrollback mode. It returns false when it
// needs more bytes for an escape sequence.
func (t *tui) scrollKey() bool {
	if len(t.inbuf) == 0 {
		return true
	}
	action, n, state := t.km().Match("scroll", t.inbuf)
	switch state {
	case keymap.Partial:
		if !t.pending {
			t.pending = true
			t.escAt = time.Now()
		}
		return false
	case keymap.Match:
		t.inbuf = append([]byte(nil), t.inbuf[n:]...)
		t.pending = false
	default:
		// An unbound key leaves scrollback.
		t.inbuf = t.inbuf[1:]
		t.pending = false
		t.exitScroll()
		return true
	}
	switch action {
	case "up", "up-arrow":
		t.scrollBy(1)
	case "down", "down-arrow":
		t.scrollBy(-1)
	case "page-up":
		t.scrollBy(t.panHeight())
	case "page-down":
		t.scrollBy(-t.panHeight())
	case "top":
		t.scrollBy(t.maxOff)
	default:
		t.exitScroll()
	}
	return true
}

func (t *tui) panHeight() int {
	if p := t.paneByID(t.scroll); p != nil {
		return p.Rect.H
	}
	return 10
}

func (t *tui) scrollBy(n int) {
	if t.scroll == "" {
		t.exitScroll()
		return
	}
	if t.scrollVT == nil {
		// The ring is still on its way; the wheel waits for it.
		return
	}
	t.offset += n
	if t.offset <= 0 {
		t.exitScroll()
		return
	}
	if t.offset > t.maxOff {
		t.offset = t.maxOff
	}
	t.dirty = true
}

func (t *tui) exitScroll() {
	// A selection anchored in this pane's scrollback has no terminal to point
	// at once the pane shows its live screen again. A selection that belongs
	// to another pane is kept: its own scroll view is not the one closing.
	if t.selText != nil && t.selText.src == selScroll && t.selText.pane == t.scroll {
		t.selText = nil
		t.press = nil
	}
	t.scroll = ""
	t.scrollVT = nil
	t.offset = 0
	t.maxOff = 0
	t.mode = ""
	t.inbuf = nil
	t.dirty = true
}

// startScroll puts a pane in scrollback. The client keeps no history until the
// operator asks for it, so the first entry requests one replay and shows the
// pane's live screen until the ring lands. A second entry asks for nothing.
func (t *tui) startScroll(p *wire.PaneInfo) {
	if p.Alt {
		t.notify("app is on the alternate screen; use its own scroll")
		return
	}
	t.mode = "scroll"
	t.scroll = p.ID
	t.scrollVT = nil
	t.offset = 0
	t.maxOff = 0
	t.armed = false
	t.dirty = true
	if m, ok := t.histMeta[p.ID]; ok {
		if m.got && m.at == p.Rect {
			t.buildScroll(p)
		}
		return
	}
	t.requestHistory(p)
}

// requestHistory asks the server for one pane's replay ring. histMeta is the
// cache key: it goes in before the request, so nothing asks twice.
func (t *tui) requestHistory(p *wire.PaneInfo) {
	if t.hist == nil {
		t.hist = map[string][]byte{}
	}
	if t.histMeta == nil {
		t.histMeta = map[string]histCache{}
	}
	t.histMeta[p.ID] = histCache{at: p.Rect}
	if t.conn != nil {
		_ = t.conn.Send(wire.Msg{Type: "req", Action: "replay", Target: p.ID})
	}
}

// buildScroll builds the tall terminal a scrollback pane is shown from, over
// the ring the server sent for the pane's current rect. The live screen keeps
// updating behind it; the tall one is a frozen snapshot.
func (t *tui) buildScroll(p *wire.PaneInfo) {
	rows := vt.ReplayRows(t.hist[p.ID], p.Rect.W, p.Rect.H, histRows+p.Rect.H)
	t.scrollVT = vt.BuildTall(rows, p.Rect.W)
	_, t.maxOff = scrollStart(t.scrollVT, p.Rect.H, 0)
	// A selection held on the live screen moves to the tall terminal now that
	// the pane shows scrollback.
	t.selToScroll(p)
}

// noteHist records a ring that just landed. A ring wrapped for a rect the pane
// no longer has is dropped, and so is one for a pane that is gone.
func (t *tui) noteHist(id string) {
	if t.histMeta == nil {
		t.histMeta = map[string]histCache{}
	}
	p := t.paneByID(id)
	if p == nil {
		delete(t.hist, id)
		delete(t.histMeta, id)
		return
	}
	if m, ok := t.histMeta[id]; ok && m.at != p.Rect {
		delete(t.hist, id)
		delete(t.histMeta, id)
		return
	}
	t.histMeta[id] = histCache{at: p.Rect, got: true}
}

// histArrived fills the scrollback of a pane that was waiting for its ring.
func (t *tui) histArrived(id string) {
	if t.scroll != id || t.scrollVT != nil {
		return
	}
	if m, ok := t.histMeta[id]; !ok || !m.got {
		return
	}
	p := t.paneByID(id)
	// A net wheel-down before the ring lands asked to scroll below the live
	// row. There is nothing there, so leave scrollback and keep the live
	// selection instead of sitting in scroll mode on the live row.
	if s := t.selText; s != nil && s.pane == id && p != nil && s.pending < 0 {
		s.pending = 0
		t.exitScroll()
		return
	}
	if p != nil {
		t.buildScroll(p)
	}
	// A wheel turn during a drag asked for this ring. Apply the turns that
	// arrived before it instead of the one-row entry scrollback normally takes.
	if s := t.selText; s != nil && s.pane == id && p != nil && s.pending != 0 {
		n := s.pending
		s.pending = 0
		t.selScrollBy(p, n)
		t.dirty = true
		return
	}
	t.scrollTop()
}

// scrollTop puts scrollback one row above the live screen. A pane whose ring
// is still on its way waits: histArrived calls this again when it lands.
func (t *tui) scrollTop() {
	if t.scroll == "" {
		return
	}
	if t.scrollVT == nil {
		t.dirty = true
		return
	}
	if t.maxOff == 0 {
		t.exitScroll()
		t.notify("no scrollback yet")
		return
	}
	t.offset = 1
	t.dirty = true
}

func (t *tui) startScrollOnFocus() {
	if t.snap == nil {
		return
	}
	p := t.paneByID(t.snap.Focus)
	if p == nil {
		return
	}
	if p.Alt {
		t.disarm()
		t.notify("app is on the alternate screen; use its own scroll")
		return
	}
	t.startScroll(p)
	if t.scroll != p.ID {
		return
	}
	t.scrollTop()
}

// focusForClick is the pane a left press must focus, or "" when focus is
// already there or the event is not a plain left press.
func focusForClick(p *wire.PaneInfo, cur string, ev mouseEvent) string {
	if ev.btn&3 != 0 || ev.btn&32 != 0 || ev.btn&64 != 0 || ev.release {
		return ""
	}
	if cur == p.ID {
		return ""
	}
	return p.ID
}

// zoomedPane returns the pane the snapshot marks as zoomed, or nil.
func zoomedPane(s *wire.Snapshot) *wire.PaneInfo {
	if s == nil || s.Zoom == "" {
		return nil
	}
	for i := range s.Panes {
		if s.Panes[i].ID == s.Zoom {
			return &s.Panes[i]
		}
	}
	return nil
}

// zoomedHit returns the zoomed pane when the point lies in the pane area, the
// box that pane fills while zoomed. Every hit test while zoomed goes through
// it, so a click on a hidden sibling's old rectangle lands on the zoomed pane.
func zoomedHit(s *wire.Snapshot, x, y int) *wire.PaneInfo {
	p := zoomedPane(s)
	if p == nil {
		return nil
	}
	a := layout.PaneAreaForWithTabBar(s.Cols, s.Rows, s.Rail)
	if x < a.X || y < a.Y || x >= a.X+a.W || y >= a.Y+a.H {
		return nil
	}
	return p
}

// visiblePanes is the panes a frame draws: all of them, or only the zoomed one
// while a zoom is set. The snapshot keeps every rect, so the rail, the switcher
// and the pane list keep their geometry.
func visiblePanes(win *wire.Snapshot) []wire.PaneInfo {
	if win == nil {
		return nil
	}
	if z := zoomedPane(win); z != nil {
		return []wire.PaneInfo{*z}
	}
	return win.Panes
}

// paneAtBox returns the pane whose whole box contains the point, border
// included. A click on a pane's frame belongs to that pane.
func (t *tui) paneAtBox(x, y int) *wire.PaneInfo {
	if t.snap == nil {
		return nil
	}
	if p := zoomedHit(t.snap, x, y); p != nil {
		return p
	}
	for i := range t.snap.Panes {
		r := t.snap.Panes[i].Rect
		// The hit area is the box including its border, so a click on the
		// frame focuses the pane.
		b := layout.BoxRect(r, t.pad()+1)
		if x >= b.X && x < b.X+b.W && y >= b.Y && y < b.Y+b.H {
			return &t.snap.Panes[i]
		}
	}
	return nil
}

func (t *tui) paneAt(x, y int) *wire.PaneInfo {
	if t.snap == nil {
		return nil
	}
	if p := zoomedHit(t.snap, x, y); p != nil {
		return p
	}
	for i := range t.snap.Panes {
		r := t.snap.Panes[i].Rect
		if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
			return &t.snap.Panes[i]
		}
	}
	return nil
}

// cell is a pane content coordinate: column and row counted from the pane's
// rect top-left.
type cell struct{ x, y int }

// selSource names the terminal a selection's coordinates are counted in: the
// pane's live screen or the tall terminal its scrollback is drawn from.
type selSource int

const (
	selLive selSource = iota
	selScroll
)

// selection is the text dm selected with the mouse. anchor and focus are
// row-major cell offsets in the terminal src names, so a wheel turn moves the
// text under the selection and not the selection off the text.
type selection struct {
	pane   string
	src    selSource
	anchor int
	focus  int

	// w and h are the pane's content size when the press landed. A snapshot
	// that changes the geometry drops the selection.
	w, h int

	// moved is set once the pointer passed the drag threshold, so a jitter on
	// a click stays a click.
	moved bool

	// pending is the wheel delta that arrived before the scrollback ring did.
	// histArrived applies it once the tall terminal exists.
	pending int
}

// heldPress is a left press on a pane that has not been forwarded yet. A drag
// turns it into a selection, a release in the same cell forwards a click.
type heldPress struct {
	pane  string
	mouse bool
	evs   []mouseEvent
}

// cellAt maps a screen point to a cell inside p's content rect.
func cellAt(p *wire.PaneInfo, x, y int) (cell, bool) {
	r := p.Rect
	if x < r.X || y < r.Y || x >= r.X+r.W || y >= r.Y+r.H {
		return cell{}, false
	}
	return cell{x: x - r.X, y: y - r.Y}, true
}

// selRow0 is the first row of the terminal the selection is drawn from that
// the pane's window shows. A live selection starts at row 0; a scrollback one
// starts at the scroll offset.
func (t *tui) selRow0(s *selection, p *wire.PaneInfo) int {
	if s.src != selScroll || t.scroll != p.ID || t.scrollVT == nil {
		return 0
	}
	row0, _ := scrollStart(t.scrollVT, p.Rect.H, t.offset)
	return row0
}

// selFocus moves the selection's focus to the point, clamped to the content
// rect of the pane the drag started in. The row is counted in the terminal the
// selection is anchored in, so a scrolled pane moves the focus with the rows
// under the pointer.
func (t *tui) selFocus(x, y int) {
	s := t.selText
	if s == nil {
		return
	}
	if s.src == selScroll && t.scroll != s.pane {
		return
	}
	p := t.paneByID(s.pane)
	if p == nil || p.Rect.W < 1 || p.Rect.H < 1 {
		return
	}
	cx := x - p.Rect.X
	cy := y - p.Rect.Y
	if cx < 0 {
		cx = 0
	}
	if cy < 0 {
		cy = 0
	}
	if cx >= p.Rect.W {
		cx = p.Rect.W - 1
	}
	if cy >= p.Rect.H {
		cy = p.Rect.H - 1
	}
	row0 := t.selRow0(s, p)
	off := (row0+cy)*p.Rect.W + cx
	if !s.moved {
		ax, ay := s.anchor%p.Rect.W, s.anchor/p.Rect.W
		dx, dy := cx-ax, (row0+cy)-ay
		if dx < 0 {
			dx = -dx
		}
		if dy < 0 {
			dy = -dy
		}
		if dx < 2 && dy < 2 {
			return // a one-cell jitter is still a click
		}
		s.moved = true
	}
	if off != s.focus {
		s.focus = off
		t.dirty = true
	}
}

// rangeIn returns the selection as a row-major range in the terminal it is
// anchored in.
func (s *selection) rangeIn() *selRange {
	if s == nil {
		return nil
	}
	lo, hi := s.anchor, s.focus
	if lo > hi {
		lo, hi = hi, lo
	}
	return &selRange{lo: lo, hi: hi}
}

// dropLiveSelection drops a selection on the live screen when new output
// scrolls that screen: the cells it named have moved, so it cannot hold its
// text. A selection in the frozen scrollback is not touched.
func (t *tui) dropLiveSelection(id string) {
	if t.selText != nil && t.selText.pane == id && t.selText.src == selLive {
		t.selText = nil
		t.press = nil
		t.dirty = true
	}
}

// clearSelection drops the selection and the press held for it. A press that
// outlives its selection would forward a phantom click to the app on release.
func (t *tui) clearSelection() {
	if t.selText != nil || t.press != nil {
		t.selText = nil
		t.press = nil
		t.dirty = true
	}
}

// finishPress ends a held left press. A release in another cell copies the
// selection; the same cell is a click, forwarded to an app that claims the
// mouse.
func (t *tui) finishPress(x, y int, ev mouseEvent) {
	pr := t.press
	t.press = nil
	if pr == nil {
		return
	}
	t.selFocus(x, y)
	if t.selText != nil && t.selText.pane == pr.pane && t.selText.focus != t.selText.anchor {
		pane := t.selText.pane
		t.copySelection()
		// A drag that wheeled back to the live screen leaves scrollback at
		// offset 0; close it so the next key reaches the app.
		if t.scroll == pane && t.offset == 0 {
			t.exitScroll()
		}
		return
	}
	t.selText = nil
	t.dirty = true
	p := t.paneByID(pr.pane)
	if !pr.mouse || t.conn == nil || p == nil {
		return
	}
	var keys []byte
	for _, e := range append(pr.evs, ev) {
		keys = append(keys, paneMouse(p, e)...)
	}
	if len(keys) > 0 {
		_ = t.conn.Send(wire.Msg{Type: "keys", Keys: keys})
	}
}

// copySelection reads the selected text out of the pane's VT and sends it to
// the terminal's clipboard.
func (t *tui) copySelection() {
	s := t.selText
	t.selText = nil
	t.dirty = true
	if s == nil {
		return
	}
	p := t.paneByID(s.pane)
	if p == nil {
		return
	}
	term := t.vts[s.pane]
	if s.src == selScroll {
		if t.scroll != s.pane {
			return
		}
		term = t.scrollVT
	}
	if term == nil {
		return
	}
	text := extractSelection(term, p.Rect.W, s.anchor, s.focus)
	os.Stdout.WriteString(osc52(text))
	t.notify(fmt.Sprintf("copied %d chars", len(text)))
}

// focusedPane is the pane the main keymap acts on, nil when the picture has
// none.
func (t *tui) focusedPane() *wire.PaneInfo {
	if t.snap == nil {
		return nil
	}
	return t.paneByID(t.snap.Focus)
}

// copyOutput asks the server for the focused pane's output since the last
// Enter. The reply lands on the message loop, which writes the clipboard.
func (t *tui) copyOutput() {
	p := t.focusedPane()
	if p == nil {
		t.notify("no pane")
		t.disarm()
		return
	}
	t.yankID++
	t.yankPane = p.ID
	_ = t.conn.Send(wire.Msg{Type: "req", Action: "yank", Target: p.ID, ID: t.yankID})
	t.disarm()
}

// copyPaneID writes the focused pane's id to the clipboard. The id is the
// handle the shell, dm exec and every other dm surface names a pane by.
func (t *tui) copyPaneID() {
	p := t.focusedPane()
	if p == nil {
		t.notify("no pane")
		t.disarm()
		return
	}
	t.writeClipboard(p.ID)
	t.notify("copied " + p.ID)
	t.disarm()
}

// copyScreen copies the focused pane's visible screen to the clipboard.
func (t *tui) copyScreen() {
	p := t.focusedPane()
	if p == nil {
		t.notify("no pane")
		t.disarm()
		return
	}
	term := t.vts[p.ID]
	if term == nil {
		t.notify("no screen for " + p.ID)
		t.disarm()
		return
	}
	text := screenText(term)
	t.writeClipboard(text)
	t.notify(fmt.Sprintf("copied %d chars", len(text)))
	t.disarm()
}

// takeYank writes the clipboard for a yank reply. A pane with no Enter has no
// anchor, so the copy falls back to the visible screen and the toast says so.
func (t *tui) takeYank(m wire.Msg) {
	if !m.OK {
		t.notify(m.Error)
		return
	}
	var res struct {
		Lines  []string `json:"lines"`
		Start  int      `json:"start"`
		Rows   int      `json:"rows"`
		Anchor bool     `json:"anchor"`
	}
	if len(m.Res) > 0 {
		if err := json.Unmarshal(m.Res, &res); err != nil {
			t.notify("yank: " + err.Error())
			return
		}
	}
	if !res.Anchor {
		term := t.vts[t.yankPane]
		if term == nil {
			t.notify("no screen for " + t.yankPane)
			return
		}
		text := screenText(term)
		t.writeClipboard(text)
		t.notify(fmt.Sprintf("no Enter yet; copied %d chars from screen", len(text)))
		return
	}
	if res.Start < 0 || res.Start > res.Rows || res.Rows > len(res.Lines) {
		t.notify("yank: reply out of range")
		return
	}
	lines := res.Lines[res.Start:res.Rows]
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	text := strings.Join(lines, "\n")
	t.writeClipboard(text)
	t.notify(fmt.Sprintf("copied %d chars", len(text)))
}

// writeClipboard sends text to the terminal's clipboard with OSC 52.
func (t *tui) writeClipboard(text string) {
	os.Stdout.WriteString(osc52(text))
}

// screenText returns the visible rows joined by a newline, trailing blanks
// trimmed and trailing blank rows dropped.
func screenText(term *vt.Term) string {
	if term == nil {
		return ""
	}
	g := term.Grid()
	_, rows := g.Size()
	lines := make([]string, 0, rows)
	for y := 0; y < rows; y++ {
		lines = append(lines, g.Text(y))
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// liveToTall is the row offset from the live screen to the tall terminal the
// scrollback is drawn from. The tall terminal ends with the live screen's
// rows, so live row r is tall row r+liveToTall.
func (t *tui) liveToTall(p *wire.PaneInfo) int {
	if t.scrollVT == nil {
		return 0
	}
	_, rows := t.scrollVT.Grid().Size()
	return rows - p.Rect.H
}

// selToScroll moves a live selection onto the tall terminal the scrollback is
// drawn from. It does nothing once the selection is already there.
func (t *tui) selToScroll(p *wire.PaneInfo) {
	s := t.selText
	if s == nil || s.pane != p.ID || s.src != selLive || t.scrollVT == nil {
		return
	}
	off := t.liveToTall(p) * p.Rect.W
	s.anchor += off
	s.focus += off
	s.src = selScroll
}

// selScrollBy scrolls the pane's scrollback while a selection drag is held and
// moves the focus with the rows under the pointer. It reports false when the
// ring has not landed yet.
func (t *tui) selScrollBy(p *wire.PaneInfo, n int) bool {
	s := t.selText
	if s == nil || s.pane != p.ID || t.scroll != p.ID || t.scrollVT == nil {
		return false
	}
	t.selToScroll(p)
	moved := t.scrollDrag(n)
	s.focus -= moved * p.Rect.W
	if moved != 0 {
		s.moved = true
	}
	t.dirty = true
	return true
}

// scrollDrag scrolls the scrollback by n rows and keeps it open at offset 0,
// so a selection stays anchored to the tall terminal until the button comes
// up. It returns the rows the view actually moved.
func (t *tui) scrollDrag(n int) int {
	if t.scrollVT == nil {
		return 0
	}
	before := t.offset
	t.offset += n
	if t.offset < 0 {
		t.offset = 0
	}
	if t.offset > t.maxOff {
		t.offset = t.maxOff
	}
	t.dirty = true
	return t.offset - before
}

// wheelSelect scrolls the pane's scrollback while a selection drag is held.
// The focus moves with the rows under the pointer, so a wheel turn past the
// pane's edge keeps selecting older rows. An alternate-screen pane has no
// scrollback: the focus stays clamped and the toast says why.
func (t *tui) wheelSelect(p *wire.PaneInfo, n int) {
	s := t.selText
	if s == nil {
		return
	}
	if p.Alt {
		t.notify("app is on the alternate screen; use its own scroll")
		return
	}
	if t.scroll != p.ID {
		t.startScroll(p)
	}
	if !t.selScrollBy(p, n) {
		s.pending += n
	}
}

// osc52 is the clipboard sequence for text. Under tmux it must ride the
// passthrough DCS with doubled escapes, or tmux swallows it.
func osc52(text string) string {
	seq := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x1b\\"
	if os.Getenv("TMUX") != "" {
		return "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
	}
	return seq
}

// urlPattern matches an http or https URL on one row. It stops at whitespace
// or a character that commonly delimits a URL in prose.
var urlPattern = regexp.MustCompile(`https?://[^\s"'<>()\[\]{}]+`)

// rowText reads one row of term as text. Trailing blanks go.
func rowText(term *vt.Term, w, row int) string {
	if term == nil || w < 1 {
		return ""
	}
	g := term.Grid()
	cols, rows := g.Size()
	if row < 0 || row >= rows {
		return ""
	}
	var b strings.Builder
	for x := 0; x < w && x < cols; {
		c := g.Cell(x, row)
		content := c.Content
		if content == "" {
			content = " "
		}
		b.WriteString(content)
		next := g.Next(x, row)
		if next <= x {
			next = x + 1
		}
		x = next
	}
	return strings.TrimRight(b.String(), " ")
}

// urlAtRow returns the URL in row of vt whose cell range covers x. The row is
// read as text and matched against urlPattern. Cells are treated one per rune,
// which holds for the ASCII URLs this matches.
func urlAtRow(term *vt.Term, w, row, x int) string {
	text := rowText(term, w, row)
	for _, loc := range urlPattern.FindAllStringIndex(text, -1) {
		start := len([]rune(text[:loc[0]]))
		end := start + len([]rune(text[loc[0]:loc[1]])) - 1
		if x >= start && x <= end {
			return strings.TrimRight(text[loc[0]:loc[1]], ".,;:!?")
		}
	}
	return ""
}

// urlMatchAtRow returns the URL in row of vt whose cell range covers x and the
// cell range itself. The row is read as text and matched against urlPattern.
// Cells are treated one per rune, which holds for the ASCII URLs this matches.
func urlMatchAtRow(term *vt.Term, w, row, x int) (string, *selRange) {
	text := rowText(term, w, row)
	for _, loc := range urlPattern.FindAllStringIndex(text, -1) {
		start := len([]rune(text[:loc[0]]))
		url := strings.TrimRight(text[loc[0]:loc[1]], ".,;:!?")
		end := start + len([]rune(url)) - 1
		if x >= start && x <= end {
			return url, &selRange{lo: start, hi: end}
		}
	}
	return "", nil
}

// paneHoverAt resolves the point in pane p to the URL range under it, or nil.
// The range is row-major in the pane's screen-local cells. The live VT is read,
// or the scrollback view when the pane is scrolled.
func (t *tui) paneHoverAt(p *wire.PaneInfo, x, y int) *linkHover {
	if p == nil {
		return nil
	}
	c, ok := cellAt(p, x, y)
	if !ok {
		return nil
	}
	term := t.vts[p.ID]
	row := c.y
	if t.scroll == p.ID && t.scrollVT != nil {
		term = t.scrollVT
		row0, _ := scrollStart(t.scrollVT, p.Rect.H, t.offset)
		row = row0 + c.y
	}
	if term == nil {
		return nil
	}
	_, rng := urlMatchAtRow(term, p.Rect.W, row, c.x)
	if rng == nil {
		return nil
	}
	return &linkHover{
		pane: p.ID, w: p.Rect.W,
		region: selRange{lo: c.y*p.Rect.W + rng.lo, hi: c.y*p.Rect.W + rng.hi},
	}
}

// boxHoverAt resolves the point to the URL range under it in the message box
// float, or nil. The float's VT is the box's interior, so the row is local.
func (t *tui) boxHoverAt(x, y int) *linkHover {
	b := t.box
	if b == nil || b.Float == nil || b.Float.VT == nil {
		return nil
	}
	r := b.Float.Rect
	if x < r.X || x >= r.X+r.W || y < r.Y || y >= r.Y+r.H {
		return nil
	}
	cx, cy := x-r.X, y-r.Y
	_, rng := urlMatchAtRow(b.Float.VT, r.W, cy, cx)
	if rng == nil {
		return nil
	}
	return &linkHover{
		pane: "box", w: r.W,
		region: selRange{lo: cy*r.W + rng.lo, hi: cy*r.W + rng.hi},
	}
}

// updateLinkHover tracks the URL under the pointer while ctrl is held, so the
// frame can underline it. A pointer off every URL, or a released ctrl, clears
// it. The terminal reports modifiers with mouse events only, so a stationary
// pointer keeps its underline until the next event moves it.
func (t *tui) updateLinkHover(x, y int, ctrl bool) {
	var h *linkHover
	if ctrl {
		switch t.mode {
		case "box":
			h = t.boxHoverAt(x, y)
		case "":
			if p := t.paneAtBox(x, y); p != nil {
				h = t.paneHoverAt(p, x, y)
			}
		}
	}
	if h == nil && t.hover == nil {
		return
	}
	if h != nil && t.hover != nil && *h == *t.hover {
		return
	}
	t.hover = h
	t.dirty = true
}

// hoverFor returns the hover range for the pane or float of id and width w, or
// nil when the hover belongs elsewhere or was measured against another width.
func (t *tui) hoverFor(id string, w int) *selRange {
	h := t.hover
	if h == nil || h.pane != id || h.w != w {
		return nil
	}
	r := h.region
	return &r
}

// urlAt returns the URL under the point in pane p, or "" when none. The live VT
// is read, or the scrollback view when the pane is scrolled. A URL is a run of
// non-space characters starting with http:// or https://; the click must land
// inside that run.
func (t *tui) urlAt(p *wire.PaneInfo, x, y int) string {
	if p == nil {
		return ""
	}
	c, ok := cellAt(p, x, y)
	if !ok {
		return ""
	}
	term := t.vts[p.ID]
	row := c.y
	if t.scroll == p.ID && t.scrollVT != nil {
		term = t.scrollVT
		row0, _ := scrollStart(t.scrollVT, p.Rect.H, t.offset)
		row = row0 + c.y
	}
	if term == nil {
		return ""
	}
	return urlAtRow(term, p.Rect.W, row, c.x)
}

func (t *tui) handleMouse(ev mouseEvent) {
	if t.sw != nil {
		return // the float owns the pointer while it is open
	}
	if t.snap == nil {
		return
	}
	if ev.release && ev.btn&3 == 0 && t.swallowRelease {
		// The menu already ran this click; its release is not the app's.
		t.swallowRelease = false
		return
	}
	if !ev.release && ev.btn&32 == 0 {
		t.swallowRelease = false
	}
	x, y := ev.x-1, ev.y-1
	t.mouseX, t.mouseY = x, y
	t.mouseSeen = true
	t.updateLinkHover(x, y, ev.btn&16 != 0)
	t.updatePadHover(x, y)
	t.updateRailHover()
	if t.mode == "menu" {
		if t.menu == nil {
			t.closeMenu()
			return
		}
		if t.menuPress {
			if ev.release {
				t.menuPress = false
				return
			}
			if ev.btn&32 == 0 {
				t.menuPress = false
			}
		}
		m := t.menu
		inside := x >= m.x && x < m.x+m.w && y >= m.y && y < m.y+len(m.items)+2
		row := m.itemAt(x, y)
		motion := ev.btn&32 != 0
		if ev.btn&64 != 0 {
			return // a wheel over the menu is not a click
		}
		if motion {
			// Hover or drag: light up the row under the pointer.
			if row >= 0 && row != m.sel {
				m.sel = row
				t.dirty = true
			}
			return
		}
		if ev.btn&3 == 0 && ev.btn&64 == 0 && !ev.release {
			// Left press: a row runs, an outside point closes the menu and
			// keeps going, so one click both dismisses and focuses.
			if row >= 0 {
				t.menuPick(row)
				t.swallowRelease = true
				return
			}
			if inside {
				return
			}
			t.closeMenu()
		} else {
			switch ev.btn & 3 {
			case 0:
				if row >= 0 {
					t.menuPick(row)
				}
			case 2: // right: release picks, press elsewhere dismisses
				if ev.release {
					if row >= 0 {
						t.menuPick(row)
					} else if !inside {
						t.closeMenu()
					}
				} else if !inside {
					t.closeMenu()
				}
			}
			return
		}
	}
	// A tab bar click belongs to the tab bar before any rail or pane logic.
	// A tab selects its window; a double click opens the window rename prompt.
	if t.mode == "" && t.tabBarClick(ev, x, y) {
		return
	}
	// The rail grip is the rail's last column. A drag of it adjusts the rail
	// width and swallows every event, motion and release alike, so nothing
	// reaches a pane even when the pointer leaves the rail.
	if t.railDragging {
		if ev.release {
			t.railDragTo(x, true)
			t.railDragging = false
			t.updatePadHover(x, y)
			t.dirty = true
			return
		}
		if ev.btn&32 == 0 {
			t.railDragging = false
			t.dirty = true
			return
		}
		t.railDragTo(x, false)
		return
	}
	// A left press on the grip starts that drag and sends nothing, so a stray
	// click on the last column does nothing.
	if t.press == nil && !t.dragging && ev.btn&3 == 0 && ev.btn&32 == 0 &&
		ev.btn&64 == 0 && !ev.release && railGripAt(t.snap, x, y) {
		t.startRailDrag()
		return
	}
	// The rail owns every event inside its columns. A left press on a pane row
	// focuses that pane; headers and empty space do nothing. A press that began
	// in the rail keeps owning its drag and release, so nothing leaks to a pane.
	if t.press == nil && !t.dragging && t.railClick(ev, x, y) {
		return
	}
	// Ctrl+click on a URL opens it. The click is consumed and its release is
	// swallowed, so the app never sees a press without its release. A ctrl+click
	// elsewhere falls through to the usual press.
	if ev.btn&16 != 0 && ev.btn&3 == 0 && ev.btn&32 == 0 && ev.btn&64 == 0 && !ev.release {
		if p := t.paneAtBox(x, y); p != nil {
			if _, ok := cellAt(p, x, y); ok {
				if u := t.urlAt(p, x, y); u != "" {
					if err := browser.Open(u); err != nil {
						t.notify("open: " + err.Error())
					} else {
						t.notify("opened " + u)
					}
					t.swallowRelease = true
					return
				}
			}
		}
	}
	// Right press is the mux's context menu, even over an app that owns the
	// mouse. It is a window-manager gesture, not the app's.
	if ev.btn&3 == 2 && ev.btn&32 == 0 && !ev.release {
		p := t.paneAtBox(x, y)
		if p == nil {
			p = t.paneByID(t.snap.Focus)
		}
		if p != nil {
			t.clearSelection()
			t.openMenu(p, x, y)
		}
		return
	}
	left := ev.btn&3 == 0 && ev.btn&32 == 0 && ev.btn&64 == 0 && !ev.release
	if t.dragging {
		if ev.release {
			t.dragTo(x, y, true)
			t.dragging = false
			t.updatePadHover(x, y)
			t.dirty = true
			return
		}
		if ev.btn&32 == 0 {
			t.dragging = false
			t.dirty = true
			return
		}
		t.dragTo(x, y, false)
		return
	}
	// A wheel while a selection drag is held scrolls the pane's scrollback and
	// moves the focus with the rows under the pointer. dm owns the gesture, so
	// the wheel does not reach the app even when the app claims the mouse.
	if t.press != nil && ev.btn&64 != 0 {
		if p := t.paneByID(t.press.pane); p != nil {
			n := 3
			if ev.btn&1 != 0 {
				n = -3
			}
			t.wheelSelect(p, n)
		}
		return
	}
	// A held left press is a selection drag until the button comes up.
	if t.press != nil {
		if ev.release && ev.btn&3 == 0 {
			t.finishPress(x, y, ev)
			return
		}
		if ev.btn&32 != 0 && ev.btn&3 == 0 {
			t.press.evs = append(t.press.evs, ev)
			t.selFocus(x, y)
			return
		}
		t.clearSelection()
	}
	// A divider belongs to the mux, unless its app claims the mouse.
	if left {
		if ds, ok := dragPress(t.snap, x, y); ok {
			t.clearSelection()
			t.startDrag(ds)
			return
		}
	}
	p := t.paneAtBox(x, y)
	if p == nil {
		return
	}
	if left {
		// A press on a pane starts a selection. The press is held: a drag
		// copies on release, a release in the same cell forwards a click.
		t.clearSelection()
		if focus := focusForClick(p, t.snap.Focus, ev); focus != "" && t.conn != nil {
			_ = t.conn.Send(wire.Msg{Type: "req", Action: "focus", Target: focus})
		}
		if c, ok := cellAt(p, x, y); ok {
			t.press = &heldPress{
				pane: p.ID, mouse: p.Mouse,
				evs: []mouseEvent{ev},
			}
			s := &selection{pane: p.ID, w: p.Rect.W, h: p.Rect.H}
			if t.scroll == p.ID && t.scrollVT != nil {
				// The pane shows scrollback: anchor the selection there, not
				// on the live screen hidden behind it.
				row0, _ := scrollStart(t.scrollVT, p.Rect.H, t.offset)
				s.src = selScroll
				s.anchor = (row0+c.y)*p.Rect.W + c.x
			} else {
				s.anchor = c.y*p.Rect.W + c.x
			}
			s.focus = s.anchor
			t.selText = s
			t.dirty = true
			return
		}
		// A press on the frame is not a selection: the app gets it as before.
		if p.Mouse && t.conn != nil {
			_ = t.conn.Send(wire.Msg{Type: "keys", Keys: paneMouse(p, ev)})
		}
		return
	}
	if ev.btn&32 != 0 && ev.btn&3 == 3 {
		// Pointer motion with no button: dm uses it for hover affordances and
		// keeps it from apps that asked only for button motion.
		return
	}
	if p.Mouse {
		// The pane daemon owns the mode; the client's VT may have missed the
		// enable sequence if it scrolled out of the replay ring.
		if t.conn != nil {
			_ = t.conn.Send(wire.Msg{Type: "keys", Keys: paneMouse(p, ev)})
		}
		return
	}
	if ev.btn&64 == 0 {
		return
	}
	if t.scroll != p.ID {
		t.startScroll(p)
	}
	if ev.btn&1 == 0 {
		t.scrollBy(3)
	} else {
		t.scrollBy(-3)
	}
}

// railHoverRow resolves the rail row the pointer lights, -1 when none. A row
// lights only after the pointer moved at least once, and only while it could
// act on the rail: no menu, modal, drag or selection press in flight, and
// never a gap row.
func (t *tui) railHoverRow() int {
	if !t.mouseSeen || t.mode != "" || t.dragging || t.railDragging ||
		t.press != nil || t.snap == nil || t.snap.Rail <= 0 {
		return -1
	}
	if it, ok := rail.RailItemAt(t.snap, t.mouseX, t.mouseY); ok && it.Kind != rail.Gap {
		return t.mouseY
	}
	return -1
}

// updateRailHover re-resolves the hover and marks the frame dirty when the
// lit row moved. The paint reads the pointer state directly, so a snapshot
// that relayouts the rail re-lights without a new pointer event.
func (t *tui) updateRailHover() {
	if h := t.railHoverRow(); h != t.railHover {
		t.railHover = h
		t.dirty = true
	}
}

// railPadAxis is the padAxis of a hover over the rail grip. It marks the zone
// as the rail's own, so the frame shows the hint without a split divider.
const railPadAxis = "rail"

// railGripAt reports whether x,y is the rail's grab strip: its last column,
// above the row the rail keeps free for its bottom control.
func railGripAt(snap *wire.Snapshot, x, y int) bool {
	if snap == nil || snap.Rail <= 0 {
		return false
	}
	return x == snap.Rail-1 && y >= 0 && y < snap.Rows-2
}

// dragStart is the split a mouse press resolved to.
type dragStart struct {
	first  string
	second string
	axis   string
	rect   layout.Rect
	zone   layout.Rect
}

// dragPress decides whether a left press at x,y starts a divider drag. The
// split's own rect is used for the ratio, so nested splits keep their full
// span. The divider always belongs to dm, even when a pane in the subtree
// claims the mouse: the app's PTY covers its content, not the border cells.
func dragPress(snap *wire.Snapshot, x, y int) (dragStart, bool) {
	if snap == nil || snap.Root == nil || snap.Zoom != "" {
		return dragStart{}, false
	}
	_, first, second, axis, r, zone, ok := padHit(snap.Root,
		layout.PaneAreaForWithTabBar(snap.Cols, snap.Rows, snap.Rail), x, y)
	if !ok {
		return dragStart{}, false
	}
	return dragStart{
		first: first, second: second, axis: axis, rect: r,
		zone: zone,
	}, true
}

// barInfo is the transient right side of the status bar: a scroll position, or
// the divider hint while the pointer rests on one.
func (t *tui) barInfo() string {
	if t.scroll != "" {
		return fmt.Sprintf("SCROLL %s +%d/%d", t.scroll, t.offset, t.maxOff)
	}
	if t.padZone != (layout.Rect{}) {
		return "drag to resize"
	}
	return ""
}

// updatePadHover tracks the divider under the pointer so the frame can show a
// grab affordance. It uses the same padHit path as the press, so hover and
// drag agree on the hit area.
func (t *tui) updatePadHover(x, y int) {
	var zone layout.Rect
	axis := ""
	if t.snap != nil && !t.dragging && !t.railDragging && t.mode == "" {
		if ds, ok := dragPress(t.snap, x, y); ok {
			zone, axis = ds.zone, ds.axis
		} else if railGripAt(t.snap, x, y) {
			// The grip is a grab strip too: same hint, no divider painted.
			zone, axis = layout.Rect{X: t.snap.Rail - 1, Y: 0, W: 1, H: t.snap.Rows - 2}, railPadAxis
		}
	}
	if zone == t.padZone && axis == t.padAxis {
		return
	}
	t.padZone, t.padAxis = zone, axis
	t.dirty = true
}

func (t *tui) startDrag(d dragStart) {
	t.dragging = true
	t.padZone = layout.Rect{}
	t.dragFirst = d.first
	t.dragSecond = d.second
	t.dragAxis = d.axis
	t.dragStart = d.rect.X
	space := d.rect.W
	if d.axis == "col" {
		t.dragStart = d.rect.Y
		space = d.rect.H
	}
	t.dragSpace = space
	t.dragAt = time.Time{}
	t.dirty = true
}

// dragTo sends the split ratio the pointer asks for. Unless forced, it sends
// at most once every 30 ms; release forces the final position through.
func (t *tui) dragTo(x, y int, force bool) {
	// Two boxes of at least five cells need ten.
	if t.dragSpace < 10 {
		return
	}
	pos := x
	if t.dragAxis == "col" {
		pos = y
	}
	off := pos - t.dragStart
	if off < 5 {
		off = 5
	}
	if off > t.dragSpace-5 {
		off = t.dragSpace - 5
	}
	if !force && !t.dragAt.IsZero() && time.Since(t.dragAt) < 30*time.Millisecond {
		return
	}
	t.dragAt = time.Now()
	ratio := float64(off) / float64(t.dragSpace)
	_ = t.conn.Send(wire.Msg{
		Type: "req", Action: "resize-split",
		Target: t.dragFirst, Other: t.dragSecond, Ratio: ratio,
	})
}

// startRailDrag begins a drag of the rail grip. It sends nothing: a click that
// never moves must not resize the rail.
func (t *tui) startRailDrag() {
	t.railDragging = true
	t.railDragAt = time.Time{}
	if t.snap != nil {
		t.railWidth = t.snap.Rail
	}
	t.padZone = layout.Rect{}
	t.padAxis = ""
	t.dirty = true
}

// railDragTo sends the rail width the pointer asks for. The thin rail is the
// narrowest a drag goes: hiding is the toggle's job, not a drag's. A width the
// drag already asked for is not sent again, so a press that never moved sends
// nothing. Unless forced, it sends at most once every 30 ms; a release forces
// the final width through.
func (t *tui) railDragTo(x int, force bool) {
	if t.snap == nil || t.conn == nil {
		return
	}
	w := x + 1
	if w < layout.CollapsedRail {
		w = layout.CollapsedRail
	}
	if m := layout.MaxRail(t.snap.Cols); w > m {
		w = m
	}
	if w == t.railWidth {
		return
	}
	if !force && !t.railDragAt.IsZero() && time.Since(t.railDragAt) < 30*time.Millisecond {
		return
	}
	t.railDragAt = time.Now()
	t.railWidth = w
	_ = t.conn.Send(wire.Msg{Type: "req", Action: "set-rail", Index: w})
}

type mouseEvent struct {
	btn     int
	x, y    int
	release bool
	raw     []byte
}

// paneMouse rewrites an SGR mouse event for the program in p: the program
// sees its own screen, so the point must be relative to the pane's content
// rect. An absolute point names a cell the program does not have, and it
// drops the wheel and the click. A point outside the content rect clamps to
// its nearest edge, so a press on the border still lands on the app. A rect
// with no area cannot map a point, and the bytes go through unchanged.
func paneMouse(p *wire.PaneInfo, ev mouseEvent) []byte {
	r := p.Rect
	if r.W < 1 || r.H < 1 {
		return ev.raw
	}
	x := clampCell(ev.x-1-r.X, r.W)
	y := clampCell(ev.y-1-r.Y, r.H)
	return sgrMouse(ev.btn, x+1, y+1, ev.release)
}

// clampCell folds a content-relative cell into 0..size-1.
func clampCell(v, size int) int {
	if v < 0 {
		return 0
	}
	if v >= size {
		return size - 1
	}
	return v
}

// sgrMouse encodes one SGR mouse event. It is the encoding dm asks the
// terminal for, and the one a pane program gets: a pane's VT keeps no other.
func sgrMouse(btn, x, y int, release bool) []byte {
	end := byte('M')
	if release {
		end = 'm'
	}
	return []byte(fmt.Sprintf("\x1b[<%d;%d;%d%c", btn, x, y, end))
}

// parseMouse reads an SGR mouse sequence: ESC [ < b ; x ; y M|m. n is the bytes
// consumed; n == 0 means the sequence is still incomplete.
func parseMouse(b []byte) (ev mouseEvent, n int, ok bool) {
	if len(b) < 3 || b[0] != 0x1b || b[1] != '[' || b[2] != '<' {
		return ev, -1, false
	}
	end := -1
	for i := 3; i < len(b) && i < 48; i++ {
		if b[i] == 'M' || b[i] == 'm' {
			end = i
			break
		}
		if (b[i] < '0' || b[i] > '9') && b[i] != ';' {
			return ev, -1, false
		}
	}
	if end < 0 {
		return ev, 0, false
	}
	parts := strings.Split(string(b[3:end]), ";")
	if len(parts) != 3 {
		return ev, end + 1, false
	}
	btn, err1 := strconv.Atoi(parts[0])
	x, err2 := strconv.Atoi(parts[1])
	y, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return ev, end + 1, false
	}
	ev = mouseEvent{btn: btn, x: x, y: y, release: b[end] == 'm', raw: append([]byte(nil), b[:end+1]...)}
	return ev, end + 1, true
}

func (t *tui) moveSel(step int) {
	if len(t.sessions) == 0 {
		return
	}
	t.sel = (t.sel + step + len(t.sessions)) % len(t.sessions)
	t.dirty = true
}

func (t *tui) pickSession() {
	if len(t.sessions) == 0 {
		t.closePicker()
		return
	}
	name := t.sessions[t.sel].Name
	t.closePicker()
	if name == t.session {
		return
	}
	t.switchTo = name
}

func (t *tui) openPicker() {
	sessions, err := transport.ListSessions()
	if err != nil {
		t.notify(err.Error())
		t.disarm()
		return
	}
	if len(sessions) == 0 {
		t.notify("no sessions")
		t.disarm()
		return
	}
	t.sessions = sessions
	t.sel = 0
	for i, s := range sessions {
		if s.Name == t.session {
			t.sel = i
		}
	}
	t.mode = "sessions"
	t.armed = false
	t.dirty = true
}

func (t *tui) closePicker() {
	t.mode = ""
	t.inbuf = nil
	t.dirty = true
}

// openRenamePrompt takes over input for the active workspace's label. The
// draft starts as the current name from the snapshot, and the prompt resolves
// the active workspace's id from the rail entries so the request targets the
// id. A workspace the snapshot does not list opens an empty draft; the prompt
// still opens.
func (t *tui) openRenamePrompt() {
	if t.snap == nil {
		t.disarm()
		return
	}
	id, current := "", ""
	for _, p := range t.snap.All {
		if p.WorkspaceName == t.snap.Workspace {
			id, current = p.Workspace, p.WorkspaceName
			break
		}
	}
	if id == "" {
		// A snapshot whose Workspace field carries the id still resolves it.
		for _, p := range t.snap.All {
			if p.Workspace == t.snap.Workspace {
				id, current = p.Workspace, p.WorkspaceName
				break
			}
		}
	}
	t.openRenamePromptFor("workspace", id, current)
}

// openProjectPrompt opens the project prompt for the active workspace.
func (t *tui) openProjectPrompt() {
	if t.snap == nil {
		t.disarm()
		return
	}
	id, project := "", ""
	for _, p := range t.snap.All {
		if p.WorkspaceName == t.snap.Workspace || p.Workspace == t.snap.Workspace {
			id, project = p.Workspace, p.Project
			break
		}
	}
	t.openRenamePromptFor("project", id, project)
}

// openRenamePromptFor takes over input for the named workspace, window or
// pane. The kind picks the action the prompt sends and the label it paints;
// the draft starts as the current name.
func (t *tui) openRenamePromptFor(kind, id, current string) {
	t.renameKind = kind
	t.renameID = id
	t.renameDraft = current
	t.mode = "rename"
	t.armed = false
	t.pending = false
	t.dirty = true
}

// closeRename drops the prompt and its draft, whatever it held.
func (t *tui) closeRename() {
	t.mode = ""
	t.renameDraft = ""
	t.renameID = ""
	t.renameKind = ""
	t.inbuf = nil
	t.pending = false
	t.dirty = true
}

// submitRename sends the trimmed draft to the server and closes the prompt. An
// empty result cancels every kind but project: a workspace label is never
// blank, while an empty project clears the tag. The prompt closes first, so a
// refusal toast lands on the live picture.
func (t *tui) submitRename() {
	if t.snap == nil {
		t.closeRename()
		return
	}
	name := strings.TrimSpace(t.renameDraft)
	target := t.renameID
	kind := t.renameKind
	action := "rename-workspace"
	switch kind {
	case "window":
		action = "rename-window"
	case "pane":
		action = "set-pane-name"
	case "project":
		action = "set-project"
	}
	t.closeRename()
	if name == "" && kind != "project" {
		return
	}
	if t.conn != nil {
		_ = t.conn.Send(wire.Msg{Type: "req", Action: action, Target: target, Name: name})
	}
}

// renameKey consumes one key while the rename prompt is open. No byte leaves
// the client: every key edits or ends the draft. Esc cancels at once, so the
// prompt never waits for the rest of an escape sequence. It returns false when
// it needs more bytes for an escape sequence.
func (t *tui) renameKey() bool {
	if len(t.inbuf) == 0 {
		return true
	}
	action, n, state := t.km().Match("rename", t.inbuf)
	if state == keymap.Partial {
		if t.inbuf[0] == 0x1b {
			// A lone esc cancels. An arrow key cancels too: its remaining bytes
			// are dropped rather than typed into the draft.
			t.inbuf = t.inbuf[1:]
			t.pending = false
			t.closeRename()
			return true
		}
		if !t.pending {
			t.pending = true
			t.escAt = time.Now()
		}
		return false
	}
	if state == keymap.Match {
		t.inbuf = append([]byte(nil), t.inbuf[n:]...)
		t.pending = false
		switch action {
		case "cancel":
			t.closeRename()
		case "submit":
			t.submitRename()
		case "backspace":
			if r := []rune(t.renameDraft); len(r) > 0 {
				t.renameDraft = string(r[:len(r)-1])
			}
			t.dirty = true
		case "clear":
			t.renameDraft = ""
			t.dirty = true
		default:
			// The arrows cancel as they always have.
			t.closeRename()
		}
		return true
	}
	b := t.inbuf[0]
	t.inbuf = t.inbuf[1:]
	t.pending = false
	if b >= 0x20 && b <= 0x7e {
		t.renameDraft += string(rune(b))
		t.dirty = true
	}
	return true
}

// toggleRail cycles the rail: expanded, thin, hidden. A hidden rail comes back
// at the last expanded width, or the default when none was seen.
func (t *tui) toggleRail() {
	want := t.railToggleWidth(true)
	t.action("set-rail", func(m *wire.Msg) { m.Index = want })
}

// toggleRailControl is a click on the rail's bottom row: full <-> thin. Hiding
// stays on `prefix b`, so a thin rail expands here instead of going hidden.
func (t *tui) toggleRailControl() {
	want := t.railToggleWidth(false)
	t.action("set-rail", func(m *wire.Msg) { m.Index = want })
}

// railToggleWidth is the width a rail toggle asks for. The key and the bottom
// control both take their width from here, so the two routes cannot drift. A
// hidden or thin rail comes back at the last expanded width, or the default
// when none was seen. hide lets `prefix b` reach the hidden state; the control
// passes false and expands instead.
func (t *tui) railToggleWidth(hide bool) int {
	rail := 0
	if t.snap != nil {
		rail = t.snap.Rail
	}
	switch {
	case rail <= layout.CollapsedRail:
		if hide && rail > 0 {
			return 0
		}
		if t.railWant > 0 {
			return t.railWant
		}
		return layout.DefaultRail
	default:
		return layout.CollapsedRail
	}
}

// railDoubleClick is how soon a second left press on the same rail row counts
// as a double click.
const railDoubleClick = 400 * time.Millisecond

// isRailDoubleClick records a left press on rail row y and reports whether it
// lands within the double click window of the previous press on the same row.
func (t *tui) isRailDoubleClick(y int) bool {
	now := time.Now()
	double := !t.railPressAt.IsZero() && t.railPressRow == y &&
		now.Sub(t.railPressAt) <= railDoubleClick
	if double {
		t.railPressAt = time.Time{}
	} else {
		t.railPressAt, t.railPressRow = now, y
	}
	return double
}

// clearRailPress forgets the tracked rail press for t, so a later press does
// not count as a double click.
func (t *tui) clearRailPress() {
	t.railPressAt = time.Time{}
}

// tabBarClick consumes a mouse event over the tab bar. A left press on a tab
// sends select-window with the window index. A second press on the same tab
// within the double-click window opens the window rename prompt. A press
// elsewhere in the tab bar row is swallowed.
func (t *tui) tabBarClick(ev mouseEvent, x, y int) bool {
	if t.snap == nil {
		return false
	}
	bar := tabBarRect(t.snap)
	if bar.W < 1 || bar.H < 1 {
		return false
	}
	if y != bar.Y || x < bar.X || x >= bar.X+bar.W {
		return false
	}
	if ev.release {
		return true
	}
	// A right press over the tab bar opens no menu yet; the row belongs to the
	// window tabs. Swallow it so it never reaches a pane.
	if ev.btn&3 == 2 && ev.btn&32 == 0 && ev.btn&64 == 0 {
		return true
	}
	if ev.btn&3 != 0 || ev.btn&32 != 0 || ev.btn&64 != 0 {
		return true
	}
	it, ok := tabBarAt(t.snap, x, y, tabWidthFor(t.km()))
	if !ok {
		return true
	}
	now := time.Now()
	if now.Sub(t.lastTabClick) < 500*time.Millisecond &&
		t.lastTabClickPos.X == x && t.lastTabClickPos.Y == y {
		t.lastTabClick = time.Time{}
		seen := make(map[string]bool)
		var winID string
		idx := 0
		for _, p := range t.snap.All {
			if p.Workspace != t.snap.Workspace {
				continue
			}
			if !seen[p.Window] {
				seen[p.Window] = true
				if idx == it.Index {
					winID = p.Window
					break
				}
				idx++
			}
		}
		if winID != "" {
			t.openRenamePromptFor("window", winID, it.Name)
		}
		return true
	}
	t.lastTabClick = now
	t.lastTabClickPos = struct{ X, Y int }{x, y}
	if t.conn != nil {
		_ = t.conn.Send(wire.Msg{Type: "req", Action: "select-window", Index: it.Index})
	}
	return true
}

// railClick consumes a mouse event over the sidebar. A left press on the bottom
// control row toggles the rail full <-> thin. A left press on a workspace row
// focuses its first pane; a second press within the double click window opens
// the workspace's rename prompt; a left press on the workspace caret toggles
// its fold.
// A left press on a window row focuses the window's first pane; a left press on
// the window caret toggles the window fold; a second press within the double
// click window opens the window's rename prompt. A left press on a pane row
// focuses that pane; a second press within the double click window opens the
// pane's rename prompt. Every other rail event is swallowed so it never reaches
// a pane, and a drag that began in the rail stays swallowed outside it.
func (t *tui) railClick(ev mouseEvent, x, y int) bool {
	if t.snap == nil || t.snap.Rail <= 0 {
		return false
	}
	inside := x >= 0 && x < t.snap.Rail && y >= 0 && y < t.snap.Rows-1
	if !inside {
		if !t.railPress {
			return false
		}
		if ev.release || ev.btn&32 != 0 {
			if ev.release {
				t.railPress = false
			}
			return true
		}
		t.railPress = false
		return false
	}
	if ev.release {
		t.railPress = false
		return true
	}
	// A right press on a row opens that row's menu. The control row is the
	// session's own menu. The press arms railPress and the menu swallows its
	// release, so the pane menu never sees it.
	if ev.btn&3 == 2 && ev.btn&32 == 0 && ev.btn&64 == 0 {
		t.railPress = true
		if rail.RailControlAt(t.snap, x, y) {
			t.openSessionMenu(x, y)
		} else if it, ok := rail.RailItemAt(t.snap, x, y); ok {
			t.openRailMenu(it, x, y)
		}
		return true
	}
	if ev.btn&3 == 0 && ev.btn&32 == 0 && ev.btn&64 == 0 {
		t.railPress = true
		if rail.RailControlAt(t.snap, x, y) {
			// The bottom row is the control: it toggles the rail and
			// never focuses a pane or folds a window.
			t.clearRailPress()
			if t.conn != nil {
				t.toggleRailControl()
			}
			return true
		}
		if it, ok := rail.RailItemAt(t.snap, x, y); ok && it.Kind == rail.Project {
			if id := rail.RailFoldAt(t.snap, x, y); id != "" {
				t.clearRailPress()
				if t.conn != nil {
					_ = t.conn.Send(wire.Msg{Type: "req", Action: "toggle-fold", Target: id})
				}
				return true
			}
			// A project header focuses the first pane of the group's first
			// workspace. A second press does nothing, and the row never
			// opens a menu.
			if t.isRailDoubleClick(y) {
				return true
			}
			if id := t.firstPaneInProject(it.Project); id != "" && t.conn != nil {
				_ = t.conn.Send(wire.Msg{Type: "req", Action: "focus", Target: id})
			}
			return true
		}
		if id := rail.RailPaneAt(t.snap, x, y); id != "" {
			// A pane row focuses its pane. A second press on the same row
			// within the double click window opens the rename prompt.
			if t.isRailDoubleClick(y) {
				if it, ok := rail.RailItemAt(t.snap, x, y); ok {
					t.openRenamePromptFor("pane", it.Pane, it.PaneName)
				}
			} else if t.conn != nil {
				_ = t.conn.Send(wire.Msg{Type: "req", Action: "focus", Target: id})
			}
			return true
		}
		if id := rail.RailFoldAt(t.snap, x, y); id != "" {
			// The caret cell toggles the workspace's or window's fold
			// without moving focus.
			t.clearRailPress()
			if t.conn != nil {
				_ = t.conn.Send(wire.Msg{Type: "req", Action: "toggle-fold", Target: id})
			}
			return true
		}
		if id := rail.RailWindowAt(t.snap, x, y); id != "" {
			// A window row focuses the first available pane in that window,
			// which also makes the window active. A second press on the same
			// row within the double click window opens the window rename
			// prompt.
			if t.isRailDoubleClick(y) {
				if it, ok := rail.RailItemAt(t.snap, x, y); ok {
					t.openRenamePromptFor("window", it.Window, it.WindowName)
				}
			} else if pid := t.firstPaneInWindow(id); pid != "" && t.conn != nil {
				_ = t.conn.Send(wire.Msg{Type: "req", Action: "focus", Target: pid})
			}
			return true
		}
		if ws := rail.RailWorkspaceAt(t.snap, x, y); ws != "" {
			// A workspace row focuses the first pane in the workspace. A
			// second press on the same row within the double click window
			// opens the workspace rename prompt.
			if t.isRailDoubleClick(y) {
				if it, ok := rail.RailItemAt(t.snap, x, y); ok {
					t.openRenamePromptFor("workspace", it.Workspace, it.Text)
				}
			} else if t.conn != nil {
				for _, p := range t.snap.All {
					if p.Workspace == ws {
						_ = t.conn.Send(wire.Msg{Type: "req", Action: "focus", Target: p.ID})
						break
					}
				}
			}
			return true
		}
		t.clearRailPress()
	}
	return true
}

// windowForPane resolves a pane id to its window id from the snapshot. An
// unknown pane returns "".
func (t *tui) windowForPane(pane string) string {
	if t.snap == nil {
		return ""
	}
	for _, p := range t.snap.All {
		if p.ID == pane {
			return p.Window
		}
	}
	return ""
}

// firstPaneInProject returns the first pane of the group's first workspace, or
// "" when the group or that workspace has no pane. A project row click goes
// here: the row names the group, and the group's first workspace is the first
// workspace row tagged with that project.
func (t *tui) firstPaneInProject(project string) string {
	if t.snap == nil || project == "" {
		return ""
	}
	workspace := ""
	for _, it := range rail.RailItems(t.snap) {
		if it.Kind == rail.Workspace && it.Project == project {
			workspace = it.Workspace
			break
		}
	}
	if workspace == "" {
		return ""
	}
	for _, p := range t.snap.All {
		if p.Workspace == workspace {
			return p.ID
		}
	}
	return ""
}

// firstPaneInWindow returns the first pane in the window from the snapshot, or
// "" when the window has no pane. A window row click and its menu Focus both
// go here.
func (t *tui) firstPaneInWindow(window string) string {
	if t.snap == nil {
		return ""
	}
	for _, p := range t.snap.All {
		if p.Window == window {
			return p.ID
		}
	}
	return ""
}
