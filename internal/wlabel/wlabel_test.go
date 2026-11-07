package wlabel

import "testing"

func TestRender(t *testing.T) {
	v := Vars{ID: "w1", Name: "user-provided-name", Named: true, Cwd: "/home/b/Code/Personal/dm", Root: "/home/b/Code/Personal/dm"}
	cases := []struct {
		tmpl string
		want string
	}{
		{"", "user-provided-name"},
		{"{{.Name}}", "user-provided-name"},
		{"{{.ID}} - {{.Name}} - {{base .Cwd}}", "w1 - user-provided-name - dm"},
		{"{{.Cwd}}", "/home/b/Code/Personal/dm"},
		{"{{base .Root}}", "dm"},
		{"plain", "plain"},
		{"{{.Name}}/{{.ID}}", "user-provided-name/w1"},
		{"{{.Unknown}}", "{{.Unknown}}"},
		{"{{base .Cwd}}", "dm"},
	}
	for _, c := range cases {
		if got := Render(c.tmpl, v); got != c.want {
			t.Errorf("Render(%q) = %q, want %q", c.tmpl, got, c.want)
		}
	}
}

func TestRenderDefault(t *testing.T) {
	named := Vars{ID: "w1", Name: "api", Named: true, Root: "/work/dm"}
	if got := Render("", named); got != "api" {
		t.Errorf("named default = %q, want api", got)
	}
	unnamed := Vars{ID: "w1", Name: "dm", Root: "/work/dm"}
	if got := Render("", unnamed); got != "w1 dm" {
		t.Errorf("unnamed default = %q, want %q", got, "w1 dm")
	}
	noRoot := Vars{ID: "w2", Name: "dm"}
	if got := Render("", noRoot); got != "w2" {
		t.Errorf("no-root default = %q, want w2", got)
	}
}

func TestRenderBaseRoot(t *testing.T) {
	if got := Render("{{base .Cwd}}", Vars{Cwd: "/"}); got != "root" {
		t.Errorf("root base = %q, want root", got)
	}
	if got := Render("{{base .Cwd}}", Vars{}); got != "" {
		t.Errorf("empty base = %q, want empty", got)
	}
}

func TestCheck(t *testing.T) {
	ok := []string{"", "{{.Name}}", "{{if .Named}}{{.Name}}{{else}}{{.ID}} {{base .Root}}{{end}}", "plain"}
	for _, tmpl := range ok {
		if err := Check(tmpl); err != nil {
			t.Errorf("Check(%q) = %v, want nil", tmpl, err)
		}
	}
	bad := []string{"{{.Nope}}", "{{base .Nope}}", "{{.Name"}
	for _, tmpl := range bad {
		if err := Check(tmpl); err == nil {
			t.Errorf("Check(%q) = nil, want an error", tmpl)
		}
	}
}
