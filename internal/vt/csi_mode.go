package vt

// This file owns the mode family: ANSI SM/RM, DEC private DECSET/DECRST, and
// the xterm XTSAVE/XTRESTORE store. It registers itself so the family list in
// dispatch.go does not grow per stream.

func init() {
	registerModes(csiTable)
}

// registerModes registers the mode family's keys.
func registerModes(t map[csiKey]csiHandler) {
	t[csiKey{Final: 'h'}] = csiSetMode
	t[csiKey{Final: 'l'}] = csiResetMode
	t[csiKey{Priv: '?', Final: 'h'}] = csiSetPrivateMode
	t[csiKey{Priv: '?', Final: 'l'}] = csiResetPrivateMode
	t[csiKey{Priv: '?', Final: 's'}] = csiSaveModes
	t[csiKey{Priv: '?', Final: 'r'}] = csiRestoreModes
}

// ANSI modes. Only the ones the grid models are handled; the rest are ignored,
// as a terminal ignores a mode it does not have.
const (
	ansiKAM = 2  // keyboard action mode
	ansiIRM = 4  // insert/replace
	ansiLNM = 20 // line feed/new line
)

// DEC private modes.
const (
	decCKM            = 1    // cursor keys application mode
	decCOLM           = 3    // 132-column mode
	decSCNM           = 5    // reverse video
	decOM             = 6    // origin mode
	decAWM            = 7    // autowrap
	decX10Mouse       = 9    // X10 mouse reporting
	decDECTCEM        = 25   // cursor visible
	decAllow80To132   = 40   // permit DECCOLM
	decMoreFix        = 41   // more(1) workaround
	decReverseWrap    = 45   // reverse wraparound
	decReverseWrap2   = 1045 // extended reverse wraparound
	decALT47          = 47   // alternate screen
	decDECNKM         = 66   // application keypad
	decDECLRMM        = 69   // left/right margin mode
	decDECNCSM        = 95   // do not clear on DECCOLM
	decMouseButton    = 1000 // button event tracking
	decMouseMotion    = 1002 // button event tracking, any motion
	decMouseMany      = 1003 // any event tracking
	decFocus          = 1004 // focus in/out reports
	decMouseUTF8      = 1005 // UTF-8 mouse encoding
	decMouseSGR       = 1006 // SGR mouse encoding
	decMouseUrxvt     = 1015 // urxvt mouse encoding
	decALT1047        = 1047 // alternate screen, cleared on exit
	decSaveCursor     = 1048 // save/restore cursor as DECSC/DECRC
	decALT1049        = 1049 // alternate screen, cleared on entry, cursor saved
	decBracketedPaste = 2004 // bracketed paste
)

// csiSetMode handles SM: one or more ANSI modes on.
func csiSetMode(term *Term, seq CSI) {
	term.setANSIModes(seq.Params, true)
}

// csiResetMode handles RM: one or more ANSI modes off.
func csiResetMode(term *Term, seq CSI) {
	term.setANSIModes(seq.Params, false)
}

// csiSetPrivateMode handles DECSET.
func csiSetPrivateMode(term *Term, seq CSI) {
	for _, n := range seq.Params {
		term.setPrivateMode(n, true)
	}
}

// csiResetPrivateMode handles DECRST.
func csiResetPrivateMode(term *Term, seq CSI) {
	for _, n := range seq.Params {
		term.setPrivateMode(n, false)
	}
}

// setANSIModes applies an SM or RM to each parameter.
func (t *Term) setANSIModes(params []int, on bool) {
	for _, n := range params {
		m := t.grid.Modes()
		switch n {
		case ansiIRM:
			m.Insert = on
		case ansiLNM:
			m.LineFeedNewLine = on
		case ansiKAM:
			// Keyboard action mode is not modelled: the client sends keys.
		default:
			continue
		}
		t.grid.SetModes(m)
	}
}

