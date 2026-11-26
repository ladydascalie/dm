package push_test

import (
	"path/filepath"
	"testing"

	"dm/internal/push"
)

// TestSessionPaths pins the two file names dm web writes and the daemon reads.
// They are the contract between the two processes.
func TestSessionPaths(t *testing.T) {
	dir := filepath.Join("state", "sessions", "work")
	if got, want := push.StorePath(dir), filepath.Join(dir, "webpush.json"); got != want {
		t.Errorf("StorePath = %q, want %q", got, want)
	}
	if got, want := push.KeysPath(dir), filepath.Join(dir, "webpush.key"); got != want {
		t.Errorf("KeysPath = %q, want %q", got, want)
	}
}

// TestSessionPathsRoundTrip proves the two names sit beside each other in one
// directory: a pair created through KeysPath is the pair LoadKeys finds.
func TestSessionPathsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	created, err := push.LoadOrCreateKeys(push.KeysPath(dir))
	if err != nil {
		t.Fatalf("LoadOrCreateKeys: %v", err)
	}
	loaded, err := push.LoadKeys(push.KeysPath(dir))
	if err != nil {
		t.Fatalf("LoadKeys: %v", err)
	}
	if created.PublicKey() != loaded.PublicKey() {
		t.Errorf("LoadKeys returned %q, want the created key %q", loaded.PublicKey(), created.PublicKey())
	}
}
