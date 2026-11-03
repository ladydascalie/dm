package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hinshun/vt10x"
)

func TestOSCFilter(t *testing.T) {
	var got []Status
	f := &oscFilter{status: func(s Status) { got = append(got, s) }}
	var out bytes.Buffer

	f.feed([]byte("hello \x1b]7501;{\"state\":\"work"), &out)
	if out.String() != "hello " {
		t.Fatalf("partial sequence leaked: %q", out.String())
	}
	f.feed([]byte("ing\",\"message\":\"build\"}\x07world"), &out)
	if out.String() != "hello world" {
		t.Fatalf("got %q", out.String())
	}
	if len(got) != 1 || got[0].State != "working" || got[0].Message != "build" {
		t.Fatalf("status %+v", got)
	}

	out.Reset()
	f.feed([]byte("\x1b]7501;idle\x1b\\ok"), &out)
	if out.String() != "ok" || len(got) != 2 || got[1].State != "idle" {
		t.Fatalf("ST terminator: out=%q got=%+v", out.String(), got)
	}

	out.Reset()
	f.feed([]byte("\x1b]0;title\x07kept"), &out)
	if out.String() != "\x1b]0;title\x07kept" {
		t.Fatalf("other OSC stripped: %q", out.String())
	}
}

func TestLayout(t *testing.T) {
	root := &Node{Dir: "row", Kids: []*Node{
		{Pane: "a"},
		{Dir: "col", Kids: []*Node{{Pane: "b"}, {Pane: "c"}}},
	}}
	rects := map[string]Rect{}
	layout(root, Rect{0, 0, 100, 29}, rects)
	if len(rects) != 3 {
		t.Fatalf("rects %+v", rects)
	}
	seen := map[[2]int]string{}
	for id, r := range rects {
		if r.W <= 0 || r.H <= 0 {
			t.Fatalf("%s has empty rect %+v", id, r)
		}
		for y := r.Y; y < r.Y+r.H; y++ {
			for x := r.X; x < r.X+r.W; x++ {
				if prev, ok := seen[[2]int{x, y}]; ok {
					t.Fatalf("%s and %s overlap at %d,%d", prev, id, x, y)
				}
				seen[[2]int{x, y}] = id
			}
		}
	}
	// Two outer borders, the border/pad/border gap, and two inner borders.
	if rects["a"].W+rects["b"].W+5 != 100 {
		t.Fatalf("widths do not tile: %+v", rects)
	}
	if got := neighbor(rects, "a", "right"); got != "b" {
		t.Fatalf("neighbor right = %q", got)
	}
	if got := neighbor(rects, "b", "down"); got != "c" {
		t.Fatalf("neighbor down = %q", got)
	}
}

// walkNodes calls f for every node in the tree, splits included.
func walkNodes(n *Node, f func(*Node)) {
	if n == nil {
		return
	}
	f(n)
	for _, k := range n.Kids {
		walkNodes(k, f)
	}
}

// boxShapes are the tree shapes the geometry must hold for.
func boxShapes() []struct {
	name string
	root *Node
} {
	leaf := func(id string) *Node { return &Node{Pane: id} }
	row := func(kids ...*Node) *Node { return &Node{Dir: "row", Kids: kids} }
	col := func(kids ...*Node) *Node { return &Node{Dir: "col", Kids: kids} }
	return []struct {
		name string
		root *Node
	}{
		{"single", leaf("a")},
		{"row2", row(leaf("a"), leaf("b"))},
		{"col2", col(leaf("a"), leaf("b"))},
		{"row3", row(leaf("a"), leaf("b"), leaf("c"))},
		{"nested", row(col(leaf("a"), leaf("c")), leaf("b"))},
		{"nested2", col(row(leaf("a"), leaf("b")), leaf("c"))},
	}
}

// TestBoxTiling proves the boxes and pads tile each split exactly, and that
// pane interiors never overlap.
func TestBoxTiling(t *testing.T) {
	for _, shape := range boxShapes() {
		area := Rect{0, 0, 80, 24}
		var walk func(n *Node, a Rect)
		walk = func(n *Node, a Rect) {
			if n.Pane != "" {
				in := insetBox(a)
				if in.W <= 0 || in.H <= 0 {
					t.Fatalf("%s: %s has an empty interior in %v", shape.name, n.Pane, a)
				}
				return
			}
			boxes := childBoxRects(n, a)
			if len(boxes) != len(n.Kids) {
				t.Fatalf("%s: %d boxes for %d kids", shape.name, len(boxes), len(n.Kids))
			}
			used := 0
			for i, b := range boxes {
				if n.Dir == "row" {
					if b.X < a.X || b.X+b.W > a.X+a.W || b.Y != a.Y || b.H != a.H {
						t.Fatalf("%s: box %v escapes %v", shape.name, b, a)
					}
					used += b.W
				} else {
					if b.Y < a.Y || b.Y+b.H > a.Y+a.H || b.X != a.X || b.W != a.W {
						t.Fatalf("%s: box %v escapes %v", shape.name, b, a)
					}
					used += b.H
				}
				if i > 0 {
					used++ // the pad before this box
				}
				walk(n.Kids[i], b)
			}
			want := a.W
			if n.Dir != "row" {
				want = a.H
			}
			if used != want {
				t.Fatalf("%s: boxes and pads use %d of %d", shape.name, used, want)
			}
		}
		walk(shape.root, area)

		rects := map[string]Rect{}
		layout(shape.root, area, rects)
		ids := allPanes(shape.root)
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				a, b := rects[ids[i]], rects[ids[j]]
				if a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H {
					t.Fatalf("%s: %s and %s overlap: %v %v", shape.name, ids[i], ids[j], a, b)
				}
			}
		}
	}
}

func TestPadHitFindsThePair(t *testing.T) {
	a, b, c := "a", "b", "c"
	flat := &Node{Dir: "row", Kids: []*Node{{Pane: a}, {Pane: b}}, Sizes: []float64{0.5, 0.5}}
	area := Rect{0, 0, 80, 24}
	boxes := childBoxRects(flat, area)
	pad := padRect(flat, boxes, 0)
	split, first, second, axis, sr, ok := padHit(flat, area, pad.X, pad.Y+3)
	if !ok || split != flat || first != a || second != b || axis != "row" || sr != area {
		t.Fatalf("pad hit = %v %q %q %q %v %v", split, first, second, axis, sr, ok)
	}
	if _, _, _, _, _, ok := padHit(flat, area, boxes[0].X+1, boxes[0].Y+1); ok {
		t.Fatal("a pane interior hit the pad")
	}

	// Nested: the outer pad pairs a with the right column.
	inner := &Node{Dir: "row", Kids: []*Node{{Pane: b}, {Pane: c}}, Sizes: []float64{0.5, 0.5}}
	nested := &Node{Dir: "row", Kids: []*Node{{Pane: a}, inner}, Sizes: []float64{0.6, 0.4}}
	nb := childBoxRects(nested, area)
	np := padRect(nested, nb, 0)
	_, first, second, axis, _, ok = padHit(nested, area, np.X, np.Y+3)
	if !ok || axis != "row" || first != a || (second != b && second != c) {
		t.Fatalf("nested pad hit = %q %q %q %v", first, second, axis, ok)
	}
	ib := childBoxRects(inner, nb[1])
	ipad := padRect(inner, ib, 0)
	_, first, second, _, _, ok = padHit(nested, area, ipad.X, ipad.Y+3)
	if !ok || first != b || second != c {
		t.Fatalf("inner pad hit = %q %q %v", first, second, ok)
	}
}

