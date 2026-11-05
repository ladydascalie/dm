package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

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
	// Each box carries its two borders; adjacent borders form the divider.
	if rects["a"].W+rects["b"].W+4 != 100 {
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

// TestDividerIsAdjacent pins the pane spacing: boxes touch, so the divider is
// the two adjacent border cells and there is no empty gap cell.
func TestDividerIsAdjacent(t *testing.T) {
	area := Rect{0, 0, 80, 24}
	row := &Node{
		Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*Node{{Pane: "a"}, {Pane: "b"}},
	}
	boxes := childBoxRects(row, area)
	if boxes[1].X != boxes[0].X+boxes[0].W {
		t.Fatalf("row boxes are not adjacent: %v", boxes)
	}
	if p := padRect(row, boxes, 0); p.X != boxes[0].X+boxes[0].W-1 || p.W != 2 {
		t.Fatalf("row divider = %v, want the two border cells", p)
	}

	col := &Node{
		Dir: "col", Sizes: []float64{0.5, 0.5},
		Kids: []*Node{{Pane: "a"}, {Pane: "b"}},
	}
	boxes = childBoxRects(col, area)
	if boxes[1].Y != boxes[0].Y+boxes[0].H {
		t.Fatalf("column boxes are not adjacent: %v", boxes)
	}
	if p := padRect(col, boxes, 0); p.Y != boxes[0].Y+boxes[0].H-1 || p.H != 2 {
		t.Fatalf("column divider = %v, want the two border cells", p)
	}

	// The content rects lose only their own borders, so an 80-wide area gives
	// two 38-cell interiors around four border cells.
	rects := map[string]Rect{}
	layout(row, area, rects)
	if rects["a"].W != 38 || rects["b"].W != 38 {
		t.Fatalf("interiors = %v, want 38 each", rects)
	}
}

func TestPadHitFindsThePair(t *testing.T) {
	a, b, c := "a", "b", "c"
	flat := &Node{Dir: "row", Kids: []*Node{{Pane: a}, {Pane: b}}, Sizes: []float64{0.5, 0.5}}
	area := Rect{0, 0, 80, 24}
	boxes := childBoxRects(flat, area)
	pad := padRect(flat, boxes, 0)
	split, first, second, axis, sr, _, ok := padHit(flat, area, pad.X, pad.Y+3)
	if !ok || split != flat || first != a || second != b || axis != "row" || sr != area {
		t.Fatalf("pad hit = %v %q %q %q %v %v", split, first, second, axis, sr, ok)
	}
	if _, _, _, _, _, _, ok := padHit(flat, area, boxes[0].X+1, boxes[0].Y+1); ok {
		t.Fatal("a pane interior hit the pad")
	}

	// Nested: the outer pad pairs a with the right column.
	inner := &Node{Dir: "row", Kids: []*Node{{Pane: b}, {Pane: c}}, Sizes: []float64{0.5, 0.5}}
	nested := &Node{Dir: "row", Kids: []*Node{{Pane: a}, inner}, Sizes: []float64{0.6, 0.4}}
	nb := childBoxRects(nested, area)
	np := padRect(nested, nb, 0)
	_, first, second, axis, _, _, ok = padHit(nested, area, np.X, np.Y+3)
	if !ok || axis != "row" || first != a || (second != b && second != c) {
		t.Fatalf("nested pad hit = %q %q %q %v", first, second, axis, ok)
	}
	ib := childBoxRects(inner, nb[1])
	ipad := padRect(inner, ib, 0)
	_, first, second, _, _, _, ok = padHit(nested, area, ipad.X, ipad.Y+3)
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
	const cols, rows = 100, 30
	sessions := []SessionInfo{{Name: "alpha", Live: true}, {Name: "beta"}}
	var buf bytes.Buffer
	drawSessions(&buf, cols, rows, sessions, 1, "alpha")
	screen := panelFilled(t, cols, rows, buf.Bytes())

	// The picker is 46 wide and centred: its top row, one row per session, the
	// tee row, the key hint and its bottom row.
	w := 46
	x := (cols - w) / 2
	y := (rows - (len(sessions) + 5)) / 2
	edges := []struct {
		row         int
		left, right rune
	}{
		{y, []rune(glyphBorderTopLeft)[0], []rune(glyphBorderTopRight)[0]},
		{y + 1, []rune(glyphBorderVertical)[0], []rune(glyphBorderVertical)[0]},
		{y + 2, []rune(glyphBorderVertical)[0], []rune(glyphBorderVertical)[0]},
		{y + 3, []rune(glyphBorderTeeLeft)[0], []rune(glyphBorderTeeRight)[0]},
		{y + 4, []rune(glyphBorderVertical)[0], []rune(glyphBorderVertical)[0]},
		{y + 5, []rune(glyphBorderBottomLeft)[0], []rune(glyphBorderBottomRight)[0]},
	}
	for _, e := range edges {
		if got := panelChar(screen, x, e.row); got != e.left {
			t.Fatalf("row %d starts with %q, want %q", e.row, got, e.left)
		}
		if got := panelChar(screen, x+w-1, e.row); got != e.right {
			t.Fatalf("row %d ends with %q, want %q", e.row, got, e.right)
		}
	}
	// Every cell of every box row is painted and no cell beside the box is.
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			got := panelChar(screen, col, row)
			in := row >= y && row <= y+5 && col >= x && col < x+w
			if in && got == 'X' {
				t.Fatalf("row %d leaves cell %d of the box unpainted", row, col)
			}
			if !in && got != 'X' {
				t.Fatalf("row %d paints cell %d outside the box: %q", row, col, got)
			}
		}
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
	if st.State != "blocked" || st.Message != "needs review" || st.App != "pi" {
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
	start := time.Now()
	deadline := start.Add(waitBudget)
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
			t.Fatalf("pane never reached alt=%v mouse=%v within the %s wait budget (waited %s): %v",
				alt, mouse, waitBudget, time.Since(start).Round(time.Millisecond), snap["panes"])
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

// railServer is an in-process server with no panes and no clients. Layout and
// snapshots do not need a spawned process.
func railServer(st *State) *server {
	return &server{
		st: st, panes: map[string]*paneConn{},
		clients: map[*client]bool{}, cols: st.Cols, rows: st.Rows,
	}
}

// rootPane gives a state a single focused pane in its first window.
func rootPane(st *State) string {
	w := st.win()
	id := w.newPaneID()
	w.Root = &Node{Pane: id}
	w.Focus = id
	st.Panes[id] = &PaneState{}
	return id
}

func TestRailGeometry(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	st := newState("rail")
	id := rootPane(st)
	s := railServer(st)

	snap := s.snapshotLocked()
	want := effectiveRail(80, defaultRail)
	if snap.Rail != want {
		t.Fatalf("new session rail = %d, want %d", snap.Rail, want)
	}
	p := snap.Panes[0]
	if p.ID != id || p.Rect.X-1 != want {
		t.Fatalf("new session pane %q box starts at %d, want %d", p.ID, p.Rect.X-1, want)
	}
	if area := paneAreaFor(80, 24, snap.Rail); area.X != want || p.Rect.X != area.X+1 {
		t.Fatalf("pane rect %+v does not sit inside area %+v", p.Rect, area)
	}

	// Rail 0 hides it and the pane starts at the screen edge.
	if rail := s.setRail(0); rail != 0 {
		t.Fatalf("set-rail 0 effective = %d", rail)
	}
	snap = s.snapshotLocked()
	if snap.Rail != 0 || snap.Panes[0].Rect.X-1 != 0 {
		t.Fatalf("hidden rail: rail=%d box x=%d", snap.Rail, snap.Panes[0].Rect.X-1)
	}

	// The thin rail keeps a fixed width and the glyph column.
	if rail := s.setRail(collapsedRail); rail != collapsedRail {
		t.Fatalf("set-rail %d effective = %d", collapsedRail, rail)
	}
	snap = s.snapshotLocked()
	if snap.Rail != collapsedRail || snap.Panes[0].Rect.X-1 != collapsedRail {
		t.Fatalf("thin rail: rail=%d box x=%d", snap.Rail, snap.Panes[0].Rect.X-1)
	}

	// A width over the clamp uses the clamp: a third of 120 is 40, under the
	// 46 cap.
	s.cols = 120
	if rail := s.setRail(50); rail != 40 {
		t.Fatalf("set-rail 50 at 120 cols effective = %d, want 40", rail)
	}
	snap = s.snapshotLocked()
	if snap.Rail != 40 || snap.Panes[0].Rect.X-1 != 40 {
		t.Fatalf("clamped rail: rail=%d box x=%d", snap.Rail, snap.Panes[0].Rect.X-1)
	}
	if area := paneAreaFor(120, 24, 45); area != (Rect{X: 40, Y: 0, W: 80, H: 23}) {
		t.Fatalf("paneAreaFor clamps to %+v", area)
	}
	// A wide screen stops at the 46 cap.
	if m := maxRail(200); m != 46 {
		t.Fatalf("maxRail(200) = %d, want 46", m)
	}
	// The third-of-screen clamp binds below the 30 cap.
	if area := paneAreaFor(60, 24, 30); area.X != 20 || area.W != 40 {
		t.Fatalf("paneAreaFor third clamp = %+v", area)
	}
	// A negative request is no rail.
	if area := paneAreaFor(80, 24, -5); area.X != 0 {
		t.Fatalf("negative rail area = %+v", area)
	}
}

// TestRailBranch runs the branch check off the snapshot path and labels the
// workspace with the focused pane's branch.
func TestRailBranch(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	old := detectGit
	detectGit = func(cwd string) gitInfo {
		if cwd == "/work/dm" {
			return gitInfo{branch: "dm-rail"}
		}
		return gitInfo{}
	}
	defer func() { detectGit = old }()

	st := newState("branch")
	id := rootPane(st)
	st.Panes[id].Cwd = "/work/dm"
	s := railServer(st)

	deadline := time.Now().Add(2 * time.Second)
	for {
		snap := s.snapshotLocked()
		if len(snap.All) == 1 && snap.All[0].Branch == "dm-rail" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("branch never reached the snapshot: %+v", snap.All)
		}
		time.Sleep(10 * time.Millisecond)
	}
	items := railItems(s.snapshotLocked())
	if len(items) < 2 || items[1].branch != "dm-rail" || items[1].folder != "dm" {
		t.Fatalf("window line = %+v, want folder dm and branch dm-rail", items)
	}
}

// TestToggleFold drives the fold action through a live session.
func TestToggleFold(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "fold")
	unfolded := func() bool {
		tree := h.rpc("fold", map[string]any{"action": "list"})
		snap := tree["snapshot"].(map[string]any)
		all := snap["all"].([]any)
		return all[0].(map[string]any)["windowUnfolded"] == true
	}
	if unfolded() {
		t.Fatal("a new window starts unfolded, want folded")
	}
	if res := h.rpc("fold", map[string]any{"action": "toggle-fold", "target": "w1"}); res["unfolded"] != true {
		t.Fatalf("toggle-fold = %v, want unfolded", res["unfolded"])
	}
	if !unfolded() {
		t.Fatal("snapshot does not show the window unfolded")
	}
	st := h.sessionState("fold")
	if !st.Workspaces[0].Windows[0].Unfolded {
		t.Fatal("fold state not persisted")
	}
	if res := h.rpc("fold", map[string]any{"action": "toggle-fold", "target": "w1"}); res["unfolded"] != false {
		t.Fatalf("second toggle-fold = %v, want folded", res["unfolded"])
	}
}

// TestSessionSizeIsLargestClient keeps a small client, such as a dm nested in
// one of the session's own panes, from shrinking every other display.
// TestSameSessionAttachRefused stops a dm nested in a pane from attaching the
// session it already lives in.
func TestSameSessionAttachRefused(t *testing.T) {
	t.Setenv("DM_SESSION", "main")
	if err := runClient("main"); err == nil || !strings.Contains(err.Error(), "already inside") {
		t.Fatalf("same-session attach = %v, want refusal", err)
	}
	t.Setenv("DM_SESSION", "work")
	err := runClient("main")
	if err == nil || strings.Contains(err.Error(), "already inside") {
		t.Fatalf("other-session attach = %v, want the terminal check", err)
	}
}

func TestSessionSizeIsLargestClient(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	st := newState("sizes")
	s := railServer(st)
	big := &client{cols: 133, rows: 43}
	small := &client{cols: 77, rows: 7}

	s.mu.Lock()
	s.clients[big] = true
	if !s.fitToClientsLocked() || s.cols != 133 || s.rows != 43 {
		t.Fatalf("big client size = %dx%d", s.cols, s.rows)
	}
	s.clients[small] = true
	if s.fitToClientsLocked() {
		t.Fatal("a small client shrank the session")
	}
	s.mu.Unlock()
	if s.cols != 133 || s.rows != 43 {
		t.Fatalf("size after small attach = %dx%d", s.cols, s.rows)
	}

	// When the big client leaves, the remaining one owns the size.
	s.mu.Lock()
	delete(s.clients, big)
	if !s.fitToClientsLocked() {
		t.Fatal("size did not follow the remaining client")
	}
	s.mu.Unlock()
	if s.cols != 77 || s.rows != 7 {
		t.Fatalf("size after big detach = %dx%d", s.cols, s.rows)
	}
}

func TestRailState(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	st := newState("rr")
	s := railServer(st)
	s.cols = 100
	if rail := s.setRail(30); rail != 30 {
		t.Fatalf("set-rail 30 effective = %d", rail)
	}

	got, err := loadState("rr")
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	if got.Rail != 30 || !got.RailSet {
		t.Fatalf("reload rail = %d set=%v, want 30 true", got.Rail, got.RailSet)
	}

	// A state written before the rail existed asks for the default and is
	// saved with it.
	old := &State{
		Name: "old", Cols: 80, Rows: 24,
		Panes:      map[string]*PaneState{},
		Workspaces: []*Workspace{{ID: "s1", Name: "main"}},
		IDVersion:  2,
	}
	if err := old.save(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(statePath("old"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	delete(raw, "rail")
	delete(raw, "railSet")
	b, _ = json.Marshal(raw)
	if err := os.WriteFile(statePath("old"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = loadState("old")
	if err != nil {
		t.Fatalf("loadState old: %v", err)
	}
	if got.Rail != defaultRail || !got.RailSet {
		t.Fatalf("state without railSet = %d set=%v, want %d true", got.Rail, got.RailSet, defaultRail)
	}
	saved, err := os.ReadFile(statePath("old"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), fmt.Sprintf("\"rail\": %d", defaultRail)) ||
		!strings.Contains(string(saved), "\"railSet\": true") {
		t.Fatalf("state without railSet was not saved: %s", saved)
	}
}

func TestRailAllPanes(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	st := newState("all")
	st.Workspaces = []*Workspace{
		{ID: "s1", Name: "main", Active: 0, Windows: []*Window{{
			ID: "w1", Name: "one", Focus: "w1p1",
			Root: &Node{
				Dir: "row", Sizes: []float64{0.5, 0.5},
				Kids: []*Node{{Pane: "w1p1"}, {Pane: "w1p2"}},
			},
		}}},
		{ID: "s2", Name: "work", Active: 0, Windows: []*Window{{
			ID: "w2", Name: "two", Focus: "w2p1",
			Root: &Node{
				Dir: "col", Sizes: []float64{0.5, 0.5},
				Kids: []*Node{{Pane: "w2p1"}, {Pane: "w2p2"}},
			},
		}}},
	}
	st.Active = 0
	st.Panes = map[string]*PaneState{
		"w1p1": {Name: "alpha"},
		"w1p2": {},
		"w2p1": {},
		"w2p2": {},
	}
	s := railServer(st)
	st.Panes["w1p2"].App = "pi"
	s.panes["w1p2"] = &paneConn{st: Status{State: "working", Message: "working hard"}}
	s.panes["w2p1"] = &paneConn{st: Status{State: "unknown", Message: "unreported"}}

	snap := s.snapshotLocked()
	if len(snap.Panes) != 2 {
		t.Fatalf("active window panes = %d, want 2", len(snap.Panes))
	}
	if len(snap.All) != 4 {
		t.Fatalf("All = %d panes, want 4: %+v", len(snap.All), snap.All)
	}
	wantIDs := []string{"w1p1", "w1p2", "w2p1", "w2p2"}
	for i, want := range wantIDs {
		if snap.All[i].ID != want {
			t.Fatalf("All[%d] = %q, want %q", i, snap.All[i].ID, want)
		}
	}
	first := snap.All[0]
	if first.Name != "alpha" || !first.Agent || !first.Focus ||
		!first.WorkspaceActive || !first.WindowActive {
		t.Fatalf("named agent row = %+v", first)
	}
	if first.Workspace != "s1" || first.WorkspaceName != "main" ||
		first.Window != "w1" || first.WindowName != "one" {
		t.Fatalf("first row group = %+v", first)
	}
	byStatus := snap.All[1]
	if !byStatus.Agent || byStatus.Status != "working" ||
		byStatus.Message != "working hard" || byStatus.Name != "" ||
		byStatus.App != "pi" {
		t.Fatalf("working pane row = %+v", byStatus)
	}
	unknown := snap.All[2]
	if unknown.Agent || unknown.Status != "unknown" || unknown.Message != "unreported" ||
		unknown.Workspace != "s2" || unknown.Window != "w2" ||
		unknown.WorkspaceActive || unknown.WindowActive || unknown.Focus {
		t.Fatalf("unknown-status shell row = %+v", unknown)
	}
	silent := snap.All[3]
	if silent.Agent || silent.Status != "" || silent.Name != "" {
		t.Fatalf("silent shell row = %+v", silent)
	}
}

func TestSetRailAction(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "ra")
	tree := h.rpc("ra", map[string]any{"action": "list"})
	snap := tree["snapshot"].(map[string]any)
	cols := int(snap["cols"].(float64))
	rows := int(snap["rows"].(float64))
	wantDefault := effectiveRail(cols, defaultRail)
	if rail := int(snap["rail"].(float64)); rail != wantDefault {
		t.Fatalf("new session rail = %d, want %d", rail, wantDefault)
	}
	pane := h.focus("ra")
	if x := h.rects("ra")[pane].X; x != paneAreaFor(cols, rows, defaultRail).X+1 {
		t.Fatalf("pane starts at x=%d, want rail+border %d", x,
			paneAreaFor(cols, rows, defaultRail).X+1)
	}

	if res := h.rpc("ra", map[string]any{"action": "set-rail", "index": 0}); res["rail"] != float64(0) {
		t.Fatalf("set-rail 0 = %v", res["rail"])
	}
	if x := h.rects("ra")[pane].X; x != 1 {
		t.Fatalf("hidden rail pane starts at x=%d, want 1", x)
	}

	want := maxRail(cols)
	res := h.rpc("ra", map[string]any{"action": "set-rail", "index": 500})
	if got := int(res["rail"].(float64)); got != want {
		t.Fatalf("set-rail 500 = %d, want clamp %d", got, want)
	}
	if x := h.rects("ra")[pane].X; x != want+1 {
		t.Fatalf("clamped rail pane starts at x=%d, want %d", x, want+1)
	}
}

// TestRailReflow drives set-rail through a live session. The pane rectangle
// and the pane's own persisted size must both follow the rail, so a skipped
// syncSizes fails even though the snapshot alone would look right.
func TestRailReflow(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "reflow")
	pane := h.focus("reflow")
	tree := h.rpc("reflow", map[string]any{"action": "list"})
	snap := tree["snapshot"].(map[string]any)
	cols := int(snap["cols"].(float64))
	rows := int(snap["rows"].(float64))
	rail := int(snap["rail"].(float64))

	check := func(label string, wantRail int) Rect {
		t.Helper()
		r := h.rects("reflow")[pane]
		area := paneAreaFor(cols, rows, wantRail)
		if r.X != area.X+1 || r.W != area.W-2 || r.H != area.H-2 {
			t.Fatalf("%s: pane rect %+v, want content of %+v", label, r, area)
		}
		st := h.sessionState("reflow")
		ps := st.Panes[pane]
		if ps == nil {
			t.Fatalf("%s: no state for pane %q", label, pane)
		}
		if ps.Cols != r.W || ps.Rows != r.H {
			t.Fatalf("%s: pane size %dx%d, rect %dx%d", label, ps.Cols, ps.Rows, r.W, r.H)
		}
		return r
	}

	before := check("new session", rail)
	if before.X != rail+1 {
		t.Fatalf("new session pane x = %d, want %d", before.X, rail+1)
	}

	if res := h.rpc("reflow", map[string]any{"action": "set-rail", "index": 0}); res["rail"] != float64(0) {
		t.Fatalf("set-rail 0 = %v", res["rail"])
	}
	hidden := check("hidden", 0)
	if hidden.X != 1 {
		t.Fatalf("hidden pane x = %d, want 1", hidden.X)
	}
	if before.X-hidden.X != rail || hidden.W-before.W != rail {
		t.Fatalf("reflow moved x %d and width %d, want both %d",
			before.X-hidden.X, hidden.W-before.W, rail)
	}

	if res := h.rpc("reflow", map[string]any{"action": "set-rail", "index": defaultRail}); res["rail"] != float64(rail) {
		t.Fatalf("set-rail %d = %v, want effective %d", defaultRail, res["rail"], rail)
	}
	back := check("restored", rail)
	if back != before {
		t.Fatalf("restored rect %+v, want %+v", back, before)
	}
}

// paneMsg is one message a pane's socket received, tagged with the pane.
type paneMsg struct {
	id string
	m  Msg
}

// zoomFixture is an in-process server whose active window is one row split,
// both panes wired to pipes, so a test reads the resize messages syncSizes
// sends. The right pane is focused.
type zoomFixture struct {
	s     *server
	left  string
	right string
	inbox chan paneMsg
}

func newZoomFixture(t *testing.T) *zoomFixture {
	t.Helper()
	t.Setenv("DM_STATE", t.TempDir())
	st := newState("zoom")
	w := st.win()
	left := rootPane(st)
	right := w.newPaneID()
	st.Panes[right] = &PaneState{}
	w.Root = &Node{Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*Node{{Pane: left}, {Pane: right}}}
	w.Focus = right

	f := &zoomFixture{s: railServer(st), left: left, right: right,
		inbox: make(chan paneMsg, 64)}
	for _, id := range []string{left, right} {
		srv, cli := net.Pipe()
		t.Cleanup(func() { srv.Close(); cli.Close() })
		f.s.panes[id] = &paneConn{id: id, c: srv}
		go func(id string, c net.Conn) {
			for {
				m, err := readMsg(c)
				if err != nil {
					return
				}
				f.inbox <- paneMsg{id: id, m: m}
			}
		}(id, cli)
	}
	// Settle the split sizes, so a later syncSizes reports only what the test
	// asked for.
	f.s.syncSizes()
	f.drain()
	return f
}

// drain returns the messages each pane received, once the channel goes quiet.
func (f *zoomFixture) drain() map[string][]Msg {
	out := map[string][]Msg{}
	for {
		select {
		case got := <-f.inbox:
			out[got.id] = append(out[got.id], got.m)
		case <-time.After(200 * time.Millisecond):
			return out
		}
	}
}

// zoom reads the session's zoomed pane id, empty when nothing is zoomed.
func (h *harness) zoom(session string) string {
	h.t.Helper()
	tree := h.rpc(session, map[string]any{"action": "list"})
	zoom, _ := tree["snapshot"].(map[string]any)["zoom"].(string)
	return zoom
}

// toggleZoom runs the zoom-pane action and returns the pane id left zoomed.
func (h *harness) toggleZoom(session string) string {
	h.t.Helper()
	res := h.rpc(session, map[string]any{"action": "zoom-pane"})
	zoom, _ := res["zoom"].(string)
	return zoom
}

// paneAreaOf is the session's pane area: the box a single unsplit pane fills.
func (h *harness) paneAreaOf(session string) Rect {
	h.t.Helper()
	tree := h.rpc(session, map[string]any{"action": "list"})
	snap := tree["snapshot"].(map[string]any)
	num := func(k string) int { return int(snap[k].(float64)) }
	return paneAreaFor(num("cols"), num("rows"), num("rail"))
}

// TestZoomSplitWindow pins the snapshot: the zoomed pane fills the pane area,
// every sibling keeps its layout rect, and Zoom names the pane.
func TestZoomSplitWindow(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "zwin")
	left := h.focus("zwin")
	right := h.split("zwin", left, "right")
	before := h.rects("zwin")
	if got := h.zoom("zwin"); got != "" {
		t.Fatalf("a new split starts zoomed on %q", got)
	}

	if got := h.toggleZoom("zwin"); got != right {
		t.Fatalf("zoom-pane = %q, want the focused pane %q", got, right)
	}
	after := h.rects("zwin")
	area := h.paneAreaOf("zwin")
	if want := insetBox(area); after[right] != want {
		t.Fatalf("zoomed rect %+v, want the single-pane box %+v of %+v", after[right], want, area)
	}
	if after[left] != before[left] {
		t.Fatalf("sibling rect moved: %+v, want %+v", after[left], before[left])
	}
	if after[right] == before[right] {
		t.Fatalf("zoom left the rect at %+v", after[right])
	}
	if got := h.zoom("zwin"); got != right {
		t.Fatalf("snapshot zoom = %q, want %q", got, right)
	}
}

// TestZoomSinglePaneNoop pins the one-pane guard: the action replies ok, sets
// no zoom, and leaves the rect alone.
func TestZoomSinglePaneNoop(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "zsolo")
	pane := h.focus("zsolo")
	before := h.rects("zsolo")
	if got := h.toggleZoom("zsolo"); got != "" {
		t.Fatalf("zoom-pane on one pane = %q, want no zoom", got)
	}
	if after := h.rects("zsolo"); after[pane] != before[pane] {
		t.Fatalf("rect = %+v, want %+v", after[pane], before[pane])
	}
}

// TestZoomSyncSizes pins the PTY side: the zoomed pane is resized to the single
// pane box, and a hidden sibling's PTY stays at its split rect.
func TestZoomSyncSizes(t *testing.T) {
	f := newZoomFixture(t)
	f.s.mu.Lock()
	area := paneAreaFor(f.s.cols, f.s.rows, f.s.railLocked())
	normal := f.s.paneRectsLocked()
	f.s.zoom = f.right
	f.s.mu.Unlock()
	want := insetBox(area)
	if normal[f.right] == want {
		t.Fatalf("the split rect %+v already fills %+v", normal[f.right], want)
	}

	f.s.syncSizes()
	got := f.drain()
	if len(got[f.left]) != 0 {
		t.Fatalf("hidden sibling got %v, want no resize", got[f.left])
	}
	if len(got[f.right]) != 1 {
		t.Fatalf("zoomed pane got %d messages, want one resize: %v", len(got[f.right]), got[f.right])
	}
	if m := got[f.right][0]; m.Action != "resize" || m.Cols != want.W || m.Rows != want.H {
		t.Fatalf("zoomed pane resize = %+v, want %dx%d", m, want.W, want.H)
	}
}

// TestZoomToggleRestores pins the toggle: a second zoom-pane clears the zoom
// and the split rects come back.
func TestZoomToggleRestores(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "ztoggle")
	left := h.focus("ztoggle")
	right := h.split("ztoggle", left, "right")
	before := h.rects("ztoggle")

	if got := h.toggleZoom("ztoggle"); got != right {
		t.Fatalf("zoom-pane = %q, want %q", got, right)
	}
	if got := h.toggleZoom("ztoggle"); got != "" {
		t.Fatalf("second zoom-pane = %q, want no zoom", got)
	}
	after := h.rects("ztoggle")
	if after[left] != before[left] || after[right] != before[right] {
		t.Fatalf("rects after the toggle = %v, want %v", after, before)
	}
	if got := h.zoom("ztoggle"); got != "" {
		t.Fatalf("snapshot zoom = %q after the toggle off", got)
	}
}

