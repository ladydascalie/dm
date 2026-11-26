package client

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"dm/internal/paths"
	rel "dm/internal/release"
	"dm/internal/version"
)

// The update check is the boot notice: an attach reads a cached answer and,
// when that answer is stale, leaves one detached `dm __update-check` behind to
// refresh it. Nothing here blocks the attach, and nothing here prints inside a
// frame: the notice is one line on stderr, written before the alternate screen.

const (
	// updateFile is the cache file, under the state root.
	updateFile = "update.json"

	// updateTTL is how long a successful answer stays fresh.
	updateTTL = 24 * time.Hour

	// updateFailTTL is how long a failed answer stays fresh. It is shorter
	// than a success but not zero, so an offline start does not retry on
	// every attach.
	updateFailTTL = time.Hour

	// updateNoticeTTL is the window the notice keeps quiet about one version.
	updateNoticeTTL = 24 * time.Hour

	// updateSkipEnv turns the check off entirely.
	updateSkipEnv = "DM_NO_UPDATE_CHECK"

	// ciEnv is the variable a CI runner sets. A CI machine is not an
	// operator waiting for a notice.
	ciEnv = "CI"
)

// updateCache is $DM_STATE/update.json.
type updateCache struct {
	CheckedAt  time.Time `json:"checkedAt"`
	Latest     string    `json:"latest"`
	Notified   string    `json:"notified,omitempty"`
	NotifiedAt time.Time `json:"notifiedAt,omitempty"`
}

// updatePath is the cache file under one state root.
func updatePath(stateDir string) string {
	return filepath.Join(stateDir, updateFile)
}

// readUpdateCache reads the cache. A missing, unreadable or corrupt file is
// simply a cache that is not there: ok is false and the caller checks again.
func readUpdateCache(stateDir string) (updateCache, bool) {
	b, err := os.ReadFile(updatePath(stateDir))
	if err != nil {
		return updateCache{}, false
	}
	var c updateCache
	if err := json.Unmarshal(b, &c); err != nil {
		return updateCache{}, false
	}
	return c, true
}

// writeUpdateCache writes the cache through a temporary file and a rename, so
// a client reading while the refresh writes sees the old file or the new one.
// A failure is the caller's to ignore.
func writeUpdateCache(stateDir string, c updateCache) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(stateDir, updateFile+".*")
	if err != nil {
		return err
	}
	name := f.Name()
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, updatePath(stateDir)); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// updateFresh reports whether the cache is too recent to check again. A
// success is fresh for a day, a failure for an hour. An absent CheckedAt is
// stale.
func updateFresh(c updateCache, now time.Time) bool {
	if c.CheckedAt.IsZero() {
		return false
	}
	ttl := updateFailTTL
	if c.Latest != "" {
		ttl = updateTTL
	}
	return now.Before(c.CheckedAt.Add(ttl))
}

// shouldCheck reports whether the attach should leave a refresh behind: not
// switched off, not on CI, running a release, and holding a stale or absent
// answer.
func shouldCheck(stateDir string, now time.Time) bool {
	if os.Getenv(updateSkipEnv) != "" || os.Getenv(ciEnv) != "" {
		return false
	}
	if !version.IsRelease(version.Version) {
		return false
	}
	c, ok := readUpdateCache(stateDir)
	if !ok {
		return true
	}
	return !updateFresh(c, now)
}

// updateNotice returns the one line the attach prints, or "" when there is
// nothing to say: either side is not a release, the latest is not newer, or
// this version was already announced within the day. When it does print, it
// records the announcement in the cache, so the notice lands once per version
// per day rather than once per attach. A cache write that fails is ignored.
func updateNotice(stateDir, current string, now time.Time) string {
	if !version.IsRelease(current) {
		return ""
	}
	c, ok := readUpdateCache(stateDir)
	if !ok || !version.IsRelease(c.Latest) {
		return ""
	}
	if version.Compare(c.Latest, current) <= 0 {
		return ""
	}
	if c.Notified == c.Latest && now.Before(c.NotifiedAt.Add(updateNoticeTTL)) {
		return ""
	}
	c.Notified, c.NotifiedAt = c.Latest, now
	_ = writeUpdateCache(stateDir, c)
	return fmt.Sprintf("dm %s is out, run dm upgrade", c.Latest)
}

// RunUpdateCheck is the `dm __update-check` process role: one release read,
// one cache write, exit. A failed refresh is not a user-visible failure, so it
// reports nothing and always leaves a cache behind.
func RunUpdateCheck() {
	dir := paths.StateDir()
	cache, _ := readUpdateCache(dir)
	cache.CheckedAt = time.Now()
	cache.Latest = ""
	ctx, cancel := context.WithTimeout(context.Background(), rel.Timeout)
	defer cancel()
	if latest, err := rel.NewReader().Latest(ctx); err == nil {
		cache.Latest = strings.TrimPrefix(latest.Tag, "v")
	}
	_ = writeUpdateCache(dir, cache)
}

// spawnUpdateCheck starts the refresh as its own process, in its own session
// and off the terminal, and returns without waiting. A spawn that fails is
// ignored: the next attach tries again.
func spawnUpdateCheck() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return
	}
	defer func() { _ = devNull.Close() }()
	cmd := exec.Command(exe, "__update-check")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devNull, devNull, devNull
	detachUpdate(cmd)
	if err := cmd.Start(); err != nil {
		return
	}
	// The attach does not wait for the answer, but the child must be reaped
	// before it turns into a zombie, so a goroutine does it.
	go func() { _ = cmd.Wait() }()
}
