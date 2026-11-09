package vt

import "testing"

// slots returns the raw slots of row y, continuations included. It exists so a
// test can assert the slot layout, which Cell deliberately hides.
func slots(g *Grid, y int) []Cell {
	return g.lines[y]
}

func TestWideCellTakesTwoSlots(t *testing.T) {
	g := NewGrid(8, 2)
	g.Write(2, 1, NewCell("世", DefaultStyle()))

	raw := slots(g, 1)
	if raw[2].Width != WidthWide || raw[2].Content != "世" {
		t.Fatalf("slot 2 = %+v, want the wide cell", raw[2])
	}
	if raw[3].Width != WidthContinuation || raw[3].Content != "" {
		t.Fatalf("slot 3 = %+v, want a continuation", raw[3])
	}
	if raw[4].Width != WidthNormal {
		t.Fatalf("slot 4 = %+v, want an untouched normal cell", raw[4])
	}
	if got := g.Next(2, 1); got != 4 {
		t.Errorf("Next(2,1) = %d, want 4", got)
	}
	if got := g.Text(1); got != "  世" {
		t.Errorf("Text(1) = %q, want %q", got, "  世")
	}
}

func TestCellResolvesContinuation(t *testing.T) {
	g := NewGrid(8, 1)
	g.Write(0, 0, NewCell("世", DefaultStyle()))

	if got := g.Cell(1, 0); got.Content != "世" || got.Width != WidthWide {
		t.Errorf("Cell(1,0) = %+v, want the wide cell at 0", got)
	}
	if got := g.Next(1, 0); got != 2 {
		t.Errorf("Next(1,0) = %d, want 2", got)
	}
}

func TestWriteOnContinuationBlanksPartner(t *testing.T) {
	g := NewGrid(8, 1)
	g.Write(0, 0, NewCell("世", DefaultStyle()))
	g.Write(1, 0, NewCell("a", DefaultStyle()))

	raw := slots(g, 0)
	if raw[0].Content != " " || raw[0].Width != WidthNormal {
		t.Errorf("slot 0 = %+v, want a blank normal cell", raw[0])
	}
	if raw[1].Content != "a" || raw[1].Width != WidthNormal {
		t.Errorf("slot 1 = %+v, want %q", raw[1], "a")
	}
	if got := g.Text(0); got != " a" {
		t.Errorf("Text(0) = %q, want %q", got, " a")
	}
}

func TestWriteOverWideCellBlanksContinuation(t *testing.T) {
	g := NewGrid(8, 1)
	g.Write(0, 0, NewCell("世", DefaultStyle()))
	g.Write(0, 0, NewCell("x", DefaultStyle()))

	raw := slots(g, 0)
	if raw[0].Content != "x" || raw[0].Width != WidthNormal {
		t.Errorf("slot 0 = %+v, want %q", raw[0], "x")
	}
	if raw[1].Width != WidthNormal || raw[1].Content != " " {
		t.Errorf("slot 1 = %+v, want a blank normal cell", raw[1])
	}
}

func TestWideCellAtRightEdgeDegrades(t *testing.T) {
	g := NewGrid(4, 1)
	g.Write(3, 0, NewCell("世", DefaultStyle()))

	raw := slots(g, 0)
	if raw[3].Width != WidthNormal || raw[3].Content != "世" {
		t.Errorf("slot 3 = %+v, want the cluster as a normal cell", raw[3])
	}
}

func TestASCIIHasNoContinuations(t *testing.T) {
	g := NewGrid(8, 2)
	for i, r := range "ascii" {
		g.Write(i, 0, NewCell(string(r), DefaultStyle()))
	}
	for x, c := range slots(g, 0) {
		if c.IsContinuation() {
			t.Fatalf("slot %d of an ASCII row is a continuation", x)
		}
		if c.Width != WidthNormal {
			t.Fatalf("slot %d width = %d, want 1", x, c.Width)
		}
	}
	for x := 0; x < 8; x++ {
		if got := g.Next(x, 0); got != x+1 {
			t.Fatalf("Next(%d) = %d, want %d", x, got, x+1)
		}
	}
	if got := g.Text(0); got != "ascii" {
		t.Errorf("Text(0) = %q, want %q", got, "ascii")
	}
}

