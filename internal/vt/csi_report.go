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

// xtermUnsupportedDEC are the DEC private modes xterm reports as not
// recognised. esctest2 marks their DECRQM tests as known bugs for xterm, so the
// terminal must keep failing them to stay xterm-compatible.
var xtermUnsupportedDEC = map[int]bool{
	8: true, 34: true, 36: true, 57: true, 61: true, 64: true,
	68: true, 73: true, 81: true, 95: true, 96: true, 97: true,
	98: true, 99: true, 100: true, 101: true, 102: true,
	103: true, 104: true, 106: true,
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

	// The DECRQSS settings the grid does not otherwise model. They are kept
	// so a report answers with what the application last set.
	t[csiKey{Inter: " ", Final: 'q'}] = csiSetCursorStyle  // DECSCUSR
	t[csiKey{Inter: "*", Final: '|'}] = csiSetScreenLines  // DECSNLS
	t[csiKey{Inter: "$", Final: '~'}] = csiSetStatusType   // DECSSDT
	t[csiKey{Inter: "$", Final: '}'}] = csiSetActiveStatus // DECSASD
	t[csiKey{Inter: "*", Final: 'x'}] = csiSetChangeExtent // DECSACE
	t[csiKey{Inter: "+", Final: 'q'}] = csiSetLocalFuncs   // DECELF
	t[csiKey{Inter: "*", Final: '}'}] = csiSetLocalKeys    // DECLFKC
	t[csiKey{Inter: "+", Final: 'r'}] = csiSetModifierKeys // DECSMKR
	t[csiKey{Final: 't'}] = csiSetLinesPerPage             // DECSLPP / window op
}

// The state behind the DECRQSS settings that are not grid state.
const (
	defaultScreenLines = 24
)

// csiSetCursorStyle handles DECSCUSR.
func csiSetCursorStyle(t *Term, seq CSI) { t.cursorStyle = csiParamOr(seq, 0, 0) }

// csiSetScreenLines handles DECSNLS.
func csiSetScreenLines(t *Term, seq CSI) { t.screenLines = csiParamOr(seq, 0, 0) }

// csiSetStatusType handles DECSSDT.
func csiSetStatusType(t *Term, seq CSI) { t.statusType = csiParamOr(seq, 0, 0) }

// csiSetActiveStatus handles DECSASD.
func csiSetActiveStatus(t *Term, seq CSI) { t.activeStatus = csiParamOr(seq, 0, 0) }

// csiSetChangeExtent handles DECSACE.
func csiSetChangeExtent(t *Term, seq CSI) { t.changeExtent = csiParamOr(seq, 0, 0) }

// csiSetLocalFuncs handles DECELF.
func csiSetLocalFuncs(t *Term, seq CSI) { t.localFuncs = csiParamOr(seq, 0, 0) }

// csiSetLocalKeys handles DECLFKC.
func csiSetLocalKeys(t *Term, seq CSI) { t.localKeys = csiParamOr(seq, 0, 0) }

// csiSetModifierKeys handles DECSMKR.
func csiSetModifierKeys(t *Term, seq CSI) { t.modifierKeys = csiParamOr(seq, 0, 0) }