func TestNodeRectMatchesLayout(t *testing.T) {
	for _, shape := range boxShapes() {
		area := Rect{0, 0, 80, 24}
		rects := map[string]Rect{}
		layout(shape.root, area, rects)
		walkNodes(shape.root, func(n *Node) {
			got, ok := nodeRect(shape.root, n, area)
			if !ok {
				t.Fatalf("%s: nodeRect missed a node", shape.name)
			}
			if n.Pane != "" {
				if got != rects[n.Pane] {
					t.Fatalf("%s: nodeRect(%s) = %v, layout = %v", shape.name, n.Pane, got, rects[n.Pane])
				}
				return
			}
			if got.W <= 0 || got.H <= 0 {
				t.Fatalf("%s: split area %v", shape.name, got)
			}
		})
	}
}

func TestDrawPaneColors(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(24, 2))
	if _, err := vt.Write([]byte(
		"\x1b[38;2;30;144;255mTRUE" +
			"\x1b[0m\x1b[48;2;10;20;30mBG" +
			"\x1b[0m\x1b[38;5;196mPAL" +
			"\x1b[0m\x1b[7mREV\x1b[0m")); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	drawPane(&buf, vt, Rect{0, 0, 24, 2})
	out := buf.String()
	for _, want := range []string{"38;2;30;144;255", "48;2;10;20;30", "38;5;196"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in rendered output", want)
		}
	}
	// Reverse over the default colours must reach the terminal as SGR 7.
	rev := out[strings.Index(out, "REV")-40 : strings.Index(out, "REV")]
	if !strings.Contains(rev, "[0;7;") && !strings.Contains(rev, ";7;") && !strings.Contains(rev, ";7m") {
		t.Fatalf("reverse not emitted: %q", rev)
	}
}

func TestDrawSessionsWidth(t *testing.T) {
	var buf bytes.Buffer
	sessions := []SessionInfo{{Name: "alpha", Live: true}, {Name: "beta"}}
	drawSessions(&buf, 100, 30, sessions, 1, "alpha")

	pos := regexp.MustCompile(`\x1b\[[0-9]+;[0-9]+H`)
	sgr := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	raw := buf.String()
	locs := pos.FindAllStringIndex(raw, -1)
	widths := map[int]bool{}
	for i, loc := range locs {
		end := len(raw)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		row := sgr.ReplaceAllString(raw[loc[1]:end], "")
		widths[len([]rune(row))] = true
	}
	if len(widths) != 1 {
		t.Fatalf("box rows have different widths: %v", widths)
	}
}

// A pane daemon outlives the server, so the read reply must keep its shape.
func TestReadReplyStaysBackwardCompatible(t *testing.T) {
	old, err := readMsg(bytes.NewReader(frame(t, `{"type":"resp","lines":["a","b"]}`)))
	if err != nil || len(old.Lines) != 2 || old.Lines[1] != "b" {
		t.Fatalf("old read reply: %+v %v", old, err)
	}
	req, err := readMsg(bytes.NewReader(frame(t, `{"type":"req","action":"history","count":7}`)))
	if err != nil || req.Count != 7 || req.Lines != nil {
		t.Fatalf("history request: %+v %v", req, err)
	}
}

func frame(t *testing.T, payload string) []byte {
	t.Helper()
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, uint32(len(payload)))
	b.WriteString(payload)
	return b.Bytes()
}

func TestParseStatusWireFormat(t *testing.T) {
	msg := base64.StdEncoding.EncodeToString([]byte("needs review"))
	st := parseStatus("state=blocked:app=pi:kind=confirm:msg=" + msg)
	if st.State != "blocked" || st.Message != "needs review" {
		t.Fatalf("pairs parsed as %+v", st)
	}
	if st := parseStatus("state=working"); st.State != "working" || st.Message != "" {
		t.Fatalf("plain pair: %+v", st)
	}
	if st := parseStatus("state=clear"); st.State != "unknown" || st.Message != "" {
		t.Fatalf("clear: %+v", st)
	}
	if st := parseStatus("blocked"); st.State != "blocked" {
		t.Fatalf("bare state: %+v", st)
	}
}

func TestTerminalQueriesAnswered(t *testing.T) {
	var replies []string
	f := &oscFilter{
		reply:  func(r string) { replies = append(replies, r) },
		cursor: func() (int, int) { return 4, 9 },
		fg:     termFG,
		bg:     termBG,
	}
	var out bytes.Buffer
	// A colour query, a CPR, a DA1, and a normal SGR that must pass through.
	f.feed([]byte("a\x1b]11;?\x1b\\b\x1b[6nc\x1b[cd\x1b[1me"), &out)
	if out.String() != "abcd\x1b[1me" {
		t.Fatalf("queries were not stripped: %q", out.String())
	}
	want := []string{
		"\x1b]11;rgb:0000/0000/0000\x1b\\",
		"\x1b[5;10R",
		"\x1b[?1;2c",
	}
	if len(replies) != len(want) {
		t.Fatalf("replies = %q", replies)
	}
	for i := range want {
		if replies[i] != want[i] {
			t.Fatalf("reply %d = %q, want %q", i, replies[i], want[i])
		}
	}

	// A query split across reads is still answered.
	replies = nil
	f.feed([]byte("\x1b]10"), &out)
	f.feed([]byte(";?\x07"), &out)
	if len(replies) != 1 || !strings.HasPrefix(replies[0], "\x1b]10;") {
		t.Fatalf("split OSC reply = %q", replies)
	}
}

func TestOSC7501QueryReply(t *testing.T) {
	queries := 0
	f := &oscFilter{
		status: func(Status) { t.Fatal("query must not become a status") },
		query:  func() { queries++ },
	}
	var out bytes.Buffer
	f.feed([]byte("a\x1b]7501;?\x1b\\b"), &out)
	if queries != 1 || out.String() != "ab" {
		t.Fatalf("queries=%d out=%q", queries, out.String())
	}
}

// The pane answers an application's feature query and publishes its status.
func TestOSC7501EndToEnd(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "probe")
	root := h.focus("probe")
	p2 := h.split("probe", root, "down")
	h.run("write-to", p2, "-s", "probe", "--msg", h.bin+" __probe --hold 2s", "--enter")

	h.waitFor("probe", p2, "PROBE-REPLY=ok")
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, err := h.try("status", p2, "-s", "probe")
		if err == nil && strings.Contains(out, "working") && strings.Contains(out, "probe reporting") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status never showed working: %q", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.waitFor("probe", p2, "PROBE-CLEARED")
	time.Sleep(200 * time.Millisecond)
	out := h.run("status", p2, "-s", "probe")
	if strings.Contains(out, "working") {
		t.Fatalf("status not cleared: %q", out)
	}
}

func TestParseMouse(t *testing.T) {
	ev, n, ok := parseMouse([]byte("\x1b[<64;12;7Mrest"))
	if !ok || n != 11 || ev.btn != 64 || ev.x != 12 || ev.y != 7 || ev.release {
		t.Fatalf("wheel up: %+v n=%d ok=%v", ev, n, ok)
	}
	ev, _, ok = parseMouse([]byte("\x1b[<65;1;1m"))
	if !ok || ev.btn != 65 || !ev.release {
		t.Fatalf("wheel down release: %+v", ev)
	}
	if _, n, ok := parseMouse([]byte("\x1b[<64;12")); ok || n != 0 {
		t.Fatalf("partial sequence: n=%d ok=%v", n, ok)
	}
	if _, n, ok := parseMouse([]byte("\x1b[A")); ok || n != -1 {
		t.Fatalf("non-mouse: n=%d ok=%v", n, ok)
	}
}

func TestScrollStart(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(40, 100))
	var b strings.Builder
	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&b, "line %d\r\n", i)
	}
	if _, err := vt.Write([]byte(b.String())); err != nil {
		t.Fatal(err)
	}
	live, max := scrollStart(vt, 10, 0)
	if live < 50 || live+10 > 100 {
		t.Fatalf("live start = %d", live)
	}
	if max < 45 {
		t.Fatalf("max offset = %d", max)
	}
	scrolled, _ := scrollStart(vt, 10, 5)
	if scrolled != live-5 {
		t.Fatalf("scrolled start = %d, want %d", scrolled, live-5)
	}
	top, _ := scrollStart(vt, 10, max+100)
	if top != 0 {
		t.Fatalf("clamped start = %d, want 0", top)
	}
}

