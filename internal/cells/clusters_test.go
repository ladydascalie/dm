package cells

import (
	"strings"
	"testing"
)

// TestFirstClusterSplitsTheFirstGlyph checks the first grapheme cluster comes
// back whole, with the width it paints. A family emoji is one cluster of two
// cells, an accented e is one cluster of one cell.
func TestFirstClusterSplitsTheFirstGlyph(t *testing.T) {
	cases := []struct {
		s     string
		first string
		cells int
	}{
		{"", "", 0},
		{"abc", "a", 1},
		{"日x", "日", 2},
		{family, family, 2},
		{"e\u0301b", "e\u0301", 1},
		{"a" + family + "b", "a", 1},
	}
	for _, c := range cases {
		first, n := FirstCluster(c.s)
		if first != c.first || n != c.cells {
			t.Errorf("FirstCluster(%q) = %q, %d, want %q, %d", c.s, first, n, c.first, c.cells)
		}
		if n != Cells(first) {
			t.Errorf("FirstCluster(%q) width %d disagrees with Cells %d", c.s, n, Cells(first))
		}
	}
}

// TestFirstClusterBytesSplitsWithoutACopy checks the byte form returns the same
// clusters, slicing the input rather than copying it.
func TestFirstClusterBytesSplitsWithoutACopy(t *testing.T) {
	if b, n := FirstClusterBytes(nil); b != nil || n != 0 {
		t.Errorf("FirstClusterBytes(nil) = %q, %d", b, n)
	}
	s := "a" + family + "b"
	first, n := FirstClusterBytes([]byte(s))
	if string(first) != "a" || n != 1 {
		t.Errorf("FirstClusterBytes head = %q, %d, want %q, 1", first, n, "a")
	}
	// The remainder starts where the first cluster ended, whole clusters.
	rest := []byte(s)[len(first):]
	second, _ := FirstClusterBytes(rest)
	if string(second) != family {
		t.Errorf("next cluster = %q, want the family glyph", second)
	}
}

// TestAllDigitsShapesAnID checks the id-shape test: digits only, at least one,
// and anything else fails.
func TestAllDigitsShapesAnID(t *testing.T) {
	yes := []string{"0", "9", "123", "000"}
	no := []string{"", "w1", "1w", "1.2", " 1", "１", "1\n"}
	for _, s := range yes {
		if !AllDigits(s) {
			t.Errorf("AllDigits(%q) = false", s)
		}
	}
	for _, s := range no {
		if AllDigits(s) {
			t.Errorf("AllDigits(%q) = true", s)
		}
	}
}

// TestElideBelowOneCell checks a budget too small for the ellipsis returns an
// empty string, never a glyph painted over the budget.
func TestElideBelowOneCell(t *testing.T) {
	if got := Elide("abc", 0); got != "" {
		t.Errorf("Elide(%q, 0) = %q, want empty", "abc", got)
	}
	if got := Elide(family, 1); got != "…" {
		t.Errorf("Elide(family, 1) = %q, want the ellipsis alone", got)
	}
	// An empty string fits any budget and comes back untouched.
	if got := Elide("", 5); got != "" {
		t.Errorf("Elide(empty, 5) = %q", got)
	}
	// A wide glyph leaves no room for the mark: cut to the budget, then mark.
	if got := Elide("日日", 3); got != "日…" {
		t.Errorf("Elide(日日, 3) = %q, want 日…", got)
	}
}

// TestClipTailEmptyBudget checks a budget of zero or less clips to nothing,
// and a string that fits comes back whole.
func TestClipTailEmptyBudget(t *testing.T) {
	for _, budget := range []int{0, -1} {
		if got := ClipTail("abc", budget); got != "" {
			t.Errorf("ClipTail(%q, %d) = %q, want empty", "abc", budget, got)
		}
	}
	if got := ClipTail("abc", 3); got != "abc" {
		t.Errorf("ClipTail(%q, 3) = %q, want it whole", "abc", got)
	}
	if got := ClipTail("abc", 10); got != "abc" {
		t.Errorf("ClipTail(%q, 10) = %q, want it whole", "abc", got)
	}
}

// TestClipTailCountsClustersNotRunes pins the cluster walk: the budget is in
// cells, so two wide runes fill three cells only if a cell is left over, and a
// join never splits mid-glyph.
func TestClipTailCountsClustersNotRunes(t *testing.T) {
	// Three cells of budget over 日日 (four cells) keeps one whole rune.
	if got := ClipTail("日日", 3); got != "日" {
		t.Errorf("ClipTail(日日, 3) = %q, want 日", got)
	}
	// A one-cell budget over the family glyph (two cells) keeps nothing.
	if got := ClipTail(family, 1); got != "" {
		t.Errorf("ClipTail(family, 1) = %q, want empty", got)
	}
	// Combining marks ride their base: one cluster, one cell.
	if got := ClipTail("abe\u0301", 2); got != "be\u0301" {
		t.Errorf("ClipTail(abe\u0301, 2) = %q, want be\u0301", got)
	}
}

// TestPadNeverExceedsTheBudget checks Pad clips long text and pads short text
// to exactly the budget, in cells not runes.
func TestPadNeverExceedsTheBudget(t *testing.T) {
	if got := Pad("abc", -1); got != "" {
		t.Errorf("Pad(abc, -1) = %q, want empty", got)
	}
	if got := Pad(family, 4); got != family+"  " {
		t.Errorf("Pad(family, 4) = %q, want the glyph plus two spaces", got)
	}
	if n := Cells(Pad(family, 4)); n != 4 {
		t.Errorf("Pad(family, 4) paints %d cells", n)
	}
	// A budget of one cell clips the two-cell glyph to nothing.
	if got := Pad(family, 1); got != " " {
		t.Errorf("Pad(family, 1) = %q, want one space", got)
	}
}

// TestCenterOfWideText checks centering counts cells: the family glyph in a
// budget of six gets two cells each side.
func TestCenterOfWideText(t *testing.T) {
	got := Center(family, 6)
	if got != "  "+family+"  " {
		t.Errorf("Center(family, 6) = %q", got)
	}
	if strings.Count(got, " ") != 4 {
		t.Errorf("Center(family, 6) is not balanced: %q", got)
	}
}
