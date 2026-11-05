package main

import (
	"bytes"
	"strings"
	"testing"
)

// renameTargetTUI is a client on a snapshot with one workspace, s1, and two
// windows: w1 is active and w2 is not. A window prompt must carry the id it
// was opened for, never the active window's.
func renameTargetTUI(t *testing.T) (*tui, chan Msg) {
	t.Helper()
	tr, got := clientTUI(t, nil)
	tr.snap.Workspace = "main"
	tr.snap.All = []AllPane{
		{ID: "a", Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one", Focus: true},
		{ID: "b", Workspace: "s1", WorkspaceName: "main", Window: "w2", WindowName: "two"},
	}
	return tr, got
}

// A window prompt opens with the window's current name and submits
// rename-window with the window's id, not the active window's.
func TestRenameWindowPromptSendsWindowID(t *testing.T) {
	tr, got := renameTargetTUI(t)
	tr.openRenamePromptFor("window", "w2", "two")
	if tr.mode != "rename" {
		t.Fatalf("mode = %q, want rename", tr.mode)
	}
	if tr.renameKind != "window" {
		t.Fatalf("kind = %q, want window", tr.renameKind)
	}
	if tr.renameDraft != "two" {
		t.Fatalf("draft = %q, want %q", tr.renameDraft, "two")
	}
	noMsg(t, got)

	renamePromptKeys(tr, "\r")
	if tr.mode != "" {
		t.Fatalf("mode = %q after enter, want empty", tr.mode)
	}
	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "rename-window" {
		t.Fatalf("sent %+v, want req rename-window", m)
	}
	if m.Target != "w2" {
		t.Fatalf("target = %q, want w2, not the active window", m.Target)
	}
	if m.Name != "two" {
		t.Fatalf("name = %q, want two", m.Name)
	}
	noMsg(t, got)
}

// A workspace prompt still sends rename-workspace, and the kind is cleared
// when the prompt closes.
func TestRenameWorkspacePromptStillSendsWorkspace(t *testing.T) {
	tr, got := renameTargetTUI(t)
	tr.openRenamePrompt()
	if tr.renameKind != "workspace" {
		t.Fatalf("kind = %q, want workspace", tr.renameKind)
	}
	if tr.renameDraft != "main" {
		t.Fatalf("draft = %q, want main", tr.renameDraft)
	}
	renamePromptKeys(tr, "\r")
	m := nextMsg(t, got)
	if m.Type != "req" || m.Action != "rename-workspace" {
		t.Fatalf("sent %+v, want req rename-workspace", m)
	}
	if m.Target != "s1" {
		t.Fatalf("target = %q, want s1", m.Target)
	}
	if tr.renameKind != "" {
		t.Fatalf("kind = %q after close, want empty", tr.renameKind)
	}
	noMsg(t, got)
}

// The float title names the kind and the id it renames, and its input row
// names the draft.
func TestRenamePromptLabelsTheKind(t *testing.T) {
	snap := testSnapshot(&Node{Pane: "a"}, 60, 8, nil)

	var wb bytes.Buffer
	drawRename(&wb, DefaultTheme(), snap, "w2", "two", "window")
	win := renamePromptVisible(wb.String())
	if !strings.Contains(win, "rename window w2") {
		t.Fatalf("window title = %q", win)
	}
	if !strings.Contains(win, " name: two") {
		t.Fatalf("window input row = %q", win)
	}

	var sb bytes.Buffer
	drawRename(&sb, DefaultTheme(), snap, "s1", "main", "workspace")
	ws := renamePromptVisible(sb.String())
	if !strings.Contains(ws, "rename workspace s1") {
		t.Fatalf("workspace title = %q", ws)
	}
	if !strings.Contains(ws, " name: main") {
		t.Fatalf("workspace input row = %q", ws)
	}
}

// The server renames the named window and leaves the active window alone.
func TestRenameWindowHonoursTarget(t *testing.T) {
	s, st := windowServer(t)
	ws := st.ws()
	w1 := ws.Windows[0]
	w2, _ := addWindow(st, "two")
	ws.Active = 0

	if err := s.renameWindow(w2.ID, "renamed"); err != nil {
		t.Fatalf("renameWindow(%q): %v", w2.ID, err)
	}
	if w2.Name != "renamed" {
		t.Fatalf("named window name = %q, want renamed", w2.Name)
	}
	if w1.Name == "renamed" {
		t.Fatalf("active window was renamed: %q", w1.Name)
	}
	if ws.Active != 0 || st.win().ID != w1.ID {
		t.Fatalf("active window = %d %q, want 0 %q", ws.Active, st.win().ID, w1.ID)
	}
}

// An unknown window and an empty name each return the error and change
// nothing.
func TestRenameWindowRefusals(t *testing.T) {
	s, st := windowServer(t)
	ws := st.ws()
	w1 := ws.Windows[0]
	before := w1.Name

	if err := s.renameWindow("w99", "ghost"); err == nil || !strings.Contains(err.Error(), `no window "w99"`) {
		t.Fatalf("unknown window error = %v, want no window \"w99\"", err)
	}
	if err := s.renameWindow(w1.ID, ""); err == nil || !strings.Contains(err.Error(), "name required") {
		t.Fatalf("empty name error = %v, want name required", err)
	}
	if w1.Name != before {
		t.Fatalf("refused rename changed the name: %q -> %q", before, w1.Name)
	}
}

// The normal draw path paints the prompt with the id the prompt carries, not
// the active workspace's name. A window prompt therefore names the window.
func TestRenameWindowPromptPaintsWindowID(t *testing.T) {
	f := newFrameTUI(t, 60, 12, 20)
	f.snap.Workspace = "main"
	f.snap.All = []AllPane{
		{ID: "w1p1", Name: "alpha", Workspace: "s1", WorkspaceName: "main",
			Window: "w1", WindowName: "one", Focus: true, WorkspaceActive: true,
			WindowActive: true, WindowUnfolded: true},
		{ID: "w2p1", Name: "beta", Workspace: "s1", WorkspaceName: "main",
			Window: "w2", WindowName: "two", WorkspaceActive: true,
			WindowUnfolded: true},
	}
	f.tui.openRenamePromptFor("window", "w2", "two")

	vis := renamePromptVisible(f.frame())
	if !strings.Contains(vis, "rename window w2") {
		t.Fatalf("prompt title does not name the window id: %q", vis)
	}
	if strings.Contains(vis, "rename window main") {
		t.Fatalf("prompt title names the active workspace: %q", vis)
	}
	if !strings.Contains(vis, " name: two") {
		t.Fatalf("prompt input row lost the draft: %q", vis)
	}
}

// The normal draw path paints a workspace prompt with the workspace id and its
// current name.
func TestRenameWorkspacePromptPaintsWorkspaceID(t *testing.T) {
	f := newFrameTUI(t, 60, 12, 20)
	f.snap.Workspace = "s1"
	f.snap.All = []AllPane{
		{ID: "w1p1", Name: "alpha", Workspace: "s1", WorkspaceName: "main",
			Window: "w1", WindowName: "one", Focus: true, WorkspaceActive: true,
			WindowActive: true, WindowUnfolded: true},
	}
	f.tui.openRenamePrompt()

	vis := renamePromptVisible(f.frame())
	if !strings.Contains(vis, "rename workspace s1") {
		t.Fatalf("prompt title does not name the workspace id: %q", vis)
	}
	if strings.Contains(vis, "rename window") {
		t.Fatalf("workspace prompt carries a window title: %q", vis)
	}
	if !strings.Contains(vis, " name: main") {
		t.Fatalf("prompt input row lost the draft: %q", vis)
	}
}
