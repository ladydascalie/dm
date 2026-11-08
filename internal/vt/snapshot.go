package vt

import (
	"encoding/json"
	"errors"
	"fmt"
)

// SnapshotVersion is the wire version of Snapshot. A snapshot of another
// version is refused, so a client and a daemon of different builds never
// misread each other's state.
const SnapshotVersion = 1

var (
	// ErrSnapshotVersion reports a snapshot the receiver cannot decode.
	ErrSnapshotVersion = errors.New("vt: unsupported snapshot version")
	// ErrSnapshotShape reports a snapshot whose grid does not match its size.
	ErrSnapshotShape = errors.New("vt: malformed snapshot")
)

// Snapshot is the visible state of a terminal: the grid, the cursor, the
// modes, the scroll region, the tab stops, the saved cursors, the charset, and
// the parser's pending state. It is the state an ANSI replay cannot express.
//
// The hidden alternate screen is not carried. A snapshot of a terminal on the
// alternate screen restores a blank primary screen, which is what the spec's
// visible-screen-only rule settles.
type Snapshot struct {
	Version    int              `json:"v"`
	Cols       int              `json:"cw"`
	Rows       int              `json:"ch"`
	Lines      []SnapshotRow    `json:"l"`
	Cursor     SnapshotCursor   `json:"c"`
	Modes      Modes            `json:"m"`
	Scroll     SnapshotScroll   `json:"r"`
	Tabs       []int            `json:"t,omitempty"`
	Saved      [2]SnapshotSaved `json:"s"`
	Charset    SnapshotCharset  `json:"h,omitzero"`
	Parser     SnapshotParser   `json:"p,omitzero"`
	State      SnapshotState    `json:"x"`
	Styles     []SnapshotStyle  `json:"y,omitempty"`
	SavedModes map[int]bool     `json:"sm,omitempty"`
}

// SnapshotRow is one row of the visible grid. A row with no cells and no wrap
// mark encodes as JSON null, because most rows are blank.
type SnapshotRow struct {
	Cells   []SnapshotCell `json:"c,omitempty"`
	Wrapped bool           `json:"w,omitempty"`
}

// MarshalJSON writes an empty row as null.
func (r SnapshotRow) MarshalJSON() ([]byte, error) {
	if len(r.Cells) == 0 && !r.Wrapped {
		return []byte("null"), nil
	}
	type row SnapshotRow
	return json.Marshal(row(r))
}

// SnapshotCursor is the cursor and its pending wrap.
type SnapshotCursor struct {
	X        int  `json:"x"`
	Y        int  `json:"y"`
	Visible  bool `json:"v,omitempty"`
	WrapNext bool `json:"w,omitempty"`
}

// SnapshotScroll is the scroll region.
type SnapshotScroll struct {
	Top    int `json:"t"`
	Bottom int `json:"b"`
	Left   int `json:"l"`
	Right  int `json:"r"`
}

// SnapshotCharset is the G0/G1 designation and the active GL. The zero value
// is ASCII in G0 and G1 with G0 active, so a default charset encodes as {}.
type SnapshotCharset struct {
	G0 CharsetSet `json:"0,omitempty"`
	G1 CharsetSet `json:"1,omitempty"`
	GL int        `json:"l,omitempty"`
}

// SnapshotSaved is one DECSC slot. The cursor, style, charset, origin mode and
// protection survive, because DECRC restores all of them.
type SnapshotSaved struct {
	Cursor     SnapshotCursor  `json:"c"`
	Style      SnapshotStyle   `json:"s,omitzero"`
	Charset    SnapshotCharset `json:"h,omitzero"`
	Origin     bool            `json:"o,omitempty"`
	Protection uint8           `json:"p,omitempty"`
}

