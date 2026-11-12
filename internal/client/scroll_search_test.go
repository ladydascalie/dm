package client

import (
	"reflect"
	"testing"
)

func TestScrollSearchEmptyQuery(t *testing.T) {
	s := NewScrollSearch([]string{"alpha", "beta"}, "")
	if got := s.Count(); got != 0 {
		t.Fatalf("Count with empty query = %d, want 0", got)
	}
	if got := s.Current(); got != -1 {
		t.Fatalf("Current with empty query = %d, want -1", got)
	}
	if got := s.CurrentRow(); got != -1 {
		t.Fatalf("CurrentRow with empty query = %d, want -1", got)
	}
	if got := s.Next(); got != -1 {
		t.Fatalf("Next with empty query = %d, want -1", got)
	}
	if got := s.Prev(); got != -1 {
		t.Fatalf("Prev with empty query = %d, want -1", got)
	}
	if got := s.Matches(); len(got) != 0 {
		t.Fatalf("Matches with empty query = %v, want none", got)
	}
}

func TestScrollSearchNoMatch(t *testing.T) {
	s := NewScrollSearch([]string{"alpha", "beta"}, "gamma")
	if got := s.Count(); got != 0 {
		t.Fatalf("Count = %d, want 0", got)
	}
	if got := s.Current(); got != -1 {
		t.Fatalf("Current = %d, want -1", got)
	}
	if got := s.Next(); got != -1 {
		t.Fatalf("Next = %d, want -1", got)
	}
	if got := s.Prev(); got != -1 {
		t.Fatalf("Prev = %d, want -1", got)
	}
	if s.HasMatch(0) || s.HasMatch(1) {
		t.Fatal("HasMatch true on a query with no match")
	}
	if got := s.SelectAtOrAbove(1); got != -1 {
		t.Fatalf("SelectAtOrAbove = %d, want -1", got)
	}
}

func TestScrollSearchSeveralMatchesInOneRow(t *testing.T) {
	s := NewScrollSearch([]string{"foo foo foo", "bar", "foo"}, "foo")
	if got, want := s.Matches(), []int{0, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Matches = %v, want %v", got, want)
	}
	if got := s.Count(); got != 2 {
		t.Fatalf("Count = %d, want 2", got)
	}
}

func TestScrollSearchMatchAcrossRows(t *testing.T) {
	rows := []string{"hello wo", "rld there"}
	s := NewScrollSearch(rows, "world")
	if got := s.Count(); got != 0 {
		t.Fatalf("Count for a match split across rows = %d, want 0", got)
	}
	s.SetQuery("hello wo")
	if got, want := s.Matches(), []int{0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Matches = %v, want %v", got, want)
	}
}

func TestScrollSearchCaseFolding(t *testing.T) {
	rows := []string{"Hello World", "hello world", "HELLO WORLD"}
	s := NewScrollSearch(rows, "hElLo wOrLd")
	if got, want := s.Matches(), []int{0, 1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Matches = %v, want %v", got, want)
	}
}

func TestScrollSearchNonASCII(t *testing.T) {
	rows := []string{"CAFÉ au lait", "thé glacé", "CAFE"}
	s := NewScrollSearch(rows, "café")
	if got, want := s.Matches(), []int{0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Matches = %v, want %v", got, want)
	}
	s.SetQuery("THÉ")
	if got, want := s.Matches(), []int{1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Matches for THÉ = %v, want %v", got, want)
	}
	s.SetQuery("ΑΒΓ")
	s.SetRows([]string{"αβγδ", "ΑΒΓΔ"})
	if got, want := s.Matches(), []int{0, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Matches for Greek text = %v, want %v", got, want)
	}
}

func TestScrollSearchWrapBothDirections(t *testing.T) {
	s := NewScrollSearch([]string{"a0", "b", "a1"}, "a")
	if got, want := s.Matches(), []int{0, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Matches = %v, want %v", got, want)
	}
	if got := s.Current(); got != 0 {
		t.Fatalf("Current = %d, want 0", got)
	}
	if got := s.Next(); got != 1 {
		t.Fatalf("Next = %d, want 1", got)
	}
	if got := s.CurrentRow(); got != 2 {
		t.Fatalf("CurrentRow = %d, want 2", got)
	}
	if got := s.Next(); got != 0 {
		t.Fatalf("Next past the last = %d, want 0", got)
	}
	if got := s.Prev(); got != 1 {
		t.Fatalf("Prev = %d, want 1", got)
	}
	if got := s.Prev(); got != 0 {
		t.Fatalf("Prev past the first = %d, want 0", got)
	}
}

func TestScrollSearchSelectAtOrAbove(t *testing.T) {
	s := NewScrollSearch([]string{"hit", "x", "hit", "x", "x", "hit"}, "hit")
	for _, tc := range []struct{ at, want int }{
		{0, 0},
		{1, 0},
		{2, 2},
		{4, 2},
		{5, 5},
		{9, 5},
	} {
		if got := s.SelectAtOrAbove(tc.at); got != tc.want {
			t.Errorf("SelectAtOrAbove(%d) = %d, want %d", tc.at, got, tc.want)
		}
		if got := s.CurrentRow(); got != tc.want {
			t.Errorf("CurrentRow after SelectAtOrAbove(%d) = %d, want %d", tc.at, got, tc.want)
		}
	}
}

func TestScrollSearchOffsetClamp(t *testing.T) {
	rows := make([]string, 10)
	for i := range rows {
		rows[i] = "row"
	}
	s := NewScrollSearch(rows, "row")
	const bandH = 4
	const maxOff = 6 // 10 rows less the 4 row band
	for _, tc := range []struct{ row, max, want int }{
		{0, maxOff, maxOff}, // wants 6, fits exactly
		{1, maxOff, 5},
		{6, maxOff, 0},
		{9, maxOff, 0}, // above the top row, clamps to 0
		{0, 3, 3},      // past maxOff, clamps to the cap
		{0, -1, 0},     // no scrollback
	} {
		if got := s.OffsetFor(tc.row, bandH, tc.max); got != tc.want {
			t.Errorf("OffsetFor(%d, %d, %d) = %d, want %d", tc.row, bandH, tc.max, got, tc.want)
		}
	}
	// The offset puts the matching row at the top of the band when it fits.
	for row := 0; row <= maxOff; row++ {
		off := s.OffsetFor(row, bandH, maxOff)
		if start := len(rows) - bandH - off; start != row {
			t.Errorf("OffsetFor(%d) leaves row %d at the top, want %d", row, start, row)
		}
	}
}
