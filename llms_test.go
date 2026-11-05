package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLLMSTextNamesEveryCommand proves the command table carries a row for
// every command in cliCommands.
func TestLLMSTextNamesEveryCommand(t *testing.T) {
	doc := llmsText()
	for _, c := range cliCommands {
		line := "dm"
		if c.name != "" {
			line += " " + c.name
		}
		if !strings.Contains(doc, "| `"+line+"`") {
			t.Errorf("llms.txt does not name %q", line)
		}
		if c.args != "" && !strings.Contains(doc, llmsCell(c.args)) {
			t.Errorf("llms.txt does not carry the argument shape of %q", line)
		}
	}
}

// TestLLMSTextKeys proves the KEYS section renders the default keymap, and that
// the close-window row is there.
func TestLLMSTextKeys(t *testing.T) {
	doc := llmsText()
	if !strings.Contains(doc, "## Keys\n") {
		t.Fatal("llms.txt has no Keys section")
	}
	if !strings.Contains(doc, "close-window") {
		t.Error("llms.txt has no close-window row")
	}
	for _, e := range LoadDefault().HelpEntries() {
		if !strings.Contains(doc, "`"+e.Key+"`") {
			t.Errorf("llms.txt does not name key %q", e.Key)
		}
	}
}

// TestLLMSTextMatchesSnapshot proves docs/llms.txt is the current output.
func TestLLMSTextMatchesSnapshot(t *testing.T) {
	want := llmsText()
	got, err := os.ReadFile(filepath.Join("docs", "llms.txt"))
	if err != nil {
		t.Fatalf("read docs/llms.txt: %v", err)
	}
	if string(got) != want {
		t.Fatal("docs/llms.txt is stale: run dm llms -o docs/llms.txt")
	}
}

// TestLLMSCommandWritesFile proves dm llms -o writes the same bytes the page
// renders.
func TestLLMSCommandWritesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "llms.txt")
	if err := runCLI([]string{"llms", "-o", path}); err != nil {
		t.Fatalf("dm llms -o: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written document: %v", err)
	}
	if string(got) != llmsText() {
		t.Fatal("dm llms -o wrote different bytes than llmsText()")
	}
}

// TestLLMSTextStaysOnePass proves the document stays short enough to read in
// one pass.
func TestLLMSTextStaysOnePass(t *testing.T) {
	if n := strings.Count(llmsText(), "\n"); n >= 400 {
		t.Fatalf("llms.txt is %d lines, want under 400", n)
	}
}
