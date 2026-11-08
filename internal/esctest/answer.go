package esctest

import (
	"fmt"
	"strconv"
	"strings"

	"dm/internal/vt"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

// The pty is 25x80, the size esctest2 resets the terminal to before every
// test. The display is a synthetic screen large enough for the window tests.
const (
	cellWidth   = 10
	cellHeight  = 20
	displayCols = 192
	displayRows = 54
)

// permanentlyResetAnsi are the ANSI modes esctest2 expects a terminal to
// report as permanently reset (4).
var permanentlyResetDec = map[int]bool{
	60: true, // DECHCCM
}

var permanentlyResetAnsi = map[int]bool{
	1: true, 5: true, 7: true, 10: true, 11: true, 13: true,
	14: true, 15: true, 16: true, 17: true, 18: true, 19: true,
}

// titlePair is one entry on the title stack. A combined push stores both
// titles in one entry, and a pop of either kind removes the whole entry.
type titlePair struct {
	icon, window string
}

// state is what the harness tracks for the queries vt10x cannot answer.
type state struct {
	decModes  map[int]bool
	ansiModes map[int]bool

	stbmTop, stbmBottom int
	slrmLeft, slrmRight int
	sgr                 []int
	decsca              int
	decscl              string
	decsclLevel         int
	decscusr            int
	slpp                int
	smkr                int
	snls                int
	ssdt                int
	elf                 int
	lfkc                int
	sasd                int
	sace                int

	iconTitle   string
	windowTitle string
	titleStack  []titlePair
	winX, winY  int
	iconified   bool

	indexed map[int]rgb
	fg      rgb
	bg      rgb
	cursor  rgb

	selection string

	savedRows, savedCols int
	saved                bool
}

func newState(rows, cols int) state {
	return state{
		decModes:  map[int]bool{7: true, 25: true},
		ansiModes: map[int]bool{},
		stbmTop:   1, stbmBottom: rows,
		slrmLeft: 1, slrmRight: cols,
		decscl:      "65;1",
		decsclLevel: 5,
		snls:        24,
		indexed:     map[int]rgb{},
		fg:          defaultFG,
		bg:          defaultBG,
		cursor:      defaultCursor,
	}
}

func (st *state) softReset(rows, cols int) {
	st.decModes = map[int]bool{7: true, 25: true}
	st.ansiModes = map[int]bool{}
	st.sgr = nil
	st.decsca = 0
	st.decscusr = 0
	st.stbmTop, st.stbmBottom = 1, rows
	st.slrmLeft, st.slrmRight = 1, cols
	st.slpp = 0
	st.smkr = 0
	st.snls = 24
	st.ssdt = 0
	st.elf = 0
	st.lfkc = 0
	st.sasd = 0
	st.sace = 0
}

func (st *state) applySGR(params []int) {
	if len(params) == 0 {
		params = []int{0}
	}
	for i := 0; i < len(params); i++ {
		p := params[i]
		switch p {
		case 0:
			st.sgr = nil
		case 1, 3, 4, 5, 7, 8, 9:
			st.sgr = append(st.sgr, p)
		case 22:
			st.sgr = removeInt(st.sgr, 1)
		case 23:
			st.sgr = removeInt(st.sgr, 3)
		case 24:
			st.sgr = removeInt(st.sgr, 4)
		case 25:
			st.sgr = removeInt(st.sgr, 5)
		case 27:
			st.sgr = removeInt(st.sgr, 7)
		case 28:
			st.sgr = removeInt(st.sgr, 8)
		case 29:
			st.sgr = removeInt(st.sgr, 9)
		case 38, 48:
			if i+1 < len(params) && params[i+1] == 5 {
				i += 2
			} else if i+1 < len(params) && params[i+1] == 2 {
				i += 4
			}
		}
	}
}

func removeInt(s []int, v int) []int {
	out := s[:0]
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

// modeState reports a mode the way xterm's DECRQM does. It reads vt10x's
// state, so a mode vt10x does not implement reports 0 (not recognised).
func (h *harness) modeState(priv bool, mode int) int {
	if !priv {
		if permanentlyResetAnsi[mode] {
			return 4
		}
		var bit vt10x.ModeFlag
		switch mode {
		case 2: // KAM
			bit = vt10x.ModeKeyboardLock
		case 4: // IRM
			bit = vt10x.ModeInsert
		case 12: // SRM
			bit = vt10x.ModeEcho
		case 20: // LNM
			bit = vt10x.ModeCRLF
		default:
			return 0
		}
		if h.vt.Mode()&bit != 0 {
			return 1
		}
		return 2
	}
	if permanentlyResetDec[mode] {
		return 4
	}
	switch mode {
	case 1: // DECCKM
		if h.vt.Mode()&vt10x.ModeAppCursor != 0 {
			return 1
		}
		return 2
	case 5: // DECSCNM
		if h.vt.Mode()&vt10x.ModeReverse != 0 {
			return 1
		}
		return 2
	case 6: // DECOM, tracked because vt10x does not expose it
		if h.st.decModes[6] {
			return 1
		}
		return 2
	case 7: // DECAWM
		if h.vt.Mode()&vt10x.ModeWrap != 0 {
			return 1
		}
		return 2
	case 25: // DECTCEM
		if h.vt.Mode()&vt10x.ModeHide == 0 {
			return 1
		}
		return 2
	case 3, 4, 18, 19, 35, 42, 66, 67, 69:
		// Modes the new VT's mode family owns. Until that stream lands the
		// harness reports the state it observed from DECSET and DECRST, so
		// the report family is provable on its own.
		if h.st.decModes[mode] {
			return 1
		}
		return 2
	}
	return 0
}

// observe updates tracked state for a sequence that is also written to the
// terminal.
func (h *harness) observe(s seq) {
	switch s.kind {
	case ']':
		h.handleOSC(s.body)
	case 'c':
		h.observeCSI(s)
	}
}

func (h *harness) observeCSI(s seq) {
	switch s.final {
	case 'h', 'l':
		set := s.final == 'h'
		for _, p := range s.params {
			if p < 0 {
				continue
			}
			switch s.priv {
			case '?':
				h.st.decModes[p] = set
			case 0:
				h.st.ansiModes[p] = set
			}
		}
	case 'm':
		if s.priv == 0 && s.inter == "" {
			h.st.applySGR(s.params)
		}
	case 'r':
		switch s.inter {
		case "":
			top, bottom := 1, h.rows()
			if len(s.params) > 0 && s.params[0] > 0 {
				top = s.params[0]
			}
			if len(s.params) > 1 && s.params[1] > 0 {
				bottom = s.params[1]
			}
			h.st.stbmTop, h.st.stbmBottom = top, bottom
		case "+":
			h.st.smkr = firstParam(s.params, 0)
		}
	case 's':
		if s.priv == 0 && s.inter == "" && len(s.params) >= 2 {
			h.st.slrmLeft, h.st.slrmRight = s.params[0], s.params[1]
		}
	case 'p':
		switch s.inter {
		case "!":
			h.st.softReset(h.rows(), h.cols())
		case "\"":
			h.st.decscl = joinParams(s.params)
			h.st.decsclLevel = firstParam(s.params, 65) - 60
		}
	case 'q':
		switch s.inter {
		case "\"":
			h.st.decsca = firstParam(s.params, 0)
		case " ":
			h.st.decscusr = firstParam(s.params, 0)
		}
	case '|':
		if s.inter == "*" {
			h.st.snls = firstParam(s.params, 0)
		}
	case '~':
		if s.inter == "$" {
			h.st.ssdt = firstParam(s.params, 0)
		}
	case '}':
		switch s.inter {
		case "'":
			h.st.elf = firstParam(s.params, 0)
		case "*":
			h.st.lfkc = firstParam(s.params, 0)
		case "$":
			h.st.sasd = firstParam(s.params, 0)
		}
	case 'x':
		if s.inter == "*" {
			h.st.sace = firstParam(s.params, 0)
		}
	}
}

// answer replies to a handled sequence. raw is the sequence as it arrived, so
// a report the new VT owns can be handed over unchanged.
func (h *harness) answer(s seq, raw []byte) {
	switch s.kind {
	case 'Z':
		h.replyVT(raw, "DECID")
	case ']':
		h.handleOSC(s.body)
	case 'P':
		h.answerDCS(s.body)
	case 'c':
		h.answerCSI(s, raw)
	}
}

// replyVT sends the new VT's answer to the host.
func (h *harness) replyVT(raw []byte, name string) {
	if b := h.vtReport(raw); len(b) > 0 {
		h.reply(string(b), name)
	}
}

func (h *harness) answerCSI(s seq, raw []byte) {
	switch s.final {
	case 'c':
		if s.priv == '>' {
			h.replyVT(raw, "DA2")
		} else {
			h.replyVT(raw, "DA1")
		}
	case 'n':
		if s.priv == '?' && firstParam(s.params, 0) == 6 {
			h.replyVT(raw, "DECXCPR")
			return
		}
		h.answerDSR(s)
	case 't':
		h.answerWinop(s)
	case 'p':
		if s.inter == "$" {
			h.answerDECRQM(s)
		}
	case 'y':
		if s.inter == "*" {
			h.answerDECRQCRA(s)
		}
	case 'q':
		if s.priv == '>' {
			h.reply("\x1bP>|esctest-harness\x1b\\", "XTVERSION")
		}
	}
}

func (h *harness) answerDSR(s seq) {
	switch firstParam(s.params, 0) {
	case 15:
		h.reply("\x1b[?10n", "DSR printer port")
	case 25:
		h.reply("\x1b[?20n", "DSR UDK")
	case 26:
		h.reply("\x1b[?27;1;0;0n", "DSR keyboard")
	case 55:
		h.reply("\x1b[?50n", "DSR locator")
	case 56:
		h.reply("\x1b[?57;0n", "DSR locator type")
	case 62:
		h.reply("\x1b[0*{", "DECMSR")
	case 63:
		pid := paramAt(s.params, 1, 0)
		h.reply(fmt.Sprintf("\x1bP%d!~0000\x1b\\", pid), "DECCKSR")
	case 75:
		h.reply("\x1b[?70n", "DSR data integrity")
	case 85:
		h.reply("\x1b[?83n", "DSR multi-session")
	}
}

func (h *harness) answerWinop(s seq) {
	switch firstParam(s.params, 0) {
	case 1:
		h.st.iconified = false
	case 2:
		h.st.iconified = true
	case 3:
		h.st.winX = paramAt(s.params, 1, 0)
		h.st.winY = paramAt(s.params, 2, 0)
	case 4:
		height := paramAt(s.params, 1, -1)
		width := paramAt(s.params, 2, -1)
		rows, cols := h.rows(), h.cols()
		if height == 0 {
			rows = displayRows
		} else if height > 0 {
			rows = height / cellHeight
		}
		if width == 0 {
			cols = displayCols
		} else if width > 0 {
			cols = width / cellWidth
		}
		h.st.saved = false
		h.resize(rows, cols)
	case 8:
		rows := paramAt(s.params, 1, -1)
		cols := paramAt(s.params, 2, -1)
		if rows == 0 {
			rows = displayRows
		}
		if cols == 0 {
			cols = displayCols
		}
		if rows < 0 {
			rows = h.rows()
		}
		if cols < 0 {
			cols = h.cols()
		}
		h.st.saved = false
		h.resize(rows, cols)
	case 9, 10:
		mode := paramAt(s.params, 1, 0)
		if mode == 0 {
			if h.st.saved {
				h.resize(h.st.savedRows, h.st.savedCols)
				h.st.saved = false
			}
			break
		}
		if !h.st.saved {
			h.st.savedRows, h.st.savedCols = h.rows(), h.cols()
			h.st.saved = true
		}
		rows, cols := h.rows(), h.cols()
		switch mode {
		case 2: // vertical
			rows = displayRows
		case 3: // horizontal
			cols = displayCols
		default: // both
			rows, cols = displayRows, displayCols
		}
		h.resize(rows, cols)
	case 11:
		if h.st.iconified {
			h.reply("\x1b[2t", "window state")
		} else {
			h.reply("\x1b[1t", "window state")
		}
	case 13:
		h.reply(fmt.Sprintf("\x1b[3;%d;%dt", h.st.winX, h.st.winY), "window position")
	case 14:
		h.reply(fmt.Sprintf("\x1b[4;%d;%dt", h.rows()*cellHeight, h.cols()*cellWidth), "window size pixels")
	case 15:
		h.reply(fmt.Sprintf("\x1b[5;%d;%dt", displayRows*cellHeight, displayCols*cellWidth), "screen size pixels")
	case 16:
		h.reply(fmt.Sprintf("\x1b[6;%d;%dt", cellHeight, cellWidth), "char size pixels")
	case 18:
		h.reply(fmt.Sprintf("\x1b[8;%d;%dt", h.rows(), h.cols()), "text area chars")
	case 19:
		h.reply(fmt.Sprintf("\x1b[9;%d;%dt", displayRows, displayCols), "screen size chars")
	case 20:
		h.reply("\x1b]L"+h.st.iconTitle+"\x1b\\", "icon label")
	case 21:
		h.reply("\x1b]l"+h.st.windowTitle+"\x1b\\", "window title")
	case 22:
		switch paramAt(s.params, 1, 0) {
		case 1:
			h.st.titleStack = append(h.st.titleStack, titlePair{icon: h.st.iconTitle})
		case 2:
			h.st.titleStack = append(h.st.titleStack, titlePair{window: h.st.windowTitle})
		default:
			h.st.titleStack = append(h.st.titleStack, titlePair{icon: h.st.iconTitle, window: h.st.windowTitle})
		}
	case 23:
		if n := len(h.st.titleStack); n > 0 {
			pair := h.st.titleStack[n-1]
			h.st.titleStack = h.st.titleStack[:n-1]
			// xterm fills a missing title from the next entry down.
			if pair.icon == "" {
				for i := len(h.st.titleStack) - 1; i >= 0; i-- {
					if h.st.titleStack[i].icon != "" {
						pair.icon = h.st.titleStack[i].icon
						break
					}
				}
			}
			if pair.window == "" {
				for i := len(h.st.titleStack) - 1; i >= 0; i-- {
					if h.st.titleStack[i].window != "" {
						pair.window = h.st.titleStack[i].window
						break
					}
				}
			}
			switch paramAt(s.params, 1, 0) {
			case 1:
				if pair.icon != "" {
					h.st.iconTitle = pair.icon
				}
			case 2:
				if pair.window != "" {
					h.st.windowTitle = pair.window
				}
			default:
				if pair.icon != "" {
					h.st.iconTitle = pair.icon
				}
				if pair.window != "" {
					h.st.windowTitle = pair.window
				}
			}
		}
	default:
		if code := firstParam(s.params, 0); code >= 24 {
			h.st.slpp = code
			h.resize(code, h.cols())
		}
	}
}

func (h *harness) answerDECRQM(s seq) {
	if h.st.decsclLevel < 3 {
		return
	}
	p := firstParam(s.params, 0)
	state := h.modeState(s.priv == '?', p)
	if s.priv == '?' {
		h.reply(fmt.Sprintf("\x1b[?%d;%d$y", p, state), "DECRQM")
	} else {
		h.reply(fmt.Sprintf("\x1b[%d;%d$y", p, state), "DECRQM")
	}
}

func (h *harness) answerDECRQCRA(s seq) {
	pid := firstParam(s.params, 0)
	rest := s.params
	if len(rest) > 2 {
		rest = rest[2:]
	} else {
		rest = nil
	}
	top := paramAt(rest, 0, 1)
	left := paramAt(rest, 1, 1)
	bottom := paramAt(rest, 2, h.rows())
	right := paramAt(rest, 3, h.cols())
	sum := checksum(h.vt, top, left, bottom, right)
	h.reply(fmt.Sprintf("\x1bP%d!~%04X\x1b\\", pid, sum), "DECRQCRA")
}

func (h *harness) answerDCS(body string) {
	switch {
	case strings.HasPrefix(body, "$q"):
		if h.st.decsclLevel < 3 {
			return
		}
		pt := body[2:]
		h.reply("\x1bP"+h.st.decrqss(pt)+"\x1b\\", "DECRQSS")
	case strings.HasPrefix(body, "+q"):
		h.reply(vt.XTGETTCAPReply(body[2:]), "XTGETTCAP")
	}
}

func (st *state) decrqss(pt string) string {
	ok := func(v string) string { return "1$r" + v }
	switch pt {
	case "$}":
		return ok(strconv.Itoa(st.sasd) + "$}")
	case "*x":
		return ok(strconv.Itoa(st.sace) + "*x")
	case "\"q":
		return ok(strconv.Itoa(st.decsca) + "\"q")
	case "\"p":
		return ok(st.decscl + "\"p")
	case "r":
		return ok(fmt.Sprintf("%d;%d", st.stbmTop, st.stbmBottom) + "r")
	case "m":
		return ok("0" + sgrSuffix(st.sgr) + "m")
	case " q":
		return ok(strconv.Itoa(st.decscusr) + " q")
	case "s":
		return ok(fmt.Sprintf("%d;%d", st.slrmLeft, st.slrmRight) + "s")
	case "t":
		return ok(strconv.Itoa(st.slpp) + "t")
	case "*|":
		return ok(strconv.Itoa(st.snls) + "*|")
	case "$~":
		return ok(strconv.Itoa(st.ssdt) + "$~")
	}
	// xterm does not implement these, and esctest2 marks them as known bugs
	// for xterm. Report them as invalid so the suite sees xterm's behaviour.
	return "0$r" + pt
}

func sgrSuffix(sgr []int) string {
	if len(sgr) == 0 {
		return ""
	}
	parts := make([]string, len(sgr))
	for i, p := range sgr {
		parts[i] = strconv.Itoa(p)
	}
	return ";" + strings.Join(parts, ";")
}

func (h *harness) handleOSC(body string) {
	fields := strings.Split(body, ";")
	if len(fields) == 0 {
		return
	}
	switch fields[0] {
	case "0":
		t := strings.Join(fields[1:], ";")
		h.st.iconTitle, h.st.windowTitle = t, t
	case "1":
		h.st.iconTitle = strings.Join(fields[1:], ";")
	case "2":
		h.st.windowTitle = strings.Join(fields[1:], ";")
	case "4", "5":
		for i := 1; i+1 < len(fields); i += 2 {
			idx, err := strconv.Atoi(fields[i])
			if err != nil {
				continue
			}
			val := fields[i+1]
			if val == "?" {
				c := h.st.indexedColor(idx)
				h.reply(fmt.Sprintf("\x1b]%s;%d;%s\x1b\\", fields[0], idx, formatColor(c)), "OSC "+fields[0])
			} else if c, ok := parseColor(val); ok {
				h.st.indexed[idx] = c
			}
		}
	case "10", "11", "12":
		base, _ := strconv.Atoi(fields[0])
		for i := 1; i < len(fields); i++ {
			num := base + i - 1
			val := fields[i]
			if val == "?" {
				c := h.st.dynamicColor(num)
				h.reply(fmt.Sprintf("\x1b]%d;%s\x1b\\", num, formatColor(c)), "OSC dynamic")
			} else if c, ok := parseColor(val); ok {
				h.st.setDynamic(num, c)
			}
		}
	case "104", "105":
		for i := 1; i < len(fields); i++ {
			idx, err := strconv.Atoi(fields[i])
			if err != nil {
				continue
			}
			delete(h.st.indexed, idx)
		}
	case "110":
		h.st.fg = defaultFG
	case "111":
		h.st.bg = defaultBG
	case "112":
		h.st.cursor = defaultCursor
	case "52":
		if len(fields) >= 3 {
			if fields[2] == "?" {
				h.reply("\x1b]52;s0;"+h.st.selection+"\x1b\\", "OSC 52")
			} else {
				h.st.selection = fields[2]
			}
		}
	}
}

func (st *state) indexedColor(idx int) rgb {
	if c, ok := st.indexed[idx]; ok {
		return c
	}
	return defaultIndexed(idx)
}

func (st *state) dynamicColor(num int) rgb {
	switch num {
	case 10:
		return st.fg
	case 11:
		return st.bg
	case 12:
		return st.cursor
	}
	return defaultFG
}

func (st *state) setDynamic(num int, c rgb) {
	switch num {
	case 10:
		st.fg = c
	case 11:
		st.bg = c
	case 12:
		st.cursor = c
	}
}

func (h *harness) resize(rows, cols int) {
	if rows < 1 {
		rows = 1
	}
	if cols < 1 {
		cols = 1
	}
	if rows == h.rows() && cols == h.cols() {
		return
	}
	oldRows, oldCols := h.rows(), h.cols()
	h.vt.Resize(cols, rows)
	_ = pty.Setsize(h.ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if h.st.stbmBottom == oldRows {
		h.st.stbmBottom = rows
	}
	if h.st.slrmRight == oldCols {
		h.st.slrmRight = cols
	}
	h.logf("resize %dx%d", rows, cols)
}

func joinParams(params []int) string {
	parts := make([]string, len(params))
	for i, p := range params {
		if p < 0 {
			parts[i] = ""
		} else {
			parts[i] = strconv.Itoa(p)
		}
	}
	return strings.Join(parts, ";")
}

// checksum is xterm's DECRQCRA with checksumExtension 23: the sum of the
// character codes in the rectangle, erased cells as a space, attributes
// ignored, no trimming, no negation.
func checksum(vt vt10x.Terminal, top, left, bottom, right int) int {
	cols, rows := vt.Size()
	if top < 1 {
		top = 1
	}
	if left < 1 {
		left = 1
	}
	if bottom > rows {
		bottom = rows
	}
	if right > cols {
		right = cols
	}
	total := 0
	for y := top - 1; y <= bottom-1; y++ {
		for x := left - 1; x <= right-1; x++ {
			ch := vt.Cell(x, y).Char
			if ch == 0 {
				ch = ' '
			}
			total += int(ch)
		}
	}
	return total & 0xffff
}
