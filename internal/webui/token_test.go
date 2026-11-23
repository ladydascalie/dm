package webui

import (
	"encoding/hex"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStream runs f with a standard stream redirected, and returns what f
// printed there. A test that wants both redirects calls it twice.
func captureStream(t *testing.T, stream **os.File, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := *stream
	*stream = w
	f()
	*stream = old
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	return string(out)
}

// printedURL is the URL line announce prints, without its indent.
func printedURL(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); strings.Contains(line, "?t=") {
			return line
		}
	}
	t.Fatalf("no URL printed in %q", out)
	return ""
}

// TestTokenSurvivesTwoRuns proves the second start serves the URL the first
// one printed, so a phone's link survives a restart.
func TestTokenSurvivesTwoRuns(t *testing.T) {
	dir := root(t, "main")
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8099}
	run := func() (out string, reused bool) {
		tok, reused, err := webToken(dir, "main", "")
		if err != nil {
			t.Fatal(err)
		}
		out = captureStream(t, &os.Stdout, func() { announce("main", addr, tok, reused) })
		return out, reused
	}

	first, reused := run()
	if reused {
		t.Fatal("the first run reused a token from nowhere")
	}
	if strings.Contains(first, "reused") {
		t.Fatalf("the first run claims a reuse:\n%s", first)
	}
	second, reused := run()
	if !reused {
		t.Fatal("the second run generated a fresh token")
	}
	if want, got := printedURL(t, first), printedURL(t, second); got != want {
		t.Fatalf("second run printed %q, want %q", got, want)
	}
	if !strings.Contains(second, "reused") {
		t.Fatalf("the second run does not say it reused the token:\n%s", second)
	}
}

// TestExplicitTokenIsKept proves --token wins and lands in the file, so the
// next bare start keeps serving the same URL.
func TestExplicitTokenIsKept(t *testing.T) {
	dir := root(t, "main")
	if _, _, err := webToken(dir, "main", ""); err != nil {
		t.Fatal(err)
	}
	tok, reused, err := webToken(dir, "main", "explicit")
	if err != nil {
		t.Fatal(err)
	}
	if tok != "explicit" || reused {
		t.Fatalf("explicit run returned %q reused=%v", tok, reused)
	}
	if got := readFile(t, webTokenPath(dir, "main")); got != "explicit" {
		t.Fatalf("file holds %q, want explicit", got)
	}
	tok, reused, err = webToken(dir, "main", "")
	if err != nil {
		t.Fatal(err)
	}
	if tok != "explicit" || !reused {
		t.Fatalf("later bare run returned %q reused=%v, want the explicit token", tok, reused)
	}
}

// TestTokenFileIsPrivate proves the token is readable by the operator alone.
func TestTokenFileIsPrivate(t *testing.T) {
	dir := root(t, "main")
	if _, _, err := webToken(dir, "main", ""); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(webTokenPath(dir, "main"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("web.token mode %o, want 0600", perm)
	}
}

// TestBrokenTokenFileStillServes proves a tree that refuses the token file is
// a warning, not a dead port: the run gets a token and says what it lost.
func TestBrokenTokenFileStillServes(t *testing.T) {
	dir := root(t, "main")
	path := webTokenPath(dir, "main")
	// A directory where the file belongs is unreadable as a token and refuses
	// the write, on every platform and for every uid.
	if err := os.MkdirAll(filepath.Join(path, "keep"), 0o700); err != nil {
		t.Fatal(err)
	}

	var (
		tok    string
		reused bool
		err    error
	)
	stderr := captureStream(t, &os.Stderr, func() {
		tok, reused, err = webToken(dir, "main", "")
	})
	if err != nil {
		t.Fatalf("a broken token file failed the run: %v", err)
	}
	if tok == "" || reused {
		t.Fatalf("token %q reused=%v, want a fresh token", tok, reused)
	}
	if len(tok) != 32 {
		t.Fatalf("token %q is %d chars, want 32", tok, len(tok))
	}
	if _, derr := hex.DecodeString(tok); derr != nil {
		t.Fatalf("token %q is not hex: %v", tok, derr)
	}
	if !strings.Contains(stderr, path) {
		t.Fatalf("stderr %q does not name %s", stderr, path)
	}
	if !strings.Contains(stderr, "generating a token for this run") {
		t.Fatalf("stderr %q does not say the token will not last", stderr)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}
