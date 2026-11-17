package ui

import (
	"bytes"
	"fmt"
	"strings"

	"dm/internal/cells"
	"dm/internal/layout"
	"dm/internal/theme"
)

// caretGlyph is the cell the drawn caret paints. It is not a glyph-set glyph:
// every set paints the same half block, and the ASCII set has no substitute for
// it.
const caretGlyph = "▏"

// Input is one line of editable text: the runes and the caret. The caller owns
// both between frames and mutates them through the methods; DrawInput reads the
// value and never writes it, so a resize leaves no state behind.
//
// The value is kept fit to paint after every method: Caret stays in
// [0, len(Value)], so no edit can push the caret off the runes.
type Input struct {
	Value []rune // Value is the text.
	Caret int    // Caret is the rune index the caret sits before, 0 to len(Value).
}

// InputProps is one frame of an input row: what it owns, what it names and the
// colours it paints with. The caller builds it, DrawInput reads it, and nothing
// in it survives the frame.
type InputProps struct {
	Rect     layout.Rect // Rect is the one row the input owns.
	Prompt   string      // Prompt is the fixed label before the text.
	Trailing string      // Trailing is the text on the row's right edge, gap included.
	Ground   theme.RGB   // Ground is the row's ground, filled to Rect.
	Dim      theme.RGB   // Dim is the prompt and the trailing text.
	Text     theme.RGB   // Text is the value.
	Caret    theme.RGB   // Caret is the caret cell.
	Active   bool        // Active says whether the caret cell is painted.
}

// SetValue replaces the text and leaves the caret after the last rune.
func (in *Input) SetValue(s string) {
	in.Value = []rune(s)
	in.Caret = len(in.Value)
}

// InsertRune puts r at the caret and moves the caret after it.
func (in *Input) InsertRune(r rune) {
	at := in.pos()
	in.Value = append(in.Value, 0)
	copy(in.Value[at+1:], in.Value[at:])
	in.Value[at] = r
	in.Caret = at + 1
}

// InsertString puts s at the caret and moves the caret after it. An empty
// string changes nothing.
func (in *Input) InsertString(s string) {
	rs := []rune(s)
	if len(rs) == 0 {
		return
	}
	at := in.pos()
	out := make([]rune, 0, len(in.Value)+len(rs))
	out = append(out, in.Value[:at]...)
	out = append(out, rs...)
	out = append(out, in.Value[at:]...)
	in.Value = out
	in.Caret = at + len(rs)
}

// Backspace removes the rune before the caret and moves the caret back onto
// where it stood. At the start of the text it changes nothing.
func (in *Input) Backspace() {
	at := in.pos()
	if at == 0 {
		in.Caret = 0
		return
	}
	in.Value = append(in.Value[:at-1], in.Value[at:]...)
	in.Caret = at - 1
}

// Delete removes the rune under the caret and leaves the caret where it stood.
// At the end of the text it changes nothing.
func (in *Input) Delete() {
	at := in.pos()
	if at >= len(in.Value) {
		in.Caret = len(in.Value)
		return
	}
	in.Value = append(in.Value[:at], in.Value[at+1:]...)
	in.Caret = at
}

// Left moves the caret one rune towards the start, and stops there.
func (in *Input) Left() {
	at := in.pos()
	if at > 0 {
		at--
	}
	in.Caret = at
}

// Right moves the caret one rune towards the end, and stops there.
func (in *Input) Right() {
	at := in.pos()
	if at < len(in.Value) {
		at++
	}
	in.Caret = at
}

// Home moves the caret to the start of the text.
func (in *Input) Home() {
	in.Caret = 0
}

// End moves the caret past the last rune.
func (in *Input) End() {
	in.Caret = len(in.Value)
}

// Kill cuts the text from the caret to the end and leaves the caret where it
// stood, so a second Kill changes nothing.
func (in *Input) Kill() {
	in.Caret = in.pos()
	in.Value = in.Value[:in.Caret]
}

// String is the text, without the caret.
func (in *Input) String() string {
	return string(in.Value)
}

// Empty reports whether the text is the empty string.
func (in *Input) Empty() bool {
	return len(in.Value) == 0
}

