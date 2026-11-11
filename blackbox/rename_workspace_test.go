package blackbox

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// renameWSShape renders the state fields a rename must never move: the ids,
// the active indexes, the window names and focus, and the pane set.
func renameWSShape(st *stateFile) string {
	var b strings.Builder
	fmt.Fprintf(&b, "active=%d nextWorkspace=%d nextWindow=%d", st.Active, st.NextWorkspace, st.NextWindow)
	for _, ws := range st.Workspaces {
		fmt.Fprintf(&b, " ws=%s/a%d/%d", ws.ID, ws.Active, len(ws.Windows))
		for _, w := range ws.Windows {
			fmt.Fprintf(&b, " win=%s/%s/f%s", w.ID, w.Name, w.Focus)
		}
	}
	ids := make([]string, 0, len(st.Panes))
	for id := range st.Panes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	fmt.Fprintf(&b, " panes=%s", strings.Join(ids, ","))
	return b.String()
}

// A rename stores the trimmed name in the state file and leaves the id alone.
func TestRenameWorkspaceKeepsIDAndTrimsName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "rwid")
	before := h.sessionState("rwid")
	if len(before.Workspaces) != 1 || before.Workspaces[0].ID != "s1" {
		t.Fatalf("fresh session workspaces = %+v", before.Workspaces)
	}
	shape := renameWSShape(before)

	res := h.rpc("rwid", map[string]any{
		"action": "rename-workspace", "target": "s1", "name": "  api work  ",
	})
	if got, _ := res["workspace"].(string); got != "s1" {
		t.Fatalf("reply workspace = %v", res["workspace"])
	}
	if got, _ := res["name"].(string); got != "api work" {
		t.Fatalf("reply name = %q, want the trimmed name", res["name"])
	}

	after := h.sessionState("rwid")
	if len(after.Workspaces) != 1 || after.Workspaces[0].ID != "s1" {
		t.Fatalf("workspace id moved: %+v", after.Workspaces)
	}
	if after.Workspaces[0].Name != "api work" {
		t.Fatalf("state name = %q, want api work", after.Workspaces[0].Name)
	}
	if got := renameWSShape(after); got != shape {
		t.Fatalf("rename moved more than the name:\nbefore %s\nafter  %s", shape, got)
	}
}

// A background workspace renames by id, and neither the active index nor the
// focus pane follows it.
func TestRenameWorkspaceBackgroundWorkspace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "rwbg")
	res := h.rpc("rwbg", map[string]any{
		"action": "new-workspace", "name": "second", "cwd": "/tmp", "nofocus": true,
	})
	bg, _ := res["workspace"].(string)
	if bg == "" || bg == "s1" {
		t.Fatalf("background workspace = %v", res)
	}
	tree := h.rpc("rwbg", map[string]any{"action": "list"})
	snap := tree["snapshot"].(map[string]any)
	wantWS := snap["workspace"].(string)
	wantFocus := snap["focus"].(string)
	if wantWS != "main" {
		t.Fatalf("active workspace = %s, want the unfocused main", wantWS)
	}
	before := h.sessionState("rwbg")
	active := before.Active

	res = h.rpc("rwbg", map[string]any{
		"action": "rename-workspace", "target": bg, "name": " bg work ",
	})
	if got, _ := res["workspace"].(string); got != bg {
		t.Fatalf("reply workspace = %v, want %s", res["workspace"], bg)
	}
	if got, _ := res["name"].(string); got != "bg work" {
		t.Fatalf("reply name = %q", res["name"])
	}

	after := h.sessionState("rwbg")
	if after.Active != active || after.Workspaces[active].ID != "s1" {
		t.Fatalf("active index moved: %d -> %d", active, after.Active)
	}
	var renamed *stateWorkspace
	for _, ws := range after.Workspaces {
		if ws.ID == bg {
			renamed = ws
		}
	}
	if renamed == nil {
		t.Fatalf("workspace %s vanished: %+v", bg, after.Workspaces)
	}
	if renamed.Name != "bg work" {
		t.Fatalf("background name = %q, want bg work", renamed.Name)
	}
	tree = h.rpc("rwbg", map[string]any{"action": "list"})
	snap = tree["snapshot"].(map[string]any)
	if snap["workspace"] != wantWS || snap["focus"] != wantFocus {
		t.Fatalf("snapshot moved: workspace %v focus %v, want %s %s",
			snap["workspace"], snap["focus"], wantWS, wantFocus)
	}
}

