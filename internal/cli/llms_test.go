package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dm/internal/clidoc"
	"dm/internal/keymap"
)

// TestLLMSTextKeys proves the KEYS section renders the default keymap, and that
// the close-window row is there.
func TestLLMSTextKeys(t *testing.T) {
	t.Parallel()
	doc := clidoc.LLMsText()
	if !strings.Contains(doc, "## Keys\n") {
		t.Fatal("llms.txt has no Keys section")
	}
	if !strings.Contains(doc, "close-window") {
		t.Error("llms.txt has no close-window row")
	}
	for _, e := range keymap.LoadDefault().HelpEntries() {
		if !strings.Contains(doc, "`"+e.Key+"`") {
			t.Errorf("llms.txt does not name key %q", e.Key)
		}
	}
}

// TestLLMSTextMatchesSnapshot proves docs/llms.txt is the current output.
func TestLLMSTextMatchesSnapshot(t *testing.T) {
	t.Parallel()
	want := clidoc.LLMsText()
	got, err := os.ReadFile(filepath.Join("../../docs", "llms.txt"))
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
	t.Parallel()
	path := filepath.Join(t.TempDir(), "llms.txt")
	if err := Run([]string{"llms", "-o", path}); err != nil {
		t.Fatalf("dm llms -o: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written document: %v", err)
	}
	if string(got) != clidoc.LLMsText() {
		t.Fatal("dm llms -o wrote different bytes than clidoc.LLMsText()")
	}
}

// TestLLMSTextStaysOnePass proves the document stays short enough to read in
// one pass.
func TestLLMSTextStaysOnePass(t *testing.T) {
	t.Parallel()
	if n := strings.Count(clidoc.LLMsText(), "\n"); n >= 443 {
		t.Fatalf("llms.txt is %d lines, want under 436", n)
	}
}