// SnapshotParser is the byte state machine's pending state. A sequence, a
// cluster or a rune split across a chunk boundary resumes exactly where it
// stopped.
type SnapshotParser struct {
	State       parserState `json:"s,omitempty"`
	Cluster     []byte      `json:"c,omitempty"`
	Pending     []byte      `json:"p,omitempty"`
	Esc         []byte      `json:"e,omitempty"`
	Seq         []byte      `json:"q,omitempty"`
	Str         []byte      `json:"t,omitempty"`
	CharsetSlot byte        `json:"d,omitempty"`
}

// SnapshotState is the terminal state outside the grid: the pen, protection,
// the conformance level and the OSC state. It carries what a report or a later
// write depends on, so a restore is not a screen copy that answers differently.
type SnapshotState struct {
	Style         SnapshotStyle  `json:"s,omitzero"`
	Protection    uint8          `json:"p,omitempty"`
	Level         int            `json:"l,omitempty"`
	IconTitle     string         `json:"i,omitempty"`
	WindowTitle   string         `json:"w,omitempty"`
	Link          string         `json:"k,omitempty"`
	Clipboard     string         `json:"cb,omitempty"`
	Status        string         `json:"st,omitempty"`
	Colors        map[int]uint32 `json:"col,omitempty"`
	CursorStyle   int            `json:"cs,omitempty"`
	ScreenLines   int            `json:"sl,omitempty"`
	StatusType    int            `json:"stt,omitempty"`
	ActiveStatus  int            `json:"as,omitempty"`
	ChangeExtent  int            `json:"ce,omitempty"`
	LocalFuncs    int            `json:"lf,omitempty"`
	LocalKeys     int            `json:"lk,omitempty"`
	ModifierKeys  int            `json:"mk,omitempty"`
	LinesPerPage  int            `json:"lp,omitempty"`
	SevenBit      int            `json:"sb"`
	DecModeState  map[int]bool   `json:"dm,omitempty"`
	AnsiModeState map[int]bool   `json:"am,omitempty"`
}

// SnapshotStyle is the compact wire form of Style. The zero value is the
// default style, so a default cell carries no style at all.
type SnapshotStyle struct {
	Fg             uint32         `json:"f,omitempty"`
	Bg             uint32         `json:"b,omitempty"`
	UnderlineColor uint32         `json:"u,omitempty"`
	Underline      UnderlineStyle `json:"l,omitempty"`
	Attrs          uint8          `json:"a,omitempty"`
}

// SnapshotCell is one slot of a row. Style indexes Snapshot.Styles, where
// index 0 is the default style, so a screen that repeats one style carries it
// once. A normal-width cell of default style encodes as a JSON string; a blank
// one encodes as "". Anything else encodes as an object. A continuation slot
// of a wide cell encodes as {"k":true}.
type SnapshotCell struct {
	Text         string
	Width        int8
	Style        uint16
	Link         string
	Protected    bool
	ISOProtected bool
}

// snapshotCellJSON is the object form of SnapshotCell.
type snapshotCellJSON struct {
	Text  string `json:"c,omitempty"`
	Wide  bool   `json:"w,omitempty"`
	Cont  bool   `json:"k,omitempty"`
	Style uint16 `json:"s,omitempty"`
	Link  string `json:"l,omitempty"`
	Prot  uint8  `json:"p,omitempty"`
}

// rgbColorFlag marks an RGB colour in the packed uint32. Palette indices stay
// below it, so a packed value is unambiguous.
const rgbColorFlag = 1 << 24

// encodeColor packs a Color into a uint32: 0 is the default, 1+n a palette
// index, and rgbColorFlag|RGB an RGB colour.
func encodeColor(c Color) uint32 {
	switch c.Kind {
	case ColorPalette:
		return uint32(c.Index) + 1
	case ColorRGB:
		return rgbColorFlag | uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B)
	}
	return 0
}

// decodeColor unpacks a uint32 written by encodeColor.
func decodeColor(v uint32) Color {
	switch {
	case v == 0:
		return DefaultColor()
	case v&rgbColorFlag != 0:
		return RGBColor(uint8(v>>16), uint8(v>>8), uint8(v))
	default:
		return PaletteColor(uint8(v - 1))
	}
}

