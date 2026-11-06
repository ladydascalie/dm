package cells

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// family is one glyph: man, joiner, woman, joiner, girl. It paints two cells and
// holds five runes, so any helper that counts runes or walks them one by one
// gets it wrong.
const family = "\U0001F468\u200D\U0001F469\u200D\U0001F467"

func textCases() []struct {
	name   string
	s      string
	cells  int
	budget int
	clip   string
	elide  string
} {
	return []struct {
		name   string
		s      string
		cells  int
		budget int
		clip   string
		elide  string
	}{
		{"ascii", "abc", 3, 2, "ab", "a…"},
		{"ascii fits", "abc", 3, 3, "abc", "abc"},
		{"two wide runes", "日日", 4, 3, "日", "日…"},
		{"joined family", family, 2, 1, "", "…"},
		{"joined family fits", family, 2, 2, family, family},
		{"base plus acute", "e\u0301", 1, 1, "e\u0301", "e\u0301"},
		{"cut inside a cluster", "a" + family, 3, 2, "a", "a…"},
		{"cut at the join", "a" + family, 3, 1, "a", "…"},
	}
}

func TestTextCells(t *testing.T) {
	for _, c := range textCases() {
		if got := Cells(c.s); got != c.cells {
			t.Errorf("%s: Cells(%q) = %d, want %d", c.name, c.s, got, c.cells)
		}
	}
}

func TestTextClipKeepsWholeClusters(t *testing.T) {
	for _, c := range textCases() {
		got := Clip(c.s, c.budget)
		if got != c.clip {
			t.Errorf("%s: Clip(%q, %d) = %q, want %q", c.name, c.s, c.budget, got, c.clip)
		}
		if n := Cells(got); n > c.budget {
			t.Errorf("%s: Clip(%q, %d) paints %d cells", c.name, c.s, c.budget, n)
		}
		if !strings.HasPrefix(c.s, got) {
			t.Errorf("%s: Clip(%q, %d) = %q is not a prefix", c.name, c.s, c.budget, got)
		}
	}
}

func TestTextElideEndsAtTheBudget(t *testing.T) {
	for _, c := range textCases() {
		got := Elide(c.s, c.budget)
		if got != c.elide {
			t.Errorf("%s: Elide(%q, %d) = %q, want %q", c.name, c.s, c.budget, got, c.elide)
		}
		if n := Cells(got); n > c.budget {
			t.Errorf("%s: Elide(%q, %d) paints %d cells", c.name, c.s, c.budget, n)
		}
	}
}

func TestTextPadCountsCells(t *testing.T) {
	for _, c := range textCases() {
		for budget := 0; budget <= c.cells+2; budget++ {
			got := Pad(c.s, budget)
			if n := Cells(got); n > budget {
				t.Errorf("%s: Pad(%q, %d) paints %d cells", c.name, c.s, budget, n)
			}
		}
	}
	// Two wide runes and one space: three cells, but only two runes.
	got := Pad("日日", 3)
	if n := Cells(got); n != 3 {
		t.Errorf("Pad(%q, 3) paints %d cells, want 3", "日日", n)
	}
	if n := utf8.RuneCountInString(got); n == 3 {
		t.Errorf("Pad(%q, 3) = %q holds %d runes, so it counted runes", "日日", got, n)
	}
}

func TestTextClipTailKeepsTheEnd(t *testing.T) {
	cases := []struct {
		s      string
		budget int
		want   string
	}{
		{"abc", 2, "bc"},
		{"日日", 3, "日"},
		{family, 2, family},
		{"a" + family, 2, family},
		{"a" + family, 1, ""},
	}
	for _, c := range cases {
		got := ClipTail(c.s, c.budget)
		if got != c.want {
			t.Errorf("ClipTail(%q, %d) = %q, want %q", c.s, c.budget, got, c.want)
		}
		if n := Cells(got); n > c.budget {
			t.Errorf("ClipTail(%q, %d) paints %d cells", c.s, c.budget, n)
		}
		if !strings.HasSuffix(c.s, got) {
			t.Errorf("ClipTail(%q, %d) = %q is not a suffix", c.s, c.budget, got)
		}
	}
}

// The five shims text.go used to carry are gone. These pin what each one did
// under the name that replaced it.
func TestTextCoversTheOldNames(t *testing.T) {
	// runeWidth: one rune at a time, wide and combining.
	if Cells("A") != 1 || Cells("日") != 2 || Cells("\u0301") != 0 {
		t.Errorf("cells of one rune = %d, %d, %d, want 1, 2, 0",
			Cells("A"), Cells("日"), Cells("\u0301"))
	}
	// runeCells: a run of text.
	if got := Cells("ab日"); got != 4 {
		t.Errorf("Cells(%q) = %d, want 4", "ab日", got)
	}
	// boxCells: a box row.
	if got := Cells("│ key │"); got != 7 {
		t.Errorf("Cells(%q) = %d, want 7", "│ key │", got)
	}
	// helpBoxElide: cut and mark, or the Ellipsis alone.
	if got := Elide("label", 4); got != "lab…" {
		t.Errorf("Elide(%q, 4) = %q, want %q", "label", got, "lab…")
	}
	if got := Elide("label", 1); got != "…" {
		t.Errorf("Elide(%q, 1) = %q, want the Ellipsis alone", "label", got)
	}
	// helpBoxPad: fill never cuts, a long string clips to the budget.
	if got := Pad("ab", 4); got != "ab  " {
		t.Errorf("Pad(%q, 4) = %q, want %q", "ab", got, "ab  ")
	}
	if got := Pad("abc", 2); got != "ab" {
		t.Errorf("Pad(%q, 2) = %q, want %q", "abc", got, "ab")
	}
}
