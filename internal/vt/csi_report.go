package vt

import (
	"fmt"
	"io"
	"strings"
)

// This file owns the report family: DSR, DA1, DA2, DECID, DECRQM, DECRQSS and
// XTGETTCAP. It registers its own keys in init(), so the family list in
// dispatch.go does not grow per stream.

func init() {
	registerReports(csiTable, escTable)
}

// ReportWriter receives the bytes a terminal sends back to its host. The
// foundation's Term has no writer and this stream may not edit term.go, so the
// sink is a package value. The swap moves it onto Term and deletes this.
var ReportWriter io.Writer

// reply sends one report to the host. A terminal with no writer drops it.
func (t *Term) reply(s string) {
	if ReportWriter == nil {
		return
	}
	_, _ = io.WriteString(ReportWriter, s)
}

// DECRQM mode states, the values xterm reports after the mode number.
const (
	modeNotRecognised    = 0
	modeSet              = 1
	modeReset            = 2
	modePermanentlyReset = 4
)

// permanentlyReset are the ANSI modes esctest2 expects a terminal to report as
// permanently reset. DECHCCM is the one DEC private mode in the set.
var permanentlyResetANSI = map[int]bool{
	1: true, 5: true, 7: true, 10: true, 11: true, 13: true,
	14: true, 15: true, 16: true, 17: true, 18: true, 19: true,
}

var permanentlyResetDEC = map[int]bool{
	60: true, // DECHCCM
}

// registerReports registers the CSI and ESC keys of the report family.
func registerReports(t map[csiKey]csiHandler, e map[escKey]escHandler) {
	t[csiKey{Final: 'c'}] = csiDA1
	t[csiKey{Priv: '>', Final: 'c'}] = csiDA2
	t[csiKey{Final: 'n'}] = csiDSR
	t[csiKey{Priv: '?', Final: 'n'}] = csiDECDSR
	t[csiKey{Inter: "$", Final: 'p'}] = csiDECRQM
	t[csiKey{Priv: '?', Inter: "$", Final: 'p'}] = csiDECRQMDEC
	e[escKey{Final: 'Z'}] = csiDECID
}

// identify is the device attributes reply: a VT level 4 terminal with the
// capabilities dm models.
const identify = "\x1b[?65;1;2;6;9;15;16;17;18;21;22;28;29c"

// csiDA1 answers a primary device attributes request.
func csiDA1(t *Term, seq CSI) {
	t.reply(identify)
}

// csiDA2 answers a secondary device attributes request: VT level 4, version
// 411, no options.
func csiDA2(t *Term, seq CSI) {
	t.reply("\x1b[>64;411;0c")
}

// csiDECID answers the ESC Z identify request with the primary attributes.
func csiDECID(t *Term) {
	t.reply(identify)
}

// csiDSR answers a device status report: 5 is the terminal is ready, 6 is the
// cursor position.
func csiDSR(t *Term, seq CSI) {
	switch csiParamOr(seq, 0, 0) {
	case 5:
		t.reply("\x1b[0n")
	case 6:
		t.replyCursorPosition(false)
	}
}

// csiDECDSR answers the DEC private device status report. Only the extended
// cursor position is modelled; the printer, keyboard, locator and session
// reports belong to the terminal that owns those devices.
func csiDECDSR(t *Term, seq CSI) {
	if csiParamOr(seq, 0, 0) == 6 {
		t.replyCursorPosition(true)
	}
}

// replyCursorPosition sends the cursor position, in grid coordinates plus one.
// The private form adds the page number, always 1.
func (t *Term) replyCursorPosition(private bool) {
	c := t.grid.Cursor()
	if private {
		t.reply(fmt.Sprintf("\x1b[?%d;%d;1R", c.Y+1, c.X+1))
		return
	}
	t.reply(fmt.Sprintf("\x1b[%d;%dR", c.Y+1, c.X+1))
}

// csiDECRQM answers a DECRQM for an ANSI mode.
func csiDECRQM(t *Term, seq CSI) {
	mode := csiParamOr(seq, 0, 0)
	t.reply(fmt.Sprintf("\x1b[%d;%d$y", mode, t.reportModeState(false, mode)))
}

// csiDECRQMDEC answers a DECRQM for a DEC private mode.
func csiDECRQMDEC(t *Term, seq CSI) {
	mode := csiParamOr(seq, 0, 0)
	t.reply(fmt.Sprintf("\x1b[?%d;%d$y", mode, t.reportModeState(true, mode)))
}

// reportModeState reads the state of a mode the way xterm's DECRQM reports it.
// A mode the grid does not model reports not recognised, because a terminal
// that ignores a mode has no state to report. The mode family fills this in as
// it adds modes; the harness keeps its own answer until then.
func (t *Term) reportModeState(private bool, mode int) int {
	g := t.grid
	m := g.Modes()
	if private {
		if permanentlyResetDEC[mode] {
			return modePermanentlyReset
		}
		switch mode {
		case 5: // DECSCNM
			return boolState(m.ReverseVideo)
		case 6: // DECOM
			return boolState(m.Origin)
		case 7: // DECAWM
			return boolState(m.Wrap)
		case 25: // DECTCEM
			return boolState(g.Cursor().Visible)
		case 47, 1047, 1049: // alternate screen
			return boolState(m.AltScreen)
		case 9, 1000, 1002, 1003: // mouse reporting
			return boolState(m.Mouse != MouseOff)
		case 1004: // focus reporting
			return boolState(m.FocusReporting)
		case 2004: // bracketed paste
			return boolState(m.BracketedPaste)
		}
		return modeNotRecognised
	}
	if permanentlyResetANSI[mode] {
		return modePermanentlyReset
	}
	switch mode {
	case 4: // IRM
		return boolState(m.Insert)
	}
	return modeNotRecognised
}

// boolState turns a flag into a DECRQM mode state.
func boolState(on bool) int {
	if on {
		return modeSet
	}
	return modeReset
}

// DECRQSSReply answers a DECRQSS body for the settings the grid holds. It
// returns the complete DCS reply. A setting it does not model reports as
// invalid, as xterm does for the ones it does not implement.
//
// The foundation parser buffers a DCS body but does not dispatch it yet, so the
// harness calls this directly. The parser unit adds the dispatch.
func (t *Term) DECRQSSReply(pt string) string {
	switch pt {
	case "r":
		r := t.grid.ScrollRegion()
		return dcsReply("1", fmt.Sprintf("%d;%d", r.Top+1, r.Bottom+1)+"r")
	case "s":
		r := t.grid.ScrollRegion()
		return dcsReply("1", fmt.Sprintf("%d;%d", r.Left+1, r.Right+1)+"s")
	}
	return dcsReply("0", pt)
}

// dcsReply wraps a DECRQSS result in the DCS envelope.
func dcsReply(status, body string) string {
	return "\x1bP" + status + "$r" + body + "\x1b\\"
}

// XTGETTCAPReply answers an XTGETTCAP request for one hex-encoded capability
// name. The terminal advertises the colour count and rejects the rest.
func XTGETTCAPReply(hex string) string {
	if strings.EqualFold(hex, "436F") { // "Co"
		return "\x1bP1+r" + hex + "=323536\x1b\\"
	}
	return "\x1bP0+r" + hex + "\x1b\\"
}
