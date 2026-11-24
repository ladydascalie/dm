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

// component is one documented UI unit. The page is the user-facing page for
// the unit; the spec's contract page stays the design record. The props and
// state rows are checked against the value's exported fields by
// TestComponentPageNamesEveryField, so a field that lands without a row fails
// the test. Level is the unit's place in the hierarchy: an atom stands alone,
// a molecule composes atoms and still owns no draw.
type component struct {
	id, pkg, name, level, summary, usage string
	props, state                         []componentField
	keys                                 []componentKey
	focus, compose                       string
	draw                                 string
}

// components is the page's unit list, in the order the section paints it.
var components = []component{{
	id:    "input",
	pkg:   "internal/ui",
	name:  "Input",
	level: "atom",
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
		{"delete a word", "DeleteWord", "The word before the caret goes, the spaces in front of it included, or nothing at 0."},
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
}, {
	id:    "search",
	pkg:   "internal/ui",
	name:  "Search",
	level: "molecule",
	summary: "Search is one query over a list of rows: the draft query, the accepted query, " +
		"the match set and the cursor. It composes the Input atom and owns no rows and no " +
		"matcher; the caller supplies both, and the view draws.",
	usage: `var s ui.Search
s.Input.SetValue("auth")
s.Refilter(len(rows), func(i int) bool {
    return strings.Contains(rows[i], s.Input.String())
})
s.Next()`,
	state: []componentField{
		{"Input", "ui.Input", "empty", "The draft query. The atom does the editing; this is the same value the rename prompt edits."},
		{"Accepted", "string", `""`, "The committed query. Commit stores the draft here; Cancel restores it."},
		{"Matches", "[]int", "nil", "The source row indices that match, in row order."},
		{"Cursor", "int", "-1", "The index into Matches, or -1 when nothing matches."},
	},
	keys: []componentKey{
		{"replace the matches", "SetMatches", "The set is copied and the cursor clamps to 0, or -1 when the set is empty."},
		{"refilter", "Refilter", "The predicate runs over rows 0..n-1, the query is Input.String(), and the set is replaced."},
		{"commit", "Commit", "Accepted becomes Input.String(). The live consumers never call it."},
		{"cancel", "Cancel", "Input returns to Accepted."},
		{"clear", "Clear", "The query, the accepted query and the matches go; the cursor is -1."},
		{"move the cursor", "SetCursor", "The cursor clamps into the set."},
		{"next match", "Next", "The cursor moves one match, wrapping past the last."},
		{"previous match", "Prev", "The cursor moves one match back, wrapping past the first."},
		{"read the match", "Match", "The source row of the cursor, or -1."},
		{"count", "Count", "The number of matches."},
	},
	focus:   "None. The molecule draws nothing and holds no focus. The view owns the caret, the selection and the viewport.",
	compose: "The atom is ui.Input. The view supplies the predicate, draws the rows and owns the viewport.",
}, {
	level: "component",
	draw:  "Draw",
	id:    "box",
	name:  "Box",
	pkg:   "internal/panel",
	summary: "A box is one bordered panel: a title, a hint bar, a focus style " +
		"and one content source. The border, the title and the hint row are the " +
		"panel's; the content is the caller's. The float is the same value with a " +
		"placement.",
	usage: `f := panel.FloatingPane{
    ID: "box", Rect: inner, Active: true,
    Title: " message box ",
    Hint: &hintbar.Bar{Hints: []hintbar.Hint{
        {Keys: []string{"esc"}, Label: "close"},
    }},
    Paint: func(buf *bytes.Buffer, r layout.Rect) {
        // write rows inside r
    },
}
f.Draw(buf, th)`,
	props: []componentField{
		{"ID", "string", `""`, "The float's id. The panel never reads it."},
		{"Rect", "layout.Rect", "zero", "The interior. The border is drawn one cell around it."},
		{"VT", "*vt.Term", "nil", "The terminal the float shows, when a terminal is the content."},
		{"Active", "bool", "false", "The focused border style."},
		{"Title", "string", `""`, "The top border label."},
		{"Hint", "*hintbar.Bar", "nil", "The bottom border's hint bar, fitted to the row on every frame. A nil bar paints no hint."},
		{"Paint", "func(*bytes.Buffer, layout.Rect)", "nil", "The content painter, one rect inside the border."},
	},
	focus: "The app sets Active. The float never reads a key or a keymap.",
	compose: "The content is a terminal or a paint func. The border, the title, the " +
		"hint row and the ground are drawn around it; the panel never centres itself.",
}, {
	level: "component",
	draw:  "Draw",
	id:    "messagelist",
	name:  "MessageList",
	pkg:   "internal/ui",
	summary: "MessageList is the list behind the message view: the entries, the topic filter, " +
		"the selection window, the read mark and the row painter. It composes Viewport and owns " +
		"no float, no border and no key byte: the caller sets the window height and paints the " +
		"chrome around the rows.",
	usage: `var list ui.MessageList
list.Set(entries)              // newest first, as the box serves them
list.SetFilter(box.NotifyPrefix)
list.View.H = inner.H          // the caller sets the window height

list.Draw(buf, th, ui.MessageListProps{
    Rect:     inner,
    Dim:      th.Palette.RailDim,
    Text:     th.Palette.RailText,
    Receipts: receipts,        // receipt count per entry seq
    Active:   true,
})`,
	props: []componentField{
		{"Rect", "layout.Rect", "zero", "The rows the list owns. A row is clipped to W and the window is H tall, placed from Rect's first row and column."},
		{"Now", "time.Time", "zero", "The clock the age column reads. Zero means time.Now()."},
		{"Dim", "theme.RGB", "", "An unselected row, and every row while the list is not Active."},
		{"Text", "theme.RGB", "", "The selected row while the list is Active."},
		{"Active", "bool", "false", "Whether the selected row keeps its full intensity. Focus is this prop and nothing else."},
		{"Receipts", "map[int]int", "nil", "Receipt count per entry seq, keyed by seq. A missing seq counts zero, and the whole column is dropped below 60 columns."},
	},
	state: []componentField{
		{"Entries", "[]box.Entry", "nil", "The log, newest first, as the box serves it."},
		{"Filter", "string", `""`, "A topic prefix. Empty shows every entry."},
		{"View", "ui.Viewport", "zero", "The selection and the window over the filtered rows. The caller sets H from the rect."},
		{"Read", "int", "0", "The highest seq the reader has seen. An entry is unread when its seq is above it."},
	},
	keys: []componentKey{
		{"replace the entries", "Set", "The log is replaced and the selection stays on its seq when the filter still shows it. A selection whose seq is gone keeps its row, clamped."},
		{"filter", "SetFilter", "The list narrows to a topic prefix, or widens on an empty one. The selection stays on its seq when the filter shows it, and keeps its row otherwise."},
		{"toggle the notify filter", "ToggleFilter", "An unfiltered list shows the notify branch, and any filtered list opens to every entry."},
		{"move the selection", "Move", "The selection moves n rows toward older entries, clamped at both ends, and the window follows it."},
		{"top", "Top", "The newest visible entry is selected and the window scrolls to the top."},
		{"bottom", "Bottom", "The oldest visible entry is selected and the window scrolls to the end."},
		{"read the selection", "Selected", "Reports the entry the selection sits on, or false on an empty list."},
		{"count", "Len", "The number of rows the filter shows."},
		{"count unread", "Unread", "The filtered entries past the read mark."},
		{"mark one read", "MarkRead", "The read mark rises to the seq, and never walks back."},
		{"mark the view read", "MarkReadAll", "Every entry the filter shows is marked read."},
		{"read a row back", "SeqAt", "The seq of the filtered row, or 0 when the row is not shown. A caller that holds a filtered index, View.Off for one, reads the seq here."},
		{"find a row", "Index", "The filtered row of the seq, or -1 when the filter hides it. A caller that holds the seq reads its row here after the entries change."},
	},
	focus: "The app sets Active. The list never reads the keymap and never sees a key byte. " +
		"The mode's key handler is the only dispatcher, so a rebind moves the key and the hint together.",
	compose: "It paints rows inside the rect a parent hands it and never paints a border. " +
		"A float's paint calls it, and the parent keeps the title, the hint and the empty note.",
}}

