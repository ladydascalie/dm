package theme

import (
	"strings"
	"testing"
)

// themeColourSet is every colour a theme owns, keyed for a membership test. The
// terminal palette and the page's variables may name only these.
func themeColourSet(th Theme) map[RGB]bool {
	all := map[RGB]bool{}
	for _, c := range themeColours(th) {
		all[c] = true
	}
	return all
}

// TestTerminalPaletteIsThemeColours proves every value of the terminal palette
// is one of the theme's own colours: the screen's colours and the sixteen slots
// name no hue the theme does not have.
func TestTerminalPaletteIsThemeColours(t *testing.T) {
	th := DefaultTheme()
	owned := themeColourSet(th)
	tp := th.Terminal()

	check := func(what string, c RGB) {
		t.Helper()
		if !owned[c] {
			t.Fatalf("%s = %v (%s) is not one of the theme's colours", what, c, c.Hex())
		}
	}
	check("Background", tp.Background)
	check("Foreground", tp.Foreground)
	check("Cursor", tp.Cursor)
	check("CursorAccent", tp.CursorAccent)
	check("Selection", tp.Selection)
	for i, c := range tp.ANSI {
		check(ANSINames[i], c)
	}
}

// TestTerminalPaletteIsComplete proves the palette fills every slot: an unset
// one would be the zero colour, which no theme owns.
func TestTerminalPaletteIsComplete(t *testing.T) {
	tp := DefaultTheme().Terminal()
	screen := map[string]RGB{
		"Background": tp.Background, "Foreground": tp.Foreground,
		"Cursor": tp.Cursor, "CursorAccent": tp.CursorAccent, "Selection": tp.Selection,
	}
	for name, c := range screen {
		if c == (RGB{}) {
			t.Fatalf("%s is unset", name)
		}
	}
	var zero RGB
	for i, c := range tp.ANSI {
		if c == zero {
			t.Fatalf("ANSI slot %d (%s) is unset", i, ANSINames[i])
		}
	}
}

// TestTerminalPaletteIsDistinct proves the sixteen slots are sixteen different
// colours, so no two ANSI slots collapse into one.
func TestTerminalPaletteIsDistinct(t *testing.T) {
	tp := DefaultTheme().Terminal()
	seen := map[RGB]int{}
	for i, c := range tp.ANSI {
		if was, ok := seen[c]; ok {
			t.Fatalf("ANSI slot %d (%s) is slot %d (%s): both are %s", i, ANSINames[i], was, ANSINames[was], c.Hex())
		}
		seen[c] = i
	}
}

// TestRGBHex pins the #rrggbb form, the one a stylesheet and an xterm theme read.
func TestRGBHex(t *testing.T) {
	cases := []struct {
		c    RGB
		want string
	}{
		{RGB{0, 0, 0}, "#000000"},
		{RGB{20, 19, 26}, "#14131a"},
		{RGB{233, 234, 241}, "#e9eaf1"},
		{RGB{255, 255, 255}, "#ffffff"},
		{RGB{1, 2, 15}, "#01020f"},
	}
	for _, tc := range cases {
		if got := tc.c.Hex(); got != tc.want {
			t.Fatalf("%v.Hex() = %q, want %q", tc.c, got, tc.want)
		}
	}
	// A returned colour parses back to itself, which is what a theme file does.
	back, err := parseThemeColour(RGB{122, 192, 235}.Hex())
	if err != nil || back != (RGB{122, 192, 235}) {
		t.Fatalf("parseThemeColour(RGB{122,192,235}.Hex()) = %v, %v", back, err)
	}
}

// TestCSSVarsAreThemeColours proves every stylesheet variable is a theme colour,
// formatted as #rrggbb, and that the page's names are all answered.
func TestCSSVarsAreThemeColours(t *testing.T) {
	th := DefaultTheme()
	owned := themeColourSet(th)
	got := map[string]string{}
	for _, v := range th.CSSVars() {
		if !strings.HasPrefix(v.Name, "--") {
			t.Fatalf("variable %q has no leading dashes", v.Name)
		}
		if _, dup := got[v.Name]; dup {
			t.Fatalf("variable %q is set twice", v.Name)
		}
		c, err := parseThemeColour(v.Value)
		if err != nil {
			t.Fatalf("variable %q = %q is not #rrggbb", v.Name, v.Value)
		}
		if !owned[c] {
			t.Fatalf("variable %q = %q is not one of the theme's colours", v.Name, v.Value)
		}
		if want := c.Hex(); v.Value != want {
			t.Fatalf("variable %q = %q, want %q", v.Name, v.Value, want)
		}
		got[v.Name] = v.Value
	}
	// The names the page's stylesheet reads. A variable it reads and the theme
	// does not answer would leave that rule colourless.
	for _, name := range []string{
		"--bg", "--panel", "--panel-edge", "--edge", "--ink", "--ink-dim",
		"--ink-faint", "--accent", "--good", "--warn", "--key", "--str",
		"--num", "--kw", "--raised",
	} {
		if _, ok := got[name]; !ok {
			t.Fatalf("the stylesheet reads %q and the palette does not answer it", name)
		}
	}
}

// TestTerminalFollowsAThemeFile proves the terminal palette and the page's
// variables come from the theme in hand, so a user theme reaches the browser
// too.
func TestTerminalFollowsAThemeFile(t *testing.T) {
	src := `palette {
  "rail-bg" "#101010"
  "bar-accent" "#ff0000"
}`
	th, err := parseTheme(src)
	if err != nil {
		t.Fatal(err)
	}
	tp := th.Terminal()
	if want := (RGB{16, 16, 16}); tp.Background != want {
		t.Fatalf("Background = %v, want %v", tp.Background, want)
	}
	if want := (RGB{255, 0, 0}); tp.ANSI[4] != want {
		t.Fatalf("ANSI slot 4 = %v, want %v", tp.ANSI[4], want)
	}
	for _, v := range th.CSSVars() {
		if v.Name == "--bg" && v.Value != "#101010" {
			t.Fatalf("--bg = %q, want %q", v.Value, "#101010")
		}
	}
}
