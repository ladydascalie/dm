package cli

// The provision verb end to end, against a temp home and a temp checkout:
// apply, the idempotent second run, --print, --check, --uninstall, and the two
// refusals. Every test moves HOME and the working directory, so none runs
// parallel and none touches the operator's pi tree.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dm/internal/testutil"
)

// provisionFixture is a temp checkout with the two artefacts and a temp home
// with a pi tree, entered as the working directory and HOME.
type provisionFixture struct {
	home string
	repo string
}

const (
	extensionPath = "skills/dm-agent/dm-agent.ts"
	skillPath     = "skills/dm-fleet"
	extensionLink = ".pi/agent/extensions/dm-agent.ts"
	skillLink     = ".pi/agent/skills/dm-fleet"
)

func newProvisionFixture(t *testing.T) provisionFixture {
	t.Helper()
	f := provisionFixture{home: t.TempDir(), repo: t.TempDir()}
	for _, path := range []string{extensionPath, skillPath} {
		full := filepath.Join(f.repo, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("kit\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(f.home, ".pi", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", f.home)
	t.Chdir(f.repo)
	return f
}

// readLink returns what the harness entry points at, or empty when nothing is
// there.
func (f provisionFixture) readLink(t *testing.T, link string) string {
	t.Helper()
	got, err := os.Readlink(filepath.Join(f.home, link))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("readlink %s: %v", link, err)
	}
	return got
}

// runProvisionVerb runs the verb and keeps every line it printed.
func runProvisionVerb(t *testing.T, argv ...string) (string, error) {
	t.Helper()
	var runErr error
	out := testutil.CaptureStdout(t, func() { runErr = Run(argv) })
	return out, runErr
}

// TestProvisionAppliesTheTwoLinks proves the default run creates both links
// into the harness and names what it changed.
func TestProvisionAppliesTheTwoLinks(t *testing.T) {
	f := newProvisionFixture(t)
	out, err := runProvisionVerb(t, "provision")
	if err != nil {
		t.Fatalf("dm provision: %v", err)
	}
	if got, want := f.readLink(t, extensionLink), filepath.Join(f.repo, extensionPath); got != want {
		t.Errorf("extension link names %q, want %q", got, want)
	}
	if got, want := f.readLink(t, skillLink), filepath.Join(f.repo, skillPath); got != want {
		t.Errorf("skill link names %q, want %q", got, want)
	}
	if got := strings.Count(out, "created"); got != 2 {
		t.Errorf("dm provision printed %q, want two created lines", out)
	}
}

// TestProvisionIsIdempotent proves a second apply says unchanged and leaves
// the links alone.
func TestProvisionIsIdempotent(t *testing.T) {
	f := newProvisionFixture(t)
	if _, err := runProvisionVerb(t, "provision"); err != nil {
		t.Fatalf("dm provision: %v", err)
	}
	before := f.readLink(t, skillLink)
	out, err := runProvisionVerb(t, "provision")
	if err != nil {
		t.Fatalf("dm provision: %v", err)
	}
	if got := strings.Count(out, "unchanged"); got != 2 {
		t.Errorf("second dm provision printed %q, want two unchanged lines", out)
	}
	if strings.Contains(out, "created") || strings.Contains(out, "replaced") {
		t.Errorf("second dm provision claims a change: %q", out)
	}
	if got := f.readLink(t, skillLink); got != before {
		t.Errorf("second dm provision moved the skill link to %q", got)
	}
}

// TestProvisionPrintWritesNothing proves --print reports the plan and leaves
// both targets alone.
func TestProvisionPrintWritesNothing(t *testing.T) {
	f := newProvisionFixture(t)
	out, err := runProvisionVerb(t, "provision", "--print")
	if err != nil {
		t.Fatalf("dm provision --print: %v", err)
	}
	for _, link := range []string{extensionLink, skillLink} {
		if got := f.readLink(t, link); got != "" {
			t.Errorf("dm provision --print wrote %s -> %q", link, got)
		}
	}
	for _, want := range []string{"missing", "dm-agent.ts", "dm-fleet", filepath.Join(f.repo, extensionPath)} {
		if !strings.Contains(out, want) {
			t.Errorf("dm provision --print does not carry %q: %q", want, out)
		}
	}
}

// TestProvisionCheckNamesEachBadTarget proves --check fails on every state it
// must refuse and names the target and the state, and passes once both links
// are in place.
func TestProvisionCheckNamesEachBadTarget(t *testing.T) {
	f := newProvisionFixture(t)
	out, err := runProvisionVerb(t, "provision", "--check")
	if err == nil {
		t.Fatal("dm provision --check passed on a missing install")
	}
	for _, want := range []string{"dm-agent.ts (missing)", "dm-fleet (missing)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("check refusal misses %q: %v", want, err)
		}
	}
	if !strings.Contains(out, "missing") {
		t.Errorf("check printed no state line: %q", out)
	}

	// A regular file where the extension goes is foreign, never replaced.
	foreign := filepath.Join(f.home, extensionLink)
	if err := os.MkdirAll(filepath.Dir(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runProvisionVerb(t, "provision", "--check"); err == nil ||
		!strings.Contains(err.Error(), "dm-agent.ts (foreign)") {
		t.Fatalf("check over a regular file = %v, want a foreign refusal", err)
	}
	if b, err := os.ReadFile(foreign); err != nil || string(b) != "mine\n" {
		t.Fatalf("check disturbed the regular file: %q, %v", b, err)
	}
}

// TestProvisionCheckNamesADanglingLink proves a link whose source is gone is
// dangling, not ok.
func TestProvisionCheckNamesADanglingLink(t *testing.T) {
	f := newProvisionFixture(t)
	if _, err := runProvisionVerb(t, "provision"); err != nil {
		t.Fatalf("dm provision: %v", err)
	}
	if err := os.Remove(filepath.Join(f.repo, skillPath)); err != nil {
		t.Fatal(err)
	}
	_, err := runProvisionVerb(t, "provision", "--check")
	if err == nil || !strings.Contains(err.Error(), "dm-fleet (dangling)") {
		t.Fatalf("check over a dangling link = %v, want a dangling refusal", err)
	}
}

// TestProvisionCheckExitsClean proves both links in place is a silent success.
func TestProvisionCheckExitsClean(t *testing.T) {
	newProvisionFixture(t)
	if _, err := runProvisionVerb(t, "provision"); err != nil {
		t.Fatalf("dm provision: %v", err)
	}
	out, err := runProvisionVerb(t, "provision", "--check")
	if err != nil {
		t.Fatalf("dm provision --check after apply: %v", err)
	}
	if got := strings.Count(out, "ok"); got != 2 {
		t.Errorf("check printed %q, want two ok lines", out)
	}
}

// TestProvisionUninstallKeepsAFileItDidNotMake proves --uninstall removes the
// link it owns and leaves a regular file at the other target alone.
func TestProvisionUninstallKeepsAFileItDidNotMake(t *testing.T) {
	f := newProvisionFixture(t)
	if _, err := runProvisionVerb(t, "provision"); err != nil {
		t.Fatalf("dm provision: %v", err)
	}
	if err := os.Remove(filepath.Join(f.home, skillLink)); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(f.home, skillLink)
	if err := os.WriteFile(mine, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runProvisionVerb(t, "provision", "--uninstall")
	if err != nil {
		t.Fatalf("dm provision --uninstall: %v", err)
	}
	if got := f.readLink(t, extensionLink); got != "" {
		t.Errorf("uninstall left the owned link %s -> %q", extensionLink, got)
	}
	if b, err := os.ReadFile(mine); err != nil || string(b) != "mine\n" {
		t.Fatalf("uninstall removed the regular file: %q, %v", b, err)
	}
	for _, want := range []string{"removed", "kept"} {
		if !strings.Contains(out, want) {
			t.Errorf("uninstall printed %q, want a %q line", out, want)
		}
	}
}

// TestProvisionRefusesWithoutPiDir proves an absent harness is a named
// refusal that says pi is the only adapter.
func TestProvisionRefusesWithoutPiDir(t *testing.T) {
	newProvisionFixture(t)
	if err := os.RemoveAll(filepath.Join(os.Getenv("HOME"), ".pi")); err != nil {
		t.Fatal(err)
	}
	_, err := runProvisionVerb(t, "provision")
	if err == nil {
		t.Fatal("dm provision without a pi tree was accepted")
	}
	for _, want := range []string{"harness directory is absent", "pi is the only adapter"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal misses %q: %v", want, err)
		}
	}
}

// TestProvisionRefusesTwoActions proves the three flags are exclusive.
func TestProvisionRefusesTwoActions(t *testing.T) {
	newProvisionFixture(t)
	for _, argv := range [][]string{
		{"provision", "--check", "--print"},
		{"provision", "--check", "--uninstall"},
		{"provision", "--print", "--uninstall"},
	} {
		_, err := runProvisionVerb(t, argv...)
		if err == nil || !strings.Contains(err.Error(), "exclusive") {
			t.Fatalf("dm %v = %v, want an exclusivity refusal", argv, err)
		}
	}
}

// TestProvisionRefusesOutsideACheckout proves a working directory with no dm
// checkout above it is refused instead of linking a file that is not there.
func TestProvisionRefusesOutsideACheckout(t *testing.T) {
	newProvisionFixture(t)
	t.Chdir(t.TempDir())
	_, err := runProvisionVerb(t, "provision")
	if err == nil || !strings.Contains(err.Error(), "no dm checkout") {
		t.Fatalf("dm provision outside a checkout = %v, want a named refusal", err)
	}
}

// TestProvisionRefusesASurplusArgument proves the verb takes none.
func TestProvisionRefusesASurplusArgument(t *testing.T) {
	newProvisionFixture(t)
	_, err := runProvisionVerb(t, "provision", "extra")
	if err == nil || !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("dm provision extra = %v, want a surplus-positional refusal", err)
	}
}
