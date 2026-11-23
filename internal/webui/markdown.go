package webui

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
)

// markdown renders agent prose for the page. GFM is what an agent writes:
// tables, task lists, strikethrough and bare links all appear in a pi session,
// and CommonMark alone would print them as punctuation.
var markdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	// Raw HTML is left out, not passed through. A session holds text an agent
	// read from anywhere, and the page must not run it.
	goldmark.WithRendererOptions(html.WithXHTML()),
)

// markdownHTML renders one piece of agent prose as the HTML the page paints.
// Anything the renderer cannot use still reaches the reader as text: an empty
// answer means the caller keeps its plain copy.
func markdownHTML(src string) string {
	if strings.TrimSpace(src) == "" {
		return ""
	}
	source := []byte(src)
	doc := markdown.Parser().Parse(text.NewReader(source))
	guardLinks(doc)
	var buf bytes.Buffer
	if err := markdown.Renderer().Render(&buf, source, doc); err != nil {
		return ""
	}
	return buf.String()
}

// guardLinks drops a destination that could run code when a thumb taps it. The
// prose can carry a link an agent copied from a page it read, so only the
// schemes a reader expects are kept; anything else loses its target and stays
// as its own text.
func guardLinks(doc ast.Node) {
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := n.(type) {
		case *ast.Link:
			if !safeTarget(string(node.Destination)) {
				node.Destination = nil
			}
		case *ast.Image:
			if !safeTarget(string(node.Destination)) {
				node.Destination = nil
			}
		}
		return ast.WalkContinue, nil
	})
}

// safeTarget reports whether a link destination may reach the page. A bare
// path stays inside the reader's own session and a fragment stays in the page,
// so both pass; a scheme is kept only when it names the web or mail.
func safeTarget(dest string) bool {
	if dest == "" {
		return false
	}
	colon := strings.Index(dest, ":")
	if colon < 0 {
		return true
	}
	// A colon after a slash is punctuation inside a path, not a scheme.
	if i := strings.IndexAny(dest, "/?#"); i >= 0 && i < colon {
		return true
	}
	switch strings.ToLower(dest[:colon]) {
	case "http", "https", "mailto":
		return true
	}
	return false
}
