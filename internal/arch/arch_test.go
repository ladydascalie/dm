package arch

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	archgo "github.com/arch-go/arch-go/api"
	"github.com/arch-go/arch-go/api/configuration"
)

// TestArchitecture is the layer gate. It builds one arch-go rule per layer of
// the model and fails when a package imports across a layer boundary the model
// forbids.
func TestArchitecture(t *testing.T) {
	rules := dependencyRules()
	result := archgo.CheckArchitecture(configuration.Load(ModulePath), configuration.Config{
		DependenciesRules: rules,
	})
	if !result.Pass {
		t.Fatalf("architecture gate failed\n%s", describe(result))
	}
	if result.DependenciesRuleResult == nil {
		t.Fatal("arch-go ran no dependency rule")
	}
	// A rule whose pattern matches no package passes silently, so check that
	// every rule saw exactly the packages its layer lists.
	results := result.DependenciesRuleResult.Results
	if len(results) != len(Layers) {
		t.Fatalf("arch-go ran %d rules, the model has %d layers", len(results), len(Layers))
	}
	for i, rule := range results {
		if want := len(Layers[i].Packages); len(rule.Verifications) != want {
			t.Errorf("layer %s: the rule selected %d packages, the model lists %d",
				Layers[i].Name, len(rule.Verifications), want)
		}
	}
}

// TestEveryPackageInOneLayer fails when the tree gains a package that no layer
// holds, when a layer holds a package twice, and when a layer names a package
// the module does not have.
func TestEveryPackageInOneLayer(t *testing.T) {
	graph, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	held := make(map[string]string)
	for _, layer := range Layers {
		for _, path := range layer.Packages {
			if other, duplicate := held[path]; duplicate {
				t.Errorf("%s sits in both the %s and the %s layer", path, other, layer.Name)
				continue
			}
			held[path] = layer.Name
			if !graph.Has(path) {
				t.Errorf("the %s layer holds %s, which the module does not have", layer.Name, path)
			}
		}
	}
	for _, pkg := range graph.Packages {
		if _, ok := held[pkg.Path]; !ok {
			t.Errorf("%s has no layer: add it to a layer in internal/arch/arch.go", pkg.Path)
		}
	}
}

// TestModel checks the shape of the model itself: named layers exist, a layer
// that may import others may import itself, and every list is sorted so the
// generated docs are stable.
func TestModel(t *testing.T) {
	for _, layer := range Layers {
		if len(layer.Packages) == 0 {
			t.Errorf("the %s layer holds no package", layer.Name)
		}
		if !sort.StringsAreSorted(layer.Packages) {
			t.Errorf("the %s layer lists its packages out of order", layer.Name)
		}
		for _, name := range layer.MayImport {
			if _, ok := LayerNamed(name); !ok {
				t.Errorf("the %s layer may import %s, which is not a layer", layer.Name, name)
			}
		}
		if len(layer.MayImport) > 0 && !importsItself(layer) {
			t.Errorf("the %s layer may import others but the model does not list itself", layer.Name)
		}
	}
}

// TestGateReportsForbiddenImport proves the gate reads the tree. internal/theme
// imports internal/cells. A rule that forbids that edge must fail and must name
// the importing package, the import and the rule.
func TestGateReportsForbiddenImport(t *testing.T) {
	cfg := configuration.Config{DependenciesRules: []*configuration.DependenciesRule{{
		Package:            pattern([]string{"dm/internal/theme"}),
		ShouldNotDependsOn: &configuration.Dependencies{Internal: []string{pattern([]string{"dm/internal/cells"})}},
	}}}
	result := archgo.CheckArchitecture(configuration.Load(ModulePath), cfg)
	if result.Pass {
		t.Fatal("arch-go accepted a rule that the tree breaks")
	}
	message := describe(result)
	for _, want := range []string{"dm/internal/theme", "dm/internal/cells"} {
		if !strings.Contains(message, want) {
			t.Errorf("the failure message does not name %s:\n%s", want, message)
		}
	}
	t.Logf("failure message:\n%s", message)
}

