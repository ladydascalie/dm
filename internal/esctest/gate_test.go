package esctest

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestEsctestBaseline runs the full esctest2 suite against the dm VT and fails
// when the pass count drops below the recorded baseline. The full run takes
// minutes, so it is opt-in: set DM_ESCTEST=1. It skips when python3 or
// esctest2 is absent, so go test ./... stays green without the dependency.
func TestEsctestBaseline(t *testing.T) {
	if os.Getenv("DM_ESCTEST") == "" {
		t.Skip("set DM_ESCTEST=1 to run the esctest2 baseline")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found")
	}

	// The test runs in the package directory, so the repo-relative testdata
	// path is two levels up. Find also checks $ESCTEST_DIR and /tmp/esctest2.
	testdata := filepath.Join("..", "..", "internal", "esctest", "testdata", "esctest")
	dir, err := Find(testdata)
	if err != nil {
		t.Skip("esctest2 not found: set ESCTEST_DIR or run make esctest-fetch")
	}

	baseline, err := readBaseline("baseline.txt")
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}

	var log bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if _, err := Run(ctx, Options{Dir: dir, Stdout: &log}); err != nil {
		t.Fatalf("run harness: %v", err)
	}

	passed, failed, known, ok := parseTally(log.String())
	if !ok {
		t.Fatalf("no tally line in the log:\n%s", tail(log.String(), 40))
	}
	t.Logf("tally: %d passed, %d known bugs, %d failed (baseline %d passed)",
		passed, known, failed, baseline.passed)
	if passed < baseline.passed {
		t.Fatalf("regression: %d passed, baseline is %d\n%s",
			passed, baseline.passed, tail(log.String(), 80))
	}
}

// TestParseTally pins the tally parser. It runs by default and needs nothing.
func TestParseTally(t *testing.T) {
	tests := []struct {
		in                    string
		passed, failed, known int
		ok                    bool
	}{
		{"*** 288 tests passed, 38 known bugs, 241 TESTS FAILED ***", 288, 241, 38, true},
		{"*** 1 test passed, 0 known bugs, 0 tests failed ***", 1, 0, 0, true},
		{"*** 0 tests passed, 1 known bug, 1 TEST FAILED ***", 0, 1, 1, true},
		{"no tally here", 0, 0, 0, false},
	}
	for _, tt := range tests {
		passed, failed, known, ok := parseTally(tt.in)
		if ok != tt.ok || passed != tt.passed || failed != tt.failed || known != tt.known {
			t.Errorf("parseTally(%q) = %d, %d, %d, %v; want %d, %d, %d, %v",
				tt.in, passed, failed, known, ok, tt.passed, tt.failed, tt.known, tt.ok)
		}
	}
}

type baselineTally struct {
	passed, failed, known int
}

func readBaseline(path string) (baselineTally, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return baselineTally{}, err
	}
	passed, failed, known, ok := parseTally(string(data))
	if !ok {
		return baselineTally{}, fmt.Errorf("no tally line in %s", path)
	}
	return baselineTally{passed: passed, failed: failed, known: known}, nil
}

var tallyRe = regexp.MustCompile(
	`\*\*\* (\d+) tests? passed, (\d+) known bugs?, (\d+) (?:TESTS? FAILED|tests? failed) \*\*\*`)

func parseTally(s string) (passed, failed, known int, ok bool) {
	m := tallyRe.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, 0, false
	}
	passed, _ = strconv.Atoi(m[1])
	known, _ = strconv.Atoi(m[2])
	failed, _ = strconv.Atoi(m[3])
	return passed, failed, known, true
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
