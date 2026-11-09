package clidoc

// actionExample is one dm exec body: the action name and a minimal JSON object
// that carries only the fields that action needs.
type actionExample struct {
	name    string
	example string
}

// actionExamples is the reference sample for every action in manActionNames,
// in the same order the man page lists them. The ids are the ones dm hands out
// in a fresh session: w1p1 for a pane, w1 for a window, s1 for a workspace, and
// w1p1/w1p2 for a split pair. The reply fields are left out, and so is any
// field the action reads with a working default. keys and data hold raw bytes,
// so their JSON is a base64 string: "Aw==" is ctrl+c.
var actionExamples = []actionExample{
	{"ping", `{"action":"ping"}`},
	{"list", `{"action":"list"}`},
	{"read", `{"action":"read","target":"w1p1"}`},
	{"history", `{"action":"history","target":"w1p1","count":200}`},
	{"replay", `{"action":"replay","target":"w1p1"}`},
	{"status", `{"action":"status","target":"w1p1"}`},

	{"write-to", `{"action":"write-to","target":"w1p1","msg":"go test ./...","enter":true}`},
	{"exec", `{"action":"exec","target":"w1p1","msg":"go test ./..."}`},
	{"keys", `{"action":"keys","target":"w1p1","keys":"Aw=="}`},

	{"split", `{"action":"split","target":"w1p1","dir":"right"}`},
	{"zoom-pane", `{"action":"zoom-pane"}`},
	{"set-rail", `{"action":"set-rail","index":30}`},
	{"resize-split", `{"action":"resize-split","target":"w1p1","other":"w1p2","ratio":0.7}`},

	{"focus", `{"action":"focus","target":"w1p2"}`},
	{"focus-dir", `{"action":"focus-dir","dir":"left"}`},
	{"next-pane", `{"action":"next-pane"}`},
	{"prev-pane", `{"action":"prev-pane"}`},
	{"close-pane", `{"action":"close-pane","target":"w1p1"}`},

	{"close-window", `{"action":"close-window","target":"w1"}`},
	{"new-window", `{"action":"new-window","target":"s1","name":"build"}`},
	{"next-window", `{"action":"next-window"}`},
	{"prev-window", `{"action":"prev-window"}`},
	{"select-window", `{"action":"select-window","index":2}`},

	{"new-workspace", `{"action":"new-workspace","name":"build"}`},
	{"next-workspace", `{"action":"next-workspace"}`},
	{"prev-workspace", `{"action":"prev-workspace"}`},
	{"list-workspaces", `{"action":"list-workspaces"}`},

	{"list-panes", `{"action":"list-panes"}`},
	{"pane", `{"action":"pane","target":"w1p1"}`},
	{"toggle-fold", `{"action":"toggle-fold","target":"w1"}`},
	{"set-pane-name", `{"action":"set-pane-name","target":"w1p1","name":"tests"}`},

	{"focus-window", `{"action":"focus-window","target":"w1"}`},
	{"focus-workspace", `{"action":"focus-workspace","target":"s1"}`},
	{"close-workspace", `{"action":"close-workspace","target":"s1"}`},

	{"rename-window", `{"action":"rename-window","target":"w1","name":"build"}`},
	{"rename-workspace", `{"action":"rename-workspace","target":"s1","name":"build"}`},

	{"msg-post", `{"action":"msg-post","res":{"key":"build","state":"done","msg":"all green"}}`},
	{"msg-list", `{"action":"msg-list","res":{"key":"build","limit":20}}`},
	{"msg-clear", `{"action":"msg-clear","res":{"key":"build"}}`},
	{"msg-wait", `{"action":"msg-wait","res":{"key":"build","state":"done","timeoutMs":60000}}`},
	{"msg-sub", `{"action":"msg-sub","res":{"keyPrefix":"lane/","afterSeq":0}}`},
	{"msg-watchers", `{"action":"msg-watchers","res":{}}`},
	{"msg-schema", `{"action":"msg-schema","res":{}}`},
}

// actionExampleOf returns the example body for an action name.
func actionExampleOf(name string) (string, bool) {
	for _, e := range actionExamples {
		if e.name == name {
			return e.example, true
		}
	}
	return "", false
}
