package clidoc

import (
	"fmt"
	"strings"
)

// componentField is one row of a component's props or state table.
type componentField struct {
	name, typ, def, desc string
}

// componentKey is one row of a component's key table: the action a key handler
// names, the method it calls, and what the call does.
type componentKey struct {
	action, method, outcome string
}

// component is one documented UI component. The page is the user-facing page
// for the component; the spec's contract page stays the design record. The
// props and state rows are checked against the value's exported fields by
// TestComponentPageNamesEveryField, so a field that lands without a row fails
// the test.
type component struct {
	id, pkg, name, summary, usage string
	props, state                  []componentField
	keys                          []componentKey
	focus, compose                string
}

// components is the page's component list, in the order the section paints it.
var components = []component{{
	id:   "input",
	pkg:  "internal/ui",
	name: "Input",
	summary: "Input is one line of editable text: the runes and the caret. " +
		"It owns the value and the caret, and nothing else. It does not own a " +
		"prompt row, a border, a mask or a selection; those arrive with a " +
		"caller that needs them.",
	usage: `var in ui.Input
in.SetValue("release")     // the caret lands after the last rune
in.InsertRune('!')         // the key of the same name

ui.DrawInput(buf, th, ui.InputProps{
    Rect:   row,
    Prompt: " name: ",
    Ground: th.Palette.RailBG,
    Dim:    th.Palette.RailDim,
    Text:   th.Palette.RailText,
    Caret:  th.Palette.BarAccent,
    Active: true,
}, in)`,
	props: []componentField{
		{"Rect", "layout.Rect", "zero", "The one row the input owns. The ground fills every cell."},
		{"Prompt", "string", `""`, "The fixed label before the text, painted in Dim and clipped rather than painted over the caret."},
		{"Trailing", "string", `""`, "Text on the row's right edge. Its own trailing spaces are the gutter, and the row keeps one cell of ground before it."},
		{"Ground", "theme.RGB", "", "The row's ground, filled to Rect."},
		{"Dim", "theme.RGB", "", "The prompt and the trailing text."},
		{"Text", "theme.RGB", "", "The value."},
		{"Caret", "theme.RGB", "", "The caret cell, painted only when Active."},
		{"Active", "bool", "false", "Whether the caret cell is painted. Focus is this prop and nothing else."},
	},
	state: []componentField{
		{"Value", "[]rune", "empty", "The text."},
		{"Caret", "int", "0", "The rune index the caret sits before, 0 to len(Value). Every method keeps it in that range."},
	},
	keys: []componentKey{
		{"replace the text", "SetValue", "The value becomes the string and the caret lands after the last rune."},
		{"type a rune", "InsertRune", "The rune lands at the caret and the caret moves after it."},
		{"paste", "InsertString", "The string lands at the caret and the caret moves after it."},
		{"backspace", "Backspace", "The rune before the caret goes, or nothing at 0."},
		{"delete", "Delete", "The rune at the caret goes, or nothing at len(Value)."},
		{"move left", "Left", "The caret moves one rune left, or stays at 0."},
		{"move right", "Right", "The caret moves one rune right, or stays at len(Value)."},
		{"move home", "Home", "The caret moves to 0."},
		{"move end", "End", "The caret moves to len(Value)."},
		{"kill the tail", "Kill", "The value is cut at the caret. The caret does not move."},
		{"read the text", "String", "Reports the value as a string."},
		{"ask if empty", "Empty", "Reports whether the value has no runes."},
	},
	focus: "The app sets Active. The input never reads the keymap and never sees a key byte. " +
		"The mode's key handler is the only dispatcher, so a rebind moves the key and the hint together.",
	compose: "A row. A box's content func or a float's Paint calls it inside the rect the parent " +
		"hands it. It never paints a border.",
}}

// componentDefault renders a default cell: a code run when there is one, and an
// empty cell otherwise.
func componentDefault(def string) string {
	if def == "" {
		return ""
	}
	return "<code>" + htmlEscape(def) + "</code>"
}

// htmlComponents renders the COMPONENTS section: one heading and a props,
// state and key table per component.
func htmlComponents(ids *idGen) string {
	var b strings.Builder
	b.WriteString(`<section class="sec" id="sec-components">`)
	b.WriteString(`<h2 id="components">Components</h2>`)
	b.WriteString(`<p>The overlay chrome is built from a small vocabulary. A component is a value the app owns, a props struct for one frame, a pure Draw and one method per key. It never reads a keymap, never sees a key byte and never holds a buffer between frames. The page for one is its contract: props, state, keys, focus and composition.</p>`)
	for _, c := range components {
		fmt.Fprintf(&b, `<h3 id="%s">%s</h3>`, htmlEscape(ids.id("comp", c.name)), htmlEscape(c.name))
		fmt.Fprintf(&b, `<p>%s</p>`, htmlEscape(c.summary))
		fmt.Fprintf(&b, `<p class="provenance">Source: <code>%s</code>.</p>`, htmlEscape(c.pkg))
		fmt.Fprintf(&b, "<pre><code>%s</code></pre>", htmlEscape(c.usage))

		b.WriteString(`<h4>Props</h4>`)
		b.WriteString(`<table><thead><tr><th>Field</th><th>Type</th><th>Default</th><th>Meaning</th></tr></thead><tbody>`)
		for _, f := range c.props {
			fmt.Fprintf(&b, `<tr data-row><td><code>%s</code></td><td><code>%s</code></td><td>%s</td><td>%s</td></tr>`,
				htmlEscape(f.name), htmlEscape(f.typ), componentDefault(f.def), htmlEscape(f.desc))
		}
		b.WriteString(`</tbody></table>`)

		b.WriteString(`<h4>State</h4>`)
		b.WriteString(`<table><thead><tr><th>Field</th><th>Type</th><th>Default</th><th>Meaning</th></tr></thead><tbody>`)
		for _, f := range c.state {
			fmt.Fprintf(&b, `<tr data-row><td><code>%s</code></td><td><code>%s</code></td><td>%s</td><td>%s</td></tr>`,
				htmlEscape(f.name), htmlEscape(f.typ), componentDefault(f.def), htmlEscape(f.desc))
		}
		b.WriteString(`</tbody></table>`)

		b.WriteString(`<h4>Keys</h4>`)
		b.WriteString(`<table><thead><tr><th>Action</th><th>Method</th><th>Outcome</th></tr></thead><tbody>`)
		for _, k := range c.keys {
			fmt.Fprintf(&b, `<tr data-row><td>%s</td><td><code>%s</code></td><td>%s</td></tr>`,
				htmlEscape(k.action), htmlEscape(k.method), htmlEscape(k.outcome))
		}
		b.WriteString(`</tbody></table>`)

		fmt.Fprintf(&b, `<p><strong>Focus.</strong> %s</p>`, htmlEscape(c.focus))
		fmt.Fprintf(&b, `<p><strong>Composition.</strong> %s</p>`, htmlEscape(c.compose))
	}
	b.WriteString(`</section>`)
	return b.String()
}
