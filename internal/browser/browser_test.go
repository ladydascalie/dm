package browser

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// fakeOpener builds a dir holding an executable that records its argument
// and returns it, so tests can prove Open launched the right opener.
func fakeOpener(t *testing.T, name string) (dir, log string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("opener faking is unix-only")
	}
	dir = t.TempDir()
	log = filepath.Join(t.TempDir(), "invoked")
	script := "#!/bin/sh\necho \"$@\" > " + log + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, log
}

func TestOpenInvokesOpener(t *testing.T) {
	name := "xdg-open"
	switch runtime.GOOS {
	case "windows":
		t.Skip("fake opener shadows unix openers only")
	case "darwin":
		name = "open"
	}
	dir, log := fakeOpener(t, name)
	t.Setenv("PATH", dir)

	if err := Open("https://example.com"); err != nil {
		t.Fatalf("Open() = %v, want nil", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if b, err := os.ReadFile(log); err == nil && string(b) == "https://example.com\n" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("opener not invoked with target within 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestOpenNoOpener(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("PATH", empty)

	if runtime.GOOS == "windows" {
		if err := Open("x"); err == nil {
			t.Fatal("Open() = nil, want error with no opener on PATH")
		}
		return
	}
	if _, err := exec.LookPath("xdg-open"); err == nil {
		t.Fatal("xdg-open found despite empty PATH")
	}
	if err := Open("x"); err == nil {
		t.Fatal("Open() = nil, want error with no opener on PATH")
	}
}