// encodeStyle converts a Style to its wire form.
func encodeStyle(s Style) SnapshotStyle {
	return SnapshotStyle{
		Fg:             encodeColor(s.Fg),
		Bg:             encodeColor(s.Bg),
		UnderlineColor: encodeColor(s.UnderlineColor),
		Underline:      s.Underline,
		Attrs:          s.Attrs,
	}
}

// style converts a wire style back to a Style.
func (s SnapshotStyle) style() Style {
	return Style{
		Fg:             decodeColor(s.Fg),
		Bg:             decodeColor(s.Bg),
		UnderlineColor: decodeColor(s.UnderlineColor),
		Underline:      s.Underline,
		Attrs:          s.Attrs,
	}
}

// encodeProtection packs the two protection bits.
func encodeProtection(p Protection) uint8 {
	var v uint8
	if p.DECSCA {
		v |= 1
	}
	if p.ISO {
		v |= 2
	}
	return v
}

// decodeProtection unpacks the two protection bits.
func decodeProtection(v uint8) Protection {
	return Protection{DECSCA: v&1 != 0, ISO: v&2 != 0}
}

// newSnapshotCell converts one grid cell to its wire form. style indexes the
// snapshot's style table.
func newSnapshotCell(c Cell, style uint16) SnapshotCell {
	return SnapshotCell{
		Text:         c.Content,
		Width:        c.Width,
		Style:        style,
		Link:         c.Link,
		Protected:    c.Protected,
		ISOProtected: c.ISOProtected,
	}
}

// cell converts a wire cell back to a grid cell, resolving its style index.
func (c SnapshotCell) cell(styles []SnapshotStyle) Cell {
	text := c.Text
	width := c.Width
	if width == WidthContinuation {
		return Continuation()
	}
	if text == "" {
		text = " "
	}
	if width != WidthWide {
		width = WidthNormal
	}
	st := DefaultStyle()
	if int(c.Style) < len(styles) {
		st = styles[c.Style].style()
	}
	return Cell{
		Content:      text,
		Width:        width,
		Style:        st,
		Link:         c.Link,
		Protected:    c.Protected,
		ISOProtected: c.ISOProtected,
	}
}

// cloneBytes copies b, so a snapshot and the terminal do not share a buffer.
func cloneBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return append([]byte(nil), b...)
}

// isDefaultBlank reports whether c paints nothing and carries no state a
// restore would lose.
func isDefaultBlank(c Cell) bool {
	return c.Width == WidthNormal && c.Content == " " && c.Style == DefaultStyle() &&
		c.Link == "" && !c.Protected && !c.ISOProtected
}

// MarshalJSON writes the compact form: a string for a plain or blank cell, an
// object for everything else.
func (c SnapshotCell) MarshalJSON() ([]byte, error) {
	plain := c.Width == WidthNormal && c.Style == 0 &&
		c.Link == "" && !c.Protected && !c.ISOProtected
	if plain {
		if c.Text == " " {
			return []byte(`""`), nil
		}
		return json.Marshal(c.Text)
	}
	o := snapshotCellJSON{
		Text:  c.Text,
		Style: c.Style,
		Link:  c.Link,
		Prot:  encodeProtection(Protection{DECSCA: c.Protected, ISO: c.ISOProtected}),
	}
	switch c.Width {
	case WidthContinuation:
		o.Text = ""
		o.Cont = true
	case WidthWide:
		o.Wide = true
	}
	return json.Marshal(o)
}

