package theme

import "dm/internal/cells"

// GlyphSet is the glyph vocabulary the chrome draws with.
type GlyphSet uint8

const (
	// GlyphAuto picks Nerd Font glyphs when the font is present and Unicode
	// otherwise. It is the zero value, so a config without the node and a test
	// TUI both get the detected set.
	GlyphAuto GlyphSet = iota
	// GlyphASCII is plain ASCII: every box, caret and mark falls back.
	GlyphASCII
	// GlyphUnicode is the built-in Unicode set.
	GlyphUnicode
	// GlyphNerd is the Unicode set with Nerd Font icons where a state or a
	// caret has one.
	GlyphNerd
)

// String is the config spelling of the set.
func (s GlyphSet) String() string {
	switch s {
	case GlyphASCII:
		return "ascii"
	case GlyphUnicode:
		return "unicode"
	case GlyphNerd:
		return "nerd"
	}
	return "auto"
}

// asciiGlyphs maps a built-in glyph to its ASCII form. A glyph with no entry is
// already ASCII, and a theme's own glyph is used as it is: the table covers
// what dm ships, not what a user invents.
var asciiGlyphs = map[string]string{
	"•": "+", "·": ".", "━": "-", "┃": "|",
	"┌": "+", "┐": "+", "└": "+", "┘": "+",
	"─": "-", "│": "|", "├": "+", "┤": "+",
	"▾": "v", "▸": ">", "▌": "|", "●": "*", "○": "o",
	"▲": "!", "✓": "+", "✕": "x", "▶": ">",
	"⧉": "S",
	"…": ">",
	"░": ":",
}

// ASCII returns the glyph's ASCII form, or the glyph itself when it has none.
func ASCII(g string) string {
	if a, ok := asciiGlyphs[g]; ok {
		return a
	}
	return g
}

// For returns the structural glyphs for a set. ASCII substitutes every glyph it
// has a form for. Unicode and Nerd keep the theme's glyphs; the Nerd fold
// carets are chosen by the caller that draws them, because they are a second
// field rather than a substitution.
func (g Glyphs) For(set GlyphSet) Glyphs {
	// The elision mark follows the set and is not a theme-file key, so
	// parseTheme never reads it: every set starts from the shipped mark and
	// ascii substitutes its own.
	g.Ellipsis = cells.Ellipsis
	if set != GlyphASCII {
		return g
	}
	g.Ellipsis = ASCII(g.Ellipsis)
	g.Grip = ASCII(g.Grip)
	g.DividerRow = ASCII(g.DividerRow)
	g.DividerCol = ASCII(g.DividerCol)
	g.BorderTopLeft = ASCII(g.BorderTopLeft)
	g.BorderTopRight = ASCII(g.BorderTopRight)
	g.BorderBottomLeft = ASCII(g.BorderBottomLeft)
	g.BorderBottomRight = ASCII(g.BorderBottomRight)
	g.BorderHorizontal = ASCII(g.BorderHorizontal)
	g.BorderVertical = ASCII(g.BorderVertical)
	g.BorderTeeLeft = ASCII(g.BorderTeeLeft)
	g.BorderTeeRight = ASCII(g.BorderTeeRight)
	g.FoldOpen = ASCII(g.FoldOpen)
	g.FoldClosed = ASCII(g.FoldClosed)
	g.ActiveMark = ASCII(g.ActiveMark)
	g.WorkspaceBar = ASCII(g.WorkspaceBar)
	g.Dirty = ASCII(g.Dirty)
	g.SyncMark = ASCII(g.SyncMark)
	g.Shadow = ASCII(g.Shadow)
	return g
}

// ElideMark is the set's elision mark: the Unicode ellipsis, or `>` for ascii.
// An unresolved Glyphs keeps the Unicode mark, so a caller that drew before
// resolution still ends a cut run with a one-cell glyph.
func (g Glyphs) ElideMark() string {
	if g.Ellipsis == "" {
		return cells.Ellipsis
	}
	return g.Ellipsis
}

// For returns the state mark for a set, with the glyph resolved into Glyph so a
// caller never has to know which field the set reads.
func (s StateStyle) For(set GlyphSet) StateStyle {
	switch set {
	case GlyphASCII:
		s.Glyph = ASCII(s.Glyph)
		s.NerdGlyph = ""
	case GlyphNerd:
		if s.NerdGlyph != "" {
			s.Glyph = s.NerdGlyph
		}
		s.NerdGlyph = ""
	}
	return s
}

// For returns the theme with every glyph resolved for a set: the structural
// glyphs, the rail marks, the state marks, and the Nerd fold carets folded into
// the plain ones. A caller that resolves once at load never has to thread the
// set through the painters.
func (t Theme) For(set GlyphSet) Theme {
	t.Glyphs = t.Glyphs.For(set)
	t.RailMarks = t.RailMarks.For(set)
	if set == GlyphNerd {
		if t.Glyphs.NerdFoldOpen != "" {
			t.Glyphs.FoldOpen = t.Glyphs.NerdFoldOpen
		}
		if t.Glyphs.NerdFoldClosed != "" {
			t.Glyphs.FoldClosed = t.Glyphs.NerdFoldClosed
		}
		if t.Glyphs.NerdSyncMark != "" {
			t.Glyphs.SyncMark = t.Glyphs.NerdSyncMark
		}
	}
	states := make(map[string]StateStyle, len(t.States))
	for name, style := range t.States {
		states[name] = style.For(set)
	}
	t.States = states
	return t
}

// For returns the rail marks for a set.
func (m RailMarks) For(set GlyphSet) RailMarks {
	if set != GlyphASCII {
		return m
	}
	m.Workspace = ASCII(m.Workspace)
	m.Pane = ASCII(m.Pane)
	return m
}
