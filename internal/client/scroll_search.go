package client

import (
	"sort"
	"strings"

	"dm/internal/ui"
	"dm/internal/vt"
)

// ScrollSearch finds one literal, case-insensitive substring in the text rows
// of a scrollback terminal. It holds no UI state and no client state: the rows
// are the tall terminal's rows and the Search holds the query, the matching row
// indexes and the current match.
type ScrollSearch struct {
	rows   []string
	Search ui.Search
}

// NewScrollSearch builds a search over rows for query. An empty query, or a
// query that matches nothing, reports no matches and a current index of -1.
func NewScrollSearch(rows []string, query string) *ScrollSearch {
	s := &ScrollSearch{rows: rows}
	s.SetQuery(query)
	return s
}

// SetRows replaces the rows and keeps the query. The current match resets to
// the first match, or -1 when the rows hold none.
func (s *ScrollSearch) SetRows(rows []string) {
	s.rows = rows
	s.find()
}

// SetQuery replaces the query and resets the current match.
func (s *ScrollSearch) SetQuery(query string) {
	s.Search.Input.SetValue(query)
	s.find()
}

// Query returns the string the model searches for.
func (s *ScrollSearch) Query() string { return s.Search.Input.String() }

// find rebuilds the match set from the rows and the query. The test is the same
// literal, case-insensitive substring for every row, and the set is in row
// order.
func (s *ScrollSearch) find() {
	needle := strings.ToLower(s.Search.Input.String())
	if needle == "" {
		// An empty query matches nothing and leaves no current match.
		s.Search.SetMatches(nil)
		return
	}
	s.Search.Refilter(len(s.rows), func(i int) bool {
		return strings.Contains(strings.ToLower(s.rows[i]), needle)
	})
}

// Matches returns the row indexes that hold a match, in row order. A row with
// several matches appears once.
func (s *ScrollSearch) Matches() []int {
	out := make([]int, len(s.Search.Matches))
	copy(out, s.Search.Matches)
	return out
}

// Count returns the number of matching rows.
func (s *ScrollSearch) Count() int { return s.Search.Count() }

// Current returns the index of the current match in Matches, or -1 when there
// is no match.
func (s *ScrollSearch) Current() int { return s.Search.Cursor }

// CurrentRow returns the row of the current match, or -1 when there is none.
func (s *ScrollSearch) CurrentRow() int { return s.Search.Match() }

// HasMatch reports whether row holds a match.
func (s *ScrollSearch) HasMatch(row int) bool {
	i := sort.SearchInts(s.Search.Matches, row)
	return i < len(s.Search.Matches) && s.Search.Matches[i] == row
}

// Next moves to the next match and wraps past the last. It returns the new
// current index, or -1 when there is no match.
func (s *ScrollSearch) Next() int {
	s.Search.Next()
	return s.Search.Cursor
}

// Prev moves to the previous match and wraps past the first. It returns the
// new current index, or -1 when there is no match.
func (s *ScrollSearch) Prev() int {
	s.Search.Prev()
	return s.Search.Cursor
}

// SelectAtOrAbove makes the nearest match at or above row current, meaning
// the largest matching row index not past row. With no such match it selects
// the first match. It returns the row of the new current match, or -1 when
// there is no match.
func (s *ScrollSearch) SelectAtOrAbove(row int) int {
	if s.Search.Count() == 0 {
		s.Search.SetCursor(-1)
		return -1
	}
	i := sort.SearchInts(s.Search.Matches, row+1) - 1
	if i < 0 {
		i = 0
	}
	s.Search.SetCursor(i)
	return s.Search.Match()
}

// OffsetFor returns the scroll offset that puts row at the top of a band of
// bandH rows, clamped to 0..maxOff. A scroll offset counts rows up from the
// bottom of the rows.
func (s *ScrollSearch) OffsetFor(row, bandH, maxOff int) int {
	if maxOff < 0 {
		maxOff = 0
	}
	off := len(s.rows) - bandH - row
	if off < 0 {
		off = 0
	}
	if off > maxOff {
		off = maxOff
	}
	return off
}

// scrollSearch is the client's scrollback search. Search is the query row: the
// draft while the prompt is open, and the query last committed. The model is
// the accepted query over the tall terminal's rows, nil while no query is
// accepted.
type scrollSearch struct {
	Search ui.Search
	model  *ScrollSearch
}

// termRows reads a tall terminal's rows as text, one string per row, trailing
// blanks dropped, so a search looks at the same rows the pane paints, live
// rows included.
func termRows(term *vt.Term) []string {
	if term == nil {
		return nil
	}
	_, rows := term.Grid().Size()
	out := make([]string, rows)
	for y := range out {
		out[y] = strings.TrimRight(term.Grid().Text(y), " ")
	}
	return out
}
