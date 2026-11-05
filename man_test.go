package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestManPageNamesEveryCommand proves the page carries a row for every command
// in the table.
func TestManPageNamesEveryCommand(t *testing.T) {
	page := manPage()
	for _, c := range cliCommands {
		if !strings.Contains(page, roffBold(c.line())) {
			t.Errorf("man page does not name %q", c.line())
		}
	}
}

// TestManPageKeys proves the KEYS section renders the default keymap, and that
// the close-window row is there.
func TestManPageKeys(t *testing.T) {
	page := manPage()
	if !strings.Contains(page, ".SH KEYS\n") {
		t.Fatal("man page has no KEYS section")
	}
	if !strings.Contains(page, "close window") {
		t.Error("man page has no close-window row")
	}
	for _, e := range LoadDefault().HelpEntries() {
		if !strings.Contains(page, roffBold(e.Key)) {
			t.Errorf("man page does not name key %q", e.Key)
		}
	}
}

// TestManPageMatchesSnapshot proves docs/dm.1 is the current output.
func TestManPageMatchesSnapshot(t *testing.T) {
	want := manPage()
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
	if string(got) != manPage() {
		t.Fatal("dm man -o wrote different bytes than manPage()")
	}
}

// TestManPageEscapesControlLine proves a summary that starts with a roff
// control character is escaped, so it cannot become a macro.
func TestManPageEscapesControlLine(t *testing.T) {
	saved := cliCommands
	defer func() { cliCommands = saved }()
	cliCommands = append(append([]cliCommand{}, saved...), cliCommand{
		name:    "escape-probe",
		summary: ".hidden macro",
		group:   groupDocs,
	})
	page := manPage()
	if !strings.Contains(page, `\&.hidden macro`) {
		t.Error("a summary starting with a dot is not escaped")
	}
	for _, line := range strings.Split(page, "\n") {
		if line == ".hidden macro" {
			t.Fatal("a summary became a roff control line")
		}
	}
}

// TestRoffEscape covers the escape rules directly.
func TestRoffEscape(t *testing.T) {
	cases := map[string]string{
		"plain":   "plain",
		".dot":    `\&.dot`,
		"'quote":  `\&'quote`,
		`a\b`:     `a\eb`,
		"mid.dot": "mid.dot",
	}
	for in, want := range cases {
		if got := roffEscape(in); got != want {
			t.Errorf("roffEscape(%q) = %q, want %q", in, got, want)
		}
	}
}