// A pane that enters the alternate screen and asks for mouse reporting must
// tell the server, because the client's replay ring may not reach back to the
// enabling sequences. The client uses that to forward the wheel to the app.
func TestAltScreenAndMouseMode(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "m")
	root := h.focus("m")
	h.run("write-to", root, "-s", "m", "--msg",
		`printf '\033[?1049h\033[?1000h'; sleep 2; printf '\033[?1000l\033[?1049l'`, "--enter")

	waitMode(t, h, "m", true, true)
	waitMode(t, h, "m", false, false)
}

func waitMode(t *testing.T, h *harness, session string, alt, mouse bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		tree := h.rpc(session, map[string]any{"action": "list"})
		snap := tree["snapshot"].(map[string]any)
		for _, raw := range snap["panes"].([]any) {
			p := raw.(map[string]any)
			gotAlt, _ := p["alt"].(bool)
			gotMouse, _ := p["mouse"].(bool)
			if gotAlt == alt && gotMouse == mouse {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane never reached alt=%v mouse=%v: %v", alt, mouse, snap["panes"])
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestIDsAreAddressable(t *testing.T) {
	st := newState("x")
	ws := st.ws()
	w := st.win()
	if ws.ID != "s1" || w.ID != "w1" {
		t.Fatalf("initial ids %q %q", ws.ID, w.ID)
	}
	if p := w.newPaneID(); p != "w1p1" {
		t.Fatalf("first pane %q", p)
	}
	if p := w.newPaneID(); p != "w1p2" {
		t.Fatalf("second pane %q", p)
	}
	if id := st.newWindowID(); id != "w2" {
		t.Fatalf("next window %q", id)
	}
	if id := st.newWorkspaceID(); id != "s2" {
		t.Fatalf("next workspace %q", id)
	}

	// A state from the random scheme migrates to the same shape.
	old := &State{
		Name: "x", Panes: map[string]*PaneState{"abc12": {}},
		Workspaces: []*Workspace{{ID: "sz6z50", Name: "main", Windows: []*Window{{
			ID: "w2e8y4", Focus: "abc12",
			Root: &Node{Dir: "row", Kids: []*Node{{Pane: "abc12"}, {Pane: "def34"}}},
		}}}},
	}
	old.migrateIDs()
	if old.Workspaces[0].ID != "s1" || old.Workspaces[0].Windows[0].ID != "w1" {
		t.Fatalf("migrated ids %q %q", old.Workspaces[0].ID, old.Workspaces[0].Windows[0].ID)
	}
	if got := allPanes(old.Workspaces[0].Windows[0].Root); got[0] != "w1p1" || got[1] != "w1p2" {
		t.Fatalf("migrated panes %v", got)
	}
	if old.Workspaces[0].Windows[0].Focus != "w1p1" {
		t.Fatalf("migrated focus %q", old.Workspaces[0].Windows[0].Focus)
	}
	if _, ok := old.Panes["w1p1"]; !ok {
		t.Fatalf("pane state not remapped: %v", old.Panes)
	}
}

func TestFocusForClick(t *testing.T) {
	p := &PaneInfo{ID: "w1p2", Mouse: true}
	if got := focusForClick(p, "w1p1", mouseEvent{btn: 0}); got != "w1p2" {
		t.Fatalf("left press on a mouse-claiming pane = %q", got)
	}
	if got := focusForClick(p, "w1p2", mouseEvent{btn: 0}); got != "" {
		t.Fatalf("left press on the focused pane = %q", got)
	}
	if got := focusForClick(p, "w1p1", mouseEvent{btn: 64}); got != "" {
		t.Fatalf("wheel focused a pane: %q", got)
	}
	if got := focusForClick(p, "w1p1", mouseEvent{btn: 0, release: true}); got != "" {
		t.Fatalf("release focused a pane: %q", got)
	}
	if got := focusForClick(p, "w1p1", mouseEvent{btn: 32}); got != "" {
		t.Fatalf("motion focused a pane: %q", got)
	}
}

func TestPaneAreaInset(t *testing.T) {
	area := paneArea(100, 30)
	if area != (Rect{X: 0, Y: 0, W: 100, H: 29}) {
		t.Fatalf("pane area = %+v", area)
	}
	if paneArea(3, 4) != (Rect{X: 0, Y: 0, W: 3, H: 3}) {
		t.Fatalf("tiny area = %+v", paneArea(3, 4))
	}
}

func TestDrawPaneBox(t *testing.T) {
	var b bytes.Buffer
	drawPaneBox(&b, Rect{X: 10, Y: 5, W: 20, H: 8}, true)
	out := b.String()
	for _, want := range []string{"┌", "┐", "└", "┘", "│", "─", "\x1b[1;96m"} {
		if !strings.Contains(out, want) {
			t.Fatalf("box missing %q: %q", want, out)
		}
	}
	if !strings.Contains(out, "\x1b[5;10H┌") || !strings.Contains(out, "\x1b[5;31H┐") {
		t.Fatalf("box corners are at the wrong cells: %q", out)
	}
	var dim bytes.Buffer
	drawPaneBox(&dim, Rect{X: 10, Y: 5, W: 20, H: 8}, false)
	if strings.Contains(dim.String(), "\x1b[1;96m") || !strings.Contains(dim.String(), "\x1b[90m") {
		t.Fatalf("inactive box is not dim: %q", dim.String())
	}
}

func TestMenuClampsAndHits(t *testing.T) {
	row := &Node{Dir: "row", Kids: []*Node{{Pane: "a"}, {Pane: "b"}},
		Sizes: []float64{0.5, 0.5}}
	tui := &tui{snap: testSnapshot(row, 100, 30, nil)}
	p := tui.paneByID("b")
	tui.openMenu(p, 98, 28)
	m := tui.menu
	if m == nil || tui.mode != "menu" {
		t.Fatalf("menu not opened: %+v mode=%q", m, tui.mode)
	}
	if m.x+m.w > 100 || m.y+len(m.items)+2 > 30 {
		t.Fatalf("menu escapes the screen: %+v", m)
	}
	if got := m.itemAt(m.x+2, m.y+1); got != 0 {
		t.Fatalf("first item row = %d", got)
	}
	if got := m.itemAt(m.x+2, m.y+len(m.items)+1); got != -1 {
		t.Fatalf("bottom border hit = %d", got)
	}
	if got := m.itemAt(0, 0); got != -1 {
		t.Fatalf("point outside the menu hit = %d", got)
	}
}

func TestRightPressOpensMenuOverMousePane(t *testing.T) {
	row := &Node{Dir: "row", Kids: []*Node{{Pane: "a"}, {Pane: "b"}},
		Sizes: []float64{0.5, 0.5}}
	snap := testSnapshot(row, 100, 30, map[string]bool{"b": true})
	tui := &tui{snap: snap, vts: map[string]vt10x.Terminal{}}
	// A right press on pane b, whose app owns the mouse, still opens the menu.
	tui.handleMouse(mouseEvent{btn: 2, x: 80, y: 5})
	if tui.mode != "menu" || tui.menu == nil || tui.menu.pane != "b" {
		t.Fatalf("right press: mode=%q menu=%+v", tui.mode, tui.menu)
	}
	// The release of the opening right press sits on the border and must not
	// dismiss the menu.
	tui.handleMouse(mouseEvent{btn: 2, release: true, x: tui.menu.x + 1, y: tui.menu.y + 1})
	if tui.mode != "menu" {
		t.Fatal("right release on the border closed the menu")
	}
	// A click on the title or border is a near miss, not a dismissal.
	tui.handleMouse(mouseEvent{btn: 0, x: tui.menu.x + 2, y: tui.menu.y + 1})
	if tui.mode != "menu" {
		t.Fatal("click on the title closed the menu")
	}
	// A left press on a menu row runs that item and closes the menu.
	done := false
	tui.menu.items[0].run = func() { done = true }
	tui.handleMouse(mouseEvent{btn: 0, x: tui.menu.x + 2, y: tui.menu.y + 1 + 1})
	if !done || tui.mode != "" || tui.menu != nil {
		t.Fatalf("left pick: done=%v mode=%q", done, tui.mode)
	}
}

func TestPaneAtBoxIncludesBorder(t *testing.T) {
	row := &Node{Dir: "row", Kids: []*Node{{Pane: "a"}, {Pane: "b"}},
		Sizes: []float64{0.5, 0.5}}
	tui := &tui{snap: testSnapshot(row, 100, 30, nil)}
	b := tui.paneByID("b").Rect
	if got := tui.paneAtBox(b.X, b.Y); got == nil || got.ID != "b" {
		t.Fatalf("top-left border did not hit b: %+v", got)
	}
	if got := tui.paneAtBox(b.X-1, b.Y-1); got == nil || got.ID != "b" {
		t.Fatalf("outer corner did not hit b: %+v", got)
	}
	if got := tui.paneAtBox(b.X+b.W, b.Y+b.H); got == nil || got.ID != "b" {
		t.Fatalf("bottom-right border did not hit b: %+v", got)
	}
	// The pad between a and b belongs to neither box.
	padX := b.X - 2
	if got := tui.paneAtBox(padX, b.Y+1); got != nil {
		t.Fatalf("pad hit pane %q", got.ID)
	}
}

func TestMenuOutsideClickFocusesPane(t *testing.T) {
	row := &Node{Dir: "row", Kids: []*Node{{Pane: "a"}, {Pane: "b"}},
		Sizes: []float64{0.5, 0.5}}
	tui := &tui{snap: testSnapshot(row, 100, 30, nil), vts: map[string]vt10x.Terminal{}}
	tui.openMenu(tui.paneByID("b"), 10, 5)
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	tui.conn = &conn{Conn: a}
	got := make(chan Msg, 1)
	go func() {
		if m, err := readMsg(b); err == nil {
			got <- m
		}
	}()
	// One left click on pane a, outside the menu, dismisses the menu and
	// focuses a in the same event.
	tui.handleMouse(mouseEvent{btn: 0, x: 5, y: 5})
	select {
	case m := <-got:
		if m.Action != "focus" || m.Target != "a" {
			t.Fatalf("outside click sent %+v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("outside click sent no focus")
	}
	if tui.menu != nil || tui.mode != "" {
		t.Fatalf("menu stayed open: %+v %q", tui.menu, tui.mode)
	}
}

func TestMenuHoverHighlights(t *testing.T) {
	row := &Node{Pane: "a"}
	tui := &tui{snap: testSnapshot(row, 100, 30, nil)}
	tui.openMenu(tui.paneByID("a"), 10, 5)
	// Bare motion (no button held) is SGR button 35.
	tui.handleMouse(mouseEvent{btn: 35, x: 14, y: tui.menu.y + 2 + 1})
	if tui.menu == nil || tui.menu.sel != 1 {
		t.Fatalf("hover did not highlight row 1: %+v", tui.menu)
	}
	if tui.mode != "menu" {
		t.Fatalf("hover closed the menu: %q", tui.mode)
	}
	// A bare click does not activate a row it merely hovers.
	ran := false
	tui.menu.items[1].run = func() { ran = true }
	tui.handleMouse(mouseEvent{btn: 35, x: 14, y: tui.menu.y + 2 + 1})
	if ran {
		t.Fatal("bare motion ran an item")
	}
}

func TestMenuRightDragPicks(t *testing.T) {
	row := &Node{Pane: "a"}
	tui := &tui{snap: testSnapshot(row, 100, 30, nil)}
	tui.openMenu(tui.paneByID("a"), 10, 5)
	done := -1
	tui.menu.items[1].run = func() { done = 1 }
	// Move with the right button held: the row under the pointer lights up.
	tui.handleMouse(mouseEvent{btn: 34, x: 14, y: tui.menu.y + 2 + 1})
	if tui.menu.sel != 1 {
		t.Fatalf("right drag did not highlight row 1: %+v", tui.menu)
	}
	// Release over the row runs it.
	tui.handleMouse(mouseEvent{btn: 2, release: true, x: 14, y: tui.menu.y + 2 + 1})
	if done != 1 || tui.menu != nil {
		t.Fatalf("right drag did not pick: done=%d menu=%+v", done, tui.menu)
	}

	// A click outside dismisses.
	tui.openMenu(tui.paneByID("a"), 10, 5)
	tui.handleMouse(mouseEvent{btn: 0, x: 1, y: 1})
	if tui.menu != nil || tui.mode != "" {
		t.Fatalf("outside click did not close: %+v", tui.menu)
	}
}

func TestMenuKeyboard(t *testing.T) {
	row := &Node{Pane: "a"}
	tui := &tui{snap: testSnapshot(row, 100, 30, nil)}
	tui.openMenu(tui.paneByID("a"), 10, 10)
	if len(tui.menu.items) < 3 {
		t.Fatalf("menu items = %d", len(tui.menu.items))
	}
	tui.inbuf = []byte("j")
	if !tui.menuKey() || tui.menu.sel != 1 {
		t.Fatalf("j did not move: %+v", tui.menu)
	}
	tui.inbuf = []byte("k")
	tui.menuKey() // back to 0
	tui.inbuf = []byte("k")
	tui.menuKey() // wraps to the last item
	if tui.menu == nil || tui.menu.sel != len(tui.menu.items)-1 {
		t.Fatalf("wrap up failed: %+v", tui.menu)
	}
	tui.inbuf = []byte("q")
	tui.menuKey()
	if tui.menu != nil || tui.mode != "" {
		t.Fatalf("q did not close: %+v %q", tui.menu, tui.mode)
	}
}

func TestDrawMenu(t *testing.T) {
	m := &menu{x: 2, y: 3, w: 30, title: "w1p2 pi", items: []menuItem{
		{label: "Split right", key: "|"},
		{label: "Close pane", key: "x"},
	}, sel: 1}
	var b bytes.Buffer
	drawMenu(&b, m)
	out := b.String()
	for _, want := range []string{"Split right", "Close pane", "w1p2 pi", "\x1b[30;46m"} {
		if !strings.Contains(out, want) {
			t.Fatalf("menu missing %q: %q", want, out)
		}
	}
}

func TestBarHighlightsFocus(t *testing.T) {
	snap := &Snapshot{
		Session: "main", Workspace: "main", Windows: []string{"1"}, Focus: "w1p2",
		Cols: 80, Rows: 24,
		Panes: []PaneInfo{{ID: "w1p1", Status: "working"}, {ID: "w1p2", Name: "w2-7f", Status: "idle"}},
	}
	var b bytes.Buffer
	drawBar(&b, snap, false, "", "")
	out := b.String()
	if !strings.Contains(out, "\x1b[30;46m") || !strings.Contains(out, "▶ w1p2 w2-7f idle") {
		t.Fatalf("bar has no focus highlight: %q", out)
	}
}

func TestSplitRatio(t *testing.T) {
	root := &Node{Pane: "a"}
	splitLeaf(root, "a", "right", "b", 0.25)
	rects := map[string]Rect{}
	layout(root, Rect{0, 0, 100, 10}, rects)
	if got := rects["a"].W; got < 21 || got > 23 {
		t.Fatalf("existing pane width = %d, want about 22", got)
	}
	if got := rects["b"].W; got < 72 || got > 74 {
		t.Fatalf("new pane width = %d, want about 73", got)
	}
	if rects["a"].W+rects["b"].W+5 != 100 {
		t.Fatalf("sizes do not tile: %+v", rects)
	}
}

func TestSplitAndPrune(t *testing.T) {
	root := &Node{Pane: "a"}
	if !splitLeaf(root, "a", "right", "b", 0) {
		t.Fatal("split failed")
	}
	if root.Dir != "row" || len(root.Kids) != 2 || root.Kids[1].Pane != "b" {
		t.Fatalf("root %+v", root)
	}
	splitLeaf(root.Kids[1], "b", "down", "c", 0)
	n, ok := pruneLeaf(root, "b")
	if !ok || len(n.Kids) != 2 || n.Kids[1].Pane != "c" {
		t.Fatalf("prune left %+v", n)
	}
}

// buildOnce builds the dm binary for the integration tests.
func buildOnce(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "dm")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

type harness struct {
	t   *testing.T
	bin string
	env []string
}

func newHarness(t *testing.T) *harness {
	state, err := os.MkdirTemp("", "dmt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Stop panes and servers this test spawned.
		filepath.Walk(state, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".pid") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			var pid int
			fmt.Sscanf(string(b), "%d", &pid)
			if pid > 0 {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
			return nil
		})
		os.RemoveAll(state)
	})
	return &harness{t: t, bin: buildOnce(t), env: append(os.Environ(), "DM_STATE="+state)}
}

func (h *harness) run(args ...string) string {
	h.t.Helper()
	cmd := exec.Command(h.bin, args...)
	cmd.Env = h.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("dm %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func (h *harness) try(args ...string) (string, error) {
	cmd := exec.Command(h.bin, args...)
	cmd.Env = h.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (h *harness) rpc(session string, v map[string]any) map[string]any {
	h.t.Helper()
	raw, _ := json.Marshal(v)
	out := h.run("exec", string(raw), "-s", session)
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		h.t.Fatalf("bad exec output %q: %v", out, err)
	}
	return res
}

func (h *harness) stateDir() string {
	for _, e := range h.env {
		if strings.HasPrefix(e, "DM_STATE=") {
			return strings.TrimPrefix(e, "DM_STATE=")
		}
	}
	return ""
}

func (h *harness) panes(session string) []string {
	h.t.Helper()
	tree := h.rpc(session, map[string]any{"action": "list"})
	snap := tree["snapshot"].(map[string]any)
	var ids []string
	for _, raw := range snap["panes"].([]any) {
		ids = append(ids, raw.(map[string]any)["id"].(string))
	}
	return ids
}

func (h *harness) focus(session string) string {
	h.t.Helper()
	tree := h.rpc(session, map[string]any{"action": "list"})
	return tree["snapshot"].(map[string]any)["focus"].(string)
}

func (h *harness) split(session, target, dir string) string {
	h.t.Helper()
	res := h.rpc(session, map[string]any{"action": "split", "target": target, "dir": dir})
	id, _ := res["pane"].(string)
	if id == "" {
		h.t.Fatalf("split returned no pane: %v", res)
	}
	return id
}

func (h *harness) waitFor(session, pane, needle string) string {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		out, err := h.try("read", pane, "-s", session)
		last = out
		if err == nil && strings.Contains(out, needle) {
			return out
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatalf("pane %s never showed %q; last read:\n%s", pane, needle, last)
	return last
}

func TestDurabilityAcrossServerKill(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "durable")
	root := h.focus("durable")
	h.run("write-to", root, "-s", "durable", "--msg", "echo MARK-DURABLE", "--enter")
	h.waitFor("durable", root, "MARK-DURABLE")

	panePID := readPID(t, filepath.Join(h.stateDir(), "sessions", "durable", "panes", root+".sock.pid"))
	serverPID := readPID(t, filepath.Join(h.stateDir(), "sessions", "durable", "server.pid"))
	if err := syscall.Kill(serverPID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)

	// A fresh RPC must restart the server and adopt the same pane process.
	h.waitFor("durable", root, "MARK-DURABLE")
	if got := readPID(t, filepath.Join(h.stateDir(), "sessions", "durable", "panes", root+".sock.pid")); got != panePID {
		t.Fatalf("pane process changed: %d -> %d", panePID, got)
	}
	if got := readPID(t, filepath.Join(h.stateDir(), "sessions", "durable", "server.pid")); got == serverPID {
		t.Fatal("server was not restarted")
	}

	// Layout survives too.
	h.split("durable", root, "right")
	serverPID = readPID(t, filepath.Join(h.stateDir(), "sessions", "durable", "server.pid"))
	_ = syscall.Kill(serverPID, syscall.SIGKILL)
	time.Sleep(300 * time.Millisecond)
	tree := h.rpc("durable", map[string]any{"action": "list"})
	snap := tree["snapshot"].(map[string]any)
	if n := len(snap["panes"].([]any)); n != 2 {
		t.Fatalf("panes after restart = %d", n)
	}
}

func TestSplitStatusAndRPC(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "s1")
	root := h.focus("s1")
	p2 := h.split("s1", root, "right")
	if p2 == root {
		t.Fatalf("split returned the target pane %q", p2)
	}

	h.run("write-to", p2, "-s", "s1", "--msg", "echo FROM-P2", "--enter")
	h.waitFor("s1", p2, "FROM-P2")

	lines := h.run("read", p2, "-s", "s1")
	if !strings.Contains(lines, "FROM-P2") {
		t.Fatalf("read p2: %q", lines)
	}

	// The same action as JSON, exactly as an agent would send it.
	h.run("exec", `{"action":"write-to","target":"`+root+`","msg":"echo FROM-JSON","enter":true}`, "-s", "s1")
	h.waitFor("s1", root, "FROM-JSON")

	h.run("write-to", root, "-s", "s1", "--msg",
		`printf '\033]7501;{"state":"blocked","message":"needs review"}\007'`, "--enter")
	deadline := time.Now().Add(5 * time.Second)
	for {
		out := h.run("status", root, "-s", "s1")
		if strings.Contains(out, "blocked") && strings.Contains(out, "needs review") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status never updated: %q", out)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Focus movement by direction.
	h.run("focus", root, "-s", "s1")
	h.run("exec", `{"action":"focus-dir","dir":"right"}`, "-s", "s1")
	tree := h.rpc("s1", map[string]any{"action": "list"})
	if snap := tree["snapshot"].(map[string]any); snap["focus"] != p2 {
		t.Fatalf("focus = %v", snap["focus"])
	}

	// Closing a pane prunes the split and keeps the session usable.
	h.run("exec", `{"action":"close-pane","target":"`+p2+`"}`, "-s", "s1")
	tree = h.rpc("s1", map[string]any{"action": "list"})
	if snap := tree["snapshot"].(map[string]any); len(snap["panes"].([]any)) != 1 {
		t.Fatalf("panes = %v", snap["panes"])
	}
}

// A shell that exits must take its split with it and leave focus on a live
// pane. Leaving a dead rect behind makes the whole client unusable.
func TestPaneExitPrunesSplit(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "x")
	root := h.focus("x")
	p2 := h.split("x", root, "right")
	h.waitPanes("x", 2)

	h.run("write-to", p2, "-s", "x", "--msg", "exit", "--enter")
	h.waitPanes("x", 1)

	h.run("write-to", root, "-s", "x", "--msg", "echo STILL-ALIVE", "--enter")
	h.waitFor("x", root, "STILL-ALIVE")

	tree := h.rpc("x", map[string]any{"action": "list"})
	snap := tree["snapshot"].(map[string]any)
	if snap["focus"] != root {
		t.Fatalf("focus = %v", snap["focus"])
	}

	// The last pane takes the session with it, like tmux.
	panePID := readPID(t, filepath.Join(h.stateDir(), "sessions", "x", "panes", root+".sock.pid"))
	h.run("write-to", root, "-s", "x", "--msg", "exit", "--enter")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(h.stateDir(), "sessions", "x")); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session x did not end")
		}
		time.Sleep(100 * time.Millisecond)
	}
	deadline = time.Now().Add(5 * time.Second)
	for syscall.Kill(panePID, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("pane process %d still alive", panePID)
		}
		time.Sleep(100 * time.Millisecond)
	}
	out := h.run("ls", "--json")
	if strings.Contains(out, `"x"`) {
		t.Fatalf("session x still listed: %s", out)
	}
}

