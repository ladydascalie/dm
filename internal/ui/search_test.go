package ui

import (
	"strings"
	"testing"
)

// TestSearchSetMatches: a new set puts the cursor on its first match, an empty
// set has no cursor, and the old cursor never survives into the new set.
func TestSearchSetMatches(t *testing.T) {
	cases := []struct {
		name    string
		matches []int
		cursor  int
		want    []int
		wantCur int
		wantRow int
	}{
		{"nil set", nil, 0, nil, -1, -1},
		{"empty set", []int{}, 0, nil, -1, -1},
		{"one row", []int{3}, 0, []int{3}, 0, 3},
		{"several rows, in row order", []int{1, 3, 4}, 0, []int{1, 3, 4}, 0, 1},
		{"the old cursor is dropped", []int{5, 6}, 1, []int{5, 6}, 0, 5},
		{"an old cursor past the set is dropped", []int{5, 6}, 9, []int{5, 6}, 0, 5},
		{"a negative old cursor is dropped", []int{5, 6}, -4, []int{5, 6}, 0, 5},
	}
	for _, c := range cases {
		s := Search{Matches: []int{0, 1, 2}, Cursor: c.cursor}
		s.SetMatches(c.matches)
		if got := s.Count(); got != len(c.want) {
			t.Fatalf("%s: count = %d, want %d", c.name, got, len(c.want))
		}
		if len(c.want) == 0 {
			if s.Matches != nil {
				t.Fatalf("%s: matches = %v, want nil", c.name, s.Matches)
			}
		} else {
			for i, want := range c.want {
				if s.Matches[i] != want {
					t.Fatalf("%s: matches = %v, want %v", c.name, s.Matches, c.want)
				}
			}
		}
		if s.Cursor != c.wantCur || s.Match() != c.wantRow {
			t.Fatalf("%s: cursor = %d match = %d, want %d and %d", c.name, s.Cursor, s.Match(), c.wantCur, c.wantRow)
		}
	}
}

// TestSearchSetMatchesCopies guards the caller's slice: the set is its own, so a
// caller that rewrites the buffer it passed cannot change the search under it.
func TestSearchSetMatchesCopies(t *testing.T) {
	passed := []int{2, 4, 6}
	s := Search{}
	s.SetMatches(passed)
	passed[0] = 99
	if got := s.Match(); got != 2 {
		t.Fatalf("match = %d, want 2 after the caller rewrote its slice", got)
	}
	if got := s.Matches[2]; got != 6 {
		t.Fatalf("matches = %v, want the set as it was passed", s.Matches)
	}
}

// TestSearchRefilter: the set is built from rows 0..n-1 in row order, the
// cursor lands on the first match, a refilter that shrinks the set under the
// cursor drops the cursor onto the new first match, and the query read is the
// draft in the atom.
func TestSearchRefilter(t *testing.T) {
	rows := []string{"main", "send", "spawn", "split", "send later"}
	match := func(query string) func(int) bool {
		return func(i int) bool {
			return strings.Contains(rows[i], query)
		}
	}
	cases := []struct {
		name    string
		query   string
		n       int
		match   func(int) bool
		want    []int
		wantCur int
	}{
		{"no rows", "send", 0, match("send"), nil, -1},
		{"a query that matches nothing", "zzz", len(rows), match("zzz"), nil, -1},
		{"one match", "spawn", len(rows), match("spawn"), []int{2}, 0},
		{"every row", "", len(rows), func(int) bool { return true }, []int{0, 1, 2, 3, 4}, 0},
		{"a subset in row order", "s", len(rows), match("s"), []int{1, 2, 3, 4}, 0},
		{"n shorter than the rows", "s", 3, match("s"), []int{1, 2}, 0},
		{"a negative n refilters nothing", "s", -1, match("s"), nil, -1},
	}
	for _, c := range cases {
		s := Search{}
		s.Input.SetValue(c.query)
		s.Cursor = 3
		s.Refilter(c.n, c.match)
		if got := s.Count(); got != len(c.want) {
			t.Fatalf("%s: count = %d, want %d", c.name, got, len(c.want))
		}
		for i, want := range c.want {
			if s.Matches[i] != want {
				t.Fatalf("%s: matches = %v, want %v", c.name, s.Matches, c.want)
			}
		}
		if s.Cursor != c.wantCur {
			t.Fatalf("%s: cursor = %d, want %d", c.name, s.Cursor, c.wantCur)
		}
	}

	// The set shrinks under the cursor: five rows match, the cursor is on the
	// last one, and the refilter lands it on the first match of the two left.
	s := Search{}
	s.Refilter(len(rows), func(int) bool { return true })
	s.SetCursor(4)
	if s.Cursor != 4 {
		t.Fatalf("cursor = %d, want 4 before the refilter", s.Cursor)
	}
	s.Input.SetValue("sen")
	s.Refilter(len(rows), match("sen"))
	if s.Cursor != 0 || s.Count() != 2 || s.Match() != 1 {
		t.Fatalf("after the shrink: cursor = %d count = %d match = %d, want 0, 2 and 1",
			s.Cursor, s.Count(), s.Match())
	}
}