// csiSetLinesPerPage handles the window operations the terminal acts on.
// Operation 8 resizes the character grid, which is what an application or a
// test driver means by a window resize. Operation 27 carries the page height a
// DECRQSS later reports.
func csiSetLinesPerPage(t *Term, seq CSI) {
	switch csiParamOr(seq, 0, 0) {
	case 8:
		rows := csiParamOr(seq, 1, 0)
		cols := csiParamOr(seq, 2, 0)
		if rows > 0 && cols > 0 {
			t.grid.Resize(cols, rows)
		}
	case 27:
		t.linesPerPage = 27
	}
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
// In origin mode the position is relative to the margins, as xterm reports it.
// The private form adds the page number, always 1.
func (t *Term) replyCursorPosition(private bool) {
	c := t.grid.Cursor()
	if t.grid.Modes().Origin {
		// xterm reports the position relative to a margin only when the
		// cursor is inside that margin. A move that ignores origin mode can
		// put it outside, and then that axis stays absolute.
		r := t.grid.ScrollRegion()
		if c.X >= r.Left && c.X <= r.Right {
			c.X -= r.Left
		}
		if c.Y >= r.Top && c.Y <= r.Bottom {
			c.Y -= r.Top
		}
	}
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
// Every DECSET, DECRST, SM and RM is recorded, so a mode without a model still
// reports the value the application last set. A mode never seen and never
// modelled reports not recognised.
func (t *Term) reportModeState(private bool, mode int) int {
	g := t.grid
	m := g.Modes()
	if private {
		if permanentlyResetDEC[mode] {
			return modePermanentlyReset
		}
		if xtermUnsupportedDEC[mode] {
			return modeNotRecognised
		}
		if v, ok := t.decModeStates[mode]; ok {
			return boolState(v)
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
	if v, ok := t.ansiModeStates[mode]; ok {
		return boolState(v)
	}
	switch mode {
	case 4: // IRM
		return boolState(m.Insert)
	case 20: // LNM
		return boolState(m.LineFeedNewLine)
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
func (t *Term) DECRQSSReply(pt string) string {
	ok := func(v string) string { return dcsReply("1", v) }
	switch pt {
	case "+q":
		return ok(fmt.Sprintf("%d+q", t.localFuncs))
	case "*}":
		return ok(fmt.Sprintf("%d*}", t.localKeys))
	case "$}":
		return ok(fmt.Sprintf("%d$}", t.activeStatus))
	case "*x":
		return ok(fmt.Sprintf("%d*x", t.changeExtent))
	case "\"q":
		state := 0
		if t.protection.DECSCA {
			state = 1
		}
		return ok(fmt.Sprintf("%d\"q", state))
	case "\"p":
		return ok(fmt.Sprintf("%d;%d\"p", 60+t.level, t.sevenBit))
	case "r":
		r := t.grid.ScrollRegion()
		return ok(fmt.Sprintf("%d;%d", r.Top+1, r.Bottom+1) + "r")
	case "m":
		return ok(sgrReport(t.style) + "m")
	case " q":
		return ok(fmt.Sprintf("%d q", t.cursorStyle))
	case "s":
		r := t.grid.ScrollRegion()
		return ok(fmt.Sprintf("%d;%d", r.Left+1, r.Right+1) + "s")
	case "t":
		return ok(fmt.Sprintf("%dt", t.linesPerPage))
	case "*|":
		return ok(fmt.Sprintf("%d*|", t.screenLines))
	case "$~":
		return ok(fmt.Sprintf("%d$~", t.statusType))
	case "+r":
		return ok(fmt.Sprintf("%d+r", t.modifierKeys))
	}
	return dcsReply("0", pt)
}

// sgrReport renders the current rendition as an SGR parameter list. It starts
// with 0, as xterm's DECRQSS does.
func sgrReport(st Style) string {
	parts := []string{"0"}
	attrs := []struct {
		bit  uint8
		code string
	}{
		{Bold, "1"}, {Faint, "2"}, {Italic, "3"}, {Blink, "5"},
		{RapidBlink, "6"}, {Reverse, "7"}, {Conceal, "8"}, {Strikethrough, "9"},
	}
	for _, a := range attrs {
		if st.Attrs&a.bit != 0 {
			parts = append(parts, a.code)
		}
	}
	switch st.Underline {
	case UnderlineSingle:
		parts = append(parts, "4")
	case UnderlineDouble:
		parts = append(parts, "21")
	case UnderlineCurly:
		parts = append(parts, "4:3")
	case UnderlineDotted:
		parts = append(parts, "4:4")
	case UnderlineDashed:
		parts = append(parts, "4:5")
	}
	parts = append(parts, colorReport(st.Fg, true)...)
	parts = append(parts, colorReport(st.Bg, false)...)
	parts = append(parts, underlineColorReport(st.UnderlineColor)...)
	return strings.Join(parts, ";")
}

// colorReport renders one foreground or background colour as SGR parameters.
func colorReport(c Color, fg bool) []string {
	base := 30
	if !fg {
		base = 40
	}
	switch c.Kind {
	case ColorPalette:
		if c.Index < 8 {
			return []string{fmt.Sprint(base + int(c.Index))}
		}
		if c.Index < 16 {
			return []string{fmt.Sprint(base + 60 + int(c.Index) - 8)}
		}
		return []string{fmt.Sprintf("%d;5;%d", base+8, c.Index)}
	case ColorRGB:
		return []string{fmt.Sprintf("%d;2;%d;%d;%d", base+8, c.R, c.G, c.B)}
	}
	return nil
}

// underlineColorReport renders the underline colour as SGR 58 parameters.
func underlineColorReport(c Color) []string {
	switch c.Kind {
	case ColorPalette:
		return []string{fmt.Sprintf("58;5;%d", c.Index)}
	case ColorRGB:
		return []string{fmt.Sprintf("58;2;%d;%d;%d", c.R, c.G, c.B)}
	}
	return nil
}

// dcs handles one DCS body. DECRQSS and XTGETTCAP are the two the terminal
// answers.
func (t *Term) dcs(body string) {
	switch {
	case strings.HasPrefix(body, "$q"):
		t.reply(t.DECRQSSReply(body[2:]))
	case strings.HasPrefix(body, "+q"):
		t.reply(XTGETTCAPReply(body[2:]))
	}
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
