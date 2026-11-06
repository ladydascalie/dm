package client

// Helpers copied from dm_test.go and box_view_client_test.go for the client
// tests. The partition-client unit deletes the duplicates from dm_test.go.

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/segment"
	"dm/internal/transport"
	"dm/internal/wire"

	"github.com/hinshun/vt10x"
)

func frame(t *testing.T, payload string) []byte {
	t.Helper()
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, uint32(len(payload)))
	b.WriteString(payload)
	return b.Bytes()
}

// railTestSnapshot is a two-pane session with a rail. Panes start after the
// rail, and All carries the same two panes in server order.
func railTestSnapshot(rail int) *wire.Snapshot {
	root := &layout.Node{
		Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*layout.Node{{Pane: "w1p1"}, {Pane: "w1p2"}},
	}
	snap := &wire.Snapshot{
		Session: "x", Workspace: "main", Windows: []string{"one"},
		Cols: 100, Rows: 10, Rail: rail, Root: root, Focus: "w1p1",
	}
	rects := map[string]layout.Rect{}
	layout.Layout(root, layout.PaneAreaFor(snap.Cols, snap.Rows, rail), rects)
	snap.Panes = []wire.PaneInfo{
		{ID: "w1p1", Rect: rects["w1p1"], Status: "working"},
		{ID: "w1p2", Rect: rects["w1p2"], Status: "idle"},
	}
	snap.All = []wire.AllPane{
		{
			ID: "w1p1", Name: "alpha", Status: "working", Agent: true,
			Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one",
			Focus: true, WorkspaceActive: true, WindowActive: true, WindowUnfolded: true,
		},
		{
			ID: "w1p2", Status: "idle", Agent: true,
			Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one",
			WorkspaceActive: true, WindowActive: true, WindowUnfolded: true,
		},
	}
	return snap
}

// railOnePane is a single pane with a rail, for glyph and width tests.
func railOnePane(status, name string, rail, cols, rows int) *wire.Snapshot {
	root := &layout.Node{Pane: "w1p1"}
	snap := &wire.Snapshot{Cols: cols, Rows: rows, Rail: rail, Root: root, Focus: "w1p1"}
	rects := map[string]layout.Rect{}
	layout.Layout(root, layout.PaneAreaFor(cols, rows, rail), rects)
	snap.Panes = []wire.PaneInfo{{ID: "w1p1", Rect: rects["w1p1"], Status: status}}
	snap.All = []wire.AllPane{{
		ID: "w1p1", Name: name, Status: status,
		Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one",
		Focus: true, WorkspaceActive: true, WindowActive: true, WindowUnfolded: true,
	}}
	return snap
}

// agentRail is railOnePane with the pane marked as an agent, so its row draws
// a state glyph.
func agentRail(status string) *wire.Snapshot {
	snap := railOnePane(status, "", 12, 40, 6)
	snap.All[0].Agent = true
	return snap
}

// barTestSnap is the footer snapshot the segment tests share: a middle active
// window and an agent in focus, so every elision step has room to show.
func barTestSnap(cols int) *wire.Snapshot {
	return &wire.Snapshot{
		Session:      "dev",
		Workspace:    "workspace-a",
		Windows:      []string{"api-server", "web", "docs"},
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

// barTexts is the text of each span, in paint order.
func barTexts(segs []segment.Seg) []string {
	out := make([]string, 0, len(segs))
	for _, s := range segs {
		out = append(out, s.Text)
	}
	return out
}

// clientTUI builds a client with a real snapshot and a conn over net.Pipe, and
// a reader that collects every message the client sends.
func clientTUI(t *testing.T, mouse map[string]bool) (*tui, chan wire.Msg) {
	t.Helper()
	row := &layout.Node{Pane: "a"}
	snap := testSnapshot(row, 100, 30, mouse)
	snap.Focus = "a"
	tui := &tui{snap: snap, vts: map[string]vt10x.Terminal{}}
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

func nextMsg(t *testing.T, got chan wire.Msg) wire.Msg {
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

func noMsg(t *testing.T, got chan wire.Msg) {
	t.Helper()
	select {
	case m := <-got:
		t.Fatalf("unexpected extra message: %+v", m)
	case <-time.After(50 * time.Millisecond):
	}
}

// testVT builds a terminal painted with data, the way a pane's VT looks.
func testVT(t *testing.T, w, h int, data string) vt10x.Terminal {
	t.Helper()
	vt := vt10x.New(vt10x.WithSize(w, h))
	if _, err := vt.Write([]byte(data)); err != nil {
		t.Fatalf("write vt: %v", err)
	}
	return vt
}

// captureStdout runs fn with os.Stdout on a pipe and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	fn()
	os.Stdout = old
	w.Close()
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func testSnapshot(root *layout.Node, cols, rows int, mouse map[string]bool) *wire.Snapshot {
	snap := &wire.Snapshot{Root: root, Cols: cols, Rows: rows}
	rects := map[string]layout.Rect{}
	layout.Layout(root, layout.PaneArea(cols, rows), rects)
	for _, id := range layout.AllPanes(root) {
		snap.Panes = append(snap.Panes, wire.PaneInfo{ID: id, Rect: rects[id], Mouse: mouse[id]})
	}
	return snap
}

// keymapTestLoad parses a keymap from text for a test TUI.
func keymapTestLoad(t *testing.T, src string) *keymap.Config {
	t.Helper()
	cfg, err := keymap.ParseConfig(src)
	if err != nil {
		t.Fatalf("parse test keymap: %v", err)
	}
	return cfg
}

// boxScreenRow reads one row of a replayed screen, trailing blanks dropped.
func boxScreenRow(screen vt10x.Terminal, y, x, w int) string {
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

// family is one glyph: man, joiner, woman, joiner, girl. It paints two cells and
// holds five runes, so any helper that counts runes or walks them one by one
// gets it wrong.
const family = "\U0001F468\u200D\U0001F469\u200D\U0001F467"