// TestSearchRefilterWideRune: the query is runes, not bytes, so a two cell rune
// is one rune in the atom and matches the row as it is typed.
func TestSearchRefilterWideRune(t *testing.T) {
	rows := []string{"名前", "名", "名前 then more", "other"}
	s := Search{}
	s.Input.SetValue("名前")
	if s.Input.Caret != 2 || s.Input.String() != "名前" {
		t.Fatalf("the atom holds %q with caret %d, want %q with caret 2", s.Input.String(), s.Input.Caret, "名前")
	}
	s.Refilter(len(rows), func(i int) bool {
		return strings.Contains(rows[i], s.Input.String())
	})
	if s.Count() != 2 || s.Matches[0] != 0 || s.Matches[1] != 2 {
		t.Fatalf("matches = %v, want [0 2]", s.Matches)
	}
}

// TestSearchCommitAndCancel: commit keeps the draft as the accepted query, and
// cancel drops a later draft back to it. The set and its cursor are the
// caller's and survive both, so an accept that changes nothing visible leaves
// the highlight where it was.
func TestSearchCommitAndCancel(t *testing.T) {
	s := Search{}
	s.Input.SetValue("spl")
	s.Commit()
	if s.Accepted != "spl" {
		t.Fatalf("accepted = %q, want spl", s.Accepted)
	}
	s.Refilter(3, func(i int) bool { return i != 1 })
	s.SetCursor(1)
	s.Input.SetValue("split typo")
	s.Cancel()
	if got := s.Input.String(); got != "spl" {
		t.Fatalf("input = %q after cancel, want spl", got)
	}
	if s.Input.Caret != 3 {
		t.Fatalf("caret = %d after cancel, want 3", s.Input.Caret)
	}
	if s.Count() != 2 || s.Cursor != 1 || s.Match() != 2 {
		t.Fatalf("cancel touched the set: count = %d cursor = %d match = %d", s.Count(), s.Cursor, s.Match())
	}

	// A commit is the draft as it stands: an edit after it is not accepted
	// until the next commit.
	s.Input.SetValue("spawn")
	s.Commit()
	if s.Accepted != "spawn" {
		t.Fatalf("accepted = %q, want spawn", s.Accepted)
	}
	s.Input.InsertRune('s')
	if got := s.Input.String(); got != "spawns" {
		t.Fatalf("input = %q, want spawns", got)
	}
	if s.Accepted != "spawn" {
		t.Fatalf("accepted = %q, want the committed query", s.Accepted)
	}
}

// TestSearchCancelWithNothingCommitted: a search nobody committed cancels to
// the empty query.
func TestSearchCancelWithNothingCommitted(t *testing.T) {
	s := Search{}
	s.Input.SetValue("draft")
	s.Input.Home()
	s.Cancel()
	if got := s.Input.String(); got != "" || s.Input.Caret != 0 {
		t.Fatalf("input = %q caret = %d after cancel, want empty at 0", got, s.Input.Caret)
	}
}

