// Package hintbar lays out one row of keyboard hints: a filled cap around each
// key, its label after it, and a faint pipe between the hints. It is the only
// place a hint row is built, so every surface wears the same row.
package hintbar

import (
	"bytes"
	"fmt"
	"math"
	"strings"

	"dm/internal/segment"
	"dm/internal/theme"
)

// Hint is one key hint: the keys that fire an action and the label that names
// it. The keys are painted as one token joined by "/", so "j/k" and
// "ctrl+u/ctrl+d" stay whole. An empty key is how a caller says the action is
// unbound, and a hint left without a key drops out of the bar whole. An empty
// label leaves the cap alone, which is how a bare prompt names its keys.
type Hint struct {
	Keys  []string
	Label string
	// Keep says the hint survives a bar too narrow for the whole row. The
	// drops take the tail first, so a Keep hint ends up pinned.
	Keep bool
}

// Bar is one hint row. Right is an optional status pinned at the right edge of
// a row placement; a border placement carries none.
type Bar struct {
	Hints []Hint
	Right []segment.Seg
}

// pipeRot is how far the separator's colour sits from the rail background
// toward the rail dim. 45% is the chosen tone: fainter reads as a scratch,
// brighter competes with the label.
const pipeRot = 0.45

// PipeColour is the separator's derived tone on the rail background. It is
// never the rail dim itself.
func PipeColour(th theme.Theme) theme.RGB {
	return mix(th.Palette.RailBG, th.Palette.RailDim, pipeRot)
}

// mix walks one colour toward another by rot.
func mix(from, to theme.RGB, rot float64) theme.RGB {
	walk := func(a, b int) int {
		return int(math.Round(float64(a) + (float64(b)-float64(a))*rot))
	}
	return theme.RGB{R: walk(from.R, to.R), G: walk(from.G, to.G), B: walk(from.B, to.B)}
}

// Segments fits the hints into width cells and lays them out: caps on the rail
// hover ground, labels and padding on the rail background, faint pipes between
// the hints, and the right status pinned at the right edge. It returns exactly
// the cells it paints, so a caller fills the rest of its row itself.
func (b Bar) Segments(width int, th theme.Theme) []segment.Seg {
	if width <= 0 {
		return nil
	}
	pal := th.Palette
	right := b.Right
	rightCells := segment.SegCells(right)
	if rightCells > 0 {
		// A status is never dropped: a row too narrow for it clips it and
		// gives every other cell to it.
		if rightCells > width {
			right = segment.ClipSegs(right, width)
			rightCells = segment.SegCells(right)
		}
	}
	left := b.left(width-rightCells, th)
	leftCells := segment.SegCells(left)
	out := make([]segment.Seg, 0, len(left)+len(right)+1)
	out = append(out, left...)
	if rightCells > 0 {
		if pad := width - leftCells - rightCells; pad > 0 {
			out = append(out, segment.Seg{Text: strings.Repeat(" ", pad), FG: pal.RailBG, BG: pal.RailBG})
		}
		out = append(out, right...)
	}
	return out
}

