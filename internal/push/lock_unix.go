//go:build !js

package push

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// fileLock is the exclusive claim on one data file, held through its lock file.
//
// The lock file is created on first use and never removed: it goes with the
// directory, and a release that unlinked it would let a later writer lock a
// fresh inode while an earlier holder still owns the old one.
type fileLock struct{ f *os.File }

// lockFile takes the exclusive lock beside path and waits for any other holder.
// The caller holds it across the whole read-modify-write, so the reload inside
// the lock sees every entry the previous holder wrote to disk.
func lockFile(path string) (*fileLock, error) {
	name := lockPath(path)
	dir := filepath.Dir(name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("push: create %s: %w", dir, err)
	}
	f, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("push: open lock %s: %w", name, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("push: lock %s: %w", name, err)
	}
	return &fileLock{f: f}, nil
}

// release drops the lock. Closing the file is the release, and the file stays
// in place for the next holder.
func (l *fileLock) release() error {
	if l == nil || l.f == nil {
		return nil
	}
	return l.f.Close()
}
