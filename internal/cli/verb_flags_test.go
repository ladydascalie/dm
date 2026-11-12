package cli

// Per-verb flag registration. The clidoc table is the parse-time source of
// truth: a verb registers exactly the flags its row declares, its Run path
// reads only fields those flags set, and the usage printer shows that row
// alone.

import (
	"bytes"
	"flag"
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"strings"
	"testing"

	"dm/internal/clidoc"
	"dm/internal/testutil"
)

// ownFlagSet are the verbs that keep their own flag.FlagSet instead of the
// cmdFlags, so the per-verb tests skip them.
var ownFlagSet = map[string]bool{"man": true, "llms": true, "html": true}

// parseVerb builds one verb's flag set the way Run does and parses the args a
// bare invocation would carry. A msg subverb travels as the positional parse
// looks for.
func parseVerb(t *testing.T, verb string) *cmdFlags {
	t.Helper()
	name := verb
	args := []string{}
	if sub, ok := strings.CutPrefix(verb, "msg "); ok {
		name = "msg"
		args = append(args, sub)
	}
	c := newFlagsAt(t.TempDir(), name)
	c.parse(args)
	return c
}

// allFlagNames is every flag name the table declares, once.
func allFlagNames() []string {
	var names []string
	seen := map[string]bool{}
	for _, verb := range clidoc.Verbs() {
		for _, f := range clidoc.VerbFlags(verb) {
			if !seen[f.Name] {
				seen[f.Name] = true
				names = append(names, f.Name)
			}
		}
	}
	return names
}

// TestVerbParsesExactlyItsTableFlags runs one subtest per verb: the set
// carries every flag the row declares, nothing else, and one flag another row
// declares is absent, which is the parse-time proof of refusal.
func TestVerbParsesExactlyItsTableFlags(t *testing.T) {
	t.Parallel()
	every := allFlagNames()
	for _, verb := range clidoc.Verbs() {
		if ownFlagSet[verb] {
			continue
		}
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			c := parseVerb(t, verb)
			declared := map[string]bool{}
			for _, f := range clidoc.VerbFlags(verb) {
				declared[f.Name] = true
				if c.fs.Lookup(f.Name) == nil {
					t.Errorf("dm %s does not register --%s", verb, f.Name)
				}
			}
			n := 0
			c.fs.VisitAll(func(*flag.Flag) { n++ })
			if n != len(declared) {
				t.Errorf("dm %s registers %d flags, want %d", verb, n, len(declared))
			}
			for _, name := range every {
				if declared[name] {
					continue
				}
				if c.fs.Lookup(name) != nil {
					t.Errorf("dm %s registers --%s, which its table row does not declare", verb, name)
				}
				break // one foreign flag absent is the proof
			}
		})
	}
}

// flagDefaults is the default every registered flag must keep, as the flag
// package renders a DefValue. -s is absent: its default follows DM_SESSION.
var flagDefaults = map[string]string{
	"msg": "", "enter": "false", "dir": "right", "lines": "0",
	"scrollback": "-1", "json": "false", "key": "", "prefix": "",
	"state": "", "body": "", "body-file": "", "seq": "-1", "from": "",
	"limit": "0", "after": "-1", "timeout": "5m0s", "dry-run": "false",
	"all": "false", "force": "false", "action-json": "",
}

// TestFlagDefaultsSurvivePerVerbRegistration proves the per-verb sets keep
// the defaults the shared set carried.
func TestFlagDefaultsSurvivePerVerbRegistration(t *testing.T) {
	t.Parallel()
	for _, verb := range clidoc.Verbs() {
		if ownFlagSet[verb] {
			continue
		}
		c := parseVerb(t, verb)
		for _, f := range clidoc.VerbFlags(verb) {
			if f.Name == "s" {
				continue
			}
			got := c.fs.Lookup(f.Name)
			if got == nil {
				t.Errorf("dm %s does not register --%s", verb, f.Name)
				continue
			}
			if want := flagDefaults[f.Name]; got.DefValue != want {
				t.Errorf("dm %s --%s defaults to %q, want %q", verb, f.Name, got.DefValue, want)
			}
		}
	}
}

