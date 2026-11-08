package segment

import (
	"bytes"
	"dm/internal/cells"
	"dm/internal/theme"
)

// Seg is one styled run of text: what it paints, the colours it paints with,
// and its own weight. The ground rides on the run, so one row can mix
// backgrounds span by span; the rail instead carries a single ground per row
// and stamps it on through writeRailSeg. Pin marks the run that opens the block
// anchored to the last column.
type Seg struct {
	Text string
	FG   theme.RGB
	BG   theme.RGB
	Bold bool
	Pin  bool
}

// SegCells is the painted cell width of a run set, counted in cells by
// text.go, never in runes.
func SegCells(runs []Seg) int {
	n := 0
	for _, s := range runs {
		n += cells.Cells(s.Text)
	}
	return n
}

// ClipSegs keeps the first budget cells of a run set, whole clusters only. The
// run whose head does not fit ends the set: a later run would paint out of
// order, so it goes too.
func ClipSegs(runs []Seg, budget int) []Seg {
	var out []Seg
	for _, s := range runs {
		if budget <= 0 {
			break
		}
		text := cells.Clip(s.Text, budget)
		if text == "" {
			if s.Text != "" {
				break
			}
			continue
		}
		budget -= cells.Cells(text)
		s.Text = text
		out = append(out, s)
	}
	return out
}

// ElideSegs cuts a run set to a cell budget and ends it with an cells.Ellipsis, so a
// long label shrinks before the right-aligned runs beside it lose a cell.
func ElideSegs(th theme.Theme, runs []Seg, budget int) []Seg {
	if budget <= 0 {
		return nil
	}
	out := ClipSegs(runs, budget-1)
	return append(out, Seg{Text: cells.Ellipsis, FG: th.Palette.RailDim})
}

// Join lays groups left to right with one gap run between them, so
// neighbouring chips never touch. The caller chooses the gap, because the
// ground between two chips is the row's own policy.
func Join(groups [][]Seg, gap Seg) []Seg {
	var out []Seg
	for i, group := range groups {
		if i > 0 {
			out = append(out, gap)
		}
		out = append(out, group...)
	}
	return out
}

// Write paints one run: reset, weight, foreground and ground in one cached
// escape, then the text. The colours are written even when they are zero,
// because black is a colour a theme may set (rail-bg #000000) and a skipped
// escape would paint the run on whatever ground is live. An empty run paints
// nothing.
func Write(buf *bytes.Buffer, s Seg) {
	if s.Text == "" {
		return
	}
	st := theme.Style{
		Fg: theme.RGBColor(uint8(s.FG.R), uint8(s.FG.G), uint8(s.FG.B)),
		Bg: theme.RGBColor(uint8(s.BG.R), uint8(s.BG.G), uint8(s.BG.B)),
	}
	if s.Bold {
		st.Attrs = theme.AttrBold
	}
	buf.WriteString(st.SGR())
	buf.WriteString(s.Text)
}