// UnmarshalJSON reads either the string form or the object form.
func (c *SnapshotCell) UnmarshalJSON(b []byte) error {
	if len(b) == 0 {
		return fmt.Errorf("%w: empty cell", ErrSnapshotShape)
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		if s == "" {
			*c = SnapshotCell{Text: " ", Width: WidthNormal}
			return nil
		}
		*c = SnapshotCell{Text: s, Width: WidthNormal}
		return nil
	}
	var o snapshotCellJSON
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	*c = SnapshotCell{
		Text:         o.Text,
		Width:        WidthNormal,
		Style:        o.Style,
		Link:         o.Link,
		Protected:    o.Prot&1 != 0,
		ISOProtected: o.Prot&2 != 0,
	}
	if o.Cont {
		*c = SnapshotCell{Width: WidthContinuation}
		return nil
	}
	if o.Wide {
		c.Width = WidthWide
	}
	if c.Text == "" {
		c.Text = " "
	}
	return nil
}

// Encode returns the snapshot as JSON.
func (s *Snapshot) Encode() ([]byte, error) {
	return json.Marshal(s)
}

// DecodeSnapshot parses a JSON snapshot and refuses another version.
func DecodeSnapshot(b []byte) (*Snapshot, error) {
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.Version != SnapshotVersion {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrSnapshotVersion, s.Version, SnapshotVersion)
	}
	return &s, nil
}

// validate checks the shape a restore relies on.
func (s *Snapshot) validate() error {
	if s.Version != SnapshotVersion {
		return fmt.Errorf("%w: got %d, want %d", ErrSnapshotVersion, s.Version, SnapshotVersion)
	}
	if s.Cols < 1 || s.Rows < 1 {
		return fmt.Errorf("%w: grid is %dx%d", ErrSnapshotShape, s.Cols, s.Rows)
	}
	if len(s.Lines) != s.Rows {
		return fmt.Errorf("%w: %d rows for height %d", ErrSnapshotShape, len(s.Lines), s.Rows)
	}
	for y, row := range s.Lines {
		if len(row.Cells) > s.Cols {
			return fmt.Errorf("%w: row %d has %d cells for width %d", ErrSnapshotShape, y, len(row.Cells), s.Cols)
		}
		for x, c := range row.Cells {
			if int(c.Style) >= len(s.Styles) && c.Style != 0 {
				return fmt.Errorf("%w: row %d cell %d style %d of %d", ErrSnapshotShape, y, x, c.Style, len(s.Styles))
			}
		}
	}
	return nil
}

