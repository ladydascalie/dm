package cli

// The --body and --body-file flags: which source wins, what each reads, and
// the refusal of the ambiguous pair.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bodyFlags builds the flag set the way parse does, so a test drives the
// flags a shell would pass.
func bodyFlags(t *testing.T, args ...string) *cmdFlags {
	t.Helper()
	c := newFlags("msg")
	c.fs.StringVar(&c.body, "body", "", "long body text under the message line")
	c.fs.StringVar(&c.bodyFile, "body-file", "", "read the body from a path, - for stdin")
	_ = c.fs.Parse(args)
	return c
}

// --body carries the text through.
func TestBodyFlagWins(t *testing.T) {
	t.Parallel()
	c := bodyFlags(t, "--body", "long\ntext")
	body, err := boxBody(c)
	if err != nil {
		t.Fatalf("boxBody: %v", err)
	}
	if body != "long\ntext" {
		t.Fatalf("body = %q", body)
	}
}

// --body-file reads the file verbatim, newlines included.
func TestBodyFileReadsThePath(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "body.txt")
	if err := os.WriteFile(path, []byte("first\nsecond\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := bodyFlags(t, "--body-file", path)
	body, err := boxBody(c)
	if err != nil {
		t.Fatalf("boxBody: %v", err)
	}
	if body != "first\nsecond\n" {
		t.Fatalf("body = %q", body)
	}
}

// --body-file - reads stdin, so an agent can pipe the long text.
// Stays serial: the test swaps the process stdin, which belongs to every test
// at once.
func TestBodyFileDashReadsStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("piped\nbody"); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old })

	c := bodyFlags(t, "--body-file", "-")
	body, err := boxBody(c)
	if err != nil {
		t.Fatalf("boxBody: %v", err)
	}
	if body != "piped\nbody" {
		t.Fatalf("body = %q", body)
	}
}

// Naming both sources is refused, and naming neither is an empty body.
func TestBodySourcesAreExclusive(t *testing.T) {
	t.Parallel()
	c := bodyFlags(t, "--body", "a", "--body-file", "/tmp/x")
	if _, err := boxBody(c); err == nil || !strings.Contains(err.Error(), "exclusive") {
		t.Fatalf("boxBody = %v, want the exclusivity error", err)
	}
	empty, err := boxBody(bodyFlags(t))
	if err != nil || empty != "" {
		t.Fatalf("body = %q err %v, want empty with no flags", empty, err)
	}
}

// A missing file is a clean error, not a panic.
func TestBodyFileMissingIsAnError(t *testing.T) {
	t.Parallel()
	c := bodyFlags(t, "--body-file", filepath.Join(t.TempDir(), "gone"))
	if _, err := boxBody(c); err == nil {
		t.Fatal("a missing body file was accepted")
	}
}
