package esctest

import (
	"strings"
	"testing"

	"dm/internal/vt"

	"github.com/hinshun/vt10x"
)

func TestToGlyph(t *testing.T) {
	tests := []struct {
		name   string
		cell   vt.Cell
		char   rune
		mode   int16
		fg, bg vt10x.Color
	}{
		{
			name: "plain text, default colours",
			cell: vt.NewCell("A", vt.DefaultStyle()),
			char: 'A', fg: vt10x.DefaultFG, bg: vt10x.DefaultBG,
		},
		{
			name: "first rune of a grapheme cluster",
			cell: vt.NewCell("e\u0301", vt.DefaultStyle()),
			char: 'e', fg: vt10x.DefaultFG, bg: vt10x.DefaultBG,
		},
		{
			name: "empty content is a space",
			cell: vt.Cell{},
			char: ' ', fg: vt10x.DefaultFG, bg: vt10x.DefaultBG,
		},
		{
			name: "style maps to vt10x bits and colours",
			cell: vt.Cell{
				Content: "X",
				Style: vt.Style{
					Fg:        vt.PaletteColor(9),
					Bg:        vt.RGBColor(1, 2, 3),
					Underline: vt.UnderlineSingle,
					Attrs:     vt.Bold | vt.Italic | vt.Blink | vt.Reverse,
				},
			},
			char: 'X',
			mode: attrBold | attrItalic | attrBlink | attrReverse | attrUnderline,
			fg:   9,
			bg:   vt10x.Color(1<<16 | 2<<8 | 3),
		},
		{
			name: "attributes vt10x has no bit for are dropped",
			cell: vt.Cell{
				Content: "Y",
				Style: vt.Style{
					Fg:    vt.DefaultColor(),
					Bg:    vt.DefaultColor(),
					Attrs: vt.Faint | vt.Conceal | vt.Strikethrough,
				},
			},
			char: 'Y', mode: 0, fg: vt10x.DefaultFG, bg: vt10x.DefaultBG,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := toGlyph(tt.cell)
			if g.Char != tt.char || g.Mode != tt.mode || g.FG != tt.fg || g.BG != tt.bg {
				t.Fatalf("toGlyph(%q) = {Char:%q Mode:%d FG:%d BG:%d}, want {%q %d %d %d}",
					tt.cell.Content, g.Char, g.Mode, g.FG, g.BG, tt.char, tt.mode, tt.fg, tt.bg)
			}
		})
	}
}

func TestToModeFlag(t *testing.T) {
	visible := vt.Cursor{Visible: true}
	hidden := vt.Cursor{Visible: false}
	tests := []struct {
		name string
		m    vt.Modes
		c    vt.Cursor
		want vt10x.ModeFlag
	}{
		{"a fresh screen wraps and shows its cursor", vt.Modes{Wrap: true}, visible, vt10x.ModeWrap},
		{"insert", vt.Modes{Insert: true}, visible, vt10x.ModeInsert},
		{"alternate screen", vt.Modes{AltScreen: true}, visible, vt10x.ModeAltScreen},
		{"reverse video", vt.Modes{ReverseVideo: true}, visible, vt10x.ModeReverse},
		{"focus reporting", vt.Modes{FocusReporting: true}, visible, vt10x.ModeFocus},
		{"hidden cursor is ModeHide", vt.Modes{}, hidden, vt10x.ModeHide},
		{"mouse x10", vt.Modes{Mouse: vt.MouseX10}, visible, vt10x.ModeMouseX10},
		{"mouse button", vt.Modes{Mouse: vt.MouseButton}, visible, vt10x.ModeMouseButton},
		{"mouse motion", vt.Modes{Mouse: vt.MouseMotion}, visible, vt10x.ModeMouseMotion},
		{"mouse many", vt.Modes{Mouse: vt.MouseMany}, visible, vt10x.ModeMouseMany},
		{"sgr mouse encoding", vt.Modes{MouseEncoding: vt.MouseEncodingSGR}, visible, vt10x.ModeMouseSgr},
		{
			name: "everything at once",
			m: vt.Modes{
				Wrap: true, Insert: true, AltScreen: true, ReverseVideo: true,
				FocusReporting: true, Mouse: vt.MouseMany, MouseEncoding: vt.MouseEncodingSGR,
			},
			c: hidden,
			want: vt10x.ModeWrap | vt10x.ModeInsert | vt10x.ModeAltScreen |
				vt10x.ModeReverse | vt10x.ModeFocus | vt10x.ModeMouseMany |
				vt10x.ModeMouseSgr | vt10x.ModeHide,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toModeFlag(tt.m, tt.c); got != tt.want {
				t.Fatalf("toModeFlag(%+v) = %b, want %b", tt.m, got, tt.want)
			}
		})
	}
}

