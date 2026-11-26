package release

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Extract writes the dm binary of a release tarball into destDir and returns
// its path.
//
// The archive holds exactly one regular file named "dm" at its root, the shape
// "tar -czf dm_<goos>_<goarch>.tar.gz -C out dm" makes. Extract refuses an
// entry that leaves the archive root, a second entry of any kind, a directory,
// a symbolic link, and an archive with no dm. The binary is written 0755. A
// refused archive leaves nothing in destDir: the dm is unpacked beside its
// final name and renamed only once the whole archive has been read.
func Extract(tarball, destDir string) (string, error) {
	file, err := os.Open(tarball)
	if err != nil {
		return "", fmt.Errorf("release: extract: %w", err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("release: extract %s: %w", tarball, err)
	}
	defer gz.Close()
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("release: extract: %w", err)
	}
	dst := filepath.Join(destDir, "dm")
	var (
		tmp   *os.File
		found bool
	)
	done := false
	defer func() {
		if done {
			return
		}
		if tmp != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("release: extract %s: %w", tarball, err)
		}
		if traversal(hdr.Name) {
			return "", fmt.Errorf("release: extract %s: entry %q leaves the archive root", tarball, hdr.Name)
		}
		if found {
			return "", fmt.Errorf("release: extract %s: second entry %q: the archive holds one dm at the root", tarball, hdr.Name)
		}
		if hdr.Name != "dm" {
			return "", fmt.Errorf("release: extract %s: entry %q: the archive holds one dm at the root", tarball, hdr.Name)
		}
		if hdr.Typeflag != tar.TypeReg {
			return "", fmt.Errorf("release: extract %s: entry dm is not a regular file", tarball)
		}
		found = true
		if tmp, err = unpack(destDir, tr); err != nil {
			return "", fmt.Errorf("release: extract %s: %w", tarball, err)
		}
	}
	if !found || tmp == nil {
		return "", fmt.Errorf("release: extract %s: no dm entry", tarball)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("release: extract %s: %w", tarball, err)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return "", fmt.Errorf("release: extract %s: %w", tarball, err)
	}
	done = true
	return dst, nil
}

// traversal reports whether an archive entry name leaves the archive root.
func traversal(name string) bool {
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return true
	}
	for _, part := range strings.Split(filepath.ToSlash(name), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// unpack writes src into a temporary file in dir and returns it open. The
// caller renames it or removes it.
func unpack(dir string, src io.Reader) (*os.File, error) {
	file, err := os.CreateTemp(dir, ".dm-extract-*")
	if err != nil {
		return nil, err
	}
	if err := fillData(file, src); err != nil {
		file.Close()
		os.Remove(file.Name())
		return nil, err
	}
	return file, nil
}

// fillData copies src into file, syncs it and gives it mode 0755.
func fillData(file *os.File, src io.Reader) error {
	if _, err := io.Copy(file, src); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	// CreateTemp makes the file 0600, so set the mode that matters.
	return file.Chmod(fs.FileMode(0o755))
}
