package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dm/internal/clidoc"
)

// TestManPageMatchesSnapshot proves docs/dm.1 is the current output.
func TestManPageMatchesSnapshot(t *testing.T) {
	t.Parallel()
	want := clidoc.ManPage()
	got, err := os.ReadFile(filepath.Join("../../docs", "dm.1"))
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
	t.Parallel()
	path := filepath.Join(t.TempDir(), "dm.1")
	if err := Run([]string{"man", "-o", path}); err != nil {
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

// TestManDashWritesStdout proves -o - is stdout, the way html honours it,
// and never a file named -.
// Stays serial: the test swaps the process stdout, which belongs to every
// test at once.
func TestManDashWritesStdout(t *testing.T) {
	t.Chdir(t.TempDir())
	// The page is bigger than a pipe buffer, so stdout is a file here.
	sink := filepath.Join(t.TempDir(), "stdout")
	f, err := os.Create(sink)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = f
	runErr := Run([]string{"man", "-o", "-"})
	os.Stdout = old
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatalf("dm man -o -: %v", runErr)
	}
	got, err := os.ReadFile(sink)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "dm \\- durable terminal multiplexer") {
		t.Fatal("dm man -o - printed no man page")
	}
	if _, err := os.Stat("-"); !os.IsNotExist(err) {
		t.Fatal("dm man -o - wrote a file named -")
	}
}
