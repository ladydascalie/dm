package push

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeFileAtomic replaces path with data at mode 0600. It writes a temp file
// in the same directory and renames it over path, so a reader sees either the
// old file or the new one and never a half-written one. The same directory
// keeps the rename on one filesystem.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("push: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("push: create temp file in %s: %w", dir, err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("push: set mode on %s: %w", name, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("push: write %s: %w", name, err)
	}
	// Sync before the rename: a VAPID key lost to a crash cannot be recovered,
	// and every subscription a phone holds would go stale with it.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("push: sync %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("push: close %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("push: rename %s to %s: %w", name, path, err)
	}
	return nil
}
