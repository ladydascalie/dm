package client

import (
	"net"
	"strings"
	"testing"
	"unicode/utf8"

	"dm/internal/layout"
	"dm/internal/testutil"
	"dm/internal/transport"
	"dm/internal/vt"
	"dm/internal/wire"
)

// clientTUI builds a client with a real snapshot and a conn over net.Pipe, and
// a reader that collects every message the client sends.
func clientTUI(t *testing.T, mouse map[string]bool) (*tui, chan wire.Msg) {
	t.Helper()
	row := &layout.Node{Pane: "a"}
	snap := testutil.TestSnapshot(row, 100, 30, mouse)
	snap.Focus = "a"
	tui := &tui{snap: snap, vts: map[string]*vt.Term{}}
	a, b := net.Pipe()
	t.Cleanup(func() {
		a.Close()
		b.Close()
	})
	tui.conn = &transport.Conn{Conn: a}
	got := make(chan wire.Msg, 16)
	go func() {
		for {
			m, err := wire.ReadMsg(b)
			if err != nil {
				close(got)
				return
			}
			got <- m
		}
	}()
	return tui, got
}

// boxScreenRow reads one row of a replayed screen, trailing blanks dropped.
func boxScreenRow(screen *vt.Term, y, x, w int) string {
	var b strings.Builder
	for i := 0; i < w; i++ {
		r := switcherCell(screen, x+i, y)
		if r == 0 {
			r = ' '
		}
		b.WriteRune(r)
	}
	return strings.TrimRight(b.String(), " ")
}

// testCell is the slice of a cell the root client tests read.
type testCell struct {
	Char    rune
	Content string
	FG      vt.Color
	BG      vt.Color
	Style   vt.Style
}

// termCell reads one cell of a terminal as a testCell.
func termCell(term *vt.Term, x, y int) testCell {
	c := term.Grid().Cell(x, y)
	r, _ := utf8.DecodeRuneInString(c.Content)
	return testCell{Char: r, Content: c.Content, FG: c.Style.Fg, BG: c.Style.Bg, Style: c.Style}
}

// screenString reads every row of a terminal as text.
func screenString(term *vt.Term) string {
	if term == nil {
		return ""
	}
	g := term.Grid()
	_, rows := g.Size()
	lines := make([]string, rows)
	for y := 0; y < rows; y++ {
		lines[y] = g.Text(y)
	}
	return strings.Join(lines, "\n")
}

// runeOfCell returns the first rune of a cell.
func runeOfCell(c vt.Cell) rune {
	r, _ := utf8.DecodeRuneInString(c.Content)
	return r
}
