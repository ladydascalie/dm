package provision

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture is a temp home with a pi tree and a temp repo root holding the two
// artefacts. Tests never touch the real home or the real repo.
type fixture struct {
	home    string
	repo    string
	harness Harness
	plan    Plan
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	repo := t.TempDir()
	home := t.TempDir()
	writeArtefacts(t, repo)
	h := Pi(home)
	if err := os.MkdirAll(h.Dir, 0o755); err != nil {
		t.Fatalf("harness dir: %v", err)
	}
	p, err := New(repo, h)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return fixture{home: home, repo: repo, harness: h, plan: p}
}

func writeArtefacts(t *testing.T, repo string) {
	t.Helper()
	writeFile(t, filepath.Join(repo, extensionSource), "export {};\n")
	writeFile(t, filepath.Join(repo, skillSource, "SKILL.md"), "---\nname: dm-fleet\n---\n")
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func statusOf(t *testing.T, statuses []Status, name string) Status {
	t.Helper()
	for _, s := range statuses {
		if s.Target.Name == name {
			return s
		}
	}
	t.Fatalf("no status for %s", name)
	return Status{}
}

func actionsOf(changes []Change) []Action {
	out := make([]Action, 0, len(changes))
	for _, c := range changes {
		out = append(out, c.Action)
	}
	return out
}

func TestNewNamesTwoTargets(t *testing.T) {
	f := newFixture(t)
	if got := len(f.plan.Targets); got != 2 {
		t.Fatalf("targets = %d, want 2", got)
	}
	want := []struct{ name, link, source string }{
		{
			"dm-agent.ts",
			filepath.Join(f.home, ".pi/agent/extensions/dm-agent.ts"),
			filepath.Join(f.repo, "skills/dm-agent/dm-agent.ts"),
		},
		{
			"dm-fleet",
			filepath.Join(f.home, ".pi/agent/skills/dm-fleet"),
			filepath.Join(f.repo, "skills/dm-fleet"),
		},
	}
	for i, w := range want {
		got := f.plan.Targets[i]
		if got.Name != w.name || got.Link != w.link || got.Source != w.source {
			t.Errorf("target %d = %+v, want %s -> %s named %s", i, got, w.link, w.source, w.name)
		}
	}
	if f.plan.RepoRoot != f.repo {
		t.Errorf("repo root = %s, want %s", f.plan.RepoRoot, f.repo)
	}
}

// TestNewSecondHarness proves the seam the docs promise: a harness that is not
// pi, with one mount and no extension, gets a plan the link rules carry out. A
// second adapter is this test's harness, not a change to the engine.
func TestNewSecondHarness(t *testing.T) {
	repo := t.TempDir()
	writeArtefacts(t, repo)
	home := t.TempDir()
	// A skills-only harness: it reads SKILL.md directories and has no
	// extension concept at all.
	h := Harness{
		Name:   "acme",
		Dir:    home,
		Mounts: []Mount{{Kind: KindSkill, Dir: filepath.Join(home, "skills"), Name: "dm-fleet"}},
	}
	p, err := New(repo, h)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(p.Targets) != 1 {
		t.Fatalf("targets = %d, want the one skill mount", len(p.Targets))
	}
	got := p.Targets[0]
	if want := filepath.Join(home, "skills", "dm-fleet"); got.Link != want {
		t.Errorf("link = %s, want %s", got.Link, want)
	}
	if want := filepath.Join(repo, "skills/dm-fleet"); got.Source != want {
		t.Errorf("source = %s, want %s", got.Source, want)
	}
	changes, err := Apply(p)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(changes) != 1 || changes[0].Action != ActionCreated {
		t.Fatalf("Apply = %+v, want one created link", changes)
	}
	statuses, err := Check(p)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(statuses) != 1 || statuses[0].State != StateOK {
		t.Fatalf("Check = %+v, want one ok link", statuses)
	}
}

func TestNewRefusesEmptyPlan(t *testing.T) {
	repo := t.TempDir()
	writeArtefacts(t, repo)
	if _, err := New(repo, Harness{Name: "acme", Dir: t.TempDir()}); err == nil {
		t.Fatal("New with no mount succeeded, want a refusal")
	}
	h := Harness{Name: "acme", Dir: t.TempDir(), Mounts: []Mount{{Kind: "widget", Dir: t.TempDir(), Name: "w"}}}
	if _, err := New(repo, h); err == nil {
		t.Fatal("New with an unknown artefact kind succeeded, want a refusal")
	}
}

func TestNewRefusesAbsentHarness(t *testing.T) {
	repo := t.TempDir()
	writeArtefacts(t, repo)
	_, err := New(repo, Pi(t.TempDir()))
	if !errors.Is(err, ErrNoHarness) {
		t.Fatalf("New with no harness dir = %v, want ErrNoHarness", err)
	}
}

func TestNewRefusesHarnessFile(t *testing.T) {
	repo := t.TempDir()
	writeArtefacts(t, repo)
	home := t.TempDir()
	file := filepath.Join(home, "not-a-dir")
	writeFile(t, file, "x")
	h := Harness{Name: "pi", Dir: file, Mounts: []Mount{
		{Kind: KindExtension, Dir: filepath.Join(file, "ext"), Name: "dm-agent.ts"},
		{Kind: KindSkill, Dir: filepath.Join(file, "skills"), Name: "dm-fleet"},
	}}
	if _, err := New(repo, h); !errors.Is(err, ErrNoHarness) {
		t.Fatalf("New over a file = %v, want ErrNoHarness", err)
	}
}

func TestCheckReportsMissingBeforeApply(t *testing.T) {
	f := newFixture(t)
	statuses, err := Check(f.plan)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	for _, s := range statuses {
		if s.State != StateMissing {
			t.Errorf("%s = %s, want missing", s.Target.Link, s.State)
		}
		if s.Points != "" {
			t.Errorf("%s points at %q, want empty", s.Target.Link, s.Points)
		}
	}
	if OK(statuses) {
		t.Fatal("OK = true on a missing install")
	}
}

func TestApplyCreatesLinksThenLeavesThemAlone(t *testing.T) {
	f := newFixture(t)
	changes, err := Apply(f.plan)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := actionsOf(changes); len(got) != 2 || got[0] != ActionCreated || got[1] != ActionCreated {
		t.Fatalf("first apply = %v, want two created", got)
	}
	for _, target := range f.plan.Targets {
		points, err := os.Readlink(target.Link)
		if err != nil {
			t.Fatalf("readlink %s: %v", target.Link, err)
		}
		if points != target.Source {
			t.Errorf("%s -> %s, want %s", target.Link, points, target.Source)
		}
	}
	statuses, err := Check(f.plan)
	if err != nil || !OK(statuses) {
		t.Fatalf("check after apply = %+v, %v; want all ok", statuses, err)
	}
	changes, err = Apply(f.plan)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	for _, c := range changes {
		if c.Action != ActionUnchanged {
			t.Errorf("second apply of %s = %s, want unchanged", c.Target.Link, c.Action)
		}
	}
}

func TestApplyReplacesLinkThatPointsElsewhere(t *testing.T) {
	f := newFixture(t)
	// The extension link exists but points at a stray file outside the repo.
	stray := filepath.Join(f.home, "stray.ts")
	writeFile(t, stray, "stray")
	if err := os.MkdirAll(filepath.Dir(f.plan.Targets[0].Link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(stray, f.plan.Targets[0].Link); err != nil {
		t.Fatal(err)
	}
	changes, err := Apply(f.plan)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if changes[0].Action != ActionReplaced {
		t.Errorf("foreign link = %s, want replaced", changes[0].Action)
	}
	if changes[1].Action != ActionCreated {
		t.Errorf("missing link = %s, want created", changes[1].Action)
	}
	if body, err := os.ReadFile(stray); err != nil || string(body) != "stray" {
		t.Errorf("replaced link damaged the stray file: %q, %v", body, err)
	}
	statuses, err := Check(f.plan)
	if err != nil || !OK(statuses) {
		t.Fatalf("check after replace = %+v, %v; want all ok", statuses, err)
	}
}

func TestApplyRefusesRegularFileAndWritesNothing(t *testing.T) {
	f := newFixture(t)
	writeFile(t, f.plan.Targets[0].Link, "hand written")
	_, err := Apply(f.plan)
	if !errors.Is(err, ErrForeignTarget) {
		t.Fatalf("Apply over a regular file = %v, want ErrForeignTarget", err)
	}
	if body, err := os.ReadFile(f.plan.Targets[0].Link); err != nil || string(body) != "hand written" {
		t.Fatalf("regular file changed: %q, %v", body, err)
	}
	if _, err := os.Lstat(f.plan.Targets[1].Link); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("apply wrote the second target after refusing: %v", err)
	}
}

func TestCheckReportsDangling(t *testing.T) {
	f := newFixture(t)
	if _, err := Apply(f.plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(f.repo, skillSource)); err != nil {
		t.Fatal(err)
	}
	statuses, err := Check(f.plan)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	s := statusOf(t, statuses, "dm-fleet")
	if s.State != StateDangling {
		t.Errorf("dm-fleet = %s, want dangling", s.State)
	}
	if !strings.HasSuffix(s.Points, filepath.Join("skills", "dm-fleet")) {
		t.Errorf("dangling points = %q, want the repo skill path", s.Points)
	}
	if OK(statuses) {
		t.Error("OK = true with a dangling link")
	}
}

func TestCheckReportsForeign(t *testing.T) {
	t.Run("regular file", func(t *testing.T) {
		f := newFixture(t)
		writeFile(t, f.plan.Targets[0].Link, "hand written")
		statuses, err := Check(f.plan)
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if s := statusOf(t, statuses, "dm-agent.ts"); s.State != StateForeign {
			t.Errorf("regular file = %s, want foreign", s.State)
		}
		if OK(statuses) {
			t.Error("OK = true with a regular file at a target")
		}
	})
	t.Run("link outside the repo", func(t *testing.T) {
		f := newFixture(t)
		outside := filepath.Join(f.home, "elsewhere.ts")
		writeFile(t, outside, "x")
		if err := os.MkdirAll(filepath.Dir(f.plan.Targets[0].Link), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, f.plan.Targets[0].Link); err != nil {
			t.Fatal(err)
		}
		statuses, err := Check(f.plan)
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if s := statusOf(t, statuses, "dm-agent.ts"); s.State != StateForeign {
			t.Errorf("outside link = %s, want foreign", s.State)
		}
	})
}

func TestRemoveDeletesOwnLinks(t *testing.T) {
	f := newFixture(t)
	if _, err := Apply(f.plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	changes, err := Remove(f.plan)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	for _, c := range changes {
		if c.Action != ActionRemoved {
			t.Errorf("remove of %s = %s, want removed", c.Target.Link, c.Action)
		}
	}
	for _, target := range f.plan.Targets {
		if _, err := os.Lstat(target.Link); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s survived remove: %v", target.Link, err)
		}
	}
	// The repo artefacts are not touched by a link removal.
	if _, err := os.Stat(filepath.Join(f.repo, extensionSource)); err != nil {
		t.Errorf("remove deleted the repo artefact: %v", err)
	}
}

func TestRemoveDropsLinkTheRepoOutlived(t *testing.T) {
	f := newFixture(t)
	if _, err := Apply(f.plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(f.repo, extensionSource)); err != nil {
		t.Fatal(err)
	}
	changes, err := Remove(f.plan)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	for _, c := range changes {
		if c.Action != ActionRemoved {
			t.Errorf("remove of a dangling repo link %s = %s, want removed", c.Target.Link, c.Action)
		}
	}
}

func TestRemoveLeavesForeignEntries(t *testing.T) {
	f := newFixture(t)
	writeFile(t, f.plan.Targets[0].Link, "hand written")
	outside := filepath.Join(f.home, "elsewhere")
	if err := os.MkdirAll(filepath.Dir(f.plan.Targets[1].Link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, f.plan.Targets[1].Link); err != nil {
		t.Fatal(err)
	}
	changes, err := Remove(f.plan)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	for _, c := range changes {
		if c.Action != ActionKept {
			t.Errorf("remove of a foreign %s = %s, want kept", c.Target.Link, c.Action)
		}
	}
	if body, err := os.ReadFile(f.plan.Targets[0].Link); err != nil || string(body) != "hand written" {
		t.Errorf("regular file changed: %q, %v", body, err)
	}
	points, err := os.Readlink(f.plan.Targets[1].Link)
	if err != nil || points != outside {
		t.Errorf("outside link changed: %q, %v", points, err)
	}
}

func TestRemoveOnCleanTreeSaysUnchanged(t *testing.T) {
	f := newFixture(t)
	changes, err := Remove(f.plan)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	for _, c := range changes {
		if c.Action != ActionUnchanged {
			t.Errorf("remove with nothing installed at %s = %s, want unchanged", c.Target.Link, c.Action)
		}
	}
}
