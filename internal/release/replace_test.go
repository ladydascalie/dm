package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplace(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dm")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(t.TempDir(), "dm")
	if err := os.WriteFile(fresh, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Replace(target, fresh); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "new" {
		t.Fatalf("target = %q, want the fresh content", body)
	}
	fi, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want 0755", fi.Mode().Perm())
	}
	// The fresh file is left where it was.
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh file went away: %v", err)
	}
	// No temporary file survives.
	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 {
		t.Fatalf("%d entries in %s, want only dm", len(left), dir)
	}
}

func TestReplaceNoTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dm")
	fresh := filepath.Join(t.TempDir(), "dm")
	if err := os.WriteFile(fresh, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Replace(target, fresh); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(target); err != nil || string(body) != "new" {
		t.Fatalf("target = %q, %v", body, err)
	}
}

func TestReplaceReadOnlyDir(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dm")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(t.TempDir(), "dm")
	if err := os.WriteFile(fresh, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	err := Replace(target, fresh)
	if err == nil {
		t.Fatal("Replace wrote into a read-only directory")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Fatalf("error = %q, want it to name %s", err, dir)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "old" {
		t.Fatalf("target = %q, want the old content", body)
	}
}

func TestReplaceMissingFresh(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dm")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := Replace(target, filepath.Join(t.TempDir(), "absent"))
	if err == nil {
		t.Fatal("Replace replaced target with a file that does not exist")
	}
	if body, readErr := os.ReadFile(target); readErr != nil || string(body) != "old" {
		t.Fatalf("target = %q, %v", body, readErr)
	}
	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 {
		t.Fatalf("%d entries in %s, want only dm: a temporary file survived", len(left), dir)
	}
}
