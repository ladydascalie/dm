package clidoc

import "dm/internal/configdoc"

// The layout surface: a named JSON document that holds a session's shape. The
// prose lives here once, so the man page, llms.txt and the html page say the
// same thing, and the snapshot tests keep them in step.

// LayoutsDir is where a layout file lives, in the form the pages print it.
const LayoutsDir = "$XDG_CONFIG_HOME/dm/layouts/<name>.json, or " +
	"$HOME/.config/dm/layouts/<name>.json when XDG_CONFIG_HOME is unset"

// LayoutsIntro is what a layout is and where it lives.
const LayoutsIntro = "A layout is a named JSON document that holds a session's " +
	"shape: its workspaces, their windows, the split trees and each pane's launch " +
	"data. A file lives at " + LayoutsDir + ". The name follows the session-name " +
	"rule: letters, digits, underscore or hyphen. dm layout save <name> captures " +
	"the shape: from the session's server when one answers, and from its " +
	"state.json when the session is stopped."

// LayoutsSeed is how a layout is applied: it seeds an empty session, an
// existing session wins, and --force replays the file over a reset.
const LayoutsSeed = "dm -L <name> starts a session from the file and attaches to " +
	"it. The session defaults to the layout name, so the same command twice " +
	"reattaches instead of stacking sessions. A layout seeds an empty session: " +
	"when the session already has state, that state wins and the file is not " +
	"applied, so a layout patches nothing. --force resets the session first, then " +
	"replays the file over the reset."

// LayoutsLimit is the one fidelity limit:
const LayoutsLimit = "Only the launch argv is durable. A pane that began as a " +
	"shell and now runs pi reopens as a shell, because the program a pane runs is " +
	"recorded at launch, not what it later became. The session's box, its secrets, " +
	"the agent state and the rail width never travel."

// LayoutsVerbs is the four verbs an operator needs.
const LayoutsVerbs = "dm layout save <name> [-s name] [--force] captures a " +
	"session, dm -L <name> applies one, and dm layout ls, show and rm list, print " +
	"and delete the files."

// LayoutsSample is one layout file: two panes in a column, the second running pi
// in the project's directory.
const LayoutsSample = `{
  "version": 1,
  "name": "go-backend",
  "workspaces": [{
    "name": "development",
    "project": "go-backend",
    "active": 0,
    "windows": [{
      "name": "api",
      "focus": "w1p1",
      "root": {
        "dir": "col",
        "sizes": [0.6, 0.4],
        "kids": [{"pane": "w1p1"}, {"pane": "w1p2"}]
      }
    }]
  }],
  "panes": {
    "w1p1": {"cwd": "/home/you/src/go-backend", "argv": ["pi"]},
    "w1p2": {"cwd": "/home/you/src/go-backend"}
  }
}`

// LayoutsFile is the layout file row the FILES sections carry. The rest of that
// table comes from configdoc, which owns the session and config paths; a layout
// belongs to this surface, so its row lives beside this prose.
var LayoutsFile = configdoc.File{
	Path:  "$XDG_CONFIG_HOME/dm/layouts/<name>.json",
	Desc:  "A captured layout: workspaces, windows, split trees and each pane's launch cwd and argv. Written by dm layout save.",
	Write: true,
}

// docsFiles is every file the pages document: the configdoc table with the
// layout file beside it.
func docsFiles() []configdoc.File {
	out := make([]configdoc.File, 0, len(configdoc.Files)+1)
	out = append(out, configdoc.Files...)
	return append(out, LayoutsFile)
}