// TestNamedImport checks both detail wordings of arch-go, one of which
// contains an apostrophe before the import path.
func TestNamedImport(t *testing.T) {
	for _, detail := range []string{
		`ShouldOnlyDependsOn.Internal rule doesn't contains imported package 'dm/internal/cells'`,
		`ShouldNotDependsOn.Internal rule contains imported package 'dm/internal/cells'`,
	} {
		if got := namedImport(detail); got != "dm/internal/cells" {
			t.Errorf("namedImport(%q) = %q", detail, got)
		}
	}
}

// TestRuleText proves that a failure names the layer, not the raw pattern.
func TestRuleText(t *testing.T) {
	rules := dependencyRules()
	if len(rules) != len(Layers) {
		t.Fatalf("got %d rules, want %d", len(rules), len(Layers))
	}
	for i, layer := range Layers {
		got := ruleText(rules[i].Package)
		if layer.Name == "leaf" {
			if !strings.Contains(got, "leaf") || !strings.Contains(got, "no package") {
				t.Errorf("the leaf rule reads %q", got)
			}
			continue
		}
		want := "layer " + layer.Name + " may import only "
		if !strings.HasPrefix(got, want) {
			t.Errorf("the %s rule reads %q, want the prefix %q", layer.Name, got, want)
		}
	}
}

func importsItself(layer Layer) bool {
	for _, name := range layer.MayImport {
		if name == layer.Name {
			return true
		}
	}
	return false
}

// dependencyRules builds one arch-go rule per layer. A rule allows exactly the
// packages of the layers the model names. A layer that names no layer may
// import nothing from the module.
func dependencyRules() []*configuration.DependenciesRule {
	rules := make([]*configuration.DependenciesRule, 0, len(Layers))
	for _, layer := range Layers {
		rule := &configuration.DependenciesRule{Package: pattern(layer.Packages)}
		if len(layer.MayImport) == 0 {
			rule.ShouldNotDependsOn = &configuration.Dependencies{Internal: []string{modulePattern()}}
		} else {
			rule.ShouldOnlyDependsOn = &configuration.Dependencies{
				Internal: []string{pattern(PackagesOf(layer.MayImport...))},
			}
		}
		rules = append(rules, rule)
	}
	return rules
}

// pattern returns an anchored regular expression that matches exactly paths.
// arch-go leaves a pattern unanchored, so dm/internal/box would also match
// dm/internal/boxview without the anchors.
func pattern(paths []string) string {
	quoted := make([]string, len(paths))
	for i, path := range paths {
		quoted[i] = regexp.QuoteMeta(path)
	}
	return `^(` + strings.Join(quoted, "|") + `)$`
}

// modulePattern matches every import path of this module.
func modulePattern() string {
	return `^` + regexp.QuoteMeta(ModulePath) + `$|^` + regexp.QuoteMeta(ModulePath) + `/`
}

// describe turns an arch-go Result into one paragraph per violation: the
// importing package, the import it may not use, and the rule it broke.
func describe(result *archgo.Result) string {
	if result.DependenciesRuleResult == nil {
		return "arch-go returned no dependency rule result"
	}
	var out strings.Builder
	for _, rule := range result.DependenciesRuleResult.Results {
		if rule.Passes {
			continue
		}
		for _, verification := range rule.Verifications {
			if verification.Passes {
				continue
			}
			for _, detail := range verification.Details {
				fmt.Fprintf(&out, "%s imports %s\n", verification.Package, namedImport(detail))
				fmt.Fprintf(&out, "  rule: %s\n", ruleText(rule.Rule.Package))
			}
		}
	}
	return out.String()
}

// namedImport returns the import path that arch-go quotes in a detail line, or
// the whole line when the wording of arch-go changes. The detail may contain an
// apostrophe, as in "doesn't", so the last quoted run is the import path.
func namedImport(detail string) string {
	end := strings.LastIndex(detail, "'")
	if end < 0 {
		return detail
	}
	start := strings.LastIndex(detail[:end], "'")
	if start < 0 {
		return detail
	}
	return detail[start+1 : end]
}

// ruleText names the layer a rule guards and the layers it allows.
func ruleText(rulePattern string) string {
	for _, layer := range Layers {
		if pattern(layer.Packages) != rulePattern {
			continue
		}
		if len(layer.MayImport) == 0 {
			return "layer " + layer.Name + " imports no package of this module"
		}
		return "layer " + layer.Name + " may import only " + strings.Join(layer.MayImport, ", ")
	}
	return "rule " + rulePattern
}
