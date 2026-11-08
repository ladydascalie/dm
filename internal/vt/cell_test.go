package vt

import "testing"

func TestNewCellMeasuresWidth(t *testing.T) {
	st := DefaultStyle()
	cases := []struct {
		content string
		want    int8
	}{
		{"a", WidthNormal},
		{"", WidthNormal},
		{"世", WidthWide},
		{"😀", WidthWide},
		{"e\u0301", WidthNormal}, // combining acute rides its base
		{"👨‍👩‍👧‍👦", WidthWide},   // a ZWJ family is one cluster
		{"\u0301", WidthNormal},  // a lone combining mark still takes a slot
	}
	for _, tc := range cases {
		got := NewCell(tc.content, st)
		if got.Width != tc.want {
			t.Errorf("NewCell(%q).Width = %d, want %d", tc.content, got.Width, tc.want)
		}
		if tc.content == "" && got.Content != " " {
			t.Errorf("NewCell(%q).Content = %q, want a space", tc.content, got.Content)
		}
	}
}

func TestFirstClusterSplitsByGrapheme(t *testing.T) {
	cases := []struct {
		in    string
		want  string
		width int
	}{
		{"abc", "a", 1},
		{"世界", "世", 2},
		{"e\u0301x", "e\u0301", 1},
		{"👨‍👩‍👧‍👦x", "👨‍👩‍👧‍👦", 2},
	}
	for _, tc := range cases {
		got, w := FirstCluster(tc.in)
		if got != tc.want || w != tc.width {
			t.Errorf("FirstCluster(%q) = %q, %d, want %q, %d", tc.in, got, w, tc.want, tc.width)
		}
	}
}

func TestColorKinds(t *testing.T) {
	if c := DefaultColor(); c.Kind != ColorDefault {
		t.Errorf("DefaultColor().Kind = %v, want ColorDefault", c.Kind)
	}
	if c := PaletteColor(42); c.Kind != ColorPalette || c.Index != 42 {
		t.Errorf("PaletteColor(42) = %+v, want a palette colour 42", c)
	}
	if c := RGBColor(1, 2, 3); c.Kind != ColorRGB || c.R != 1 || c.G != 2 || c.B != 3 {
		t.Errorf("RGBColor(1,2,3) = %+v, want rgb 1,2,3", c)
	}
}
