package client

import (
	"bytes"
	"fmt"
	"strings"

	"dm/internal/layout"
	"dm/internal/segment"
	"dm/internal/theme"
	"dm/internal/wire"
)

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
	Rect  layout.Rect
}

// tabBarItems lays out one tab per window in the active workspace. A tab is its
// name plus one padding cell on each side. Tabs run left to right and stop at
// the tab bar's right edge; a tab that does not fit is dropped.
func tabBarItems(snap *wire.Snapshot) []tabBarItem {
	bar := tabBarRect(snap)
	if bar.W < 1 || bar.H < 1 {
		return nil
	}
	out := make([]tabBarItem, 0, len(snap.Windows))
	x := bar.X
	for i, name := range snap.Windows {
		w := segment.SegCells([]segment.Seg{{Text: name}}) + 2
		if w < 3 {
			w = 3
		}
		if x+w > bar.X+bar.W {
			break
		}
		out = append(out, tabBarItem{
			Index: i,
			Name:  name,
			Rect:  layout.Rect{X: x, Y: bar.Y, W: w, H: bar.H},
		})
		x += w
	}
	return out
}

// tabBarAt returns the tab under the point. A point outside the tab bar or in
// the gap between tabs returns false.
func tabBarAt(snap *wire.Snapshot, x, y int) (tabBarItem, bool) {
	bar := tabBarRect(snap)
	if y != bar.Y || x < bar.X || x >= bar.X+bar.W {
		return tabBarItem{}, false
	}
	for _, it := range tabBarItems(snap) {
		if x >= it.Rect.X && x < it.Rect.X+it.Rect.W {
			return it, true
		}
	}
	return tabBarItem{}, false
}

// DrawTabBar paints the window tab bar: one tab per window in the active
// workspace. The active tab is highlighted. The row is painted whole, so a tab
// that goes away leaves no glyph behind.
func DrawTabBar(buf *bytes.Buffer, th theme.Theme, snap *wire.Snapshot) {
	bar := tabBarRect(snap)
	if bar.W < 1 || bar.H < 1 {
		return
	}
	p := th.Palette
	fmt.Fprintf(buf, "\x1b[%d;%dH", bar.Y+1, bar.X+1)
	buf.WriteString("\x1b[0m" + p.RailBG.BG())
	buf.WriteString(strings.Repeat(" ", bar.W))
	buf.WriteString("\x1b[0m")
	for _, it := range tabBarItems(snap) {
		if snap.ActiveWindow == it.Index {
			buf.WriteString("\x1b[0m" + p.RailFocusBG.BG() + p.RailText.FG())
		} else {
			buf.WriteString("\x1b[0m" + p.RailBG.BG() + p.RailDim.FG())
		}
		fmt.Fprintf(buf, "\x1b[%d;%dH", it.Rect.Y+1, it.Rect.X+1)
		buf.WriteString(" " + it.Name + " ")
	}
	buf.WriteString("\x1b[0m")
}