func TestResizeKeepsCells(t *testing.T) {
	g := NewGrid(6, 3)
	g.Write(0, 0, NewCell("世", DefaultStyle()))
	g.Write(2, 0, NewCell("a", DefaultStyle()))
	g.Write(3, 2, NewCell("z", DefaultStyle()))

	g.Resize(10, 5)
	if cols, rows := g.Size(); cols != 10 || rows != 5 {
		t.Fatalf("Size() = %d,%d, want 10,5", cols, rows)
	}
	if got := g.Text(0); got != "世a" {
		t.Errorf("after growing, Text(0) = %q, want %q", got, "世a")
	}
	if got := g.Text(2); got != "   z" {
		t.Errorf("after growing, Text(2) = %q, want %q", got, "   z")
	}
	if raw := slots(g, 0); raw[1].Width != WidthContinuation {
		t.Errorf("after growing, slot 1 = %+v, want a continuation", raw[1])
	}

	// Both slots of the wide cell still fit, so shrinking keeps it.
	g.Resize(2, 2)
	if got := g.Text(0); got != "世" {
		t.Errorf("after shrinking to two columns, Text(0) = %q, want %q", got, "世")
	}
	if got := g.Text(1); got != "" {
		t.Errorf("after shrinking, the dropped row Text(1) = %q, want empty", got)
	}
}

func TestResizeBlankedWideCellAtEdge(t *testing.T) {
	g := NewGrid(6, 1)
	g.Write(3, 0, NewCell("世", DefaultStyle()))
	g.Resize(4, 1)

	raw := slots(g, 0)
	if raw[3].Width != WidthNormal || raw[3].IsContinuation() {
		t.Errorf("slot 3 = %+v, want a blank normal cell", raw[3])
	}
}

func TestResizeClampsCursorScrollAndTabs(t *testing.T) {
	g := NewGrid(20, 10)
	g.SetCursor(15, 8)
	g.SetScrollRegion(ScrollRegion{Top: 2, Bottom: 7, Left: 0, Right: 19})
	g.Resize(4, 3)

	if c := g.Cursor(); c.X != 3 || c.Y != 2 {
		t.Errorf("Cursor() = %+v, want 3,2", c)
	}
	if r := g.ScrollRegion(); r.Bottom > 2 || r.Right > 3 {
		t.Errorf("ScrollRegion() = %+v, want clamped to the grid", r)
	}
	if tabs := g.TabStops(); len(tabs) != 4 {
		t.Errorf("len(TabStops()) = %d, want 4", len(tabs))
	}
}

// A pane starts small and grows to its split's box. The default margins cover
// the whole screen, so they must grow with it; a clamped region would wrap and
// scroll inside the pane's first size.
func TestResizeExpandsFullScrollRegion(t *testing.T) {
	g := NewGrid(80, 24)
	g.Resize(52, 20)
	g.Resize(97, 39)
	if r := g.ScrollRegion(); r != (ScrollRegion{Top: 0, Bottom: 38, Left: 0, Right: 96}) {
		t.Fatalf("ScrollRegion() = %+v, want the whole grid", r)
	}

	// Margins the application set stay put and are only clamped.
	g = NewGrid(80, 24)
	g.SetScrollRegion(ScrollRegion{Top: 2, Bottom: 7, Left: 3, Right: 60})
	g.Resize(97, 39)
	if r := g.ScrollRegion(); r != (ScrollRegion{Top: 2, Bottom: 7, Left: 3, Right: 60}) {
		t.Fatalf("ScrollRegion() = %+v, want the application's margins", r)
	}
}

