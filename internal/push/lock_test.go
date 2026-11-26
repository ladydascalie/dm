//go:build !js

package push_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"dm/internal/push"
)

// TestLockFileSerializesAdd proves the lock is not decorative. The test opens
// the store's lock file and takes the same flock the package takes, then asserts
// a second Add writes nothing until that flock is released.
func TestLockFileSerializesAdd(t *testing.T) {
	path := storePath(t)
	store, err := push.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	held, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open lock file: %v", err)
	}
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatalf("flock: %v", err)
	}

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		done <- store.Add(sub("https://push.example.com/a", "phone"))
	}()
	<-started

	select {
	case err := <-done:
		t.Fatalf("Add returned %v while the lock file was held, want it to block", err)
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the store file appeared while the lock file was held: %v", err)
	}

	if err := held.Close(); err != nil {
		t.Fatalf("release the lock: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Add after the lock was released: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Add did not finish after the lock was released")
	}

	reloaded, err := push.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got, want := endpoints(reloaded.List()), []string{"https://push.example.com/a"}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("reload holds %v, want %v", got, want)
	}
}

// TestLockFileMode proves the lock file is 0600 like the file it guards.
func TestLockFileMode(t *testing.T) {
	path := storePath(t)
	store, err := push.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Add(sub("https://push.example.com/a", "phone")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	info, err := os.Stat(path + ".lock")
	if err != nil {
		t.Fatalf("stat lock file: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("lock file mode = %o, want 600", mode)
	}
}

// TestLockFileSerializesKeyCreation proves the key path takes the same lock:
// while a holder owns <key path>.lock, LoadOrCreateKeys waits instead of seeing
// no file and generating a second pair.
func TestLockFileSerializesKeyCreation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vapid.json")
	held, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open lock file: %v", err)
	}
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatalf("flock: %v", err)
	}

	type result struct {
		keys *push.Keys
		err  error
	}
	started := make(chan struct{})
	done := make(chan result, 1)
	go func() {
		close(started)
		keys, err := push.LoadOrCreateKeys(path)
		done <- result{keys: keys, err: err}
	}()
	<-started

	select {
	case got := <-done:
		t.Fatalf("LoadOrCreateKeys returned %v while the lock file was held, want it to block", got.err)
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a key file appeared while the lock file was held: %v", err)
	}

	if err := held.Close(); err != nil {
		t.Fatalf("release the lock: %v", err)
	}
	var created *push.Keys
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("LoadOrCreateKeys after the lock was released: %v", got.err)
		}
		created = got.keys
	case <-time.After(10 * time.Second):
		t.Fatal("LoadOrCreateKeys did not finish after the lock was released")
	}
	onDisk, err := push.LoadKeys(path)
	if err != nil {
		t.Fatalf("LoadKeys: %v", err)
	}
	if onDisk.PublicKey() != created.PublicKey() {
		t.Errorf("stored public key = %q, want the returned one %q", onDisk.PublicKey(), created.PublicKey())
	}
}
