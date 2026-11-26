package push

import (
	"fmt"
	"os"
	"path/filepath"
)

// syncDir flushes a directory after the rename inside it. It is a variable so a
// test can record the call; the shipped value is the plain fsync.
var syncDir = func(d *os.File) error { return d.Sync() }

// writeFileAtomic replaces path with data at mode 0600. It writes a temp file
// in the same directory, syncs it, opens the directory, renames the temp file
// over path and syncs the directory, so a reader sees either the old file or
// the new one and a power loss cannot undo the rename. The same directory keeps
// the rename on one filesystem.
//
// The directory is opened before the rename on purpose. A directory that
// refuses the open, for lack of read permission or any other reason, fails the
// call while path still holds the old bytes, so the caller's in-memory value
// and the file stay in step.
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
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("push: open directory %s: %w", dir, err)
	}
	defer d.Close()
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("push: rename %s to %s: %w", name, path, err)
	}
	// The rename itself must reach the disk, or a power loss can undo it and
	// leave the old file, or none, in its place.
	if err := syncDir(d); err != nil {
		return fmt.Errorf("push: sync directory %s: %w", dir, err)
	}
	return nil
}