// fieldFlag maps a cmdFlags field to the flag that sets it.
var fieldFlag = map[string]string{
	"session": "s", "msg": "msg", "enter": "enter", "dir": "dir",
	"msgJSON": "action-json", "lines": "lines", "scrollback": "scrollback",
	"jsonOut": "json", "key": "key", "keyPrefix": "prefix", "state": "state",
	"body": "body", "bodyFile": "body-file", "seq": "seq", "from": "from",
	"limit": "limit", "after": "after", "timeout": "timeout", "dry": "dry-run",
	"all": "all", "force": "force",
}

// cmdFlagsMethods are the methods the walk recurses into. parse is missing on
// purpose: it registers flags instead of reading them.
var cmdFlagsMethods = map[string]bool{"boxCursor": true}

// notFlags are the cmdFlags fields no flag sets.
var notFlags = map[string]bool{"fs": true, "args": true, "root": true, "usage": true}

// TestEveryVerbReadsOnlyDeclaredFlags walks Run's switch and every function
// its cases reach that reads the cmdFlags, and proves each field read is a
// flag the verb's table row declares. A read the table does not declare is a
// bug: the verb would eat a flag its help never shows.
func TestEveryVerbReadsOnlyDeclaredFlags(t *testing.T) {
	t.Parallel()
	fset := gotoken.NewFileSet()
	f, err := parser.ParseFile(fset, "cli.go", nil, 0)
	if err != nil {
		t.Fatalf("parse cli.go: %v", err)
	}
	// The functions that read the cmdFlags: a *cmdFlags receiver or param.
	readers := map[string]*ast.FuncDecl{}
	takesFlags := func(ty ast.Expr) bool {
		star, ok := ty.(*ast.StarExpr)
		return ok && fmtIdent(star.X, "cmdFlags")
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fn.Recv != nil && len(fn.Recv.List) > 0 && takesFlags(fn.Recv.List[0].Type) {
			readers[fn.Name.Name] = fn
			continue
		}
		for _, p := range fn.Type.Params.List {
			if takesFlags(p.Type) {
				readers[fn.Name.Name] = fn
				break
			}
		}
	}
	// Run builds its cmdFlags inside the cases, so it is a reader by name.
	var run *ast.FuncDecl
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "Run" {
			run = fn
		}
	}
	if run == nil {
		t.Fatal("Run not found in cli.go")
	}

	// walk collects the flags one node's field reads set, following calls to
	// the other readers. visited breaks the recursion on a cycle.
	var walk func(node ast.Node, into map[string]bool, visited map[string]bool)
	walk = func(node ast.Node, into map[string]bool, visited map[string]bool) {
		ast.Inspect(node, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if ok {
				switch fun := call.Fun.(type) {
				case *ast.SelectorExpr:
					if id, ok := fun.X.(*ast.Ident); ok && id.Name == "c" &&
						cmdFlagsMethods[fun.Sel.Name] && !visited[fun.Sel.Name] {
						visited[fun.Sel.Name] = true
						walk(readers[fun.Sel.Name].Body, into, visited)
					}
				case *ast.Ident:
					if inner, ok := readers[fun.Name]; ok && inner != run && !visited[fun.Name] {
						visited[fun.Name] = true
						walk(inner.Body, into, visited)
					}
				}
				return true
			}
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok || id.Name != "c" {
				return true
			}
			name := sel.Sel.Name
			switch {
			case name == "parse", cmdFlagsMethods[name], notFlags[name]:
			case fieldFlag[name] != "":
				into[fieldFlag[name]] = true
			default:
				t.Errorf("cmdFlags field %q is not mapped to a flag; extend fieldFlag or drop the field", name)
			}
			return true
		})
	}

	// verbOf maps a Run case label to the table row that parses it. The bare
	// fallback parses as attach.
	verbOf := func(label string) string {
		if label == "" {
			return "attach" // the bare fallback parses as attach
		}
		aliases := map[string]string{"a": "attach", "write": "write-to", "list-sessions": "ls"}
		if v, ok := aliases[label]; ok {
			return v
		}
		for _, v := range clidoc.Verbs() {
			if v == label {
				return v
			}
		}
		return label
	}

	// declaredOf gathers the flags a verb's rows declare. The msg case
	// dispatches on args, so its rows are the union of the msg subverbs.
	declaredOf := func(verb string) map[string]bool {
		out := map[string]bool{}
		for _, v := range clidoc.Verbs() {
			if verb == "msg" && !strings.HasPrefix(v, "msg ") {
				continue
			}
			if verb != "msg" && v != verb {
				continue
			}
			for _, fl := range clidoc.VerbFlags(v) {
				out[fl.Name] = true
			}
		}
		return out
	}

	// dispatch checks a verb whose body dispatches on its first argument:
	// each case label L must read only the flags the row prefix+L declares.
	dispatch := func(fn *ast.FuncDecl, prefix string) int {
		checked := 0
		for _, stmt := range fn.Body.List {
			sw, ok := stmt.(*ast.SwitchStmt)
			if !ok {
				continue
			}
			for _, stmt := range sw.Body.List {
				cc, ok := stmt.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, e := range cc.List {
					bl, ok := e.(*ast.BasicLit)
					if !ok || bl.Kind != gotoken.STRING {
						continue
					}
					label := strings.Trim(bl.Value, `"`)
					reads := map[string]bool{}
					walk(cc, reads, map[string]bool{fn.Name.Name: true})
					declared := declaredOf(prefix + label)
					if len(declared) == 0 {
						t.Errorf("%s case %q maps to no table row", fn.Name.Name, prefix+label)
						continue
					}
					checked++
					for name := range reads {
						if !declared[name] {
							t.Errorf("dm %s%s reads %q, which its table row does not declare", prefix, label, name)
						}
					}
				}
			}
		}
		if checked == 0 {
			t.Errorf("%s dispatches no cases", fn.Name.Name)
		}
		return checked
	}

	skip := map[string]bool{"man": true, "llms": true, "html": true}
	found := 0
	for _, stmt := range run.Body.List {
		sw, ok := stmt.(*ast.SwitchStmt)
		if !ok {
			continue // the early return for a bare dm
		}
		for _, stmt := range sw.Body.List {
			cc, ok := stmt.(*ast.CaseClause)
			if !ok {
				continue
			}
			labels := []string{""}
			if cc.List != nil {
				labels = nil
				for _, e := range cc.List {
					bl, ok := e.(*ast.BasicLit)
					if !ok || bl.Kind != gotoken.STRING {
						continue
					}
					labels = append(labels, strings.Trim(bl.Value, `"`))
				}
			}
			reads := map[string]bool{}
			walk(cc, reads, map[string]bool{"Run": true})
			for _, label := range labels {
				verb := verbOf(label)
				if skip[verb] {
					continue // man, llms and html keep their own flag set
				}
				if verb == "msg" {
					// The msg case dispatches on args[0], so each subverb
					// is checked against its own row, and the case body
					// itself against the union.
					reads := map[string]bool{}
					walk(cc, reads, map[string]bool{"Run": true, "runBox": true})
					for name := range reads {
						if !declaredOf("msg")[name] {
							t.Errorf("dm msg reads %q, which no msg row declares", name)
						}
					}
					found += dispatch(readers["runBox"], "msg ")
					continue
				}
				declared := declaredOf(verb)
				if len(declared) == 0 {
					t.Errorf("Run case %q maps to no table row", label)
					continue
				}
				found++
				for name := range reads {
					if !declared[name] {
						t.Errorf("dm %s reads %q, which its table row does not declare", verb, name)
					}
				}
			}
		}
	}
	if found == 0 {
		t.Fatal("no cases walked")
	}
}