func (h *harness) waitPanes(session string, n int) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		tree := h.rpc(session, map[string]any{"action": "list"})
		snap := tree["snapshot"].(map[string]any)
		if len(snap["panes"].([]any)) == n {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatalf("session %s never reached %d panes", session, n)
}

func TestWindowsAndWorkspaces(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "w1")
	h.run("exec", `{"action":"new-window"}`, "-s", "w1")
	h.run("exec", `{"action":"new-workspace","name":"other"}`, "-s", "w1")
	tree := h.rpc("w1", map[string]any{"action": "list"})
	snap := tree["snapshot"].(map[string]any)
	if snap["workspace"] != "other" {
		t.Fatalf("workspace = %v", snap["workspace"])
	}
	h.run("exec", `{"action":"prev-workspace"}`, "-s", "w1")
	tree = h.rpc("w1", map[string]any{"action": "list"})
	snap = tree["snapshot"].(map[string]any)
	if snap["workspace"] != "main" || len(snap["windows"].([]any)) != 2 {
		t.Fatalf("snapshot %v", snap)
	}
}

// History returns the rows above the live screen, oldest first, using the
// same reachable window as the client's scrollback.
func TestPaneHistory(t *testing.T) {
	h := newHarness(t)

	h.run("new", "-s", "hist")
	root := h.focus("hist")
	h.run("write-to", root, "-s", "hist", "--msg",
		`for i in $(seq 1 80); do echo HIST-$(printf %03d $i); done`, "--enter")
	h.waitFor("hist", root, "HIST-080")
	time.Sleep(200 * time.Millisecond)

	screen := h.run("read", root, "-s", "hist")
	if strings.Contains(screen, "HIST-001") {
		t.Fatalf("HIST-001 still on screen:\n%s", screen)
	}

	// Enough history for the requested window: exact count, oldest first.
	hist := h.history("hist", root, 20)
	if len(hist) != 20 {
		t.Fatalf("history returned %d rows, want 20:\n%v", len(hist), hist)
	}
	last, numbered := 0, 0
	for _, line := range hist {
		var num int
		if _, err := fmt.Sscanf(line, "HIST-%d", &num); err == nil {
			numbered++
			if num <= last {
				t.Fatalf("history not oldest first at %q:\n%v", line, hist)
			}
			last = num
		}
	}
	if numbered != 20 {
		t.Fatalf("history returned %d numbered rows, want 20:\n%v", numbered, hist)
	}
	if last >= 80 {
		t.Fatalf("history window does not end above the live screen: last=%d\n%v", last, hist)
	}

	// A pane in a background workspace keeps its own PTY size. The tall replay
	// must use that, not the inactive window's layout rect.
	bgRes := h.rpc("hist", map[string]any{"action": "new-workspace", "name": "bg",
		"window": "t", "cwd": "/tmp", "nofocus": true})
	bg, _ := bgRes["pane"].(string)
	if bg == "" {
		t.Fatalf("background pane: %v", bgRes)
	}
	h.run("write-to", bg, "-s", "hist", "--msg",
		`for i in $(seq 1 60); do echo BG-$(printf %03d $i); done`, "--enter")
	h.waitFor("hist", bg, "BG-060")
	time.Sleep(200 * time.Millisecond)
	bgHist := h.history("hist", bg, 5)
	if len(bgHist) != 5 {
		t.Fatalf("background history rows = %d, want 5: %v", len(bgHist), bgHist)
	}
	lastBg := 0
	fmt.Sscanf(bgHist[len(bgHist)-1], "BG-%d", &lastBg)
	if lastBg < 30 || lastBg > 45 {
		t.Fatalf("background history window is not next to the screen: last=%d\n%v", lastBg, bgHist)
	}

	// A wide window reaches the first row; -s before the pane id works.
	wide := h.run("read", "-s", "hist", root, "--scrollback", "100")
	if !strings.Contains(wide, "HIST-001") {
		t.Fatalf("history missing HIST-001:\n%s", wide)
	}

	if out, err := h.try("read", root, "-s", "hist", "--scrollback", "0"); err == nil {
		t.Fatalf("--scrollback 0 accepted: %q", out)
	}
	if out, err := h.try("exec", `{"action":"history","target":"`+root+`","count":-1}`, "-s", "hist"); err == nil {
		t.Fatalf("negative lines accepted: %q", out)
	}
	if out, err := h.try("read", "nope", "-s", "hist", "--scrollback", "5"); err == nil {
		t.Fatalf("unknown pane accepted: %q", out)
	}

	// Leading blank rows are not reachable, like the client's scrollStart.
	h.run("new", "-s", "histb")
	root = h.focus("histb")
	h.run("write-to", root, "-s", "histb", "--msg",
		`printf '\033[H\033[2J\033[10;1H'; for i in $(seq 1 80); do echo BLANK-$(printf %03d $i); done`, "--enter")
	h.waitFor("histb", root, "BLANK-080")
	time.Sleep(200 * time.Millisecond)
	blank := h.history("histb", root, 500)
	if len(blank) == 0 || strings.TrimSpace(blank[0]) == "" {
		t.Fatalf("history includes leading blank rows:\n%q", blank)
	}

	// A blank row immediately above the live screen is part of the window.
	h.run("new", "-s", "histg")
	root = h.focus("histg")
	rows := h.rects("histg")[root].H
	// sh -c keeps the shell prompt out of the picture until the sleep ends.
	cmd := fmt.Sprintf(`sh -c "printf '\033[H\033[2J'; echo TOP; echo; `+
		`for i in \$(seq 1 %d); do echo TAIL-\$(printf %%03d \$i); done; `+
		`printf '\033[%d;1H'; sleep 15"`, rows, rows+2)
	h.run("write-to", root, "-s", "histg", "--msg", cmd, "--enter")
	h.waitFor("histg", root, fmt.Sprintf("TAIL-%03d", rows))
	time.Sleep(200 * time.Millisecond)
	if got := h.history("histg", root, 1); len(got) != 1 || got[0] != "" {
		t.Fatalf("blank row above the live screen not returned: %q", got)
	}

	// A pane with no scrollback answers empty, not an error.
	h.run("new", "-s", "short")
	root = h.focus("short")
	if got := h.history("short", root, 5); len(got) != 0 {
		t.Fatalf("short pane returned %v", got)
	}

	// The client refuses scrollback on the alternate screen; so does the server.
	h.run("new", "-s", "alt")
	root = h.focus("alt")
	h.run("write-to", root, "-s", "alt", "--msg", `printf '\033[?1049h'; sleep 3`, "--enter")
	waitMode(t, h, "alt", true, false)
	if out, err := h.try("exec", `{"action":"history","target":"`+root+`","count":5}`, "-s", "alt"); err == nil {
		t.Fatalf("alt pane returned history: %q", out)
	}
}

