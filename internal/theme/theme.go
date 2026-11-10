// Package theme holds the palette, glyphs, state styles and the theme file
// parser.
package theme

import (
	"fmt"
	"math"
	"os"
	"path/filepath"

	"dm/internal/cells"
	"dm/internal/kdl"
)

// accentSlots is the number of workspace accent slots.
const accentSlots = 6

// RGB is an explicit colour. The rail never uses palette slots: a theme may
// map a slot to a colour that fails contrast on its own background, and the
// rail must pass WCAG AA on every theme.
type RGB struct{ R, G, B int }

// FG is the foreground escape for the colour at the current depth.
func (c RGB) FG() string { return "\x1b[" + c.SGR(false) + "m" }

// BG is the background escape for the colour at the current depth.
func (c RGB) BG() string { return "\x1b[" + c.SGR(true) + "m" }

// Lum is the WCAG relative luminance.
func (c RGB) Lum() float64 {
	lin := func(v int) float64 {
		f := float64(v) / 255
		if f <= 0.04045 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// Contrast is the WCAG contrast ratio, 1:1 to 21:1.
func Contrast(a, b RGB) float64 {
	la, lb := a.Lum(), b.Lum()
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// Theme is everything the renderer can restyle: the rail and bar palette, the
// six workspace accents, the structural glyphs, the per-state marks, and the
// rail's row-level marks. A theme file overrides the palette, accents, glyphs
// and states; the rail marks keep their built-in values.
type Theme struct {
	Palette   Palette
	Accents   [accentSlots]RGB
	Glyphs    Glyphs
	States    map[string]StateStyle
	RailMarks RailMarks
}

// RailMarks are the row-level markers the rail uses to separate a workspace
// row from a window row from a pane row. They are fixed built-in values.
type RailMarks struct {
	Workspace string
	Pane      string
}

// Palette is the rail and status bar RGB set.
type Palette struct {
	RailBG, RailFocusBG, RailHoverBG, RailText, RailDim, RailHeader, RailWindow RGB
	RailDiffAdd, RailDiffDel, RailWorking, BarAccent                            RGB
}

// Glyphs are the structural decorators: the drag grip, the divider hover
// marks, the box, menu, picker and help borders and tees, the rail's fold
// carets in both forms, the active-window mark, the workspace bar, the dirty
// dot, and the elision mark.
type Glyphs struct {
	Grip              string
	DividerRow        string
	DividerCol        string
	BorderTopLeft     string
	BorderTopRight    string
	BorderBottomLeft  string
	BorderBottomRight string
	BorderHorizontal  string
	BorderVertical    string
	BorderTeeLeft     string
	BorderTeeRight    string
	FoldOpen          string
	FoldClosed        string
	NerdFoldOpen      string
	NerdFoldClosed    string
	ActiveMark        string
	WorkspaceBar      string
	Dirty             string
	// Ellipsis marks a cut run. Glyphs.For resolves it per set; it is not a
	// theme-file key, so Glyphs.set never reads it.
	Ellipsis string
}

// StateStyle is one state mark: the Unicode glyph, the Nerd Font glyph when the
// state has one, and the colour.
type StateStyle struct {
	Glyph     string
	NerdGlyph string
	Colour    RGB
}

// DefaultTheme returns the built-in theme. Its palette and state colours pass
// WCAG AA on both rail backgrounds; TestRailContrast and TestBarContrast guard
// it. A user theme is not validated.
func DefaultTheme() Theme {
	return Theme{
		Palette: Palette{
			RailBG:      RGB{20, 19, 26},    // #14131a
			RailFocusBG: RGB{44, 42, 56},    // #2c2a38
			RailHoverBG: RGB{33, 32, 45},    // #21202d
			RailText:    RGB{233, 234, 241}, // #e9eaf1
			RailDim:     RGB{143, 147, 168}, // #8f93a8
			RailHeader:  RGB{166, 169, 189}, // #a6a9bd
			RailWindow:  RGB{149, 153, 174}, // #9599ae
			RailDiffAdd: RGB{120, 220, 130}, // #78dc82
			RailDiffDel: RGB{255, 125, 125}, // #ff7d7d
			RailWorking: RGB{245, 190, 80},  // #f5be50
			BarAccent:   RGB{122, 192, 235}, // #7ac0eb
		},
		Accents: [6]RGB{
			{187, 154, 247}, // #bb9af7 violet
			{110, 220, 235}, // #6edceb cyan
			{240, 140, 215}, // #f08cd7 magenta
			{126, 169, 255}, // #7ea9ff blue
			{240, 160, 90},  // #f0a05a orange
			{140, 215, 155}, // #8cd79b green
		},
		Glyphs: Glyphs{
			Grip:              "•",
			DividerRow:        "━",
			DividerCol:        "┃",
			BorderTopLeft:     "┌",
			BorderTopRight:    "┐",
			BorderBottomLeft:  "└",
			BorderBottomRight: "┘",
			BorderHorizontal:  "─",
			BorderVertical:    "│",
			BorderTeeLeft:     "├",
			BorderTeeRight:    "┤",
			FoldOpen:          "▾",
			FoldClosed:        "▸",
			NerdFoldOpen:      "\uf0d7",
			NerdFoldClosed:    "\uf0da",
			ActiveMark:        "▸",
			WorkspaceBar:      "▌",
			Dirty:             "●",
			Ellipsis:          "…",
		},
		States: map[string]StateStyle{
			"working": {Glyph: "●", NerdGlyph: "\uf013", Colour: RGB{245, 190, 80}},  // #f5be50
			"idle":    {Glyph: "○", NerdGlyph: "\uf111", Colour: RGB{120, 200, 225}}, // #78c8e1
			"blocked": {Glyph: "▲", NerdGlyph: "\uf071", Colour: RGB{255, 110, 110}}, // #ff6e6e
			"done":    {Glyph: "✓", NerdGlyph: "\uf00c", Colour: RGB{110, 225, 145}}, // #6ee191
			"error":   {Glyph: "✕", NerdGlyph: "\uf057", Colour: RGB{255, 130, 160}}, // #ff82a0
			"unknown": {Glyph: "?", NerdGlyph: "\uf128", Colour: RGB{143, 147, 168}}, // #8f93a8
			"shell":   {Glyph: "▶", Colour: RGB{143, 147, 168}},                      // #8f93a8, no Nerd form
		},
		RailMarks: RailMarks{
			Workspace: "•",
			Pane:      "·",
		},
	}
}

// themeBlocks are the only top-level blocks a theme file accepts.
var themeBlocks = map[string]bool{
	"palette": true,
	"accents": true,
	"glyphs":  true,
	"states":  true,
}

// parseTheme returns the built-in theme with the overrides from one theme file.
// Every block and key is optional. An unknown or duplicate block or key, a
// malformed colour, or a glyph that is not one rune of cell width one is an
// error that names the line.
func parseTheme(src string) (Theme, error) {
	t := DefaultTheme()
	toks, err := kdl.Scan(src)
	if err != nil {
		return t, err
	}
	seen := map[string]bool{}
	i := 0
	for i < len(toks) && toks[i].Kind != kdl.EOF {
		tok := toks[i]
		if tok.Kind != kdl.Ident {
			return t, fmt.Errorf("line %d: expected block name, got %q", tok.Line, tok.Value)
		}
		block := tok.Value
		if !themeBlocks[block] {
			return t, fmt.Errorf("line %d: unknown block %q", tok.Line, block)
		}
		if seen[block] {
			return t, fmt.Errorf("line %d: duplicate block %q", tok.Line, block)
		}
		seen[block] = true
		i++
		if i >= len(toks) || toks[i].Kind != kdl.LBrace {
			return t, fmt.Errorf("line %d: expected '{' after %q", tok.Line, block)
		}
		i++
		if block == "states" {
			i, err = parseThemeStates(toks, i, &t)
		} else {
			i, err = parseThemePairs(toks, i, block, &t)
		}
		if err != nil {
			return t, err
		}
	}
	return t, nil
}

// parseThemePairs walks one flat block's quoted key and value pairs up to its
// closing brace and applies each pair to t.
func parseThemePairs(toks []kdl.Token, i int, block string, t *Theme) (int, error) {
	keys := map[string]bool{}
	for {
		if i >= len(toks) || toks[i].Kind == kdl.EOF {
			return i, fmt.Errorf("line %d: unexpected end of file, expected '}'", toks[len(toks)-1].Line)
		}
		if toks[i].Kind == kdl.RBrace {
			return i + 1, nil
		}
		if toks[i].Kind != kdl.String {
			return i, fmt.Errorf("line %d: expected quoted key in %q block", toks[i].Line, block)
		}
		key, keyLine := toks[i].Value, toks[i].Line
		i++
		if i >= len(toks) || toks[i].Kind != kdl.String {
			line := keyLine
			if i < len(toks) {
				line = toks[i].Line
			}
			return i, fmt.Errorf("line %d: expected quoted value after %q", line, key)
		}
		val, valLine := toks[i].Value, toks[i].Line
		i++
		if keys[key] {
			return i, fmt.Errorf("line %d: duplicate key %q in %q block", keyLine, key, block)
		}
		keys[key] = true
		switch block {
		case "palette":
			c, err := parseThemeColour(val)
			if err != nil {
				return i, fmt.Errorf("line %d: %v", valLine, err)
			}
			if !t.Palette.set(key, c) {
				return i, fmt.Errorf("line %d: unknown palette key %q", keyLine, key)
			}
		case "accents":
			c, err := parseThemeColour(val)
			if err != nil {
				return i, fmt.Errorf("line %d: %v", valLine, err)
			}
			if !setAccent(&t.Accents, key, c) {
				return i, fmt.Errorf("line %d: unknown accents key %q", keyLine, key)
			}
		case "glyphs":
			if err := validateThemeGlyph(val); err != nil {
				return i, fmt.Errorf("line %d: %v", valLine, err)
			}
			if !t.Glyphs.set(key, val) {
				return i, fmt.Errorf("line %d: unknown glyphs key %q", keyLine, key)
			}
		}
	}
}

// parseThemeStates walks the states block's nested state blocks. A state's
// glyph and colour are each optional; an omitted one keeps the built-in value.
// A theme glyph replaces both the Unicode and the Nerd Font form.
func parseThemeStates(toks []kdl.Token, i int, t *Theme) (int, error) {
	seen := map[string]bool{}
	for {
		if i >= len(toks) || toks[i].Kind == kdl.EOF {
			return i, fmt.Errorf("line %d: unexpected end of file, expected '}'", toks[len(toks)-1].Line)
		}
		if toks[i].Kind == kdl.RBrace {
			return i + 1, nil
		}
		if toks[i].Kind != kdl.String {
			return i, fmt.Errorf("line %d: expected quoted state name", toks[i].Line)
		}
		name, nameLine := toks[i].Value, toks[i].Line
		if seen[name] {
			return i, fmt.Errorf("line %d: duplicate state %q", nameLine, name)
		}
		seen[name] = true
		style, ok := t.States[name]
		if !ok {
			return i, fmt.Errorf("line %d: unknown state %q", nameLine, name)
		}
		i++
		if i >= len(toks) || toks[i].Kind != kdl.LBrace {
			return i, fmt.Errorf("line %d: expected '{' after state %q", nameLine, name)
		}
		i++
		keys := map[string]bool{}
		for {
			if i >= len(toks) || toks[i].Kind == kdl.EOF {
				return i, fmt.Errorf("line %d: unexpected end of file, expected '}'", toks[len(toks)-1].Line)
			}
			if toks[i].Kind == kdl.RBrace {
				i++
				break
			}
			if toks[i].Kind != kdl.String {
				return i, fmt.Errorf("line %d: expected quoted key in state %q", toks[i].Line, name)
			}
			key, keyLine := toks[i].Value, toks[i].Line
			i++
			if i >= len(toks) || toks[i].Kind != kdl.String {
				line := keyLine
				if i < len(toks) {
					line = toks[i].Line
				}
				return i, fmt.Errorf("line %d: expected quoted value after %q", line, key)
			}
			val, valLine := toks[i].Value, toks[i].Line
			i++
			if keys[key] {
				return i, fmt.Errorf("line %d: duplicate key %q in state %q", keyLine, key, name)
			}
			keys[key] = true
			switch key {
			case "glyph":
				if err := validateThemeGlyph(val); err != nil {
					return i, fmt.Errorf("line %d: %v", valLine, err)
				}
				style.Glyph = val
				style.NerdGlyph = val
			case "colour":
				c, err := parseThemeColour(val)
				if err != nil {
					return i, fmt.Errorf("line %d: %v", valLine, err)
				}
				style.Colour = c
			default:
				return i, fmt.Errorf("line %d: unknown state key %q in state %q", keyLine, key, name)
			}
		}
		t.States[name] = style
	}
}

// set assigns a palette key. It reports false for an unknown key.
func (p *Palette) set(key string, c RGB) bool {
	switch key {
	case "rail-bg":
		p.RailBG = c
	case "rail-focus-bg":
		p.RailFocusBG = c
	case "rail-hover-bg":
		p.RailHoverBG = c
	case "rail-text":
		p.RailText = c
	case "rail-dim":
		p.RailDim = c
	case "rail-header":
		p.RailHeader = c
	case "rail-window":
		p.RailWindow = c
	case "rail-diff-add":
		p.RailDiffAdd = c
	case "rail-diff-del":
		p.RailDiffDel = c
	case "rail-working":
		p.RailWorking = c
	case "bar-accent":
		p.BarAccent = c
	default:
		return false
	}
	return true
}

// setAccent assigns one "accent-N" key. It reports false for an unknown key.
func setAccent(a *[6]RGB, key string, c RGB) bool {
	for n := 1; n <= len(a); n++ {
		if key == fmt.Sprintf("accent-%d", n) {
			a[n-1] = c
			return true
		}
	}
	return false
}

// set assigns a glyph key. An explicit fold caret also wins over the Nerd Font
// icon for that item, so both forms take the theme glyph. It reports false for
// an unknown key.
func (g *Glyphs) set(key, val string) bool {
	switch key {
	case "grip":
		g.Grip = val
	case "divider-row":
		g.DividerRow = val
	case "divider-col":
		g.DividerCol = val
	case "border-top-left":
		g.BorderTopLeft = val
	case "border-top-right":
		g.BorderTopRight = val
	case "border-bottom-left":
		g.BorderBottomLeft = val
	case "border-bottom-right":
		g.BorderBottomRight = val
	case "border-horizontal":
		g.BorderHorizontal = val
	case "border-vertical":
		g.BorderVertical = val
	case "border-tee-left":
		g.BorderTeeLeft = val
	case "border-tee-right":
		g.BorderTeeRight = val
	case "fold-open":
		g.FoldOpen = val
		g.NerdFoldOpen = val
	case "fold-closed":
		g.FoldClosed = val
		g.NerdFoldClosed = val
	case "active-mark":
		g.ActiveMark = val
	case "workspace-bar":
		g.WorkspaceBar = val
	case "dirty":
		g.Dirty = val
	default:
		return false
	}
	return true
}

// parseThemeColour reads a #rrggbb colour.
func parseThemeColour(s string) (RGB, error) {
	if len(s) != 7 || s[0] != '#' {
		return RGB{}, fmt.Errorf("colour %q is not #rrggbb", s)
	}
	v := 0
	for i := 1; i < len(s); i++ {
		c := s[i]
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case c >= 'a' && c <= 'f':
			d = int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = int(c-'A') + 10
		default:
			return RGB{}, fmt.Errorf("colour %q is not #rrggbb", s)
		}
		v = v*16 + d
	}
	return RGB{(v >> 16) & 0xff, (v >> 8) & 0xff, v & 0xff}, nil
}

// validateThemeGlyph holds a glyph to exactly one rune of cell width one, so it
// cannot shift the row it lands on.
func validateThemeGlyph(s string) error {
	r := []rune(s)
	if len(r) != 1 {
		return fmt.Errorf("glyph %q must be exactly one rune", s)
	}
	if cells.Cells(s) != 1 {
		return fmt.Errorf("glyph %q must be one cell wide", s)
	}
	return nil
}

// LoadThemeFile reads <dir>/themes/<name>.kdl. An empty name keeps the built-in
// theme. A read or parse failure returns the built-in theme and the error.
func LoadThemeFile(dir, name string) (Theme, error) {
	if name == "" {
		return DefaultTheme(), nil
	}
	path := filepath.Join(dir, "themes", name+".kdl")
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultTheme(), err
	}
	t, err := parseTheme(string(data))
	if err != nil {
		return DefaultTheme(), fmt.Errorf("%s: %w", path, err)
	}
	return t, nil
}

// CheckThemeName holds a theme name to [A-Za-z0-9_-]+, so it can never leave
// the themes directory.
func CheckThemeName(name string) error {
	if name == "" {
		return fmt.Errorf("empty theme name")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return fmt.Errorf("theme name %q must match [A-Za-z0-9_-]+", name)
		}
	}
	return nil
}