// fmtIdent reports whether expr is the identifier name.
func fmtIdent(expr ast.Expr, name string) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == name
}

// TestMsgWithNoSubcommandPrintsTheList proves dm msg prints the subcommand
// list instead of a flag dump or an error.
func TestMsgWithNoSubcommandPrintsTheList(t *testing.T) {
	t.Parallel()
	out := testutil.CaptureStdout(t, func() {
		if err := Run([]string{"msg"}); err != nil {
			t.Fatalf("dm msg: %v", err)
		}
	})
	for _, want := range []string{"dm msg post", "dm msg list", "dm msg wait", "dm msg schema"} {
		if !strings.Contains(out, want) {
			t.Errorf("dm msg does not name %q", want)
		}
	}
}

// usageOf renders one verb's usage printer into a buffer.
func usageOf(t *testing.T, c *cmdFlags) string {
	t.Helper()
	var b bytes.Buffer
	c.fs.SetOutput(&b)
	c.fs.Usage()
	return b.String()
}

// TestVerbUsagePrintsOnlyItsOwnFlags proves the usage printer shows the
// verb's summary and its flags alone.
func TestVerbUsagePrintsOnlyItsOwnFlags(t *testing.T) {
	t.Parallel()
	c := parseVerb(t, "split")
	text := usageOf(t, c)
	for _, want := range []string{"split a pane: left right up down", "--dir D", "--json", "dm split -h"} {
		if !strings.Contains(text, want) {
			t.Errorf("split usage does not carry %q", want)
		}
	}
	for _, no := range []string{"--state", "--dry-run", "--scrollback"} {
		if strings.Contains(text, no) {
			t.Errorf("split usage carries %q", no)
		}
	}
}

