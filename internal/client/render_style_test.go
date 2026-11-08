package client

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"dm/internal/shot"
	"dm/internal/theme"
	"dm/internal/vt"
)

// painted reports whether some run paints glyph with the colour parameter
// param in the same escape.
func painted(out, glyph, param string) bool {
	for i := 0; ; {
		j := strings.Index(out[i:], glyph)
		if j < 0 {
			return false
		}
		j += i
		if k := strings.LastIndex(out[:j], "\x1b["); k >= 0 && strings.Contains(out[k:j], param) {
			return true
		}
		i = j + len(glyph)
	}
}

// paramOf is a colour's parameter inside a combined escape.
func paramOf(c theme.RGB, bg bool) string { return ";" + c.SGR(bg) + ";" }

// TestCellStyleBitsMatchTheme pins the assumption renderStyle makes: the cell
// style's enums and bits are the theme's, value for value.
func TestCellStyleBitsMatchTheme(t *testing.T) {
	attrs := []struct {
		name   string
		cell   uint8
		render uint8
	}{
		{"bold", vt.Bold, theme.AttrBold},
		{"faint", vt.Faint, theme.AttrFaint},
		{"italic", vt.Italic, theme.AttrItalic},
		{"blink", vt.Blink, theme.AttrBlink},
		{"rapid blink", vt.RapidBlink, theme.AttrRapidBlink},
		{"reverse", vt.Reverse, theme.AttrReverse},
		{"conceal", vt.Conceal, theme.AttrConceal},
		{"strikethrough", vt.Strikethrough, theme.AttrStrikethrough},
	}
	for _, a := range attrs {
		if a.cell != a.render {
			t.Fatalf("%s: cell bit %08b, render bit %08b", a.name, a.cell, a.render)
		}
	}
	underlines := []struct {
		name   string
		cell   vt.UnderlineStyle
		render uint8
	}{
		{"none", vt.UnderlineNone, theme.UnderlineNone},
		{"single", vt.UnderlineSingle, theme.UnderlineSingle},
		{"double", vt.UnderlineDouble, theme.UnderlineDouble},
		{"curly", vt.UnderlineCurly, theme.UnderlineCurly},
		{"dotted", vt.UnderlineDotted, theme.UnderlineDotted},
		{"dashed", vt.UnderlineDashed, theme.UnderlineDashed},
	}
	for _, u := range underlines {
		if uint8(u.cell) != u.render {
			t.Fatalf("underline %s: cell %d, render %d", u.name, u.cell, u.render)
		}
	}
	kinds := []struct {
		name string
		cell vt.ColorKind
		want theme.ColorKind
	}{
		{"default", vt.ColorDefault, theme.ColorDefault},
		{"palette", vt.ColorPalette, theme.ColorPalette},
		{"rgb", vt.ColorRGB, theme.ColorRGB},
	}
	for _, k := range kinds {
		if theme.ColorKind(k.cell) != k.want {
			t.Fatalf("colour kind %s: cell %d, render %d", k.name, k.cell, k.want)
		}
	}
}

// TestCellSGRSuppressesUnchanged proves a style equal to the live one writes
// nothing, which is what keeps a row of one style to one escape.
func TestCellSGRSuppressesUnchanged(t *testing.T) {
	st := vt.Style{Fg: vt.PaletteColor(1), Attrs: vt.Bold}
	if got := cellSGR(st, &st); got != "" {
		t.Fatalf("unchanged style wrote %q", got)
	}
	if got := cellSGR(st, nil); got == "" {
		t.Fatal("first style wrote nothing")
	}
}

// TestFrameStyleCacheHitRate proves the cache earns its place on a real frame:
// every scene asks for the same styles again and again, so most asks hit.
func TestFrameStyleCacheHitRate(t *testing.T) {
	scenes, err := shot.LoadScenes(filepath.Join(shotData, "scenes.json"))
	if errors.Is(err, fs.ErrNotExist) {
		t.Skipf("no scene data at %s", shotData)
	}
	if err != nil {
		t.Fatalf("scenes: %v", err)
	}
	th, err := shot.LoadTheme(shotData, "dark")
	if err != nil {
		t.Fatalf("theme: %v", err)
	}
	worst := 1.0
	for _, sc := range scenes {
		tr, _ := buildSceneTUI(t, sc, th)
		theme.ResetSGRCache()
		tr.draw()
		s := theme.SGRCache()
		if s.Lookups() == 0 {
			t.Fatalf("scene %s asked the cache nothing", sc.Name)
		}
		t.Logf("%s: %v", sc.Name, s)
		if s.Rate() < worst {
			worst = s.Rate()
		}
	}
	t.Logf("worst scene hit rate: %.1f%%", 100*worst)
	if worst < 0.5 {
		t.Fatalf("worst scene hit rate %.1f%%, want at least 50%%", 100*worst)
	}
}

// TestStyledFrameHitRateWarms proves a frame full of a coloured style hits on
// every cell after the first: the cache is warm for the next frame.
func TestStyledFrameHitRateWarms(t *testing.T) {
	f := newFrameTUI(t, 100, 20, 34)
	term := f.tui.vts["w1p1"]
	for i := 0; i < 20; i++ {
		term.Write([]byte("\x1b[31;44mred on blue\x1b[0m\r\n"))
	}
	term.Flush()
	theme.ResetSGRCache()
	f.frame()
	first := theme.SGRCache()
	if first.Misses == 0 {
		t.Fatal("cold frame missed nothing")
	}
	f.tui.dirty = true
	f.frame()
	second := theme.SGRCache()
	if missed := second.Misses - first.Misses; missed > 1 {
		t.Fatalf("warm frame added %d misses, want the same styles to hit", missed)
	}
}
