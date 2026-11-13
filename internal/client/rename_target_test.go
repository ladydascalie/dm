package client

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"dm/internal/layout"
	"dm/internal/testutil"
	"dm/internal/theme"
	"dm/internal/wire"
)

// renameTargetTUI is a client on a snapshot with one workspace, s1, and two
// windows: w1 is active and w2 is not. A window prompt must carry the id it
// was opened for, never the active window's.
func renameTargetTUI(t *testing.T) (*tui, chan wire.Msg) {
	t.Helper()
	tr, got := clientTUI(t, nil)
	tr.snap.Workspace = "main"
	tr.snap.All = []wire.AllPane{
		{ID: "a", Workspace: "s1", WorkspaceName: "main", Window: "w1", WindowName: "one", Focus: true},
		{ID: "b", Workspace: "s1", WorkspaceName: "main", Window: "w2", WindowName: "two"},
	}
	return tr, got
}

// A window prompt opens with the window's current name and submits
// rename-window with the window's id, not the active window's.
func TestRenameWindowPromptSendsWindowID(t *testing.T) {
	t.Parallel()
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
	testutil.NoMsg(t, got)

	renamePromptKeys(tr, "\r")
	if tr.mode != "" {
		t.Fatalf("mode = %q after enter, want empty", tr.mode)
	}
	m := testutil.NextMsg(t, got)
	if m.Type != "req" || m.Action != "rename-window" {
		t.Fatalf("sent %+v, want req rename-window", m)
	}
	if m.Target != "w2" {
		t.Fatalf("target = %q, want w2, not the active window", m.Target)
	}
	if m.Name != "two" {
		t.Fatalf("name = %q, want two", m.Name)
	}
	testutil.NoMsg(t, got)
}

// A workspace prompt still sends rename-workspace, and the kind is cleared
// when the prompt closes.
func TestRenameWorkspacePromptStillSendsWorkspace(t *testing.T) {
	t.Parallel()
	tr, got := renameTargetTUI(t)
	tr.openRenamePrompt()
	if tr.renameKind != "workspace" {
		t.Fatalf("kind = %q, want workspace", tr.renameKind)
	}
	if tr.renameDraft != "main" {
		t.Fatalf("draft = %q, want main", tr.renameDraft)
	}
	renamePromptKeys(tr, "\r")
	m := testutil.NextMsg(t, got)
	if m.Type != "req" || m.Action != "rename-workspace" {
		t.Fatalf("sent %+v, want req rename-workspace", m)
	}
	if m.Target != "s1" {
		t.Fatalf("target = %q, want s1", m.Target)
	}
	if tr.renameKind != "" {
		t.Fatalf("kind = %q after close, want empty", tr.renameKind)
	}
	testutil.NoMsg(t, got)
}

// The float title names the kind and the id it renames, and its input row
// names the draft.
func TestRenamePromptLabelsTheKind(t *testing.T) {
	t.Parallel()
	snap := testutil.TestSnapshot(&layout.Node{Pane: "a"}, 60, 8, nil)

	var wb bytes.Buffer
	drawRename(&wb, theme.DefaultTheme(), snap, "w2", "two", false, "window")
	win := renamePromptVisible(wb.String())
	if !strings.Contains(win, "rename window w2") {
		t.Fatalf("window title = %q", win)
	}
	if !strings.Contains(win, " name: two") {
		t.Fatalf("window input row = %q", win)
	}

	var sb bytes.Buffer
	drawRename(&sb, theme.DefaultTheme(), snap, "s1", "main", false, "workspace")
	ws := renamePromptVisible(sb.String())
	if !strings.Contains(ws, "rename workspace s1") {
		t.Fatalf("workspace title = %q", ws)
	}
	if !strings.Contains(ws, " name: main") {
		t.Fatalf("workspace input row = %q", ws)
	}
}

// The normal draw path paints the prompt with the id the prompt carries, not
// the active workspace's name. A window prompt therefore names the window.
func TestRenameWindowPromptPaintsWindowID(t *testing.T) {
	t.Parallel()
	f := newFrameTUI(t, 60, 12, 20)
	f.snap.Workspace = "main"
	f.snap.All = []wire.AllPane{
		{ID: "w1p1", Name: "alpha", Workspace: "s1", WorkspaceName: "main",
			Window: "w1", WindowName: "one", Focus: true, WorkspaceActive: true,
			WindowActive: true, WindowUnfolded: true, WorkspaceUnfolded: true},
		{ID: "w2p1", Name: "beta", Workspace: "s1", WorkspaceName: "main",
			Window: "w2", WindowName: "two", WorkspaceActive: true,
			WindowUnfolded: true, WorkspaceUnfolded: true},
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
	t.Parallel()
	f := newFrameTUI(t, 60, 12, 20)
	f.snap.Workspace = "s1"
	f.snap.All = []wire.AllPane{
		{ID: "w1p1", Name: "alpha", Workspace: "s1", WorkspaceName: "main",
			Window: "w1", WindowName: "one", Focus: true, WorkspaceActive: true,
			WindowActive: true, WindowUnfolded: true, WorkspaceUnfolded: true},
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

// A pane prompt opens with the pane's current name and submits set-pane-name
// with the pane's id and the edited draft.
func TestRenamePanePromptSendsPaneName(t *testing.T) {
	t.Parallel()
	tr, got := renameTargetTUI(t)
	tr.openRenamePromptFor("pane", "a", "alpha")
	if tr.mode != "rename" {
		t.Fatalf("mode = %q, want rename", tr.mode)
	}
	if tr.renameKind != "pane" {
		t.Fatalf("kind = %q, want pane", tr.renameKind)
	}
	if tr.renameDraft != "alpha" {
		t.Fatalf("draft = %q, want alpha", tr.renameDraft)
	}
	testutil.NoMsg(t, got)

	tr.renameDraft = ""
	renamePromptKeys(tr, "api\r")
	if tr.mode != "" {
		t.Fatalf("mode = %q after enter, want empty", tr.mode)
	}
	m := testutil.NextMsg(t, got)
	if m.Type != "req" || m.Action != "set-pane-name" {
		t.Fatalf("sent %+v, want req set-pane-name", m)
	}
	if m.Target != "a" {
		t.Fatalf("target = %q, want a", m.Target)
	}
	if m.Name != "api" {
		t.Fatalf("name = %q, want api", m.Name)
	}
	testutil.NoMsg(t, got)
}

// Esc cancels a pane rename prompt without sending a request.
func TestRenamePanePromptEscCancels(t *testing.T) {
	t.Parallel()
	tr, got := renameTargetTUI(t)
	tr.openRenamePromptFor("pane", "a", "alpha")
	renamePromptKeys(tr, "\x1b")
	tr.pasteDeadline = time.Now().Add(-escTimeout)
	if !tr.expirePaste(time.Now()) {
		t.Fatal("the stalled escape was not flushed")
	}
	if tr.mode != "" {
		t.Fatalf("mode = %q after esc, want empty", tr.mode)
	}
	testutil.NoMsg(t, got)
}

// An empty draft cancels a pane rename prompt without sending a request.
func TestRenamePanePromptEmptyDraftCancels(t *testing.T) {
	t.Parallel()
	tr, got := renameTargetTUI(t)
	tr.openRenamePromptFor("pane", "a", "alpha")
	renamePromptKeys(tr, "\x15\r")
	if tr.mode != "" {
		t.Fatalf("mode = %q after empty enter, want empty", tr.mode)
	}
	testutil.NoMsg(t, got)
}
