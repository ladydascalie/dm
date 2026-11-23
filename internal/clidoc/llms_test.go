package clidoc

import (
	"strings"
	"testing"
)

// TestLLMSTextNamesEveryCommand proves the command table names every command in
// cliCommands, alone in its cell or beside its group in a shared row.
func TestLLMSTextNamesEveryCommand(t *testing.T) {
	doc := LLMsText()
	for _, c := range cliCommands {
		line := "dm"
		if c.name != "" {
			line += " " + c.name
		}
		if !namesCommand(doc, line) {
			t.Errorf("llms.txt does not name %q", line)
		}
		if c.args != "" && !strings.Contains(doc, llmsCell(c.args)) {
			t.Errorf("llms.txt does not carry the argument shape of %q", line)
		}
	}
}

// namesCommand reports whether the doc names a command as a whole word: alone in
// its cell, or beside the next name of its group.
func namesCommand(doc, line string) bool {
	return strings.Contains(doc, line+"`") || strings.Contains(doc, line+",")
}
