package clidoc

import (
	"encoding/json"
	"strings"
	"testing"

	"dm/internal/wire"
)

// TestActionExamplesCoverEveryName proves the table and manActionNames name the
// same actions: no action without an example, no example without an action.
func TestActionExamplesCoverEveryName(t *testing.T) {
	inNames := make(map[string]bool, len(manActionNames))
	for _, n := range manActionNames {
		inNames[n] = true
	}
	have := make(map[string]bool, len(actionExamples))
	for _, e := range actionExamples {
		if e.name == "" {
			t.Error("actionExamples has a row with no name")
			continue
		}
		if strings.TrimSpace(e.example) == "" {
			t.Errorf("action %q has an empty example", e.name)
		}
		if have[e.name] {
			t.Errorf("action %q has more than one example", e.name)
		}
		have[e.name] = true
		if !inNames[e.name] {
			t.Errorf("action %q is not in manActionNames", e.name)
		}
	}
	for _, n := range manActionNames {
		if !have[n] {
			t.Errorf("action %q has no example", n)
		}
	}
}

// TestActionExamplesParse proves every example is a JSON object that decodes
// into wire.Msg and names the action it sits under.
func TestActionExamplesParse(t *testing.T) {
	for _, e := range actionExamples {
		var m wire.Msg
		if err := json.Unmarshal([]byte(e.example), &m); err != nil {
			t.Errorf("action %q: bad JSON: %v", e.name, err)
			continue
		}
		if m.Action != e.name {
			t.Errorf("action %q: body names action %q", e.name, m.Action)
		}
	}
}

// TestActionExamplesOneLine proves every example stays on one line, because
// each renderer prints it in a single code block or table cell.
func TestActionExamplesOneLine(t *testing.T) {
	for _, e := range actionExamples {
		if strings.ContainsAny(e.example, "\r\n") {
			t.Errorf("action %q: example spans more than one line", e.name)
		}
	}
}

// TestLLMSActionGroupsCoverEveryName proves the llms.txt table packs every
// action exactly once, so a grouped row cannot drop or repeat an action.
func TestLLMSActionGroupsCoverEveryName(t *testing.T) {
	want := make(map[string]bool, len(manActionNames))
	for _, n := range manActionNames {
		want[n] = true
	}
	seen := make(map[string]bool, len(manActionNames))
	for _, group := range llmsActionGroups {
		for _, n := range group {
			if !want[n] {
				t.Errorf("llms group names %q, which is not in manActionNames", n)
			}
			if seen[n] {
				t.Errorf("llms group repeats %q", n)
			}
			seen[n] = true
		}
	}
	for _, n := range manActionNames {
		if !seen[n] {
			t.Errorf("llms groups do not name %q", n)
		}
	}
}
