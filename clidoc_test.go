package main

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"strings"
	"testing"
)

// runCLICases reads main.go and returns the string labels of every case in
// runCLI's switch, plus whether the switch has a default clause. The default
// is the bare-word attach fallback.
func runCLICases(t *testing.T) (labels []string, hasDefault bool) {
	t.Helper()
	fset := gotoken.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "runCLI" {
			return true
		}
		found = true
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			for _, stmt := range sw.Body.List {
				cc, ok := stmt.(*ast.CaseClause)
				if !ok {
					continue
				}
				if cc.List == nil {
					hasDefault = true
					continue
				}
				for _, e := range cc.List {
					bl, ok := e.(*ast.BasicLit)
					if !ok || bl.Kind != gotoken.STRING {
						continue
					}
					labels = append(labels, strings.Trim(bl.Value, `"`))
				}
			}
			return false
		})
		return false
	})
	if !found {
		t.Fatal("runCLI not found in main.go")
	}
	return labels, hasDefault
}

// covered reports whether a case label is named by the table, either directly
// or as the first word of a compound name such as "msg post".
func covered(label string) bool {
	for _, c := range cliCommands {
		if c.name == label {
			return true
		}
		for _, a := range c.aliases {
			if a == label {
				return true
			}
		}
		if strings.HasPrefix(c.name, label+" ") {
			return true
		}
	}
	return false
}

func TestTableCoversRunCLI(t *testing.T) {
	labels, hasDefault := runCLICases(t)
	if len(labels) == 0 {
		t.Fatal("no case labels found in runCLI")
	}
	for _, label := range labels {
		if !covered(label) {
			t.Errorf("runCLI case %q has no row in cliCommands", label)
		}
	}
	if !hasDefault {
		t.Fatal("runCLI has no default clause for the bare-word attach fallback")
	}
	bare := false
	for _, c := range cliCommands {
		if c.name == "" {
			bare = true
		}
	}
	if !bare {
		t.Error("cliCommands has no row for the bare-word attach fallback")
	}
}

func TestTableNamesHaveCase(t *testing.T) {
	labels, _ := runCLICases(t)
	set := make(map[string]bool, len(labels))
	for _, l := range labels {
		set[l] = true
	}
	for _, c := range cliCommands {
		if c.name == "" {
			continue
		}
		first := c.name
		if i := strings.IndexByte(first, ' '); i >= 0 {
			first = first[:i]
		}
		if !set[first] {
			t.Errorf("cliCommands row %q has no case in runCLI", c.name)
		}
	}
}

func TestNoDuplicateNamesOrAliases(t *testing.T) {
	seen := map[string]string{}
	add := func(word, owner string) {
		if prev, ok := seen[word]; ok {
			t.Errorf("%q is both %q and %q", word, prev, owner)
			return
		}
		seen[word] = owner
	}
	for _, c := range cliCommands {
		if c.name != "" {
			add(c.name, c.name)
		}
		for _, a := range c.aliases {
			add(a, c.name)
		}
	}
}

func TestUsageNamesEveryCommand(t *testing.T) {
	text := usageText()
	for _, c := range cliCommands {
		if !strings.Contains(text, c.line()) {
			t.Errorf("usage text does not name %q", c.line())
		}
	}
}

func TestEveryRowHasSummary(t *testing.T) {
	for _, c := range cliCommands {
		if strings.TrimSpace(c.summary) == "" {
			t.Errorf("cliCommands row %q has an empty summary", c.name)
		}
	}
}
