package esctest

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// familyFlag selects one esctest2 family. esctest2's --include is a regex over
// the full test name, so DECSTR matches DECSTRTests and DECSTRTests.test_*.
var familyFlag = flag.String("family", "",
	"esctest2 family (a regex over test names) to run, e.g. DECSTR")

// TestEsctestFamily runs one esctest2 family and fails when that family has
// any failure. Set DM_ESCTEST=1 and pass -args -family=NAME. It skips when
// DM_ESCTEST is unset, python3 is absent or esctest2 is absent, so the default
// go test stays green without the dependency.
func TestEsctestFamily(t *testing.T) {
	requireFamily(t, *familyFlag)
}

// requireFamily is the helper a stream unit calls, or uses through
// TestEsctestFamily, to gate its own accept on one esctest2 family.
func requireFamily(t *testing.T, family string) {
	t.Helper()
	if os.Getenv("DM_ESCTEST") == "" {
		t.Skip("set DM_ESCTEST=1 to run esctest2")
	}
	if family == "" {
		t.Skip("set a family: -args -family=DECSTR")
	}
	if _, err := regexp.Compile(family); err != nil {
		t.Fatalf("invalid family %q: %v", family, err)
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

	var log bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err := Run(ctx, Options{
		Dir:    dir,
		Args:   []string{"--include=" + family},
		Stdout: &log,
	}); err != nil {
		t.Fatalf("run harness: %v", err)
	}

	if err := familyResult(family, log.String()); err != nil {
		t.Fatal(err)
	}
	passed, _, known, _ := parseTally(log.String())
	t.Logf("family %s: %d passed, %d known bugs, 0 failed", family, passed, known)
}

// familyResult turns an esctest2 log into the gate verdict for one family.
// It fails on any failed test, and it fails when nothing ran: an unknown
// family must not pass silently.
func familyResult(family, log string) error {
	passed, failed, known, ok := parseTally(log)
	if !ok {
		return fmt.Errorf("family %s: no tally line in the log:\n%s", family, tail(log, 40))
	}
	if passed+failed+known == 0 {
		return fmt.Errorf(
			"unknown family %q: no esctest2 tests matched; the family is a regex over test names, for example DECSTR or DECSTRTests",
			family)
	}
	if failed > 0 {
		return fmt.Errorf("family %s: %d passed, %d known bugs, %d failed:\n%s",
			family, passed, known, failed, failingTests(log))
	}
	return nil
}

// failingTests returns the block esctest2 prints after "Failing tests:", or
// the tail of the log when that block is absent.
func failingTests(log string) string {
	const marker = "Failing tests:"
	if i := bytes.LastIndex([]byte(log), []byte(marker)); i >= 0 {
		return tail(log[i:], 20)
	}
	return tail(log, 40)
}

// TestFamilyResult pins the verdict without the dependency.
func TestFamilyResult(t *testing.T) {
	tests := []struct {
		name    string
		family  string
		log     string
		wantErr string
	}{
		{
			name:   "clean family passes",
			family: "DECSTR",
			log:    "*** 12 tests passed, 2 known bugs, 0 tests failed ***",
		},
		{
			name:   "known bugs do not fail the family",
			family: "DECSTR",
			log:    "*** 0 tests passed, 5 known bugs, 0 tests failed ***",
		},
		{
			name:    "a failure fails the family",
			family:  "DECSTR",
			log:     "*** 3 tests passed, 0 known bugs, 1 TEST FAILED ***\nFailing tests:\nDECSTRTests.test_1",
			wantErr: "1 failed",
		},
		{
			name:    "no tests is an unknown family",
			family:  "NOPE",
			log:     "*** 0 tests passed, 0 known bugs, 0 tests failed ***",
			wantErr: `unknown family "NOPE"`,
		},
		{
			name:    "no tally is a harness error",
			family:  "DECSTR",
			log:     "",
			wantErr: "no tally line",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := familyResult(tt.family, tt.log)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("familyResult = %v, want nil", err)
				}
				return
			}
			if err == nil || !bytes.Contains([]byte(err.Error()), []byte(tt.wantErr)) {
				t.Fatalf("familyResult = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestFamilyFlagParses runs the test binary with -family and checks it parses.
// DM_ESCTEST is cleared so the child takes the skip path, not a real run.
func TestFamilyFlagParses(t *testing.T) {
	cmd := exec.Command(os.Args[0],
		"-test.run=^TestEsctestFamily$",
		"-test.v",
		"-family=DECSTR",
	)
	cmd.Env = withoutEnv(os.Environ(), "DM_ESCTEST")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test binary rejected -family: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("DM_ESCTEST")) {
		t.Fatalf("child did not take the skip path:\n%s", out)
	}
}

// TestFamilySkipWithoutDependency checks the gate is green when the
// dependency is absent. The child asks for a real run but has no python3 on
// PATH, so it must skip, not fail.
func TestFamilySkipWithoutDependency(t *testing.T) {
	cmd := exec.Command(os.Args[0],
		"-test.run=^TestEsctestFamily$",
		"-test.v",
		"-family=DECSTR",
	)
	cmd.Env = append(withoutEnv(os.Environ(), "PATH"), "PATH=")
	cmd.Env = append(withoutEnv(cmd.Env, "DM_ESCTEST"), "DM_ESCTEST=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child failed without python3: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("python3 not found")) {
		t.Fatalf("child did not skip for a missing python3:\n%s", out)
	}
}

func withoutEnv(env []string, key string) []string {
	out := env[:0:0]
	for _, e := range env {
		if len(e) >= len(key) && e[:len(key)] == key+"=" {
			continue
		}
		out = append(out, e)
	}
	return out
}
