// Package arch holds the shipped layer model of the dm module and the package
// graph the model is checked against.
//
// The model is the single source of truth. The gate in arch_test.go builds its
// rules from it, and internal/archdoc renders the package graph page from it.
// Add a package to Layers when the tree gains one.
package arch

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// ModulePath is the import path of this module.
const ModulePath = "dm"

// Layer is one tier of the package model.
type Layer struct {
	// Name is the short name used by the gate, the docs and the failures.
	Name string
	// Packages are the full import paths this layer holds.
	Packages []string
	// MayImport names the layers this layer may import, itself included.
	// An empty list means the layer imports nothing from the module.
	MayImport []string
}

// Layers is the shipped model, ordered from the top of the stack down.
//
// The order is the documentation order. It states nothing the gate reads: the
// gate reads MayImport.
var Layers = []Layer{
	{
		Name:      "entry",
		Packages:  []string{"dm", "dm/cmd/dm-wasm", "dm/cmd/esctest-harness"},
		MayImport: []string{"entry", "services", "presentation", "domain", "leaf", "tooling"},
	},
	{
		Name: "services",
		Packages: []string{
			"dm/internal/cli",
			"dm/internal/client",
			"dm/internal/pane",
			"dm/internal/proxy",
			"dm/internal/server",
			"dm/internal/transport",
			"dm/internal/websession",
			"dm/internal/webui",
		},
		MayImport: []string{"services", "presentation", "domain", "leaf"},
	},
	{
		Name: "presentation",
		Packages: []string{
			"dm/internal/boxview",
			"dm/internal/clidoc",
			"dm/internal/configdoc",
			"dm/internal/esctest",
			"dm/internal/hintbar",
			"dm/internal/keymapview",
			"dm/internal/palette",
			"dm/internal/panel",
			"dm/internal/rail",
			"dm/internal/segment",
			"dm/internal/shot",
			"dm/internal/switcher",
			"dm/internal/testutil",
			"dm/internal/ui",
		},
		MayImport: []string{"presentation", "domain", "leaf"},
	},
	{
		Name: "domain",
		Packages: []string{
			"dm/internal/ask",
			"dm/internal/box",
			"dm/internal/keymap",
			"dm/internal/layout",
			"dm/internal/layoutdoc",
			"dm/internal/notify",
			"dm/internal/osc",
			"dm/internal/secrets",
			"dm/internal/session",
			"dm/internal/theme",
			"dm/internal/vt",
			"dm/internal/wire",
		},
		MayImport: []string{"domain", "leaf"},
	},
	{
		Name: "leaf",
		Packages: []string{
			"dm/blackbox",
			"dm/internal/browser",
			"dm/internal/cells",
			"dm/internal/kdl",
			"dm/internal/lesson",
			"dm/internal/paths",
			"dm/internal/ptyspawn",
			"dm/internal/redact",
			"dm/internal/webshell",
			"dm/internal/wlabel",
		},
	},
	{
		Name:      "tooling",
		Packages:  []string{"dm/internal/arch", "dm/internal/archdoc"},
		MayImport: []string{"tooling", "domain", "leaf"},
	},
}

// LayerNamed returns the layer called name.
func LayerNamed(name string) (Layer, bool) {
	for _, l := range Layers {
		if l.Name == name {
			return l, true
		}
	}
	return Layer{}, false
}

// PackagesOf returns the import paths of the named layers, in model order.
func PackagesOf(names ...string) []string {
	var out []string
	for _, l := range Layers {
		for _, name := range names {
			if l.Name == name {
				out = append(out, l.Packages...)
				break
			}
		}
	}
	return out
}

// Package is one node of the import graph.
type Package struct {
	// Path is the import path.
	Path string
	// Imports holds every import the package declares, sorted.
	Imports []string
}

// Graph is the import graph of the module: one node per package.
type Graph struct {
	// Packages is sorted by Path.
	Packages []Package
}

// Edge is one import that starts and ends inside the module.
type Edge struct {
	From string
	To   string
}

var (
	loadOnce sync.Once
	loaded   *Graph
	loadErr  error
)

// Load returns the import graph of the module.
//
// It runs the Go toolchain once per process, from the module root, and caches
// the result. Later calls return the same graph.
func Load() (*Graph, error) {
	loadOnce.Do(func() { loaded, loadErr = load() })
	return loaded, loadErr
}

func load() (*Graph, error) {
	root, err := moduleRoot()
	if err != nil {
		return nil, err
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("go", "list", "-f", `{{.ImportPath}} {{join .Imports " "}}`, "./...")
	cmd.Dir = root
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go list in %s: %w: %s", root, err, strings.TrimSpace(stderr.String()))
	}
	graph := &Graph{}
	for _, line := range strings.Split(stdout.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		imports := append([]string(nil), fields[1:]...)
		sort.Strings(imports)
		graph.Packages = append(graph.Packages, Package{Path: fields[0], Imports: imports})
	}
	sort.Slice(graph.Packages, func(i, j int) bool { return graph.Packages[i].Path < graph.Packages[j].Path })
	return graph, nil
}

// moduleRoot returns the directory that holds the go.mod of the module.
func moduleRoot() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMOD: %w", err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		return "", errors.New("not inside a Go module: the graph needs a go.mod")
	}
	return filepath.Dir(gomod), nil
}

// Has reports whether the module has a package at path.
func (g *Graph) Has(path string) bool {
	for _, p := range g.Packages {
		if p.Path == path {
			return true
		}
	}
	return false
}

// Edges returns the internal edges, sorted by From then To.
func (g *Graph) Edges() []Edge {
	var out []Edge
	for _, p := range g.Packages {
		for _, imp := range p.Imports {
			if g.Has(imp) {
				out = append(out, Edge{From: p.Path, To: imp})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out
}
