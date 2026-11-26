package push_test

import (
	"bytes"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"dm/internal/push"
)

// TestKeysSurviveReload proves the VAPID pair is generated once and reused: the
// second load returns the same public key and does not rewrite the file, so a
// phone that subscribed once keeps hearing from dm.
func TestKeysSurviveReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vapid.json")
	first, err := push.LoadOrCreateKeys(path)
	if err != nil {
		t.Fatalf("first LoadOrCreateKeys: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	second, err := push.LoadOrCreateKeys(path)
	if err != nil {
		t.Fatalf("second LoadOrCreateKeys: %v", err)
	}
	if first.PublicKey() != second.PublicKey() {
		t.Errorf("public key changed across reload: %q then %q", first.PublicKey(), second.PublicKey())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("the second load rewrote the key file")
	}
}

// TestKeysFile proves the key file is 0600 and holds only the pair.
func TestKeysFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vapid.json")
	if _, err := push.LoadOrCreateKeys(path); err != nil {
		t.Fatalf("LoadOrCreateKeys: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode = %o, want 600", mode)
	}
	names := dirNames(t, filepath.Dir(path))
	if want := []string{"vapid.json", "vapid.json.lock"}; !reflect.DeepEqual(names, want) {
		t.Errorf("directory holds %v, want %v", names, want)
	}
}

// TestPublicKeyIsPoint proves PublicKey is the base64url uncompressed point a
// browser accepts as applicationServerKey.
func TestPublicKeyIsPoint(t *testing.T) {
	keys := newKeys(t)
	raw, err := base64.RawURLEncoding.DecodeString(keys.PublicKey())
	if err != nil {
		t.Fatalf("PublicKey is not base64url: %v", err)
	}
	if len(raw) != 65 || raw[0] != 0x04 {
		t.Fatalf("public key is %d bytes starting with %#x, want a 65-byte uncompressed point", len(raw), raw[0])
	}
	if _, err := ecdh.P256().NewPublicKey(raw); err != nil {
		t.Errorf("the public key is not a P-256 point: %v", err)
	}
}

// TestKeysCorruptFile proves a damaged file is an error and is never
// overwritten: rotating here would invalidate every subscription on the phone.
func TestKeysCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vapid.json")
	const damaged = "{not json"
	if err := os.WriteFile(path, []byte(damaged), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := push.LoadOrCreateKeys(path); err == nil {
		t.Fatal("LoadOrCreateKeys returned no error for a corrupt file")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != damaged {
		t.Errorf("LoadOrCreateKeys overwrote the corrupt file: %q", after)
	}
}

// TestKeysOffCurvePoint proves a 65-byte public half that is not a P-256 point
// is an error, not a panic.
func TestKeysOffCurvePoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vapid.json")
	if _, err := push.LoadOrCreateKeys(path); err != nil {
		t.Fatalf("LoadOrCreateKeys: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var pair struct {
		Private string `json:"private"`
		Public  string `json:"public"`
	}
	if err := json.Unmarshal(raw, &pair); err != nil {
		t.Fatal(err)
	}
	// 0x04 followed by zeros is the right length and prefix, but not a point.
	offCurve := make([]byte, 65)
	offCurve[0] = 0x04
	pair.Public = base64.RawURLEncoding.EncodeToString(offCurve)
	mixed, err := json.Marshal(pair)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, mixed, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := push.LoadOrCreateKeys(path); err == nil {
		t.Fatal("LoadOrCreateKeys accepted an off-curve public key")
	}
}

// TestKeysMismatchedHalves proves a file whose halves do not belong together is
// refused rather than used to sign with one key and name another.
func TestKeysMismatchedHalves(t *testing.T) {
	pathA := filepath.Join(t.TempDir(), "a.json")
	pathB := filepath.Join(t.TempDir(), "b.json")
	if _, err := push.LoadOrCreateKeys(pathA); err != nil {
		t.Fatalf("LoadOrCreateKeys a: %v", err)
	}
	if _, err := push.LoadOrCreateKeys(pathB); err != nil {
		t.Fatalf("LoadOrCreateKeys b: %v", err)
	}
	pairA, err := os.ReadFile(pathA)
	if err != nil {
		t.Fatal(err)
	}
	pairB, err := os.ReadFile(pathB)
	if err != nil {
		t.Fatal(err)
	}
	// Replace the public key of file A with the public key of file B.
	var a struct {
		Private string `json:"private"`
		Public  string `json:"public"`
	}
	var b struct {
		Private string `json:"private"`
		Public  string `json:"public"`
	}
	if err := json.Unmarshal(pairA, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(pairB, &b); err != nil {
		t.Fatal(err)
	}
	a.Public = b.Public
	mixed, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	mixedPath := filepath.Join(t.TempDir(), "mixed.json")
	if err := os.WriteFile(mixedPath, mixed, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := push.LoadOrCreateKeys(mixedPath); err == nil {
		t.Fatal("LoadOrCreateKeys accepted a public key that does not belong to the private key")
	}
}

// TestKeysModeRepairedOnLoad proves a key file whose mode was widened is pulled
// back to 0600 on load: the private half must not stay readable by others.
func TestKeysModeRepairedOnLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vapid.json")
	first, err := push.LoadOrCreateKeys(path)
	if err != nil {
		t.Fatalf("LoadOrCreateKeys: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("widen mode: %v", err)
	}
	second, err := push.LoadOrCreateKeys(path)
	if err != nil {
		t.Fatalf("LoadOrCreateKeys after chmod 0644: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode after load = %o, want 600", mode)
	}
	if first.PublicKey() != second.PublicKey() {
		t.Errorf("public key changed while repairing the mode")
	}
}

// TestKeysFailedOpenLeavesNoKeyFile proves the key path has the store's shape:
// a directory that cannot be opened fails the write before the rename, so no
// key file appears and only the lock file is left behind, and the next call
// generates and stores one.
func TestKeysFailedOpenLeavesNoKeyFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can open a directory that lacks read permission")
	}
	dir := filepath.Join(t.TempDir(), "keys")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "vapid.json")
	if err := os.Chmod(dir, 0o300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	if _, err := push.LoadOrCreateKeys(path); err == nil {
		t.Fatal("LoadOrCreateKeys returned no error in a directory that cannot be opened")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	names := dirNames(t, dir)
	if want := []string{"vapid.json.lock"}; !reflect.DeepEqual(names, want) {
		t.Errorf("directory holds %v after the failed write, want only the lock file %v", names, want)
	}
	if _, err := push.LoadOrCreateKeys(path); err != nil {
		t.Fatalf("LoadOrCreateKeys after the failure: %v", err)
	}
}

// TestLoadKeysMissing proves a reader never mints a pair: the daemon asks for
// the pair dm web generated and must not create one for a phone that never
// subscribed.
func TestLoadKeysMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vapid.json")
	_, err := push.LoadKeys(path)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("LoadKeys on a missing file = %v, want fs.ErrNotExist", err)
	}
	if names := dirNames(t, dir); len(names) != 0 {
		t.Errorf("LoadKeys left %v behind, want an untouched directory", names)
	}
}