func (h *harness) history(session, pane string, n int) []string {
	h.t.Helper()
	res := h.rpc(session, map[string]any{"action": "history", "target": pane, "count": n})
	raw, _ := res["lines"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		line, _ := v.(string)
		out = append(out, line)
	}
	return out
}

func testSnapshot(root *Node, cols, rows int, mouse map[string]bool) *Snapshot {
	snap := &Snapshot{Root: root, Cols: cols, Rows: rows}
	rects := map[string]Rect{}
	layout(root, paneArea(cols, rows), rects)
	for _, id := range allPanes(root) {
		snap.Panes = append(snap.Panes, PaneInfo{ID: id, Rect: rects[id], Mouse: mouse[id]})
	}
	return snap
}

// DragPress is the press decision: dividers start a drag, panes and splits
// whose subtree claimed the mouse do not. handleMouse must call it.
func TestDragPress(t *testing.T) {
	row := &Node{Dir: "row", Kids: []*Node{{Pane: "a"}, {Pane: "b"}},
		Sizes: []float64{0.5, 0.5}}
	snap := testSnapshot(row, 100, 30, nil)
	ds, ok := dragPress(snap, 49, 5)
	if !ok || ds.first != "a" || ds.second != "b" || ds.axis != "row" ||
		ds.rect != paneArea(100, 30) {
		t.Fatalf("divider press: %+v ok=%v", ds, ok)
	}
	if _, ok := dragPress(snap, 10, 5); ok {
		t.Fatal("pane press started a drag")
	}

	// The press path itself must use dragPress.
	tui := &tui{snap: snap}
	tui.handleMouse(mouseEvent{btn: 0, x: 50, y: 6})
	if !tui.dragging {
		t.Fatal("handleMouse did not start a drag on a divider")
	}
	// startDrag must take its origin and cell space from the pressed rect.
	if tui.dragStart != ds.rect.X || tui.dragSpace != ds.rect.W-1 {
		t.Fatalf("startDrag stored start=%d space=%d, want %d/%d",
			tui.dragStart, tui.dragSpace, ds.rect.X, ds.rect.W-1)
	}

	// Same-axis nesting: the outer hit carries the owning split's full rect,
	// not the bounding box of the two adjacent leaves.
	same := &Node{Dir: "row", Kids: []*Node{
		{Pane: "a"},
		{Dir: "row", Kids: []*Node{{Pane: "b"}, {Pane: "c"}}},
	}}
	snap = testSnapshot(same, 100, 30, nil)
	// The root is what layout hands its tree: the whole pane area.
	want := paneArea(snap.Cols, snap.Rows)
	ds, ok = dragPress(snap, 49, 5)
	if !ok || ds.first != "a" || ds.second != "b" || ds.rect != want {
		t.Fatalf("same-axis outer press: %+v ok=%v, want rect %v", ds, ok, want)
	}
	tui.snap = snap
	tui.startDrag(ds)
	if tui.dragStart != want.X || tui.dragSpace != want.W-1 {
		t.Fatalf("same-axis startDrag stored start=%d space=%d, want %d/%d",
			tui.dragStart, tui.dragSpace, want.X, want.W-1)
	}

	// Any mouse-claiming pane in the owning split's subtree owns the divider.
	nested := &Node{Dir: "row", Kids: []*Node{
		{Pane: "a"},
		{Dir: "row", Kids: []*Node{{Pane: "b"}, {Pane: "c"}}},
	}}
	snap = testSnapshot(nested, 100, 30, map[string]bool{"c": true})
	if _, ok := dragPress(snap, 49, 5); ok {
		t.Fatal("drag started although the split's subtree claimed the mouse")
	}
	if _, ok := dragPress(snap, 74, 5); ok {
		t.Fatal("inner drag started although its subtree claimed the mouse")
	}
}

