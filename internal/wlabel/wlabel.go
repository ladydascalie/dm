// Package wlabel renders a window label from a Go text/template. The tab bar,
// the sidebar window row and the status bar chip share it, so one window reads
// the same in every place.
package wlabel

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
)

// Default is the template a config without a window-label node uses. A window
// the user named shows that name; an unnamed window shows its id and the base
// of its repository root, e.g. "w1 dm".
const Default = `{{if .Named}}{{.Name}}{{else}}{{.ID}}{{with base .Root}} {{.}}{{end}}{{end}}`

// Vars are the values a window label template can reference.
type Vars struct {
	// ID is the window id, e.g. w1.
	ID string
	// Name is the window's name, the label rename-window edits.
	Name string
	// Named is true when the user set the name with rename-window.
	Named bool
	// Cwd is the source pane's working directory.
	Cwd string
	// Root is the source pane's repository root, empty outside git.
	Root string
}

// funcs are the template functions a label may call.
var funcs = template.FuncMap{
	"base": base,
}

// cache holds one parsed template per source string. A label renders on every
// frame, so parsing once per distinct template keeps the draw cheap.
var cache sync.Map // map[string]*template.Template

// parse returns the parsed template for src, or an error.
func parse(src string) (*template.Template, error) {
	if t, ok := cache.Load(src); ok {
		if t == nil {
			return nil, fmt.Errorf("invalid template %q", src)
		}
		return t.(*template.Template), nil
	}
	t, err := template.New("label").Funcs(funcs).Parse(src)
	if err != nil {
		cache.Store(src, nil)
		return nil, err
	}
	cache.Store(src, t)
	return t, nil
}

// Render expands tmpl with v. An empty tmpl uses Default. A template that does
// not parse or execute comes back unchanged, so a bad config never paints a
// blank label.
func Render(tmpl string, v Vars) string {
	if tmpl == "" {
		tmpl = Default
	}
	t, err := parse(tmpl)
	if err != nil {
		return tmpl
	}
	var b strings.Builder
	if err := t.Execute(&b, v); err != nil {
		return tmpl
	}
	return b.String()
}

// Check reports whether tmpl is a valid window label template. An empty
// template is valid and means Default. It parses the template and executes it
// against a zero Vars, so an unknown field or function fails at config load
// instead of at draw time.
func Check(tmpl string) error {
	if tmpl == "" {
		return nil
	}
	t, err := parse(tmpl)
	if err != nil {
		return err
	}
	var b strings.Builder
	if err := t.Execute(&b, Vars{}); err != nil {
		return err
	}
	return nil
}

// base is the last path element, with the filesystem root spelled out.
func base(p string) string {
	if p == "" {
		return ""
	}
	p = filepath.Clean(p)
	b := filepath.Base(p)
	if b == "/" || b == "." || b == string(filepath.Separator) {
		return "root"
	}
	return b
}
