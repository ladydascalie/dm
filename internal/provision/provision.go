// Package provision installs one harness's copy of dm's teaching kit.
//
// The kit is two artefacts from this repo: the dm-agent extension and the
// dm-fleet skill. Plan names the two targets, Apply and Remove carry the plan
// out, and Check reports what each target holds. Both artefacts are symlinks
// into the repo, so an edit in the repo is live in the harness at once and a
// binary never ships a stale lesson.
//
// The package knows pi through one adapter file, adapter_pi.go. The plan and
// the link rules are harness-free: a second harness adds a function there and
// nothing else in this package learns about it.
package provision

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// The artefacts this repo installs, relative to the repo root, and the names
// they take in the harness.
const (
	extensionSource = "skills/dm-agent/dm-agent.ts"
	extensionName   = "dm-agent.ts"
	skillSource     = "skills/dm-fleet"
	skillName       = "dm-fleet"
)

// ErrNoHarness reports that the harness directory is absent. An install there
// would invent a layout the harness never had, so the plan refuses instead.
var ErrNoHarness = errors.New("provision: harness directory is absent")

// ErrForeignTarget reports an entry at a target that provision did not make.
// Apply refuses the whole plan before it writes anything; Remove leaves the
// entry in place.
var ErrForeignTarget = errors.New("provision: target is not a symlink")

// Harness is one agent harness: the directories it loads extensions and skills
// from. Dir must exist. Apply creates the two subdirectories when they are
// absent.
type Harness struct {
	Name          string
	Dir           string
	ExtensionsDir string
	SkillsDir     string
}

// Target is one symlink in the plan.
type Target struct {
	Name   string
	Link   string
	Source string
}

// Plan is the install for one harness.
type Plan struct {
	Harness  Harness
	RepoRoot string
	Targets  []Target
}

// New returns the install plan for one harness. It refuses when the harness
// directory is absent and names the adapter, because pi is the only one.
func New(repoRoot string, h Harness) (Plan, error) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return Plan{}, fmt.Errorf("provision: repo root %s: %w", repoRoot, err)
	}
	if h.ExtensionsDir == "" || h.SkillsDir == "" {
		return Plan{}, fmt.Errorf("%w: %s has no extension or skill directory", ErrNoHarness, h.Name)
	}
	fi, err := os.Stat(h.Dir)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: %s (%s is the only adapter): %w", ErrNoHarness, h.Dir, h.Name, err)
	}
	if !fi.IsDir() {
		return Plan{}, fmt.Errorf("%w: %s is not a directory (%s is the only adapter)", ErrNoHarness, h.Dir, h.Name)
	}
	return Plan{
		Harness:  h,
		RepoRoot: root,
		Targets: []Target{
			{
				Name:   extensionName,
				Link:   filepath.Join(h.ExtensionsDir, extensionName),
				Source: filepath.Join(root, extensionSource),
			},
			{
				Name:   skillName,
				Link:   filepath.Join(h.SkillsDir, skillName),
				Source: filepath.Join(root, skillSource),
			},
		},
	}, nil
}