// Both an empty and a spaces-only name are refused and change nothing.
func TestRenameWorkspaceRefusesEmptyName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "rwempty")
	shape := renameWSShape(h.sessionState("rwempty"))

	for _, name := range []string{"", "   \t "} {
		raw, _ := json.Marshal(map[string]any{
			"action": "rename-workspace", "target": "s1", "name": name,
		})
		out, err := h.try("exec", string(raw), "-s", "rwempty")
		if err == nil {
			t.Fatalf("name %q was accepted:\n%s", name, out)
		}
		if !strings.Contains(out, "name required") {
			t.Fatalf("name %q error = %q, want name required", name, out)
		}
		if got := renameWSShape(h.sessionState("rwempty")); got != shape {
			t.Fatalf("refused name %q changed the state:\nbefore %s\nafter  %s", name, shape, got)
		}
	}
}

// An id that names no workspace is refused, and nothing changes.
func TestRenameWorkspaceUnknownID(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "rwunk")
	shape := renameWSShape(h.sessionState("rwunk"))

	out, err := h.try("exec", `{"action":"rename-workspace","target":"s99","name":"ghost"}`, "-s", "rwunk")
	if err == nil {
		t.Fatalf("unknown workspace accepted:\n%s", out)
	}
	if !strings.Contains(out, `no workspace "s99"`) {
		t.Fatalf("error = %q, want no workspace \"s99\"", out)
	}
	if got := renameWSShape(h.sessionState("rwunk")); got != shape {
		t.Fatalf("refused rename changed the state:\nbefore %s\nafter  %s", shape, got)
	}
}

// The CLI verb prints the reply as JSON and exits 0; a missing argument exits
// non-zero.
func TestRenameWorkspaceCLI(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "rwcli")

	out := h.run("rename-workspace", "s1", "cli-work", "-s", "rwcli")
	var res struct {
		Workspace string `json:"workspace"`
		Name      string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("verb output is not JSON: %v\n%s", err, out)
	}
	if res.Workspace != "s1" || res.Name != "cli-work" {
		t.Fatalf("verb reply = %+v, want s1 cli-work", res)
	}
	if got := h.sessionState("rwcli").Workspaces[0].Name; got != "cli-work" {
		t.Fatalf("state name = %q, want cli-work", got)
	}

	for _, args := range [][]string{
		{"rename-workspace", "-s", "rwcli"},
		{"rename-workspace", "s1", "-s", "rwcli"},
	} {
		if out, err := h.try(args...); err == nil {
			t.Fatalf("dm %s exited 0:\n%s", strings.Join(args, " "), out)
		}
	}
}

// A client asking for panes or workspaces sees the new label.
func TestRenameWorkspaceLabelReachesClients(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.run("new", "-s", "rwlbl")
	h.run("rename-workspace", "s1", "api work", "-s", "rwlbl")

	res := h.rpc("rwlbl", map[string]any{"action": "list-panes"})
	panes, _ := res["panes"].([]any)
	if len(panes) == 0 {
		t.Fatalf("list-panes returned nothing: %v", res)
	}
	for _, raw := range panes {
		p := raw.(map[string]any)
		if p["workspace"] != "s1" {
			continue
		}
		if p["workspaceName"] != "api work" {
			t.Fatalf("list-panes workspaceName = %v, want api work", p["workspaceName"])
		}
	}

	out := h.run("exec", `{"action":"list-workspaces"}`, "-s", "rwlbl")
	var list []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("list-workspaces output: %v\n%s", err, out)
	}
	seen := false
	for _, ws := range list {
		if ws.ID != "s1" {
			continue
		}
		seen = true
		if ws.Name != "api work" {
			t.Fatalf("list-workspaces name = %q, want api work", ws.Name)
		}
	}
	if !seen {
		t.Fatalf("list-workspaces has no s1: %s", out)
	}
}
