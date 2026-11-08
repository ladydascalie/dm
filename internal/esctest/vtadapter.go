package esctest

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"sync"
	"unicode/utf8"

	"dm/internal/vt"

	"github.com/hinshun/vt10x"
)

// vtAdapter makes the new VT (*vt.Term) satisfy vt10x.Terminal, so the
// harness can drive either terminal without changing its answers. The
// conversion is lossy in the ways the spec allows: a cell becomes its first
// rune, and only the style bits vt10x models survive.
type vtAdapter struct {
	term *vt.Term
	mu   sync.Mutex
}

var _ vt10x.Terminal = (*vtAdapter)(nil)

// vt10x glyph attribute bits. They are unexported in vt10x, so they are
// repeated here in vt10x's order.
const (
	attrReverse int16 = 1 << iota
	attrUnderline
	attrBold
	attrGfx
	attrItalic
	attrBlink
	attrWrap
)

func newVTAdapter(cols, rows int) *vtAdapter {
	return &vtAdapter{term: vt.NewTerm(cols, rows)}
}

// flush places the cluster the new VT is still building. The new VT keeps the
// last cluster pending until the next rune proves it cannot extend, so a read
// that needs the grid must place it first.
func (a *vtAdapter) flush() {
	a.term.Flush()
}

func (a *vtAdapter) Write(p []byte) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.term.Write(p)
	return len(p), nil
}

// Parse consumes r until EOF and feeds it to the terminal.
func (a *vtAdapter) Parse(bf *bufio.Reader) error {
	for {
		b, err := bf.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if _, err := a.Write([]byte{b}); err != nil {
			return err
		}
	}
}

func (a *vtAdapter) String() string {
	a.flush()
	g := a.term.Grid()
	_, rows := g.Size()
	lines := make([]string, rows)
	for y := range lines {
		lines[y] = g.Text(y)
	}
	return strings.Join(lines, "\n")
}

func (a *vtAdapter) Size() (cols, rows int) {
	return a.term.Grid().Size()
}

func (a *vtAdapter) Resize(cols, rows int) {
	a.term.Grid().Resize(cols, rows)
}

func (a *vtAdapter) Mode() vt10x.ModeFlag {
	a.flush()
	g := a.term.Grid()
	return toModeFlag(g.Modes(), g.Cursor())
}

func (a *vtAdapter) Title() string {
	return ""
}

func (a *vtAdapter) Cell(x, y int) vt10x.Glyph {
	a.flush()
	return toGlyph(a.term.Grid().Cell(x, y))
}

func (a *vtAdapter) Cursor() vt10x.Cursor {
	a.flush()
	c := a.term.Grid().Cursor()
	return vt10x.Cursor{X: c.X, Y: c.Y}
}

func (a *vtAdapter) CursorVisible() bool {
	a.flush()
	return a.term.Grid().Cursor().Visible
}

func (a *vtAdapter) Lock()   { a.mu.Lock() }
func (a *vtAdapter) Unlock() { a.mu.Unlock() }

// toGlyph converts one cell. The new VT keeps a whole grapheme cluster; vt10x
// keeps one rune, which is what xterm's checksum counts.
func toGlyph(c vt.Cell) vt10x.Glyph {
	ch, _ := utf8.DecodeRuneInString(c.Content)
	if ch == utf8.RuneError && c.Content == "" {
		ch = ' '
	}
	return vt10x.Glyph{
		Char: ch,
		Mode: styleMode(c.Style),
		FG:   toColor(c.Style.Fg, true),
		BG:   toColor(c.Style.Bg, false),
	}
}

// styleMode maps the new VT's attribute bits onto vt10x's glyph bits. Faint,
// conceal and strikethrough have no vt10x bit and are dropped.
func styleMode(st vt.Style) int16 {
	var mode int16
	if st.Attrs&vt.Bold != 0 {
		mode |= attrBold
	}
	if st.Attrs&vt.Italic != 0 {
		mode |= attrItalic
	}
	if st.Attrs&(vt.Blink|vt.RapidBlink) != 0 {
		mode |= attrBlink
	}
	if st.Attrs&vt.Reverse != 0 {
		mode |= attrReverse
	}
	if st.Underline != vt.UnderlineNone {
		mode |= attrUnderline
	}
	return mode
}

// toColor maps a new VT colour to vt10x's encoding: 0..15 ANSI, 16..255 the
// xterm palette, and a 24-bit colour packed into the low bits.
func toColor(c vt.Color, fg bool) vt10x.Color {
	switch c.Kind {
	case vt.ColorPalette:
		return vt10x.Color(c.Index)
	case vt.ColorRGB:
		return vt10x.Color(uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B))
	default:
		if fg {
			return vt10x.DefaultFG
		}
		return vt10x.DefaultBG
	}
}

// toModeFlag maps the new VT's modes onto vt10x's flags. A hidden cursor is
// ModeHide, as vt10x reports it.
func toModeFlag(m vt.Modes, c vt.Cursor) vt10x.ModeFlag {
	var f vt10x.ModeFlag
	if m.Wrap {
		f |= vt10x.ModeWrap
	}
	if m.Insert {
		f |= vt10x.ModeInsert
	}
	if m.AltScreen {
		f |= vt10x.ModeAltScreen
	}
	if m.ReverseVideo {
		f |= vt10x.ModeReverse
	}
	if m.FocusReporting {
		f |= vt10x.ModeFocus
	}
	if !c.Visible {
		f |= vt10x.ModeHide
	}
	switch m.Mouse {
	case vt.MouseX10:
		f |= vt10x.ModeMouseX10
	case vt.MouseButton:
		f |= vt10x.ModeMouseButton
	case vt.MouseMotion:
		f |= vt10x.ModeMouseMotion
	case vt.MouseMany:
		f |= vt10x.ModeMouseMany
	}
	if m.MouseEncoding == vt.MouseEncodingSGR {
		f |= vt10x.ModeMouseSgr
	}
	return f
}
