package theme

// ANSISlots is the number of ANSI colours a terminal palette names.
const ANSISlots = 16

// ANSINames are the keys an xterm theme gives the sixteen ANSI slots, in ANSI
// order. A host spreads them into its terminal's theme, so the slot table lives
// here and not in the host.
var ANSINames = [ANSISlots]string{
	"black", "red", "green", "yellow",
	"blue", "magenta", "cyan", "white",
	"brightBlack", "brightRed", "brightGreen", "brightYellow",
	"brightBlue", "brightMagenta", "brightCyan", "brightWhite",
}

// TerminalPalette is the colour set a terminal host paints with: the screen's
// own colours and the sixteen ANSI slots. Every value is one of the theme's own
// colours, so the terminal inside a page and the page around it come from one
// palette.
type TerminalPalette struct {
	// Background, Foreground, Cursor, CursorAccent and Selection are the
	// screen's own colours: the text, the block cursor and what sits inside it,
	// and the background behind a selection.
	Background, Foreground, Cursor, CursorAccent, Selection RGB
	// ANSI is the sixteen ANSI slots, in ANSI order.
	ANSI [ANSISlots]RGB
}

// Terminal returns the theme's terminal palette. Each value names the theme
// colour behind it: the rail and bar take the screen's own colours, and the
// slots take the rail palette, the six accents and the per-state marks.
//
// Slot 0 is the background itself, as the brief that added this asked; a
// program that paints text in "black" paints it invisible, which is the one
// cost of a dark theme whose black is its own background.
func (t Theme) Terminal() TerminalPalette {
	state := func(name string) RGB { return t.States[name].Colour }
	return TerminalPalette{
		Background:   t.Palette.RailBG,      // the rail's own background
		Foreground:   t.Palette.RailText,    // the rail's text
		Cursor:       t.Palette.BarAccent,   // the status bar's accent
		CursorAccent: t.Palette.RailBG,      // text inside the cursor: the screen's background
		Selection:    t.Palette.RailFocusBG, // the rail's focus row
		ANSI: [ANSISlots]RGB{
			t.Palette.RailBG,      // 0 black
			t.Palette.RailDiffDel, // 1 red
			t.Palette.RailDiffAdd, // 2 green
			t.Palette.RailWorking, // 3 yellow
			t.Palette.BarAccent,   // 4 blue
			t.Accents[2],          // 5 magenta
			t.Accents[1],          // 6 cyan
			t.Palette.RailDim,     // 7 white
			t.Palette.RailWindow,  // 8 bright black
			state("blocked"),      // 9 bright red
			state("done"),         // 10 bright green
			t.Accents[4],          // 11 bright yellow
			t.Accents[3],          // 12 bright blue
			t.Accents[0],          // 13 bright magenta
			state("idle"),         // 14 bright cyan
			t.Palette.RailText,    // 15 bright white
		},
	}
}

// CSSVar is one stylesheet variable and the colour it takes.
type CSSVar struct {
	// Name is the variable's name, with its leading dashes.
	Name string
	// Value is the colour as #rrggbb.
	Value string
}

// CSSVars returns the colour variables a page's stylesheet reads, each one a
// theme colour formatted as #rrggbb. The names are the ones cmd/dm-wasm's page
// uses: the first fourteen dress the page it already had, and --raised is the
// chip and selected-row surface that had a literal of its own.
//
// The mapping keeps the page's roles: --bg is the page behind the cards, --panel
// the card surface, --panel-edge the card's border, --edge the border of a
// control, the three inks are text, and the rest colour a token each.
func (t Theme) CSSVars() []CSSVar {
	p := t.Palette
	return []CSSVar{
		{"--bg", p.RailBG.Hex()},
		{"--panel", p.RailHoverBG.Hex()},
		{"--panel-edge", p.RailFocusBG.Hex()},
		{"--edge", p.RailDim.Hex()},
		{"--ink", p.RailText.Hex()},
		{"--ink-dim", p.RailHeader.Hex()},
		{"--ink-faint", p.RailWindow.Hex()},
		{"--accent", p.BarAccent.Hex()},
		{"--good", p.RailDiffAdd.Hex()},
		{"--warn", p.RailWorking.Hex()},
		{"--key", t.Accents[3].Hex()},
		{"--str", t.Accents[5].Hex()},
		{"--num", p.RailWorking.Hex()},
		{"--kw", t.Accents[0].Hex()},
		{"--raised", p.RailFocusBG.Hex()},
	}
}
