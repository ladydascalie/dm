package main

import (
	"os"
	"path/filepath"
	"testing"

	"dm/internal/clidoc"
)

// TestManPageMatchesSnapshot proves docs/dm.1 is the current output.
func TestManPageMatchesSnapshot(t *testing.T) {
	want := clidoc.ManPage()
	got, err := os.ReadFile(filepath.Join("docs", "dm.1"))
	if err != nil {
		t.Fatalf("read docs/dm.1: %v", err)
	}
	if string(got) != want {
		t.Fatal("docs/dm.1 is stale: run dm man -o docs/dm.1")
	}
}

// TestManCommandWritesFile proves dm man -o writes the same bytes the page
// renders, and prints nothing.
func TestManCommandWritesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dm.1")
	if err := runCLI([]string{"man", "-o", path}); err != nil {
		t.Fatalf("dm man -o: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written page: %v", err)
	}
	if string(got) != clidoc.ManPage() {
		t.Fatal("dm man -o wrote different bytes than clidoc.ManPage()")
	}
}
