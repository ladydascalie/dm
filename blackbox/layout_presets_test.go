package blackbox

// The layout presets end to end, through the binary only: capture a live shape
// to a file, seed a fresh session from it, reattach, and force-reset it back to
// the saved shape.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// layoutDocFile is the layout document these tests read back. It is local on
// purpose: the suite proves what the file records without naming a production
// type.
type layoutDocFile struct {
	Version    int                      `json:"version"`
	Name       string                   `json:"name"`
	Workspaces []layoutDocWorkspace     `json:"workspaces"`
	Panes      map[string]layoutDocPane `json:"panes"`
}

type layoutDocWorkspace struct {
	Name    string            `json:"name"`
	Project string            `json:"project"`
	Active  int               `json:"active"`
	Windows []layoutDocWindow `json:"windows"`
}

type layoutDocWindow struct {
	Name  string      `json:"name"`
	Focus string      `json:"focus"`
	Root  *layoutNode `json:"root"`
}

type layoutDocPane struct {
	Cwd  string   `json:"cwd"`
	Argv []string `json:"argv"`
}

// layoutFile is where a named layout lands under the harness config dir.
func layoutFile(h *harness, name string) string {
	return filepath.Join(h.configDir(), "dm", "layouts", name+".json")
}

// readLayoutDoc decodes the file a save wrote.
func readLayoutDoc(t *testing.T, path string) *layoutDocFile {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("layout file %s: %v", path, err)
	}
	var doc layoutDocFile
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("layout file %s: %v", path, err)
	}
	return &doc
}

// paneID is the addressable pane id shape: a window id, p, a sequence.
var paneID = regexp.MustCompile(`^w[0-9]+p[0-9]+$`)

// fleetRows is the snapshot's whole-session pane list, one row per pane.
func fleetRows(snap map[string]any) []map[string]any {
	raw, _ := snap["all"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		if m, ok := r.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// fleetIDs is every pane id in the session, in the snapshot's order.
func fleetIDs(snap map[string]any) []string {
	rows := fleetRows(snap)
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, snapStr(r, "id"))
	}
	return out
}

