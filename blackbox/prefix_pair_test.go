package blackbox

// The prefix pair across a chunk boundary, end to end. Two registered values
// where one is a proper prefix of the other, pushed in name order so the short
// one arrives first, and a program that writes the long value in two pieces
// with a pause between them. The scan must not finalise the short value when
// its prefix completes: the bytes wait, and the longer value redacts whole.
// A scan that finalises publishes the longer value's tail on the read surface
// and in the yank.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	prefixShort = "poc-"
	prefixLong  = "poc-token-1234"
)

// TestPrefixPairSplitWriteStaysRedacted drives the split through a real shell:
// `printf 'poc-'`, a pause twice as long as nothing in particular, then
// `printf 'token-1234'`. The two writes reach the pane as two chunks.
func TestPrefixPairSplitWriteStaysRedacted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	bin := t.TempDir()
	// The pass tool runs `pass show <ref>`; a secret never rides argv. The
	// stub reads the ref and prints the value that ref names.
	script := "#!/bin/sh\ncase \"$2\" in\n" +
		"short) printf '%s\\n' '" + prefixShort + "' ;;\n" +
		"long) printf '%s\\n' '" + prefixLong + "' ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(bin, "pass"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	for i, e := range h.env {
		if strings.HasPrefix(e, "PATH=") {
			h.env[i] = "PATH=" + bin + string(os.PathListSeparator) + strings.TrimPrefix(e, "PATH=")
			break
		}
	}

	h.run("new", "-s", "pair")
	pane := h.focus("pair")
	// Name order decides the push order: "aa" sorts first, so the pane's list
	// holds the short value before the long one, which is the order the
	// reviewer's repro needs.
	h.run("secret", "register", "aa", "--tool", "pass", "--ref", "short", "-s", "pair")
	h.run("secret", "register", "zz", "--tool", "pass", "--ref", "long", "-s", "pair")
	h.run("secret", "use", "aa", "-s", "pair")
	h.run("secret", "use", "zz", "-s", "pair")

	h.run("write-to", pane, "-s", "pair", "--enter",
		"--msg", "printf '"+prefixShort+"'; sleep 0.3; printf 'token-1234'")

	redacted := strings.Repeat("*", len(prefixLong))
	screen := h.waitFor("pair", pane, redacted)
	if strings.Contains(screen, prefixLong) {
		t.Fatalf("the read surface holds the value: %s", screen)
	}

	yanked := h.run("yank", pane, "-s", "pair")
	if strings.Contains(yanked, prefixLong) {
		t.Fatalf("the yank holds the value: %q", yanked)
	}
	if !strings.Contains(yanked, redacted) {
		t.Fatalf("the yank = %q, want the value redacted whole", yanked)
	}
}