// setPrivateMode applies one DECSET or DECRST parameter.
func (t *Term) setPrivateMode(n int, on bool) {
	g := t.grid
	m := g.Modes()
	switch n {
	case decCKM:
		m.CursorKeysApplication = on
	case decCOLM:
		t.setColumnMode(on)
		return
	case decSCNM:
		m.ReverseVideo = on
	case decOM:
		if m.Origin != on {
			m.Origin = on
			g.SetModes(m)
			t.originHome(on)
			return
		}
		return
	case decAWM:
		m.Wrap = on
		if !on {
			g.SetWrapNext(false)
		}
	case decX10Mouse:
		t.setMouse(MouseX10, on)
		return
	case decDECTCEM:
		g.SetCursorVisible(on)
		return
	case decAllow80To132:
		m.Allow80To132 = on
	case decMoreFix:
		m.MoreFix = on
	case decReverseWrap, decReverseWrap2:
		m.ReverseWrap = on
	case decALT47:
		if on {
			g.EnterAltScreen(false)
		} else {
			g.ExitAltScreen(false)
		}
		return
	case decDECNKM:
		m.ApplicationKeypad = on
	case decDECLRMM:
		m.LeftRightMargin = on
		if !on {
			// DECSLRM is disabled when DECLRMM is reset, so the left and
			// right margins return to the screen edges.
			cols, _ := g.Size()
			r := g.ScrollRegion()
			r.Left, r.Right = 0, cols-1
			g.SetScrollRegion(r)
		}
	case decDECNCSM:
		m.NoClearOnColumnMode = on
	case decMouseButton:
		t.setMouse(MouseButton, on)
		return
	case decMouseMotion:
		t.setMouse(MouseMotion, on)
		return
	case decMouseMany:
		t.setMouse(MouseMany, on)
		return
	case decFocus:
		m.FocusReporting = on
	case decMouseSGR:
		if on {
			m.MouseEncoding = MouseEncodingSGR
		} else if m.MouseEncoding == MouseEncodingSGR {
			m.MouseEncoding = MouseEncodingDefault
		}
	case decMouseUTF8, decMouseUrxvt:
		// No separate encoding state: dm's client encodes mouse input itself.
	case decSaveCursor:
		if on {
			g.SaveCursor(t.style)
		} else {
			t.style = g.RestoreCursor()
		}
		return
	case decALT1047:
		if on {
			g.EnterAltScreen(false)
		} else {
			g.ExitAltScreen(true)
		}
		return
	case decALT1049:
		if on {
			g.SaveCursor(t.style)
			g.EnterAltScreen(true)
		} else {
			g.ExitAltScreen(true)
			t.style = g.RestoreCursor()
		}
		return
	case decBracketedPaste:
		m.BracketedPaste = on
	default:
		// An unknown private mode is ignored, as a terminal ignores one it
		// does not have.
		return
	}
	g.SetModes(m)
}

// setMouse turns one mouse tracking mode on, or off when it is the current one.
func (t *Term) setMouse(mode MouseMode, on bool) {
	g := t.grid
	m := g.Modes()
	if on {
		m.Mouse = mode
	} else if m.Mouse == mode {
		m.Mouse = MouseOff
	}
	g.SetModes(m)
}

// originHome moves the cursor to the origin of the current margins when origin
// mode turns on, and to the absolute origin when it turns off, as DECOM does.
// Cursor addressing relative to the margins is the cursor family's job.
func (t *Term) originHome(on bool) {
	g := t.grid
	if !on {
		g.SetCursor(0, 0)
		return
	}
	r := g.ScrollRegion()
	g.SetCursor(r.Left, r.Top)
}

// setColumnMode applies DECCOLM. The width changes only when DECSET 40 allowed
// it, and the change clears the screen and the margins unless DECNCSM says
// otherwise.
func (t *Term) setColumnMode(on bool) {
	g := t.grid
	m := g.Modes()
	if !m.Allow80To132 || m.ColumnMode == on {
		return
	}
	m.ColumnMode = on
	g.SetModes(m)
	cols := 80
	if on {
		cols = 132
	}
	_, rows := g.Size()
	g.Resize(cols, rows)
	if !m.NoClearOnColumnMode || t.level < 5 {
		g.Clear()
	}
	g.resetScrollRegion()
	g.SetCursor(0, 0)
}

// csiSaveModes handles XTSAVE: remember each mode's current value.
func csiSaveModes(term *Term, seq CSI) {
	g := term.grid
	for _, n := range seq.Params {
		v, ok := term.privateMode(n)
		if !ok {
			continue
		}
		if g.savedModes == nil {
			g.savedModes = map[int]bool{}
		}
		g.savedModes[n] = v
	}
}

// csiRestoreModes handles XTRESTORE: put each saved mode value back. A mode
// that was never saved is reset, as xterm does.
func csiRestoreModes(term *Term, seq CSI) {
	for _, n := range seq.Params {
		term.setPrivateMode(n, term.grid.savedModes[n])
	}
}

// privateMode reports the current value of a DEC private mode the grid models.
func (t *Term) privateMode(n int) (value, ok bool) {
	g := t.grid
	m := g.Modes()
	switch n {
	case decCKM:
		return m.CursorKeysApplication, true
	case decCOLM:
		return m.ColumnMode, true
	case decSCNM:
		return m.ReverseVideo, true
	case decOM:
		return m.Origin, true
	case decAWM:
		return m.Wrap, true
	case decX10Mouse:
		return m.Mouse == MouseX10, true
	case decDECTCEM:
		return g.Cursor().Visible, true
	case decAllow80To132:
		return m.Allow80To132, true
	case decMoreFix:
		return m.MoreFix, true
	case decReverseWrap, decReverseWrap2:
		return m.ReverseWrap, true
	case decDECNKM:
		return m.ApplicationKeypad, true
	case decDECLRMM:
		return m.LeftRightMargin, true
	case decDECNCSM:
		return m.NoClearOnColumnMode, true
	case decMouseButton:
		return m.Mouse == MouseButton, true
	case decMouseMotion:
		return m.Mouse == MouseMotion, true
	case decMouseMany:
		return m.Mouse == MouseMany, true
	case decFocus:
		return m.FocusReporting, true
	case decMouseSGR:
		return m.MouseEncoding == MouseEncodingSGR, true
	case decBracketedPaste:
		return m.BracketedPaste, true
	default:
		return false, false
	}
}