// pos is the caret clamped to the text, so a caller that sets Value directly
// and leaves Caret behind cannot push the next edit past the runes.
func (in *Input) pos() int {
	switch {
	case in.Caret < 0:
		return 0
	case in.Caret > len(in.Value):
		return len(in.Value)
	}
	return in.Caret
}

// DrawInput paints one input row into buf and returns the cell the terminal
// parks its cursor on: the caret's cell. It fills Rect with Ground, paints the
// prompt in Dim, the value in Text, and the trailing text in Dim on the row's
// right edge. The caret cell is painted only when Active, so an inactive input
// leaves the row to the caller's cursor.
//
// The caret stays on screen. When the text before it outgrows the room the
// prompt and the trailing text leave, the head is elided with the glyph set's
// mark, and the text after the caret is cut to the cells that remain. No edit
// and no width can push the caret off the row, and no cell of Rect is left to
// the frame before it.
func DrawInput(buf *bytes.Buffer, th theme.Theme, p InputProps, in Input) (cx, cy int) {
	r := p.Rect
	if r.W <= 0 || r.H <= 0 {
		return r.X, r.Y
	}
	caretCells := 0
	if p.Active {
		caretCells = cells.Cells(caretGlyph)
	}
	// The prompt keeps its cells before the trailing text claims any, and the
	// caret keeps its cell before both: a prompt wider than the row is cut
	// short rather than painted over the caret.
	prompt := cells.Clip(p.Prompt, max(r.W-caretCells, 0))
	trailing := cells.Clip(p.Trailing, max(r.W-caretCells-cells.Cells(prompt), 0))
	room := max(r.W-caretCells-cells.Cells(prompt)-cells.Cells(trailing), 0)

	at := in.Caret
	if at < 0 {
		at = 0
	}
	if at > len(in.Value) {
		at = len(in.Value)
	}
	head := string(in.Value[:at])
	if cells.Cells(head) > room {
		// The mark then the tail of the head: the cells beside the caret stay
		// readable and the caret stays on screen.
		if mark := th.Glyphs.ElideMark(); room > cells.Cells(mark) {
			head = mark + cells.ClipTail(head, room-cells.Cells(mark))
		} else {
			head = ""
		}
	}
	tail := cells.Clip(string(in.Value[at:]), room-cells.Cells(head))

	// The caret sits after the head it can show, clamped inside the row: a
	// row with no caret cell parks the cursor on its last cell, never past
	// it.
	cx = r.X + cells.Cells(prompt) + cells.Cells(head)
	if maxX := r.X + r.W - 1; cx > maxX {
		cx = maxX
	}
	cy = r.Y

	// The ground is live from the row escape, so a run only names its own
	// foreground; a second ground escape would repaint the last run's
	// foreground to the terminal default.
	used := 0
	emit := func(text string, fg theme.RGB) {
		buf.WriteString(theme.Style{Fg: styleColor(fg), Bg: styleColor(p.Ground)}.SGR())
		buf.WriteString(text)
		used += cells.Cells(text)
	}
	fmt.Fprintf(buf, "\x1b[%d;%dH", r.Y+1, r.X+1)
	buf.WriteString(theme.Style{Bg: styleColor(p.Ground)}.SGR())
	if prompt != "" {
		emit(prompt, p.Dim)
	}
	emit(head, p.Text)
	if p.Active {
		emit(caretGlyph, p.Caret)
	}
	if tail != "" {
		emit(tail, p.Text)
	}
	if trailing != "" {
		if gap := r.W - used - cells.Cells(trailing); gap > 0 {
			buf.WriteString(theme.Style{Bg: styleColor(p.Ground)}.SGR())
			buf.WriteString(strings.Repeat(" ", gap))
			used += gap
		}
		emit(trailing, p.Dim)
	}
	if n := r.W - used; n > 0 {
		buf.WriteString(strings.Repeat(" ", n))
	}
	buf.WriteString("\x1b[0m")
	return cx, cy
}

// styleColor names a theme colour the way a style escape reads it.
func styleColor(c theme.RGB) theme.Color {
	return theme.RGBColor(uint8(c.R), uint8(c.G), uint8(c.B))
}
