package main

import (
	"sync"
	"testing"
	"time"
)

// TestGitStateReachesAll runs the git check off the snapshot path and confirms
// every field lands on the AllPane for the pane's working directory.
func TestGitStateReachesAll(t *testing.T) {
	t.Setenv("DM_STATE", t.TempDir())
	st := newState("gitstate")
	id := rootPane(st)
	st.Panes[id].Cwd = "/work/dm"

	var mu sync.Mutex
	var seen []string
	old := detectGit
	detectGit = func(cwd string) gitInfo {
		mu.Lock()
		seen = append(seen, cwd)
		mu.Unlock()
		if cwd == "/work/dm" {
			return gitInfo{
				branch: "dm-git", worktree: "dm-git-w1",
				dirty: true, added: 7, deleted: 3,
			}
		}
		return gitInfo{}
	}
	defer func() { detectGit = old }()

	s := railServer(st)
	deadline := time.Now().Add(2 * time.Second)
	for {
		snap := s.snapshotLocked()
		if len(snap.All) == 1 && snap.All[0].Added == 7 {
			got := snap.All[0]
			if got.Branch != "dm-git" || got.Worktree != "dm-git-w1" ||
				!got.Dirty || got.Deleted != 3 {
				t.Fatalf("All pane git state = %+v", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("git state never reached the snapshot: %+v", snap.All)
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, cwd := range seen {
		if cwd == "/work/dm" {
			return
		}
	}
	t.Fatalf("detectGit never saw the pane cwd: %q", seen)
}

// TestGitStateOutsideRepo checks that a directory outside git reports the zero
// value, so the rail has nothing to show there.
func TestGitStateOutsideRepo(t *testing.T) {
	got := detectGit(t.TempDir())
	if got != (gitInfo{}) {
		t.Fatalf("non-repo git info = %+v, want zero", got)
	}
}

// TestParseNumstat sums a normal line, counts nothing for a binary file's dash
// counts, and skips a blank line.
func TestParseNumstat(t *testing.T) {
	out := []byte("12\t4\tserver.go\n-\t-\tlogo.png\n\n3\t0\tproto.go\n")
	added, deleted := parseNumstat(out)
	if added != 15 || deleted != 4 {
		t.Fatalf("parseNumstat = %d added, %d deleted, want 15, 4", added, deleted)
	}
	if a, d := parseNumstat([]byte("\n")); a != 0 || d != 0 {
		t.Fatalf("parseNumstat on a blank line = %d, %d", a, d)
	}
}