// TestSearchClear: clear drops the whole search, and a cleared search counts no
// rows and has no cursor.
func TestSearchClear(t *testing.T) {
	s := Search{}
	s.Input.SetValue("send")
	s.Commit()
	s.Refilter(3, func(int) bool { return true })
	s.SetCursor(2)
	s.Clear()
	if got := s.Input.String(); got != "" || s.Input.Caret != 0 {
		t.Fatalf("input = %q caret = %d after clear, want empty at 0", got, s.Input.Caret)
	}
	if s.Accepted != "" {
		t.Fatalf("accepted = %q after clear, want empty", s.Accepted)
	}
	if s.Matches != nil {
		t.Fatalf("matches = %v after clear, want nil", s.Matches)
	}
	if s.Cursor != -1 || s.Match() != -1 || s.Count() != 0 {
		t.Fatalf("after clear: cursor = %d match = %d count = %d", s.Cursor, s.Match(), s.Count())
	}
}

// TestSearchSetCursor: a cursor inside the set is kept, a cursor below it is the
// first match, a cursor past it is the last, and an empty set has none.
func TestSearchSetCursor(t *testing.T) {
	cases := []struct {
		name    string
		set     []int
		i       int
		want    int
		wantRow int
	}{
		{"inside the set", []int{7, 8, 9}, 1, 1, 8},
		{"below the set", []int{7, 8, 9}, -3, 0, 7},
		{"past the set", []int{7, 8, 9}, 9, 2, 9},
		{"the last match", []int{7, 8, 9}, 2, 2, 9},
		{"an empty set", nil, 0, -1, -1},
		{"an empty set with a positive cursor", nil, 4, -1, -1},
	}
	for _, c := range cases {
		s := Search{}
		s.SetMatches(c.set)
		s.SetCursor(c.i)
		if s.Cursor != c.want || s.Match() != c.wantRow {
			t.Fatalf("%s: cursor = %d match = %d, want %d and %d", c.name, s.Cursor, s.Match(), c.want, c.wantRow)
		}
	}
}

// TestSearchNextPrev: next and prev move one match and wrap both ways, one match
// wraps to itself, and an empty set has no cursor to move.
func TestSearchNextPrev(t *testing.T) {
	steps := []struct {
		name   string
		set    []int
		cursor int
		next   bool
		want   int
	}{
		{"next down the set", []int{1, 2, 3}, 0, true, 1},
		{"next wraps to the first", []int{1, 2, 3}, 2, true, 0},
		{"prev up the set", []int{1, 2, 3}, 1, false, 0},
		{"prev wraps to the last", []int{1, 2, 3}, 0, false, 2},
		{"a single match wraps to itself down", []int{4}, 0, true, 0},
		{"a single match wraps to itself up", []int{4}, 0, false, 0},
		{"an empty set stays empty down", nil, -1, true, -1},
		{"an empty set stays empty up", nil, -1, false, -1},
		{"a stale cursor starts at the first down", []int{1, 2, 3}, 9, true, 0},
		{"a stale cursor starts at the first up", []int{1, 2, 3}, 9, false, 0},
	}
	for _, c := range steps {
		s := Search{Matches: append([]int(nil), c.set...), Cursor: c.cursor}
		if c.next {
			s.Next()
		} else {
			s.Prev()
		}
		if s.Cursor != c.want {
			t.Fatalf("%s: cursor = %d, want %d", c.name, s.Cursor, c.want)
		}
	}
}

// TestSearchMatchAndCount: the match is the row under the cursor, never a row
// number of its own, and a cursor outside the set answers -1.
func TestSearchMatchAndCount(t *testing.T) {
	cases := []struct {
		name    string
		set     []int
		cursor  int
		want    int
		wantCnt int
	}{
		{"an empty set", nil, -1, -1, 0},
		{"no cursor on a set", []int{2, 5}, -1, -1, 2},
		{"a cursor past the set", []int{2, 5}, 7, -1, 2},
		{"the first match", []int{2, 5}, 0, 2, 2},
		{"the last match", []int{2, 5}, 1, 5, 2},
	}
	for _, c := range cases {
		s := Search{Matches: c.set, Cursor: c.cursor}
		if got := s.Match(); got != c.want {
			t.Fatalf("%s: match = %d, want %d", c.name, got, c.want)
		}
		if got := s.Count(); got != c.wantCnt {
			t.Fatalf("%s: count = %d, want %d", c.name, got, c.wantCnt)
		}
	}
}
