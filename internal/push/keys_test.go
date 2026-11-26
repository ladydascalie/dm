package push_test

import (
	"bytes"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
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
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "vapid.json" {
		t.Errorf("directory holds %d entries, want only vapid.json", len(entries))
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
// key file appears, and the next call generates and stores one.
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
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, len(entries))
		for i, entry := range entries {
			names[i] = entry.Name()
		}
		t.Errorf("directory holds %v after the failed write, want nothing", names)
	}
	if _, err := push.LoadOrCreateKeys(path); err != nil {
		t.Fatalf("LoadOrCreateKeys after the failure: %v", err)
	}
}
