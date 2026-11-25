package paths

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestLockSessionIsExclusive pins the lock's contract: a second taker gets
// ErrSessionLocked, the non-creating probe sees the holder, and Release frees
// the file without unlinking it.
func TestLockSessionIsExclusive(t *testing.T) {
	root := t.TempDir()
	dir := SessionDirAt(root, "s")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	held, err := SessionLockHeld(root, "s")
	if err != nil || held {
		t.Fatalf("probe on a missing lock = %v, %v; want false, nil", held, err)
	}
	lock, err := LockSession(root, "s")
	if err != nil {
		t.Fatalf("LockSession: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "server.lock")); err != nil {
		t.Fatalf("LockSession created no lock file: %v", err)
	}
	if _, err := LockSession(root, "s"); !errors.Is(err, ErrSessionLocked) {
		t.Fatalf("second LockSession = %v, want ErrSessionLocked", err)
	}
	if held, err := SessionLockHeld(root, "s"); err != nil || !held {
		t.Fatalf("probe on a held lock = %v, %v; want true, nil", held, err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "server.lock")); err != nil {
		t.Fatalf("Release unlinked the lock file: %v", err)
	}
	if held, err := SessionLockHeld(root, "s"); err != nil || held {
		t.Fatalf("probe after Release = %v, %v; want false, nil", held, err)
	}
}

// TestStillOursFollowsThePathsInode proves the ownership check answers for the
// file at the path, not for the fd: a replaced lock file is a successor's.
func TestStillOursFollowsThePathsInode(t *testing.T) {
	root := t.TempDir()
	dir := SessionDirAt(root, "s")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	lock, err := LockSession(root, "s")
	if err != nil {
		t.Fatalf("LockSession: %v", err)
	}
	defer func() { _ = lock.Release() }()
	if !lock.StillOurs() {
		t.Fatal("a fresh lock does not own its file")
	}
	path := filepath.Join(dir, "server.lock")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if lock.StillOurs() {
		t.Fatal("a replaced lock file still counts as ours")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if lock.StillOurs() {
		t.Fatal("a missing lock file still counts as ours")
	}
}
