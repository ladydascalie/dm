package release

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Replace puts fresh in place of target.
//
// It writes a temporary file beside target, syncs it, gives it mode 0755 and
// renames it over target, so target is either the old binary or the new one and
// never half of each. A directory that cannot take the write is an error that
// names the directory.
func Replace(target, fresh string) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".dm-upgrade-*")
	if err != nil {
		return fmt.Errorf("release: replace %s: cannot write in %s: %w", target, dir, err)
	}
	name := tmp.Name()
	done := false
	defer func() {
		if !done {
			os.Remove(name)
		}
	}()
	if err := fill(tmp, fresh); err != nil {
		tmp.Close()
		return fmt.Errorf("release: replace %s: %w", target, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("release: replace %s: %w", target, err)
	}
	if err := os.Rename(name, target); err != nil {
		return fmt.Errorf("release: replace %s: %w", target, err)
	}
	done = true
	// A rename is durable once the directory is synced too. A failure here
	// leaves the new binary in place, so it is not worth an error.
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// fill copies fresh into dst, syncs it and gives it mode 0755.
func fill(dst *os.File, fresh string) error {
	src, err := os.Open(fresh)
	if err != nil {
		return err
	}
	defer src.Close()
	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	if err := dst.Sync(); err != nil {
		return err
	}
	return dst.Chmod(0o755)
}
