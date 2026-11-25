package archdoc

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dm/internal/arch"
)

var update = flag.Bool("update", false, "rewrite "+DocPath+" from the model")

// TestGenerated regenerates the package graph page in memory and compares it
// with the committed file. -update rewrites the file.
func TestGenerated(t *testing.T) {
	graph, err := arch.Load()
	if err != nil {
		t.Fatal(err)
	}
	want, err := Render(graph)
	if err != nil {
		t.Fatal(err)
	}
	path := moduleFile(DocPath)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("rewrote %s", DocPath)
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v: run make docs-internals", err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s is stale: run make docs-internals", DocPath)
	}
}

// TestPageTable fails when the layer table of the prose page drifts from the
// model.
func TestPageTable(t *testing.T) {
	page, err := os.ReadFile(moduleFile(PagePath))
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckTable(page); err != nil {
		t.Fatal(err)
	}
}

// moduleFile resolves a path relative to the module root. The tests run in the
// package directory.
func moduleFile(path string) string {
	return filepath.Join("..", "..", filepath.FromSlash(path))
}

// TestCitations resolves every citation under docs/internals: the file must
// exist and the symbol must be declared in it. A line number is not an anchor.
func TestCitations(t *testing.T) {
	bad, err := CheckCitations(moduleFile(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		lines := make([]string, len(bad))
		for i, e := range bad {
			lines[i] = e.String()
		}
		t.Fatalf("%d citation(s) do not resolve:\n%s", len(bad), strings.Join(lines, "\n"))
	}
}
