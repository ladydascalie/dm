package clidoc

import (
	"strings"
	"testing"
)

// TestToggleSecretsNoteInEveryPage proves the man page, the HTML page and
// llms.txt all carry the same note about the ctrl+g toggle, so no one of them
// can drift into claiming a control the toggle is not.
func TestToggleSecretsNoteInEveryPage(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		page string
		note string
	}{
		{"man", ManPage(), roffEscape(ToggleSecretsNote)},
		{"html", HTMLPage(), htmlEscape(ToggleSecretsNote)},
		{"llms", LLMsText(), ToggleSecretsNote},
	} {
		if !strings.Contains(c.page, c.note) {
			t.Errorf("%s does not carry ToggleSecretsNote", c.name)
		}
	}
}

// TestToggleSecretsNoteStatesTheContract proves the note keeps the three
// clauses the toggle's documentation owes a reader: what the affordance is for,
// that it is not a security control, and that the values survive while it is
// off.
func TestToggleSecretsNoteStatesTheContract(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		"edit a secret in place",
		"not a security control",
		"registered values are kept",
	} {
		if !strings.Contains(ToggleSecretsNote, want) {
			t.Errorf("ToggleSecretsNote does not say %q", want)
		}
	}
}
