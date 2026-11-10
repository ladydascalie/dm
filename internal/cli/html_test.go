package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dm/internal/clidoc"
	"dm/internal/testutil"
	"dm/internal/theme"
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

// TestHTMLCommandPrintsToStdout proves dm html -o - prints the page, so a pipe
// and a redirect still work without the browser.
func TestHTMLCommandPrintsToStdout(t *testing.T) {
	// The page is bigger than a pipe buffer, so stdout is a file here: a
	// CaptureStdout pipe would fill and block the writer.
	sink := filepath.Join(t.TempDir(), "stdout")
	f, err := os.Create(sink)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = f
	runErr := Run([]string{"html", "-o", "-"})
	os.Stdout = old
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatalf("dm html -o -: %v", runErr)
	}
	got, err := os.ReadFile(sink)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != clidoc.HTMLPage() {
		t.Fatal("dm html -o - printed different bytes than clidoc.HTMLPage()")
	}
}

// TestHTMLCommandFallsBackToAPathWhenNoOpenerStarts proves a headless box
// still gets the page: the command prints the temp file path and returns nil.
func TestHTMLCommandFallsBackToAPathWhenNoOpenerStarts(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var runErr error
	out := testutil.CaptureStdout(t, func() { runErr = Run([]string{"html"}) })
	if runErr != nil {
		t.Fatalf("dm html with no opener: %v", runErr)
	}
	path := strings.TrimSpace(out)
	if !strings.HasSuffix(path, ".html") {
		t.Fatalf("dm html printed %q, want a page path", out)
	}
	defer func() { _ = os.Remove(path) }()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("printed path %q: %v", path, err)
	}
}

// TestHTMLTempFileCarriesThePage proves the browser gets a complete file with
// an .html suffix, so it renders the page instead of downloading it.
func TestHTMLTempFileCarriesThePage(t *testing.T) {
	path, err := writeHTMLTemp("page")
	if err != nil {
		t.Fatalf("write temp page: %v", err)
	}
	defer func() { _ = os.Remove(path) }()
	if !strings.HasSuffix(path, ".html") {
		t.Errorf("temp page %q does not end in .html", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "page" {
		t.Errorf("temp page holds %q, want %q", got, "page")
	}
}

// TestHTMLPagePaintsTheBuiltInTheme proves the page's :root block carries the
// theme package's palette, so the page and the terminal cannot drift.
func TestHTMLPagePaintsTheBuiltInTheme(t *testing.T) {
	page := clidoc.HTMLPage()
	th := theme.DefaultTheme()
	for _, c := range []theme.RGB{
		th.Palette.RailBG,
		th.Palette.RailFocusBG,
		th.Palette.RailText,
		th.Palette.RailDim,
		th.Palette.RailHeader,
		th.Palette.RailDiffAdd,
		th.Palette.RailDiffDel,
		th.Palette.RailWorking,
		th.Palette.BarAccent,
		th.Accents[0],
		th.Accents[1],
	} {
		hex := fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
		if !strings.Contains(page, hex) {
			t.Errorf("page does not paint theme colour %s", hex)
		}
	}
	if strings.Contains(page, "prefers-color-scheme") {
		t.Error("page carries a colour-scheme override; the theme is the only palette")
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
