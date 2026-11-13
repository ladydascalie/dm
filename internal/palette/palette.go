// Package palette is the command palette's model, independent of the client:
// the command list, the fuzzy filter and the query buffer with its selection.
// The client drives and draws it.
package palette

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

// Model holds the palette state: the command list, the query buffer, the
// filtered rows and the selection.
type Model struct {
	commands []Command
	rows     []int
	query    []rune
	sel      int
}

// New builds a model over an ordered command list. The list order is the
// tie-break order of the filter.
func New(commands []Command) *Model {
	m := &Model{commands: append([]Command(nil), commands...)}
	m.refilter()
	return m
}

// Query returns the query buffer as a string.
func (m *Model) Query() string { return string(m.query) }

// Rows returns the filtered commands in display order: best match first, all
// commands in input order when the query is empty.
func (m *Model) Rows() []Command {
	out := make([]Command, len(m.rows))
	for i, at := range m.rows {
		out[i] = m.commands[at]
	}
	return out
}

// SelectedIndex returns the position of the selection inside Rows, or 0 when
// no row exists.
func (m *Model) SelectedIndex() int {
	if len(m.rows) == 0 {
		return 0
	}
	return m.sel
}

// Selected returns the command under the selection. It reports false when the
// filter left no rows to pick from.
func (m *Model) Selected() (Command, bool) {
	if len(m.rows) == 0 {
		return Command{}, false
	}
	return m.commands[m.rows[m.sel]], true
}

// AppendRune types one rune into the query and refilters.
func (m *Model) AppendRune(r rune) {
	m.query = append(m.query, r)
	m.refilter()
}

// Backspace drops the last rune from the query and refilters. On an empty
// query it does nothing.
func (m *Model) Backspace() {
	if len(m.query) == 0 {
		return
	}
	m.query = m.query[:len(m.query)-1]
	m.refilter()
}

// Cycle moves the selection down for a positive step and up for a negative
// one, wrapping at the ends. It does nothing when no row exists.
func (m *Model) Cycle(d int) {
	n := len(m.rows)
	if n == 0 {
		return
	}
	m.sel = ((m.sel+d)%n + n) % n
}

// refilter reruns the filter and clamps the selection to the rows left.
func (m *Model) refilter() {
	m.rows = Filter(m.commands, m.Query())
	if m.sel >= len(m.rows) {
		m.sel = max(len(m.rows)-1, 0)
	}
}