func (h *harness) rects(session string) map[string]Rect {
	h.t.Helper()
	tree := h.rpc(session, map[string]any{"action": "list"})
	snap := tree["snapshot"].(map[string]any)
	out := map[string]Rect{}
	for _, raw := range snap["panes"].([]any) {
		p := raw.(map[string]any)
		rr := p["rect"].(map[string]any)
		num := func(k string) int { return int(rr[k].(float64)) }
		out[p["id"].(string)] = Rect{X: num("X"), Y: num("Y"), W: num("W"), H: num("H")}
	}
	return out
}

// A nested drag resizes the outer split, not the inner one, and clamps.
func TestResizeSplit(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "rs")
	p1 := h.focus("rs")
	p2 := h.split("rs", p1, "right")
	p3 := h.split("rs", p1, "down")
	if focus := h.focus("rs"); focus != p3 {
		t.Fatalf("focus = %q, want %q", focus, p3)
	}

	before := h.rects("rs")
	h.run("exec", `{"action":"resize-split","target":"`+p1+`","other":"`+p2+`","ratio":0.25}`, "-s", "rs")
	after := h.rects("rs")
	if after[p2].W <= before[p2].W || after[p1].W >= before[p1].W {
		t.Fatalf("outer widths did not move: before=%v after=%v", before, after)
	}
	if after[p1].H != before[p1].H || after[p3].H != before[p3].H {
		t.Fatalf("inner heights moved with the outer drag: before=%v after=%v", before, after)
	}
	if focus := h.focus("rs"); focus != p3 {
		t.Fatalf("resize moved focus to %q", focus)
	}

	// The inner split still resizes along its own axis.
	before = after
	h.run("exec", `{"action":"resize-split","target":"`+p1+`","other":"`+p3+`","ratio":0.7}`, "-s", "rs")
	after = h.rects("rs")
	if after[p1].H == before[p1].H || after[p3].H == before[p3].H {
		t.Fatalf("inner heights did not move: before=%v after=%v", before, after)
	}
	if after[p1].W != before[p1].W || after[p2].W != before[p2].W {
		t.Fatalf("inner drag changed widths: before=%v after=%v", before, after)
	}

	// Ratios 0 and 1 clamp the squeezed side to exactly three cells. Leaving
	// both sides merely >= 3 would also pass an even split without a clamp.
	h.run("exec", `{"action":"resize-split","target":"`+p1+`","other":"`+p2+`","ratio":0}`, "-s", "rs")
	got := h.rects("rs")
	if got[p1].W != 3 || got[p2].W <= 3 {
		t.Fatalf("ratio 0 width: %v", got)
	}
	h.run("exec", `{"action":"resize-split","target":"`+p1+`","other":"`+p2+`","ratio":1}`, "-s", "rs")
	got = h.rects("rs")
	if got[p2].W != 3 || got[p1].W <= 3 {
		t.Fatalf("ratio 1 width: %v", got)
	}
	h.run("exec", `{"action":"resize-split","target":"`+p1+`","other":"`+p3+`","ratio":0}`, "-s", "rs")
	got = h.rects("rs")
	if got[p1].H != 3 || got[p3].H <= 3 {
		t.Fatalf("ratio 0 height: %v", got)
	}
	h.run("exec", `{"action":"resize-split","target":"`+p1+`","other":"`+p3+`","ratio":1}`, "-s", "rs")
	got = h.rects("rs")
	if got[p3].H != 3 || got[p1].H <= 3 {
		t.Fatalf("ratio 1 height: %v", got)
	}
	if focus := h.focus("rs"); focus != p3 {
		t.Fatalf("resize moved focus to %q", focus)
	}

	if out, err := h.try("exec", `{"action":"resize-split","target":"nope","other":"`+p2+`","ratio":0.5}`, "-s", "rs"); err == nil {
		t.Fatalf("unknown pane accepted: %q", out)
	}

	// Same-axis nesting: row{ q1, row{q2,q3} }. The outer divider spans the
	// whole row, so the minimum is exactly 3 cells, not the leaf bounding box.
	h.run("new", "-s", "rs2")
	q1 := h.focus("rs2")
	q2 := h.split("rs2", q1, "right")
	q3 := h.split("rs2", q2, "right")
	tree := h.rpc("rs2", map[string]any{"action": "list"})
	cols := int(tree["snapshot"].(map[string]any)["cols"].(float64))
	h.run("exec", `{"action":"resize-split","target":"`+q1+`","other":"`+q2+`","ratio":0}`, "-s", "rs2")
	got = h.rects("rs2")
	if got[q1].W != 3 {
		t.Fatalf("outer minimum with same-axis nesting = %v, want 3", got)
	}
	if got[q2].W+got[q3].W+3 != cols-8 {
		t.Fatalf("outer right side width: %v", got)
	}
	h.run("exec", `{"action":"resize-split","target":"`+q1+`","other":"`+q2+`","ratio":1}`, "-s", "rs2")
	got = h.rects("rs2")
	if got[q1].W != cols-8 {
		t.Fatalf("outer maximum with same-axis nesting: %v", got)
	}
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("pid file %s: %v", path, err)
	}
	var pid int
	if _, err := fmt.Sscanf(string(b), "%d", &pid); err != nil {
		t.Fatalf("pid file %s: %v", path, err)
	}
	return pid
}