// Snapshot returns the terminal's visible state.
func (t *Term) Snapshot() *Snapshot {
	g := t.grid
	styles := []SnapshotStyle{encodeStyle(DefaultStyle())}
	styleIndex := map[SnapshotStyle]uint16{styles[0]: 0}
	styleOf := func(st Style) uint16 {
		coded := encodeStyle(st)
		if i, ok := styleIndex[coded]; ok {
			return i
		}
		i := uint16(len(styles))
		styles = append(styles, coded)
		styleIndex[coded] = i
		return i
	}
	s := &Snapshot{
		Version: SnapshotVersion,
		Cols:    g.cols,
		Rows:    g.rows,
		Lines:   make([]SnapshotRow, g.rows),
		Cursor: SnapshotCursor{
			X: g.cursor.X, Y: g.cursor.Y,
			Visible: g.cursor.Visible, WrapNext: g.cursor.WrapNext,
		},
		Modes:   g.modes,
		Scroll:  SnapshotScroll{Top: g.scroll.Top, Bottom: g.scroll.Bottom, Left: g.scroll.Left, Right: g.scroll.Right},
		Charset: SnapshotCharset{G0: g.charset.G0, G1: g.charset.G1, GL: g.charset.GL},
		Parser: SnapshotParser{
			State:       t.parser.state,
			Cluster:     cloneBytes(t.parser.cluster),
			Pending:     cloneBytes(t.parser.pending),
			Esc:         cloneBytes(t.parser.esc),
			Seq:         cloneBytes(t.parser.seq),
			Str:         cloneBytes(t.parser.str),
			CharsetSlot: t.parser.charsetSlot,
		},
		State: SnapshotState{
			Style:        encodeStyle(t.style),
			Protection:   encodeProtection(t.protection),
			Level:        t.level,
			IconTitle:    t.iconTitle,
			WindowTitle:  t.windowTitle,
			Link:         t.link,
			Clipboard:    t.clipboard,
			Status:       t.status,
			CursorStyle:  t.cursorStyle,
			ScreenLines:  t.screenLines,
			StatusType:   t.statusType,
			ActiveStatus: t.activeStatus,
			ChangeExtent: t.changeExtent,
			LocalFuncs:   t.localFuncs,
			LocalKeys:    t.localKeys,
			ModifierKeys: t.modifierKeys,
			LinesPerPage: t.linesPerPage,
			SevenBit:     t.sevenBit,
		},
	}
	for y := range g.lines {
		line := g.lines[y]
		n := len(line)
		for n > 0 && isDefaultBlank(line[n-1]) {
			n--
		}
		row := SnapshotRow{Wrapped: g.wrapped[y]}
		if n > 0 {
			row.Cells = make([]SnapshotCell, n)
			for x := range row.Cells {
				row.Cells[x] = newSnapshotCell(line[x], styleOf(line[x].Style))
			}
		}
		s.Lines[y] = row
	}
	if len(styles) > 1 {
		s.Styles = styles
	}
	for x, on := range g.tabs {
		if on {
			s.Tabs = append(s.Tabs, x)
		}
	}
	for i := range g.saved {
		sc := g.saved[i]
		s.Saved[i] = SnapshotSaved{
			Cursor: SnapshotCursor{
				X: sc.Cursor.X, Y: sc.Cursor.Y,
				Visible: sc.Cursor.Visible, WrapNext: sc.Cursor.WrapNext,
			},
			Style:      encodeStyle(sc.Style),
			Charset:    SnapshotCharset{G0: sc.Charset.G0, G1: sc.Charset.G1, GL: sc.Charset.GL},
			Origin:     sc.Origin,
			Protection: encodeProtection(sc.Protection),
		}
	}
	if len(g.savedModes) > 0 {
		s.SavedModes = make(map[int]bool, len(g.savedModes))
		for k, v := range g.savedModes {
			s.SavedModes[k] = v
		}
	}
	if len(t.colors) > 0 {
		s.State.Colors = make(map[int]uint32, len(t.colors))
		for k, v := range t.colors {
			s.State.Colors[k] = encodeColor(v)
		}
	}
	if len(t.decModeStates) > 0 {
		s.State.DecModeState = make(map[int]bool, len(t.decModeStates))
		for k, v := range t.decModeStates {
			s.State.DecModeState[k] = v
		}
	}
	if len(t.ansiModeStates) > 0 {
		s.State.AnsiModeState = make(map[int]bool, len(t.ansiModeStates))
		for k, v := range t.ansiModeStates {
			s.State.AnsiModeState[k] = v
		}
	}
	return s
}

