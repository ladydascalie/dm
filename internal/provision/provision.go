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

// The artefacts this repo installs, relative to the repo root. The paths are
// the repo side: a harness never names them, it names a Kind.
const (
	extensionSource = "skills/dm-agent/dm-agent.ts"
	skillSource     = "skills/dm-fleet"
)

// Kind names one artefact the kit installs.
type Kind string

const (
	// KindExtension is a program the harness loads as an extension or a
	// plugin. It is the only harness-specific artefact: its body calls the
	// harness's own tool API.
	KindExtension Kind = "extension"
	// KindSkill is a directory holding SKILL.md, in the Agent Skills layout
	// every harness on the list reads.
	KindSkill Kind = "skill"
)

// kit is the repo side of every artefact: the path a mount's Kind points at.
// A new artefact is one row here and one Mount in each harness that carries it.
var kit = map[Kind]string{
	KindExtension: extensionSource,
	KindSkill:     skillSource,
}

// Mount is one artefact placed in a harness: where its link lands, and the
// name it takes there. A harness with no extension concept lists no extension
// mount, so one mount is a complete install.
type Mount struct {
	Kind Kind
	Dir  string
	Name string
}

// Harness is one agent harness: its root and the artefacts it can carry. A
// second harness is one adapter function returning this value; nothing in the
// plan, the link rules or the verbs asks which harness it is.
//
// Dir must exist. Apply creates a mount's directory when it is absent, so a
// harness that has never had an extension gets one.
type Harness struct {
	Name   string
	Dir    string
	Mounts []Mount
}

// ErrNoHarness reports that the harness directory is absent. An install there
// would invent a layout the harness never had, so the plan refuses instead.
var ErrNoHarness = errors.New("provision: harness directory is absent")

// ErrForeignTarget reports an entry at a target that provision did not make.
// Apply refuses the whole plan before it writes anything; Remove leaves the
// entry in place.
var ErrForeignTarget = errors.New("provision: target is not a symlink")

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
// New returns the install plan for one harness. The plan is built from the
// harness's mounts and the kit table alone: this function names no harness.
// It refuses a harness root that is absent, a harness with no mount, a mount
// of an unknown kind, a mount with no directory or name, and two mounts that
// would land on the same path.
func New(repoRoot string, h Harness) (Plan, error) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return Plan{}, fmt.Errorf("provision: repo root %s: %w", repoRoot, err)
	}
	if len(h.Mounts) == 0 {
		return Plan{}, fmt.Errorf("provision: %s mounts no artefact", h.Name)
	}
	fi, err := os.Stat(h.Dir)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: %s (%s): %w", ErrNoHarness, h.Dir, h.Name, err)
	}
	if !fi.IsDir() {
		return Plan{}, fmt.Errorf("%w: %s is not a directory (%s)", ErrNoHarness, h.Dir, h.Name)
	}
	targets := make([]Target, 0, len(h.Mounts))
	seen := make(map[string]bool, len(h.Mounts))
	for _, m := range h.Mounts {
		source, known := kit[m.Kind]
		if !known {
			return Plan{}, fmt.Errorf("provision: %s mounts unknown artefact %q", h.Name, m.Kind)
		}
		if m.Dir == "" || m.Name == "" {
			return Plan{}, fmt.Errorf("provision: %s mounts %q with no directory or name", h.Name, m.Kind)
		}
		link := filepath.Join(m.Dir, m.Name)
		if seen[link] {
			return Plan{}, fmt.Errorf("provision: %s mounts two artefacts at %s", h.Name, link)
		}
		seen[link] = true
		targets = append(targets, Target{
			Name:   m.Name,
			Link:   link,
			Source: filepath.Join(root, source),
		})
	}
	return Plan{Harness: h, RepoRoot: root, Targets: targets}, nil
}
