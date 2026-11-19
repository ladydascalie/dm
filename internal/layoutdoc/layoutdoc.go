// Package layoutdoc owns the layout file: a named JSON document that holds a
// session's shape so it can be captured once and applied to a fresh session.
//
// The keys mirror state.json, so a save is a projection of session state and an
// apply is a restore with fresh ids. Pane ids inside a file are local to it: a
// document can never collide with a live session's ids, because BuildInto
// assigns every id from the target's own counters.
//
// The package holds the document and nothing else: no server and no CLI.
// Project reads a session.State, so a capture needs no running daemon.
package layoutdoc

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"dm/internal/layout"
)

// Version is the document version this package reads and writes. Load refuses
// any other version, so a newer file fails loudly instead of applying halfway.
const Version = 1

// Doc is one layout document, the file this package reads and writes.
type Doc struct {
	Version    int             `json:"version"`
	Name       string          `json:"name"`
	Workspaces []Workspace     `json:"workspaces"`
	Panes      map[string]Pane `json:"panes"`
}

// Workspace is one workspace in the document: its name, its project and the
// windows it holds in rail order.
type Workspace struct {
	Name    string   `json:"name"`
	Project string   `json:"project,omitempty"`
	Active  int      `json:"active"`
	Windows []Window `json:"windows"`
}

// Window is one window in the document: its label, its focused pane and the
// split tree those panes hang from.
type Window struct {
	Name  string       `json:"name"`
	Focus string       `json:"focus"`
	Root  *layout.Node `json:"root"`
}

// Pane is the launch data a layout keeps for one pane. Nothing else travels:
// the shell, the recorded status and the agent name belong to the session that
// had them, not to the shape.
type Pane struct {
	Cwd  string   `json:"cwd,omitempty"`
	Argv []string `json:"argv,omitempty"`
}

// Validate reports whether the document is a layout this package can apply.
// Every refusal names the field it refused, with the workspace and the window
// index that carry it, and the pane id whose launch data it refused.
func (d *Doc) Validate() error {
	if d.Version != Version {
		return fmt.Errorf("version %d: want %d", d.Version, Version)
	}
	if len(d.Workspaces) == 0 {
		return errors.New("workspaces: empty")
	}
	names := make(map[string]bool, len(d.Workspaces))
	// owner maps a pane id to the tree that already holds it, so one pane can
	// never be placed twice.
	owner := make(map[string]string)
	for i, ws := range d.Workspaces {
		wp := fmt.Sprintf("workspaces[%d]", i)
		if ws.Name == "" {
			return fmt.Errorf("%s.name: empty", wp)
		}
		if names[ws.Name] {
			return fmt.Errorf("%s.name: duplicate workspace name %q", wp, ws.Name)
		}
		names[ws.Name] = true
		if len(ws.Windows) == 0 {
			return fmt.Errorf("%s.windows: empty", wp)
		}
		for j, w := range ws.Windows {
			winPath := fmt.Sprintf("%s.windows[%d]", wp, j)
			if w.Name == "" {
				return fmt.Errorf("%s.name: empty", winPath)
			}
			ids, err := leaves(w.Root)
			if err != nil {
				return fmt.Errorf("%s.root: %w", winPath, err)
			}
			if len(ids) == 0 {
				return fmt.Errorf("%s.root: no pane leaf", winPath)
			}
			inTree := make(map[string]bool, len(ids))
			for _, id := range ids {
				if inTree[id] {
					return fmt.Errorf("%s.root: pane %q appears twice in the tree", winPath, id)
				}
				inTree[id] = true
				if other, taken := owner[id]; taken {
					return fmt.Errorf("%s.root: pane %q is already in %s", winPath, id, other)
				}
				owner[id] = winPath
			}
			if !inTree[w.Focus] {
				return fmt.Errorf("%s.focus: pane %q is not a leaf of this window", winPath, w.Focus)
			}
			for _, id := range ids {
				p, ok := d.Panes[id]
				if !ok {
					return fmt.Errorf("%s.root: pane %q is missing from panes", winPath, id)
				}
				if err := validatePane(id, p); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// validatePane refuses launch data the server cannot carry back to a pane.
// layoutdoc cannot import server, so the rules the server's own validateArgv
// enforces are stated here again: an empty element makes the pane run the shell
// instead of the named program, a comma is the --argv flag's separator so the
// element arrives split and the pane replays a different program, and a NUL
// byte makes the spawn fail. A relative cwd is resolved against the server
// process's cwd, so the same file would mean a different folder depending on
// where dm ran; an empty cwd is kept, because it means the pane's own default.
func validatePane(id string, p Pane) error {
	for i, a := range p.Argv {
		switch {
		case a == "":
			return fmt.Errorf("panes[%q].argv[%d]: empty", id, i)
		case strings.ContainsRune(a, ','):
			return fmt.Errorf("panes[%q].argv[%d]: contains a comma", id, i)
		case strings.ContainsRune(a, '\x00'):
			return fmt.Errorf("panes[%q].argv[%d]: contains a NUL byte", id, i)
		}
	}
	if p.Cwd != "" && !filepath.IsAbs(p.Cwd) {
		return fmt.Errorf("panes[%q].cwd: %q is not absolute", id, p.Cwd)
	}
	return nil
}

// leaves collects the pane ids of a tree in layout.Walk order. It refuses a
// node that holds both a pane id and kids, because Walk reads such a node as a
// leaf, so the kids would never reach the session and the layout would lose
// panes. It also refuses a node that holds neither a pane id nor kids, because
// the window would keep a half with no id to focus or close; a tree with no
// leaf at all keeps the whole-window refusal the caller names instead.
func leaves(n *layout.Node) ([]string, error) {
	var out []string
	dead := ""
	var walk func(*layout.Node, string) error
	walk = func(n *layout.Node, path string) error {
		if n == nil {
			return nil
		}
		if n.Pane != "" {
			if len(n.Kids) > 0 {
				return fmt.Errorf("node %q holds a pane id and kids", n.Pane)
			}
			out = append(out, n.Pane)
			return nil
		}
		if len(n.Kids) == 0 {
			if dead == "" {
				dead = path
			}
			return nil
		}
		for i, k := range n.Kids {
			if err := walk(k, fmt.Sprintf("%s.kids[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(n, "root"); err != nil {
		return nil, err
	}
	if len(out) > 0 && dead != "" {
		return nil, fmt.Errorf("node at %s has no kids and no pane id", dead)
	}
	return out, nil
}
