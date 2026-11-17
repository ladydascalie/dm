package ui

import "testing"

// TestHints lays hints out as one plain row: keys joined by "/", one space and
// the label, two spaces between hints, and no space at either end. Empty keys
// drop out, and a hint with no key left drops out whole.
func TestHints(t *testing.T) {
	cases := []struct {
		name  string
		hints []Hint
		want  string
	}{
		{"no hints", nil, ""},
		{"empty list", []Hint{}, ""},
		{"one key", []Hint{{Keys: []string{"q"}, Label: "quit"}}, "q quit"},
		{"one key without a label", []Hint{{Keys: []string{"q"}}}, "q"},
		{"keys joined by a slash", []Hint{{Keys: []string{"j", "k"}, Label: "down/up"}}, "j/k down/up"},
		{"hints joined by two spaces", []Hint{
			{Keys: []string{"j", "k"}, Label: "down/up"},
			{Keys: []string{"g", "G"}, Label: "top/bottom"},
		}, "j/k down/up  g/G top/bottom"},
		{"an empty key drops out", []Hint{{Keys: []string{"", "ctrl+x"}, Label: "close"}}, "ctrl+x close"},
		{"a hint with no key drops out", []Hint{{Keys: []string{""}, Label: "quit"}}, ""},
		{"a hint with no keys at all drops out", []Hint{{Label: "quit"}}, ""},
		{"no key and no label drops out", []Hint{{}}, ""},
		{"unbound hint leaves its neighbours", []Hint{
			{Keys: []string{"q"}, Label: "quit"},
			{Keys: []string{""}, Label: "sync"},
			{Keys: []string{"r"}, Label: "read"},
		}, "q quit  r read"},
		{"every key empty", []Hint{{Keys: []string{"", ""}, Label: "quit"}}, ""},
		{"a label with spaces reads as written", []Hint{{Keys: []string{"ctrl+x"}, Label: "cut the line"}}, "ctrl+x cut the line"},
		{"no leading or trailing space", []Hint{{Keys: []string{"q"}, Label: "quit"}}, "q quit"},
	}
	for _, c := range cases {
		if got := Hints(c.hints); got != c.want {
			t.Fatalf("%s: Hints = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestShortKey folds only a control chord with one rune after it: ctrl+u is
// ^u, and everything else keeps its name so a footer never prints ^up.
func TestShortKey(t *testing.T) {
	cases := []struct{ key, want string }{
		{"ctrl+u", "^u"},
		{"ctrl+d", "^d"},
		{"ctrl+[", "^["},
		{"ctrl+shift+u", "ctrl+shift+u"},
		{"ctrl+up", "ctrl+up"},
		{"ctrl+space", "ctrl+space"},
		{"ctrl+", "ctrl+"},
		{"alt+u", "alt+u"},
		{"esc", "esc"},
		{"", ""},
	}
	for _, c := range cases {
		if got := ShortKey(c.key); got != c.want {
			t.Fatalf("ShortKey(%q) = %q, want %q", c.key, got, c.want)
		}
	}
}

// TestHintsPlainText holds the component to plain text: no escape sequence and
// no cell of styling, because the panel paints the row.
func TestHintsPlainText(t *testing.T) {
	got := Hints([]Hint{
		{Keys: []string{"esc"}, Label: "close"},
		{Keys: []string{"ctrl+x"}, Label: "cut"},
	})
	for i := 0; i < len(got); i++ {
		if got[i] == 0x1b {
			t.Fatalf("Hints = %q, want plain text", got)
		}
	}
	if got != "esc close  ctrl+x cut" {
		t.Fatalf("Hints = %q, want %q", got, "esc close  ctrl+x cut")
	}
}
