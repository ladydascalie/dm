package client

import (
	"sort"
	"strings"
)

// ScrollSearch finds one literal, case-insensitive substring in the text rows
// of a scrollback terminal. It holds no UI state and no client state.
type ScrollSearch struct {
	rows    []string
	query   string
	matches []int
	cur     int
}

// NewScrollSearch builds a search over rows for query. An empty query, or a
// query that matches nothing, reports no matches and a current index of -1.
func NewScrollSearch(rows []string, query string) *ScrollSearch {
	s := &ScrollSearch{rows: rows, cur: -1}
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
	s.query = query
	s.find()
}

// Query returns the string the model searches for.
func (s *ScrollSearch) Query() string { return s.query }

func (s *ScrollSearch) find() {
	s.matches = s.matches[:0]
	s.cur = -1
	if s.query == "" {
		return
	}
	needle := strings.ToLower(s.query)
	for i, row := range s.rows {
		if strings.Contains(strings.ToLower(row), needle) {
			s.matches = append(s.matches, i)
		}
	}
	if len(s.matches) > 0 {
		s.cur = 0
	}
}

// Matches returns the row indexes that hold a match, in row order. A row with
// several matches appears once.
func (s *ScrollSearch) Matches() []int {
	out := make([]int, len(s.matches))
	copy(out, s.matches)
	return out
}

// Count returns the number of matching rows.
func (s *ScrollSearch) Count() int { return len(s.matches) }

// Current returns the index of the current match in Matches, or -1 when there
// is no match.
func (s *ScrollSearch) Current() int { return s.cur }

// CurrentRow returns the row of the current match, or -1 when there is none.
func (s *ScrollSearch) CurrentRow() int {
	if s.cur < 0 || s.cur >= len(s.matches) {
		return -1
	}
	return s.matches[s.cur]
}

// HasMatch reports whether row holds a match.
func (s *ScrollSearch) HasMatch(row int) bool {
	i := sort.SearchInts(s.matches, row)
	return i < len(s.matches) && s.matches[i] == row
}

// Next moves to the next match and wraps past the last. It returns the new
// current index, or -1 when there is no match.
func (s *ScrollSearch) Next() int {
	n := len(s.matches)
	if n == 0 {
		s.cur = -1
		return -1
	}
	if s.cur < 0 || s.cur >= n {
		s.cur = 0
	} else {
		s.cur = (s.cur + 1) % n
	}
	return s.cur
}

// Prev moves to the previous match and wraps past the first. It returns the
// new current index, or -1 when there is no match.
func (s *ScrollSearch) Prev() int {
	n := len(s.matches)
	if n == 0 {
		s.cur = -1
		return -1
	}
	if s.cur < 0 || s.cur >= n {
		s.cur = n - 1
	} else {
		s.cur = (s.cur - 1 + n) % n
	}
	return s.cur
}

// SelectAtOrAbove makes the nearest match at or above row current, meaning
// the largest matching row index not past row. With no such match it selects
// the first match. It returns the row of the new current match, or -1 when
// there is no match.
func (s *ScrollSearch) SelectAtOrAbove(row int) int {
	if len(s.matches) == 0 {
		s.cur = -1
		return -1
	}
	i := sort.SearchInts(s.matches, row+1) - 1
	if i < 0 {
		i = 0
	}
	s.cur = i
	return s.matches[i]
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
