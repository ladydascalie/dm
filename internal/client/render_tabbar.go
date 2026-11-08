package client

import (
	"bytes"
	"fmt"
	"strings"

	"dm/internal/cells"
	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/rail"
	"dm/internal/theme"
	"dm/internal/wire"
	"dm/internal/wlabel"
)

// tabWidth is the tab bar's tab width bounds in cells. A tab is its name
// centered with at least one padding cell on each side, clamped to these bounds.
type tabWidth struct {
	Min int
	Max int
}

// tabWidthFor returns the tab width bounds the config asks for.
func tabWidthFor(cfg *keymap.Config) tabWidth {
	return tabWidth{Min: cfg.TabMinWidth(), Max: cfg.TabMaxWidth()}
}

// tabBarRect returns the tab bar row: row 0, to the right of the rail. It is
// always one row high. A screen too short for a tab bar returns an empty rect.
func tabBarRect(snap *wire.Snapshot) layout.Rect {
	if snap == nil || snap.Cols < 1 || snap.Rows < 3 {
		return layout.Rect{}
	}
	x := snap.Rail
	if x < 0 {
		x = 0
	}
	w := snap.Cols - x
	if w < 1 {
		w = 1
	}
	return layout.Rect{X: x, Y: 0, W: w, H: layout.TabBarRows}
}

// tabBarItem is one window tab: its window index, its name and its screen
// rect.
type tabBarItem struct {
	Index int
	Name  string
	Label string
	Rect  layout.Rect
}

// tabCells is the width of one tab: the name plus one padding cell on each
// side, clamped to the configured bounds.
func tabCells(name string, tw tabWidth) int {
	w := cells.Cells(name) + 2
	if w < tw.Min {
		w = tw.Min
	}
	if w > tw.Max {
		w = tw.Max
	}
	if w < 3 {
		w = 3
	}
	return w
}

// tabBarItems lays out one tab per window in the active workspace. A tab is its
// name centered with at least one padding cell on each side, clamped to the
// configured width bounds. Tabs run left to right and stop at the bar's right
// edge; a tab that does not fit is dropped.
func tabBarItems(snap *wire.Snapshot, tw tabWidth) []tabBarItem {
	bar := tabBarRect(snap)
	if bar.W < 1 || bar.H < 1 {
		return nil
	}
	out := make([]tabBarItem, 0, len(snap.Windows))
	x := bar.X
	for i, win := range snap.Windows {
		label := wlabel.Render(snap.WindowLabel, rail.WindowVars(snap, win))
		width := tabCells(label, tw)
		if x+width > bar.X+bar.W {
			break
		}
		out = append(out, tabBarItem{
			Index: i,
			Name:  win.Name,
			Label: label,
			Rect:  layout.Rect{X: x, Y: bar.Y, W: width, H: bar.H},
		})
		x += width
	}
	return out
}

// tabBarAt returns the tab under the point. A point outside the tab bar or in
// the gap between tabs returns false.
func tabBarAt(snap *wire.Snapshot, x, y int, tw tabWidth) (tabBarItem, bool) {
	bar := tabBarRect(snap)
	if y != bar.Y || x < bar.X || x >= bar.X+bar.W {
		return tabBarItem{}, false
	}
	for _, it := range tabBarItems(snap, tw) {
		if x >= it.Rect.X && x < it.Rect.X+it.Rect.W {
			return it, true
		}
	}
	return tabBarItem{}, false
}

// DrawTabBar paints the window tab bar: one tab per window in the active
// workspace. The active tab is highlighted. The row is painted whole, so a tab
// that goes away leaves no glyph behind.
func DrawTabBar(buf *bytes.Buffer, th theme.Theme, snap *wire.Snapshot, tw tabWidth) {
	bar := tabBarRect(snap)
	if bar.W < 1 || bar.H < 1 {
		return
	}
	p := th.Palette
	fmt.Fprintf(buf, "\x1b[%d;%dH", bar.Y+1, bar.X+1)
	buf.WriteString(groundSGR(p.RailBG))
	buf.WriteString(strings.Repeat(" ", bar.W))
	buf.WriteString("\x1b[0m")
	for _, it := range tabBarItems(snap, tw) {
		if snap.ActiveWindow == it.Index {
			buf.WriteString(theme.Style{Fg: styleColor(p.RailText), Bg: styleColor(p.RailFocusBG)}.SGR())
		} else {
			buf.WriteString(theme.Style{Fg: styleColor(p.RailDim), Bg: styleColor(p.RailBG)}.SGR())
		}
		fmt.Fprintf(buf, "\x1b[%d;%dH", it.Rect.Y+1, it.Rect.X+1)
		buf.WriteString(cells.Center(cells.Elide(it.Label, it.Rect.W-2), it.Rect.W))
	}
	buf.WriteString("\x1b[0m")
}