// TestZoomClearsOnFocusChange pins the focus guard: a focus move ends the zoom
// and the split rects return.
func TestZoomClearsOnFocusChange(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "zfocus")
	left := h.focus("zfocus")
	right := h.split("zfocus", left, "right")
	before := h.rects("zfocus")

	if got := h.toggleZoom("zfocus"); got != right {
		t.Fatalf("zoom-pane = %q, want %q", got, right)
	}
	h.rpc("zfocus", map[string]any{"action": "focus", "target": left})
	if got := h.zoom("zfocus"); got != "" {
		t.Fatalf("zoom survived a focus change: %q", got)
	}
	if got := h.rects("zfocus"); got[right] != before[right] || got[left] != before[left] {
		t.Fatalf("rects after the focus change = %v, want %v", got, before)
	}
}

// TestZoomClearsOnSplit pins the split guard: the new pane is visible, so the
// zoom ends and the split pane stops filling the area.
func TestZoomClearsOnSplit(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "zsplit")
	left := h.focus("zsplit")
	right := h.split("zsplit", left, "right")

	if got := h.toggleZoom("zsplit"); got != right {
		t.Fatalf("zoom-pane = %q, want %q", got, right)
	}
	h.split("zsplit", right, "down")
	if got := h.zoom("zsplit"); got != "" {
		t.Fatalf("zoom survived a split: %q", got)
	}
	if got, want := h.rects("zsplit")[right], insetBox(h.paneAreaOf("zsplit")); got == want {
		t.Fatalf("pane %q still fills the area %+v after the split", right, want)
	}
}

// TestZoomClearsOnClosePane pins the close guard: closing a sibling ends the
// zoom, so the zoomed pane returns to its split rect.
func TestZoomClearsOnClosePane(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "zclose")
	a := h.focus("zclose")
	b := h.split("zclose", a, "right")
	c := h.split("zclose", b, "down")

	if got := h.toggleZoom("zclose"); got != c {
		t.Fatalf("zoom-pane = %q, want %q", got, c)
	}
	h.rpc("zclose", map[string]any{"action": "close-pane", "target": a})
	if got := h.zoom("zclose"); got != "" {
		t.Fatalf("zoom survived a close: %q", got)
	}
	if got, want := h.rects("zclose")[c], insetBox(h.paneAreaOf("zclose")); got == want {
		t.Fatalf("pane %q still fills the area %+v after the close", c, want)
	}
}

// TestZoomClearsOnResizeSplit pins the resize-split guard: the drag is a layout
// change, so the zoom ends even though the handler skips afterMutation.
func TestZoomClearsOnResizeSplit(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "zdrag")
	left := h.focus("zdrag")
	right := h.split("zdrag", left, "right")

	if got := h.toggleZoom("zdrag"); got != right {
		t.Fatalf("zoom-pane = %q, want %q", got, right)
	}
	h.rpc("zdrag", map[string]any{"action": "resize-split",
		"target": left, "other": right, "ratio": 0.3})
	if got := h.zoom("zdrag"); got != "" {
		t.Fatalf("zoom survived resize-split: %q", got)
	}
	if got, want := h.rects("zdrag")[right], insetBox(h.paneAreaOf("zdrag")); got == want {
		t.Fatalf("pane %q still fills the area %+v after the drag", right, want)
	}
}

// TestZoomKeepsOnSetRail pins the rail exception: a rail width change keeps the
// zoom and the zoomed pane takes the new area.
func TestZoomKeepsOnSetRail(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "zrail")
	left := h.focus("zrail")
	right := h.split("zrail", left, "right")

	if got := h.toggleZoom("zrail"); got != right {
		t.Fatalf("zoom-pane = %q, want %q", got, right)
	}
	h.rpc("zrail", map[string]any{"action": "set-rail", "index": 0})
	if got := h.zoom("zrail"); got != right {
		t.Fatalf("set-rail cleared the zoom: %q", got)
	}
	want := insetBox(h.paneAreaOf("zrail"))
	if got := h.rects("zrail")[right]; got != want {
		t.Fatalf("zoomed rect %+v after the rail change, want %+v", got, want)
	}
	if want.X != 1 {
		t.Fatalf("hidden rail box starts at %d, want 1", want.X)
	}
}

// TestZoomKeepsOnTerminalResize pins the resize exception: a bigger client
// resizes the session, keeps the zoom, and the zoomed pane takes the new area.
func TestZoomKeepsOnTerminalResize(t *testing.T) {
	f := newZoomFixture(t)
	f.s.mu.Lock()
	f.s.zoom = f.right
	f.s.mu.Unlock()
	f.s.syncSizes()
	f.drain()

	// The resize case in serve: the largest client sets the size, then the
	// panes are resynced.
	f.s.mu.Lock()
	f.s.clients[&client{cols: 140, rows: 44}] = true
	grew := f.s.fitToClientsLocked()
	f.s.mu.Unlock()
	if !grew {
		t.Fatal("the session did not take the client size")
	}
	f.s.syncSizes()
	got := f.drain()

	f.s.mu.Lock()
	snap := f.s.snapshotLocked()
	f.s.mu.Unlock()
	if snap.Zoom != f.right {
		t.Fatalf("a terminal resize cleared the zoom: %q", snap.Zoom)
	}
	want := insetBox(paneAreaFor(140, 44, effectiveRail(140, defaultRail)))
	var rect Rect
	for _, p := range snap.Panes {
		if p.ID == f.right {
			rect = p.Rect
		}
	}
	if rect != want {
		t.Fatalf("zoomed rect %+v, want %+v", rect, want)
	}
	if len(got[f.right]) != 1 ||
		got[f.right][0].Cols != want.W || got[f.right][0].Rows != want.H {
		t.Fatalf("zoomed pane resize = %v, want one to %dx%d", got[f.right], want.W, want.H)
	}
}

// TestZoomStalePaneNotSent pins the validation: a zoom that names a pane of
// another window is dropped, so the snapshot and the PTYs stay unzoomed.
func TestZoomStalePaneNotSent(t *testing.T) {
	f := newZoomFixture(t)
	f.s.st.Workspaces[0].Windows = append(f.s.st.Workspaces[0].Windows,
		&Window{ID: "w2", Name: "two", Root: &Node{Pane: "w2p1"}})
	f.s.st.Panes["w2p1"] = &PaneState{}

	f.s.mu.Lock()
	f.s.zoom = "w2p1"
	snap := f.s.snapshotLocked()
	held := f.s.zoom
	area := paneAreaFor(f.s.cols, f.s.rows, f.s.railLocked())
	f.s.mu.Unlock()
	if snap.Zoom != "" {
		t.Fatalf("snapshot sent a zoom outside the active window: %q", snap.Zoom)
	}
	if held != "" {
		t.Fatalf("stale zoom %q held after the snapshot", held)
	}
	for _, p := range snap.Panes {
		if p.ID == f.right && p.Rect == insetBox(area) {
			t.Fatalf("pane %q fills the area %+v", f.right, area)
		}
	}

	f.s.syncSizes()
	if got := f.drain(); len(got[f.left])+len(got[f.right]) != 0 {
		t.Fatalf("a stale zoom resized the active window: %v", got)
	}
}

// TestZoomClearsOnPaneExit pins the death guard: when the zoomed pane's daemon
// dies, the pane leaves the layout and the zoom goes with it.
func TestZoomClearsOnPaneExit(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "zdead")
	left := h.focus("zdead")
	right := h.split("zdead", left, "right")

	if got := h.toggleZoom("zdead"); got != right {
		t.Fatalf("zoom-pane = %q, want %q", got, right)
	}
	pid := readPID(t, filepath.Join(h.stateDir(), "sessions", "zdead", "panes", right+".sock.pid"))
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		zoom := h.zoom("zdead")
		if zoom == "" && len(h.panes("zdead")) == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("zoom %q survived the exit of pane %q", zoom, right)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// railTestSnapshot is a two-pane session with a rail. Panes start after the
