package esctest

import (
	"fmt"
	"strconv"
	"strings"

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

// titlePair is one entry on the title stack. A combined push stores both
// titles in one entry, and a pop of either kind removes the whole entry.
type titlePair struct {
	icon, window string
}

// state is what the harness tracks for the queries the terminal does not
// answer: the window, the title stack and the OSC colours.
type state struct {
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
		indexed: map[int]rgb{},
		fg:      defaultFG,
		bg:      defaultBG,
		cursor:  defaultCursor,
	}
}

// observe updates tracked state for a sequence that is also written to the
// terminal. The report family used to need mode and DECRQSS tracking here; the
// terminal answers those now, so only the OSC state is left.
func (h *harness) observe(s seq) {
	if s.kind == ']' {
		h.handleOSC(s.body)
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
		h.answerDCS(raw, s.body)
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
		// A plain cursor-position report belongs to the terminal under test.
		if s.priv == 0 && firstParam(s.params, 0) == 6 {
			h.replyVT(raw, "DSR")
			return
		}
		h.answerDSR(s)
	case 't':
		// The window operations are the harness's, but the terminal records
		// the page height a DECRQSS later reports.
		_ = h.vtReport(raw)
		h.answerWinop(s)
	case 'p':
		if s.inter == "$" {
			h.replyVT(raw, "DECRQM")
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
			h.resize(code, h.cols())
		}
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
	top, left, bottom, right = h.decrqcraRect(top, left, bottom, right)
	sum := checksum(h.vt, top, left, bottom, right)
	h.reply(fmt.Sprintf("\x1bP%d!~%04X\x1b\\", pid, sum), "DECRQCRA")
}

// decrqcraRect applies the origin-mode offset to a DECRQCRA rectangle. In origin
// mode xterm reads the rectangle relative to the top and left margins, as it
// reads the cursor. The margins come from the report terminal, which the
// harness keeps in step with the terminal under test: the vt10x view does not
// expose them, and the report terminal sees every margin and mode sequence.
func (h *harness) decrqcraRect(top, left, bottom, right int) (int, int, int, int) {
	g := h.reports.Grid()
	if g == nil || !g.Modes().Origin {
		return top, left, bottom, right
	}
	r := g.ScrollRegion()
	return top + r.Top, left + r.Left, bottom + r.Top, right + r.Left
}

func (h *harness) answerDCS(raw []byte, body string) {
	switch {
	case strings.HasPrefix(body, "$q"):
		h.replyVT(raw, "DECRQSS")
	case strings.HasPrefix(body, "+q"):
		h.replyVT(raw, "XTGETTCAP")
	}
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
	h.vt.Resize(cols, rows)
	_ = pty.Setsize(h.ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	h.logf("resize %dx%d", rows, cols)
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