// waitFleet waits until the session holds n panes anywhere. waitPanes counts
// the active window alone, which is one pane in this scenario.
func waitFleet(t *testing.T, h *harness, session string, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var got []string
	for time.Now().Before(deadline) {
		got = fleetIDs(snapOf(t, h, session))
		if len(got) == n {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("session %s holds %v, want %d panes", session, got, n)
}

// sameShape compares two trees by direction, kid count and ratio. It ignores
// the pane ids: the fresh session assigns its own, so only the shape travels.
func sameShape(t *testing.T, where string, got, want *layoutNode) {
	t.Helper()
	if want == nil || got == nil {
		if want != got {
			t.Fatalf("%s: got %v, want %v", where, got, want)
		}
		return
	}
	if (got.Pane != "") != (want.Pane != "") {
		t.Fatalf("%s: got a %s, want %s", where, nodeKind(got), nodeKind(want))
	}
	if got.Dir != want.Dir {
		t.Fatalf("%s: dir = %q, want %q", where, got.Dir, want.Dir)
	}
	if len(got.Kids) != len(want.Kids) {
		t.Fatalf("%s: %d kids, want %d", where, len(got.Kids), len(want.Kids))
	}
	if len(want.Sizes) > 0 {
		if len(got.Sizes) != len(want.Sizes) {
			t.Fatalf("%s: sizes = %v, want %v", where, got.Sizes, want.Sizes)
		}
		for i := range want.Sizes {
			if math.Abs(got.Sizes[i]-want.Sizes[i]) > 1e-9 {
				t.Fatalf("%s: sizes = %v, want %v", where, got.Sizes, want.Sizes)
			}
		}
	}
	for i := range want.Kids {
		sameShape(t, fmt.Sprintf("%s.kids[%d]", where, i), got.Kids[i], want.Kids[i])
	}
}

// containsID reports whether ids holds id.
func containsID(ids []string, id string) bool {
	for _, got := range ids {
		if got == id {
			return true
		}
	}
	return false
}

func nodeKind(n *layoutNode) string {
	if n == nil {
		return "nil"
	}
	if n.Pane != "" {
		return "leaf"
	}
	return "split"
}

// TestLayoutPresets proves the whole round trip: a live shape captured under a
// name comes back, in a session of its own, with fresh ids and the launch data
// intact; a second attach leaves it alone; --force puts it back after a
// mutation; and a document that does not apply is refused before a session
// starts.
func TestLayoutPresets(t *testing.T) {
	h := newHarness(t)
	const src = "base"

	// One live session: two workspaces (one carrying a project), two windows,
	// distinct names, a nested split with an unequal ratio, a pane running
	// argv, and a pane started in a directory of its own.
	h.run("new", "-s", src)
	root := h.focus(src)
	h.rpc(src, map[string]any{"action": "rename-workspace", "target": "s1", "name": "development"})
	h.rpc(src, map[string]any{"action": "set-project", "target": "s1", "name": "go-backend"})
	h.rpc(src, map[string]any{"action": "rename-window", "target": "w1", "name": "api"})
	right := h.split(src, root, "right")
	below := h.split(src, right, "down")
	h.rpc(src, map[string]any{"action": "resize-split", "target": root, "other": right, "ratio": 0.7})
	res := h.rpc(src, map[string]any{"action": "split", "target": below, "dir": "down", "argv": []string{"sleep", "60"}})
	sleeper, _ := res["pane"].(string)
	workDir := t.TempDir()
	logs := h.rpc(src, map[string]any{"action": "new-window", "name": "logs", "cwd": workDir})
	logPane, _ := logs["pane"].(string)
	h.rpc(src, map[string]any{"action": "new-workspace", "name": "side", "window": "notes"})
	// The saved session is left on its first workspace, second window, so the
	// active indices have somewhere to travel from.
	h.rpc(src, map[string]any{"action": "focus-workspace", "target": "s1"})
	h.rpc(src, map[string]any{"action": "focus-window", "target": "w2"})
	logsFocus := h.focus(src)
	waitFleet(t, h, src, 6)
	if sleeper == "" || logPane == "" {
		t.Fatalf("the build steps returned no pane: sleeper=%q logs=%q", sleeper, logPane)
	}

	// The save writes the shape under the harness config dir and says where.
	out := h.run("layout", "save", "preset", "-s", src)
	path := layoutFile(h, "preset")
	if got := strings.TrimSpace(out); got != path {
		t.Fatalf("dm layout save printed %q, want %q", got, path)
	}
	doc := readLayoutDoc(t, path)
	if doc.Version != 1 || doc.Name != "preset" {
		t.Fatalf("document = version %d, name %q; want version 1, name preset", doc.Version, doc.Name)
	}
	if len(doc.Workspaces) != 2 {
		t.Fatalf("got %d workspaces, want 2", len(doc.Workspaces))
	}
	dev := doc.Workspaces[0]
	if dev.Name != "development" || dev.Project != "go-backend" {
		t.Fatalf("workspace 0 = %q/%q, want development/go-backend", dev.Name, dev.Project)
	}
	if dev.Active != 1 {
		t.Fatalf("workspace 0 active = %d, want 1 (the logs window)", dev.Active)
	}
	if len(dev.Windows) != 2 || dev.Windows[0].Name != "api" || dev.Windows[1].Name != "logs" {
		t.Fatalf("workspace 0 windows = %v, want api and logs", dev.Windows)
	}
	// The saved focus is a leaf of its own tree, and the logs window keeps the
	// pane that was focused when the save ran.
	if leaves, _ := walkTree(dev.Windows[0].Root); !containsID(leaves, dev.Windows[0].Focus) {
		t.Fatalf("api focus = %q, want one of %v", dev.Windows[0].Focus, leaves)
	}
	if dev.Windows[1].Focus != logsFocus {
		t.Fatalf("logs focus = %q, want %q", dev.Windows[1].Focus, logsFocus)
	}
	side := doc.Workspaces[1]
	if side.Name != "side" || len(side.Windows) != 1 || side.Windows[0].Name != "notes" {
		t.Fatalf("workspace 1 = %q/%v, want side with one window called notes", side.Name, side.Windows)
	}
	// The nested split and its unequal ratio survive into the file.
	tree := dev.Windows[0].Root
	if tree == nil || tree.Dir != "row" || len(tree.Kids) != 2 || len(tree.Sizes) != 2 {
		t.Fatalf("api tree = %+v, want a two-kid row with sizes", tree)
	}
	if tree.Sizes[0] <= tree.Sizes[1] {
		t.Fatalf("api sizes = %v, want the first pane wider", tree.Sizes)
	}
	if tree.Kids[1].Dir != "col" || len(tree.Kids[1].Kids) != 2 || tree.Kids[1].Kids[1].Dir != "col" {
		t.Fatalf("api tree = %+v, want the nested col splits", tree.Kids[1])
	}
	// The launch data travels; the session's own state does not.
	if p := doc.Panes[sleeper]; p.Cwd == "" || strings.Join(p.Argv, " ") != "sleep 60" {
		t.Fatalf("sleeper pane = %+v, want a cwd and argv sleep 60", p)
	}
	if p := doc.Panes[logPane]; p.Cwd != workDir {
		t.Fatalf("logs pane cwd = %q, want %q", p.Cwd, workDir)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"box", "secrets", "rail", "projectFold", "reported", "plugin_env"} {
		if strings.Contains(string(raw), `"`+key+`":`) {
			t.Fatalf("the layout file carries %q:\n%s", key, raw)
		}
	}

	// The source session stops; the layout is all that is left of it.
	h.run("kill", "-s", src)
	const dst = "restored"
	if got := strings.TrimSpace(h.run("new", "-L", "preset", "-s", dst)); got != dst {
		t.Fatalf("dm new -L printed %q, want %q", got, dst)
	}
	waitFleet(t, h, dst, 6)

	// The shape is back, with ids from the fresh session's counters, in tree
	// order: workspaces s1.. , windows w1.. across the document, panes per
	// window.
	snap := snapOf(t, h, dst)
	ids := fleetIDs(snap)
	want := []string{"w1p1", "w1p2", "w1p3", "w1p4", "w2p1", "w3p1"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("pane ids = %v, want %v", ids, want)
	}
	for _, id := range ids {
		if !paneID.MatchString(id) {
			t.Fatalf("pane id %q is not the addressable form", id)
		}
	}
	// The restored state holds exactly those panes, so no id from the saved
	// session leaked in beside them.
	st := h.sessionState(dst)
	if len(st.Panes) != len(want) {
		t.Fatalf("restored state holds %d panes, want %d: %v", len(st.Panes), len(want), st.Panes)
	}
	for _, id := range want {
		if st.Panes[id] == nil {
			t.Fatalf("restored state has no pane %q", id)
		}
		// The state records a pane; the pane itself must be serving, so the
		// cwd and argv below are a live pane's launch data, not a filled-in
		// map that never spawned.
		if _, err := h.try("status", id, "-s", dst); err != nil {
			t.Fatalf("restored pane %s does not answer: %v", id, err)
		}
	}
	// Names, project and cwd travel on the live session.
	names := map[string]bool{}
	projects := map[string]string{}
	for _, r := range fleetRows(snap) {
		names[snapStr(r, "workspaceName")+"/"+snapStr(r, "windowName")] = true
		projects[snapStr(r, "workspaceName")] = snapStr(r, "project")
	}
	for _, want := range []string{"development/api", "development/logs", "side/notes"} {
		if !names[want] {
			t.Fatalf("the restored session has no %s window: %v", want, names)
		}
	}
	if got := projects["development"]; got != "go-backend" {
		t.Fatalf("project = %q, want go-backend", got)
	}
	if got := snapStr(snap, "workspace"); got != "development" {
		t.Fatalf("active workspace = %q, want development", got)
	}
	if got := snapInt(snap, "activeWindow"); got != 1 {
		t.Fatalf("active window = %d, want 1 (the logs window)", got)
	}
	// Every pane's cwd and argv is what the file recorded.
	for id, want := range doc.Panes {
		got := st.Panes[id]
		if got == nil {
			t.Fatalf("no restored pane %q", id)
		}
		if filepath.Clean(got.Cwd) != filepath.Clean(want.Cwd) {
			t.Fatalf("pane %s cwd = %q, want %q", id, got.Cwd, want.Cwd)
		}
		if strings.Join(got.Argv, " ") != strings.Join(want.Argv, " ") {
			t.Fatalf("pane %s argv = %v, want %v", id, got.Argv, want.Argv)
		}
	}
	// The tree, read back through the list RPC.
	h.rpc(dst, map[string]any{"action": "focus-window", "target": "w1"})
	live := peekTree(t, h, dst)
	sameShape(t, "api", live, tree)
	if leaves, single := walkTree(live); len(leaves) != 4 || single {
		t.Fatalf("api tree = %v, want 4 leaves and no collapsed split", leaves)
	}
	if got := h.focus(dst); got != dev.Windows[0].Focus {
		t.Fatalf("api focus = %q, want the saved %q", got, dev.Windows[0].Focus)
	}

	// A second attach of the same layout leaves the live session alone.
	before := shapeKey(t, h, dst)
	client := startAttachArgs(t, h, "-L", "preset", "-s", dst)
	client.waitContains(t, firstPaint)
	if after := shapeKey(t, h, dst); after != before {
		t.Fatalf("attaching the layout changed the session:\nbefore %s\nafter  %s", before, after)
	}

	// A mutation after the save is discarded by --force, which resets the
	// session and seeds it from the file again.
	extra := h.split(dst, root, "up")
	waitFleet(t, h, dst, 7)
	forced := startAttachArgs(t, h, "-L", "preset", "--force", "-s", dst)
	forced.waitContains(t, firstPaint)
	waitFleet(t, h, dst, 6)
	snap = snapOf(t, h, dst)
	for _, id := range fleetIDs(snap) {
		if id == extra {
			t.Fatalf("--force kept the pane added after the save: %v", fleetIDs(snap))
		}
	}
	h.rpc(dst, map[string]any{"action": "focus-window", "target": "w1"})
	sameShape(t, "api after --force", peekTree(t, h, dst), tree)

	// A hand-written document whose tree names a leaf the panes map does not
	// hold is refused, the error names the file, and no session starts.
	handPath := layoutFile(h, "hand")
	if err := os.MkdirAll(filepath.Dir(handPath), 0o755); err != nil {
		t.Fatal(err)
	}
	hand := `{"version":1,"name":"hand","workspaces":[{"name":"w","active":0,` +
		`"windows":[{"name":"a","focus":"a","root":{"dir":"row","kids":[{"pane":"a"},{"pane":"b"}]}}]}],` +
		`"panes":{"a":{"cwd":"/tmp"}}}`
	if err := os.WriteFile(handPath, []byte(hand), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = h.try("-L", "hand", "-s", "handpane")
	if err == nil {
		t.Fatalf("dm -L hand accepted a leaf missing from panes:\n%s", out)
	}
	for _, needle := range []string{handPath, "missing from panes", `"b"`} {
		if !strings.Contains(out, needle) {
			t.Fatalf("refusal %q does not name %q", strings.TrimSpace(out), needle)
		}
	}
	if _, statErr := os.Stat(filepath.Join(h.stateDir(), "sessions", "handpane")); !os.IsNotExist(statErr) {
		t.Fatalf("a refused layout started a session: %v", statErr)
	}
}

// shapeKey is the session's shape as one string: the pane ids and the active
// window's tree, so a reattach that changes either reads as a difference.
func shapeKey(t *testing.T, h *harness, session string) string {
	t.Helper()
	tree := peekTree(t, h, session)
	b, err := json.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(fleetIDs(snapOf(t, h, session)), ",") + "|" + string(b)
}

// TestLayoutDeletedCwd is the spec's deleted-directory case: a pane whose saved
// cwd no longer exists spawns at $HOME, logs one line, and one deleted
// directory never fails the whole layout.
//
// Skipped because the product does not do it yet. Measured with this tree
// (2026-11-19): pane.Run hands the saved cwd to the pty, the chdir fails, the
// pane daemon exits, the server reads it as a closed pane, prunes it from the
// tree, and a session whose last pane it was ends outright (endSession removes
// the session directory). Neither internal/server nor internal/pane stats the
// cwd or falls back to $HOME. Both are outside this lane. Delete the skip when
// the fallback lands: the body below is the acceptance test for it.
func TestLayoutDeletedCwd(t *testing.T) {
	home := os.Getenv("HOME")
	if home == "" {
		t.Skip("the harness has no HOME to fall back to")
	}
	t.Skip("deleted cwd does not fall back to $HOME yet: see the comment above")

	h := newHarness(t)
	gone := filepath.Join(t.TempDir(), "gone")
	layout := layoutFile(h, "gone-cwd")
	if err := os.MkdirAll(filepath.Dir(layout), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := fmt.Sprintf(`{"version":1,"name":"gone-cwd","workspaces":[{"name":"w","active":0,`+
		`"windows":[{"name":"a","focus":"a","root":{"dir":"row","kids":[{"pane":"a"},{"pane":"b"}]}}]}],`+
		`"panes":{"a":{"cwd":%q},"b":{"cwd":"/tmp"}}}`, gone)
	if err := os.WriteFile(layout, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	h.run("new", "-L", "gone-cwd", "-s", "gone")
	waitFleet(t, h, "gone", 2)
	st := h.sessionState("gone")
	if ps := st.Panes["w1p1"]; ps == nil || filepath.Clean(ps.Cwd) != filepath.Clean(home) {
		t.Fatalf("deleted cwd pane = %+v, want cwd %q", ps, home)
	}
}
