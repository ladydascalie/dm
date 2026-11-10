package cli

import (
	"fmt"
	"os"

	"dm/internal/browser"
	"dm/internal/clidoc"
)

// runHTML writes the page to out, prints it when out is "-", and opens it in
// the browser when out is empty. A browser that will not start leaves the page
// on disk and prints its path, so a headless box still gets the file.
func runHTML(out string) error {
	page := clidoc.HTMLPage()
	if out == "-" {
		fmt.Print(page)
		return nil
	}
	if out != "" {
		return os.WriteFile(out, []byte(page), 0o644)
	}
	path, err := writeHTMLTemp(page)
	if err != nil {
		return err
	}
	if err := browser.Open(path); err != nil {
		fmt.Fprintf(os.Stderr, "dm html: %v\n", err)
		fmt.Println(path)
	}
	return nil
}

// writeHTMLTemp writes the page under the temp directory. The .html suffix
// makes the browser render the file instead of downloading it.
func writeHTMLTemp(page string) (string, error) {
	f, err := os.CreateTemp("", "dm-*.html")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(page); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return f.Name(), nil
}
