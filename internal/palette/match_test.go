package palette

import "testing"

// names turns a ranked index list into the names it points at, for readable
// expectations.
func names(cmds []Command, idx []int) []string {
	out := make([]string, len(idx))
	for i, at := range idx {
		out[i] = cmds[at].Name
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFilterOrdering(t *testing.T) {
	tests := []struct {
		name  string
		query string
		cmds  []Command
		want  []string
	}{
		{
			name:  "consecutive run beats spread out match",
			query: "abc",
			cmds:  []Command{{Name: "axbxc"}, {Name: "abc"}},
			want:  []string{"abc", "axbxc"},
		},
		{
			name:  "word boundary beats mid word match",
			query: "ba",
			cmds:  []Command{{Name: "xbar"}, {Name: "bar"}},
			want:  []string{"bar", "xbar"},
		},
		{
			name:  "camel case hump counts as a boundary",
			query: "np",
			cmds:  []Command{{Name: "unplug"}, {Name: "NewPane"}},
			want:  []string{"NewPane", "unplug"},
		},
		{
			name:  "earlier match beats later match",
			query: "o",
			cmds:  []Command{{Name: "xao"}, {Name: "xoa"}},
			want:  []string{"xoa", "xao"},
		},
		{
			name:  "separator starts a word",
			query: "pw",
			cmds:  []Command{{Name: "pnewpane"}, {Name: "pane-wire"}},
			want:  []string{"pane-wire", "pnewpane"},
		},
		{
			name:  "case folds on both sides",
			query: "HW",
			cmds:  []Command{{Name: "héllo wörld"}},
			want:  []string{"héllo wörld"},
		},
		{
			name:  "unicode runes match as a subsequence",
			query: "日テ",
			cmds:  []Command{{Name: "日本語テスト"}},
			want:  []string{"日本語テスト"},
		},
		{
			name:  "hint match ranks under the same name match",
			query: "pane",
			cmds: []Command{
				{Name: "wire", Hint: "close the pane"},
				{Name: "pane"},
			},
			want: []string{"pane", "wire"},
		},
		{
			name:  "hint carries a command whose name does not match",
			query: "zoom",
			cmds:  []Command{{Name: "fit", Hint: "zoom the pane"}},
			want:  []string{"fit"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := names(tt.cmds, Filter(tt.cmds, tt.query))
			if !equal(got, tt.want) {
				t.Errorf("Filter(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestFilterTieBreaksOnInputOrder(t *testing.T) {
	cmds := []Command{
		{Name: "same"}, {Name: "other"}, {Name: "same"}, {Name: "same"},
	}
	got := names(cmds, Filter(cmds, "same"))
	want := []string{"same", "same", "same"}
	if !equal(got, want) {
		t.Errorf("Filter tie-break = %v, want %v in input order", got, want)
	}
}

func TestFilterEmptyQueryReturnsAll(t *testing.T) {
	cmds := []Command{{Name: "c"}, {Name: "b"}, {Name: "a"}}
	got := names(cmds, Filter(cmds, ""))
	want := []string{"c", "b", "a"}
	if !equal(got, want) {
		t.Errorf("Filter(\"\") = %v, want %v", got, want)
	}
}

func TestFilterNoMatchReturnsEmpty(t *testing.T) {
	cmds := []Command{{Name: "abc"}, {Name: "def"}}
	if got := Filter(cmds, "zzz"); len(got) != 0 {
		t.Errorf("Filter(\"zzz\") = %v, want empty", got)
	}
}

func TestScorePrefersEarlierOnEqualQuality(t *testing.T) {
	// Both alignments of "ab" in "axbxab" score the same by run and boundary,
	// so the earliest one wins the tie.
	if s, ok := score([]rune("axbxab"), []rune("ab")); !ok || s == 0 {
		t.Errorf("score(axbxab, ab) = %d, %v, want a positive match", s, ok)
	}
	if _, ok := score([]rune("short"), []rune("longer")); ok {
		t.Error("score(short, longer) matched, want no match")
	}
}
