// Package testutil holds the test helpers shared by more than one package.
// It is test support only: no production code imports it.
package testutil

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/segment"
	"dm/internal/session"
	"dm/internal/vt"
	"dm/internal/wire"
)

// Frame wraps a payload in the wire length prefix.
func Frame(t *testing.T, payload string) []byte {
	t.Helper()
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, uint32(len(payload)))
	b.WriteString(payload)
	return b.Bytes()
}

// NextMsg reads the next message the client sent, or fails on timeout.
func NextMsg(t *testing.T, got chan wire.Msg) wire.Msg {
	t.Helper()
	select {
	case m, ok := <-got:
		if !ok {
			t.Fatal("conn closed")
		}
		return m
	case <-time.After(time.Second):
		t.Fatal("no message")
	}
	return wire.Msg{}
}

// NoMsg fails when the client sends a message inside the quiet window.
func NoMsg(t *testing.T, got chan wire.Msg) {
	t.Helper()
	select {
	case m := <-got:
		t.Fatalf("unexpected extra message: %+v", m)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestVT builds a terminal painted with data, the way a pane's VT looks.
func TestVT(t *testing.T, w, h int, data string) *vt.Term {
	t.Helper()
	term := vt.NewTerm(w, h)
	term.Write([]byte(data))
	term.Flush()
	return term
}

// CaptureStdout runs fn with os.Stdout on a pipe and returns what it printed.
func CaptureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	fn()
	os.Stdout = old
	_ = w.Close()
	defer func() { _ = r.Close() }()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// Pad is the default pane padding, so a test's geometry matches a session's.
func Pad() int { return keymap.LoadDefault().Padding() }

// TestSnapshot builds a snapshot from a layout tree, with a rect per pane.
func TestSnapshot(root *layout.Node, cols, rows int, mouse map[string]bool) *wire.Snapshot {
	snap := &wire.Snapshot{Root: root, Cols: cols, Rows: rows}
	rects := map[string]layout.Rect{}
	layout.Layout(root, layout.PaneArea(cols, rows), Pad(), rects)
	for _, id := range layout.AllPanes(root) {
		snap.Panes = append(snap.Panes, wire.PaneInfo{ID: id, Rect: rects[id], Mouse: mouse[id]})
	}
	return snap
}

// KeymapTestLoad parses a keymap from text for a test TUI.
func KeymapTestLoad(t *testing.T, src string) *keymap.Config {
	t.Helper()
	cfg, err := keymap.ParseConfig(src)
	if err != nil {
		t.Fatalf("parse test keymap: %v", err)
	}
	return cfg
}

// RailTestSnapshot is a two-pane session with a rail. Panes start after the
// rail, and All carries the same two panes in server order.
func RailTestSnapshot(rail int) *wire.Snapshot {
	root := &layout.Node{
		Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*layout.Node{{Pane: "w1p1"}, {Pane: "w1p2"}},
	}
	snap := &wire.Snapshot{
		Session: "x", Workspace: "main", Windows: []wire.WindowInfo{{ID: "w1", Name: "one"}},
		Cols: 100, Rows: 10, Rail: rail, Root: root, Focus: "w1p1",
	}
	rects := map[string]layout.Rect{}
	layout.Layout(root, layout.PaneAreaFor(snap.Cols, snap.Rows, rail), Pad(), rects)
	snap.Panes = []wire.PaneInfo{
		{ID: "w1p1", Rect: rects["w1p1"], Status: "working"},
		{ID: "w1p2", Rect: rects["w1p2"], Status: "idle"},
	}
	snap.All = []wire.AllPane{
		{
			ID: "w1p1", Name: "alpha", Status: "working", Agent: true,
			Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one",
			Focus: true, WorkspaceActive: true, WindowActive: true, WindowUnfolded: true, WorkspaceUnfolded: true,
		},
		{
			ID: "w1p2", Status: "idle", Agent: true,
			Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one",
			WorkspaceActive: true, WindowActive: true, WindowUnfolded: true, WorkspaceUnfolded: true,
		},
	}
	return snap
}

// RailOnePane is a single pane with a rail, for glyph and width tests.
func RailOnePane(status, name string, rail, cols, rows int) *wire.Snapshot {
	root := &layout.Node{Pane: "w1p1"}
	snap := &wire.Snapshot{Cols: cols, Rows: rows, Rail: rail, Root: root, Focus: "w1p1"}
	rects := map[string]layout.Rect{}
	layout.Layout(root, layout.PaneAreaFor(cols, rows, rail), Pad(), rects)
	snap.Panes = []wire.PaneInfo{{ID: "w1p1", Rect: rects["w1p1"], Status: status}}
	snap.All = []wire.AllPane{{
		ID: "w1p1", Name: name, Status: status,
		Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one",
		Focus: true, WorkspaceActive: true, WindowActive: true, WindowUnfolded: true, WorkspaceUnfolded: true,
	}}
	return snap
}

// AgentRail is RailOnePane with the pane marked as an agent, so its row draws
// a state glyph.
func AgentRail(status string) *wire.Snapshot {
	snap := RailOnePane(status, "", 12, 40, 6)
	snap.All[0].Agent = true
	return snap
}

// BarTestSnap is the footer snapshot the segment tests share: a middle active
// window and an agent in focus, so every elision step has room to show.
func BarTestSnap(cols int) *wire.Snapshot {
	return &wire.Snapshot{
		Session:      "dev",
		Workspace:    "workspace-a",
		Windows:      []wire.WindowInfo{{ID: "w1", Name: "api-server", Named: true}, {ID: "w2", Name: "web", Named: true}, {ID: "w3", Name: "docs", Named: true}},
		ActiveWindow: 1,
		Focus:        "w1p2",
		Panes: []wire.PaneInfo{
			{ID: "w1p1", Name: "bash"},
			{ID: "w1p2", Name: "shell", Status: "idle"},
		},
		All: []wire.AllPane{
			{ID: "w1p1", Agent: false},
			{ID: "w1p2", Agent: true, Status: "idle"},
		},
		Cols: cols,
		Rows: 24,
	}
}

// BarTexts is the text of each span, in paint order.
func BarTexts(segs []segment.Seg) []string {
	out := make([]string, 0, len(segs))
	for _, s := range segs {
		out = append(out, s.Text)
	}
	return out
}

// Family is one glyph: man, joiner, woman, joiner, girl. It paints two cells
// and holds five runes, so any helper that counts runes or walks them one by
// one gets it wrong.
const Family = "\U0001F468\u200D\U0001F469\u200D\U0001F467"

// RootPane gives a state a single focused pane in its first window.
func RootPane(st *session.State) string {
	w := st.ActiveWindow()
	id := w.NewPaneID()
	w.Root = &layout.Node{Pane: id}
	w.Focus = id
	st.Panes[id] = &session.PaneState{}
	return id
}

// PaintTerm writes one pane's rows into buf with absolute cursor moves and a
// full SGR for each style change. Tests that replay a frame use it instead of
// carrying a copy of the client's painter.
func PaintTerm(buf *bytes.Buffer, term *vt.Term, r layout.Rect, row0 int) {
	if term == nil || r.W <= 0 || r.H <= 0 {
		return
	}
	g := term.Grid()
	cols, rows := g.Size()
	for y := 0; y < r.H; y++ {
		fmt.Fprintf(buf, "\x1b[%d;%dH", r.Y+y+1, r.X+1)
		var cur *vt.Style
		for x := 0; x < r.W; {
			var c vt.Cell
			next := x + 1
			row := row0 + y
			if x < cols && row >= 0 && row < rows {
				c = g.Cell(x, row)
				if n := g.Next(x, row); n > next {
					next = n
				}
			}
			buf.WriteString(testSGR(c.Style, cur))
			st := c.Style
			cur = &st
			content := c.Content
			if content == "" || c.Width == vt.WidthContinuation {
				content = " "
			}
			buf.WriteString(content)
			x = next
		}
	}
}

func testSGR(st vt.Style, cur *vt.Style) string {
	if cur != nil && *cur == st {
		return ""
	}
	p := []string{"0"}
	if st.Attrs&vt.Bold != 0 {
		p = append(p, "1")
	}
	if st.Underline != vt.UnderlineNone {
		p = append(p, "4")
	}
	if st.Attrs&vt.Reverse != 0 {
		p = append(p, "7")
	}
	p = append(p, testColor(st.Fg, false), testColor(st.Bg, true))
	return "\x1b[" + strings.Join(p, ";") + "m"
}

func testColor(c vt.Color, bg bool) string {
	base := 30
	if bg {
		base = 40
	}
	switch c.Kind {
	case vt.ColorPalette:
		n := int(c.Index)
		switch {
		case n < 8:
			return fmt.Sprint(base + n)
		case n < 16:
			return fmt.Sprint(base + 60 + n - 8)
		default:
			if bg {
				return "48;5;" + fmt.Sprint(n)
			}
			return "38;5;" + fmt.Sprint(n)
		}
	case vt.ColorRGB:
		if bg {
			return fmt.Sprintf("48;2;%d;%d;%d", c.R, c.G, c.B)
		}
		return fmt.Sprintf("38;2;%d;%d;%d", c.R, c.G, c.B)
	default:
		if bg {
			return "49"
		}
		return "39"
	}
}
