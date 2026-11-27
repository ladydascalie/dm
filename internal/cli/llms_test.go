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
// one pass. The budget rose from 480 to 500 when the ask verbs and their
// actions joined the page, and to 510 when the copy section did; each section
// carries its own lines, so the page still reads in one pass.
func TestLLMSTextStaysOnePass(t *testing.T) {
	t.Parallel()
	if n := strings.Count(clidoc.LLMsText(), "\n"); n >= 510 {
		t.Fatalf("llms.txt is %d lines, want under 510", n)
	}
}

// TestLLMSDashWritesStdout proves -o - is stdout, the way html honours it,
// and never a file named -.
// Stays serial: the test swaps the process stdout, which belongs to every
// test at once.
func TestLLMSDashWritesStdout(t *testing.T) {
	t.Chdir(t.TempDir())
	// The document is bigger than a pipe buffer, so stdout is a file here.
	sink := filepath.Join(t.TempDir(), "stdout")
	f, err := os.Create(sink)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = f
	runErr := Run([]string{"llms", "-o", "-"})
	os.Stdout = old
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatalf("dm llms -o -: %v", runErr)
	}
	got, err := os.ReadFile(sink)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "## Keys\n") {
		t.Fatal("dm llms -o - printed no llms.txt")
	}
	if _, err := os.Stat("-"); !os.IsNotExist(err) {
		t.Fatal("dm llms -o - wrote a file named -")
	}
}
