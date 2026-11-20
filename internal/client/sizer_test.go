package client

import (
	"bytes"
	"errors"
	"io/fs"
	"path/filepath"
	"testing"

	"dm/internal/layout"
	"dm/internal/shot"
	"dm/internal/vt"
	"dm/internal/wire"
)

// The sizer message says whether this client fits each pane's terminal to its
// layout rect. A client told it does not keeps the pane's own grid, which the
// sizing client claimed, and draws it in a larger rect.

// TestSizerVerdictTogglesTheRectFit pins the three states the client can be in:
// no sizer message yet, told it sizes, and told it does not.
func TestSizerVerdictTogglesTheRectFit(t *testing.T) {
	t.Parallel()
	tr := &tui{sizing: false, sizerKnown: false}
	if !tr.sizesPanes() {
		t.Fatal("a client that never received a sizer frame stopped fitting the rect")
	}
	sizing, notSizing := true, false
	tr.apply(wire.Msg{Type: "sizer", Sizing: &sizing})
	if !tr.sizesPanes() {
		t.Fatal("a client told it sizes the panes stopped fitting the rect")
	}
	tr.apply(wire.Msg{Type: "sizer", Sizing: &notSizing})
	if tr.sizesPanes() {
		t.Fatal("a client told it does not size the panes kept fitting the rect")
	}
	// A frame with no verdict changes nothing, so an old server cannot make a
	// client give up the fit by leaving the field out.
	tr.apply(wire.Msg{Type: "sizer"})
	if tr.sizesPanes() {
		t.Fatal("a sizer frame without a verdict restored the rect fit")
	}
}

// TestNonSizerKeepsThePanesOwnGrid: a non-sizer restores a screen at the size
// the sizing client chose and does not reflow it to the layout rect.
func TestNonSizerKeepsThePanesOwnGrid(t *testing.T) {
	t.Parallel()
	src := vt.NewTerm(12, 4)
	src.Write([]byte("CLAIMED"))
	src.Flush()
	rect := layout.Rect{X: 0, Y: 0, W: 40, H: 10}

	notSizing := false
	tr := &tui{vts: map[string]*vt.Term{}}
	tr.apply(wire.Msg{Type: "sizer", Sizing: &notSizing})
	if !tr.applySnapshot("w1p1", rect, src.Snapshot()) {
		t.Fatal("applySnapshot refused a screen")
	}
	if cols, rows := tr.vts["w1p1"].Grid().Size(); cols != 12 || rows != 4 {
		t.Fatalf("the non-sizer grid is %dx%d, want the pane's own 12x4", cols, rows)
	}

	// The same screen for the sizing client lands at the rect.
	sizing := true
	tr.apply(wire.Msg{Type: "sizer", Sizing: &sizing})
	tr.snap = &wire.Snapshot{Panes: []wire.PaneInfo{{ID: "w1p1", Rect: rect}}}
	tr.apply(wire.Msg{Type: "screen", Target: "w1p1", VTSnap: src.Snapshot()})
	if cols, rows := tr.vts["w1p1"].Grid().Size(); cols != rect.W || rows != rect.H {
		t.Fatalf("the sizer grid is %dx%d, want the rect %dx%d", cols, rows, rect.W, rect.H)
	}
}

// TestNonSizerPaintsABiggerRectWithoutChangingThePainter: the painter already
// blanks every cell outside the pane's grid, so a non-sizer needs no other
// change to draw a smaller grid in a larger rect.
func TestNonSizerPaintsABiggerRectWithoutChangingThePainter(t *testing.T) {
	t.Parallel()
	term := vt.NewTerm(8, 2)
	term.Write([]byte("ABC"))
	term.Flush()
	r := layout.Rect{X: 0, Y: 0, W: 20, H: 5}
	var buf bytes.Buffer
	drawPaneFromMarkedClip(&buf, term, r, 0, r.W, 0, nil, nil, nil)

	screen := vt.NewTerm(r.W, r.H)
	screen.Write(buf.Bytes())
	screen.Flush()
	g := screen.Grid()
	if got := g.Cell(0, 0).Content; got != "A" {
		t.Fatalf("cell 0,0 is %q, want the pane's own A", got)
	}
	// Column 8 is one past the grid, and row 2 is one past it too.
	if got := g.Cell(8, 0).Content; got != "" && got != " " {
		t.Fatalf("cell past the grid's width is %q, want a blank", got)
	}
	if got := g.Cell(0, 2).Content; got != "" && got != " " {
		t.Fatalf("cell below the grid's height is %q, want a blank", got)
	}
}

// TestSizerTUIKeepsTheGoldenFrame: a single TUI client is the sizing client,
// and its frame is byte-identical to the golden's. The goldens are drawn from a
// tui that never received a sizer frame, so this pins rule (6): the default fit
// and the told-it-sizes fit are the same picture.
func TestSizerTUIKeepsTheGoldenFrame(t *testing.T) {
	t.Parallel()
	scenes, err := shot.LoadScenes(filepath.Join(shotData, "scenes.json"))
	if errors.Is(err, fs.ErrNotExist) {
		t.Skipf("no scene data at %s", shotData)
	}
	if err != nil {
		t.Fatalf("scenes: %v", err)
	}
	themes, err := shot.ThemeNames(shotData)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skipf("no theme data at %s", shotData)
	}
	if err != nil {
		t.Fatalf("themes: %v", err)
	}
	if len(scenes) == 0 || len(themes) == 0 {
		t.Skip("no scenes or themes")
	}
	th, err := shot.LoadTheme(shotData, themes[0])
	if err != nil {
		t.Fatalf("theme %s: %v", themes[0], err)
	}
	sizing := true
	for _, sc := range scenes {
		_, want := renderScene(t, sc, th)
		tr, out := buildSceneTUI(t, sc, th)
		tr.apply(wire.Msg{Type: "sizer", Sizing: &sizing})
		tr.draw()
		if !bytes.Equal(out.Bytes(), want) {
			t.Fatalf("scene %s: a sizing TUI drew a different frame than the golden", sc.Name)
		}
	}
}
