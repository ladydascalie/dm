package main

import "bytes"

// seg is one styled run of text: what it paints, the colours it paints with,
// and its own weight. The ground rides on the run, so one row can mix
// backgrounds span by span; the rail instead carries a single ground per row
// and stamps it on through writeRailSeg. pin marks the run that opens the block
// anchored to the last column.
type seg struct {
	text string
	fg   rgb
	bg   rgb
	bold bool
	pin  bool
}

// segCells is the painted cell width of a run set, counted in cells by
// text.go, never in runes.
func segCells(runs []seg) int {
	n := 0
	for _, s := range runs {
		n += cells(s.text)
	}
	return n
}

// clipSegs keeps the first budget cells of a run set, whole clusters only. The
// run whose head does not fit ends the set: a later run would paint out of
// order, so it goes too.
func clipSegs(runs []seg, budget int) []seg {
	var out []seg
	for _, s := range runs {
		if budget <= 0 {
			break
		}
		text := clip(s.text, budget)
		if text == "" {
			if s.text != "" {
				break
			}
			continue
		}
		budget -= cells(text)
		s.text = text
		out = append(out, s)
	}
	return out
}

// elideSegs cuts a run set to a cell budget and ends it with an ellipsis, so a
// long label shrinks before the right-aligned runs beside it lose a cell.
func elideSegs(runs []seg, budget int) []seg {
	if budget <= 0 {
		return nil
	}
	out := clipSegs(runs, budget-1)
	return append(out, seg{text: ellipsis, fg: railDim})
}

// join lays groups left to right with one gap run between them, so
// neighbouring chips never touch. The caller chooses the gap, because the
// ground between two chips is the row's own policy.
func join(groups [][]seg, gap seg) []seg {
	var out []seg
	for i, group := range groups {
		if i > 0 {
			out = append(out, gap)
		}
		out = append(out, group...)
	}
	return out
}

// write paints one run: reset, ground, weight, foreground, then the text. The
// colours are written even when they are zero, because black is a colour a
// theme may set (rail-bg #000000) and a skipped escape would paint the run on
// whatever ground is live. An empty run paints nothing.
func write(buf *bytes.Buffer, s seg) {
	if s.text == "" {
		return
	}
	buf.WriteString("\x1b[0m" + s.bg.bg())
	if s.bold {
		buf.WriteString("\x1b[1m")
	}
	buf.WriteString(s.fg.fg())
	buf.WriteString(s.text)
}

// railSeg and barSeg are the names the rail and the bar gave this run before it
// was one type. The switcher's rail rows (switcher_draw.go), rail_test.go and
// dm_test.go still spell them; each name goes when its caller does.
type (
	railSeg = seg
	barSeg  = seg
)

// barCells is the old name for a bar run's width. dm_test.go still spells it.
func barCells(runs []seg) int { return segCells(runs) }
