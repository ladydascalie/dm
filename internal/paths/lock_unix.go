//go:build !js

package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// SessionLock is the exclusive claim on one session's directory. Whichever
// process holds it may write or remove that directory; no other process can.
type SessionLock struct {
	f    *os.File
	path string
	info os.FileInfo
}

// LockSession takes the session lock without waiting. It returns
// ErrSessionLocked when another process holds it, so a caller can refuse
// instead of removing a live session's directory. A missing lock file is
// created; its removal belongs to whoever removes the directory.
func LockSession(root, name string) (*SessionLock, error) {
	path := filepath.Join(SessionDirAt(root, name), "server.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrSessionLocked
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &SessionLock{f: f, path: path, info: info}, nil
}

// SessionLockHeld reports whether some process holds the session lock. It never
// creates the lock file: a probe leaves the tree exactly as it found it, so a
// dry run can ask without writing.
func SessionLockHeld(root, name string) (bool, error) {
	path := filepath.Join(SessionDirAt(root, name), "server.lock")
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

// Release drops the lock. It never unlinks the lock file: the file goes with
// the directory, and a release that removed it would leave another holder's
// flock on a deleted inode.
func (l *SessionLock) Release() error {
	if l == nil {
		return nil
	}
	return l.f.Close()
}

// StillOurs reports whether the lock file at its path is still the file this
// lock holds. A false answer means the directory was replaced under the lock:
// a successor owns the path now, and this process must not remove it.
func (l *SessionLock) StillOurs() bool {
	if l == nil {
		return false
	}
	cur, err := os.Stat(l.path)
	if err != nil {
		return false
	}
	return os.SameFile(l.info, cur)
}
