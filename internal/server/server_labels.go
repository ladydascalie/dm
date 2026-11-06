package server

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// branchTTL is how long a detected git label stays fresh. A stale entry is
// served while a background check runs, so a snapshot never waits on git.
const branchTTL = 10 * time.Second

type branchEntry struct {
	info    gitInfo
	at      time.Time
	loading bool
}

// gitInfo is what the rail shows for a working directory: the branch, for a
// linked worktree its own folder name, the repository root, and the
// uncommitted change counts.
type gitInfo struct {
	branch   string
	worktree string
	// root is the repository's toplevel as git reports it, cleaned. It is the
	// folder a linked worktree shares with its main checkout.
	root    string
	inRepo  bool
	dirty   bool
	added   int
	deleted int
}

// gitOutput runs one git command against cwd with the one second timeout the
// rail can afford.
func gitOutput(cwd string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, args...)...).Output()
}

// parseNumstat sums a git diff --numstat report. A binary file prints a dash
// instead of a count; that column adds nothing.
func parseNumstat(out []byte) (added, deleted int) {
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if n, err := strconv.Atoi(f[0]); err == nil {
			added += n
		}
		if n, err := strconv.Atoi(f[1]); err == nil {
			deleted += n
		}
	}
	return added, deleted
}

// detectGit is a variable so tests can stub git.
var detectGit = func(cwd string) gitInfo {
	out, err := gitOutput(cwd, "rev-parse", "--abbrev-ref", "HEAD",
		"--show-toplevel", "--git-dir", "--git-common-dir")
	if err != nil {
		return gitInfo{}
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) < 4 {
		return gitInfo{}
	}
	// A detached HEAD prints the literal HEAD. Leave the branch empty so the
	// worktree folder, or the window name, labels the rail line.
	info := gitInfo{inRepo: true}
	if b := lines[0]; b != "HEAD" {
		info.branch = b
	}
	root := filepath.Clean(lines[1])
	info.root = root
	gd, cd := lines[2], lines[3]
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(cwd, gd)
	}
	if !filepath.IsAbs(cd) {
		cd = filepath.Join(cwd, cd)
	}
	// A linked worktree has its own git dir under the main repo's worktrees
	// directory, while the common dir stays in the main checkout.
	if filepath.Clean(gd) != filepath.Clean(cd) {
		info.worktree = filepath.Base(root)
	}
	// Tracked changes only: an untracked file is not a change the rail counts.
	if st, err := gitOutput(cwd, "status", "--porcelain=v1", "-z",
		"--untracked-files=no"); err == nil {
		info.dirty = strings.Trim(string(st), "\x00") != ""
	}
	if ns, err := gitOutput(cwd, "diff", "--numstat", "HEAD"); err == nil {
		info.added, info.deleted = parseNumstat(ns)
	}
	return info
}

// gitFor returns the cached git labels for a working directory and starts a
// background check when the entry is missing or stale. It never runs git on
// the caller's goroutine, because snapshots hold s.mu.
func (s *server) gitFor(cwd string) gitInfo {
	if cwd == "" {
		return gitInfo{}
	}
	now := time.Now()
	s.branchMu.Lock()
	if s.branches == nil {
		s.branches = map[string]*branchEntry{}
	}
	e := s.branches[cwd]
	if e == nil {
		s.branches[cwd] = &branchEntry{at: now, loading: true}
		s.branchMu.Unlock()
		go s.loadGit(cwd)
		return gitInfo{}
	}
	info := e.info
	load := now.Sub(e.at) > branchTTL && !e.loading
	if load {
		e.loading = true
	}
	s.branchMu.Unlock()
	if load {
		go s.loadGit(cwd)
	}
	return info
}

func (s *server) loadGit(cwd string) {
	info := detectGit(cwd)
	s.branchMu.Lock()
	e := s.branches[cwd]
	if e == nil {
		e = &branchEntry{}
		s.branches[cwd] = e
	}
	changed := e.info != info
	e.info, e.at, e.loading = info, time.Now(), false
	s.branchMu.Unlock()
	if changed {
		s.pushSnapshot()
	}
}
