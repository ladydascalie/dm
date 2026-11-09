package blackbox

import (
	"strings"
	"testing"
)

// TestListTable proves dm list prints a human table by default and keeps the
// raw tree behind --json.
func TestListTable(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "table")
	root := h.focus("table")
	h.split("table", root, "right")
	h.run("set-project", "s1", "dm", "-s", "table")

	out := h.run("list", "-s", "table")
	for _, want := range []string{"PANE", "WS", "WINDOW", "APP", "STATUS", "CWD", "BRANCH", root, "s1", "w1", "shell"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list table %q does not contain %q", out, want)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Fatalf("list without --json printed JSON: %q", out)
	}

	raw := h.run("list", "-s", "table", "--json")
	if !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		t.Fatalf("list --json did not print JSON: %q", raw)
	}
}

// TestLsTable proves dm ls prints counts and the active project for a live
// session, and reads the same facts from state.json once the server is gone.
func TestLsTable(t *testing.T) {
	h := newHarness(t)
	h.run("new", "-s", "one")
	h.run("new", "-s", "two")
	h.run("set-project", "s1", "dm", "-s", "one")

	out := h.run("ls")
	for _, want := range []string{"NAME", "STATE", "PANES", "WINDOWS", "WORKSPACES", "PROJECT", "CWD", "one", "two", "live", "dm"} {
		if !strings.Contains(out, want) {
			t.Fatalf("ls table %q does not contain %q", out, want)
		}
	}

	h.run("kill", "-s", "one")
	out = h.run("ls")
	if !strings.Contains(out, "dead") {
		t.Fatalf("ls after kill = %q, want a dead row", out)
	}
	if !strings.Contains(out, "dm") {
		t.Fatalf("ls after kill lost the project: %q", out)
	}
}
