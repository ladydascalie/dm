package push

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteFileAtomicSyncsDirectoryOnce makes the directory sync observable. A
// recorder replaces syncDir, so deleting the call or moving it before the
// rename fails this test instead of leaving the suite green.
func TestWriteFileAtomicSyncsDirectoryOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	original := syncDir
	t.Cleanup(func() { syncDir = original })
	calls := 0
	var saw []byte
	syncDir = func(d *os.File) error {
		calls++
		// The rename must have landed before the directory is synced.
		saw, _ = os.ReadFile(path)
		return nil
	}

	if err := writeFileAtomic(path, []byte("new")); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	if calls != 1 {
		t.Errorf("directory sync ran %d times, want 1", calls)
	}
	if string(saw) != "new" {
		t.Errorf("directory sync saw %q, want the bytes the rename put in place", saw)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("file holds %q, want new", got)
	}
}
