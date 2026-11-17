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

// Model holds the palette state: the command list and the query with the rows
// it keeps and the selection among them.
//
// The query is a ui.Search, so the model owns no buffer, no match set and no
// selection of its own: the app types into Search.Input through the component's
// methods and the model filters for the value it holds the next time it reads
// the rows. The palette is a live filter, so it never commits a query.
type Model struct {
	commands []Command
	// Search is the query row: the draft, the matching command indexes in
	// display order and the selection among them.
	Search ui.Search
	// filtered is the query the rows were filtered for.
	filtered string
}

// New builds a model over an ordered command list. The list order is the
// tie-break order of the filter.
func New(commands []Command) *Model {
	m := &Model{commands: append([]Command(nil), commands...)}
	m.Search.SetMatches(Filter(m.commands, m.filtered))
	return m
}

// Query returns the query text.
func (m *Model) Query() string { return m.Search.Input.String() }

// Rows returns the filtered commands in display order: best match first, all
// commands in input order when the query is empty. A query typed since the last
// read is filtered here, so the rows always answer the query the model holds.
func (m *Model) Rows() []Command {
	m.refilter()
	matches := m.Search.Matches
	out := make([]Command, len(matches))
	for i, at := range matches {
		out[i] = m.commands[at]
	}
	return out
}

// SelectedIndex returns the position of the selection inside Rows, or 0 when
// no row exists.
func (m *Model) SelectedIndex() int {
	m.refilter()
	if m.Search.Count() == 0 {
		return 0
	}
	return m.Search.Cursor
}

// Selected returns the command under the selection. It reports false when the
// filter left no rows to pick from.
func (m *Model) Selected() (Command, bool) {
	m.refilter()
	at := m.Search.Match()
	if at < 0 {
		return Command{}, false
	}
	return m.commands[at], true
}

// Cycle moves the selection down for a positive step and up for a negative
// one, wrapping at the ends. It does nothing when no row exists.
func (m *Model) Cycle(d int) {
	m.refilter()
	n := m.Search.Count()
	if n == 0 {
		return
	}
	// One lap is no move, so the loop never runs more than a lap: a positive
	// step walks down, a negative one walks up, and both wrap.
	switch {
	case d > 0:
		for i := d % n; i > 0; i-- {
			m.Search.Next()
		}
	case d < 0:
		for i := -d % n; i > 0; i-- {
			m.Search.Prev()
		}
	}
}

// refilter runs the filter when the query changed and keeps the selection on
// the same position, clamped to the rows left. The rows the model was built
// with stand for the empty query, so a model nobody has typed into never
// filters.
func (m *Model) refilter() {
	q := m.Query()
	if q == m.filtered {
		return
	}
	m.filtered = q
	cur := m.Search.Cursor
	m.Search.SetMatches(Filter(m.commands, q))
	m.Search.SetCursor(cur)
}
