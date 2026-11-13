package shot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"dm/internal/theme"
	"dm/internal/vt"
)

// Scene is one picture the oracle renders: a size, a pane tree, the pane
// content and the optional overlays. It mirrors internal/shot/testdata/scenes.json.
type Scene struct {
	Name         string   `json:"name"`
	Cols         int      `json:"cols"`
	Rows         int      `json:"rows"`
	Rail         int      `json:"rail"`
	Theme        string   `json:"theme"`
	Root         *Node    `json:"root"`
	Panes        []Pane   `json:"panes"`
	Windows      []Window `json:"windows"`
	ActiveWindow int      `json:"activeWindow"`
	Focus        string   `json:"focus"`
	Toast        string   `json:"toast,omitempty"`
	Float        *Float   `json:"float,omitempty"`
	// FloatShadow turns the config's float-shadow node on, so a scene
	// exercises the drop shadow the floats paint.
	FloatShadow bool      `json:"floatShadow,omitempty"`
	Switcher    bool      `json:"switcher,omitempty"`
	Help        bool      `json:"help,omitempty"`
	Mode        string    `json:"mode,omitempty"`
	Sessions    []Session `json:"sessions,omitempty"`
}

// Node is one node of the pane tree: a pane leaf or a split with kids.
type Node struct {
	Pane string  `json:"pane,omitempty"`
	Dir  string  `json:"dir,omitempty"`
	Kids []*Node `json:"kids,omitempty"`
}

// Pane is one pane's id, name, agent state and initial screen content. Status,
// Agent, Title and Project are the dashboard's columns; a scene that leaves
// them out draws the pane as a shell with no title and no project.
type Pane struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Status  string `json:"status,omitempty"`
	Agent   bool   `json:"agent,omitempty"`
	Title   string `json:"title,omitempty"`
	Project string `json:"project,omitempty"`
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

// GridFromScreen reads a terminal's visible cells into a Grid. A wide glyph
// occupies its first slot and marks its continuation slots, so the rasteriser
// advances the pen by the glyph's real width once.
func GridFromScreen(term *vt.Term, w, h int) *Grid {
	g := NewGrid(w, h)
	if term == nil {
		return g
	}
	src := term.Grid()
	cols, rows := src.Size()
	for y := 0; y < h; y++ {
		for x := 0; x < w; {
			if y >= rows || x >= cols {
				g.Set(x, y, Cell{Content: " ", Fg: DefaultFG, Bg: DefaultBG})
				x++
				continue
			}
			c := src.Cell(x, y)
			next := src.Next(x, y)
			if next <= x {
				next = x + 1
			}
			g.Set(x, y, Cell{
				Content: c.Content,
				Fg:      colour(c.Style.Fg, DefaultFG),
				Bg:      colour(c.Style.Bg, DefaultBG),
				Attr:    attr(c.Style),
			})
			for k := x + 1; k < next && k < w; k++ {
				g.Set(k, y, Cell{Cont: true})
			}
			x = next
		}
	}
	return g
}

func attr(st vt.Style) Attr {
	var a Attr
	if st.Attrs&vt.Bold != 0 {
		a |= Bold
	}
	if st.Underline != vt.UnderlineNone {
		a |= Underline
	}
	if st.Attrs&vt.Reverse != 0 {
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

// colour maps a cell colour to RGB. A default colour takes def.
func colour(c vt.Color, def RGB) RGB {
	switch c.Kind {
	case vt.ColorPalette:
		if c.Index < 16 {
			return ansi16[c.Index]
		}
		return xterm256(int(c.Index))
	case vt.ColorRGB:
		return RGB{c.R, c.G, c.B}
	default:
		return def
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
