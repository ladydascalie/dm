package cli

// dm provision installs dm's teaching kit into the one harness dm drives, pi.
// The plan and the link rules live in internal/provision; this file is the
// verb: the flags, the repo root the kit ships from, and the lines it prints.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dm/internal/provision"
)

// kitFile marks a dm checkout: the extension provision links. The verb walks
// up from the working directory for it, so `dm provision` in a checkout needs
// no path flag, and the link names the same file the binary does.
const kitFile = "skills/dm-agent/dm-agent.ts"

// errNoKitRoot reports a working directory with no dm checkout above it.
var errNoKitRoot = errors.New("provision: no dm checkout above the working directory")

// runProvision reads the three flags, builds the plan for pi, and carries out
// the one action asked for. Apply is the default, because installing is what
// the verb is for; --check, --print and --uninstall each turn it into a
// report instead.
func runProvision(c *cmdFlags) error {
	if c.check && c.print || c.check && c.uninstall || c.print && c.uninstall {
		return errors.New("provision: --check, --print and --uninstall are exclusive")
	}
	root, err := kitRoot()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("provision: home directory: %w", err)
	}
	plan, err := provision.New(root, provision.Pi(home))
	if err != nil {
		return err
	}
	switch {
	case c.check:
		return checkProvision(plan)
	case c.print:
		return printProvision(plan)
	case c.uninstall:
		changes, err := provision.Remove(plan)
		if err != nil {
			return err
		}
		for _, ch := range changes {
			fmt.Printf("%s\t%s\t%s\n", ch.Action, ch.Target.Name, ch.Target.Link)
		}
		return nil
	default:
		changes, err := provision.Apply(plan)
		if err != nil {
			return err
		}
		for _, ch := range changes {
			fmt.Printf("%s\t%s\t%s\n", ch.Action, ch.Target.Name, ch.Target.Link)
		}
		return nil
	}
}

// checkProvision reports every target and refuses with the names of the ones
// that are not ok, so the exit status and the message agree.
func checkProvision(p provision.Plan) error {
	statuses, err := provision.Check(p)
	if err != nil {
		return err
	}
	bad := make([]string, 0, len(statuses))
	for _, s := range statuses {
		fmt.Printf("%s\t%s\t%s\n", s.State, s.Target.Name, s.Target.Link)
		if s.State != provision.StateOK {
			bad = append(bad, fmt.Sprintf("%s (%s)", s.Target.Name, s.State))
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("provision: %s", strings.Join(bad, ", "))
	}
	return nil
}

// printProvision prints the plan it would carry out, with the file each link
// names, and writes nothing.
func printProvision(p provision.Plan) error {
	statuses, err := provision.Check(p)
	if err != nil {
		return err
	}
	for _, s := range statuses {
		fmt.Printf("%s\t%s\t%s -> %s\n", s.State, s.Target.Name, s.Target.Link, s.Target.Source)
	}
	return nil
}

// kitRoot finds the dm checkout provision links from. It is the nearest
// directory at or above the working directory that holds the extension, so a
// run from a subdirectory or a test finds the same repo, and a run outside a
// checkout refuses rather than link a file that is not there.
func kitRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("provision: working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, kitFile)); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("%w: no %s", errNoKitRoot, filepath.Join("skills", "dm-agent", "dm-agent.ts"))
		}
		dir = parent
	}
}