// compareScreens fails unless two terminals agree cell for cell and on the
// cursor, visibility, buffer, wrap and mouse modes.
func compareScreens(t *testing.T, want, got vt10x.View) {
	t.Helper()
	wc, wr := want.Size()
	gc, gr := got.Size()
	if wc != gc || wr != gr {
		t.Fatalf("size %dx%d, want %dx%d", gc, gr, wc, wr)
	}
	want.Lock()
	defer want.Unlock()
	got.Lock()
	defer got.Unlock()
	for y := 0; y < wr; y++ {
		for x := 0; x < wc; x++ {
			a := normCell(want.Cell(x, y))
			b := normCell(got.Cell(x, y))
			if a.Char != b.Char || a.FG != b.FG || a.BG != b.BG {
				t.Fatalf("cell %d,%d = {%q %v %v}, want {%q %v %v}",
					x, y, b.Char, b.FG, b.BG, a.Char, a.FG, a.BG)
			}
		}
	}
	if a, b := want.Cursor(), got.Cursor(); a.X != b.X || a.Y != b.Y {
		t.Fatalf("cursor %d,%d, want %d,%d", b.X, b.Y, a.X, a.Y)
	}
	if want.CursorVisible() != got.CursorVisible() {
		t.Fatalf("cursor visible = %v, want %v", got.CursorVisible(), want.CursorVisible())
	}
	if a, b := want.Mode(), got.Mode(); (a & vt10x.ModeAltScreen) != (b & vt10x.ModeAltScreen) {
		t.Fatalf("alt = %v, want %v", b&vt10x.ModeAltScreen != 0, a&vt10x.ModeAltScreen != 0)
	}
	if a, b := want.Mode(), got.Mode(); (a & vt10x.ModeWrap) != (b & vt10x.ModeWrap) {
		t.Fatalf("wrap = %v, want %v", b&vt10x.ModeWrap != 0, a&vt10x.ModeWrap != 0)
	}
	if a, b := want.Mode(), got.Mode(); (a & vt10x.ModeMouseMask) != (b & vt10x.ModeMouseMask) {
		t.Fatalf("mouse mask = %v, want %v", b&vt10x.ModeMouseMask, a&vt10x.ModeMouseMask)
	}
	if a, b := want.Mode(), got.Mode(); (a & vt10x.ModeMouseSgr) != (b & vt10x.ModeMouseSgr) {
		t.Fatalf("mouse sgr = %v, want %v", b&vt10x.ModeMouseSgr != 0, a&vt10x.ModeMouseSgr != 0)
	}
}

