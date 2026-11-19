package webshell

// The pretend world: one project the commands agree with. A path not here is
// not on disk, and every command reads the same two maps, so cd, ls, cat and
// git cannot contradict each other.

// tree maps a directory to its entries, in the order ls prints them.
var tree = map[string][]string{
	Home:               {"projects"},
	Home + "/projects": {"hello"},
	Repo:               {"CHANGELOG.md", "README.md", "greet.go", "main.go"},
	"/":                {"home"},
	"/home":            {"guest"},
}

// files maps a path to its contents.
var files = map[string]string{
	Repo + "/README.md": "# hello\n\nA tiny Go project that greets you.\n\n    go run .\n",
	Repo + "/CHANGELOG.md": `# Changelog

Every change to hello, newest first.

## Unreleased

- greet takes the name from the caller
- main prints what greet returns
- add a case for an empty name

## 0.4.0

- greet returns the string instead of printing it
- main prints the result
- README says how to run it

## 0.3.3

- fix the greeting for a name with a space
- drop the unused import

## 0.3.2

- print the greeting once, not twice
- keep the build green

## 0.3.1

- add an example to the README
- rename the package to main

## 0.3.0

- split greet into its own file
- add the first test

## 0.2.3

- refuse an empty name
- say hello, not hi

## 0.2.2

- trim the name before printing
- lowercase the first letter

## 0.2.1

- print to stdout instead of stderr
- add the run line to the README

## 0.2.0

- read the name from the command line
- default to world when it is missing

## 0.1.1

- move the greeting into a function
- add a blank line before the output

## 0.1.0

- first commit
- print hello, world
`,
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