// Restore replaces the terminal's state with s. A snapshot of another version
// or the wrong shape is refused and leaves the terminal unchanged.
func (t *Term) Restore(s *Snapshot) error {
	if s == nil {
		return fmt.Errorf("%w: nil", ErrSnapshotShape)
	}
	if err := s.validate(); err != nil {
		return err
	}
	g := NewGrid(s.Cols, s.Rows)
	for y := range g.lines {
		row := s.Lines[y]
		for x := 0; x < s.Cols && x < len(row.Cells); x++ {
			g.lines[y][x] = row.Cells[x].cell(s.Styles)
		}
		g.repairLine(y)
		g.wrapped[y] = row.Wrapped
	}
	g.cursor = Cursor{
		X:        clamp(s.Cursor.X, 0, s.Cols-1),
		Y:        clamp(s.Cursor.Y, 0, s.Rows-1),
		Visible:  s.Cursor.Visible,
		WrapNext: s.Cursor.WrapNext,
	}
	g.modes = s.Modes
	g.SetScrollRegion(ScrollRegion{
		Top: s.Scroll.Top, Bottom: s.Scroll.Bottom,
		Left: s.Scroll.Left, Right: s.Scroll.Right,
	})
	g.charset = Charset{G0: s.Charset.G0, G1: s.Charset.G1, GL: s.Charset.GL}
	g.tabs = make([]bool, s.Cols)
	for _, x := range s.Tabs {
		if x >= 0 && x < s.Cols {
			g.tabs[x] = true
		}
	}
	for i := range g.saved {
		sc := s.Saved[i]
		g.saved[i] = SavedCursor{
			Cursor: Cursor{
				X:        clamp(sc.Cursor.X, 0, s.Cols-1),
				Y:        clamp(sc.Cursor.Y, 0, s.Rows-1),
				Visible:  sc.Cursor.Visible,
				WrapNext: sc.Cursor.WrapNext,
			},
			Style:      sc.Style.style(),
			Charset:    Charset{G0: sc.Charset.G0, G1: sc.Charset.G1, GL: sc.Charset.GL},
			Origin:     sc.Origin,
			Protection: decodeProtection(sc.Protection),
		}
	}
	if len(s.SavedModes) > 0 {
		g.savedModes = make(map[int]bool, len(s.SavedModes))
		for k, v := range s.SavedModes {
			g.savedModes[k] = v
		}
	}
	if s.Modes.AltScreen {
		// The alternate screen is visible, so the hidden primary screen starts
		// blank. ExitAltScreen then has a grid to swap into.
		g.altLines = g.blankLines()
		g.altWrapped = make([]bool, s.Rows)
	}

	t.grid = g
	t.parser = parser{
		state:       s.Parser.State,
		cluster:     cloneBytes(s.Parser.Cluster),
		pending:     cloneBytes(s.Parser.Pending),
		esc:         cloneBytes(s.Parser.Esc),
		seq:         cloneBytes(s.Parser.Seq),
		str:         cloneBytes(s.Parser.Str),
		charsetSlot: s.Parser.CharsetSlot,
	}
	t.style = s.State.Style.style()
	t.protection = decodeProtection(s.State.Protection)
	t.level = s.State.Level
	if t.level == 0 {
		t.level = 5
	}
	t.iconTitle = s.State.IconTitle
	t.windowTitle = s.State.WindowTitle
	t.link = s.State.Link
	t.clipboard = s.State.Clipboard
	t.status = s.State.Status
	t.cursorStyle = s.State.CursorStyle
	t.screenLines = s.State.ScreenLines
	if t.screenLines == 0 {
		t.screenLines = defaultScreenLines
	}
	t.statusType = s.State.StatusType
	t.activeStatus = s.State.ActiveStatus
	t.changeExtent = s.State.ChangeExtent
	t.localFuncs = s.State.LocalFuncs
	t.localKeys = s.State.LocalKeys
	t.modifierKeys = s.State.ModifierKeys
	t.linesPerPage = s.State.LinesPerPage
	t.sevenBit = s.State.SevenBit
	t.colors = nil
	if len(s.State.Colors) > 0 {
		t.colors = make(map[int]Color, len(s.State.Colors))
		for k, v := range s.State.Colors {
			t.colors[k] = decodeColor(v)
		}
	}
	t.decModeStates = nil
	if len(s.State.DecModeState) > 0 {
		t.decModeStates = make(map[int]bool, len(s.State.DecModeState))
		for k, v := range s.State.DecModeState {
			t.decModeStates[k] = v
		}
	}
	t.ansiModeStates = nil
	if len(s.State.AnsiModeState) > 0 {
		t.ansiModeStates = make(map[int]bool, len(s.State.AnsiModeState))
		for k, v := range s.State.AnsiModeState {
			t.ansiModeStates[k] = v
		}
	}
	return nil
}
