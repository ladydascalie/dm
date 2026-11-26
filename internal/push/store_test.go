package push_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"dm/internal/push"
)

// storePath returns a subscription file path inside a fresh temp directory.
func storePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "subscriptions.json")
}

// sub builds a subscription with a stable timestamp and a label.
func sub(endpoint, label string) push.Subscription {
	return push.Subscription{
		Endpoint: endpoint,
		P256DH:   "p256dh-" + endpoint,
		Auth:     "auth-" + endpoint,
		At:       time.Now().UTC().Truncate(time.Second),
		Label:    label,
	}
}

// TestStoreAddReplacesEndpoint proves the endpoint is the identity: a second
// subscribe from the same phone leaves one entry, the newer one.
func TestStoreAddReplacesEndpoint(t *testing.T) {
	path := storePath(t)
	store, err := push.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Add(sub("https://push.example.com/a", "first")); err != nil {
		t.Fatalf("Add first: %v", err)
	}
	if err := store.Add(sub("https://push.example.com/b", "other")); err != nil {
		t.Fatalf("Add other: %v", err)
	}
	if err := store.Add(sub("https://push.example.com/a", "second")); err != nil {
		t.Fatalf("Add second: %v", err)
	}
	subs := store.List()
	if len(subs) != 2 {
		t.Fatalf("List returned %d entries, want 2", len(subs))
	}
	if subs[0].Endpoint != "https://push.example.com/a" || subs[0].Label != "second" {
		t.Errorf("entry = %+v, want the newer label on endpoint a", subs[0])
	}
	if subs[1].Endpoint != "https://push.example.com/b" {
		t.Errorf("second entry = %+v, want endpoint b", subs[1])
	}
}

