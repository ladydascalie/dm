package client

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"dm/internal/cells"
	"dm/internal/layout"
	"dm/internal/segment"
	"dm/internal/shot"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"
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

// oldBarRow is the status bar row as drawBar wrote it before the cache carried
// its ground: a reset, then the bare background. It is the frozen reference the
// cached row must still paint.
func oldBarRow(th theme.Theme, snap *wire.Snapshot, armed bool, info string, set theme.GlyphSet) string {
	var b bytes.Buffer
	segs := barSegments(th, snap, armed, info, set)
	left, pinned := barSplitPinned(segs)
	fmt.Fprintf(&b, "\x1b[%d;1H", snap.Rows)
	for _, s := range left {
		segment.Write(&b, s)
	}
	if n := snap.Cols - segment.SegCells(segs); n > 0 {
		b.WriteString("\x1b[0m")
		b.WriteString(th.Palette.RailBG.BG())
		b.WriteString(strings.Repeat(" ", n))
	}
	for _, s := range pinned {
		segment.Write(&b, s)
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

// oldTabBarRow is the tab bar row as DrawTabBar wrote it before the cache
// carried its escapes: reset, ground and foreground as separate escapes.
func oldTabBarRow(th theme.Theme, snap *wire.Snapshot, tw tabWidth) string {
	bar := tabBarRect(snap)
	if bar.W < 1 || bar.H < 1 {
		return ""
	}
	p := th.Palette
	var b bytes.Buffer
	fmt.Fprintf(&b, "\x1b[%d;%dH", bar.Y+1, bar.X+1)
	b.WriteString("\x1b[0m")
	b.WriteString(p.RailBG.BG())
	b.WriteString(strings.Repeat(" ", bar.W))
	b.WriteString("\x1b[0m")
	for _, it := range tabBarItems(snap, tw) {
		if snap.ActiveWindow == it.Index {
			b.WriteString("\x1b[0m")
			b.WriteString(p.RailFocusBG.BG())
			b.WriteString(p.RailText.FG())
		} else {
			b.WriteString("\x1b[0m")
			b.WriteString(p.RailBG.BG())
			b.WriteString(p.RailDim.FG())
		}
		fmt.Fprintf(&b, "\x1b[%d;%dH", it.Rect.Y+1, it.Rect.X+1)
		b.WriteString(cells.Center(cells.Elide(it.Label, it.Rect.W-2), it.Rect.W))
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

// sameCells proves two byte strings leave a terminal in the same visible
// state: the same glyph in every cell, in the same colours and attributes.
func sameCells(t *testing.T, oldBytes, newBytes string, cols, rows int) {
	t.Helper()
	render := func(b []byte) *vt.Grid {
		term := vt.NewTerm(cols, rows)
		term.Write(b)
		term.Flush()
		return term.Grid()
	}
	a, b := render([]byte(oldBytes)), render([]byte(newBytes))
	if aw, ah := a.Size(); aw != cols || ah != rows {
		t.Fatalf("reference grid %dx%d, want %dx%d", aw, ah, cols, rows)
	}
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			if got, want := b.Cell(x, y), a.Cell(x, y); got != want {
				t.Fatalf("cell %d,%d: cached %+v, direct %+v", x, y, got, want)
			}
		}
	}
}

// TestBarRowPaintsLikeTheDirectForm proves the status bar's cached row paints
// every cell exactly as the pre-change direct escapes did, and that a second
// identical row asks the cache and hits.
func TestBarRowPaintsLikeTheDirectForm(t *testing.T) {
	setDepth(t, theme.TrueColor)
	th := theme.DefaultTheme()
	snap := &wire.Snapshot{
		Session: "main", Workspace: "s1",
		Windows: testWindows("one", "two"), ActiveWindow: 0,
		Cols: 100, Rows: 6, Rail: 26,
		Focus: "w1p1",
		Panes: []wire.PaneInfo{{ID: "w1p1", Name: "alpha", Status: "working"}},
	}

	var direct bytes.Buffer
	drawBar(&direct, th, snap, false, "", theme.GlyphNerd)
	want := oldBarRow(th, snap, false, "", theme.GlyphNerd)
	sameCells(t, want, direct.String(), snap.Cols, snap.Rows)

	theme.ResetSGRCache()
	var cold, warm bytes.Buffer
	drawBar(&cold, th, snap, false, "", theme.GlyphNerd)
	first := theme.SGRCache()
	if first.Misses == 0 {
		t.Fatal("first bar row missed the cache nothing")
	}
	drawBar(&warm, th, snap, false, "", theme.GlyphNerd)
	second := theme.SGRCache()
	if cold.String() != warm.String() {
		t.Fatalf("identical bar rows differ:\n cold %q\n warm %q", cold.String(), warm.String())
	}
	if missed := second.Misses - first.Misses; missed != 0 {
		t.Fatalf("second bar row added %d misses, want the same styles to hit", missed)
	}
	if second.Hits <= first.Hits {
		t.Fatalf("second bar row added no hits: %v then %v", first, second)
	}
}

// TestTabBarRowPaintsLikeTheDirectForm proves the tab bar's cached row paints
// every cell exactly as the pre-change direct escapes did, and that a second
// identical row asks the cache and hits.
func TestTabBarRowPaintsLikeTheDirectForm(t *testing.T) {
	setDepth(t, theme.TrueColor)
	th := theme.DefaultTheme()
	snap := &wire.Snapshot{
		Cols: 100, Rows: 20, Rail: 26,
		Windows: testWindows("one", "two"), ActiveWindow: 1,
	}
	tw := testTabWidth()

	var direct bytes.Buffer
	DrawTabBar(&direct, th, snap, tw)
	want := oldTabBarRow(th, snap, tw)
	sameCells(t, want, direct.String(), snap.Cols, snap.Rows)

	theme.ResetSGRCache()
	var cold, warm bytes.Buffer
	DrawTabBar(&cold, th, snap, tw)
	first := theme.SGRCache()
	if first.Misses == 0 {
		t.Fatal("first tab bar row missed the cache nothing")
	}
	DrawTabBar(&warm, th, snap, tw)
	second := theme.SGRCache()
	if cold.String() != warm.String() {
		t.Fatalf("identical tab bar rows differ:\n cold %q\n warm %q", cold.String(), warm.String())
	}
	if missed := second.Misses - first.Misses; missed != 0 {
		t.Fatalf("second tab bar row added %d misses, want the same styles to hit", missed)
	}
	if second.Hits <= first.Hits {
		t.Fatalf("second tab bar row added no hits: %v then %v", first, second)
	}
}

// TestModalInputRowPaintsLikeTheDirectForm proves the rename row's cached
// foreground and ground paint as the direct foreground over the bare ground
// did.
func TestModalInputRowPaintsLikeTheDirectForm(t *testing.T) {
	setDepth(t, theme.TrueColor)
	th := theme.DefaultTheme()
	r := layout.Rect{X: 2, Y: 1, W: 30, H: 1}

	var direct bytes.Buffer
	renameInputRow(&direct, th, r, "workspace", "worktree")

	// The pre-change row: a reset, the bare ground, then one foreground escape
	// per run.
	p := th.Palette
	const caret = "\u258f"
	var want bytes.Buffer
	fmt.Fprintf(&want, "\x1b[%d;%dH\x1b[0m", r.Y+1, r.X+1)
	want.WriteString(p.RailBG.BG())
	fmt.Fprintf(&want, "%s name: ", p.RailDim.FG())
	want.WriteString(p.RailText.FG())
	want.WriteString("worktree")
	want.WriteString(p.BarAccent.FG())
	want.WriteString(caret)
	if n := r.W - cells.Cells(" name: ") - cells.Cells("worktree") - 1; n > 0 {
		want.WriteString(p.RailBG.BG())
		want.WriteString(strings.Repeat(" ", n))
	}
	want.WriteString("\x1b[0m")

	// The row sits on a wider screen, so no cell wraps.
	sameCells(t, want.String(), direct.String(), 40, 3)
}