func TestDefaultState(t *testing.T) {
	g := NewGrid(20, 4)

	if c := g.Cursor(); c.X != 0 || c.Y != 0 || !c.Visible || c.WrapNext {
		t.Errorf("Cursor() = %+v, want a visible cursor at 0,0", c)
	}
	m := g.Modes()
	if !m.Wrap || m.Origin || m.Insert || m.AltScreen || m.Mouse != MouseOff {
		t.Errorf("Modes() = %+v, want only wrap on", m)
	}
	if r := g.ScrollRegion(); r != (ScrollRegion{Top: 0, Bottom: 3, Left: 0, Right: 19}) {
		t.Errorf("ScrollRegion() = %+v, want the whole grid", r)
	}
	if cs := g.Charset(); cs != (Charset{}) {
		t.Errorf("Charset() = %+v, want the zero charset", cs)
	}
	for x, stop := range g.TabStops() {
		want := x > 0 && x%8 == 0
		if stop != want {
			t.Errorf("tab stop at %d = %v, want %v", x, stop, want)
		}
	}
}

func TestTabStops(t *testing.T) {
	g := NewGrid(20, 1)

	if got := g.NextTabStop(0); got != 8 {
		t.Errorf("NextTabStop(0) = %d, want 8", got)
	}
	if got := g.NextTabStop(15); got != 16 {
		t.Errorf("NextTabStop(15) = %d, want 16", got)
	}
	if got := g.NextTabStop(19); got != 19 {
		t.Errorf("NextTabStop(19) = %d, want the last column", got)
	}
	if got := g.PrevTabStop(15); got != 8 {
		t.Errorf("PrevTabStop(15) = %d, want 8", got)
	}
	if got := g.PrevTabStop(0); got != 0 {
		t.Errorf("PrevTabStop(0) = %d, want 0", got)
	}

	g.ClearTabStops()
	g.SetTabStop(5)
	if got := g.NextTabStop(0); got != 5 {
		t.Errorf("after clearing, NextTabStop(0) = %d, want 5", got)
	}
}

func TestSaveAndRestoreCursor(t *testing.T) {
	g := NewGrid(10, 5)
	style := DefaultStyle()
	style.Attrs = Bold

	g.SetCursor(4, 3)
	g.SetCharset(Charset{G0: CharsetDECSpecial, G1: CharsetASCII, GL: 1})
	m := g.Modes()
	m.Origin = true
	g.SetModes(m)
	g.SaveCursor(style, Protection{DECSCA: true, ISO: true})

	g.SetCursor(0, 0)
	g.SetCharset(Charset{})
	m.Origin = false
	g.SetModes(m)

	got, prot := g.RestoreCursor()
	if got.Attrs != Bold {
		t.Errorf("RestoreCursor() style = %+v, want bold", got)
	}
	if !prot.DECSCA || !prot.ISO {
		t.Errorf("RestoreCursor() protection = %+v, want both set", prot)
	}
	if c := g.Cursor(); c.X != 4 || c.Y != 3 {
		t.Errorf("after restore, Cursor() = %+v, want 4,3", c)
	}
	if cs := g.Charset(); cs.G0 != CharsetDECSpecial || cs.GL != 1 {
		t.Errorf("after restore, Charset() = %+v, want G0 DEC special, GL 1", cs)
	}
	if !g.Modes().Origin {
		t.Error("after restore, origin mode is off, want on")
	}
}

func TestTextSkipsContinuationAndTrims(t *testing.T) {
	g := NewGrid(10, 1)
	g.Write(0, 0, NewCell("a", DefaultStyle()))
	g.Write(1, 0, NewCell("世", DefaultStyle()))
	g.Write(3, 0, NewCell("b", DefaultStyle()))

	if got := g.Text(0); got != "a世b" {
		t.Errorf("Text(0) = %q, want %q", got, "a世b")
	}
}

// AltTransitions counts each successful switch, so a caller reading output in
// chunks can notice an excursion that entered and left inside one read.
func TestAltTransitionsCountsBothSwitches(t *testing.T) {
	g := NewGrid(10, 3)
	if n := g.AltTransitions(); n != 0 {
		t.Fatalf("new grid transitions = %d, want 0", n)
	}
	g.EnterAltScreen(true)
	g.EnterAltScreen(true) // already there: no switch
	g.ExitAltScreen(true)
	g.ExitAltScreen(true) // already back: no switch
	if n := g.AltTransitions(); n != 2 {
		t.Fatalf("transitions = %d, want 2", n)
	}
}