// TestLoadKeysReturnsCreatedKey proves a reader sees the pair the creator wrote,
// and that reading it rewrites nothing.
func TestLoadKeysReturnsCreatedKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vapid.json")
	created, err := push.LoadOrCreateKeys(path)
	if err != nil {
		t.Fatalf("LoadOrCreateKeys: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	loaded, err := push.LoadKeys(path)
	if err != nil {
		t.Fatalf("LoadKeys: %v", err)
	}
	if got, want := loaded.PublicKey(), created.PublicKey(); got != want {
		t.Errorf("LoadKeys public key = %q, want the creator's %q", got, want)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read key file again: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("LoadKeys rewrote the key file")
	}
}

// TestLoadKeysCorruptFile proves a reader reports a damaged file instead of
// minting a pair over it.
func TestLoadKeysCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vapid.json")
	const damaged = "{not json"
	if err := os.WriteFile(path, []byte(damaged), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := push.LoadKeys(path); err == nil {
		t.Fatal("LoadKeys returned no error for a corrupt file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	if string(raw) != damaged {
		t.Errorf("LoadKeys overwrote the damaged file")
	}
}

// TestLoadOrCreateKeysConcurrent proves the key lock serializes generation: N
// callers on a fresh path all get the one pair the first of them wrote. Without
// the lock, two callers that both see no file each generate a pair, and the
// loser returns a key that is not on disk.
func TestLoadOrCreateKeysConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vapid.json")
	const callers = 8
	keys := make([]*push.Keys, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			keys[i], errs[i] = push.LoadOrCreateKeys(path)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	want := keys[0].PublicKey()
	for i, got := range keys {
		if got.PublicKey() != want {
			t.Errorf("caller %d got public key %q, want %q: a racing caller minted a pair", i, got.PublicKey(), want)
		}
	}
	onDisk, err := push.LoadKeys(path)
	if err != nil {
		t.Fatalf("LoadKeys: %v", err)
	}
	if onDisk.PublicKey() != want {
		t.Errorf("stored public key = %q, want the one every caller returned %q", onDisk.PublicKey(), want)
	}
}
