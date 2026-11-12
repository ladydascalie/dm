package server

import (
	"fmt"
	"testing"
	"time"

	"dm/internal/session"
	"dm/internal/testutil"
)

// stubDetect answers every cwd with the same gitInfo.
func stubDetect(info gitInfo) func(string) gitInfo {
	return func(string) gitInfo { return info }
}

// setEntry plants a cache entry the way an earlier good probe left it.
func setEntry(s *server, cwd string, info gitInfo) {
	s.branchMu.Lock()
	if s.branches == nil {
		s.branches = map[string]*branchEntry{}
	}
	s.branches[cwd] = &branchEntry{info: info, at: time.Now()}
	s.branchMu.Unlock()
}

// getEntry reads one cache entry under the cache lock.
func getEntry(s *server, cwd string) *branchEntry {
	s.branchMu.Lock()
	defer s.branchMu.Unlock()
	return s.branches[cwd]
}

// goodGit is a last good answer the tests keep alive across a timeout.
var goodGit = gitInfo{
	branch: "main", root: "/work/dm", inRepo: true,
	dirty: true, added: 7, deleted: 3,
}

// TestSlowProbeKeepsLastGood pins the timeout rule: a probe that hits the one
// second limit keeps the previous branch and dirty counts in the cache and
// pins the entry stale, instead of reading the zero value as clean or outside
// a repository.
func TestSlowProbeKeepsLastGood(t *testing.T) {
	t.Parallel()
	st := session.NewStateAt(t.TempDir(), "keeplast")
	id := testutil.RootPane(st)
	st.Panes[id].Cwd = "/work/dm"
	s := railServer(st)
	const cwd = "/work/dm"
	setEntry(s, cwd, goodGit)
	s.detect = stubDetect(gitInfo{timeout: true})

	s.loadGit(cwd)

	e := getEntry(s, cwd)
	if e == nil {
		t.Fatal("a timed-out probe dropped the cache entry")
	}
	if e.info != goodGit {
		t.Fatalf("timed-out probe stored %+v, want the last good answer %+v", e.info, goodGit)
	}
	if !e.stale {
		t.Fatal("a timed-out probe left the entry unpinned")
	}
}

// TestNotARepoReplacesCache pins that a real not-a-repository answer, which is
// the zero value with no timeout, replaces the cache as any real answer does.
func TestNotARepoReplacesCache(t *testing.T) {
	t.Parallel()
	st := session.NewStateAt(t.TempDir(), "notarepo")
	id := testutil.RootPane(st)
	st.Panes[id].Cwd = "/work/plain"
	s := railServer(st)
	const cwd = "/work/plain"
	setEntry(s, cwd, goodGit)
	s.detect = stubDetect(gitInfo{})

	s.loadGit(cwd)

	e := getEntry(s, cwd)
	if e == nil {
		t.Fatal("a real answer dropped the cache entry")
	}
	if e.info != (gitInfo{}) {
		t.Fatalf("a real not-a-repo answer stored %+v, want the zero value", e.info)
	}
	if e.stale {
		t.Fatal("a real answer left the entry pinned stale")
	}
}

// TestFreshAnswerReplacesCache pins that a real answer replaces a kept
// last-good entry and clears its stale pin.
func TestFreshAnswerReplacesCache(t *testing.T) {
	t.Parallel()
	st := session.NewStateAt(t.TempDir(), "fresh")
	id := testutil.RootPane(st)
	st.Panes[id].Cwd = "/work/dm"
	s := railServer(st)
	const cwd = "/work/dm"
	setEntry(s, cwd, goodGit)
	// A timeout first, so the entry carries the stale pin a fresh answer must
	// clear.
	s.detect = stubDetect(gitInfo{timeout: true})
	s.loadGit(cwd)
	if e := getEntry(s, cwd); e == nil || !e.stale {
		t.Fatalf("entry before the fresh answer = %+v, want stale", e)
	}

	fresh := gitInfo{branch: "lane", root: "/work/dm", inRepo: true, added: 2}
	s.detect = stubDetect(fresh)
	s.loadGit(cwd)

	e := getEntry(s, cwd)
	if e == nil || e.info != fresh || e.stale {
		t.Fatalf("entry after a fresh answer = %+v stale=%v, want %+v unpinned", e, e.stale, fresh)
	}
}

// TestBranchCacheBounds pins the ceiling: more working directories than the
// bound leaves at most branchCacheMax entries.
func TestBranchCacheBounds(t *testing.T) {
	t.Parallel()
	st := session.NewStateAt(t.TempDir(), "bounds")
	s := railServer(st)
	release := make(chan struct{})
	s.detect = func(string) gitInfo {
		<-release
		return gitInfo{}
	}
	for i := 0; i < branchCacheMax+5; i++ {
		s.gitFor(fmt.Sprintf("/bound/%d", i))
	}
	s.branchMu.Lock()
	n := len(s.branches)
	s.branchMu.Unlock()
	if n > branchCacheMax {
		t.Fatalf("cache holds %d entries, want at most %d", n, branchCacheMax)
	}
	close(release)
}

// TestBranchCacheStartsEmpty pins that the cache is memory only: a fresh
// server starts with no entries and nothing reads one back from disk.
func TestBranchCacheStartsEmpty(t *testing.T) {
	t.Parallel()
	st := session.NewStateAt(t.TempDir(), "emptycache")
	s := railServer(st)
	s.branchMu.Lock()
	n := len(s.branches)
	s.branchMu.Unlock()
	if n != 0 {
		t.Fatalf("a fresh server holds %d branch entries, want none", n)
	}
}

// TestBranchCacheEvictsUnused pins the eviction rule: when no pane points at a
// working directory any more, the next probe drops that entry.
func TestBranchCacheEvictsUnused(t *testing.T) {
	t.Parallel()
	st := session.NewStateAt(t.TempDir(), "evict")
	id := testutil.RootPane(st)
	st.Panes[id].Cwd = "/work/gone"
	s := railServer(st)
	s.detect = stubDetect(goodGit)
	s.loadGit("/work/gone")
	if getEntry(s, "/work/gone") == nil {
		t.Fatal("the pane's cwd had no cache entry after its probe")
	}

	st.Panes[id].Cwd = "/work/kept"
	s.loadGit("/work/kept")

	if getEntry(s, "/work/gone") != nil {
		t.Fatal("a cwd no pane points at stayed in the cache")
	}
	if getEntry(s, "/work/kept") == nil {
		t.Fatal("the new pane cwd lost its cache entry")
	}
}
