package ui

// Search is one query over rows the caller owns: the draft the operator types,
// the query last committed, and the matching rows with the cursor on one of
// them. It is the molecule over the Input atom: Input is the draft, and every
// key that edits the query goes through the atom's methods.
//
// The molecule holds the state and the keys, and nothing else. It holds no
// rows, no matcher, no viewport and no draw: the caller owns the rows and the
// predicate, and the caller owns the mode. A live filter refilters after every
// edit and never commits; a committed search refilters on accept and lets the
// draft drift until a cancel drops it. Refilter is the only call that touches
// the set, and it never runs on its own.
type Search struct {
	Input    Input  // Input is the draft query, the atom the molecule edits.
	Accepted string // Accepted is the query last committed, empty when none.
	Matches  []int  // Matches are the source row indices that match, in row order.
	Cursor   int    // Cursor is the index into Matches, -1 when there is none.
}

// SetMatches replaces the set with a copy of matches and puts the cursor on the
// first match: 0 when the set has a row, -1 when it is empty. The copy keeps
// the caller's slice its own, so a rebuild can reuse the buffer it passed. The
// old cursor is dropped, because a new set numbered from the old one would
// point at a row it no longer has.
func (s *Search) SetMatches(matches []int) {
	if len(matches) == 0 {
		s.Matches = nil
		s.Cursor = -1
		return
	}
	s.Matches = make([]int, len(matches))
	copy(s.Matches, matches)
	s.Cursor = 0
}

// Refilter rebuilds the set from rows 0..n-1 with match and hands it to
// SetMatches, so a query edit lands the cursor on the first match. A negative n
// refilters nothing. The query is the one Input.String() returns: the caller's
// predicate reads it through the Search, so the set and the draft the caller is
// about to paint cannot disagree.
func (s *Search) Refilter(n int, match func(i int) bool) {
	if n < 0 {
		n = 0
	}
	hits := make([]int, 0, n)
	for i := 0; i < n; i++ {
		if match(i) {
			hits = append(hits, i)
		}
	}
	s.SetMatches(hits)
}

// Commit accepts the draft: Accepted becomes the query in the input atom. The
// set and its cursor stay as they are, so the caller keeps the matches it
// accepted.
func (s *Search) Commit() {
	s.Accepted = s.Input.String()
}

// Cancel drops the draft and restores the committed query into the input atom.
// The caret lands after the last rune. The set is left to the caller: the
// caller's predicate knows the rows, so the caller refilters after the query
// changes, as it does after any other edit.
func (s *Search) Cancel() {
	s.Input.SetValue(s.Accepted)
}

// Clear empties the whole search: the draft, the accepted query, the set and
// the cursor. The caller drops the highlight when it sees Count() == 0.
func (s *Search) Clear() {
	s.Input.SetValue("")
	s.Accepted = ""
	s.Matches = nil
	s.Cursor = -1
}

// SetCursor puts the cursor at i, clamped to the set: below the first match it
// is the first, past the last match it is the last, and an empty set has no
// cursor. A caller that keeps a selection of its own across a refilter, the
// switcher's pick for example, sets the cursor here.
func (s *Search) SetCursor(i int) {
	n := len(s.Matches)
	switch {
	case n == 0:
		s.Cursor = -1
	case i < 0:
		s.Cursor = 0
	case i >= n:
		s.Cursor = n - 1
	default:
		s.Cursor = i
	}
}

// Next moves the cursor one match down and wraps from the last match to the
// first. An empty set has no cursor to move.
func (s *Search) Next() {
	s.step(1)
}

// Prev moves the cursor one match up and wraps from the first match to the
// last.
func (s *Search) Prev() {
	s.step(-1)
}

// step moves the cursor by d and wraps it into the set. A cursor outside the
// set starts at the first match, which is what a caller that wrote the field
// directly leaves behind.
func (s *Search) step(d int) {
	n := len(s.Matches)
	if n == 0 {
		s.Cursor = -1
		return
	}
	if s.Cursor < 0 || s.Cursor >= n {
		s.Cursor = 0
		return
	}
	s.Cursor = (s.Cursor + d + n) % n
}

// Match is the source row index under the cursor, or -1 when there is no
// match.
func (s *Search) Match() int {
	if s.Cursor < 0 || s.Cursor >= len(s.Matches) {
		return -1
	}
	return s.Matches[s.Cursor]
}

// Count is how many rows match.
func (s *Search) Count() int {
	return len(s.Matches)
}