// A snapshot written into a fresh terminal must reproduce the screen exactly.
// Plain text, every colour class, reverse video, an alternate screen, a moved
// cursor and the mode flags all have to survive; dropping any one fails.
func TestScreenSnapshotRoundTrip(t *testing.T) {
	src := vt10x.New(vt10x.WithSize(24, 6))
	content := "\x1b[?1049h" + // alternate screen, the harder buffer
		"PLAIN\r\n" +
		"\x1b[31mRED\x1b[0m \x1b[44mBLUEBG\x1b[0m\r\n" +
		"\x1b[38;5;196mPAL256\x1b[0m\r\n" +
		"\x1b[38;2;30;144;255mTRUE\x1b[0m\r\n" +
		"\x1b[7mREV\x1b[0m \x1b[31;42;7mMIXREV\x1b[0m" +
		"\x1b[5;7H" + // cursor away from the end of the output
		"\x1b[?25l" + // hidden cursor
		"\x1b[?7l" + // wrap off
		"\x1b[?1002h\x1b[?1006h" // mouse motion plus SGR
	if _, err := src.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	dst := vt10x.New(vt10x.WithSize(24, 6))
	if _, err := dst.Write(screenBytes(src)); err != nil {
		t.Fatal(err)
	}
	compareScreens(t, src, dst)

	// The same on the primary screen with wrap on, which is the common case.
	plain := vt10x.New(vt10x.WithSize(20, 3))
	if _, err := plain.Write([]byte("\x1b[36mCYAN\x1b[0m text\x1b[2;3H")); err != nil {
		t.Fatal(err)
	}
	plainDst := vt10x.New(vt10x.WithSize(20, 3))
	if _, err := plainDst.Write(screenBytes(plain)); err != nil {
		t.Fatal(err)
	}
	compareScreens(t, plain, plainDst)
}

// The pane action returns canonical ANSI for a live shell pane, not the ring.
func TestScreenAction(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "snap")
	pane := h.focus("snap")
	h.run("write-to", pane, "-s", "snap", "--msg", "echo SNAP-MARK", "--enter")
	h.waitFor("snap", pane, "SNAP-MARK")
	t.Setenv("DM_STATE", h.stateDir())

	m, err := paneRequest("snap", pane, Msg{Type: "req", Action: "screen"})
	if err != nil {
		t.Fatalf("screen: %v", err)
	}
	if m.Type != "screen" || len(m.Data) == 0 {
		t.Fatalf("screen reply type=%q len=%d", m.Type, len(m.Data))
	}
	if !bytes.Contains(m.Data, []byte("\x1b[")) {
		t.Fatalf("screen reply has no ANSI: %q", m.Data)
	}
	rect := h.rects("snap")[pane]
	vt := vt10x.New(vt10x.WithSize(rect.W, rect.H))
	if _, err := vt.Write(m.Data); err != nil {
		t.Fatal(err)
	}
	if got := vt.String(); !strings.Contains(got, "SNAP-MARK") {
		t.Fatalf("screen snapshot lost the pane text:\n%s", got)
	}
}

// A screen replaces the live VT; a later replay only replaces history.
func TestClientScreenThenReplay(t *testing.T) {
	src := vt10x.New(vt10x.WithSize(12, 3))
	if _, err := src.Write([]byte("\x1b[33mLIVE-SCREEN\x1b[0m\x1b[2;4H")); err != nil {
		t.Fatal(err)
	}
	data := screenBytes(src)

	vts := map[string]vt10x.Terminal{"p1": vt10x.New(vt10x.WithSize(12, 3))}
	_, _ = vts["p1"].Write([]byte("OLD CONTENT"))
	hist := map[string][]byte{}

	applyScreen(vts, "p1", Rect{W: 12, H: 3}, data)
	applyReplay(hist, "p1", []byte("RING-HISTORY"))

	live := vts["p1"]
	if live == nil {
		t.Fatal("screen did not install a VT")
	}
	got := live.String()
	if !strings.Contains(got, "LIVE-SCREEN") {
		t.Fatalf("live VT missing screen content:\n%s", got)
	}
	if strings.Contains(got, "OLD CONTENT") {
		t.Fatalf("live VT kept the old content:\n%s", got)
	}
	if strings.Contains(got, "RING-HISTORY") {
		t.Fatalf("replay wrote into the live VT:\n%s", got)
	}
	if string(hist["p1"]) != "RING-HISTORY" {
		t.Fatalf("history = %q", hist["p1"])
	}

	// A second replay still leaves the live VT alone.
	applyReplay(hist, "p1", []byte("MORE-HISTORY"))
	if again := live.String(); again != got {
		t.Fatalf("replay changed the live VT:\n%s", again)
	}
	if string(hist["p1"]) != "MORE-HISTORY" {
		t.Fatalf("history = %q", hist["p1"])
	}
}
