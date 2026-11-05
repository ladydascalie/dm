package clidoc

import (
	"strings"
	"testing"
)

// TestLLMSTextNamesEveryCommand proves the command table carries a row for
// every command in cliCommands.
func TestLLMSTextNamesEveryCommand(t *testing.T) {
	doc := LLMsText()
	for _, c := range cliCommands {
		line := "dm"
		if c.name != "" {
			line += " " + c.name
		}
		if !strings.Contains(doc, "| `"+line+"`") {
			t.Errorf("llms.txt does not name %q", line)
		}
		if c.args != "" && !strings.Contains(doc, llmsCell(c.args)) {
			t.Errorf("llms.txt does not carry the argument shape of %q", line)
		}
	}
}
