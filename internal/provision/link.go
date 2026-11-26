package provision

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// State is what Check found at one target.
type State string

const (
	// StateOK is a symlink whose target exists inside the repo root.
	StateOK State = "ok"
	// StateMissing is nothing at the link path.
	StateMissing State = "missing"
	// StateDangling is a symlink whose target does not exist.
	StateDangling State = "dangling"
	// StateForeign is an entry provision did not make: a regular file, a
	// directory, or a symlink that resolves outside the repo root.
	StateForeign State = "foreign"
)

// Status is the state of one target.
type Status struct {
	Target Target
	State  State
	// Points is the path the link names, resolved against the link's own
	// directory. It is empty when nothing sits at the link path.
	Points string
}

// Action is what Apply or Remove did to one target.
type Action string

const (
	ActionCreated   Action = "created"
	ActionReplaced  Action = "replaced"
	ActionUnchanged Action = "unchanged"
	ActionRemoved   Action = "removed"
	ActionKept      Action = "kept"
)

// Change is one target and what happened to it.
type Change struct {
	Target Target
	Action Action
}

// Check reports the state of every target and writes nothing.
func Check(p Plan) ([]Status, error) {
	statuses := make([]Status, 0, len(p.Targets))
	for _, t := range p.Targets {
		fi, err := os.Lstat(t.Link)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			statuses = append(statuses, Status{Target: t, State: StateMissing})
			continue
		case err != nil:
			return nil, fmt.Errorf("provision: check %s: %w", t.Link, err)
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			statuses = append(statuses, Status{Target: t, State: StateForeign})
			continue
		}
		points, err := linkTarget(t.Link)
		if err != nil {
			return nil, fmt.Errorf("provision: check %s: %w", t.Link, err)
		}
		status := Status{Target: t, Points: points}
		switch _, err := os.Stat(t.Link); {
		case errors.Is(err, fs.ErrNotExist):
			status.State = StateDangling
		case err != nil:
			return nil, fmt.Errorf("provision: check %s: %w", t.Link, err)
		case !insideRoot(p.RepoRoot, points):
			status.State = StateForeign
		default:
			status.State = StateOK
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

// OK reports whether every target is ok. It is the condition the verb exits
// non-zero on.
func OK(statuses []Status) bool {
	for _, s := range statuses {
		if s.State != StateOK {
			return false
		}
	}
	return true
}

// Apply writes the plan: it creates a missing link, replaces a link that
// points elsewhere, and leaves a correct link alone. It refuses before it
// touches anything when a target holds a regular file or a directory, because
// half an install is worse than none.
func Apply(p Plan) ([]Change, error) {
	if err := guard(p); err != nil {
		return nil, err
	}
	changes := make([]Change, 0, len(p.Targets))
	for _, t := range p.Targets {
		change, err := applyTarget(t)
		if err != nil {
			return changes, err
		}
		changes = append(changes, change)
	}
	return changes, nil
}

// Remove deletes a link only when the path it names sits inside the repo root.
// A regular file, a directory, or a link that points elsewhere is left in
// place and reported as kept.
func Remove(p Plan) ([]Change, error) {
	changes := make([]Change, 0, len(p.Targets))
	for _, t := range p.Targets {
		fi, err := os.Lstat(t.Link)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			changes = append(changes, Change{Target: t, Action: ActionUnchanged})
			continue
		case err != nil:
			return changes, fmt.Errorf("provision: remove %s: %w", t.Link, err)
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			changes = append(changes, Change{Target: t, Action: ActionKept})
			continue
		}
		points, err := linkTarget(t.Link)
		if err != nil {
			return changes, fmt.Errorf("provision: remove %s: %w", t.Link, err)
		}
		if !insideRoot(p.RepoRoot, points) {
			changes = append(changes, Change{Target: t, Action: ActionKept})
			continue
		}
		if err := os.Remove(t.Link); err != nil {
			return changes, fmt.Errorf("provision: remove %s: %w", t.Link, err)
		}
		changes = append(changes, Change{Target: t, Action: ActionRemoved})
	}
	return changes, nil
}

// guard refuses the whole plan when one target holds something provision did
// not make.
func guard(p Plan) error {
	for _, t := range p.Targets {
		fi, err := os.Lstat(t.Link)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("provision: apply %s: %w", t.Link, err)
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			return fmt.Errorf("%w: %s", ErrForeignTarget, t.Link)
		}
	}
	return nil
}

func applyTarget(t Target) (Change, error) {
	fi, err := os.Lstat(t.Link)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(t.Link), 0o755); err != nil {
			return Change{}, fmt.Errorf("provision: apply %s: %w", t.Link, err)
		}
		if err := os.Symlink(t.Source, t.Link); err != nil {
			return Change{}, fmt.Errorf("provision: apply %s: %w", t.Link, err)
		}
		return Change{Target: t, Action: ActionCreated}, nil
	}
	if err != nil {
		return Change{}, fmt.Errorf("provision: apply %s: %w", t.Link, err)
	}
	if fi.Mode()&fs.ModeSymlink == 0 {
		return Change{}, fmt.Errorf("%w: %s", ErrForeignTarget, t.Link)
	}
	points, err := linkTarget(t.Link)
	if err != nil {
		return Change{}, fmt.Errorf("provision: apply %s: %w", t.Link, err)
	}
	if canonical(points) == canonical(t.Source) {
		return Change{Target: t, Action: ActionUnchanged}, nil
	}
	if err := os.Remove(t.Link); err != nil {
		return Change{}, fmt.Errorf("provision: apply %s: %w", t.Link, err)
	}
	if err := os.Symlink(t.Source, t.Link); err != nil {
		return Change{}, fmt.Errorf("provision: apply %s: %w", t.Link, err)
	}
	return Change{Target: t, Action: ActionReplaced}, nil
}

// linkTarget reads a symlink and returns the absolute path it names, whether
// or not that path exists.
func linkTarget(link string) (string, error) {
	name, err := os.Readlink(link)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(name) {
		name = filepath.Join(filepath.Dir(link), name)
	}
	return filepath.Clean(name), nil
}

// insideRoot reports whether path sits at or under root, resolving symlinks on
// both sides. A dangling link still counts as inside when the path it names is
// inside, so Remove can clear a link the repo has outlived.
func insideRoot(root, path string) bool {
	root, path = canonical(root), canonical(path)
	if root == path {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

// canonical returns the absolute path with every existing symlink resolved.
// For a path that does not exist it resolves the longest existing prefix and
// rejoins the rest, so a dangling link still compares against a real root.
func canonical(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	var rest []string
	for dir := abs; ; dir = filepath.Dir(dir) {
		rest = append([]string{filepath.Base(dir)}, rest...)
		parent := filepath.Dir(dir)
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Join(append([]string{resolved}, rest...)...)
		}
		if parent == dir {
			return abs
		}
	}
}
