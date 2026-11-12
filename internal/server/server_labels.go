package server

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// branchTTL is how long a detected git label stays fresh. A stale entry is
// served while a background check runs, so a snapshot never waits on git.
const branchTTL = 10 * time.Second

// branchCacheMax bounds the branch cache. A session walks more working
// directories than it shows panes for, and every pane comes back to its cwd,
// so a small ceiling loses nothing the rail shows.
const branchCacheMax = 64

type branchEntry struct {
	info    gitInfo
	at      time.Time
	loading bool
	// stale pins the entry as an unconfirmed answer: the last probe timed
	// out, so what the row shows is the last good answer, not a fresh one.
	// It clears when a probe returns a real answer.
	stale bool
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
	// timeout marks an inconclusive probe: git hit the one second limit, so
	// nothing here was observed. The cache keeps the previous answer and pins
	// it stale instead of reading the zero value as clean or outside a repo.
	// A directory genuinely outside git reports the zero value with this
	// false, and that answer replaces the cache.
	timeout bool
}

// errGitTimeout says a git probe hit the one second limit. Exec buries the
// context deadline under the kill signal, so gitOutput raises the sentinel
// itself while it still knows the context expired.
var errGitTimeout = errors.New("git probe timed out")

// gitOutput runs one git command against cwd with the one second timeout the
// rail can afford.
func gitOutput(cwd string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, args...)...).Output()
	if err != nil && ctx.Err() != nil {
		return nil, errGitTimeout
	}
	return out, err
}

// timedOut reports whether a git failure was the one second limit cutting the
// probe short. Any other failure, a missing repo above all, is a real answer.
func timedOut(err error) bool {
	return errors.Is(err, errGitTimeout)
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
		return gitInfo{timeout: timedOut(err)}
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
	st, err := gitOutput(cwd, "status", "--porcelain=v1", "-z",
		"--untracked-files=no")
	if err != nil {
		info.timeout = timedOut(err)
		return info
	}
	info.dirty = strings.Trim(string(st), "\x00") != ""

	ns, err := gitOutput(cwd, "diff", "--numstat", "HEAD")
	if err != nil {
		info.timeout = timedOut(err)
		return info
	}
	info.added, info.deleted = parseNumstat(ns)
	return info
}

// gitDetect returns the server's git detector: the field a test stubs, or
// the production detectGit. A field keeps a stub from touching the package
// global while tests run in parallel.
func (s *server) gitDetect() func(string) gitInfo {
	if s.detect != nil {
		return s.detect
	}
	return detectGit
}

// gitFor returns the cached git labels for a working directory and starts a
// background check when the entry is missing or stale. A timed-out probe left
// the entry pinned stale, and it keeps serving its last good answer past the
// TTL until a real answer replaces it. It never runs git on the caller's
// goroutine, because snapshots hold s.mu.
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
		if len(s.branches) > branchCacheMax {
			s.evictOldestLocked()
		}
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

// evictOldestLocked drops the least recently answered entry. The caller holds
// branchMu and has just grown the map, so the map is above the bound and the
// youngest entry is the one it just added.
func (s *server) evictOldestLocked() {
	oldest := ""
	for cwd, e := range s.branches {
		if oldest == "" || e.at.Before(s.branches[oldest].at) {
			oldest = cwd
		}
	}
	if oldest != "" {
		delete(s.branches, oldest)
	}
}

// branchRefs lists the working directories the session's panes point at. The
// caller must not hold branchMu: this takes s.mu.
func (s *server) branchRefs() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	refs := make(map[string]bool, len(s.st.Panes))
	for _, p := range s.st.Panes {
		if p.Cwd != "" {
			refs[p.Cwd] = true
		}
	}
	return refs
}

// pruneBranches drops cache entries no pane points at any more and holds the
// map to branchCacheMax. A restart starts from an empty map, so nothing here
// reads or writes disk.
func (s *server) pruneBranches() {
	refs := s.branchRefs()
	s.branchMu.Lock()
	defer s.branchMu.Unlock()
	for cwd := range s.branches {
		if !refs[cwd] {
			delete(s.branches, cwd)
		}
	}
	for len(s.branches) > branchCacheMax {
		s.evictOldestLocked()
	}
}

func (s *server) loadGit(cwd string) {
	info := s.gitDetect()(cwd)
	s.branchMu.Lock()
	if s.branches == nil {
		s.branches = map[string]*branchEntry{}
	}
	e := s.branches[cwd]
	if e == nil {
		e = &branchEntry{}
		s.branches[cwd] = e
	}
	changed := false
	if info.timeout {
		// The probe learned nothing. Keep the last good answer and pin the
		// entry stale instead of reporting the pane clean or outside a repo.
		// Resetting at spaces the retries one TTL apart.
		e.stale = true
		e.at, e.loading = time.Now(), false
	} else {
		changed = e.info != info
		e.info, e.at, e.loading, e.stale = info, time.Now(), false, false
	}
	s.branchMu.Unlock()
	if changed {
		s.pushSnapshot()
	}
	s.pruneBranches()
}
