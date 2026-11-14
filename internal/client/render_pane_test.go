package client

// The screen clip on pane rows: a pane whose rect runs past the screen paints
// only the columns inside it, so a frame drawn on a stale snapshot carries no
// cells past the edge.

import (
	"bytes"
	"testing"
	"unicode/utf8"

	"dm/internal/layout"
	"dm/internal/theme"
	"dm/internal/vt"
	"dm/internal/wire"
)

// checkFrameCols walks a frame and fails on any content cell past cols. A
// cursor move to column c starts a run of printable bytes; the run reaches
// column c+len-1. Escape sequences end a run and keep the column, so an SGR
// between a move and its cells does not hide the cells.
func checkFrameCols(t *testing.T, frame string, cols int) {
	t.Helper()
	col, run := 0, 0
	fail := func() {
		if col > 0 && col+run-1 > cols {
			t.Fatalf("frame writes cell %d, past the %d-col screen", col+run-1, cols)
		}
		run = 0
	}
	for i := 0; i < len(frame); {
		if frame[i] != 0x1b {
			// Cells, not bytes: a glyph's UTF-8 length is not its width.
			_, w := utf8.DecodeRuneInString(frame[i:])
			run++
			i += w
			continue
		}
		fail()
		j := i + 1
		if j < len(frame) && frame[j] == '[' {
			j++
			for j < len(frame) && (frame[j] < 0x40 || frame[j] > 0x7e) {
				j++
			}
			if j < len(frame) && frame[j] == 'H' {
				// A cursor move: the params carry the row and column.
				params := frame[i+2 : j]
				for k := 0; k < len(params); k++ {
					if params[k] == ';' {
						col = 0
						for _, d := range params[k+1:] {
							if d < '0' || d > '9' {
								break
							}
							col = col*10 + int(d-'0')
						}
						break
					}
				}
			}
			i = j + 1
			continue
		}
		i = j + 1
	}
	fail()
}

// staleShrinkTUI is a one-pane session whose snapshot the test shrinks without
// refitting the pane's rect, the state between a SIGWINCH and the server's
// next snapshot.
func staleShrinkTUI(t *testing.T, cols int) (*tui, *bytes.Buffer, layout.Rect) {
	t.Helper()
	snap := &wire.Snapshot{Cols: 100, Rows: 30, Rail: 0,
		Root: &layout.Node{Pane: "w1p1"}, Focus: "w1p1"}
	rect := layout.Rect{X: 50, Y: 4, W: 40, H: 10}
	snap.Panes = []wire.PaneInfo{{ID: "w1p1", Rect: rect}}
	snap.All = []wire.AllPane{{
		ID: "w1p1", Name: "alpha", Workspace: "s1", WorkspaceName: "main",
		Window: "w1", WindowName: "one", Focus: true, WorkspaceActive: true,
		WindowActive: true, WindowUnfolded: true, WorkspaceUnfolded: true,
	}}
	snap.Cols = cols
	out := &bytes.Buffer{}
	tui := &tui{snap: snap, vts: map[string]*vt.Term{}, out: out, glyphs: theme.GlyphNerd}
	term := vt.NewTerm(40, rect.H)
	term.Write([]byte("overflow-row"))
	term.Flush()
	tui.vts["w1p1"] = term
	return tui, out, rect
}

// A shrink the server has not confirmed yet leaves the pane's rect wider than
// the screen. The frame drawn from the stale snapshot clips the pane rows to
// the screen: no cell lands past the edge, and the row shows the cells the
// clip kept.
func TestPaneRowClipsToScreenOnStaleSnapshot(t *testing.T) {
	t.Parallel()
	tui, out, rect := staleShrinkTUI(t, 100)
	tui.draw() // the first frame fits, so the second is the shrink's frame

	stale := *tui.snap
	stale.Cols = 60
	tui.snap = &stale
	out.Reset()
	tui.draw()
	checkFrameCols(t, out.String(), stale.Cols)

	live := vt.NewTerm(stale.Cols, stale.Rows)
	live.Write(out.Bytes())
	live.Flush()
	for x := rect.X; x < stale.Cols; x++ {
		if c := live.Grid().Cell(x, rect.Y); c.Content == "" {
			t.Fatalf("column %d is blank, want the clipped row's cells", x)
		}
	}
	if got := live.Grid().Cell(rect.X, rect.Y).Content; got != "o" {
		t.Fatalf("first kept cell = %q, want the row's first cell", got)
	}
}

// A pane whose rect starts before the screen paints from its first on-screen
// column, not from the rect's left edge.
func TestPaneRowClipsLeftEdge(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	term := vt.NewTerm(10, 1)
	term.Write([]byte("0123456789"))
	term.Flush()
	r := layout.Rect{X: -3, Y: 0, W: 10, H: 1}
	drawPaneRow(&buf, term, r, 0, 0, nil, nil, nil, 6)
	live := vt.NewTerm(6, 1)
	live.Write(buf.Bytes())
	live.Flush()
	g := live.Grid()
	want := "345678"
	for i, w := range want {
		if got := g.Cell(i, 0).Content; got != string(w) {
			t.Fatalf("cell %d = %q, want %q", i, got, string(w))
		}
	}
}

// A pane whose rect runs past the screen paints only the columns inside it:
// the cells past the screen width never reach the frame.
func TestPaneRowClipsRightEdge(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	term := vt.NewTerm(10, 1)
	term.Write([]byte("0123456789"))
	term.Flush()
	r := layout.Rect{X: 0, Y: 0, W: 10, H: 1}
	drawPaneRow(&buf, term, r, 0, 0, nil, nil, nil, 6)
	live := vt.NewTerm(10, 1)
	live.Write(buf.Bytes())
	live.Flush()
	g := live.Grid()
	for i := 0; i < 6; i++ {
		if got := g.Cell(i, 0).Content; got != string(rune('0'+i)) {
			t.Fatalf("cell %d = %q, want %q", i, got, string(rune('0'+i)))
		}
	}
	for i := 6; i < 10; i++ {
		if got := g.Cell(i, 0).Content; got != "" && got != " " {
			t.Fatalf("cell %d = %q, want the clip's blank", i, got)
		}
	}
}

// A pane entirely past the screen paints nothing.
func TestPaneRowOffScreenPaintsNothing(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	term := vt.NewTerm(10, 1)
	term.Write([]byte("0123456789"))
	term.Flush()
	drawPaneRow(&buf, term, layout.Rect{X: 6, Y: 0, W: 10, H: 1}, 0, 0, nil, nil, nil, 6)
	if buf.Len() != 0 {
		t.Fatalf("a pane past the screen painted bytes: %q", buf.String())
	}
}
