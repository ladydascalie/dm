package clidoc

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"strings"
	"testing"
)

// runCLICases reads cli.go and returns the string labels of every case in
// Run's switch, plus whether the switch has a default clause. The default
// is the bare-word attach fallback.
func runCLICases(t *testing.T) (labels []string, hasDefault bool) {
	t.Helper()
	fset := gotoken.NewFileSet()
	f, err := parser.ParseFile(fset, "../cli/cli.go", nil, 0)
	if err != nil {
		t.Fatalf("parse ../cli/cli.go: %v", err)
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Run" {
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
		t.Fatal("Run not found in ../cli/cli.go")
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
		t.Fatal("no case labels found in Run")
	}
	for _, label := range labels {
		if !covered(label) {
			t.Errorf("Run case %q has no row in cliCommands", label)
		}
	}
	if !hasDefault {
		t.Fatal("Run has no default clause for the bare-word attach fallback")
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
			t.Errorf("cliCommands row %q has no case in Run", c.name)
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
	text := UsageText()
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

// TestGroupsAreCompleteAndOrdered proves every row carries a group the table
// knows, each group's rows sit together in table order, no group is empty,
// and UsageText renders the sections in that order.
func TestGroupsAreCompleteAndOrdered(t *testing.T) {
	ids := map[string]int{}
	for i, g := range cliGroups {
		ids[g.id] = i
	}
	last := map[string]int{}
	for i, c := range cliCommands {
		if _, ok := ids[c.group]; !ok {
			t.Errorf("row %q carries unknown group %q", c.name, c.group)
			continue
		}
		if prev, ok := last[c.group]; ok && prev != i-1 {
			t.Errorf("row %q sits outside its group's run: %q was row %d, this is row %d", c.name, c.group, prev, i)
		}
		last[c.group] = i
	}
	for _, g := range cliGroups {
		if _, ok := last[g.id]; !ok {
			t.Errorf("group %q has no rows", g.title)
		}
	}
	text := UsageText()
	pos := -1
	for _, g := range cliGroups {
		p := strings.Index(text, g.title+"\n")
		if p < 0 {
			t.Errorf("usage text does not carry the %q section", g.title)
			continue
		}
		if p < pos {
			t.Errorf("usage text renders %q out of order", g.title)
		}
		pos = p
	}
}

// TestVerbHelpCarriesTheRow proves VerbHelp renders a verb's summary and its
// flags, and returns nothing for a name the table does not carry.
func TestVerbHelpCarriesTheRow(t *testing.T) {
	text := VerbHelp("split")
	for _, want := range []string{"dm split <pane> --dir D", "split a pane: left right up down", "--dir D", "dm split -h"} {
		if !strings.Contains(text, want) {
			t.Errorf("VerbHelp(split) does not carry %q", want)
		}
	}
	if !strings.Contains(VerbHelp("read"), "print N rows above the screen") {
		t.Error("VerbHelp(read) does not carry the scrollback form")
	}
	if got := VerbHelp("no-such-verb"); got != "" {
		t.Errorf("VerbHelp(unknown) = %q, want empty", got)
	}
}
