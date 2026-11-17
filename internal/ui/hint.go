package ui

import "strings"

// Hint is one key hint: the keys that fire an action and the label that names
// it. A key written by the caller is painted as it stands, so "ctrl+x" reads
// the same on every screen. An empty key is how a caller says the action is
// unbound: the hint then paints none of its keys, and a hint left without a key
// is dropped whole.
type Hint struct {
	Keys  []string
	Label string
}

// Hints lays hints out as one row of plain text: one hint's keys joined by "/",
// then one space and the label when there is a label, then two spaces before
// the next hint. It writes no leading and no trailing space, and an empty list
// is the empty string.
//
// The result carries no escape sequence. A hint row is styled by the panel that
// paints it, never by this component.
func Hints(hints []Hint) string {
	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		text := hintText(h)
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "  ")
}

// hintText is one hint's text, or the empty string when every key is empty.
func hintText(h Hint) string {
	keys := make([]string, 0, len(h.Keys))
	for _, k := range h.Keys {
		if k == "" {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return ""
	}
	text := strings.Join(keys, "/")
	if h.Label != "" {
		text += " " + h.Label
	}
	return text
}
