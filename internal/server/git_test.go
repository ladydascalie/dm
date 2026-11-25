package server

import (
	"dm/internal/session"
	"dm/internal/testutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestGitStateReachesAll runs the git check off the snapshot path and confirms
// every field lands on the AllPane for the pane's working directory.
func TestGitStateReachesAll(t *testing.T) {
	t.Parallel()
	st := session.NewStateAt(t.TempDir(), "gitstate")
	id := testutil.RootPane(st)
	st.Panes[id].Cwd = "/work/dm"

	var mu sync.Mutex
	var seen []string
	s := railServer(st)
	s.detect = func(cwd string) gitInfo {
		mu.Lock()
		seen = append(seen, cwd)
		mu.Unlock()
		if cwd == "/work/dm" {
			return gitInfo{
				branch: "dm-git", worktree: "dm-git-w1",
				root:  "/work/dm",
				dirty: true, added: 7, deleted: 3, files: 2,
				head: "abc1234", headSubject: "fix the rail",
				upstream: "origin/dm-git", ahead: 4, behind: 1,
			}
		}
		return gitInfo{}
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		snap := snapshot(s)
		if len(snap.All) == 1 && snap.All[0].Added == 7 {
			got := snap.All[0]
			if got.Branch != "dm-git" || got.Worktree != "dm-git-w1" ||
				got.Cwd != "/work/dm" || got.Root != "/work/dm" ||
				!got.Dirty || got.Deleted != 3 || got.Files != 2 ||
				got.Head != "abc1234" || got.HeadSubject != "fix the rail" ||
				got.Upstream != "origin/dm-git" || got.Ahead != 4 || got.Behind != 1 {
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
	t.Parallel()
	got := detectGit(t.TempDir())
	if got != (gitInfo{}) {
		t.Fatalf("non-repo git info = %+v, want zero", got)
	}
}

// TestParseNumstat sums a normal line, counts nothing for a binary file's dash
// counts, counts the binary file itself, and skips a blank line.
func TestParseNumstat(t *testing.T) {
	t.Parallel()
	out := []byte("12\t4\tserver.go\n-\t-\tlogo.png\n\n3\t0\tproto.go\n")
	added, deleted, files := parseNumstat(out)
	if added != 15 || deleted != 4 || files != 3 {
		t.Fatalf("parseNumstat = %d added, %d deleted, %d files, want 15, 4, 3",
			added, deleted, files)
	}
	if a, d, n := parseNumstat([]byte("\n")); a != 0 || d != 0 || n != 0 {
		t.Fatalf("parseNumstat on a blank line = %d, %d, %d", a, d, n)
	}
}

// gitEnv is the environment the helpers run git with: the operator's own git
// config stays out of the test.
func gitEnv() []string {
	return append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=dm", "GIT_AUTHOR_EMAIL=dm@example.com",
		"GIT_COMMITTER_NAME=dm", "GIT_COMMITTER_EMAIL=dm@example.com",
	)
}

// gitRun runs one git command in dir, with a repo config isolated from the
// operator's own.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
}

// gitTop asks git for the toplevel of dir, so a test compares detectGit's root
// with the folder git itself reports.
func gitTop(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse --show-toplevel in %s: %v", dir, err)
	}
	return filepath.Clean(strings.TrimSpace(string(out)))
}

// newGitRepo makes a temporary repo with one commit on main and one file.
func newGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "config", "user.name", "dm")
	gitRun(t, dir, "config", "user.email", "dm@example.com")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "file.txt")
	gitRun(t, dir, "commit", "-q", "-m", "first")
	return dir
}

// TestDetectGitReal drives the production detectGit against real git: a clean
// repo, a dirty file, a linked worktree, a detached HEAD and a plain directory.
func TestDetectGitReal(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := newGitRepo(t)
	if got := detectGit(repo); !got.inRepo || got.branch != "main" ||
		got.worktree != "" || got.root != gitTop(t, repo) ||
		got.dirty || got.added != 0 || got.deleted != 0 {
		t.Fatalf("clean repo = %+v, want main, in repo, clean and zero counts", got)
	}

	// A subdirectory reports the repository toplevel, not its own path.
	sub := filepath.Join(repo, "deep")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := detectGit(sub); got.root != gitTop(t, repo) || got.branch != "main" {
		t.Fatalf("subdirectory = %+v, want main and the repo toplevel %q", got, gitTop(t, repo))
	}

	// One more line on a tracked file is a dirty tree with an added count.
	path := filepath.Join(repo, "file.txt")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, []byte("second\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	got := detectGit(repo)
	if !got.dirty || got.added < 1 || got.deleted != 0 {
		t.Fatalf("dirty repo = %+v, want dirty with an added line", got)
	}

	// A linked worktree reports its own branch and its own folder.
	wt := filepath.Join(t.TempDir(), "lane-wt")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "lane", wt)
	got = detectGit(wt)
	if !got.inRepo || got.branch != "lane" || got.worktree != "lane-wt" ||
		filepath.Base(got.root) != "lane-wt" ||
		got.dirty || got.added != 0 || got.deleted != 0 {
		t.Fatalf("linked worktree = %+v, want lane in lane-wt, clean, rooted at the worktree", got)
	}

	// A detached checkout has no branch, so the folder names the line.
	gitRun(t, wt, "checkout", "-q", "--detach")
	got = detectGit(wt)
	if got.branch != "" || got.worktree != "lane-wt" ||
		filepath.Base(got.root) != "lane-wt" {
		t.Fatalf("detached worktree = %+v, want no branch, the folder and its root", got)
	}

	// A plain directory is outside git and returns the zero value, so it
	// reports no root.
	if got := detectGit(t.TempDir()); got != (gitInfo{}) || got.root != "" {
		t.Fatalf("plain directory = %+v, want zero and no root", got)
	}
}