// rail, and All carries the same two panes in server order.
func railTestSnapshot(rail int) *Snapshot {
	root := &Node{
		Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*Node{{Pane: "w1p1"}, {Pane: "w1p2"}},
	}
	snap := &Snapshot{
		Session: "x", Workspace: "main", Windows: []string{"one"},
		Cols: 100, Rows: 10, Rail: rail, Root: root, Focus: "w1p1",
	}
	rects := map[string]Rect{}
	layout(root, paneAreaFor(snap.Cols, snap.Rows, rail), rects)
	snap.Panes = []PaneInfo{
		{ID: "w1p1", Rect: rects["w1p1"], Status: "working"},
		{ID: "w1p2", Rect: rects["w1p2"], Status: "idle"},
	}
	snap.All = []AllPane{
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
func railOnePane(status, name string, rail, cols, rows int) *Snapshot {
	root := &Node{Pane: "w1p1"}
	snap := &Snapshot{Cols: cols, Rows: rows, Rail: rail, Root: root, Focus: "w1p1"}
	rects := map[string]Rect{}
	layout(root, paneAreaFor(cols, rows, rail), rects)
	snap.Panes = []PaneInfo{{ID: "w1p1", Rect: rects["w1p1"], Status: status}}
	snap.All = []AllPane{{
		ID: "w1p1", Name: name, Status: status,
		Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one",
		Focus: true, WorkspaceActive: true, WindowActive: true, WindowUnfolded: true,
	}}
	return snap
}

// agentRail is railOnePane with the pane marked as an agent, so its row draws
// a state glyph.
func agentRail(status string) *Snapshot {
	snap := railOnePane(status, "", 12, 40, 6)
	snap.All[0].Agent = true
	return snap
}

func TestRailItemsGrouping(t *testing.T) {
	snap := &Snapshot{All: []AllPane{
		{
			ID: "w1p1", Name: "alpha", Workspace: "s1", WorkspaceName: "main",
			Window: "w1", WindowName: "one", WorkspaceActive: true, WindowActive: true,
			Focus: true, WindowUnfolded: true, Message: "working hard",
		},
		{
			ID: "w1p2", Workspace: "s1", WorkspaceName: "main", Window: "w1",
			WindowName: "one", WindowUnfolded: true,
		},
		{
			ID: "w1p3", Workspace: "s1", WorkspaceName: "main", Window: "w2",
			WindowName: "two", WindowUnfolded: true,
		},
		{
			ID: "w2p1", App: "pi", Agent: true, Workspace: "s2", WorkspaceName: "work",
			Window: "w3", WindowName: "three", WindowUnfolded: true,
		},
	}}
	items := railItems(snap)
	wantKinds := []railKind{
		railWorkspace, railWindow, railWindowNote, railPane, railPane,
		railWindow, railPane,
		railGap,
		railWorkspace, railWindow, railPane,
	}
	wantText := []string{
		"main", "one", "working hard", "alpha", "w1p2",
		"two", "w1p3",
		"",
		"work", "three", "pi",
	}
	if len(items) != len(wantKinds) {
		t.Fatalf("rail has %d rows, want %d: %+v", len(items), len(wantKinds), items)
	}
	for i, want := range wantKinds {
		if items[i].kind != want || items[i].text != wantText[i] {
			t.Fatalf("row %d = kind %d %q, want kind %d %q", i,
				items[i].kind, items[i].text, want, wantText[i])
		}
	}
	gaps := 0
	for _, it := range items {
		if it.kind == railGap {
			gaps++
		}
	}
	if gaps != 1 || items[7].kind != railGap {
		t.Fatalf("rail has %d gaps, want one at row 7: %+v", gaps, items)
	}
	if items[0].count != 3 || items[8].count != 1 {
		t.Fatalf("workspace pane counts = %d, %d, want 3, 1", items[0].count, items[8].count)
	}
	if items[0].workspace != "s1" || items[8].workspace != "s2" {
		t.Fatalf("workspace ids = %q, %q", items[0].workspace, items[8].workspace)
	}
	if !items[0].active || items[8].active {
		t.Fatalf("workspace rows active = %v, %v", items[0].active, items[8].active)
	}
	if !items[10].agent || items[3].agent {
		t.Fatalf("pane rows agent = %v, %v", items[10].agent, items[3].agent)
	}
	if !items[3].focus || items[4].focus {
		t.Fatalf("pane rows focus = %v, %v", items[3].focus, items[4].focus)
	}
	// A folded window drops its note line and its panes but keeps the window
	// line, whose message still belongs to the window.
	snap.All[0].WindowUnfolded = false
	for _, it := range railItems(snap) {
		if it.kind == railWindowNote {
			t.Fatalf("folded rail drew a note line: %+v", it)
		}
	}
}

func TestRailGlyphPerState(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	cases := []struct {
		status string
		glyph  string
		colour rgb
	}{
		{"working", "●", rgb{245, 190, 80}},
		{"idle", "○", rgb{120, 200, 225}},
		{"blocked", "▲", rgb{255, 110, 110}},
		{"done", "✓", rgb{110, 225, 145}},
		{"error", "✕", rgb{255, 130, 160}},
		{"unknown", "?", railDim},
		{"", "?", railDim},
	}
	seen := map[rgb]string{}
	for _, c := range cases {
		g, colour := railGlyph(c.status, false)
		if g != c.glyph || colour != c.colour {
			t.Fatalf("railGlyph(%q) = %q/%v, want %q/%v", c.status, g, colour, c.glyph, c.colour)
		}
		if prev, ok := seen[colour]; ok && prev != c.glyph {
			t.Fatalf("colour %v doubles for %q and %q", colour, prev, c.glyph)
		}
		seen[colour] = c.glyph

		var b bytes.Buffer
		drawRail(&b, agentRail(c.status))
		out := b.String()
		if !strings.Contains(out, c.glyph) || !strings.Contains(out, colour.fg()) {
			t.Fatalf("status %q not drawn as %q in %q: %q", c.status, c.glyph, colour.fg(), out)
		}
	}
}

// TestRailContrast holds the rail to WCAG AA on both backgrounds it paints.
func TestRailContrast(t *testing.T) {
	foregrounds := map[string]rgb{
		"text":    railText,
		"dim":     railDim,
		"header":  railHeader,
		"window":  railWindowFg,
		"diffAdd": railDiffAdd,
		"diffDel": railDiffDel,
	}
	for i, accent := range railAccents {
		foregrounds[fmt.Sprintf("accent %d", i)] = accent
	}
	for _, status := range []string{
		"working", "idle", "blocked", "done", "error", "unknown", "",
	} {
		fallback, colour := railGlyph(status, false)
		_, nerdColour := railGlyph(status, true)
		if fallback == "" || nerdColour != colour {
			t.Fatalf("state %q differs between glyph forms", status)
		}
		foregrounds["state "+status] = colour
	}
	for name, fg := range foregrounds {
		for bgName, bg := range map[string]rgb{"rail": railBG, "focus": railFocusBG} {
			if r := contrast(fg, bg); r < 4.5 {
				t.Fatalf("%s on %s = %.2f:1, want >= 4.5:1", name, bgName, r)
			}
		}
	}
	if r := contrast(railText, railBG); r < 7 {
		t.Fatalf("body text on rail = %.2f:1, want >= 7:1", r)
	}
}

// TestRailAccent keeps railAccent stable per id and the six palette hues
// distinct. Giving two live workspaces different hues is railItems' job.
func TestRailAccent(t *testing.T) {
	for _, ws := range []string{"main", "work", "dm-git-w1", ""} {
		first, second := railAccent(ws), railAccent(ws)
		if first != second {
			t.Fatalf("railAccent(%q) = %v then %v", ws, first, second)
		}
	}
	if len(railAccents) != 6 {
		t.Fatalf("railAccents has %d hues, want 6", len(railAccents))
	}
	seen := map[rgb]bool{}
	for _, accent := range railAccents {
		if seen[accent] {
			t.Fatalf("accent %v repeats in railAccents", accent)
		}
		seen[accent] = true
	}
	for _, ws := range []string{"main", "work", "scratch", "s1", "s2", "ws1", "ws6"} {
		if got := railAccent(ws); !seen[got] {
			t.Fatalf("railAccent(%q) = %v, which is not in railAccents", ws, got)
		}
	}
}

// TestRailNerdIcons draws with Nerd Font icons: a caret on the window line, a
// robot for agents, a terminal for shells, and an icon for every state.
func TestRailNerdIcons(t *testing.T) {
	t.Setenv("DM_NERD", "1")
	agent := railOnePane("working", "pi", 12, 40, 6)
	agent.All[0].Agent = true
	agent.All[0].InRepo = true
	var b bytes.Buffer
	drawRail(&b, agent)
	out := b.String()
	for _, want := range []string{"\uf07b", "\uf0d7", "\uf135", "\uf013", "pi", "clean"} {
		if want == "\uf07b" {
			continue
		}
		if !strings.Contains(out, want) {
			t.Fatalf("nerd rail missing %q: %q", want, out)
		}
	}
	if strings.Contains(out, "\uf07b") {
		t.Fatalf("nerd rail still draws the old folder icon: %q", out)
	}
	if strings.Contains(out, "\u258e") {
		t.Fatalf("nerd rail drew the fallback bar: %q", out)
	}
	// The full rail nests: accent bar at 0, caret at 2, pane mark at 6, glyph
	// at 8, under the note line at 4.
	vt := vt10x.New(vt10x.WithSize(40, 6))
	if _, err := vt.Write(b.Bytes()); err != nil {
		t.Fatalf("draw nerd rail: %v", err)
	}
	vt.Lock()
	bar, caret := vt.Cell(0, 0).Char, vt.Cell(2, 1).Char
	mark, glyph := vt.Cell(6, 2).Char, vt.Cell(8, 2).Char
	vt.Unlock()
	if bar != '▌' || caret != '\uf0d7' || mark != '\uf135' || glyph != '\uf013' {
		t.Fatalf("nerd columns = %q %q %q %q, want bar caret robot gear",
			bar, caret, mark, glyph)
	}

	shell := railOnePane("unknown", "", 12, 40, 6)
	b.Reset()
	drawRail(&b, shell)
	out = b.String()
	if !strings.Contains(out, "\uf120") {
		t.Fatalf("nerd shell row = %q, want the terminal icon", out)
	}
	if strings.Contains(out, "\uf128") {
		t.Fatalf("nerd shell row drew an unknown-state icon: %q", out)
	}
}

// TestRailShellHasNoStateGlyph keeps the state column blank for a pane that is
// not an agent: it has no status to report, so the question mark would ask
// about nothing.
func TestRailShellHasNoStateGlyph(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	for _, status := range []string{"", "unknown"} {
		var b bytes.Buffer
		drawRail(&b, railOnePane(status, "w1p1", 12, 40, 6))
		vt := vt10x.New(vt10x.WithSize(40, 6))
		if _, err := vt.Write(b.Bytes()); err != nil {
			t.Fatalf("draw shell rail: %v", err)
		}
		vt.Lock()
		glyph, text := vt.Cell(8, 2).Char, vt.Cell(10, 2).Char
		vt.Unlock()
		if glyph != ' ' || text != 'w' {
			t.Fatalf("shell row status %q = %q at 8, %q at 10: want a blank cell",
				status, glyph, text)
		}
	}

	// An agent that has not reported yet still asks with the question mark.
	var b bytes.Buffer
	drawRail(&b, agentRail(""))
	vt := vt10x.New(vt10x.WithSize(40, 6))
	if _, err := vt.Write(b.Bytes()); err != nil {
		t.Fatalf("draw agent rail: %v", err)
	}
	vt.Lock()
	glyph := vt.Cell(8, 2).Char
	vt.Unlock()
	if glyph != '?' {
		t.Fatalf("agent row without a status = %q at 8, want ?", glyph)
	}
}

// TestRailAgentMarker separates agents from shells. An agent gets a bar in the
// two cells a shell leaves blank, and its app= stands in for a missing name.
func TestRailAgentMarker(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	agent := railOnePane("unknown", "", 20, 40, 6)
	agent.All[0].Agent = true
	agent.All[0].App = "pi"
	var b bytes.Buffer
	drawRail(&b, agent)
	out := b.String()
	if !strings.Contains(out, "\u258e") {
		t.Fatalf("agent row has no marker: %q", out)
	}
	if !strings.Contains(out, "pi") {
		t.Fatalf("agent row does not show the app: %q", out)
	}

	shell := railOnePane("unknown", "", 20, 40, 6)
	b.Reset()
	drawRail(&b, shell)
	out = b.String()
	if strings.Contains(out, "\u258e") {
		t.Fatalf("shell row has the agent marker: %q", out)
	}
	if !strings.Contains(out, "w1p1") {
		t.Fatalf("shell row does not fall back to the pane id: %q", out)
	}

	// The thin rail keeps the marker and the glyph in their columns.
	thin := railOnePane("working", "alpha", collapsedRail, 40, 6)
	thin.All[0].Agent = true
	b.Reset()
	drawRail(&b, thin)
	vt := vt10x.New(vt10x.WithSize(40, 6))
	if _, err := vt.Write(b.Bytes()); err != nil {
		t.Fatalf("draw thin agent rail: %v", err)
	}
	vt.Lock()
	bar, glyph := vt.Cell(0, 0).Char, vt.Cell(2, 0).Char
	vt.Unlock()
	if bar != '\u258e' || glyph != '●' {
		t.Fatalf("thin agent row = %q at 0, %q at 2", bar, glyph)
	}
}

func TestRailHit(t *testing.T) {
	snap := railTestSnapshot(10)
	// Rows: workspace, window, w1p1, w1p2.
	cases := []struct {
		x, y int
		want string
	}{
		{5, 2, "w1p1"},
		{5, 3, "w1p2"},
		{5, 0, ""},  // workspace header
		{5, 1, ""},  // window header
		{5, 4, ""},  // past the last row
		{5, 9, ""},  // status bar
		{10, 2, ""}, // pane area
		{-1, 2, ""}, // left of the screen
	}
	for _, c := range cases {
		if got := railPaneAt(snap, c.x, c.y); got != c.want {
			t.Fatalf("railPaneAt(%d,%d) = %q, want %q", c.x, c.y, got, c.want)
		}
	}
	// The last item row above the control can be a pane row. The control
	// takes Rows-2, so the item rows stop at Rows-3.
	small := railTestSnapshot(10)
	small.Rows = 5
	small.All = []AllPane{
		{
			ID: "w1p1", Workspace: "s1", WorkspaceName: "main", Window: "w1",
			WindowName: "one", WindowUnfolded: true,
		},
		{
			ID: "w1p2", Workspace: "s1", WorkspaceName: "main", Window: "w1",
			WindowName: "one", WindowUnfolded: true,
		},
	}
	if got := railPaneAt(small, 5, 2); got != "w1p1" {
		t.Fatalf("last pane row = %q", got)
	}
}

func TestRailClickFocusesPane(t *testing.T) {
	tui, got := clientTUI(t, nil)
	tui.snap = railTestSnapshot(10)
	// Left press on w1p2's row focuses w1p2, not the focused w1p1. The row is
	// zero-based y=3, so the one-based mouse row is 4.
	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 4})
	if m := nextMsg(t, got); m.Action != "focus" || m.Target != "w1p2" {
		t.Fatalf("rail click sent %+v, want focus w1p2", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 3, y: 4})
	// A window row toggles its fold; a workspace row does nothing.
	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 2})
	if m := nextMsg(t, got); m.Action != "toggle-fold" || m.Target != "w1" {
		t.Fatalf("window click sent %+v, want toggle-fold w1", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 3, y: 2})
	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 1})
	select {
	case m := <-got:
		t.Fatalf("workspace header click sent %+v", m)
	case <-time.After(50 * time.Millisecond):
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 3, y: 1})
	// A pane click still focuses the pane under the pointer, not a rail row.
	p := tui.paneByID("w1p2")
	tui.handleMouse(mouseEvent{btn: 0, x: p.Rect.X + 1, y: p.Rect.Y + 1})
	if m := nextMsg(t, got); m.Action != "focus" || m.Target != "w1p2" {
		t.Fatalf("pane click sent %+v, want focus w1p2", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: p.Rect.X + 1, y: p.Rect.Y + 1})
	// A right press in the rail must not open a pane menu.
	tui.handleMouse(mouseEvent{btn: 2, x: 3, y: 4})
	if tui.mode != "" || tui.menu != nil {
		t.Fatalf("rail right press opened %q %+v", tui.mode, tui.menu)
	}
	// A wheel over a pane row must not focus it. SGR wheel-up is button 64.
	tui.handleMouse(mouseEvent{btn: 64, x: 3, y: 4})
	select {
	case m := <-got:
		t.Fatalf("rail wheel sent %+v", m)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestRailFold keeps panes folded by default and unfolds on the window row.
func TestRailFold(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railTestSnapshot(20)
	for i := range snap.All {
		snap.All[i].WindowUnfolded = false
	}
	items := railItems(snap)
	if len(items) != 2 || items[0].kind != railWorkspace || items[1].kind != railWindow {
		t.Fatalf("folded rail rows = %+v, want workspace and window", items)
	}
	if !items[1].folded || items[1].window != "w1" {
		t.Fatalf("folded window row = %+v", items[1])
	}
	var b bytes.Buffer
	drawRail(&b, snap)
	if strings.Contains(b.String(), "alpha") {
		t.Fatalf("folded rail drew a pane: %q", b.String())
	}
	if got := railWindowAt(snap, 3, 1); got != "w1" {
		t.Fatalf("window row hit = %q, want w1", got)
	}
	if got := railPaneAt(snap, 3, 1); got != "" {
		t.Fatalf("window row is a pane hit: %q", got)
	}

	// The thin rail is a glyph column, so it lists panes regardless of the fold
	// and has no workspace or window row to unfold.
	snap.Rail = collapsedRail
	items = railItems(snap)
	if len(items) != 2 || items[0].kind != railPane || items[1].kind != railPane {
		t.Fatalf("thin rail rows = %+v, want the two panes", items)
	}
	if items[0].pane != "w1p1" || items[1].pane != "w1p2" {
		t.Fatalf("thin rail panes = %q, %q, want w1p1, w1p2", items[0].pane, items[1].pane)
	}
	if got := railWindowAt(snap, 3, 0); got != "" {
		t.Fatalf("thin rail row folds a window: %q", got)
	}
}

// TestRailBranchLabel gives the window line the branch of its focused pane.
func TestRailBranchLabel(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railTestSnapshot(20)
	snap.All[0].Branch = "dm-rail"
	snap.All[1].Branch = "other"
	items := railItems(snap)
	if items[1].branch != "dm-rail" {
		t.Fatalf("window line branch = %q, want the focused pane's dm-rail",
			items[1].branch)
	}
	// The branch trails the label on the drawn row.
	if line := railRows(t, snap)[1]; !strings.Contains(line, "one  dm-rail") {
		t.Fatalf("window line = %q, want the name then the branch", line)
	}
	// Without a focused pane in the window, the first pane with a branch wins.
	snap.All[0].Focus = false
	items = railItems(snap)
	if items[1].branch != "dm-rail" {
		t.Fatalf("window branch without focus = %q", items[1].branch)
	}
	snap.All[1].Focus = true
	items = railItems(snap)
	if items[1].branch != "other" {
		t.Fatalf("window branch with w1p2 focused = %q", items[1].branch)
	}
}

// TestRailWindowWorktree draws a linked worktree's own folder first and its
// branch after it in dim grey. The worktree field still rides the row.
func TestRailWindowWorktree(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railOnePane("working", "pi", defaultRail, 60, 6)
	snap.All[0].Agent = true
	snap.All[0].Worktree = "lenient-w1"
	snap.All[0].Branch = "lazy-branch"
	snap.All[0].Cwd = "/work/lenient-w1"
	snap.All[0].Root = "/work/lenient-w1"
	snap.All[0].InRepo = true
	items := railItems(snap)
	if len(items) != 3 || items[1].folder != "lenient-w1" ||
		items[1].branch != "lazy-branch" {
		t.Fatalf("window line = %+v, want the folder and the branch", items)
	}
	if items[1].worktree != "lenient-w1" {
		t.Fatalf("window line lost the worktree: %+v", items[1])
	}
	var b bytes.Buffer
	drawRail(&b, snap)
	out := b.String()
	if !strings.Contains(out, railWindowFg.fg()+"lenient-w1") {
		t.Fatalf("folder is not in the window colour: %q", out)
	}
	if !strings.Contains(out, railDim.fg()+"lazy-branch") {
		t.Fatalf("branch is not in the dim colour: %q", out)
	}
	line := railRows(t, snap)[1]
	if !strings.HasPrefix(line, "  ▾ lenient-w1  lazy-branch") {
		t.Fatalf("window line = %q, want the folder then the branch", line)
	}
	if strings.Contains(line, "one") {
		t.Fatalf("window line fell back to the window name: %q", line)
	}

	// Without a branch the folder stands alone.
	snap.All[0].Branch = ""
	line = railRows(t, snap)[1]
	if strings.Contains(line, "lazy-branch") || !strings.Contains(line, "lenient-w1") {
		t.Fatalf("window line without a branch = %q", line)
	}

	// A window whose panes report no directory falls back to its name.
	snap.All[0].Cwd, snap.All[0].Root = "", ""
	if items := railItems(snap); items[1].folder != "" {
		t.Fatalf("window without a cwd = %+v, want no folder", items[1])
	}
	line = railRows(t, snap)[1]
	if !strings.Contains(line, "one") || strings.Contains(line, "lenient-w1") {
		t.Fatalf("window line without a folder = %q, want the window name", line)
	}
}

// railTree is two workspaces, each with one unfolded window. The first is a
// dirty linked worktree with stats and a message; the second is clean.
func railTree(rail int) *Snapshot {
	snap := &Snapshot{
		Cols: 100, Rows: 12, Rail: rail,
		Root: &Node{Pane: "w1p1"}, Focus: "w1p1",
	}
	snap.Panes = []PaneInfo{{ID: "w1p1", Status: "working"}}
	snap.All = []AllPane{
		{
			ID: "w1p1", Name: "pi", Status: "working", Agent: true,
			Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one",
			Focus: true, WorkspaceActive: true, WindowActive: true, WindowUnfolded: true,
			Worktree: "dm-rail-w1", Branch: "lenient-branch", InRepo: true,
			Dirty: true, Added: 12, Deleted: 4, Message: "working hard",
		},
		{
			ID: "w2p1", Workspace: "s2", WorkspaceName: "work", Window: "w2",
			WindowName: "two", WindowUnfolded: true, Branch: "main",
		},
	}
	return snap
}

// railRows draws a rail and returns the text of each row inside the rail width.
func railRows(t *testing.T, snap *Snapshot) []string {
	t.Helper()
	var b bytes.Buffer
	drawRail(&b, snap)
	vt := vt10x.New(vt10x.WithSize(snap.Cols, snap.Rows))
	if _, err := vt.Write(b.Bytes()); err != nil {
		t.Fatalf("draw rail: %v", err)
	}
	out := make([]string, snap.Rows-1)
	vt.Lock()
	for y := range out {
		var sb strings.Builder
		for x := 0; x < snap.Rail; x++ {
			g := vt.Cell(x, y)
			if g.Char == 0 {
				sb.WriteByte(' ')
				continue
			}
			sb.WriteRune(g.Char)
		}
		out[y] = strings.TrimRight(sb.String(), " ")
	}
	vt.Unlock()
	return out
}

// TestRailWindowStats draws the git tail of the window line: the pair, one side
// alone, clean, and the dirty dot without a line count.
func TestRailWindowStats(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	cases := []struct {
		name    string
		dirty   bool
		added   int
		deleted int
		want    []string
		absent  []string
	}{
		{"both", true, 12, 4, []string{"●", "+12", "-4"}, nil},
		{"added", false, 3, 0, []string{"+3"}, []string{"-3", "clean"}},
		{"deleted", false, 0, 5, []string{"-5"}, []string{"+5", "clean"}},
		{"clean", false, 0, 0, []string{"clean"}, []string{"+"}},
		{"dirty only", true, 0, 0, []string{"●"}, []string{"clean"}},
	}
	for _, c := range cases {
		snap := railTree(defaultRail)
		snap.All[0].Dirty, snap.All[0].Added, snap.All[0].Deleted = c.dirty, c.added, c.deleted
		line := railRows(t, snap)[1]
		for _, want := range c.want {
			if !strings.Contains(line, want) {
				t.Fatalf("%s: window line missing %q: %q", c.name, want, line)
			}
		}
		for _, absent := range c.absent {
			if strings.Contains(line, absent) {
				t.Fatalf("%s: window line shows %q: %q", c.name, absent, line)
			}
		}
	}

	// The pair keeps its colours and ends flush at the right edge.
	snap := railTree(defaultRail)
	var b bytes.Buffer
	drawRail(&b, snap)
	out := b.String()
	if !strings.Contains(out, railDiffAdd.fg()+"+12") ||
		!strings.Contains(out, railDiffDel.fg()+"-4") {
		t.Fatalf("stats colours missing: %q", out)
	}
	vt := vt10x.New(vt10x.WithSize(100, 12))
	if _, err := vt.Write(b.Bytes()); err != nil {
		t.Fatalf("draw tree rail: %v", err)
	}
	vt.Lock()
	last := vt.Cell(defaultRail-1, 1).Char
	vt.Unlock()
	if last != '4' {
		t.Fatalf("stats end at %q, want the last column", last)
	}
}

// TestRailWindowFolderTruncate gives the folder priority over the branch: a
// long branch goes first and leaves the whole folder, and only a folder that
// cannot fit on its own is cut with an ellipsis. The stats survive both.
func TestRailWindowFolderTruncate(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	// The folder fits alone at 34 columns; the branch does not fit beside it.
	snap := railTree(defaultRail)
	snap.All[0].Cwd = "/work/dm/long-folder-name"
	snap.All[0].Root = "/work/dm"
	snap.All[0].Worktree = ""
	snap.All[0].Branch = strings.Repeat("verylongbranch-", 2)
	line := railRows(t, snap)[1]
	if !strings.Contains(line, "long-folder-name") || strings.Contains(line, "…") {
		t.Fatalf("folder was cut before the branch dropped: %q", line)
	}
	if strings.Contains(line, "verylongbranch") {
		t.Fatalf("branch survived without room: %q", line)
	}
	if !strings.Contains(line, "● +12 -4") {
		t.Fatalf("stats dropped at %d cols: %q", defaultRail, line)
	}
	if n := cells(line); n != snap.Rail {
		t.Fatalf("window line is %d cells, want %d", n, snap.Rail)
	}

	// A folder wider than the rail is cut and ends with an ellipsis, and the
	// rail never writes past its width.
	snap.All[0].Cwd = "/work/dm/" + strings.Repeat("deep-folder-", 4)
	snap.All[0].Root = "/work/dm"
	line = railRows(t, snap)[1]
	if !strings.Contains(line, "…") {
		t.Fatalf("long folder was not elided: %q", line)
	}
	if !strings.HasPrefix(line, "  ▾ deep-folder-") {
		t.Fatalf("folder lost its head or indent: %q", line)
	}
	if strings.Contains(line, "verylongbranch") {
		t.Fatalf("branch survived the elision: %q", line)
	}
	if !strings.Contains(line, "● +12 -4") {
		t.Fatalf("stats dropped at %d cols: %q", defaultRail, line)
	}
	if n := cells(line); n != snap.Rail {
		t.Fatalf("window line is %d cells, want %d", n, snap.Rail)
	}
	var b bytes.Buffer
	drawRail(&b, snap)
	vt := vt10x.New(vt10x.WithSize(100, 12))
	if _, err := vt.Write(b.Bytes()); err != nil {
		t.Fatalf("draw truncated rail: %v", err)
	}
	vt.Lock()
	for y := 0; y < 12; y++ {
		for x := defaultRail; x < 100; x++ {
			if g := vt.Cell(x, y); g.Char != 0 && g.Char != ' ' {
				vt.Unlock()
				t.Fatalf("rail wrote %q at %d,%d, past width %d", g.Char, x, y, defaultRail)
			}
		}
	}
	vt.Unlock()
}

// TestRailWindowFolderLine draws the default 34 column window line with a
// folder, a branch and the stats all on it.
func TestRailWindowFolderLine(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	if defaultRail != 34 {
		t.Fatalf("default rail = %d, want 34", defaultRail)
	}
	snap := railTree(defaultRail)
	snap.All[0].Cwd, snap.All[0].Root = "/work/dm/rail", "/work/dm"
	snap.All[0].Worktree = ""
	snap.All[0].Branch = "dm-rail"
	line := railRows(t, snap)[1]
	if want := "  ▾ rail  dm-rail"; !strings.HasPrefix(line, want) {
		t.Fatalf("window line = %q, want it to start with %q", line, want)
	}
	if want := "● +12 -4"; !strings.HasSuffix(line, want) {
		t.Fatalf("window line = %q, want it to end with %q", line, want)
	}
	if n := cells(line); n != snap.Rail {
		t.Fatalf("window line is %d cells, want %d", n, snap.Rail)
	}
	var b bytes.Buffer
	drawRail(&b, snap)
	out := b.String()
	if !strings.Contains(out, railWindowFg.fg()+"rail") {
		t.Fatalf("folder is not in the window colour: %q", out)
	}
	if !strings.Contains(out, railDim.fg()+"dm-rail") {
		t.Fatalf("branch is not in the dim colour: %q", out)
	}
}

// TestRailWindowBranchEqualsFolder draws the branch once when it equals the
// folder: the folder is the label, so a dim second copy would repeat it.
func TestRailWindowBranchEqualsFolder(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	// One window, so the colour check below sees this line and no other.
	snap := railOnePane("working", "pi", defaultRail, 60, 6)
	snap.All[0].Cwd, snap.All[0].Root = "/work/main", "/work"
	snap.All[0].Branch = "main"
	snap.All[0].InRepo = true
	snap.All[0].Dirty, snap.All[0].Added = true, 3
	line := railRows(t, snap)[1]
	if want := "  ▾ main"; !strings.HasPrefix(line, want) {
		t.Fatalf("window line = %q, want it to start with %q", line, want)
	}
	if want := "● +3"; !strings.HasSuffix(line, want) {
		t.Fatalf("window line = %q, want it to end with %q", line, want)
	}
	if n := strings.Count(line, "main"); n != 1 {
		t.Fatalf("window line draws the folder %d times: %q", n, line)
	}
	if n := cells(line); n != snap.Rail {
		t.Fatalf("window line is %d cells, want %d", n, snap.Rail)
	}
	var b bytes.Buffer
	drawRail(&b, snap)
	out := b.String()
	if !strings.Contains(out, railWindowFg.fg()+"main") {
		t.Fatalf("folder is not in the window colour: %q", out)
	}
	if strings.Contains(out, railDim.fg()+"main") {
		t.Fatalf("equal branch is still drawn in the dim colour: %q", out)
	}
}

// TestRailWindowFolderNested draws a folder below the repository root: the
// relative path keeps its forward slash on the 34 column line.
func TestRailWindowFolderNested(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railTree(defaultRail)
	snap.All[0].Cwd, snap.All[0].Root = "/work/dm/a/b", "/work/dm"
	snap.All[0].Worktree = ""
	snap.All[0].Branch = "lean-branch"
	if items := railItems(snap); items[1].folder != "a/b" {
		t.Fatalf("window folder = %q, want a/b", items[1].folder)
	}
	line := railRows(t, snap)[1]
	if want := "  ▾ a/b  lean-branch"; !strings.HasPrefix(line, want) {
		t.Fatalf("window line = %q, want it to start with %q", line, want)
	}
	if want := "● +12 -4"; !strings.HasSuffix(line, want) {
		t.Fatalf("window line = %q, want it to end with %q", line, want)
	}
	if strings.Contains(line, `\`) {
		t.Fatalf("nested folder used a backslash: %q", line)
	}
	if n := cells(line); n != snap.Rail {
		t.Fatalf("window line is %d cells, want %d", n, snap.Rail)
	}
}

// TestRailWindowFolderFocus takes the window folder from the focused pane, the
// pane the git state comes from, then from the window's first pane.
func TestRailWindowFolderFocus(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railTestSnapshot(defaultRail)
	snap.All[0].Cwd, snap.All[0].Root = "/work/dm/one", "/work/dm"
	snap.All[1].Cwd, snap.All[1].Root = "/work/dm/two", "/work/dm"
	if items := railItems(snap); items[1].folder != "one" {
		t.Fatalf("window folder = %q, want the focused pane's one", items[1].folder)
	}
	// The second pane focused: the folder follows it and the line redraws.
	snap.All[0].Focus, snap.All[1].Focus = false, true
	if items := railItems(snap); items[1].folder != "two" {
		t.Fatalf("window folder after refocus = %q, want two", items[1].folder)
	}
	if line := railRows(t, snap)[1]; !strings.Contains(line, "two") {
		t.Fatalf("window line = %q, want the focused pane's folder", line)
	}
	// No pane focused: the window's first pane names the folder.
	snap.All[1].Focus = false
	if items := railItems(snap); items[1].folder != "one" {
		t.Fatalf("window folder without focus = %q, want the first pane's one", items[1].folder)
	}
}

// TestRailFolder gives the window line a folder for every pane: the path below
// the repository root, the root's own name at the top, the folder name outside
// git, and nothing for a pane with no directory.
func TestRailFolder(t *testing.T) {
	cases := []struct{ cwd, root, want string }{
		{"", "/work/dm", ""},
		{"/work/dm", "/work/dm", "dm"},
		{"/work/dm/rail", "/work/dm", "rail"},
		{"/work/dm/a/b", "/work/dm", "a/b"},
		// A sibling that shares a prefix with the root is not below it.
		{"/work/dm-other", "/work/dm", "dm-other"},
		{"/work/elsewhere", "/work/dm", "elsewhere"},
		{"/work/scratch", "", "scratch"},
	}
	for _, c := range cases {
		if got := railFolder(c.cwd, c.root); got != c.want {
			t.Fatalf("railFolder(%q, %q) = %q, want %q", c.cwd, c.root, got, c.want)
		}
	}

	// A pane outside git still shows its folder, and no stats ride along.
	t.Setenv("DM_NERD", "0")
	snap := railTree(defaultRail)
	snap.All[0].Cwd, snap.All[0].Root = "/home/op/scratch", ""
	snap.All[0].InRepo = false
	snap.All[0].Branch = ""
	snap.All[0].Worktree = ""
	if items := railItems(snap); items[1].folder != "scratch" {
		t.Fatalf("non-repo folder = %q, want scratch", items[1].folder)
	}
	line := railRows(t, snap)[1]
	if !strings.HasPrefix(line, "  ▾ scratch") {
		t.Fatalf("non-repo window line = %q, want the folder", line)
	}
	for _, absent := range []string{"clean", "+", "●"} {
		if strings.Contains(line, absent) {
			t.Fatalf("non-repo window line shows %q: %q", absent, line)
		}
	}
}

// TestRailTreeHit folds a window from its line and from its note line, and
// leaves a gap row and a workspace row inert.
func TestRailTreeHit(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railTree(30)
	// Rows: 0 workspace, 1 window, 2 note, 3 pane, 4 gap, 5 workspace, 6 window.
	if got := railWindowAt(snap, 3, 1); got != "w1" {
		t.Fatalf("window line hit = %q, want w1", got)
	}
	if got := railWindowAt(snap, 3, 2); got != "w1" {
		t.Fatalf("note line hit = %q, want w1", got)
	}
	if got := railWindowAt(snap, 3, 0); got != "" {
		t.Fatalf("workspace row folded %q", got)
	}
	if got := railWindowAt(snap, 3, 4); got != "" {
		t.Fatalf("gap row folded %q", got)
	}
	if got := railPaneAt(snap, 3, 1); got != "" {
		t.Fatalf("window line focused pane %q", got)
	}
	if got := railPaneAt(snap, 3, 2); got != "" {
		t.Fatalf("note line focused pane %q", got)
	}
	if got := railPaneAt(snap, 3, 3); got != "w1p1" {
		t.Fatalf("pane row hit = %q, want w1p1", got)
	}

	tui, got := clientTUI(t, nil)
	tui.snap = snap
	// One-based mouse rows: window 2, note 3, workspace 1, gap 5.
	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 2})
	if m := nextMsg(t, got); m.Action != "toggle-fold" || m.Target != "w1" {
		t.Fatalf("window line click sent %+v, want toggle-fold w1", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 3, y: 2})
	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 3})
	if m := nextMsg(t, got); m.Action != "toggle-fold" || m.Target != "w1" {
		t.Fatalf("note line click sent %+v, want toggle-fold w1", m)
	}
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 3, y: 3})
	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 1})
	noMsg(t, got)
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 3, y: 1})
	tui.handleMouse(mouseEvent{btn: 0, x: 3, y: 5})
	noMsg(t, got)
	tui.handleMouse(mouseEvent{btn: 0, release: true, x: 3, y: 5})
}

// TestRailWorkspaceAccent paints the bar in the workspace's own hue, keeps the
// pane count at the right edge, and leaves a gap row empty.
func TestRailWorkspaceAccent(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railTree(defaultRail)
	want := map[string]rgb{}
	for _, it := range railItems(snap) {
		if it.kind == railWorkspace {
			want[it.workspace] = it.accent
		}
	}
	if len(want) != 2 || want["s1"] == want["s2"] {
		t.Fatalf("workspace accents = %v, want two distinct hues", want)
	}
	var b bytes.Buffer
	drawRail(&b, snap)
	out := b.String()
	for ws, accent := range want {
		if !strings.Contains(out, accent.fg()+"▌") {
			t.Fatalf("workspace %q does not wear its accent %v: %q", ws, accent, out)
		}
	}
	vt := vt10x.New(vt10x.WithSize(100, 12))
	if _, err := vt.Write(b.Bytes()); err != nil {
		t.Fatalf("draw accent rail: %v", err)
	}
	vt.Lock()
	count := vt.Cell(defaultRail-1, 0).Char
	gapRow := true
	for x := 0; x < defaultRail; x++ {
		if g := vt.Cell(x, 4); g.Char != 0 && g.Char != ' ' {
			gapRow = false
		}
	}
	vt.Unlock()
	if count != '1' {
		t.Fatalf("workspace count at the right edge = %q, want 1", count)
	}
	if !gapRow {
		t.Fatal("gap row is not empty")
	}
}

// TestAllPaneInRepo carries the in-repo flag onto the wire row, so a directory
// git never saw cannot look clean.
func TestAllPaneInRepo(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	old := detectGit
	detectGit = func(cwd string) gitInfo {
		if cwd == "/work/dm" {
			return gitInfo{branch: "dm-rail", inRepo: true}
		}
		return gitInfo{}
	}
	defer func() { detectGit = old }()

	st := newState("inrepo")
	id := rootPane(st)
	st.Panes[id].Cwd = "/work/dm"
	s := railServer(st)
	deadline := time.Now().Add(2 * time.Second)
	for {
		snap := s.snapshotLocked()
		if len(snap.All) == 1 && snap.All[0].Branch == "dm-rail" {
			if !snap.All[0].InRepo {
				t.Fatalf("AllPane.InRepo = false, want true")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("git state never reached the snapshot: %+v", snap.All)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestRailAccentDistinct gives live workspaces distinct hues even when their
// ids hash to the same preferred slot.
func TestRailAccentDistinct(t *testing.T) {
	ids := []string{"s2", "s3", "s4", "s5"}
	if railAccent("s3") != railAccent("s4") || railAccent("s2") != railAccent("s5") {
		t.Fatalf("fixture ids do not collide: %v %v %v %v",
			railAccent("s2"), railAccent("s3"), railAccent("s4"), railAccent("s5"))
	}
	snap := &Snapshot{Cols: 100, Rows: 20, Rail: defaultRail}
	for _, id := range ids {
		snap.All = append(snap.All, AllPane{
			ID: id + "p1", Workspace: id, WorkspaceName: id,
			Window: id + "w1", WindowName: "one", WindowUnfolded: true,
		})
	}
	seen := map[rgb]string{}
	want := map[string]rgb{}
	for _, it := range railItems(snap) {
		if it.kind != railWorkspace {
			continue
		}
		if prev, ok := seen[it.accent]; ok {
			t.Fatalf("workspaces %q and %q share accent %v", prev, it.workspace, it.accent)
		}
		seen[it.accent] = it.workspace
		want[it.workspace] = it.accent
	}
	if len(want) != len(ids) || len(seen) != len(ids) {
		t.Fatalf("rail has %d workspace accents, want %d", len(seen), len(ids))
	}
	// drawRail paints the carried accent, not a fresh hash.
	var b bytes.Buffer
	drawRail(&b, snap)
	out := b.String()
	for ws, accent := range want {
		if !strings.Contains(out, accent.fg()+"▌") {
			t.Fatalf("workspace %q does not wear its accent %v: %q", ws, accent, out)
		}
	}
}

// TestRailNoStatsOutsideRepo keeps a directory git never saw free of a clean
// claim: the rail draws no stats tail there.
func TestRailNoStatsOutsideRepo(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railTree(defaultRail)
	snap.All[0].InRepo = false
	line := railRows(t, snap)[1]
	for _, absent := range []string{"clean", "+", "●"} {
		if strings.Contains(line, absent) {
			t.Fatalf("non-repo window line shows %q: %q", absent, line)
		}
	}
	if regexp.MustCompile(`-[0-9]`).MatchString(line) {
		t.Fatalf("non-repo window line shows a line count: %q", line)
	}
	// A repo with nothing to report says so.
	snap.All[0].InRepo = true
	snap.All[0].Dirty, snap.All[0].Added, snap.All[0].Deleted = false, 0, 0
	line = railRows(t, snap)[1]
	if !strings.Contains(line, "clean") {
		t.Fatalf("clean repo window line = %q", line)
	}
}

// TestRailStatsWholeAtClamp draws a long folder with the fullest pair at the
// 26 column clamp: the folder elides and the pair survives whole.
func TestRailStatsWholeAtClamp(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railTree(effectiveRail(80, defaultRail))
	if snap.Rail != 26 {
		t.Fatalf("clamp width = %d, want 26", snap.Rail)
	}
	snap.All[0].Cwd = "/work/" + strings.Repeat("a-folder-that-runs-long-", 3)
	snap.All[0].Root = "/work"
	snap.All[0].Worktree = ""
	snap.All[0].Branch = "a-branch-that-runs-long-too"
	snap.All[0].Dirty, snap.All[0].Added, snap.All[0].Deleted = true, 5000, 3000
	line := railRows(t, snap)[1]
	if !strings.Contains(line, "● +5000 -3000") {
		t.Fatalf("pair at the clamp = %q", line)
	}
	if !strings.Contains(line, "…") {
		t.Fatalf("long folder was not elided: %q", line)
	}
	if strings.Contains(line, "a-branch") {
		t.Fatalf("branch survived at the clamp: %q", line)
	}
	if n := cells(line); n > snap.Rail {
		t.Fatalf("window line %q is %d cells, want <= %d", line, n, snap.Rail)
	}
}

// TestRailStatsDropWhole gives up the stats before it draws half a pair.
func TestRailStatsDropWhole(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railTree(7)
	line := railRows(t, snap)[1]
	for _, absent := range []string{"+", "-", "●", "clean"} {
		if strings.Contains(line, absent) {
			t.Fatalf("rail %d draws %q of the stats: %q", snap.Rail, absent, line)
		}
	}
	// One column more fits the whole pair.
	if line := railRows(t, railTree(8))[1]; !strings.Contains(line, "● +12 -4") {
		t.Fatalf("rail 8 = %q, want the whole pair", line)
	}
}

// TestRailNoteElision cuts the message with an ellipsis inside the rail.
func TestRailNoteElision(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railTree(defaultRail)
	snap.All[0].Message = strings.Repeat("a very long status message ", 3)
	note := railRows(t, snap)[2]
	if !strings.Contains(note, "…") {
		t.Fatalf("note line was not elided: %q", note)
	}
	if n := cells(note); n > snap.Rail {
		t.Fatalf("note line %q is %d cells, want <= %d", note, n, snap.Rail)
	}
	if !strings.HasPrefix(note, "    a very") {
		t.Fatalf("note line lost its indent or text: %q", note)
	}
}

func TestRailToggle(t *testing.T) {
	tui, got := clientTUI(t, nil)
	// A client that has only seen a hidden rail asks for the default.
	tui.apply(Msg{Type: "snapshot", Snap: &Snapshot{Rail: 0, Cols: 100, Rows: 30}})
	tui.key('b')
	if m := nextMsg(t, got); m.Action != "set-rail" || m.Index != defaultRail {
		t.Fatalf("toggle from hidden sent %+v, want set-rail %d", m, defaultRail)
	}
	// A full rail collapses to the thin width, not to hidden.
	tui.apply(Msg{Type: "snapshot", Snap: &Snapshot{Rail: 30, RailWant: 30, Cols: 100, Rows: 30}})
	tui.key('b')
	if m := nextMsg(t, got); m.Action != "set-rail" || m.Index != collapsedRail {
		t.Fatalf("toggle from full sent %+v, want set-rail %d", m, collapsedRail)
	}
	// The thin rail hides, and the full width is still remembered.
	tui.apply(Msg{Type: "snapshot", Snap: &Snapshot{
		Rail:     collapsedRail,
		RailWant: collapsedRail, Cols: 100, Rows: 30,
	}})
	tui.key('b')
	if m := nextMsg(t, got); m.Action != "set-rail" || m.Index != 0 {
		t.Fatalf("toggle from thin sent %+v, want set-rail 0", m)
	}
	tui.apply(Msg{Type: "snapshot", Snap: &Snapshot{Rail: 0, Cols: 100, Rows: 30}})
	tui.key('b')
	if m := nextMsg(t, got); m.Action != "set-rail" || m.Index != 30 {
		t.Fatalf("toggle after hide sent %+v, want set-rail 30", m)
	}
	// A narrow terminal clamps the effective rail but the requested width
	// survives, so hiding and showing restores the request, not the clamp.
	tui.apply(Msg{Type: "snapshot", Snap: &Snapshot{
		Rail: effectiveRail(80, 30), RailWant: 30, Cols: 80, Rows: 30,
	}})
	tui.key('b')
	if m := nextMsg(t, got); m.Action != "set-rail" || m.Index != collapsedRail {
		t.Fatalf("narrow collapse sent %+v, want set-rail %d", m, collapsedRail)
	}
	tui.apply(Msg{Type: "snapshot", Snap: &Snapshot{
		Rail: 0, RailWant: 0, Cols: 80, Rows: 30,
	}})
	tui.key('b')
	if m := nextMsg(t, got); m.Action != "set-rail" || m.Index != 30 {
		t.Fatalf("narrow toggle on sent %+v, want set-rail 30", m)
	}
	if got := LoadDefault().Key("main", "rename-workspace"); got != "r" {
		t.Fatalf("rename-workspace key = %q, want r", got)
	}
	found := false
	for _, kv := range LoadDefault().HelpRows() {
		if kv[0] == "r" && kv[1] == "rename workspace" {
			found = true
		}
	}
	if !found {
		t.Fatal("keymap has no r binding")
	}
}

// TestRailCollapsed draws and hits the thin rail. It shows one state glyph per
// pane and no names, from the first row, inside the fixed collapsed width.
func TestRailCollapsed(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	snap := railOnePane("working", "alpha", collapsedRail, 40, 6)
	var b bytes.Buffer
	drawRail(&b, snap)
	out := b.String()
	if !strings.Contains(out, "●") {
		t.Fatalf("thin rail has no state glyph: %q", out)
	}
	for _, name := range []string{"main", "one", "alpha"} {
		if strings.Contains(out, name) {
			t.Fatalf("thin rail shows %q: %q", name, out)
		}
	}
	vt := vt10x.New(vt10x.WithSize(40, 6))
	if _, err := vt.Write(b.Bytes()); err != nil {
		t.Fatalf("draw thin rail: %v", err)
	}
	vt.Lock()
	glyphX := -1
	for x := 0; x < 40; x++ {
		g := vt.Cell(x, 0)
		if g.Char == '●' {
			glyphX = x
		}
	}
	vt.Unlock()
	if glyphX != 2 {
		t.Fatalf("state glyph at x=%d, want 2", glyphX)
	}
	if got := railPaneAt(snap, 2, 0); got != "w1p1" {
		t.Fatalf("thin rail hit = %q, want w1p1", got)
	}
	// Past the fixed width is pane area, not rail.
	if got := railPaneAt(snap, collapsedRail, 0); got != "" {
		t.Fatalf("hit past collapsed width = %q", got)
	}
}

func TestRailNeverWritesPastWidth(t *testing.T) {
	t.Setenv("DM_NERD", "0")
	if cells("A") != 1 || cells("日") != 2 || cells("\u0301") != 0 {
		t.Fatalf("cell widths: A=%d 日=%d combining=%d",
			cells("A"), cells("日"), cells("\u0301"))
	}
	cases := []struct {
		name  string
		label string
		wide  bool
	}{
		{"ascii name", strings.Repeat("long-name-", 8), false},
		{"wide name", strings.Repeat("日本語", 8), false},
		{"wide workspace header", "", true},
	}
	for _, c := range cases {
		snap := railOnePane("working", c.label, 6, 20, 6)
		if c.wide {
			snap.All[0].WorkspaceName = strings.Repeat("全角見出し", 6)
		}
		var b bytes.Buffer
		drawRail(&b, snap)
		vt := vt10x.New(vt10x.WithSize(20, 6))
		if _, err := vt.Write(b.Bytes()); err != nil {
			t.Fatalf("%s: draw rail: %v", c.name, err)
		}
		vt.Lock()
		for y := 0; y < 6; y++ {
			for x := 6; x < 20; x++ {
				if g := vt.Cell(x, y); g.Char != 0 && g.Char != ' ' {
					vt.Unlock()
					t.Fatalf("%s: rail wrote %q at %d,%d, past width 6", c.name, g.Char, x, y)
				}
			}
		}
		if g := vt.Cell(0, 0); g.Char == 0 {
			vt.Unlock()
			t.Fatalf("%s: rail drew nothing", c.name)
		}
		vt.Unlock()
	}
}

func TestBarNeverWritesPastWidth(t *testing.T) {
	snap := &Snapshot{
		Session:   strings.Repeat("日本語", 10),
		Workspace: strings.Repeat("作業", 6),
		Windows:   []string{"一"},
		Cols:      20,
		Rows:      5,
		Focus:     "w1p1",
		Panes: []PaneInfo{{
			ID: "w1p1", Name: strings.Repeat("日本語", 4),
			Status: "working",
		}},
	}
	var b bytes.Buffer
	drawBar(&b, snap, false, "")
	vt := vt10x.New(vt10x.WithSize(20, 5))
	if _, err := vt.Write(b.Bytes()); err != nil {
		t.Fatalf("draw bar: %v", err)
	}
	vt.Lock()
	defer vt.Unlock()
	// The bar is the last row. An overrun would wrap and scroll it up.
	painted := false
	for x := 0; x < 20; x++ {
		if g := vt.Cell(x, 4); g.Char != 0 && g.Char != ' ' {
			painted = true
		}
	}
	if !painted {
		t.Fatal("bar did not paint the last row")
	}
	for y := 0; y < 4; y++ {
		for x := 0; x < 20; x++ {
			if g := vt.Cell(x, y); g.Char != 0 && g.Char != ' ' {
				t.Fatalf("bar wrote past its row at %d,%d: %q", x, y, g.Char)
			}
		}
	}
}

// barTestSnap is the footer snapshot the segment tests share: a middle active
// window and an agent in focus, so every elision step has room to show.
func barTestSnap(cols int) *Snapshot {
	return &Snapshot{
		Session:      "dev",
		Workspace:    "workspace-a",
		Windows:      []string{"api-server", "web", "docs"},
		ActiveWindow: 1,
		Focus:        "w1p2",
		Panes: []PaneInfo{
			{ID: "w1p1", Name: "bash"},
			{ID: "w1p2", Name: "shell", Status: "idle"},
		},
		All: []AllPane{
			{ID: "w1p1", Agent: false},
			{ID: "w1p2", Agent: true, Status: "idle"},
		},
		Cols: cols,
		Rows: 24,
	}
}

// barTexts is the text of each span, in paint order.
func barTexts(segs []seg) []string {
	out := make([]string, 0, len(segs))
	for _, seg := range segs {
		out = append(out, seg.text)
	}
	return out
}

// barElideStage names the rung of the elision ladder barTestSnap has reached.
func barElideStage(segs []seg) string {
	var text strings.Builder
	chips := 0
	for _, seg := range segs {
		if seg.pin {
			continue
		}
		text.WriteString(seg.text)
		if len(seg.text) == 1 && seg.text[0] >= '1' && seg.text[0] <= '9' {
			chips++
		}
	}
	all := text.String()
	switch {
	case strings.Contains(all, " api-server"):
		return "full"
	case chips == 3:
		return "names"
	case !strings.Contains(all, "workspace-a"):
		if strings.Contains(all, "dev") {
			return "workspace"
		}
		if strings.Contains(all, " idle") {
			return "session"
		}
		if strings.Contains(all, " shell") {
			return "status"
		}
		return "name"
	}
	return "chips"
}

// TestBarSegmentOrder pins the footer's left block order, its colours, and the
// single gutter cell between segments, with the pinned right block last.
func TestBarSegmentOrder(t *testing.T) {
	snap := barTestSnap(80)
	glyph, colour := railGlyph("idle", nerdFonts())
	segs := barSegments(snap, true, "READY")

	// Groups, in paint order: session, workspace, one chip per window in index
	// order, the focus chip, then the pinned block.
	groups := [][]string{
		{"dev"},
		{"workspace-a"},
		{"1 api-server"},
		{"▸ ", "2 web"},
		{"3 docs"},
		{glyph, " w1p2", " shell", " idle"},
	}
	var want []string
	for k, group := range groups {
		if k > 0 {
			want = append(want, " ")
		}
		want = append(want, group...)
	}
	want = append(want, "PREFIX ", "READY")
	got := barTexts(segs)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("bar spans = %q, want %q", got, want)
	}

	if segs[0].fg != railText {
		t.Fatalf("session colour = %v, want railText", segs[0].fg)
	}
	if segs[2].fg != railHeader {
		t.Fatalf("workspace colour = %v, want railHeader", segs[2].fg)
	}
	if segs[4].fg != railDim || segs[4].bg != railBG || segs[9].fg != railDim {
		t.Fatalf("inactive chips = %+v %+v, want railDim on railBG", segs[4], segs[9])
	}

	// The active window reads as active: an accent mark, then bold text on the
	// focus fill.
	mark, chip := segs[6], segs[7]
	if mark.text != "▸ " || mark.fg != barAccent || mark.bg != railFocusBG {
		t.Fatalf("active mark = %+v, want the accent ▸ on railFocusBG", mark)
	}
	if chip.text != "2 web" || !chip.bold || chip.bg != railFocusBG {
		t.Fatalf("active chip = %+v, want bold on railFocusBG", chip)
	}

	// The state glyph is the only state colour on the bar.
	if segs[11].fg != colour || segs[11].bg != railFocusBG {
		t.Fatalf("focus glyph = %+v, want %v on railFocusBG", segs[11], colour)
	}
	if segs[12].fg != railText || !segs[12].bold {
		t.Fatalf("pane id = %+v, want bold railText", segs[12])
	}
	if segs[13].fg != railDim || segs[14].fg != railDim {
		t.Fatalf("pane name and status = %v %v, want railDim", segs[13].fg, segs[14].fg)
	}
	for _, k := range []int{11, 12, 13, 14} {
		if segs[k].bg != railFocusBG {
			t.Fatalf("focus span %d bg = %v, want railFocusBG", k, segs[k].bg)
		}
	}

	// One gutter cell between segments, and only between them: no group starts
	// or ends with one, and none sits inside the focus chip. The gutter is a
	// single cell of the bar ground, so a colour slip shows here.
	gutters := 0
	for k, seg := range segs {
		if seg.pin {
			continue
		}
		if seg.text == " " {
			gutters++
			if seg.bg != barGutter || seg.bg != railBG {
				t.Fatalf("gutter %d bg = %v, want the bar ground %v", k, seg.bg, barGutter)
			}
			continue
		}
		if seg.text == "" {
			t.Fatalf("span %d is empty", k)
		}
	}
	if gutters != len(groups)-1 {
		t.Fatalf("gutters = %d, want %d", gutters, len(groups)-1)
	}

	// The pinned block comes last, and nothing follows it.
	left := len(want) - 2
	for k := 0; k < left; k++ {
		if segs[k].pin {
			t.Fatalf("span %d is pinned inside the left block", k)
		}
	}
	if segs[left].text != "PREFIX " || !segs[left].pin {
		t.Fatalf("span %d = %+v, want the pinned PREFIX", left, segs[left])
	}
	if segs[len(segs)-1].text != "READY" || !segs[len(segs)-1].pin {
		t.Fatalf("last span = %+v, want the pinned info", segs[len(segs)-1])
	}
	if segCells(segs) > snap.Cols {
		t.Fatalf("bar spans %d cells, want <= %d", segCells(segs), snap.Cols)
	}
}

// TestBarElide walks the elision ladder: each width stops at the rung it needs
// and never paints past the column count.
func TestBarElide(t *testing.T) {
	glyph, _ := railGlyph("idle", nerdFonts())
	cases := []struct {
		cols int
		want string
		text []string
	}{
		{80, "full", []string{
			"dev", " ", "workspace-a", " ", "1 api-server",
			" ", "▸ ", "2 web", " ", "3 docs", " ", glyph, " w1p2", " shell",
			" idle",
		}},
		{50, "names", []string{
			"dev", " ", "workspace-a", " ", "1", " ", "▸ ",
			"2", " ", "3", " ", glyph, " w1p2", " shell", " idle",
		}},
		{34, "workspace", []string{
			"dev", " ", "workspa…", " ", "▸ ", "2", " ",
			glyph, " w1p2", " shell", " idle",
		}},
		{20, "status", []string{
			"…", " ", "…", " ", "▸ ", "2", " ", glyph,
			" w1p2", " shell",
		}},
		{40, "chips", nil},
		{30, "workspace", nil},
		{26, "session", nil},
		{16, "name", nil},
	}
	for _, c := range cases {
		segs := barSegments(barTestSnap(c.cols), false, "")
		if got := segCells(segs); got > c.cols {
			t.Fatalf("cols %d: spans paint %d cells: %q", c.cols, got, barTexts(segs))
		}
		if got := barElideStage(segs); got != c.want {
			t.Fatalf("cols %d: elision reached %q, want %q: %q",
				c.cols, got, c.want, barTexts(segs))
		}
		if c.text != nil {
			if got := strings.Join(barTexts(segs), "|"); got != strings.Join(c.text, "|") {
				t.Fatalf("cols %d: spans = %q, want %q", c.cols, got, c.text)
			}
		}
	}

	// The active window chip survives every width that keeps a chip at all.
	for _, cols := range []int{60, 45, 40, 33, 30, 25, 22, 18, 16, 12} {
		segs := barSegments(barTestSnap(cols), false, "")
		if segCells(segs) > cols {
			t.Fatalf("cols %d: spans paint %d cells", cols, segCells(segs))
		}
		text := strings.Join(barTexts(segs), "")
		if cols >= 30 && !strings.Contains(text, "2") {
			t.Fatalf("cols %d: active chip dropped: %q", cols, text)
		}
		// Only the shrinking rungs end a name with an ellipsis; a dropped
		// chip or window name leaves no mark.
		switch barElideStage(segs) {
		case "workspace", "session", "status", "name":
			if !strings.Contains(text, "…") {
				t.Fatalf("cols %d: elided line has no ellipsis: %q", cols, text)
			}
		}
	}

	// A zero or negative width paints nothing.
	for _, cols := range []int{0, -5} {
		if segs := barSegments(barTestSnap(cols), true, "READY"); len(segs) != 0 {
			t.Fatalf("cols %d: spans = %q, want none", cols, barTexts(segs))
		}
	}
	if segs := barSegments(nil, false, ""); len(segs) != 0 {
		t.Fatalf("nil snapshot: spans = %q, want none", barTexts(segs))
	}
}

// TestBarPinned keeps the pinned block whole while the left block gives way:
// both segments survive at 34 columns, and info survives alone at 20.
func TestBarPinned(t *testing.T) {
	snap := &Snapshot{
		Session:      "long-session-name",
		Workspace:    "a-very-long-workspace-name",
		Windows:      []string{"a-very-long-window-name-one", "two"},
		ActiveWindow: 0,
		Focus:        "w1p1",
		Panes:        []PaneInfo{{ID: "w1p1", Name: "a-long-pane-name", Status: "working"}},
		All:          []AllPane{{ID: "w1p1", Agent: true}},
		Cols:         34,
		Rows:         24,
	}
	info := "12 rows above, 4 hidden"

	segs := barSegments(snap, true, info)
	var pinned []seg
	for _, seg := range segs {
		if seg.pin {
			pinned = append(pinned, seg)
		}
	}
	if len(pinned) != 2 {
		t.Fatalf("pinned spans = %q, want PREFIX and info", barTexts(segs))
	}
	if pinned[0].text != "PREFIX " || pinned[1].text != info {
		t.Fatalf("pinned spans = %q %q, want PREFIX and the whole info",
			pinned[0].text, pinned[1].text)
	}
	if segCells(segs) > snap.Cols {
		t.Fatalf("spans paint %d cells, want <= %d", segCells(segs), snap.Cols)
	}

	// 20 columns hold the info alone: PREFIX goes whole, never the left block's
	// room, and the left block gives way first.
	snap.Cols = 20
	segs = barSegments(snap, true, info)
	if len(segs) != 1 || !segs[0].pin {
		t.Fatalf("20 cols: spans = %q, want the pinned info alone", barTexts(segs))
	}
	if strings.Contains(segs[0].text, "PREFIX") {
		t.Fatalf("20 cols: PREFIX survived: %q", segs[0].text)
	}
	if !strings.HasSuffix(segs[0].text, "…") {
		t.Fatalf("20 cols: clipped info has no ellipsis: %q", segs[0].text)
	}
	if got := segCells(segs); got != 20 {
		t.Fatalf("20 cols: spans paint %d cells, want 20", got)
	}

	// An unarmed bar carries no PREFIX at all.
	snap.Cols = 80
	segs = barSegments(snap, false, info)
	for _, seg := range segs {
		if seg.pin && seg.text == "PREFIX " {
			t.Fatalf("unarmed bar painted PREFIX: %q", barTexts(segs))
		}
	}
}

// TestBarPinnedGutter leaves room for the ground cell between the blocks, even
// when the left block would otherwise fill the row exactly.
func TestBarPinnedGutter(t *testing.T) {
	cases := []struct {
		cols  int
		armed bool
		info  string
	}{
		{34, true, ""},                  // the exact fit the review found
		{34, true, "12 rows above"},     // the right block takes most of it
		{50, true, "12 rows above"},     // long names fill the rest
		{40, false, "SCROLL w1p2 +1/9"}, // info alone pins the right block
		{20, true, "12 rows above"},     // info alone: no PREFIX, no left room
	}
	for _, c := range cases {
		segs := barSegments(barTestSnap(c.cols), c.armed, c.info)
		leftCells, rightCells, pinned := 0, 0, false
		for _, seg := range segs {
			if seg.pin {
				pinned = true
				rightCells += cells(seg.text)
				continue
			}
			leftCells += cells(seg.text)
		}
		if !pinned {
			t.Fatalf("cols %d: no pinned block", c.cols)
		}
		if got := segCells(segs); got > c.cols {
			t.Fatalf("cols %d: spans paint %d cells", c.cols, got)
		}
		if leftCells == 0 {
			continue // nothing to separate from
		}
		if leftCells+rightCells > c.cols-1 {
			t.Fatalf("cols %d: left %d + pinned %d leaves no gutter cell",
				c.cols, leftCells, rightCells)
		}
	}
}

// TestBarPinnedAnchored pins the paint: the right block ends on the last
// column, and one ground cell separates it from the left block.
func TestBarPinnedAnchored(t *testing.T) {
	info := "SCROLL w1p2 +1/9"
	for _, cols := range []int{60, 50, 34} {
		snap := barTestSnap(cols)
		segs := barSegments(snap, true, info)
		var pinnedText, leftText string
		for _, seg := range segs {
			if seg.pin {
				pinnedText += seg.text
				continue
			}
			leftText += seg.text
		}
		if pinnedText != "PREFIX "+info {
			t.Fatalf("cols %d: pinned spans = %q", cols, pinnedText)
		}
		start := cols - cells(pinnedText)

		var b bytes.Buffer
		drawBar(&b, snap, true, info)
		vt := vt10x.New(vt10x.WithSize(cols, snap.Rows))
		if _, err := vt.Write(b.Bytes()); err != nil {
			t.Fatalf("cols %d: draw bar: %v", cols, err)
		}
		vt.Lock()
		// One cell is one rune here: the pin test data carries no wide rune in
		// the pinned block, so the columns line up with the cells.
		want := []rune(pinnedText)
		for i, r := range want {
			if x := start + i; x >= cols {
				vt.Unlock()
				t.Fatalf("cols %d: pinned block starts at %d and runs past the row", cols, start)
			} else if cell := vt.Cell(x, snap.Rows-1); cell.Char != r {
				vt.Unlock()
				t.Fatalf("cols %d: cell %d = %q, want %q from the pinned block",
					cols, x, cell.Char, r)
			}
		}
		ground := vt10x.Color(barGutter.r<<16 | barGutter.g<<8 | barGutter.b)
		if start > 0 {
			cell := vt.Cell(start-1, snap.Rows-1)
			if cell.Char != ' ' || cell.BG != ground {
				vt.Unlock()
				t.Fatalf("cols %d: cell before the pinned block = %q bg %v, want a ground gutter",
					cols, cell.Char, cell.BG)
			}
		}
		if leftText == "" {
			vt.Unlock()
			t.Fatalf("cols %d: pinned only, want a left block", cols)
		}
		vt.Unlock()
	}
}

// TestBarWideRunes keeps wide runes whole and inside the column count.
func TestBarWideRunes(t *testing.T) {
	glyph, _ := railGlyph("working", nerdFonts())
	sources := []string{
		"日本語セッション", "作業ディレクトリ", "▸ ", "1 一二三",
		"2 四五六", " w1p1", " 日本語", " working", glyph,
	}
	snap := &Snapshot{
		Session:      "日本語セッション",
		Workspace:    "作業ディレクトリ",
		Windows:      []string{"一二三", "四五六"},
		ActiveWindow: 0,
		Focus:        "w1p1",
		Panes:        []PaneInfo{{ID: "w1p1", Name: "日本語", Status: "working"}},
		All:          []AllPane{{ID: "w1p1", Agent: true}},
		Rows:         24,
	}
	for _, cols := range []int{80, 41, 40, 21, 8, 3, 1} {
		snap.Cols = cols
		segs := barSegments(snap, false, "")
		if got := segCells(segs); got > cols {
			t.Fatalf("cols %d: spans paint %d cells", cols, got)
		}
		for _, seg := range segs {
			text := strings.TrimSuffix(seg.text, "…")
			if strings.ContainsRune(text, utf8.RuneError) {
				t.Fatalf("cols %d: split rune in %q", cols, seg.text)
			}
			if text == "" || text == " " {
				continue
			}
			// Every painted run is a whole-rune prefix of the text it came from.
			whole := false
			for _, src := range sources {
				if strings.HasPrefix(src, text) {
					whole = true
					break
				}
			}
			if !whole {
				t.Fatalf("cols %d: %q is not a whole rune prefix of a source",
					cols, seg.text)
			}
		}
	}
}

// TestBarFocusChip reads the focus chip from the focused pane: an agent shows
// its state glyph and colour on the focus fill, a shell shows the prompt marker.
func TestBarFocusChip(t *testing.T) {
	nerd := nerdFonts()
	snap := &Snapshot{
		Session: "s",
		Focus:   "w1p1",
		Panes:   []PaneInfo{{ID: "w1p1", Name: "agent", Status: "blocked"}},
		All:     []AllPane{{ID: "w1p1", Agent: true, Status: "blocked"}},
		Cols:    80,
		Rows:    24,
	}
	glyph, colour := railGlyph("blocked", nerd)
	segs := barSegments(snap, false, "")
	if len(segs) < 4 {
		t.Fatalf("spans = %q, want the focus chip", barTexts(segs))
	}
	chip := segs[2]
	if chip.text != glyph {
		t.Fatalf("blocked glyph = %q, want %q", chip.text, glyph)
	}
	if chip.fg != colour {
		t.Fatalf("blocked glyph colour = %v, want %v", chip.fg, colour)
	}
	if chip.bg != railFocusBG {
		t.Fatalf("blocked glyph bg = %v, want railFocusBG", chip.bg)
	}
	if colour == railDim {
		t.Fatal("blocked shares the shell colour")
	}
	if got := segs[len(segs)-1].text; got != " blocked" {
		t.Fatalf("status word = %q, want blocked", got)
	}
	// The painted bar carries the same glyph in that colour on the focus fill.
	var b bytes.Buffer
	drawBar(&b, snap, false, "")
	out := b.String()
	if !strings.Contains(out, colour.fg()+glyph) {
		t.Fatalf("bar does not paint %q in %v: %q", glyph, colour, out)
	}
	if !strings.Contains(out, railFocusBG.bg()+colour.fg()+glyph) {
		t.Fatalf("bar does not paint the glyph on railFocusBG: %q", out)
	}

	// A shell reports no state: it gets the prompt marker in dim text, and its
	// empty status reads as unknown.
	snap.Panes[0] = PaneInfo{ID: "w1p1", Name: "bash"}
	snap.All[0] = AllPane{ID: "w1p1", Agent: false}
	segs = barSegments(snap, false, "")
	if len(segs) < 4 {
		t.Fatalf("spans = %q, want the focus chip", barTexts(segs))
	}
	if segs[2].text != "▶" || segs[2].fg != railDim {
		t.Fatalf("shell glyph = %+v, want dim ▶", segs[2])
	}
	if got := segs[len(segs)-1].text; got != " unknown" {
		t.Fatalf("shell status word = %q, want unknown", got)
	}
}

// TestDividerHover shows the grab affordance under the pointer and clears it
// when the pointer leaves. Hover and press use the same padHit area.
// TestPadHitCrossesChildBorders pins the nested case: a full-width horizontal
// divider is grabbable even when the pointer sits over the vertical divider of
// the split below it.
func TestPadHitCrossesChildBorders(t *testing.T) {
	inner := &Node{
		Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*Node{{Pane: "b"}, {Pane: "c"}},
	}
	root := &Node{
		Dir: "col", Sizes: []float64{0.5, 0.5},
		Kids: []*Node{{Pane: "a"}, inner},
	}
	area := Rect{0, 0, 80, 24}
	boxes := childBoxRects(root, area)
	pad := padRect(root, boxes, 0)
	ib := childBoxRects(inner, boxes[1])
	ipad := padRect(inner, ib, 0)

	for _, x := range []int{ipad.X, ipad.X + 1} {
		split, first, second, axis, _, zone, ok := padHit(root, area, x, pad.Y+1)
		if !ok || split != root || first != "a" || (second != "b" && second != "c") ||
			axis != "col" || zone != pad {
			t.Fatalf("pad hit at x=%d = %v %q %q %q %v %v, want a + b/c on the root",
				x, split, first, second, axis, zone, ok)
		}
	}

	// The same cell starts a vertical drag over the root split's full width.
	snap := &Snapshot{Cols: 80, Rows: 25, Root: root}
	ds, ok := dragPress(snap, ipad.X, pad.Y+1)
	if !ok || ds.first != "a" || (ds.second != "b" && ds.second != "c") ||
		ds.axis != "col" || ds.rect != area || ds.zone != pad {
		t.Fatalf("drag press over a nested border = %+v ok=%v", ds, ok)
	}
}

// TestDividerGripsAlwaysVisible keeps a handle on every divider even before the
// pointer arrives, so the affordance does not depend on hover working.
func TestDividerGripsAlwaysVisible(t *testing.T) {
	inner := &Node{
		Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*Node{{Pane: "b"}, {Pane: "c"}},
	}
	root := &Node{
		Dir: "col", Sizes: []float64{0.5, 0.5},
		Kids: []*Node{{Pane: "a"}, inner},
	}
	snap := &Snapshot{Cols: 100, Rows: 30, Rail: 26, Root: root}
	var buf bytes.Buffer
	drawGrips(&buf, snap)
	vt := vt10x.New(vt10x.WithSize(100, 30))
	if _, err := vt.Write(buf.Bytes()); err != nil {
		t.Fatalf("draw grips: %v", err)
	}
	vt.Lock()
	defer vt.Unlock()
	// Horizontal divider midpoint: both cells.
	for _, y := range []int{13, 14} {
		if g := vt.Cell(63, y); g.Char != '•' {
			t.Fatalf("horizontal grip at 63,%d = %q", y, g.Char)
		}
	}
	// Vertical divider midpoint: both cells.
	for _, x := range []int{62, 63} {
		if g := vt.Cell(x, 21); g.Char != '•' {
			t.Fatalf("vertical grip at %d,21 = %q", x, g.Char)
		}
	}
	if n := strings.Count(buf.String(), "•"); n != 4 {
		t.Fatalf("grips = %d, want 4", n)
	}
}

func TestDividerHover(t *testing.T) {
	row := &Node{
		Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*Node{{Pane: "a"}, {Pane: "b"}},
	}
	snap := testSnapshot(row, 100, 30, nil)
	snap.Focus = "a"
	tui := &tui{snap: snap, vts: map[string]vt10x.Terminal{}}
	boxes := childBoxRects(row, paneArea(100, 30))
	zone := padRect(row, boxes, 0)
	// SGR button 35 is pointer motion with no button.
	tui.handleMouse(mouseEvent{btn: 35, x: zone.X + 1, y: zone.Y + 2})
	if tui.padZone != zone || tui.padAxis != "row" {
		t.Fatalf("hover zone = %v/%q, want %v/row", tui.padZone, tui.padAxis, zone)
	}
	if got := tui.barInfo(); got != "drag to resize" {
		t.Fatalf("hover hint = %q", got)
	}
	var b bytes.Buffer
	drawDivider(&b, tui.padZone, tui.padAxis)
	out := b.String()
	if !strings.Contains(out, "┃") || !strings.Contains(out, "\x1b[1;96m") {
		t.Fatalf("divider not highlighted: %q", out)
	}
	// The press lands on the same zone.
	if ds, ok := dragPress(snap, zone.X+1, zone.Y+2); !ok || ds.zone != zone {
		t.Fatalf("press zone = %v ok=%v, want %v", ds.zone, ok, zone)
	}
	// Leaving the divider clears it.
	tui.handleMouse(mouseEvent{btn: 35, x: 10, y: 10})
	if tui.padZone != (Rect{}) {
		t.Fatalf("hover survived leaving the divider: %v", tui.padZone)
	}
	if got := tui.barInfo(); got != "" {
		t.Fatalf("stale hover hint = %q", got)
	}
}

// TestHoverMotionNotForwarded keeps no-button motion from reaching a pane that
// asked only for button motion.
func TestHoverMotionNotForwarded(t *testing.T) {
	tui, got := clientTUI(t, map[string]bool{"a": true})
	p := tui.paneByID("a")
	tui.handleMouse(mouseEvent{
		btn: 35, x: p.Rect.X + 1, y: p.Rect.Y + 1,
		raw: []byte("\x1b[<35;2;2M"),
	})
	noMsg(t, got)
}

func TestRailDragUsesOffset(t *testing.T) {
	root := &Node{
		Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*Node{{Pane: "a"}, {Pane: "b"}},
	}
	snap := &Snapshot{Cols: 100, Rows: 30, Rail: 20, Root: root}
	area := paneAreaFor(100, 30, 20)
	boxes := childBoxRects(root, area)
	pad := padRect(root, boxes, 0)
	ds, ok := dragPress(snap, pad.X, pad.Y+1)
	if !ok || ds.first != "a" || ds.second != "b" || ds.rect != area {
		t.Fatalf("rail-aware divider press = %+v ok=%v, want rect %+v", ds, ok, area)
	}
	// The rail-zero pad position is inside pane a once the rail is counted.
	if _, ok := dragPress(snap, 49, pad.Y+1); ok {
		t.Fatal("drag started at the rail-zero pad, offset was dropped")
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
	row := &Node{
		Dir: "row", Kids: []*Node{{Pane: "a"}, {Pane: "b"}},
		Sizes: []float64{0.5, 0.5},
	}
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
	row := &Node{
		Dir: "row", Kids: []*Node{{Pane: "a"}, {Pane: "b"}},
		Sizes: []float64{0.5, 0.5},
	}
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
	row := &Node{
		Dir: "row", Kids: []*Node{{Pane: "a"}, {Pane: "b"}},
		Sizes: []float64{0.5, 0.5},
	}
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
	// Adjacent boxes share the divider: a's right border and b's left border
	// are adjacent cells, and each belongs to its own pane.
	if got := tui.paneAtBox(b.X-1, b.Y+1); got == nil || got.ID != "b" {
		t.Fatalf("left border did not hit b: %+v", got)
	}
	if got := tui.paneAtBox(b.X-2, b.Y+1); got == nil || got.ID != "a" {
		t.Fatalf("a's right border did not hit a: %+v", got)
	}
}

func TestMenuOutsideClickFocusesPane(t *testing.T) {
	row := &Node{
		Dir: "row", Kids: []*Node{{Pane: "a"}, {Pane: "b"}},
		Sizes: []float64{0.5, 0.5},
	}
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

// A wheel event is not a left press: it must not start a selection, a divider
// drag or a menu item.
func TestWheelIsNotAPress(t *testing.T) {
	row := &Node{
		Dir: "row", Kids: []*Node{{Pane: "a"}, {Pane: "b"}},
		Sizes: []float64{0.5, 0.5},
	}
	tui := &tui{snap: testSnapshot(row, 100, 30, nil), vts: map[string]vt10x.Terminal{}}
	tui.handleMouse(mouseEvent{btn: 64, x: 5, y: 5})
	if tui.press != nil || tui.dragging {
		t.Fatalf("wheel started a press or drag: press=%+v dragging=%v", tui.press, tui.dragging)
	}
	// On a pad, a wheel must not start a resize drag either.
	padX := tui.paneByID("b").Rect.X - 2
	tui.handleMouse(mouseEvent{btn: 64, x: padX + 1, y: 5})
	if tui.dragging {
		t.Fatal("wheel on a pad started a drag")
	}
	// With a menu open, a wheel must not run an item or close it.
	tui.openMenu(tui.paneByID("b"), 10, 5)
	ran := false
	tui.menu.items[0].run = func() { ran = true }
	tui.handleMouse(mouseEvent{btn: 64, x: tui.menu.x + 2, y: tui.menu.y + 2})
	if ran || tui.menu == nil {
		t.Fatalf("wheel over the menu: ran=%v menu=%+v", ran, tui.menu)
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

// clientTUI builds a client with a real snapshot and a conn over net.Pipe, and
// a reader that collects every message the client sends.
func clientTUI(t *testing.T, mouse map[string]bool) (*tui, chan Msg) {
	t.Helper()
	row := &Node{Pane: "a"}
	snap := testSnapshot(row, 100, 30, mouse)
	snap.Focus = "a"
	tui := &tui{snap: snap, vts: map[string]vt10x.Terminal{}}
	a, b := net.Pipe()
	t.Cleanup(func() {
		a.Close()
		b.Close()
	})
	tui.conn = &conn{Conn: a}
	got := make(chan Msg, 16)
	go func() {
		for {
			m, err := readMsg(b)
			if err != nil {
				close(got)
				return
			}
			got <- m
		}
	}()
	return tui, got
}

func pasteTUI(t *testing.T) (*tui, chan Msg) {
	t.Helper()
	return clientTUI(t, nil)
}

func nextMsg(t *testing.T, got chan Msg) Msg {
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
	return Msg{}
}

func noMsg(t *testing.T, got chan Msg) {
	t.Helper()
	select {
	case m := <-got:
		t.Fatalf("unexpected extra message: %+v", m)
	case <-time.After(50 * time.Millisecond):
	}
}

// A paste is one keys message with its newlines intact. Bytes after the end
// marker stay out of it and the prefix never arms: a client that forwarded the
// whole buffer, or armed on its contents, fails here.
func TestPasteIsOneKeysMessage(t *testing.T) {
	tui, got := pasteTUI(t)
	paste := "\x1b[200~line one\nline two\n\x1b[201~"
	tui.inbuf = append(tui.inbuf, []byte(paste+"tail")...)
	tui.processInput()
	m := nextMsg(t, got)
	if m.Type != "keys" || string(m.Keys) != paste {
		t.Fatalf("paste came through as %+v (%q)", m, m.Keys)
	}
	if tui.armed {
		t.Fatal("the paste armed the prefix")
	}
	m = nextMsg(t, got)
	if m.Type != "keys" || string(m.Keys) != "tail" {
		t.Fatalf("trailing bytes came through as %+v (%q)", m, m.Keys)
	}
	noMsg(t, got)
	if len(tui.inbuf) != 0 || tui.armed {
		t.Fatalf("paste left state: inbuf=%q armed=%v", tui.inbuf, tui.armed)
	}
}

// A ctrl+b inside a paste is text, not a prefix arm.
func TestPasteCtrlBDoesNotArm(t *testing.T) {
	tui, got := pasteTUI(t)
	paste := "\x1b[200~a\x02b\x1b[201~"
	tui.inbuf = append(tui.inbuf, []byte(paste)...)
	tui.processInput()
	m := nextMsg(t, got)
	if m.Type != "keys" || string(m.Keys) != paste {
		t.Fatalf("paste came through as %+v (%q)", m, m.Keys)
	}
	if tui.armed {
		t.Fatal("ctrl+b in a paste armed the prefix")
	}
	noMsg(t, got)
}

// A paste split across two reads is buffered and forwarded once, intact.
func TestPasteSpansReads(t *testing.T) {
	tui, got := pasteTUI(t)
	head := "\x1b[200~first\n"
	tail := "second\n\x1b[201~"
	tui.inbuf = append(tui.inbuf, []byte(head)...)
	tui.processInput()
	noMsg(t, got)
	if len(tui.inbuf) == 0 {
		t.Fatal("partial paste was not buffered")
	}
	tui.inbuf = append(tui.inbuf, []byte(tail)...)
	tui.processInput()
	m := nextMsg(t, got)
	if m.Type != "keys" || string(m.Keys) != head+tail {
		t.Fatalf("split paste came through as %+v (%q)", m, m.Keys)
	}
	noMsg(t, got)
}

// A paste closes an open menu and reaches the pane instead of a menu item.
func TestPasteClosesMenu(t *testing.T) {
	tui, got := pasteTUI(t)
	tui.openMenu(tui.paneByID("a"), 10, 5)
	if tui.menu == nil {
		t.Fatal("menu did not open")
	}
	paste := "\x1b[200~x\x1b[201~"
	tui.inbuf = append(tui.inbuf, []byte(paste)...)
	tui.processInput()
	m := nextMsg(t, got)
	if m.Type != "keys" || string(m.Keys) != paste {
		t.Fatalf("paste came through as %+v (%q)", m, m.Keys)
	}
	if tui.menu != nil || tui.mode != "" {
		t.Fatalf("paste left the menu open: %+v %q", tui.menu, tui.mode)
	}
}

// A read that splits right after the escape still yields one paste: the lone
// escape is held as a possible marker instead of going to the pane.
func TestPasteSplitAtLoneEscape(t *testing.T) {
	tui, got := pasteTUI(t)
	tui.inbuf = append(tui.inbuf, 0x1b)
	tui.processInput()
	noMsg(t, got)
	if tui.pasteDeadline.IsZero() {
		t.Fatal("lone escape was not held")
	}
	tui.inbuf = append(tui.inbuf, []byte("[200~a\nb\x1b[201~")...)
	tui.processInput()
	m := nextMsg(t, got)
	want := "\x1b[200~a\nb\x1b[201~"
	if m.Type != "keys" || string(m.Keys) != want {
		t.Fatalf("split paste came through as %+v (%q)", m, m.Keys)
	}
	noMsg(t, got)
}

// A real lone escape waits for the deadline and then reaches the pane.
func TestLoneEscapeFlushedAfterDeadline(t *testing.T) {
	tui, got := pasteTUI(t)
	start := time.Now()
	tui.inbuf = append(tui.inbuf, 0x1b)
	tui.processInput()
	noMsg(t, got)
	if tui.expirePaste(start) {
		t.Fatal("the escape expired before its deadline")
	}
	noMsg(t, got)
	if !tui.expirePaste(start.Add(escTimeout + time.Millisecond)) {
		t.Fatal("the escape did not expire after its deadline")
	}
	m := nextMsg(t, got)
	if m.Type != "keys" || string(m.Keys) != "\x1b" {
		t.Fatalf("lone escape came through as %+v (%q)", m, m.Keys)
	}
	noMsg(t, got)
}

// Bytes that cannot start a paste disprove the partial marker, so its deadline
// cannot flush a later, genuine paste early.
func TestPartialMarkerDisprovedClearsDeadline(t *testing.T) {
	tui, got := pasteTUI(t)
	tui.inbuf = append(tui.inbuf, []byte("\x1b[2")...)
	tui.processInput()
	noMsg(t, got)
	if tui.pasteDeadline.IsZero() {
		t.Fatal("partial marker was not held")
	}
	tui.inbuf = append(tui.inbuf, 'x')
	tui.processInput()
	m := nextMsg(t, got)
	if m.Type != "keys" || string(m.Keys) != "\x1b[2x" {
		t.Fatalf("disproved marker came through as %+v (%q)", m, m.Keys)
	}
	if !tui.pasteDeadline.IsZero() {
		t.Fatal("disproved marker kept its deadline")
	}
	if tui.expirePaste(time.Now().Add(time.Second)) {
		t.Fatal("a disproved marker was flushed again")
	}
}

// A start marker that never completes is not a paste. The stall path sends
// the bytes down the normal input path instead of holding them forever.
func TestPasteStallFallsBack(t *testing.T) {
	tui, got := pasteTUI(t)
	tui.inbuf = append(tui.inbuf, []byte("\x1b[")...)
	tui.processInput()
	noMsg(t, got)
	tui.staleInput()
	m := nextMsg(t, got)
	if m.Type != "keys" || string(m.Keys) != "\x1b[" {
		t.Fatalf("stalled marker came through as %+v (%q)", m, m.Keys)
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

// A backwards drag covers the same row-major range as the forward one.
func TestSelectionRangeNormalizes(t *testing.T) {
	s := &selection{pane: "a", anchor: cell{4, 2}, focus: cell{1, 0}}
	r := s.rangeIn(10)
	if r.lo != 1 || r.hi != 24 {
		t.Fatalf("backwards range = %+v, want lo 1 hi 24", r)
	}
	s = &selection{pane: "a", anchor: cell{1, 0}, focus: cell{4, 2}}
	if got := s.rangeIn(10); got.lo != 1 || got.hi != 24 {
		t.Fatalf("forwards range = %+v", got)
	}
	if got := (*selection)(nil).rangeIn(10); got != nil {
		t.Fatal("nil selection has a range")
	}
}

func TestExtractSelection(t *testing.T) {
	vt := testVT(t, 12, 3, "ab  cd\r\nsecond line\r\n")
	cases := []struct {
		name string
		a, b cell
		want string
	}{
		{"trailing spaces", cell{0, 0}, cell{6, 0}, "ab  cd"},
		{"multiple rows", cell{0, 0}, cell{5, 1}, "ab  cd\nsecond"},
		{"single cell", cell{1, 0}, cell{1, 0}, "b"},
		{"backwards drag", cell{5, 1}, cell{0, 0}, "ab  cd\nsecond"},
	}
	for _, c := range cases {
		if got := extractSelection(vt, 12, c.a, c.b); got != c.want {
			t.Fatalf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestOSC52Clipboard(t *testing.T) {
	t.Setenv("TMUX", "")
	seq := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("hi\nthere")) + "\x1b\\"
	if got := osc52("hi\nthere"); got != seq {
		t.Fatalf("osc52 = %q, want %q", got, seq)
	}
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	inner := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("hi")) + "\x1b\\"
	want := "\x1bPtmux;" + strings.ReplaceAll(inner, "\x1b", "\x1b\x1b") + "\x1b\\"
	if got := osc52("hi"); got != want {
		t.Fatalf("tmux osc52 = %q, want %q", got, want)
	}
}

// A click that never moves is not a selection: the app that claims the mouse
// gets the press and the release.
func TestClickForwardsToMouseApp(t *testing.T) {
	tui, got := clientTUI(t, map[string]bool{"a": true})
	p := tui.paneByID("a")
	tui.vts["a"] = testVT(t, p.Rect.W, p.Rect.H, "hello")
	press := mouseEvent{
		btn: 0, x: p.Rect.X + 2, y: p.Rect.Y + 1,
		raw: []byte("\x1b[<0;2;2M"),
	}
	release := mouseEvent{
		btn: 0, release: true, x: p.Rect.X + 2, y: p.Rect.Y + 1,
		raw: []byte("\x1b[<0;2;2m"),
	}
	tui.handleMouse(press)
	noMsg(t, got)
	if tui.press == nil {
		t.Fatal("press was not held")
	}
	tui.handleMouse(release)
	m := nextMsg(t, got)
	want := string(press.raw) + string(release.raw)
	if m.Type != "keys" || string(m.Keys) != want {
		t.Fatalf("click sent %+v (%q), want %q", m, m.Keys, want)
	}
	if tui.selText != nil || tui.press != nil {
		t.Fatal("click left selection state behind")
	}
	noMsg(t, got)
}

// A left press held on a pane must not survive a right press and a menu pick.
// Otherwise the later left release forwards a click the app never asked for.
func TestRightPressCancelsHeldClick(t *testing.T) {
	tui, got := clientTUI(t, map[string]bool{"a": true})
	p := tui.paneByID("a")
	x, y := p.Rect.X+2, p.Rect.Y+2
	tui.handleMouse(mouseEvent{btn: 0, x: x, y: y, raw: []byte("\x1b[<0;3;3M")})
	if tui.press == nil {
		t.Fatal("press was not held")
	}
	tui.handleMouse(mouseEvent{btn: 2, x: x, y: y, raw: []byte("\x1b[<2;3;3M")})
	if tui.menu == nil {
		t.Fatal("right press did not open the menu")
	}
	if tui.press != nil {
		t.Fatal("the held press survived the right press")
	}
	// Pick a menu row with the left button, then release the left button.
	tui.handleMouse(mouseEvent{
		btn: 0, x: tui.menu.x + 2, y: tui.menu.y + 2,
		raw: []byte("\x1b[<0;4;4M"),
	})
	tui.handleMouse(mouseEvent{
		btn: 0, release: true, x: x, y: y,
		raw: []byte("\x1b[<0;3;3m"),
	})
	deadline := time.After(100 * time.Millisecond)
	for {
		select {
		case m := <-got:
			if m.Type == "keys" {
				t.Fatalf("a phantom click reached the app: %q", m.Keys)
			}
		case <-deadline:
			return
		}
	}
}

// A snapshot that changes the starting pane's rect drops the selection: its
// cells belong to the old geometry.
func TestSelectionClearsOnResize(t *testing.T) {
	tui, _ := clientTUI(t, nil)
	p := tui.paneByID("a")
	tui.handleMouse(mouseEvent{
		btn: 0, x: p.Rect.X + 1, y: p.Rect.Y + 1,
		raw: []byte("\x1b[<0;1;1M"),
	})
	tui.handleMouse(mouseEvent{
		btn: 32, x: p.Rect.X + 4, y: p.Rect.Y + 1,
		raw: []byte("\x1b[<32;4;1M"),
	})
	if tui.selText == nil || !tui.selText.moved {
		t.Fatal("drag did not select")
	}
	// The same geometry keeps the selection.
	row := &Node{Pane: "a"}
	same := testSnapshot(row, 100, 30, nil)
	same.Focus = "a"
	tui.apply(Msg{Type: "snapshot", Snap: same})
	if tui.selText == nil {
		t.Fatal("an unchanged snapshot dropped the selection")
	}
	// A resized pane drops it.
	resized := testSnapshot(row, 90, 30, nil)
	resized.Focus = "a"
	tui.apply(Msg{Type: "snapshot", Snap: resized})
	if tui.selText != nil || tui.press != nil {
		t.Fatal("selection survived the resize")
	}
}

// A one-cell jitter is not a drag: the app that owns the mouse still gets the
// click.
func TestClickJitterStaysAClick(t *testing.T) {
	tui, got := clientTUI(t, map[string]bool{"a": true})
	p := tui.paneByID("a")
	tui.vts["a"] = testVT(t, p.Rect.W, p.Rect.H, "hello")
	press := mouseEvent{
		btn: 0, x: p.Rect.X + 2, y: p.Rect.Y + 2,
		raw: []byte("\x1b[<0;3;3M"),
	}
	release := mouseEvent{
		btn: 0, release: true, x: p.Rect.X + 3, y: p.Rect.Y + 2,
		raw: []byte("\x1b[<0;4;3m"),
	}
	motion := mouseEvent{
		btn: 32, x: p.Rect.X + 3, y: p.Rect.Y + 2,
		raw: []byte("\x1b[<32;4;3M"),
	}
	tui.handleMouse(press)
	tui.handleMouse(motion)
	tui.handleMouse(release)
	m := nextMsg(t, got)
	if m.Type != "keys" {
		t.Fatalf("jitter sent %+v", m)
	}
	// The held press, the jitter and the release all reach the app.
	if want := string(press.raw) + string(motion.raw) + string(release.raw); string(m.Keys) != want {
		t.Fatalf("jitter sent %q, want %q", m.Keys, want)
	}
	if tui.selText != nil || tui.press != nil {
		t.Fatal("jitter left selection state behind")
	}
	noMsg(t, got)
}

// Two cells is a drag: the range is selected and nothing reaches the app.
func TestTwoCellDragSelects(t *testing.T) {
	tui, got := clientTUI(t, map[string]bool{"a": true})
	p := tui.paneByID("a")
	tui.vts["a"] = testVT(t, p.Rect.W, p.Rect.H, "hello world")
	oscOut := captureStdout(t, func() {
		tui.handleMouse(mouseEvent{
			btn: 0, x: p.Rect.X + 1, y: p.Rect.Y + 1,
			raw: []byte("\x1b[<0;1;1M"),
		})
		tui.handleMouse(mouseEvent{
			btn: 32, x: p.Rect.X + 3, y: p.Rect.Y + 1,
			raw: []byte("\x1b[<32;3;1M"),
		})
		tui.handleMouse(mouseEvent{
			btn: 0, release: true, x: p.Rect.X + 3, y: p.Rect.Y + 1,
			raw: []byte("\x1b[<0;3;1m"),
		})
	})
	noMsg(t, got)
	if len(tui.toasts) != 1 || tui.toasts[0].text != "copied 3 chars" {
		t.Fatalf("toasts = %+v, want copied 3 chars", tui.toasts)
	}
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("hel")) + "\x1b\\"
	if !strings.Contains(oscOut, want) {
		t.Fatalf("clipboard payload missing from %q", oscOut)
	}
}

// A drag copies the range instead of forwarding anything to the app.
func TestDragCopiesSelection(t *testing.T) {
	tui, got := clientTUI(t, map[string]bool{"a": true})
	p := tui.paneByID("a")
	tui.vts["a"] = testVT(t, p.Rect.W, p.Rect.H, "hello world\r\nsecond line")
	oscOut := captureStdout(t, func() {
		tui.handleMouse(mouseEvent{
			btn: 0, x: p.Rect.X + 1, y: p.Rect.Y + 1,
			raw: []byte("\x1b[<0;1;1M"),
		})
		tui.handleMouse(mouseEvent{
			btn: 32, x: p.Rect.X + 5, y: p.Rect.Y + 1,
			raw: []byte("\x1b[<32;5;1M"),
		})
		tui.handleMouse(mouseEvent{
			btn: 0, release: true, x: p.Rect.X + 5, y: p.Rect.Y + 1,
			raw: []byte("\x1b[<0;5;1m"),
		})
	})
	noMsg(t, got)
	if tui.selText != nil || tui.press != nil {
		t.Fatal("drag left selection state behind")
	}
	if len(tui.toasts) != 1 || tui.toasts[0].text != "copied 5 chars" {
		t.Fatalf("toasts = %+v, want copied 5 chars", tui.toasts)
	}
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("hello")) + "\x1b\\"
	if !strings.Contains(oscOut, want) {
		t.Fatalf("clipboard payload missing from %q", oscOut)
	}
}

// A drag past the pane's edge stops at its content rect.
func TestSelectionClampsToStartingPane(t *testing.T) {
	tui, _ := clientTUI(t, nil)
	p := tui.paneByID("a")
	tui.handleMouse(mouseEvent{
		btn: 0, x: p.Rect.X + 1, y: p.Rect.Y + 1,
		raw: []byte("\x1b[<0;1;1M"),
	})
	tui.handleMouse(mouseEvent{
		btn: 32, x: p.Rect.X + p.Rect.W + 40,
		y: p.Rect.Y + p.Rect.H + 40, raw: []byte("\x1b[<32;99;99M"),
	})
	want := cell{x: p.Rect.W - 1, y: p.Rect.H - 1}
	if tui.selText == nil || tui.selText.focus != want {
		t.Fatalf("focus = %+v, want %+v", tui.selText, want)
	}
	if tui.selText.pane != "a" {
		t.Fatalf("selection left its pane: %q", tui.selText.pane)
	}
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

// The selected glyphs, and only them, are reversed. Parse the SGR state rather
// than count sequences: an off-by-one paints the cell after the selection.
func TestSelectionHighlight(t *testing.T) {
	vt := testVT(t, 10, 2, "abcdef\r\nghijkl")
	plain := &bytes.Buffer{}
	drawPane(plain, vt, Rect{0, 0, 10, 2})
	if strings.Contains(plain.String(), "\x1b[7m") {
		t.Fatalf("unselected paint has reverse video: %q", plain.String())
	}
	marked := &bytes.Buffer{}
	drawPaneFrom(marked, vt, Rect{0, 0, 10, 2}, 0, &selRange{lo: 1, hi: 3}, nil)
	rev := reversedCells(marked.String())
	for ch, want := range map[rune]bool{'a': false, 'b': true, 'c': true, 'd': true, 'e': false, 'g': false} {
		if rev[ch] != want {
			t.Fatalf("%q reversed=%v want %v (%q)", ch, rev[ch], want, marked.String())
		}
	}
}

// reversedCells walks an ANSI stream and reports each printed rune's reverse state.
func reversedCells(s string) map[rune]bool {
	out := map[rune]bool{}
	on := false
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) && s[j] == 'm' {
				for _, p := range strings.Split(s[i+2:j], ";") {
					switch p {
					case "7":
						on = true
					case "0", "27":
						on = false
					}
				}
			}
			i = j + 1
			continue
		}
		out[rune(s[i])] = on
		i++
	}
	return out
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

// TestBarPadsToWidth paints the whole last row on the bar ground, so no cell of
// the previous frame survives and the row reaches its final column.
func TestBarPadsToWidth(t *testing.T) {
	snap := &Snapshot{
		Session: "dev", Workspace: "work", Windows: []string{"one", "two"},
		ActiveWindow: 0, Focus: "w1p1",
		Panes: []PaneInfo{{ID: "w1p1", Name: "bash"}},
		All:   []AllPane{{ID: "w1p1", Agent: false}},
		Cols:  60, Rows: 6,
	}
	vt := vt10x.New(vt10x.WithSize(snap.Cols, snap.Rows))
	// A previous frame leaves stale glyphs on the bar row.
	if _, err := vt.Write([]byte(fmt.Sprintf("\x1b[%d;1H%s", snap.Rows, strings.Repeat("X", snap.Cols)))); err != nil {
		t.Fatalf("stale frame: %v", err)
	}
	var b bytes.Buffer
	drawBar(&b, snap, false, "")
	if _, err := vt.Write(b.Bytes()); err != nil {
		t.Fatalf("draw bar: %v", err)
	}
	vt.Lock()
	defer vt.Unlock()
	ground := vt10x.Color(barGutter.r<<16 | barGutter.g<<8 | barGutter.b)
	for x := 0; x < snap.Cols; x++ {
		cell := vt.Cell(x, snap.Rows-1)
		if cell.Char == 'X' || cell.Char == 0 {
			t.Fatalf("cell %d = %q, want a painted cell", x, cell.Char)
		}
	}
	// The row is padded on the bar ground up to its final column.
	last := vt.Cell(snap.Cols-1, snap.Rows-1)
	if last.Char != ' ' || last.BG != ground {
		t.Fatalf("last cell = %q bg %v, want a gutter space on %v",
			last.Char, last.BG, ground)
	}
	if out := b.String(); !strings.Contains(out, "\x1b["+strconv.Itoa(snap.Rows)+";1H") {
		t.Fatalf("bar does not start on row %d: %q", snap.Rows, out)
	}
}

// TestBarUnarmed paints no prefix flag.
func TestBarUnarmed(t *testing.T) {
	snap := barTestSnap(80)
	if out := barTexts(barSegments(snap, false, "12 rows above")); strings.Contains(strings.Join(out, ""), "PREFIX") {
		t.Fatalf("unarmed spans carry PREFIX: %q", out)
	}
	var b bytes.Buffer
	drawBar(&b, snap, false, "")
	if strings.Contains(b.String(), "PREFIX") {
		t.Fatalf("unarmed bar paints PREFIX: %q", b.String())
	}
	b.Reset()
	drawBar(&b, snap, true, "")
	if !strings.Contains(b.String(), "PREFIX") {
		t.Fatalf("armed bar has no PREFIX: %q", b.String())
	}
}

// TestBarContrast holds every footer foreground to WCAG AA on both grounds it
// paints: the gutter and the focus fill.
func TestBarContrast(t *testing.T) {
	foregrounds := map[string]rgb{
		"text":   railText,
		"header": railHeader,
		"dim":    railDim,
		"accent": barAccent,
	}
	for _, status := range []string{
		"working", "idle", "blocked", "done", "error", "unknown", "",
	} {
		fallback, colour := railGlyph(status, false)
		_, nerdColour := railGlyph(status, true)
		if fallback == "" || nerdColour != colour {
			t.Fatalf("state %q differs between glyph forms", status)
		}
		foregrounds["state "+status] = colour
	}
	if barGutter != railBG {
		t.Fatalf("bar ground = %v, want railBG", barGutter)
	}
	for name, fg := range foregrounds {
		for bgName, bg := range map[string]rgb{"gutter": barGutter, "focus": railFocusBG} {
			if r := contrast(fg, bg); r < 4.5 {
				t.Fatalf("%s on %s = %.2f:1, want >= 4.5:1", name, bgName, r)
			}
		}
	}
}

func TestSplitRatio(t *testing.T) {
	root := &Node{Pane: "a"}
	splitLeaf(root, "a", "right", "b", 0.25)
	rects := map[string]Rect{}
	layout(root, Rect{0, 0, 100, 10}, rects)
	if got := rects["a"].W; got < 22 || got > 24 {
		t.Fatalf("existing pane width = %d, want about 23", got)
	}
	if got := rects["b"].W; got < 72 || got > 74 {
		t.Fatalf("new pane width = %d, want about 73", got)
	}
	if rects["a"].W+rects["b"].W+4 != 100 {
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

// waitBudget bounds how long waitFor polls for a needle. A pane runs the
// operator's login shell, so its startup (zshrc, git status, plugin
// bootstraps) delays the first command: input typed before the shell reads it
// waits in the PTY, and the output lands late. Budget for a cold shell start.
const waitBudget = 30 * time.Second

func (h *harness) waitFor(session, pane, needle string) string {
	h.t.Helper()
	start := time.Now()
	deadline := start.Add(waitBudget)
	var last string
	for time.Now().Before(deadline) {
		out, err := h.try("read", pane, "-s", session)
		last = out
		if err == nil && strings.Contains(out, needle) {
			return out
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatalf("pane %s never showed %q within the %s wait budget (waited %s); last read:\n%s",
		pane, needle, waitBudget, time.Since(start).Round(time.Millisecond), last)
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

// A pane that spoke the status protocol stays an agent after state=clear. The
// mark persists, so a restart cannot demote it back to a shell either.
func TestAgentStaysAfterClear(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "sticky")
	pane := h.focus("sticky")

	agent := func() bool {
		tree := h.rpc("sticky", map[string]any{"action": "list"})
		snap := tree["snapshot"].(map[string]any)
		all, _ := snap["all"].([]any)
		if len(all) == 0 {
			t.Fatalf("snapshot has no all rows: %v", snap)
		}
		return all[0].(map[string]any)["agent"] == true
	}
	waitAgent := func(label string, want bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			if agent() == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: agent != %v", label, want)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	waitAgent("fresh shell", false)

	h.run("write-to", pane, "-s", "sticky", "--msg",
		`printf '\033]7501;state=working:app=pi\007'`, "--enter")
	waitAgent("reported working", true)
	app := func() string {
		tree := h.rpc("sticky", map[string]any{"action": "list"})
		snap := tree["snapshot"].(map[string]any)
		all := snap["all"].([]any)
		s, _ := all[0].(map[string]any)["app"].(string)
		return s
	}
	deadline := time.Now().Add(5 * time.Second)
	for app() != "pi" {
		if time.Now().After(deadline) {
			t.Fatalf("app never recorded")
		}
		time.Sleep(50 * time.Millisecond)
	}

	h.run("write-to", pane, "-s", "sticky", "--msg",
		`printf '\033]7501;state=clear\007'`, "--enter")
	deadline = time.Now().Add(5 * time.Second)
	for {
		out := h.run("status", pane, "-s", "sticky")
		if strings.Contains(out, "unknown") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("clear never arrived: %q", out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	waitAgent("after clear", true)

	if got := app(); got != "pi" {
		t.Fatalf("app after clear = %q, want pi", got)
	}
	ps := h.sessionState("sticky").Panes[pane]
	if ps == nil || !ps.Reported || ps.App != "pi" {
		t.Fatalf("agent mark not persisted: %+v", ps)
	}
}

// TestCloseLastPaneDropsWindow closes a window's only pane. The window goes
// away and the session falls back to its other workspace instead of refusing.
func TestCloseLastPaneDropsWindow(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "last")
	h.run("exec", `{"action":"new-workspace","name":"two"}`, "-s", "last")
	h.run("exec", `{"action":"close-pane"}`, "-s", "last")
	deadline := time.Now().Add(5 * time.Second)
	for {
		tree := h.rpc("last", map[string]any{"action": "list"})
		snap := tree["snapshot"].(map[string]any)
		panes := snap["panes"].([]any)
		if snap["workspace"] == "main" && len(panes) == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("close of the last pane left %v panes in %v", len(panes), snap["workspace"])
		}
		time.Sleep(50 * time.Millisecond)
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

// TestWindowAutoName labels a window after the folder it was created in.
func TestWindowAutoName(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "auto")
	cwd, _ := os.Getwd()
	want := folderName(cwd)
	windows := func() []any {
		tree := h.rpc("auto", map[string]any{"action": "list"})
		snap := tree["snapshot"].(map[string]any)
		return snap["windows"].([]any)
	}
	if got := windows(); len(got) != 1 || got[0] != want {
		t.Fatalf("initial windows = %v, want [%q]", got, want)
	}

	sub := filepath.Join(t.TempDir(), "proj-x")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	res := h.rpc("auto", map[string]any{"action": "new-window", "cwd": sub})
	if got := windows(); len(got) != 2 || got[1] != "proj-x" {
		t.Fatalf("new window name = %v, want proj-x", got)
	}
	ps := h.sessionState("auto").Panes[res["pane"].(string)]
	if ps == nil || ps.Cwd != sub {
		t.Fatalf("new window pane cwd = %+v, want %q", ps, sub)
	}

	// An explicit name still wins.
	h.rpc("auto", map[string]any{"action": "new-window", "name": "tests", "cwd": sub})
	if got := windows(); got[len(got)-1] != "tests" {
		t.Fatalf("explicit window name = %v", got)
	}

	// The TUI sends the focused pane as the target; the new window still lands
	// in that pane's folder and takes its name.
	pane := h.focus("auto")
	h.rpc("auto", map[string]any{"action": "new-window", "target": pane})
	if got := windows(); got[len(got)-1] != "proj-x" {
		t.Fatalf("new window from pane %s = %v, want proj-x", pane, got)
	}

	// A new workspace's first window takes the folder name too.
	h.rpc("auto", map[string]any{"action": "new-workspace", "name": "other", "cwd": sub})
	if got := windows(); len(got) != 1 || got[0] != "proj-x" {
		t.Fatalf("new workspace window = %v, want proj-x", got)
	}
}

// TestNewPaneFollowsShellCwd opens a new pane where its source pane's shell is
// now, not where that pane was born: dm launched from /a, the pane cd'd to /b,
// so a new window, workspace or split belongs to /b.
func TestNewPaneFollowsShellCwd(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "cwd")
	root := h.focus("cwd")

	now := filepath.Join(t.TempDir(), "now")
	if err := os.MkdirAll(now, 0o755); err != nil {
		t.Fatal(err)
	}
	h.run("write-to", root, "-s", "cwd", "--msg", "cd "+now, "--enter")
	deadline := time.Now().Add(waitBudget)
	for {
		ps := h.sessionState("cwd").Panes[root]
		if ps != nil && ps.Cwd == now {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane %s never recorded %q", root, now)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// A workspace takes the folder the active pane stands in, and so does the
	// window the workspace is born with.
	h.rpc("cwd", map[string]any{"action": "new-workspace", "name": "second"})
	st := h.sessionState("cwd")
	if len(st.Workspaces) != 2 {
		t.Fatalf("workspaces = %d, want 2", len(st.Workspaces))
	}
	ws := st.Workspaces[1]
	if id := ws.Windows[0].Focus; st.Panes[id] == nil || st.Panes[id].Cwd != now {
		t.Fatalf("new workspace pane cwd = %+v, want %q", st.Panes[id], now)
	}
	if name := ws.Windows[0].Name; name != "now" {
		t.Fatalf("new workspace window name = %q, want now", name)
	}

	// A split inherits the same folder, whichever workspace its target is in.
	res := h.rpc("cwd", map[string]any{"action": "split", "target": root, "dir": "right"})
	split := res["pane"].(string)
	if ps := h.sessionState("cwd").Panes[split]; ps == nil || ps.Cwd != now {
		t.Fatalf("split pane cwd = %+v, want %q", ps, now)
	}
}

// TestShellCwd reads a process's directory through /proc and survives a pid
// that has none.
func TestShellCwd(t *testing.T) {
	dir := t.TempDir()
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", "cd "+want+"; sleep 10")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	deadline := time.Now().Add(5 * time.Second)
	for shellCwd(cmd.Process.Pid) != want {
		if time.Now().After(deadline) {
			t.Fatalf("shellCwd(%d) never reached %q", cmd.Process.Pid, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := shellCwd(-1); got != "" {
		t.Fatalf("shellCwd(-1) = %q, want empty", got)
	}
}

func TestFolderName(t *testing.T) {
	cases := map[string]string{"/": "root", "/a/b/": "b", "/home/b": "b", "": "", ".": "root"}
	for dir, want := range cases {
		if got := folderName(dir); got != want {
			t.Fatalf("folderName(%q) = %q, want %q", dir, got, want)
		}
	}
}

// TestWindowNameMigration renames the old positional window labels on load.
func TestWindowNameMigration(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	st := newState("mig")
	w := st.Workspaces[0].Windows[0]
	w.Name = "1"
	w.Focus = "w1p1"
	st.Panes["w1p1"] = &PaneState{Cwd: "/code/dm"}
	if err := st.save(); err != nil {
		t.Fatal(err)
	}
	got, err := loadState("mig")
	if err != nil {
		t.Fatal(err)
	}
	if name := got.Workspaces[0].Windows[0].Name; name != "dm" {
		t.Fatalf("migrated window name = %q, want dm", name)
	}

	// A name that is not positional survives the load.
	got.Workspaces[0].Windows[0].Name = "tests"
	if err := got.save(); err != nil {
		t.Fatal(err)
	}
	again, err := loadState("mig")
	if err != nil {
		t.Fatal(err)
	}
	if name := again.Workspaces[0].Windows[0].Name; name != "tests" {
		t.Fatalf("named window = %q, want tests", name)
	}
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
	bgRes := h.rpc("hist", map[string]any{
		"action": "new-workspace", "name": "bg",
		"window": "t", "cwd": "/tmp", "nofocus": true,
	})
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
	// One line short of a full screen: the final newline scrolls twice, so the
	// blank row printed after TOP is the row immediately above the live screen.
	cmd := fmt.Sprintf(`sh -c "printf '\033[H\033[2J'; echo TOP; echo; `+
		`for i in \$(seq 1 %d); do echo TAIL-\$(printf %%03d \$i); done; `+
		`printf '\033[%d;1H'; sleep 15"`, rows-1, rows+2)
	h.run("write-to", root, "-s", "histg", "--msg", cmd, "--enter")
	h.waitFor("histg", root, fmt.Sprintf("TAIL-%03d", rows-1))
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
	row := &Node{
		Dir: "row", Kids: []*Node{{Pane: "a"}, {Pane: "b"}},
		Sizes: []float64{0.5, 0.5},
	}
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
	if tui.dragStart != ds.rect.X || tui.dragSpace != ds.rect.W {
		t.Fatalf("startDrag stored start=%d space=%d, want %d/%d",
			tui.dragStart, tui.dragSpace, ds.rect.X, ds.rect.W)
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
	if tui.dragStart != want.X || tui.dragSpace != want.W {
		t.Fatalf("same-axis startDrag stored start=%d space=%d, want %d/%d",
			tui.dragStart, tui.dragSpace, want.X, want.W)
	}

	// A divider still belongs to dm when a pane in the split's subtree claims
	// the mouse: the app's PTY covers its content, not the border cells.
	nested := &Node{Dir: "row", Kids: []*Node{
		{Pane: "a"},
		{Dir: "row", Kids: []*Node{{Pane: "b"}, {Pane: "c"}}},
	}}
	snap = testSnapshot(nested, 100, 30, map[string]bool{"c": true})
	if ds, ok := dragPress(snap, 49, 5); !ok || ds.first != "a" {
		t.Fatalf("claimed-mouse divider press = %+v ok=%v", ds, ok)
	}
	if ds, ok := dragPress(snap, 74, 5); !ok {
		t.Fatalf("claimed-mouse inner divider press = %+v ok=%v", ds, ok)
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

// sessionState reads the server's persisted state for a harness session.
func (h *harness) sessionState(name string) *State {
	h.t.Helper()
	b, err := os.ReadFile(filepath.Join(h.stateDir(), "sessions", name, "state.json"))
	if err != nil {
		h.t.Fatalf("state file: %v", err)
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		h.t.Fatalf("state json: %v", err)
	}
	return &st
}

// A nested drag resizes the outer split, not the inner one, and clamps.
func TestResizeSplit(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "rs")
	// This test measures the pane area exactly. Hide the rail, which a new
	// session otherwise asks for, so the wrapper stays rail-zero.
	h.run("exec", `{"action":"set-rail","index":0}`, "-s", "rs")
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
	h.run("exec", `{"action":"set-rail","index":0}`, "-s", "rs2")
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
	if got[q2].W+got[q3].W != cols-9 {
		t.Fatalf("outer right side width: %v", got)
	}
	h.run("exec", `{"action":"resize-split","target":"`+q1+`","other":"`+q2+`","ratio":1}`, "-s", "rs2")
	got = h.rects("rs2")
	if got[q1].W != cols-7 {
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

// keymapTestLoad parses a keymap from text for a test TUI.
func keymapTestLoad(t *testing.T, src string) *Config {
	t.Helper()
	cfg, err := parseConfig(src)
	if err != nil {
		t.Fatalf("parse test keymap: %v", err)
	}
	return cfg
}

// TestKeymapDispatchRemapsPrefixSplitAndDetach: a custom keymap moves the
// prefix, the split key and the detach key. The configured keys drive the
// actions and the old keys do not.
func TestKeymapDispatchRemapsPrefixSplitAndDetach(t *testing.T) {
	tui, got := clientTUI(t, nil)
	tui.keymap = keymapTestLoad(t, `
prefix "ctrl+a"

keymap "main" {
    "send-prefix" "prefix"
    "split-right" "S"
    "detach" "X"
}
`)
	// The configured prefix arms, and the configured split key splits right.
	feed(tui, "\x01")
	if !tui.armed {
		t.Fatal("the configured prefix did not arm")
	}
	noMsg(t, got)
	feed(tui, "S")
	m := nextMsg(t, got)
	if m.Action != "split" || m.Dir != "right" {
		t.Fatalf("the configured split key sent %+v", m)
	}
	if tui.armed {
		t.Fatal("the split action left the client armed")
	}

	// The old prefix byte is plain input now.
	feed(tui, "\x02")
	m = nextMsg(t, got)
	if m.Type != "keys" || string(m.Keys) != "\x02" {
		t.Fatalf("the old prefix byte sent %+v", m)
	}
	if tui.armed {
		t.Fatal("the old prefix byte armed the client")
	}

	// The old split key is unbound: it notifies and disarms.
	tui.arm()
	feed(tui, "|")
	if tui.armed {
		t.Fatal("the old split key stayed armed")
	}
	if n := len(tui.toasts); n == 0 || !strings.Contains(tui.toasts[n-1].text, "no binding") {
		t.Fatalf("the old split key raised %+v", tui.toasts)
	}
	noMsg(t, got)

	// The old detach key is unbound: it notifies and does not quit.
	tui.arm()
	feed(tui, "d")
	if tui.quit {
		t.Fatal("the old detach key still quit")
	}
	if tui.armed {
		t.Fatal("the old detach key stayed armed")
	}

	// The configured detach key quits.
	feed(tui, "\x01X")
	if !tui.quit {
		t.Fatal("the configured detach key did not quit")
	}
}

// TestKeymapDispatchMenuLabels: each menu row shows the configured main key for
// its action, or nothing when the action is unbound. The menu's own keys come
// from the menu mode.
func TestKeymapDispatchMenuLabels(t *testing.T) {
	tui := &tui{snap: testSnapshot(&Node{Pane: "a"}, 100, 30, nil)}
	tui.keymap = keymapTestLoad(t, `
prefix "ctrl+b"

keymap "main" {
    "split-right" "S"
    "close-pane" "Q"
    "detach" "Z"
}

keymap "menu" {
    "down" "J"
}
`)
	tui.openMenu(tui.paneByID("a"), 5, 3)
	if tui.menu == nil {
		t.Fatal("menu did not open")
	}
	want := map[string]string{
		"Split right":      "S",
		"Close pane":       "Q",
		"Detach":           "Z",
		"Split down":       "",
		"Split left":       "",
		"Split up":         "",
		"Focus":            "",
		"Scrollback":       "",
		"New window":       "",
		"New workspace":    "",
		"Rename workspace": "",
		"Switch session":   "",
	}
	for _, it := range tui.menu.items {
		key, ok := want[it.label]
		if !ok {
			t.Fatalf("unexpected menu row %q", it.label)
		}
		if it.key != key {
			t.Fatalf("%s key = %q, want %q", it.label, it.key, key)
		}
	}
	// The item keys are hints. The row walk is the menu mode's own binding.
	tui.inbuf = []byte("J")
	if !tui.menuKey() || tui.menu.sel != 1 {
		t.Fatalf("J did not move the selection: %+v", tui.menu)
	}
	// A main-mode key is not a menu key: an unbound one closes the menu.
	tui.inbuf = []byte("S")
	tui.menuKey()
	if tui.menu != nil {
		t.Fatal("an unbound menu key kept the menu open")
	}
}

// TestKeymapDispatchUnboundNotifiesAndDisarms: a key with no binding raises the
// notify toast and leaves the armed state.
func TestKeymapDispatchUnboundNotifiesAndDisarms(t *testing.T) {
	tui, got := clientTUI(t, nil)
	feed(tui, "\x02")
	if !tui.armed {
		t.Fatal("the prefix did not arm")
	}
	feed(tui, "\x01")
	if tui.armed {
		t.Fatal("an unbound key kept the client armed")
	}
	if n := len(tui.toasts); n == 0 || !strings.Contains(tui.toasts[n-1].text, "no binding for") {
		t.Fatalf("an unbound key raised %+v", tui.toasts)
	}
	noMsg(t, got)
}

// TestKeymapDispatchPartialEscapeWaitsAndLoneEscDismisses: a partial sequence
// waits for its remaining bytes, a stalled esc resolves to dismiss, and a
// complete arrow still runs its action.
func TestKeymapDispatchPartialEscapeWaitsAndLoneEscDismisses(t *testing.T) {
	tui, got := clientTUI(t, nil)
	feed(tui, "\x02")
	if !tui.armed {
		t.Fatal("the prefix did not arm")
	}
	start := time.Now()
	feed(tui, "\x1b")
	if !tui.armed {
		t.Fatal("the escape disarmed instead of waiting")
	}
	noMsg(t, got)
	// The escape is held as a possible paste start first. Once that deadline
	// passes it takes the normal path and waits as the head of an arrow.
	if !tui.expirePaste(start.Add(escTimeout + time.Millisecond)) {
		t.Fatal("the escape was not released from the paste check")
	}
	if !tui.pending || !tui.armed {
		t.Fatalf("a lone escape did not wait: pending=%v armed=%v", tui.pending, tui.armed)
	}
	if tui.expireArmed(tui.escAt) {
		t.Fatal("the escape expired before its deadline")
	}
	if !tui.expireArmed(tui.escAt.Add(escTimeout + time.Millisecond)) {
		t.Fatal("the escape did not expire")
	}
	if tui.armed || len(tui.inbuf) != 0 {
		t.Fatalf("stalled escape left state: armed=%v inbuf=%q", tui.armed, tui.inbuf)
	}

	// A complete sequence runs its action.
	feed(tui, "\x02")
	feed(tui, "\x1b[A")
	m := nextMsg(t, got)
	if m.Action != "focus-dir" || m.Dir != "up" {
		t.Fatalf("the arrow sent %+v", m)
	}
	if tui.armed {
		t.Fatal("the arrow left the client armed")
	}
}

// TestKeymapDispatchUnboundEscapeKeepsItsTail: an unbound escape sequence is one
// key. It disarms and notifies, and none of its bytes reach the pane.
func TestKeymapDispatchUnboundEscapeKeepsItsTail(t *testing.T) {
	tui, got := clientTUI(t, nil)
	for _, seq := range []string{"\x1bOP", "\x1b[1;5A", "\x1b[H"} {
		tui.arm()
		feed(tui, seq)
		if tui.armed {
			t.Fatalf("%q left the client armed", seq)
		}
		if len(tui.inbuf) != 0 {
			t.Fatalf("%q left %q in the buffer", seq, tui.inbuf)
		}
		noMsg(t, got)
	}
	if n := len(tui.toasts); n == 0 || !strings.Contains(tui.toasts[n-1].text, "no binding for") {
		t.Fatalf("the escape tail raised %+v", tui.toasts)
	}
}

// TestKeymapDispatchSplitPrefixStaysArmed: a multi-byte prefix that arrives in
// two reads arms and stays armed past the esc deadline.
func TestKeymapDispatchSplitPrefixStaysArmed(t *testing.T) {
	tui, got := clientTUI(t, nil)
	tui.keymap = keymapTestLoad(t, `
prefix "up"

keymap "main" {
    "split-right" "|"
}
`)
	// The head waits as a possible prefix, then the rest completes it.
	start := time.Now()
	feed(tui, "\x1b")
	tui.expirePaste(start.Add(escTimeout + time.Millisecond))
	if !tui.pending || tui.armed {
		t.Fatalf("prefix head: pending=%v armed=%v", tui.pending, tui.armed)
	}
	feed(tui, "[A")
	if !tui.armed || tui.pending {
		t.Fatalf("split prefix: armed=%v pending=%v", tui.armed, tui.pending)
	}
	if tui.expireArmed(tui.escAt.Add(escTimeout + time.Millisecond)) {
		t.Fatal("the armed sequence expired with the old prefix head")
	}
	if !tui.armed {
		t.Fatal("the client disarmed itself")
	}
	feed(tui, "|")
	m := nextMsg(t, got)
	if m.Action != "split" || m.Dir != "right" {
		t.Fatalf("the split key sent %+v", m)
	}
}

// TestKeymapDispatchSendPrefixSendsConfiguredBytes: the armed send-prefix action
// sends the configured prefix bytes, not a literal ctrl+b.
func TestKeymapDispatchSendPrefixSendsConfiguredBytes(t *testing.T) {
	tui, got := clientTUI(t, nil)
	tui.keymap = keymapTestLoad(t, `
prefix "ctrl+a"

keymap "main" {
    "send-prefix" "prefix"
}
`)
	feed(tui, "\x01")
	if !tui.armed {
		t.Fatal("the configured prefix did not arm")
	}
	feed(tui, "\x01")
	m := nextMsg(t, got)
	if m.Type != "keys" || string(m.Keys) != "\x01" {
		t.Fatalf("send-prefix sent %+v (%q)", m, m.Keys)
	}
	if tui.armed {
		t.Fatal("send-prefix left the client armed")
	}
	noMsg(t, got)
}

// TestReloadActionReadsConfigAndTheme: the reload action re-reads the default
// config path and its named theme. A changed keymap and theme replace the
// running ones and a toast confirms the reload.
func TestReloadActionReadsConfigAndTheme(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgDir := filepath.Join(dir, "dm")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	applyTestTheme(t, DefaultTheme())
	tui := &tui{keymap: LoadDefault()}

	path := filepath.Join(cfgDir, "config.kdl")
	if err := os.WriteFile(path, []byte(`theme "night"
keymap "main" {
    "split-right" "S"
    "reload" "ctrl+r"
}
`), 0600); err != nil {
		t.Fatal(err)
	}
	writeTheme(t, cfgDir, "night", `palette { "rail-bg" "#010203" }`)

	feed(tui, "\x02\x12")
	if tui.armed {
		t.Fatal("reload left the client armed")
	}
	if got := tui.keymap.Key("main", "split-right"); got != "S" {
		t.Fatalf("split-right key = %q, want S after reload", got)
	}
	if got := tui.keymap.Theme().Palette.RailBG; got != (rgb{1, 2, 3}) {
		t.Fatalf("keymap theme rail-bg = %v, want 1 2 3", got)
	}
	if railBG != (rgb{1, 2, 3}) {
		t.Fatalf("applied theme rail-bg = %v, want 1 2 3", railBG)
	}
	if !tui.dirty {
		t.Fatal("reload did not mark the frame dirty")
	}
	if n := len(tui.toasts); n == 0 || tui.toasts[n-1].text != "config reloaded" {
		t.Fatalf("reload toasts = %+v, want a config reloaded toast", tui.toasts)
	}
}

// TestReloadActionKeepsSettingsOnBadConfig: a malformed reload leaves the
// running keymap and theme in place and toasts the error.
func TestReloadActionKeepsSettingsOnBadConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgDir := filepath.Join(dir, "dm")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfgDir, "config.kdl")
	if err := os.WriteFile(path, []byte(`theme "old"
keymap "main" {
    "split-right" "S"
    "reload" "ctrl+r"
}
`), 0600); err != nil {
		t.Fatal(err)
	}
	writeTheme(t, cfgDir, "old", `palette { "rail-bg" "#040506" }`)
	oldCfg, err := LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	applyTestTheme(t, oldCfg.Theme())
	tui := &tui{keymap: oldCfg}

	if err := os.WriteFile(path, []byte(`bogus "x"`), 0600); err != nil {
		t.Fatal(err)
	}
	feed(tui, "\x02\x12")
	if tui.armed {
		t.Fatal("failed reload left the client armed")
	}
	if tui.keymap != oldCfg {
		t.Fatal("failed reload replaced the running keymap")
	}
	if got := tui.keymap.Key("main", "split-right"); got != "S" {
		t.Fatalf("split-right key = %q, want S after a failed reload", got)
	}
	if got := tui.keymap.Theme().Palette.RailBG; got != (rgb{4, 5, 6}) {
		t.Fatalf("keymap theme rail-bg = %v, want 4 5 6", got)
	}
	if railBG != (rgb{4, 5, 6}) {
		t.Fatalf("applied theme rail-bg = %v, want 4 5 6", railBG)
	}
	if n := len(tui.toasts); n == 0 || !strings.Contains(tui.toasts[n-1].text, "unknown node") {
		t.Fatalf("failed reload toasts = %+v, want the parse error", tui.toasts)
	}
}

// TestKeymapDispatchPartialEscapeInMenu: a menu arrow split across two reads
// waits for its remaining bytes and then moves the selection.
func TestKeymapDispatchPartialEscapeInMenu(t *testing.T) {
	tui := &tui{snap: testSnapshot(&Node{Pane: "a"}, 100, 30, nil), keymap: LoadDefault()}
	tui.openMenu(tui.paneByID("a"), 5, 3)
	if tui.menu == nil {
		t.Fatal("menu did not open")
	}
	tui.inbuf = []byte("\x1b")
	if tui.menuKey() {
		t.Fatal("a lone escape did not wait")
	}
	if !tui.pending || tui.menu == nil {
		t.Fatalf("partial escape: pending=%v menu=%+v", tui.pending, tui.menu)
	}
	tui.inbuf = append(tui.inbuf, '[', 'B')
	if !tui.menuKey() {
		t.Fatal("the completed arrow still waited")
	}
	if tui.menu == nil || tui.menu.sel != 1 || tui.pending {
		t.Fatalf("the arrow did not move: %+v pending=%v", tui.menu, tui.pending)
	}
}

// TestKeymapDispatchSwitcherLeftRightAreNoOps: left, right and shift-tab have no
// meaning in the switcher list. They neither close it nor type into the query,
// and an unbound escape sequence is not text either.
func TestKeymapDispatchSwitcherLeftRightAreNoOps(t *testing.T) {
	tui, got := switcherTUI(t)
	feed(tui, "\x02a")
	if tui.sw == nil {
		t.Fatal("the switcher did not open")
	}
	for _, seq := range []string{"\x1b[D", "\x1b[C", "\x1b[Z", "\x1bOP"} {
		feed(tui, seq)
		if tui.sw == nil {
			t.Fatalf("%q closed the switcher", seq)
		}
		if q := tui.sw.q(); q != "" {
			t.Fatalf("%q typed %q into the query", seq, q)
		}
	}
	// Plain text still reaches the query, and the arrows still move.
	feed(tui, "beta")
	if q := tui.sw.q(); q != "beta" {
		t.Fatalf("query = %q, want beta", q)
	}
	feed(tui, "\x1b[A")
	if tui.sw == nil {
		t.Fatal("the up arrow closed the switcher")
	}
	noMsg(t, got)
}

// TestHelpOpensAndCloses: the help action adds the keymap float and the help
// mode, and esc takes both down again. A second press toggles it closed.
func TestHelpOpensAndCloses(t *testing.T) {
	tui, _ := clientTUI(t, nil)
	tui.runAction("help")
	if tui.keys == nil {
		t.Fatal("the help action did not open the keymap view")
	}
	if tui.mode != "help" {
		t.Fatalf("mode = %q, want help", tui.mode)
	}
	if len(tui.floats) != 1 || tui.floats[0] != tui.keys.float {
		t.Fatalf("floats = %+v, want the keymap float", tui.floats)
	}
	if tui.armed {
		t.Fatal("the help action left the client armed")
	}
	// A lone esc is the close key. It waits as the head of an escape sequence,
	// then closes once its deadline passes.
	feed(tui, "\x1b")
	if tui.keys == nil || !tui.pending {
		t.Fatalf("a lone esc did not wait: keys=%v pending=%v", tui.keys, tui.pending)
	}
	if !tui.expireMode(tui.escAt.Add(escTimeout + time.Millisecond)) {
		t.Fatal("the stalled escape did not close the view")
	}
	if tui.keys != nil || tui.mode != "" || len(tui.floats) != 0 || len(tui.inbuf) != 0 {
		t.Fatalf("esc did not close the view: keys=%v mode=%q floats=%d inbuf=%q",
			tui.keys, tui.mode, len(tui.floats), tui.inbuf)
	}

	// The default ? opens the view, and ? closes it again.
	feed(tui, "\x02?")
	if tui.keys == nil || tui.mode != "help" {
		t.Fatalf("prefix ? did not open the view: keys=%v mode=%q", tui.keys, tui.mode)
	}
	feed(tui, "?")
	if tui.keys != nil || tui.mode != "" {
		t.Fatalf("? did not close the view: keys=%v mode=%q", tui.keys, tui.mode)
	}
}

// zoomFrameTUI is a tui over a row split with a rail, each pane's screen
// carrying a mark, so a frame's bytes say which panes it painted.
type zoomFrameTUI struct {
	tui  *tui
	snap *Snapshot
	out  *bytes.Buffer
}

func newZoomFrameTUI(t *testing.T) *zoomFrameTUI {
	t.Helper()
	const cols, rows, rail = 100, 20, 20
	root := &Node{Dir: "row", Sizes: []float64{0.5, 0.5},
		Kids: []*Node{{Pane: "a"}, {Pane: "b"}}}
	snap := testSnapshot(root, cols, rows, nil)
	snap.Rail = rail
	snap.Focus = "b"
	snap.Session = "one"
	snap.Workspace = "s1"
	snap.Windows = []string{"main"}
	for i := range snap.Panes {
		snap.Panes[i].Name = snap.Panes[i].ID + "-name"
	}
	relayout(snap)
	out := &bytes.Buffer{}
	tui := &tui{snap: snap, vts: map[string]vt10x.Terminal{}, out: out}
	for _, p := range snap.Panes {
		vt := vt10x.New(vt10x.WithSize(p.Rect.W, p.Rect.H))
		tui.vts[p.ID] = vt
		vt.Write([]byte("\x1b[?25l\x1b[1;1H" + strings.ToUpper(p.ID) + "-MARK"))
	}
	return &zoomFrameTUI{tui: tui, snap: snap, out: out}
}

// frame draws one frame and returns its bytes.
func (f *zoomFrameTUI) frame() string {
	f.out.Reset()
	f.tui.draw()
	return f.out.String()
}

// zoom sets or clears the snapshot's zoom the way the server does: the zoomed
// pane takes the single-pane box and its siblings keep their layout rects.
func (f *zoomFrameTUI) zoom(id string) {
	f.snap.Zoom = id
	relayout(f.snap)
	if id != "" {
		area := paneAreaFor(f.snap.Cols, f.snap.Rows, f.snap.Rail)
		for i := range f.snap.Panes {
			if f.snap.Panes[i].ID == id {
				f.snap.Panes[i].Rect = insetBox(area)
			}
		}
	}
}

// screenGlyphs counts one glyph as a terminal ends up.
func screenGlyphs(vt vt10x.Terminal, ch rune) int {
	cols, rows := vt.Size()
	return screenGlyphsIn(vt, ch, Rect{0, 0, cols, rows})
}

// screenCorners counts the box corners on a screen, so a frame that paints one
// box and a frame that paints two are told apart.
func screenCorners(vt vt10x.Terminal) int {
	n := 0
	for _, ch := range []rune{'┌', '┐', '└', '┘'} {
		n += screenGlyphs(vt, ch)
	}
	return n
}

// screenGlyphsIn counts one glyph inside a rectangle as the terminal ends up.
func screenGlyphsIn(vt vt10x.Terminal, ch rune, r Rect) int {
	vt.Lock()
	defer vt.Unlock()
	n := 0
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			if vt.Cell(x, y).Char == ch {
				n++
			}
		}
	}
	return n
}

// TestZoomFrameFilter pins the draw filter: the zoomed frame paints the zoomed
// pane alone, with no sibling box and no grip, and clearing the zoom brings the
// unzoomed picture back byte for byte.
func TestZoomFrameFilter(t *testing.T) {
	f := newZoomFrameTUI(t)
	first := f.frame()
	if !strings.Contains(first, "A-MARK") || !strings.Contains(first, "B-MARK") {
		t.Fatalf("the split frame painted one pane: %q", first)
	}
	area := paneAreaFor(f.snap.Cols, f.snap.Rows, f.snap.Rail)
	scr := vt10x.New(vt10x.WithSize(f.snap.Cols, f.snap.Rows))
	if _, err := scr.Write([]byte(first)); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	if got := screenCorners(scr); got != 8 {
		t.Fatalf("split screen carries %d box corners, want two boxes' 8", got)
	}
	if got := screenGlyphsIn(scr, '•', area); got != 2 {
		t.Fatalf("split screen carries %d grips in the pane area, want 2", got)
	}

	f.zoom("b")
	zoomed := f.frame()
	if strings.Contains(zoomed, "A-MARK") {
		t.Fatalf("the zoomed frame painted the hidden sibling: %q", zoomed)
	}
	if !strings.Contains(zoomed, "B-MARK") {
		t.Fatalf("the zoomed frame dropped the zoomed pane: %q", zoomed)
	}
	if _, err := scr.Write([]byte(zoomed)); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	if got := screenCorners(scr); got != 4 {
		t.Fatalf("zoomed screen carries %d box corners, want the zoomed pane's 4", got)
	}
	if got := screenGlyphsIn(scr, '•', area); got != 0 {
		t.Fatalf("zoomed screen carries %d grips in the pane area, want none", got)
	}
	// The hidden sibling's own sides run down the zoomed pane's middle. Its
	// top and bottom rows land under the zoomed box's borders, so the sides are
	// the cells that tell a filtered frame from an unfiltered one.
	inside := insetBox(area)
	inside.X, inside.Y = inside.X+1, inside.Y+1
	inside.W, inside.H = inside.W-2, inside.H-2
	if got := screenGlyphsIn(scr, '│', inside); got != 0 {
		t.Fatalf("zoomed screen carries %d sibling borders inside the pane, want none", got)
	}
	scr.Lock()
	corner := scr.Cell(area.X, area.Y).Char
	scr.Unlock()
	if corner != '┌' {
		t.Fatalf("zoomed box corner = %q, want it at the pane area %d,%d", corner, area.X, area.Y)
	}

	f.zoom("")
	if back := f.frame(); back != first {
		t.Fatalf("the unzoomed frame came back changed:\n got %q\nwant %q", back, first)
	}
}

// TestZoomHitsFollowTheZoomedPane pins the hit filter: a point in a hidden
// sibling's old rectangle lands on the zoomed pane, and a point outside the
// pane area keeps its present answer.
func TestZoomHitsFollowTheZoomedPane(t *testing.T) {
	f := newZoomFrameTUI(t)
	a := f.tui.paneByID("a").Rect
	x, y := a.X+2, a.Y+2
	f.zoom("b")
	for _, box := range []bool{true, false} {
		var got *PaneInfo
		if box {
			got = f.tui.paneAtBox(x, y)
		} else {
			got = f.tui.paneAt(x, y)
		}
		if got == nil || got.ID != "b" {
			t.Fatalf("box=%v: a hidden sibling's cell hit %+v, want the zoomed b", box, got)
		}
	}
	// The rail column and the bar row are outside the pane area.
	if got := f.tui.paneAtBox(1, 1); got != nil {
		t.Fatalf("a rail cell hit %+v while zoomed", got)
	}
	if got := f.tui.paneAt(f.snap.Cols-1, f.snap.Rows-1); got != nil {
		t.Fatalf("a bar cell hit %+v while zoomed", got)
	}
	// Unzoomed, the same point is the sibling's again.
	f.zoom("")
	if got := f.tui.paneAtBox(x, y); got == nil || got.ID != "a" {
		t.Fatalf("unzoomed cell hit %+v, want a", got)
	}
}

// TestZoomDragPressGuard pins the divider guard: the same cell starts a drag
// while unzoomed and cannot start one while zoomed.
func TestZoomDragPressGuard(t *testing.T) {
	f := newZoomFrameTUI(t)
	root := f.snap.Root
	boxes := childBoxRects(root, paneAreaFor(f.snap.Cols, f.snap.Rows, f.snap.Rail))
	pad := padRect(root, boxes, 0)
	x, y := pad.X, pad.Y+2
	if ds, ok := dragPress(f.snap, x, y); !ok || ds.first != "a" || ds.second != "b" {
		t.Fatalf("the split divider took no press: %+v ok=%v", ds, ok)
	}

	f.zoom("b")
	if ds, ok := dragPress(f.snap, x, y); ok {
		t.Fatalf("a zoomed frame started a drag: %+v", ds)
	}
	tui := &tui{snap: f.snap, vts: map[string]vt10x.Terminal{}}
	tui.handleMouse(mouseEvent{btn: 0, x: x, y: y})
	if tui.dragging {
		t.Fatal("a press on a hidden split's divider started a drag")
	}
	// The hover goes through the same guard, so it has nothing to show.
	tui.updatePadHover(x, y)
	if tui.padZone != (Rect{}) || tui.barInfo() != "" {
		t.Fatalf("a zoomed frame showed a divider: %v %q", tui.padZone, tui.barInfo())
	}
}

// TestZoomLayoutSig pins the repaint trigger: the signature differs when only
// the zoom differs, even with every rectangle the same.
func TestZoomLayoutSig(t *testing.T) {
	f := newZoomFrameTUI(t)
	plain := f.tui.layoutSig(f.snap)
	if f.snap.Zoom != "" {
		t.Fatalf("the fixture starts zoomed on %q", f.snap.Zoom)
	}
	f.snap.Zoom = "b"
	if got := f.tui.layoutSig(f.snap); got == plain {
		t.Fatalf("the signature ignored the zoom: %q", got)
	}
	f.snap.Zoom = ""
	if got := f.tui.layoutSig(f.snap); got != plain {
		t.Fatalf("clearing the zoom left the signature at %q, want %q", got, plain)
	}
}

// TestZoomBarMark pins the focus chip: the chip carries a Z in its own spans
// while zoomed, and the elision ladder drops it with the status word and the
// name.
func TestZoomBarMark(t *testing.T) {
	f := newZoomFrameTUI(t)
	chipTexts := func(l *barLeft) []string {
		var out []string
		for _, g := range l.groups() {
			if len(g) > 0 && g[0].bg == railFocusBG {
				for _, s := range g {
					out = append(out, s.text)
				}
			}
		}
		return out
	}
	has := func(texts []string, want string) bool {
		for _, s := range texts {
			if s == want {
				return true
			}
		}
		return false
	}

	plain := chipTexts(barLeftFor(f.snap))
	if has(plain, " Z") {
		t.Fatalf("an unzoomed chip carries the mark: %q", plain)
	}
	f.snap.Zoom = "b"
	zoomed := chipTexts(barLeftFor(f.snap))
	if !has(zoomed, " Z") {
		t.Fatalf("the zoomed chip carries no mark: %q", zoomed)
	}
	// The mark is a span of the chip, not a block of its own, and it sits
	// between the id and the name.
	id := -1
	for i, s := range zoomed {
		if s == " b" {
			id = i
		}
	}
	if id < 0 || id+1 >= len(zoomed) || zoomed[id+1] != " Z" {
		t.Fatalf("the mark is not the span after the id: %q", zoomed)
	}

	// Walk the ladder. The status word goes first, then the mark, then the
	// name; the mark never shows without the id.
	l := barLeftFor(f.snap)
	full := segCells(join(l.groups(), seg{text: " "}))
	sawNameWithoutMark := false
	for budget := 1; budget <= full; budget++ {
		segs := barLeftFor(f.snap).fit(budget)
		var got []string
		for _, s := range segs {
			if s.bg == railFocusBG {
				got = append(got, s.text)
			}
		}
		mark := has(got, " Z")
		if has(got, " unknown") && !mark {
			t.Fatalf("budget %d kept the status word and dropped the mark: %q", budget, got)
		}
		if mark && !has(got, " b") {
			t.Fatalf("budget %d kept the mark and dropped the id: %q", budget, got)
		}
		if has(got, " b-name") && !mark {
			sawNameWithoutMark = true
		}
	}
	if !sawNameWithoutMark {
		t.Fatal("no budget dropped the mark while the name survived")
	}
}

// TestZoomExecReachesTheHandler pins the action name: the keymap action sends
// the server's own action, and dm exec reaches the same handler on focus.
func TestZoomExecReachesTheHandler(t *testing.T) {
	tui, got := clientTUI(t, nil)
	tui.armed = true
	tui.runAction("zoom-pane")
	select {
	case m := <-got:
		if m.Type != "req" || m.Action != "zoom-pane" {
			t.Fatalf("the action sent %+v, want a req for zoom-pane", m)
		}
	case <-time.After(time.Second):
		t.Fatal("the zoom-pane action sent nothing")
	}
	if tui.armed {
		t.Fatal("the zoom-pane action left the client armed")
	}

	h := newHarness(t)
	h.run("new", "-s", "zx")
	left := h.focus("zx")
	right := h.split("zx", left, "right")
	h.run("exec", `{"action":"zoom-pane"}`, "-s", "zx")
	if z := h.zoom("zx"); z != right {
		t.Fatalf("dm exec zoom-pane zoomed %q, want the focused %q", z, right)
	}
	h.run("exec", `{"action":"zoom-pane"}`, "-s", "zx")
	if z := h.zoom("zx"); z != "" {
		t.Fatalf("a second dm exec zoom-pane left %q zoomed", z)
	}
}