// componentDefault renders a default cell: a code run when there is one, and an
// empty cell otherwise.
func componentDefault(def string) string {
	if def == "" {
		return ""
	}
	return "<code>" + htmlEscape(def) + "</code>"
}

// htmlComponents renders the COMPONENTS section: one heading, a props table
// where the unit has props, a state table and a key table.
func htmlComponents(ids *idGen) string {
	var b strings.Builder
	b.WriteString(`<section class="sec" id="sec-components">`)
	b.WriteString(`<h2 id="components">Components</h2>`)
	b.WriteString(`<p>The overlay chrome is built from a small vocabulary. An atom is a value with one job. A molecule composes atoms and still owns no draw. A component adds a props struct for one frame and a Draw. Every level owns its state and answers methods; none reads a keymap or holds a buffer between frames. The page for one is its contract: props, state, keys, focus and composition.</p>`)
	for _, c := range components {
		fmt.Fprintf(&b, `<h3 id="%s">%s</h3>`, htmlEscape(ids.id("comp", c.name)), htmlEscape(c.name))
		fmt.Fprintf(&b, `<p>%s</p>`, htmlEscape(c.summary))
		fmt.Fprintf(&b, `<p class="provenance">Source: <code>%s</code> · %s.</p>`, htmlEscape(c.pkg), htmlEscape(c.level))
		fmt.Fprintf(&b, "<pre><code>%s</code></pre>", htmlEscape(c.usage))

		if len(c.props) > 0 {
			b.WriteString(`<h4>Props</h4>`)
			b.WriteString(`<table><thead><tr><th>Field</th><th>Type</th><th>Default</th><th>Meaning</th></tr></thead><tbody>`)
			for _, f := range c.props {
				fmt.Fprintf(&b, `<tr data-row><td><code>%s</code></td><td><code>%s</code></td><td>%s</td><td>%s</td></tr>`,
					htmlEscape(f.name), htmlEscape(f.typ), componentDefault(f.def), htmlEscape(f.desc))
			}
			b.WriteString(`</tbody></table>`)
		}

		if len(c.state) > 0 {
			b.WriteString(`<h4>State</h4>`)
			b.WriteString(`<table><thead><tr><th>Field</th><th>Type</th><th>Default</th><th>Meaning</th></tr></thead><tbody>`)
			for _, f := range c.state {
				fmt.Fprintf(&b, `<tr data-row><td><code>%s</code></td><td><code>%s</code></td><td>%s</td><td>%s</td></tr>`,
					htmlEscape(f.name), htmlEscape(f.typ), componentDefault(f.def), htmlEscape(f.desc))
			}
			b.WriteString(`</tbody></table>`)
		}

		if len(c.keys) > 0 {
			b.WriteString(`<h4>Keys</h4>`)
			b.WriteString(`<table><thead><tr><th>Action</th><th>Method</th><th>Outcome</th></tr></thead><tbody>`)
			for _, k := range c.keys {
				fmt.Fprintf(&b, `<tr data-row><td>%s</td><td><code>%s</code></td><td>%s</td></tr>`,
					htmlEscape(k.action), htmlEscape(k.method), htmlEscape(k.outcome))
			}
			b.WriteString(`</tbody></table>`)
		}

		if c.focus != "" {
			fmt.Fprintf(&b, `<p><strong>Focus.</strong> %s</p>`, htmlEscape(c.focus))
		}
		if c.compose != "" {
			fmt.Fprintf(&b, `<p><strong>Composition.</strong> %s</p>`, htmlEscape(c.compose))
		}
	}
	b.WriteString(`</section>`)
	return b.String()
}
