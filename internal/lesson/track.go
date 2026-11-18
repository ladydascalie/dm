package lesson

// Tracks are the tour's content. Every step here stays inside what the browser
// build really does: a pretend shell over one project, and a real client with
// splits, zoom, swap, the rail, the layout presets, windows, workspaces and
// scrollback.

// Tracks is every track the tour offers, in order.
var Tracks = []Track{
	{
		ID:    "basics",
		Title: "The basics",
		Steps: []Step{
			{
				Title: "Run a command",
				Note:  "Type it and press enter. The pane is a pretend shell, but the keys are real.",
				Keys:  []Key{{Label: "git status", Bytes: "git status"}, {Label: "enter", Bytes: "\r"}},
				Done:  Rule{Command: "git status"},
				Hint:  "type git status, then press enter.",
			},
			{
				Title: "Split the window",
				Note:  "The prefix works from any mode: ctrl+b, let go, then the key.",
				Keys:  []Key{{Label: "ctrl+b", Bytes: "\x02"}, {Label: "|", Bytes: "|"}},
				Done:  Rule{Action: "split-right"},
				Hint:  "ctrl+b, then |.",
			},
			{
				Title: "Move the focus",
				Note:  "Focus walks to the pane on the left. The bright frame follows it.",
				Keys:  []Key{{Label: "ctrl+b", Bytes: "\x02"}, {Label: "h", Bytes: "h"}},
				Done:  Rule{Action: "focus-left"},
				Hint:  "ctrl+b, then h.",
			},
			{
				Title: "Zoom it",
				Note:  "A zoomed pane covers the tiling and the rail still lists the rest.",
				Keys:  []Key{{Label: "ctrl+b", Bytes: "\x02"}, {Label: "z", Bytes: "z"}},
				Done:  Rule{Action: "zoom-pane"},
				Hint:  "ctrl+b, then z.",
			},
			{
				Title: "Put it back",
				Note:  "The same keys unzoom, and the two panes come back where they were.",
				Keys:  []Key{{Label: "ctrl+b", Bytes: "\x02"}, {Label: "z", Bytes: "z"}},
				Done:  Rule{Action: "zoom-pane"},
				Hint:  "ctrl+b, then z again.",
			},
		},
	},
	{
		ID:    "windows",
		Title: "The wider session",
		Steps: []Step{
			{
				Title: "Open a window",
				Note:  "A window is a tab. It keeps its own panes, its own shell and its own layout.",
				Keys:  []Key{{Label: "ctrl+b", Bytes: "\x02"}, {Label: "c", Bytes: "c"}},
				Done:  Rule{Action: "new-window"},
				Hint:  "ctrl+b, then c.",
			},
			{
				Title: "Walk the windows",
				Note:  "w steps to the next window and wraps. The tab bar and the rail follow it.",
				Keys:  []Key{{Label: "ctrl+b", Bytes: "\x02"}, {Label: "w", Bytes: "w"}},
				Done:  Rule{Action: "next-window"},
				Hint:  "ctrl+b, then w.",
			},
			{
				Title: "Start a workspace",
				Note:  "A workspace groups windows. The rail lists both and the new one takes the focus.",
				Keys:  []Key{{Label: "ctrl+b", Bytes: "\x02"}, {Label: "t", Bytes: "t"}},
				Done:  Rule{Action: "new-workspace"},
				Hint:  "ctrl+b, then t.",
			},
			{
				Title: "Come back",
				Note:  "The workspace cycle wraps too, so one press returns to the first workspace.",
				Keys:  []Key{{Label: "ctrl+b", Bytes: "\x02"}, {Label: ")", Bytes: ")"}},
				Done:  Rule{Action: "next-workspace"},
				Hint:  "ctrl+b, then ) (shift and zero).",
			},
			{
				Title: "Print a long file",
				Note:  "The shell stands in ~/projects/hello, and this file is longer than the pane keeps.",
				Keys:  []Key{{Label: "cat CHANGELOG.md", Bytes: "cat CHANGELOG.md"}, {Label: "enter", Bytes: "\r"}},
				Done:  Rule{Command: "cat CHANGELOG.md"},
				Hint:  "type cat CHANGELOG.md, then enter.",
			},
			{
				Title: "Read it back",
				Note:  "Scrollback replays what scrolled off the top. k walks up, j comes back, and the wheel works too.",
				Keys:  []Key{{Label: "ctrl+b", Bytes: "\x02"}, {Label: "[", Bytes: "["}},
				Done:  Rule{Action: "scrollback"},
				Hint:  "ctrl+b, then [.",
			},
		},
	},
}