// Rows lays the hints out as whole rows of at most width cells, in the order
// they were given, so a caller that owns a block of rows paints every key
// instead of dropping the ones a single row cannot hold. Nothing drops here: the
// rows grow to fit the list. A hint wider than width alone is laid on a row of
// its own and clipped by the painter.
func (b Bar) Rows(width int, th theme.Theme) [][]segment.Seg {
	if width <= 0 {
		return nil
	}
	sep := []segment.Seg{pipe(th)}
	sepCells := segment.SegCells(sep)
	var rows [][]segment.Seg
	var row []segment.Seg
	cells := 0
	for _, h := range b.Hints {
		segs := hintSegs(h, th)
		if len(segs) == 0 {
			continue
		}
		n := segment.SegCells(segs)
		switch {
		case len(row) == 0:
			row, cells = segs, n
		case cells+sepCells+n <= width:
			row = append(append(row, sep...), segs...)
			cells += sepCells + n
		default:
			rows = append(rows, row)
			row, cells = segs, n
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows
}

// Cells is the painted width of the fitted bar.
func (b Bar) Cells(width int, th theme.Theme) int {
	return segment.SegCells(b.Segments(width, th))
}

// Text is the plain text the fitted bar paints at width, styling left out. A
// caller reads a row with it; a painter that needs the colours takes Segments.
func (b Bar) Text(width int, th theme.Theme) string {
	var out strings.Builder
	for _, s := range b.Segments(width, th) {
		out.WriteString(s.Text)
	}
	return out.String()
}

// Line paints a bar inline on a row a caller owns, at x, y, clipped to width
// cells. It writes no fill: the caller's row ground shows in the cells the bar
// leaves alone. It returns the cells written.
func Line(buf *bytes.Buffer, th theme.Theme, x, y, width int, b Bar) int {
	if width <= 0 {
		return 0
	}
	runs := segment.ClipSegs(b.Segments(width, th), width)
	if len(runs) == 0 {
		return 0
	}
	fmt.Fprintf(buf, "\x1b[%d;%dH", y+1, x+1)
	for _, s := range runs {
		segment.Write(buf, s)
	}
	return segment.SegCells(runs)
}

// left fits the hints into width cells and joins them with the pipe.
func (b Bar) left(width int, th theme.Theme) []segment.Seg {
	if width <= 0 {
		return nil
	}
	kept := fitted(b.Hints, width, th)
	var out []segment.Seg
	for i, h := range kept {
		if i > 0 {
			out = append(out, pipe(th))
		}
		out = append(out, hintSegs(h, th)...)
	}
	return out
}

// fitted keeps the longest set of hints that fits width cells, the pipes
// between them included. It drops whole hints from the tail, never a cap, a
// key token or a pipe on its own, and a Keep hint survives the drops. When the
// Keep hints alone are wider than the row there is nothing left to save the
// row with, so they drop from the tail too.
func fitted(hints []Hint, width int, th theme.Theme) []Hint {
	kept := make([]Hint, 0, len(hints))
	for _, h := range hints {
		if len(hintSegs(h, th)) > 0 {
			kept = append(kept, h)
		}
	}
	for len(kept) > 0 && barCells(kept, th) > width {
		drop := -1
		for i := len(kept) - 1; i >= 0; i-- {
			if !kept[i].Keep {
				drop = i
				break
			}
		}
		if drop < 0 {
			drop = len(kept) - 1
		}
		kept = append(kept[:drop], kept[drop+1:]...)
	}
	return kept
}

// barCells is the painted width of a hint list, the pipes between them
// included.
func barCells(hints []Hint, th theme.Theme) int {
	n := 0
	for i, h := range hints {
		if i > 0 {
			n += segment.SegCells([]segment.Seg{pipe(th)})
		}
		n += segment.SegCells(hintSegs(h, th))
	}
	return n
}

// hintSegs is one hint's runs: the cap, then one space and the label. A hint
// with no key paints nothing, and a hint with no label ends on its cap.
func hintSegs(h Hint, th theme.Theme) []segment.Seg {
	keys := make([]string, 0, len(h.Keys))
	for _, k := range h.Keys {
		if k != "" {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	pal := th.Palette
	token := strings.Join(keys, "/")
	cap := segment.Seg{Text: " " + token + " ", FG: pal.RailText, BG: pal.RailHoverBG, Bold: true}
	if h.Label == "" {
		return []segment.Seg{cap}
	}
	return []segment.Seg{cap, {Text: " " + h.Label, FG: pal.RailText, BG: pal.RailBG}}
}

// pipe is the separator between two hints: one space either side of the
// theme's vertical rule, in the derived faint tone. The glyph is the theme's,
// so an ascii theme keeps an ascii pipe.
func pipe(th theme.Theme) segment.Seg {
	pal := th.Palette
	return segment.Seg{
		Text: " " + th.Glyphs.BorderVertical + " ",
		FG:   PipeColour(th),
		BG:   pal.RailBG,
	}
}
