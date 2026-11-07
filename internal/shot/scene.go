package shot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"dm/internal/theme"
	"dm/internal/vt"

	"github.com/hinshun/vt10x"
)

// Scene is one picture the oracle renders: a size, a pane tree, the pane
// content and the optional overlays. It mirrors internal/shot/testdata/scenes.json.
type Scene struct {
	Name         string    `json:"name"`
	Cols         int       `json:"cols"`
	Rows         int       `json:"rows"`
	Rail         int       `json:"rail"`
	Theme        string    `json:"theme"`
	Root         *Node     `json:"root"`
	Panes        []Pane    `json:"panes"`
	Windows      []Window  `json:"windows"`
	ActiveWindow int       `json:"activeWindow"`
	Focus        string    `json:"focus"`
	Toast        string    `json:"toast,omitempty"`
	Float        *Float    `json:"float,omitempty"`
	Switcher     bool      `json:"switcher,omitempty"`
	Help         bool      `json:"help,omitempty"`
	Mode         string    `json:"mode,omitempty"`
	Sessions     []Session `json:"sessions,omitempty"`
}

// Node is one node of the pane tree: a pane leaf or a split with kids.
type Node struct {
	Pane string  `json:"pane,omitempty"`
	Dir  string  `json:"dir,omitempty"`
	Kids []*Node `json:"kids,omitempty"`
}

// Pane is one pane's id, name and initial screen content.
type Pane struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

// Window is one tab bar entry.
type Window struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Float is one floating pane over the layout.
type Float struct {
	ID      string `json:"id"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	W       int    `json:"w"`
	H       int    `json:"h"`
	Content string `json:"content"`
}

// Session is one row of the session picker.
type Session struct {
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

type sceneFile struct {
	Scenes []Scene `json:"scenes"`
}

// LoadScenes reads the scene list. A missing file returns the os error, so a
// caller can skip instead of fail.
func LoadScenes(path string) ([]Scene, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f sceneFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	return f.Scenes, nil
}

// LoadTheme reads one theme file from dir. An empty name returns the built-in
// theme.
func LoadTheme(dir, name string) (theme.Theme, error) {
	return theme.LoadThemeFile(dir, name)
}

// ThemeNames returns the base names of the theme files in <dir>/themes, sorted.
func ThemeNames(dir string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "themes", "*.kdl"))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(paths))
	for _, p := range paths {
		names = append(names, strings.TrimSuffix(filepath.Base(p), ".kdl"))
	}
	return names, nil
}

// DefaultFG and DefaultBG are the colours a cell with no explicit colour gets.
// dm leaves pane defaults to the host terminal, so the oracle picks a plain
// white on black.
var (
	DefaultFG = RGB{255, 255, 255}
	DefaultBG = RGB{0, 0, 0}
)

// attrReverse is vt10x's reverse-video mode bit. vt10x keeps it unexported.
const attrReverse = 1 << 0

// GridFromScreen reads a terminal's visible cells into a Grid. A wide glyph
// stays in the one cell vt10x put it in, which is what makes the rasteriser's
// real-width advance overshoot.
func GridFromScreen(s vt10x.Terminal, w, h int) *Grid {
	g := NewGrid(w, h)
	s.Lock()
	defer s.Unlock()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			gl := vt.NormCell(s.Cell(x, y))
			g.Set(x, y, Cell{
				Content: string(gl.Char),
				Fg:      colour(gl.FG, DefaultFG),
				Bg:      colour(gl.BG, DefaultBG),
				Attr:    attr(gl.Mode),
			})
		}
	}
	return g
}

func attr(mode int16) Attr {
	var a Attr
	if mode&vt.AttrBold != 0 {
		a |= Bold
	}
	if mode&vt.AttrUnderline != 0 {
		a |= Underline
	}
	if mode&attrReverse != 0 {
		a |= Reverse
	}
	return a
}

// ansi16 is the xterm palette for the 16 ANSI colours.
var ansi16 = [16]RGB{
	{0x00, 0x00, 0x00}, {0xcd, 0x00, 0x00}, {0x00, 0xcd, 0x00}, {0xcd, 0xcd, 0x00},
	{0x00, 0x00, 0xee}, {0xcd, 0x00, 0xcd}, {0x00, 0xcd, 0xcd}, {0xe5, 0xe5, 0xe5},
	{0x7f, 0x7f, 0x7f}, {0xff, 0x00, 0x00}, {0x00, 0xff, 0x00}, {0xff, 0xff, 0x00},
	{0x5c, 0x5c, 0xff}, {0xff, 0x00, 0xff}, {0x00, 0xff, 0xff}, {0xff, 0xff, 0xff},
}

// colour maps a vt10x colour to RGB. A default colour takes def.
func colour(c vt10x.Color, def RGB) RGB {
	switch {
	case c == vt10x.DefaultFG || c == vt10x.DefaultBG || c == vt10x.DefaultCursor:
		return def
	case c < 16:
		return ansi16[c]
	case c < 256:
		return xterm256(int(c))
	default:
		return RGB{uint8(c >> 16), uint8(c >> 8), uint8(c)}
	}
}

// xterm256 maps an xterm palette index to RGB: a 6x6x6 cube then a grey ramp.
func xterm256(i int) RGB {
	if i < 232 {
		i -= 16
		level := func(v int) uint8 {
			if v == 0 {
				return 0
			}
			return uint8(55 + v*40)
		}
		return RGB{level(i / 36), level((i / 6) % 6), level(i % 6)}
	}
	v := uint8(8 + (i-232)*10)
	return RGB{v, v, v}
}
