package server

import (
	"os"
	"path/filepath"
	"testing"

	"dm/internal/paths"
	"dm/internal/session"
)

// lockServer builds an in-process server for one session directory and takes
// its lock, the way Run does, so a test can drive endSession against it.
func lockServer(t *testing.T, name string) (*server, string) {
	t.Helper()
	root := t.TempDir()
	st := session.NewStateAt(root, name)
	dir := st.SessionDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	lock, err := paths.LockSession(root, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	s := newServer(st, nil, nil)
	s.lock = lock
	s.exit = func(int) {}
	return s, dir
}

// TestEndSessionRemovesItsOwnDirectory is the legal case: the server holds the
// lock for the directory it removes.
func TestEndSessionRemovesItsOwnDirectory(t *testing.T) {
	s, dir := lockServer(t, "ended")
	s.endSession()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("endSession left its own directory behind: %v", err)
	}
}

// TestEndSessionKeepsASuccessorsDirectory pins the guard: endSession removes
// the directory only while the lock file at the path is still the inode the
// server locked. A reset that replaced the directory under a vanishing server
// hands the path to a successor, and the outgoing server must not delete it.
func TestEndSessionKeepsASuccessorsDirectory(t *testing.T) {
	s, dir := lockServer(t, "ended")
	// The reset's effect: the lock file at the path is a new inode, so the
	// directory is a successor's.
	if err := os.Remove(filepath.Join(dir, "server.lock")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s.endSession()
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("endSession removed a successor's directory: %v", err)
	}
}
