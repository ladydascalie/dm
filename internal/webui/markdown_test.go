package webui

import (
	"strings"
	"testing"
)

// TestMarkdownRendersGFM proves the prose an agent actually writes survives the
// trip: a heading, a table, a fence and a list all reach the page as markup
// rather than as punctuation.
func TestMarkdownRendersGFM(t *testing.T) {
	t.Parallel()
	html := markdownHTML("## What changed\n\n| state | chrome |\n|---|---|\n| before | 42% |\n\n```go\nx := 1\n```\n\n- one\n- two\n")
	for _, want := range []string{
		"<h2>What changed</h2>",
		"<table>",
		"<td>42%</td>",
		`<pre><code class="language-go">`,
		"<li>one</li>",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("%q missing from\n%s", want, html)
		}
	}
}

// TestMarkdownRefusesRawHTML proves a session cannot hand the page a script.
// The reader's phone must not run what an agent read somewhere else.
func TestMarkdownRefusesRawHTML(t *testing.T) {
	t.Parallel()
	html := markdownHTML("before <script>alert(1)</script> after\n")
	if strings.Contains(html, "<script") {
		t.Fatalf("raw HTML reached the page:\n%s", html)
	}
	if !strings.Contains(html, "before") || !strings.Contains(html, "after") {
		t.Fatalf("the prose around it was lost:\n%s", html)
	}
}

// TestMarkdownRefusesScriptingLinks proves a link keeps its text and loses a
// target the browser would run.
func TestMarkdownRefusesScriptingLinks(t *testing.T) {
	t.Parallel()
	html := markdownHTML("[tap me](javascript:alert(1)) and [fine](https://example.com/x)\n")
	if strings.Contains(html, "javascript:") {
		t.Fatalf("a scripting target reached the page:\n%s", html)
	}
	if !strings.Contains(html, "tap me") {
		t.Fatalf("the link text was lost:\n%s", html)
	}
	if !strings.Contains(html, `href="https://example.com/x"`) {
		t.Fatalf("an ordinary link lost its target:\n%s", html)
	}
}

// TestMarkdownEmpty proves a blank step asks for no markup, so the page keeps
// its plain copy instead of painting an empty block.
func TestMarkdownEmpty(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "   \n\t"} {
		if got := markdownHTML(in); got != "" {
			t.Fatalf("markdownHTML(%q) = %q, want empty", in, got)
		}
	}
}

// TestSafeTarget pins which destinations a thumb may follow.
func TestSafeTarget(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"https://example.com":       true,
		"http://example.com":        true,
		"mailto:b@example.com":      true,
		"#turn-2":                   true,
		"internal/webui/session.go": true, // a relative path
		"a/b:c":                     true, // a colon after a slash is punctuation
		"javascript:alert(1)":       false,
		"JAVASCRIPT:alert(1)":       false,
		"data:text/html,<script>":   false,
		"file:///etc/passwd":        false,
		"":                          false,
	}
	for dest, want := range cases {
		if got := safeTarget(dest); got != want {
			t.Fatalf("safeTarget(%q) = %v, want %v", dest, got, want)
		}
	}
}
