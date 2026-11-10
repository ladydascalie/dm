package server

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dm/internal/session"
	"dm/internal/testutil"
	"dm/internal/wire"
)

// diffServer is an in-process server with one pane whose shell stands in dir.
func diffServer(t *testing.T, name, dir string) (*server, string) {
	t.Helper()
	t.Setenv("DM_STATE", t.TempDir())
	st := session.NewState(name)
	id := testutil.RootPane(st)
	st.Panes[id].Cwd = dir
	return railServer(st), id
}

// requireGit skips a test on a host without git.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

// TestPaneDiffShowsStatAndPatch: the reply carries the stat block, a blank
// line, then the patch.
func TestPaneDiffShowsStatAndPatch(t *testing.T) {
	requireGit(t)
	dir := newGitRepo(t)
	path := filepath.Join(dir, "file.txt")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, []byte("second\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	s, id := diffServer(t, "diffstat", dir)
	lines := s.paneDiff(id)
	if len(lines) < 3 {
		t.Fatalf("diff = %q, want a stat block and a patch", lines)
	}
	text := strings.Join(lines, "\n")
	if !strings.Contains(text, "file.txt") || !strings.Contains(text, "+second") ||
		!strings.Contains(text, "diff --git") {
		t.Fatalf("diff text = %q, want the file, the added line and the git header", text)
	}
	blank := -1
	for i, line := range lines {
		if line == "" {
			blank = i
			break
		}
	}
	if blank < 0 {
		t.Fatalf("diff has no blank line between the stat and the patch: %q", lines)
	}
	if !strings.HasPrefix(lines[blank+1], "diff --git") {
		t.Fatalf("after the blank line comes %q, want the git header", lines[blank+1])
	}
	if !strings.Contains(strings.Join(lines[:blank], "\n"), "1 +") {
		t.Fatalf("stat block = %q, want the added count", lines[:blank])
	}
}

// TestPaneDiffTruncates: a diff longer than the cap ends with the line that
// says the output was cut.
func TestPaneDiffTruncates(t *testing.T) {
	requireGit(t)
	dir := newGitRepo(t)
	long := make([]string, diffMaxLines+500)
	for i := range long {
		long[i] = fmt.Sprintf("line %d", i)
	}
	// The file is committed, then a longer version replaces it, so the change
	// is unstaged and the diff is the one the pager shows.
	path := filepath.Join(dir, "long.txt")
	if err := os.WriteFile(path, []byte(strings.Join(long, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "long.txt")
	gitRun(t, dir, "commit", "-q", "-m", "long")
	long = append(long, long...)
	if err := os.WriteFile(path, []byte(strings.Join(long, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, id := diffServer(t, "diffcut", dir)
	lines := s.paneDiff(id)
	if len(lines) != diffMaxLines+1 {
		t.Fatalf("diff carried %d lines, want the %d cap plus the marker", len(lines), diffMaxLines)
	}
	last := lines[len(lines)-1]
	if !strings.Contains(last, "cut at") || !strings.Contains(last, fmt.Sprint(diffMaxLines)) {
		t.Fatalf("last line = %q, want the cut marker", last)
	}
}

// TestPaneDiffExplains: a folder outside git and a clean tree answer with one
// line, not an error.
func TestPaneDiffExplains(t *testing.T) {
	requireGit(t)
	plain := t.TempDir()
	s, id := diffServer(t, "diffplain", plain)
	if lines := s.paneDiff(id); len(lines) != 1 || !strings.Contains(lines[0], "not a git repository") {
		t.Fatalf("outside git = %q, want one explanatory line", lines)
	}
	clean := newGitRepo(t)
	s, id = diffServer(t, "diffclean", clean)
	if lines := s.paneDiff(id); len(lines) != 1 || !strings.Contains(lines[0], "no uncommitted changes") {
		t.Fatalf("clean tree = %q, want one explanatory line", lines)
	}
	// A pane with no folder at all still answers, so the pager has something
	// to show.
	s, id = diffServer(t, "diffnowhere", "")
	if lines := s.paneDiff(id); len(lines) != 1 || !strings.Contains(lines[0], "no working directory") {
		t.Fatalf("no cwd = %q, want one explanatory line", lines)
	}
}

// TestDiffRequestRepliesWithLines: the req action rides back with the text in
// Lines and the request's id.
func TestDiffRequestRepliesWithLines(t *testing.T) {
	requireGit(t)
	dir := newGitRepo(t)
	path := filepath.Join(dir, "file.txt")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, []byte("changed\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	s, id := diffServer(t, "diffreq", dir)
	srv, cli := net.Pipe()
	t.Cleanup(func() { _ = srv.Close(); _ = cli.Close() })
	cl := newClient(srv)
	s.mu.Lock()
	s.clients[cl] = true
	s.mu.Unlock()
	t.Cleanup(func() {
		s.mu.Lock()
		delete(s.clients, cl)
		s.mu.Unlock()
		cl.shutdown()
	})

	s.handleReq(cl, wire.Msg{Type: "req", Action: "diff", Target: id, ID: 9})
	_ = cli.SetReadDeadline(time.Now().Add(2 * time.Second))
	reply, err := wire.ReadMsg(cli)
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if reply.Type != "resp" || !reply.OK || reply.Action != "diff" || reply.ID != 9 {
		t.Fatalf("reply = %+v, want a good diff resp for id 9", reply)
	}
	if !strings.Contains(strings.Join(reply.Lines, "\n"), "+changed") {
		t.Fatalf("reply lines = %q, want the patch", reply.Lines)
	}
}
