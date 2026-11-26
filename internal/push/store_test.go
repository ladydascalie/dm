package push_test

import (
	"errors"
	"io/fs"
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
	names := dirNames(t, filepath.Dir(path))
	if want := []string{"subscriptions.json", "subscriptions.json.lock"}; !reflect.DeepEqual(names, want) {
		t.Errorf("directory holds %v, want %v", names, want)
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

// dirNames returns the names in dir, sorted by ReadDir.
func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}

// TestStoreTwoStoresShareOneFile proves two Stores on one path keep each other's
// entries. Each Add reloads the file with the lock file held, so the second
// writer starts from what the first wrote instead of from its own stale list.
func TestStoreTwoStoresShareOneFile(t *testing.T) {
	path := storePath(t)
	web, err := push.Load(path)
	if err != nil {
		t.Fatalf("Load web: %v", err)
	}
	daemon, err := push.Load(path)
	if err != nil {
		t.Fatalf("Load daemon: %v", err)
	}
	if err := web.Add(sub("https://push.example.com/a", "web")); err != nil {
		t.Fatalf("web Add a: %v", err)
	}
	if err := daemon.Add(sub("https://push.example.com/b", "daemon")); err != nil {
		t.Fatalf("daemon Add b: %v", err)
	}
	if err := web.Add(sub("https://push.example.com/c", "web")); err != nil {
		t.Fatalf("web Add c: %v", err)
	}

	reloaded, err := push.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got := endpoints(reloaded.List())
	want := []string{
		"https://push.example.com/a",
		"https://push.example.com/b",
		"https://push.example.com/c",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reload holds %v, want %v: a writer dropped another Store's entry", got, want)
	}
}

// TestStoreRemoveSeenByOtherStore proves a Remove through one Store survives the
// other Store's next write, even when that Store still holds the removed entry
// in memory: the Add reloads the file first and does not resurrect it.
func TestStoreRemoveSeenByOtherStore(t *testing.T) {
	path := storePath(t)
	web, err := push.Load(path)
	if err != nil {
		t.Fatalf("Load web: %v", err)
	}
	for _, endpoint := range []string{"https://push.example.com/a", "https://push.example.com/b"} {
		if err := web.Add(sub(endpoint, "web")); err != nil {
			t.Fatalf("web Add %s: %v", endpoint, err)
		}
	}
	daemon, err := push.Load(path)
	if err != nil {
		t.Fatalf("Load daemon: %v", err)
	}
	removed, err := daemon.Remove("https://push.example.com/a")
	if err != nil || !removed {
		t.Fatalf("daemon Remove a = %v, %v; want true, nil", removed, err)
	}
	reloaded, err := push.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got, want := endpoints(reloaded.List()), []string{"https://push.example.com/b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reload after the Remove holds %v, want %v", got, want)
	}

	// web never saw the removal; its next write must still not bring a back.
	if err := web.Add(sub("https://push.example.com/c", "web")); err != nil {
		t.Fatalf("web Add c: %v", err)
	}
	reloaded, err = push.Load(path)
	if err != nil {
		t.Fatalf("reload after the later Add: %v", err)
	}
	want := []string{"https://push.example.com/b", "https://push.example.com/c"}
	if got := endpoints(reloaded.List()); !reflect.DeepEqual(got, want) {
		t.Errorf("reload after the later Add holds %v, want %v", got, want)
	}
}

// endpoints returns the endpoints of subs in order.
func endpoints(subs []push.Subscription) []string {
	got := make([]string, len(subs))
	for i, sub := range subs {
		got[i] = sub.Endpoint
	}
	return got
}

// TestStoreNarrowModeLeftAlone proves Load leaves a read-only store at 0400. A
// mode that grants nothing beyond 0600 is not a leak, and widening it would
// undo an operator's protection.
func TestStoreNarrowModeLeftAlone(t *testing.T) {
	path := storePath(t)
	store, err := push.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Add(sub("https://push.example.com/a", "phone")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatalf("narrow mode: %v", err)
	}
	reloaded, err := push.Load(path)
	if err != nil {
		t.Fatalf("Load on a 0400 file: %v", err)
	}
	if got := endpoints(reloaded.List()); !reflect.DeepEqual(got, []string{"https://push.example.com/a"}) {
		t.Errorf("reload = %v, want the one stored entry", got)
	}
	if mode := fileMode(t, path); mode != 0o400 {
		t.Errorf("mode after Load = %o, want 400", mode)
	}
}

// TestStoreRemoveAbsentCreatesNothing proves a Remove whose store file is absent
// returns (false, nil) and leaves the tree as it found it. dm ls lists every
// directory under sessions, so a directory recreated here would be a phantom
// session on the next reset.
func TestStoreRemoveAbsentCreatesNothing(t *testing.T) {
	const endpoint = "https://push.example.com/a"

	// The session directory itself is gone: a reset removed it after this Store
	// was loaded.
	dir := filepath.Join(t.TempDir(), "work")
	path := filepath.Join(dir, "subscriptions.json")
	gone, err := push.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	removed, err := gone.Remove(endpoint)
	if err != nil || removed {
		t.Fatalf("Remove on an absent store = %v, %v; want false, nil", removed, err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stat %s = %v after Remove, want the directory to be absent", dir, err)
	}

	// The directory exists but holds no store yet: a session whose first
	// subscribe failed. Remove must not add a lock file to it.
	existing := t.TempDir()
	live, err := push.Load(filepath.Join(existing, "subscriptions.json"))
	if err != nil {
		t.Fatalf("Load in an existing directory: %v", err)
	}
	removed, err = live.Remove(endpoint)
	if err != nil || removed {
		t.Fatalf("Remove on an absent store in an existing directory = %v, %v; want false, nil", removed, err)
	}
	if names := dirNames(t, existing); len(names) != 0 {
		t.Errorf("Remove left %v behind, want an untouched directory", names)
	}

	// Add is the writer: it still creates the directory and the store the
	// Remove declined to create.
	if err := gone.Add(sub(endpoint, "phone")); err != nil {
		t.Fatalf("Add after the absent Remove: %v", err)
	}
	reloaded, err := push.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := endpoints(reloaded.List()); !reflect.DeepEqual(got, []string{endpoint}) {
		t.Errorf("reload = %v, want the added endpoint", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Add did not create the store file: %v", err)
	}
}
