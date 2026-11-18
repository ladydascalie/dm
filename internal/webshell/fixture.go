package webshell

// The pretend world: one project the commands agree with. A path not here is
// not on disk, and every command reads the same two maps, so cd, ls, cat and
// git cannot contradict each other.

// tree maps a directory to its entries, in the order ls prints them.
var tree = map[string][]string{
	Home:               {"projects"},
	Home + "/projects": {"hello"},
	Repo:               {"README.md", "greet.go", "main.go"},
	"/":                {"home"},
	"/home":            {"guest"},
}

// files maps a path to its contents.
var files = map[string]string{
	Repo + "/README.md": "# hello\n\nA tiny Go project that greets you.\n\n    go run .\n",
	Repo + "/main.go": `package main

import "fmt"

func main() {
	fmt.Println(greet("world"))
}
`,
	Repo + "/greet.go": `package main

import "fmt"

func greet(who string) string {
	return fmt.Sprintf("hello, %s", who)
}
`,
	Home + "/.profile": "export PS1='guest@dm:\\w$ '\n",
}

const gitStatus = "On branch main\r\n" +
	"Your branch is up to date with 'origin/main'.\r\n" +
	"\r\n" +
	"Changes not staged for commit:\r\n" +
	"  (use \"git add <file>...\" to update what will be committed)\r\n" +
	"  (use \"git restore <file>...\" to discard changes in working directory)\r\n" +
	"\tmodified:   greet.go\r\n" +
	"\r\n" +
	"no changes added to commit (use \"git add\" and/or \"git commit -a\")\r\n"

const gitLog = "3f2a1c9 greet: take the name from the caller\r\n" +
	"a71b0e4 main: print the greeting\r\n" +
	"5c0d733 greet: return the string instead of printing it\r\n" +
	"9e88b21 first commit\r\n"

const gitDiff = "diff --git a/greet.go b/greet.go\r\n" +
	"index 5c0d733..3f2a1c9 100644\r\n" +
	"--- a/greet.go\r\n" +
	"+++ b/greet.go\r\n" +
	"@@ -3,5 +3,5 @@ package main\r\n" +
	" import \"fmt\"\r\n" +
	" \r\n" +
	" func greet(who string) string {\r\n" +
	"-\treturn \"hello, world\"\r\n" +
	"+\treturn fmt.Sprintf(\"hello, %s\", who)\r\n" +
	" }\r\n"
