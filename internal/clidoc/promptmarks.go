package clidoc

// The prompt-mark section is shell integration the user installs, not part of
// dm's own surface, so it lives beside the tables rather than inside one. The
// man page, the HTML page and llms.txt render these strings, and none of them
// invents its own wording.

// PromptMark is one OSC 133 mark dm reads: the letter in the sequence and what
// it tells dm.
type PromptMark struct {
	Letter  string
	Meaning string
}

// PromptMarks are the marks a yank uses. A and B come from the shell's prompt,
// C and D bracket the command's output.
var PromptMarks = []PromptMark{
	{"A", "the prompt starts. Without a D mark, this is where the output ends"},
	{"C", "the command starts, and its output begins here. The copy starts here"},
	{"D", "the command ended. The copy ends here"},
}

// PromptMarkHook is the zsh hook that emits the marks. zsh only: another shell
// needs the same three prints around its own prompt and command hooks.
const PromptMarkHook = `precmd() { print -Pn '\e]133;D\e\\\e]133;A\e\\' }; preexec() { print -Pn '\e]133;C\e\\' }`

// PromptMarkSpec is the proposal the marks come from.
const PromptMarkSpec = "https://gitlab.freedesktop.org/Per_Bothner/specifications/blob/master/proposals/semantic-prompts.md"

// PromptMarkIntro says what the marks are for.
const PromptMarkIntro = "dm copies a command's output with prefix y and dm yank. The Enter dm forwards marks where that output starts; the shell's OSC 133 marks make both edges exact. Without them the copy starts after the echoed command line and ends at the last row with content, so the trailing prompt is copied too. With them the copy is the command's output alone, even when the prompt repaints its line on Enter."

// PromptMarkLine is the compact form llms.txt prints: the marks dm compares
// against, in one line. The man page and the HTML page table them instead.
const PromptMarkLine = "dm reads `133;A` (the prompt starts, and the output ends there when no D arrives), `133;C` (the command starts, and the copy starts here) and `133;D` (the command ended, and the copy ends here)."

// PromptMarkWhere says where the hook goes and how it reaches a remote host.
const PromptMarkWhere = "zsh emits the marks with one line in ~/.zshrc, before the prompt theme registers its own precmd hook:"

// PromptMarkLanes says what a pane needs for dm to see the marks.
const PromptMarkLanes = "The marks are ordinary output, so they travel over ssh and a nested multiplexer like any other bytes. A shell on a remote host needs the same line in its own start file; a pane running a program instead of a shell has no prompt and no marks. dm reads the marks per pane and needs no configuration of its own."