// stubProbeGit puts a git on PATH that answers the probe's commands, so a test
// can drive detectGit through answers real git only gives in a special
// repository. DM_STUB_UNBORN fails the log line and DM_STUB_NOUPSTREAM fails
// the two upstream commands, the way an unborn HEAD and an untracked branch do.
func stubProbeGit(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
shift 2
case "$*" in
"rev-parse --abbrev-ref HEAD --show-toplevel --git-dir --git-common-dir")
	printf 'main\n/work/repo\n/work/repo/.git\n/work/repo/.git\n' ;;
"status --porcelain=v1 -z --untracked-files=no")
	printf ' M file.txt\0' ;;
"diff --numstat HEAD")
	printf '2\t1\tfile.txt\n3\t0\tnew.txt\n' ;;
"log -1 --format=%h%x00%s")
	if [ -n "$DM_STUB_UNBORN" ]; then exit 128; fi
	printf 'abc1234\0fix the rail\n' ;;
"rev-parse --abbrev-ref --symbolic-full-name @{u}")
	if [ -n "$DM_STUB_NOUPSTREAM" ]; then exit 128; fi
	printf 'origin/main\n' ;;
"rev-list --left-right --count @{u}...HEAD")
	if [ -n "$DM_STUB_NOUPSTREAM" ]; then exit 128; fi
	printf '2\t5\n' ;;
*)
	exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestDetectGitFacts drives the probe through a stubbed git. A failure that is
// not a timeout is a real answer: an unborn HEAD leaves the head fields empty
// and an untracked branch leaves the upstream counts empty, and neither marks
// the probe inconclusive.
func TestDetectGitFacts(t *testing.T) {
	stubProbeGit(t)

	want := gitInfo{
		branch: "main", root: "/work/repo", inRepo: true,
		dirty: true, added: 5, deleted: 1, files: 2,
		head: "abc1234", headSubject: "fix the rail",
		upstream: "origin/main", ahead: 5, behind: 2,
	}
	if got := detectGit("/work/repo"); got != want {
		t.Fatalf("normal probe = %+v, want %+v", got, want)
	}

	t.Run("no upstream", func(t *testing.T) {
		t.Setenv("DM_STUB_NOUPSTREAM", "1")
		got := detectGit("/work/repo")
		if got.timeout {
			t.Fatalf("a missing upstream marked the probe inconclusive: %+v", got)
		}
		if got.upstream != "" || got.ahead != 0 || got.behind != 0 {
			t.Fatalf("no upstream = %+v, want the counts left empty", got)
		}
		if got.head != "abc1234" || got.headSubject != "fix the rail" {
			t.Fatalf("no upstream lost the head line: %+v", got)
		}
	})

	t.Run("unborn head", func(t *testing.T) {
		t.Setenv("DM_STUB_UNBORN", "1")
		got := detectGit("/work/repo")
		if got.timeout {
			t.Fatalf("an unborn HEAD marked the probe inconclusive: %+v", got)
		}
		if got.head != "" || got.headSubject != "" {
			t.Fatalf("unborn HEAD = %+v, want the head line left empty", got)
		}
		if !got.inRepo || got.upstream != "origin/main" || got.ahead != 5 {
			t.Fatalf("unborn HEAD lost the other facts: %+v", got)
		}
	})
}

// TestDetectGitTimeout pins the timeout classification: a git that hangs until
// the one second limit cuts it off is an inconclusive probe, not a real
// not-a-repository answer.
func TestDetectGitTimeout(t *testing.T) {
	bin := t.TempDir()
	// exec makes the script the git process itself, so the context kill ends
	// the sleep and nothing lingers.
	script := "#!/bin/sh\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	start := time.Now()
	got := detectGit(t.TempDir())
	if !got.timeout || got.inRepo {
		t.Fatalf("hanging git = %+v, want an inconclusive probe outside a repo", got)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("hanging git took %s, want the one second cut", d)
	}
}
