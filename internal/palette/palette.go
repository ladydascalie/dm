// Package palette is the command palette's model, independent of the client:
// the command list, the fuzzy filter and the query row with its selection. The
// client drives and draws it.
package palette

import "dm/internal/ui"

// Command is one entry the palette offers. Name is the display label, Hint a
// one-line description and Action the action id the client dispatches on.
type Command struct {
	Name   string
	Hint   string
	Action string
	// Key is the key label that runs the command, the label the help view
	// prints. Empty when the command has no key.
	Key string
}

// Model holds the palette state: the command list, the query row, the filtered
// rows and the selection.
//
// The query row is a ui.Input, so the model owns no buffer of its own: the app
// types into Input through the component's methods and the model filters for
// the value it holds the next time it reads the rows.
type Model struct {
	commands []Command
	rows     []int
	sel      int
	// Input is the query row.
	Input ui.Input
	// filtered is the query the rows were filtered for.
	filtered string
}

// New builds a model over an ordered command list. The list order is the
// tie-break order of the filter.
func New(commands []Command) *Model {
	m := &Model{commands: append([]Command(nil), commands...)}
	m.rows = Filter(m.commands, m.filtered)
	return m
}

// Query returns the query text.
func (m *Model) Query() string { return m.Input.String() }

// Rows returns the filtered commands in display order: best match first, all
// commands in input order when the query is empty. A query typed since the last
// read is filtered here, so the rows always answer the query the model holds.
func (m *Model) Rows() []Command {
	m.refilter()
	out := make([]Command, len(m.rows))
	for i, at := range m.rows {
		out[i] = m.commands[at]
	}
	return out
}

// SelectedIndex returns the position of the selection inside Rows, or 0 when
// no row exists.
func (m *Model) SelectedIndex() int {
	m.refilter()
	if len(m.rows) == 0 {
		return 0
	}
	return m.sel
}

// Selected returns the command under the selection. It reports false when the
// filter left no rows to pick from.
func (m *Model) Selected() (Command, bool) {
	m.refilter()
	if len(m.rows) == 0 {
		return Command{}, false
	}
	return m.commands[m.rows[m.sel]], true
}

// Cycle moves the selection down for a positive step and up for a negative
// one, wrapping at the ends. It does nothing when no row exists.
func (m *Model) Cycle(d int) {
	m.refilter()
	n := len(m.rows)
	if n == 0 {
		return
	}
	m.sel = ((m.sel+d)%n + n) % n
}

// refilter runs the filter when the query changed and clamps the selection to
// the rows left. The rows the model was built with stand for the empty query,
// so a model nobody has typed into never filters.
func (m *Model) refilter() {
	if q := m.Query(); q != m.filtered {
		m.filtered = q
		m.rows = Filter(m.commands, q)
	}
	if m.sel >= len(m.rows) {
		m.sel = max(len(m.rows)-1, 0)
	}
}