func TestVTAdapterWriteAndCell(t *testing.T) {
	a := newVTAdapter(10, 2)
	if _, err := a.Write([]byte("abc")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	for x, want := range map[int]rune{0: 'a', 1: 'b', 2: 'c'} {
		if got := a.Cell(x, 0).Char; got != want {
			t.Errorf("Cell(%d,0).Char = %q, want %q", x, got, want)
		}
	}
	if c := a.Cursor(); c.X != 3 || c.Y != 0 {
		t.Errorf("Cursor = (%d,%d), want (3,0)", c.X, c.Y)
	}
	if got := a.String(); !strings.HasPrefix(got, "abc") {
		t.Errorf("String = %q, want prefix \"abc\"", got)
	}
	if cols, rows := a.Size(); cols != 10 || rows != 2 {
		t.Errorf("Size = (%d,%d), want (10,2)", cols, rows)
	}
}

// TestVTAdapterPendingCluster keeps a cluster split over two writes together.
// The new VT holds the last cluster until the next rune proves it complete, so
// a read must flush it without tearing a combining mark off its base.
func TestVTAdapterPendingCluster(t *testing.T) {
	a := newVTAdapter(10, 1)
	_, _ = a.Write([]byte("e"))
	_, _ = a.Write([]byte("\u0301"))
	if got := a.Cell(0, 0).Char; got != 'e' {
		t.Fatalf("Cell(0,0).Char = %q, want 'e'", got)
	}
	if got := a.Cell(1, 0).Char; got != ' ' {
		t.Fatalf("Cell(1,0).Char = %q, want a space: the cluster is one cell", got)
	}
}

func TestVTAdapterCursorVisibleAndMode(t *testing.T) {
	a := newVTAdapter(10, 2)
	if !a.CursorVisible() {
		t.Fatal("a fresh adapter hides its cursor")
	}
	if got := a.Mode(); got&vt10x.ModeWrap == 0 {
		t.Fatalf("Mode = %b, want ModeWrap", got)
	}
	a.term.Grid().SetCursorVisible(false)
	if a.CursorVisible() {
		t.Fatal("CursorVisible = true after hiding the cursor")
	}
	if got := a.Mode(); got&vt10x.ModeHide == 0 {
		t.Fatalf("Mode = %b, want ModeHide after hiding the cursor", got)
	}
}

func TestVTChoice(t *testing.T) {
	tests := []struct {
		env     string
		want    string
		wantErr string
	}{
		{env: "", want: "vt10x"},
		{env: "vt10x", want: "vt10x"},
		{env: "new", want: "new"},
		{env: "vt10x2", wantErr: "unknown DM_ESCTEST_VT"},
		{env: "NEW", wantErr: "unknown DM_ESCTEST_VT"},
	}
	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			t.Setenv(vtEnv, tt.env)
			got, err := vtChoice()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("vtChoice = (%q,%v), want error containing %q", got, err, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.env) {
					t.Fatalf("error %q does not name the value %q", err, tt.env)
				}
				return
			}
			if err != nil {
				t.Fatalf("vtChoice: %v", err)
			}
			if got != tt.want {
				t.Fatalf("vtChoice = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewTerminalHonoursChoice(t *testing.T) {
	if _, ok := newTerminal("new", nil).(*vtAdapter); !ok {
		t.Fatal("choice new did not build the adapter")
	}
	if _, ok := newTerminal("vt10x", nil).(*vtAdapter); ok {
		t.Fatal("choice vt10x built the adapter")
	}
}
