package webshell

import (
	"regexp"
	"strings"
	"testing"
)

// plain drops the SGR sequences, so an assertion can read the words.
func plain(s string) string {
	return regexp.MustCompile("\\x1b\\[[0-9;]*m").ReplaceAllString(s, "")
}

// type is one line as a person would type it, then enter.
func run(s *Shell, line string) string {
	return string(s.Feed([]byte(line + "\r")))
}

func TestPromptFollowsTheDirectory(t *testing.T) {
	s := New()
	if got := string(s.Greet()); !strings.Contains(got, "~/projects/hello") {
		t.Fatalf("greeting has no prompt for the repo: %q", got)
	}
	if got := run(s, "cd .."); strings.Contains(got, "no such file") {
		t.Fatalf("cd .. refused: %q", got)
	}
	if got := string(s.Feed([]byte(""))); got != "" {
		t.Fatalf("an empty read printed %q", got)
	}
	if got := run(s, "pwd"); !strings.Contains(got, "/home/guest/projects") {
		t.Fatalf("pwd after cd .. = %q", got)
	}
	if got := plain(run(s, "")); !strings.Contains(got, ":~/projects$") {
		t.Fatalf("prompt for ~/projects = %q", got)
	}
}

func TestLsAndCdAgree(t *testing.T) {
	s := New()
	if got := plain(run(s, "cd /home/guest")); !strings.Contains(got, ":~$") {
		t.Fatalf("prompt after cd home = %q", got)
	}
	if got := run(s, "ls"); !strings.Contains(got, "projects") {
		t.Fatalf("ls in home = %q", got)
	}
	if got := run(s, "cd projects/hello"); !strings.Contains(got, "~/projects/hello") {
		t.Fatalf("prompt in the repo = %q", got)
	}
	got := run(s, "ls")
	for _, want := range []string{"README.md", "greet.go", "main.go"} {
		if !strings.Contains(got, want) {
			t.Fatalf("ls in the repo = %q, missing %s", got, want)
		}
	}
}

func TestCatReadsTheSameTree(t *testing.T) {
	s := New()
	if got := run(s, "cat greet.go"); !strings.Contains(got, "fmt.Sprintf") {
		t.Fatalf("cat greet.go = %q", got)
	}
	if got := run(s, "cat nope.go"); !strings.Contains(got, "No such file") {
		t.Fatalf("cat of a missing file = %q", got)
	}
	if got := run(s, "cat .."); !strings.Contains(got, "Is a directory") {
		t.Fatalf("cat of a folder = %q", got)
	}
	if got := run(s, "cat"); !strings.Contains(got, "usage") {
		t.Fatalf("cat with no argument = %q", got)
	}
}

func TestGitKnowsWhereItStands(t *testing.T) {
	s := New()
	if got := run(s, "git status"); !strings.Contains(got, "modified:   greet.go") {
		t.Fatalf("git status in the repo = %q", got)
	}
	if got := run(s, "git diff"); !strings.Contains(got, "+++ b/greet.go") {
		t.Fatalf("git diff = %q", got)
	}
	if got := run(s, "git log"); !strings.Contains(got, "first commit") {
		t.Fatalf("git log = %q", got)
	}
	run(s, "cd /home/guest")
	if got := run(s, "git status"); !strings.Contains(got, "not a git repository") {
		t.Fatalf("git status outside the repo = %q", got)
	}
}

func TestUnknownCommandsAreRefused(t *testing.T) {
	s := New()
	got := run(s, "sudo rm -rf /")
	if !strings.Contains(got, "sudo: command not found") {
		t.Fatalf("an unknown command = %q", got)
	}
	if got := run(s, "git push"); !strings.Contains(got, "not part of this pretend repository") {
		t.Fatalf("a git verb the fake does not have = %q", got)
	}
}

func TestLineEditing(t *testing.T) {
	s := New()
	// abd, backspace, c -> abc, and the prompt does not know "abc".
	if got := string(s.Feed([]byte("abd\x7f" + "c"))); got != "abd\b \bc" {
		t.Fatalf("typing and erasing = %q", got)
	}
	if got := run(s, ""); !strings.Contains(got, "abc: command not found") {
		t.Fatalf("the edited line = %q", got)
	}
	// ctrl+u erases the whole line.
	if got := string(s.Feed([]byte("zzz\x15"))); !strings.HasSuffix(got, "\b \b\b \b\b \b") {
		t.Fatalf("ctrl+u = %q", got)
	}
	if got := run(s, ""); strings.Contains(got, "zzz") {
		t.Fatalf("ctrl+u left the line behind: %q", got)
	}
}

func TestOnRunHearsTheLine(t *testing.T) {
	s := New()
	var heard []string
	s.OnRun = func(line string) { heard = append(heard, line) }
	run(s, "echo hi")
	run(s, "")
	if len(heard) != 2 || heard[0] != "echo hi" || heard[1] != "" {
		t.Fatalf("OnRun heard %q", heard)
	}
}

func TestUpAndDownWalkTheHistory(t *testing.T) {
	s := New()
	run(s, "echo one")
	run(s, "echo two")

	// Up returns the newest line, and prints it back with the prompt.
	got := plain(string(s.Feed([]byte("\x1b[A"))))
	if !strings.HasSuffix(got, "$ echo two") {
		t.Fatalf("up = %q", got)
	}
	got = plain(string(s.Feed([]byte("\x1b[A"))))
	if !strings.HasSuffix(got, "$ echo one") {
		t.Fatalf("second up = %q", got)
	}
	// The oldest line does not walk further back: nothing is redrawn, and the
	// line is still the one on screen.
	if got := s.Feed([]byte("\x1b[A")); got != nil {
		t.Fatalf("up past the oldest redrew %q", got)
	}
	if got := plain(string(s.redraw())); !strings.HasSuffix(got, "$ echo one") {
		t.Fatalf("the line after walking to the oldest = %q", got)
	}
	// Down comes back, and lands on the empty line being typed.
	s.Feed([]byte("\x1b[A"))
	s.Feed([]byte("\x1b[A"))
	got = plain(string(s.Feed([]byte("\x1b[B"))))
	if !strings.HasSuffix(got, "$ echo two") {
		t.Fatalf("down = %q", got)
	}
	got = plain(string(s.Feed([]byte("\x1b[B"))))
	if !strings.HasSuffix(got, "$ ") {
		t.Fatalf("down to the draft = %q", got)
	}
	// The draft comes back rather than being lost.
	s.Feed([]byte("half"))
	s.Feed([]byte("\x1b[A"))
	s.Feed([]byte("\x1b[B"))
	got = plain(string(s.Feed([]byte("\r"))))
	if !strings.Contains(got, "half: command not found") {
		t.Fatalf("the draft after walking = %q", got)
	}
}

func TestHistorySkipsRepeatsAndBlanks(t *testing.T) {
	s := New()
	run(s, "pwd")
	run(s, "pwd")
	run(s, "")
	if len(s.history) != 1 {
		t.Fatalf("history = %q, want one entry", s.history)
	}
	// An empty history ignores up.
	empty := New()
	if got := empty.Feed([]byte("\x1b[A")); got != nil {
		t.Fatalf("up on an empty history printed %q", got)
	}
}
