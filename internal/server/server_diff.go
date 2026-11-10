package server

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// diffMaxLines is the longest diff one reply carries. A generated file can
// print hundreds of thousands of lines, and the client holds the whole reply
// in memory.
const diffMaxLines = 4000

// diffTimeout is the deadline for one git command behind the diff. It is
// longer than the rail's second: the operator asked for this one, and a large
// tree takes its time.
const diffTimeout = 5 * time.Second

// gitDiffOutput runs one git command for the diff, with the diff's deadline.
func gitDiffOutput(cwd string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), diffTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, args...)...).Output()
}

// paneDiff is the uncommitted diff of a pane's shell folder: the stat block, a
// blank line, then the patch. It is cut at diffMaxLines with a final line that
// says so. A folder outside git, or a tree with no change, answers with one
// line that explains it: nothing to show is not an error.
func (s *server) paneDiff(id string) []string {
	cwd := s.liveCwd(id)
	if cwd == "" {
		return []string{"pane " + id + " has no working directory"}
	}
	if _, err := gitDiffOutput(cwd, "rev-parse", "--show-toplevel"); err != nil {
		return []string{cwd + " is not a git repository"}
	}
	stat, err := gitDiffOutput(cwd, "--no-pager", "diff", "--stat")
	if err != nil {
		return []string{"git diff --stat: " + err.Error()}
	}
	body, err := gitDiffOutput(cwd, "--no-pager", "diff")
	if err != nil {
		return []string{"git diff: " + err.Error()}
	}
	var lines []string
	for _, block := range [][]byte{stat, body} {
		text := strings.TrimRight(string(block), "\n")
		if text == "" {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, strings.Split(text, "\n")...)
	}
	if len(lines) == 0 {
		return []string{"no uncommitted changes in " + cwd}
	}
	if len(lines) > diffMaxLines {
		lines = lines[:diffMaxLines]
		lines = append(lines, fmt.Sprintf("diff cut at %d lines; the tree has more", diffMaxLines))
	}
	return lines
}
