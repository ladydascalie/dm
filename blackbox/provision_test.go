package blackbox

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// provisionKeep is what a user's own file holds when it stands where a link
// would go. Uninstall must leave these bytes untouched.
const provisionKeep = "hand-written, not dm's\n"

// provisionHarness is a built dm and a temp HOME holding the pi harness tree.
//
// The binary runs with its working directory at the module root, because dm
// provision finds the checkout by walking up for skills/dm-agent/dm-agent.ts.
// HOME carries one fake harness: .pi/agent with both directories pi loads
// extensions and skills from, so the test never reads or writes the real home.
type provisionHarness struct {
	t    *testing.T
	bin  string
	env  []string
	root string
	home string
}

func newProvisionHarness(t *testing.T) *provisionHarness {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("module root: %v", err)
	}
	home := t.TempDir()
	for _, dir := range []string{
		filepath.Join(home, ".pi", "agent", "extensions"),
		filepath.Join(home, ".pi", "agent", "skills"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("fake harness: %v", err)
		}
	}
	// testEnv drops every dm and XDG override; HOME goes too, so the binary
	// is the only process that sees this home and no reader finds two values.
	env := make([]string, 0, len(os.Environ())+1)
	for _, e := range testEnv(os.Environ()) {
		if strings.HasPrefix(e, "HOME=") {
			continue
		}
		env = append(env, e)
	}
	env = append(env, "HOME="+home)
	return &provisionHarness{t: t, bin: buildOnce(t), env: env, root: root, home: home}
}

func (h *provisionHarness) run(args ...string) string {
	h.t.Helper()
	out, err := h.try(args...)
	if err != nil {
		h.t.Fatalf("dm %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func (h *provisionHarness) try(args ...string) (string, error) {
	cmd := exec.Command(h.bin, args...)
	cmd.Env = h.env
	cmd.Dir = h.root
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// provisionLines compares whole output lines, so a verb that prints extra
// chatter fails rather than passes on a substring.
func provisionLines(t *testing.T, out string, want ...string) {
	t.Helper()
	got := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if !slices.Equal(got, want) {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func provisionAssertLink(t *testing.T, link, want string) {
	t.Helper()
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("link %s: %v", link, err)
	}
	if fi.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("%s is %s, want a symlink", link, fi.Mode())
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink %s: %v", link, err)
	}
	if got != want {
		t.Fatalf("link %s -> %s, want %s", link, got, want)
	}
	if _, err := os.Stat(link); err != nil {
		t.Fatalf("link %s does not resolve: %v", link, err)
	}
}

// TestProvision walks the verb through its whole life against a real binary:
// install, install again, a user file standing where a link was, uninstall.
func TestProvision(t *testing.T) {
	h := newProvisionHarness(t)
	agent := filepath.Join(h.home, ".pi", "agent")
	ext := filepath.Join(agent, "extensions", "dm-agent.ts")
	skill := filepath.Join(agent, "skills", "dm-fleet")
	extSource := filepath.Join(h.root, "skills", "dm-agent", "dm-agent.ts")
	skillSource := filepath.Join(h.root, "skills", "dm-fleet")

	// Apply writes both links into the harness directories and names what it
	// changed, one line per target.
	provisionLines(t, h.run("provision"),
		"created\tdm-agent.ts\t"+ext,
		"created\tdm-fleet\t"+skill,
	)
	provisionAssertLink(t, ext, extSource)
	provisionAssertLink(t, skill, skillSource)

	// A second apply is idempotent: same links, and it reports no change.
	provisionLines(t, h.run("provision"),
		"unchanged\tdm-agent.ts\t"+ext,
		"unchanged\tdm-fleet\t"+skill,
	)
	provisionAssertLink(t, ext, extSource)
	provisionAssertLink(t, skill, skillSource)

	// A clean tree passes check before the test breaks it.
	provisionLines(t, h.run("provision", "--check"),
		"ok\tdm-agent.ts\t"+ext,
		"ok\tdm-fleet\t"+skill,
	)

	// A regular file replaces one link. Check must report it foreign and exit
	// non-zero, naming the target.
	if err := os.Remove(ext); err != nil {
		t.Fatalf("remove link: %v", err)
	}
	if err := os.WriteFile(ext, []byte(provisionKeep), 0o644); err != nil {
		t.Fatalf("write regular file: %v", err)
	}
	out, err := h.try("provision", "--check")
	if err == nil {
		t.Fatalf("check passed with a regular file at %s:\n%s", ext, out)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("check exit = %v, want exit status 1:\n%s", err, out)
	}
	for _, want := range []string{
		"foreign\tdm-agent.ts\t" + ext,
		"ok\tdm-fleet\t" + skill,
		"dm: provision: dm-agent.ts (foreign)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("check output %q does not contain %q", out, want)
		}
	}

	// Uninstall removes the link it owns and leaves the regular file alone,
	// bytes and all. The repo artefacts are untouched either way.
	provisionLines(t, h.run("provision", "--uninstall"),
		"kept\tdm-agent.ts\t"+ext,
		"removed\tdm-fleet\t"+skill,
	)
	if _, err := os.Lstat(skill); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("skill link after uninstall: %v, want it removed", err)
	}
	fi, err := os.Lstat(ext)
	if err != nil {
		t.Fatalf("regular file after uninstall: %v", err)
	}
	if fi.Mode()&fs.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		t.Fatalf("%s is %s after uninstall, want a regular file", ext, fi.Mode())
	}
	kept, err := os.ReadFile(ext)
	if err != nil {
		t.Fatalf("read kept file: %v", err)
	}
	if string(kept) != provisionKeep {
		t.Fatalf("kept file holds %q, want %q", kept, provisionKeep)
	}
	for _, src := range []string{extSource, skillSource} {
		if _, err := os.Stat(src); err != nil {
			t.Fatalf("repo artefact %s: %v", src, err)
		}
	}
}
