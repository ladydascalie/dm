package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dm/internal/clidoc"
)

// TestHTMLPageMatchesSnapshot proves docs/dm.html is the current output.
func TestHTMLPageMatchesSnapshot(t *testing.T) {
	want := clidoc.HTMLPage()
	got, err := os.ReadFile(filepath.Join("../../docs", "dm.html"))
	if err != nil {
		t.Fatalf("read docs/dm.html: %v", err)
	}
	if string(got) != want {
		t.Fatal("docs/dm.html is stale: run dm html -o docs/dm.html")
	}
}

// TestHTMLCommandWritesFile proves dm html -o writes the same bytes the page
// renders, and prints nothing.
func TestHTMLCommandWritesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dm.html")
	if err := Run([]string{"html", "-o", path}); err != nil {
		t.Fatalf("dm html -o: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written page: %v", err)
	}
	if string(got) != clidoc.HTMLPage() {
		t.Fatal("dm html -o wrote different bytes than clidoc.HTMLPage()")
	}
}

// TestHTMLPageHasNoExternalResource proves the page carries no resource that
// would need the network or a file beside it.
func TestHTMLPageHasNoExternalResource(t *testing.T) {
	page := strings.ToLower(clidoc.HTMLPage())
	for _, bad := range []string{"<link", "<script src", "<img", "@import"} {
		if strings.Contains(page, bad) {
			t.Errorf("docs/dm.html carries an external resource: %q", bad)
		}
	}
	if !strings.Contains(page, "<style>") {
		t.Error("docs/dm.html has no inline style")
	}
	if !strings.Contains(page, "<script>") {
		t.Error("docs/dm.html has no inline script")
	}
}

// TestHTMLPageNamesEveryCommand proves the page renders the whole command table.
func TestHTMLPageNamesEveryCommand(t *testing.T) {
	page := clidoc.HTMLPage()
	for _, argv := range []string{"dm ls", "dm msg post", "dm man", "dm html"} {
		if !strings.Contains(page, argv) {
			t.Errorf("docs/dm.html does not name %q", argv)
		}
	}
}