// TestMsgUsageFollowsTheSubverb proves dm msg wait -h prints wait's help,
// and a msg with no subverb prints the subcommand list.
func TestMsgUsageFollowsTheSubverb(t *testing.T) {
	t.Parallel()
	c := parseVerb(t, "msg wait")
	text := usageOf(t, c)
	for _, want := range []string{"block until an entry matches", "--timeout D", "--json"} {
		if !strings.Contains(text, want) {
			t.Errorf("msg wait usage does not carry %q", want)
		}
	}
	for _, no := range []string{"--dry-run", "dm msg list"} {
		if strings.Contains(text, no) {
			t.Errorf("msg wait usage carries %q", no)
		}
	}
	c = parseVerb(t, "msg")
	text = usageOf(t, c)
	for _, want := range []string{"dm msg post", "dm msg schema", "dm msg <verb> -h"} {
		if !strings.Contains(text, want) {
			t.Errorf("msg usage does not carry %q", want)
		}
	}
	for _, want := range []string{"dm msg show", "dm msg sub", "dm msg clear", "dm msg watchers"} {
		if !strings.Contains(text, want) {
			t.Errorf("msg usage does not name %q", want)
		}
	}
}

// TestBareFlagUsageCarriesTheGroupedSurface proves the bare-word fallback
// raises the grouped top-level usage, since a bad flag there is not one
// verb's mistake.
func TestBareFlagUsageCarriesTheGroupedSurface(t *testing.T) {
	t.Parallel()
	c := newFlagsAt(t.TempDir(), "attach")
	c.usage = clidoc.UsageText
	c.parse(nil)
	text := usageOf(t, c)
	for _, want := range []string{"Sessions", "Panes", "Windows and workspaces", "Observe", "Message box", "Docs"} {
		if !strings.Contains(text, want) {
			t.Errorf("top-level usage does not carry the %q section", want)
		}
	}
}
