package lesson

// Tracks are the tour's content. Every step here stays inside what the browser
// build really does: a pretend shell over one project, and a real client with
// splits, zoom, swap, the rail and the layout presets.

// Tracks is every track the tour offers, in order.
var Tracks = []Track{
	{
		ID:    "basics",
		Title: "The basics",
		Steps: []Step{
			{
				Title: "Run a command",
				Note:  "Type it and press enter. The pane is a pretend shell, but the keys are real.",
				Keys:  []string{"git status", "enter"},
				Done:  Rule{Command: "git status"},
				Hint:  "type git status, then press enter.",
			},
			{
				Title: "Split the window",
				Note:  "The prefix works from any mode: ctrl+b, let go, then the key.",
				Keys:  []string{"ctrl+b", "|"},
				Done:  Rule{Action: "split-right"},
				Hint:  "ctrl+b, then |.",
			},
			{
				Title: "Move the focus",
				Note:  "Focus walks to the pane on the left. The bright frame follows it.",
				Keys:  []string{"ctrl+b", "h"},
				Done:  Rule{Action: "focus-left"},
				Hint:  "ctrl+b, then h.",
			},
			{
				Title: "Zoom it",
				Note:  "A zoomed pane covers the tiling and the rail still lists the rest.",
				Keys:  []string{"ctrl+b", "z"},
				Done:  Rule{Action: "zoom-pane"},
				Hint:  "ctrl+b, then z.",
			},
			{
				Title: "Put it back",
				Note:  "The same keys unzoom, and the two panes come back where they were.",
				Keys:  []string{"ctrl+b", "z"},
				Done:  Rule{Action: "zoom-pane"},
				Hint:  "ctrl+b, then z again.",
			},
		},
	},
}
