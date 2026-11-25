package archdoc

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DocsDir is the documentation tree whose citations this file checks, relative
// to the module root.
const DocsDir = "docs/internals"

// PackageSymbol is the symbol a citation names when it points at a file's
// package clause or package comment. A file's package is not a declaration, so
// it needs a name of its own.
const PackageSymbol = "package"

// anchoredRe matches a token that is meant to be a citation: it carries a line
// hint or a symbol. A bare path is a file reference and is checked only when it
// is written in backticks.
var anchoredRe = regexp.MustCompile(`([A-Za-z0-9_][A-Za-z0-9_./-]*\.go)(?::([0-9]+)(?:-[0-9]+)?|#([A-Za-z0-9_.]+))?`)

// symbolIndex holds the declarations of every Go file in the module.
type symbolIndex struct {
	files map[string]map[string]bool
}

// loadSymbols parses every Go file under root. A file maps to its package
// clause and to the name of each package-level declaration, method and struct
// field. A method is Type.Method, a field is Type.Field.
func loadSymbols(root string) (*symbolIndex, error) {
	ix := &symbolIndex{files: map[string]map[string]bool{}}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "mockups", "mockups-hints", "mockups-web", "out", "gremlins-results", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return err
		}
		ix.files[filepath.ToSlash(rel)] = symbolsOf(f)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ix, nil
}

// symbolsOf returns the names a citation may name in f.
func symbolsOf(f *ast.File) map[string]bool {
	syms := map[string]bool{PackageSymbol: true}
	for _, d := range f.Decls {
		switch dd := d.(type) {
		case *ast.FuncDecl:
			name := dd.Name.Name
			if dd.Recv != nil && len(dd.Recv.List) > 0 {
				name = receiverName(dd.Recv.List[0].Type) + "." + name
			}
			syms[name] = true
		case *ast.GenDecl:
			for _, s := range dd.Specs {
				switch sp := s.(type) {
				case *ast.TypeSpec:
					syms[sp.Name.Name] = true
					if st, ok := sp.Type.(*ast.StructType); ok {
						for _, fl := range st.Fields.List {
							for _, n := range fl.Names {
								syms[sp.Name.Name+"."+n.Name] = true
							}
						}
					}
				case *ast.ValueSpec:
					for _, n := range sp.Names {
						if n.Name != "_" {
							syms[n.Name] = true
						}
					}
				}
			}
		}
	}
	return syms
}

func receiverName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return receiverName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return receiverName(t.X)
	}
	return "?"
}

// citationError is one citation the checker cannot resolve.
type citationError struct {
	Page   string
	Line   int
	Token  string
	Reason string
}

func (e citationError) String() string {
	return fmt.Sprintf("%s:%d: %s: %s", e.Page, e.Line, e.Token, e.Reason)
}

// CheckCitations walks every page under docs/internals and resolves its
// citations. An anchored citation must name a symbol the file declares; a bare
// path must name a file that exists. A path:line token is an error: a line is
// not a stable anchor.
func CheckCitations(root string) ([]citationError, error) {
	ix, err := loadSymbols(root)
	if err != nil {
		return nil, err
	}
	var out []citationError
	err = filepath.WalkDir(filepath.Join(root, filepath.FromSlash(DocsDir)), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		page, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		page = filepath.ToSlash(page)
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out = append(out, checkPage(page, string(body), ix)...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Page != out[j].Page {
			return out[i].Page < out[j].Page
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

// checkPage resolves the citations of one page.
func checkPage(page, body string, ix *symbolIndex) []citationError {
	var out []citationError
	for _, loc := range anchoredRe.FindAllStringSubmatchIndex(body, -1) {
		if loc[1] < len(body) && isWordByte(body[loc[1]]) {
			continue // a longer name such as config.kdl.good, not a citation
		}
		token := body[loc[0]:loc[1]]
		line := 1 + strings.Count(body[:loc[0]], "\n")
		file := body[loc[2]:loc[3]]
		symbol := ""
		if loc[6] >= 0 {
			symbol = body[loc[6]:loc[7]]
		}
		if loc[4] >= 0 {
			out = append(out, citationError{page, line, token,
				"line-number anchor; cite the symbol as path#symbol"})
			continue
		}
		rel := resolveFile(file, ix)
		if rel == "" {
			out = append(out, citationError{page, line, token,
				fmt.Sprintf("no Go file named %s", file)})
			continue
		}
		if symbol != "" && !ix.files[rel][symbol] {
			out = append(out, citationError{page, line, token,
				fmt.Sprintf("%s does not declare %s", rel, symbol)})
		}
	}
	out = append(out, checkBarePaths(page, body, ix)...)
	return out
}

// bareRe finds a backticked bare path: a path with no line hint and no symbol.
var bareRe = regexp.MustCompile("`([A-Za-z0-9_][A-Za-z0-9_./-]*[.]go)`")

// checkBarePaths resolves the file references a page writes in backticks with
// no anchor. The reference must name a file that exists.
func checkBarePaths(page, body string, ix *symbolIndex) []citationError {
	var out []citationError
	for _, loc := range bareRe.FindAllStringSubmatchIndex(body, -1) {
		token := body[loc[2]:loc[3]]
		line := 1 + strings.Count(body[:loc[0]], "\n")
		if resolveFile(token, ix) == "" {
			out = append(out, citationError{page, line, token,
				fmt.Sprintf("no Go file named %s", token)})
		}
	}
	return out
}

// isWordByte reports whether b continues a file name.
func isWordByte(b byte) bool {
	return b == '.' || b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// resolveFile maps a citation path to a module file. A path with a slash is
// module-relative; a bare name matches a unique file in the tree.
func resolveFile(file string, ix *symbolIndex) string {
	if _, ok := ix.files[file]; ok {
		return file
	}
	base := path.Base(file)
	var found string
	for rel := range ix.files {
		if path.Base(rel) == base {
			if found != "" {
				return ""
			}
			found = rel
		}
	}
	return found
}
