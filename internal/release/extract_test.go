package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// entry is one member of a test tarball.
type entry struct {
	name string
	body string
	mode int64
	flag byte
	link string
}

// tarball writes a gzipped tar holding entries and returns its path.
func tarball(t *testing.T, entries ...entry) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.body)), Typeflag: e.flag, Linkname: e.link}
		if hdr.Typeflag == 0 {
			hdr.Typeflag = tar.TypeReg
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "dm.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtract(t *testing.T) {
	path := tarball(t, entry{name: "dm", body: "the binary", mode: 0o600})
	dir := t.TempDir()
	got, err := Extract(path, filepath.Join(dir, "fresh"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "fresh", "dm")
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	body, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "the binary" {
		t.Fatalf("body = %q", body)
	}
	fi, err := os.Stat(got)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want 0755", fi.Mode().Perm())
	}
}

func TestExtractRefusals(t *testing.T) {
	tests := []struct {
		name    string
		entries []entry
		wantIn  string
	}{
		{
			name:    "traversal",
			entries: []entry{{name: "../dm", body: "x", mode: 0o755}},
			wantIn:  "leaves the archive root",
		},
		{
			name:    "absolute path",
			entries: []entry{{name: "/dm", body: "x", mode: 0o755}},
			wantIn:  "leaves the archive root",
		},
		{
			name:    "nested",
			entries: []entry{{name: "bin/dm", body: "x", mode: 0o755}},
			wantIn:  "one dm at the root",
		},
		{
			name:    "second entry",
			entries: []entry{{name: "dm", body: "x", mode: 0o755}, {name: "README", body: "y", mode: 0o644}},
			wantIn:  "second entry",
		},
		{
			name:    "two dm entries",
			entries: []entry{{name: "dm", body: "x", mode: 0o755}, {name: "dm", body: "y", mode: 0o755}},
			wantIn:  "second entry",
		},
		{
			name:    "no dm",
			entries: []entry{{name: "README", body: "x", mode: 0o644}},
			wantIn:  "one dm at the root",
		},
		{
			name:    "empty archive",
			entries: nil,
			wantIn:  "no dm entry",
		},
		{
			name:    "symlink",
			entries: []entry{{name: "dm", flag: tar.TypeSymlink, link: "/bin/sh"}},
			wantIn:  "not a regular file",
		},
		{
			name:    "directory",
			entries: []entry{{name: "dm", flag: tar.TypeDir, mode: 0o755}},
			wantIn:  "not a regular file",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := tarball(t, tc.entries...)
			dir := t.TempDir()
			_, err := Extract(path, dir)
			if err == nil {
				t.Fatal("Extract returned no error")
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Fatalf("error = %q, want it to name %q", err, tc.wantIn)
			}
			if _, statErr := os.Stat(filepath.Join(dir, "dm")); !os.IsNotExist(statErr) {
				t.Fatalf("dm survived a refused extract: %v", statErr)
			}
		})
	}
}

func TestExtractZeroByte(t *testing.T) {
	// An empty dm is still a regular file: the digest is what refuses it.
	path := tarball(t, entry{name: "dm", mode: 0o755})
	got, err := Extract(path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(got)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != 0 || fi.Mode().Perm() != 0o755 {
		t.Fatalf("dm is %d bytes of %v", fi.Size(), fi.Mode().Perm())
	}
}

func TestExtractNotAGzip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dm.tar.gz")
	if err := os.WriteFile(path, []byte("not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(path, t.TempDir()); err == nil {
		t.Fatal("Extract returned no error for a file that is not gzip")
	}
}