// TestStoreSurvivesReload proves a rewrite lands on disk: a fresh Load sees the
// replaced entry and not the one it replaced.
func TestStoreSurvivesReload(t *testing.T) {
	path := storePath(t)
	store, err := push.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Add(sub("https://push.example.com/a", "first")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := store.Add(sub("https://push.example.com/a", "second")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	reloaded, err := push.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got, want := reloaded.List(), store.List(); !reflect.DeepEqual(got, want) {
		t.Errorf("reload = %+v, want %+v", got, want)
	}
	if reloaded.List()[0].Label != "second" {
		t.Errorf("reload kept the replaced entry, want the newer one")
	}
}

// TestStoreRemove proves Remove reports whether it deleted something and that
// the removal survives a reload.
func TestStoreRemove(t *testing.T) {
	path := storePath(t)
	store, err := push.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, endpoint := range []string{"https://push.example.com/a", "https://push.example.com/b"} {
		if err := store.Add(sub(endpoint, "phone")); err != nil {
			t.Fatalf("Add %s: %v", endpoint, err)
		}
	}
	removed, err := store.Remove("https://push.example.com/a")
	if err != nil || !removed {
		t.Fatalf("Remove a = %v, %v; want true, nil", removed, err)
	}
	if removed, err := store.Remove("https://push.example.com/a"); err != nil || removed {
		t.Fatalf("second Remove a = %v, %v; want false, nil", removed, err)
	}
	reloaded, err := push.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	subs := reloaded.List()
	if len(subs) != 1 || subs[0].Endpoint != "https://push.example.com/b" {
		t.Fatalf("reload = %+v, want only endpoint b", subs)
	}
}

// TestStoreListIsCopy proves a caller cannot edit the store through List.
func TestStoreListIsCopy(t *testing.T) {
	store, err := push.Load(storePath(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Add(sub("https://push.example.com/a", "phone")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	subs := store.List()
	subs[0].Label = "edited"
	if got := store.List()[0].Label; got != "phone" {
		t.Errorf("List returned the store's own slice: label = %q", got)
	}
}

// TestStoreMissingFile proves a fresh install is an empty store, not an error.
func TestStoreMissingFile(t *testing.T) {
	store, err := push.Load(storePath(t))
	if err != nil {
		t.Fatalf("Load of a missing file: %v", err)
	}
	if subs := store.List(); len(subs) != 0 || subs == nil {
		t.Errorf("List = %#v, want an empty, non-nil list", subs)
	}
}

// TestStoreUnreadableFile proves a corrupt store is an error the caller sees,
// not a silent empty list that the next Add overwrites.
func TestStoreUnreadableFile(t *testing.T) {
	path := storePath(t)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := push.Load(path); err == nil {
		t.Fatal("Load returned no error for a corrupt file")
	}

	dirPath := filepath.Join(t.TempDir(), "store-is-a-directory")
	if err := os.Mkdir(dirPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := push.Load(dirPath); err == nil {
		t.Fatal("Load returned no error for a path that is a directory")
	}
}

// TestStoreFileMode proves the store lands at 0600 and leaves no temp file
// behind.
func TestStoreFileMode(t *testing.T) {
	path := storePath(t)
	store, err := push.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Add(sub("https://push.example.com/a", "phone")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode = %o, want 600", mode)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "subscriptions.json" {
		names := make([]string, len(entries))
		for i, entry := range entries {
			names[i] = entry.Name()
		}
		t.Errorf("directory holds %v, want only the store file", names)
	}
}

// TestStoreFailedWriteKeepsMemory proves a failed write leaves the store as it
// was, so memory and disk never disagree.
func TestStoreFailedWriteKeepsMemory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write through a read-only directory")
	}
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "subscriptions.json")
	store, err := push.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Add(sub("https://push.example.com/a", "phone")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	before := store.List()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	if err := store.Add(sub("https://push.example.com/b", "phone")); err == nil {
		t.Fatal("Add returned no error in a read-only directory")
	}
	if got := store.List(); !reflect.DeepEqual(got, before) {
		t.Errorf("List after a failed write = %+v, want %+v", got, before)
	}
}

// TestStoreModeRepairedOnLoad proves a store file whose mode was widened is
// pulled back to 0600 on load: it holds every phone's auth secret and p256dh.
func TestStoreModeRepairedOnLoad(t *testing.T) {
	path := storePath(t)
	store, err := push.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Add(sub("https://push.example.com/a", "phone")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("widen mode: %v", err)
	}
	reloaded, err := push.Load(path)
	if err != nil {
		t.Fatalf("Load after chmod 0644: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode after load = %o, want 600", mode)
	}
	if got := reloaded.List(); len(got) != 1 || got[0].Endpoint != "https://push.example.com/a" {
		t.Errorf("reload = %+v, want the one stored entry", got)
	}
}

// TestStoreFailedOpenLeavesState proves a directory that cannot be opened fails
// the write before the rename: memory and the file keep the old entries, and a
// later Add from the same store still holds both.
func TestStoreFailedOpenLeavesState(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can open a directory that lacks read permission")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "subscriptions.json")
	store, err := push.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	first := sub("https://push.example.com/a", "phone")
	if err := store.Add(first); err != nil {
		t.Fatalf("Add first: %v", err)
	}
	// Write and search, but no read, so the directory opens with EACCES while a
	// temp file can still be created in it.
	if err := os.Chmod(dir, 0o300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	second := sub("https://push.example.com/b", "phone")
	if err := store.Add(second); err == nil {
		t.Fatal("Add returned no error in a directory that cannot be opened")
	}
	if got := store.List(); !reflect.DeepEqual(got, []push.Subscription{first}) {
		t.Errorf("memory after the failed write = %+v, want only the first entry", got)
	}

	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	reloaded, err := push.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.List(); !reflect.DeepEqual(got, []push.Subscription{first}) {
		t.Errorf("file after the failed write = %+v, want only the first entry", got)
	}

	// The store still holds the first entry, so this write keeps both.
	if err := store.Add(second); err != nil {
		t.Fatalf("Add after the failed write: %v", err)
	}
	reloaded, err = push.Load(path)
	if err != nil {
		t.Fatalf("reload after the later Add: %v", err)
	}
	if got := reloaded.List(); !reflect.DeepEqual(got, []push.Subscription{first, second}) {
		t.Errorf("file after the later Add = %+v, want both entries", got)
	}
}
