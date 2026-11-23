package webui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"dm/internal/ask"
	"dm/internal/box"
	"dm/internal/wire"
)

// The phone runs dm commands through the server action the CLI already sends.
// The catalogue is one table here, so the page builds its menus from the same
// rows this file runs. No row invents an action: every id is the action name.

// group names the menu section a command sits in.
const (
	groupPanes      = "panes"
	groupWindows    = "windows"
	groupWorkspaces = "workspaces"
	groupBox        = "box"
	groupAsks       = "asks"
)

// targetKind is what a command's target names. Empty means the row takes none,
// so a target on it is a refusal rather than a dropped field.
type targetKind string

const (
	targetNone      targetKind = ""
	targetPane      targetKind = "pane"
	targetWindow    targetKind = "window"
	targetWorkspace targetKind = "workspace"
	targetAsk       targetKind = "ask"
)

// sites are the directions a split takes, the values of the request's site
// field and of wire.Msg.Dir.
var sites = []string{"right", "left", "up", "down"}

// layouts are the preset names select-layout accepts. They are published here
// so the page's menu comes from the server's list rather than a copy in the
// script; the daemon stays the judge of the value.
var layouts = []string{"even-horizontal", "even-vertical", "main-horizontal", "main-vertical", "tiled"}

// command is one catalogue row. Fields names the request fields the row reads,
// so a field outside the list is refused before it reaches the socket.
type command struct {
	ID      string     `json:"id"`
	Label   string     `json:"label"`
	Group   string     `json:"group"`
	Target  targetKind `json:"target,omitempty"`
	Fields  []string   `json:"fields,omitempty"`
	Sites   []string   `json:"sites,omitempty"`
	Layouts []string   `json:"layouts,omitempty"`
	// apply maps the request fields onto the wire message. The target is set
	// from the row's kind before it runs, so a row that takes none can never
	// send one.
	apply func(*wire.Msg, actionReq) error
}

// commands is the phone's whole command surface. The order is the menu order:
// the rows that change a pane come first, then the window and workspace moves,
// then the box.
var commands = []command{
	{ID: "split", Label: "split", Group: groupPanes, Target: targetPane,
		Fields: []string{"target", "site"}, Sites: sites,
		apply: func(m *wire.Msg, q actionReq) error {
			m.Dir = "right"
			if q.site != "" {
				m.Dir = q.site
			}
			// NoFocus stays false: the page wants the split to take the focus,
			// which is the action's own default.
			return nil
		}},
	{ID: "zoom-pane", Label: "zoom pane", Group: groupPanes},
	{ID: "close-pane", Label: "close pane", Group: groupPanes, Target: targetPane,
		Fields: []string{"target"}},
	{ID: "focus", Label: "focus pane", Group: groupPanes, Target: targetPane,
		Fields: []string{"target"}},
	{ID: "next-pane", Label: "next pane", Group: groupPanes},
	{ID: "prev-pane", Label: "previous pane", Group: groupPanes},
	{ID: "set-pane-name", Label: "rename pane", Group: groupPanes, Target: targetPane,
		Fields: []string{"target", "name"},
		apply: func(m *wire.Msg, q actionReq) error {
			m.Name = q.name
			return nil
		}},
	{ID: "select-layout", Label: "select layout", Group: groupPanes,
		Fields: []string{"layout"}, Layouts: layouts,
		apply: func(m *wire.Msg, q actionReq) error {
			m.Layout = q.layout
			return nil
		}},

	{ID: "new-window", Label: "new window", Group: groupWindows,
		Fields: []string{"cwd"},
		apply: func(m *wire.Msg, q actionReq) error {
			m.Cwd = q.cwd
			return nil
		}},
	{ID: "close-window", Label: "close window", Group: groupWindows, Target: targetWindow,
		Fields: []string{"target"}},
	{ID: "focus-window", Label: "focus window", Group: groupWindows, Target: targetWindow,
		Fields: []string{"target"}},
	{ID: "rename-window", Label: "rename window", Group: groupWindows, Target: targetWindow,
		Fields: []string{"target", "name"},
		apply: func(m *wire.Msg, q actionReq) error {
			m.Name = q.name
			return nil
		}},
	{ID: "next-window", Label: "next window", Group: groupWindows},
	{ID: "prev-window", Label: "previous window", Group: groupWindows},

	{ID: "new-workspace", Label: "new workspace", Group: groupWorkspaces,
		Fields: []string{"name", "cwd"},
		apply: func(m *wire.Msg, q actionReq) error {
			m.Name = q.name
			m.Cwd = q.cwd
			return nil
		}},
	{ID: "focus-workspace", Label: "focus workspace", Group: groupWorkspaces, Target: targetWorkspace,
		Fields: []string{"target"}},
	{ID: "next-workspace", Label: "next workspace", Group: groupWorkspaces},
	{ID: "prev-workspace", Label: "previous workspace", Group: groupWorkspaces},

	{ID: "msg-post", Label: "post to the box", Group: groupBox,
		Fields: []string{"key", "state", "msg"},
		apply: func(m *wire.Msg, q actionReq) error {
			// The phone is not a pane, so from stays empty and the box records
			// no origin pane. The daemon's own decoder reads this payload, the
			// way it reads the fleet's list request.
			b, err := json.Marshal(box.PostReq{Key: q.key, State: box.State(q.state), Msg: q.msg})
			if err != nil {
				return err
			}
			m.Res = b
			return nil
		}},

	// ask-answer takes the whole answer body as one object, because an answer
	// is a list of items and the endpoint's other fields are strings. The
	// daemon's own decoder reads this payload, so the form is checked against
	// the contract the store enforces before the socket sees it.
	{ID: "ask-answer", Label: "answer the ask", Group: groupAsks, Target: targetAsk,
		Fields: []string{"target", "answer"},
		apply: func(m *wire.Msg, q actionReq) error {
			if len(q.answer) == 0 {
				return fmt.Errorf("ask-answer needs an answer")
			}
			req, err := ask.ParseAnswer(q.answer)
			if err != nil {
				return err
			}
			b, err := json.Marshal(req)
			if err != nil {
				return err
			}
			m.Res = b
			return nil
		}},
}

// catalogue is the /api/commands payload: the rows, one per command.
type catalogueReply struct {
	Commands []command `json:"commands"`
}

// commands serves the catalogue the page builds its menus from. It refuses in
// read-only mode for the same reason the action endpoint does: a menu of
// commands that cannot run is a lie on the screen.
func (h *Handler) commands(w http.ResponseWriter, r *http.Request) {
	if h.cfg.ReadOnly {
		http.Error(w, "dm web: read only", http.StatusForbidden)
		return
	}
	writeJSON(w, http.StatusOK, catalogueReply{Commands: commands})
}

// actionReq is one POST /api/action body. Every field is a string, and an
// absent field is the empty one: a field the row names is read, and a field it
// does not name is refused whether or not it carries a value.
type actionReq struct {
	id     string
	target string
	name   string
	layout string
	msg    string
	state  string
	key    string
	cwd    string
	site   string
	// answer is the ask-answer body: one object, kept raw because it is the
	// daemon's shape and not a field of the endpoint's own.
	answer json.RawMessage
}

// decodeAction reads one request body. The decoder refuses a field outside the
// ten the endpoint publishes, and each row then refuses a field it does not
// name, so a typo is the error it looks like.
func decodeAction(r *http.Request) (actionReq, map[string]bool, error) {
	var raw struct {
		ID     *string          `json:"id"`
		Target *string          `json:"target"`
		Name   *string          `json:"name"`
		Layout *string          `json:"layout"`
		Msg    *string          `json:"msg"`
		State  *string          `json:"state"`
		Key    *string          `json:"key"`
		Cwd    *string          `json:"cwd"`
		Site   *string          `json:"site"`
		Answer *json.RawMessage `json:"answer"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return actionReq{}, nil, err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return actionReq{}, nil, fmt.Errorf("trailing data after the request")
	}
	q := actionReq{}
	present := map[string]bool{}
	set := func(name string, p *string, into *string) {
		if p != nil {
			present[name] = true
			*into = *p
		}
	}
	set("id", raw.ID, &q.id)
	set("target", raw.Target, &q.target)
	set("name", raw.Name, &q.name)
	set("layout", raw.Layout, &q.layout)
	set("msg", raw.Msg, &q.msg)
	set("state", raw.State, &q.state)
	set("key", raw.Key, &q.key)
	set("cwd", raw.Cwd, &q.cwd)
	set("site", raw.Site, &q.site)
	if raw.Answer != nil {
		present["answer"] = true
		q.answer = *raw.Answer
	}
	return q, present, nil
}

// action runs one catalogue command through the session socket and answers the
// daemon's reply body, so the page can read a new pane id or a box entry.
func (h *Handler) action(w http.ResponseWriter, r *http.Request) {
	if h.cfg.ReadOnly {
		http.Error(w, "dm web: read only", http.StatusForbidden)
		return
	}
	q, present, err := decodeAction(r)
	if err != nil {
		http.Error(w, "dm web: "+err.Error(), http.StatusBadRequest)
		return
	}
	if q.id == "" {
		http.Error(w, "dm web: an action needs an id", http.StatusBadRequest)
		return
	}
	cmd, ok := commandByID(q.id)
	if !ok {
		http.Error(w, "dm web: unknown command "+q.id, http.StatusBadRequest)
		return
	}
	if err := cmd.checkFields(present); err != nil {
		http.Error(w, "dm web: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := cmd.checkTarget(q.target); err != nil {
		http.Error(w, "dm web: "+err.Error(), http.StatusBadRequest)
		return
	}
	if q.site != "" && !oneOf(q.site, sites) {
		http.Error(w, fmt.Sprintf("dm web: bad site %q: want %s", q.site, joinOr(sites)), http.StatusBadRequest)
		return
	}
	m := wire.Msg{Action: cmd.ID}
	if cmd.Target != targetNone {
		// An empty target is the daemon's to resolve: it names the focused
		// pane, window or workspace, and the page must not guess one.
		m.Target = q.target
	}
	if cmd.apply != nil {
		if err := cmd.apply(&m, q); err != nil {
			http.Error(w, "dm web: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	got, err := h.rpc(m)
	if err != nil {
		http.Error(w, "dm web: "+err.Error(), http.StatusBadGateway)
		return
	}
	if !got.OK {
		// The daemon's verdict is the page's error text, named as the daemon
		// named it.
		http.Error(w, "dm web: "+got.Error, http.StatusBadRequest)
		return
	}
	// A command that answers with nothing is still a success, so the page gets
	// one shape to read back rather than an empty body or a null.
	if len(got.Res) == 0 || string(got.Res) == "null" {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	writeJSON(w, http.StatusOK, got.Res)
}

// commandByID finds one catalogue row.
func commandByID(id string) (command, bool) {
	for _, c := range commands {
		if c.ID == id {
			return c, true
		}
	}
	return command{}, false
}

// checkFields refuses a request field the row does not name. The id names the
// row, so it is never refused; every other field must be on the row's list.
func (c command) checkFields(present map[string]bool) error {
	for name := range present {
		if name == "id" {
			continue
		}
		if !oneOf(name, c.Fields) {
			return fmt.Errorf("%s takes no %s", c.ID, name)
		}
	}
	return nil
}

// checkTarget checks the id shape of the row's target kind. The pane pattern is
// the one the pane view already uses; one check covers a window id and a
// workspace id, and the prefix letter must match the kind.
func (c command) checkTarget(id string) error {
	if c.Target == targetNone || id == "" {
		return nil
	}
	switch c.Target {
	case targetPane:
		if !paneIDPattern.MatchString(id) {
			return fmt.Errorf("bad pane id %q", id)
		}
	case targetAsk:
		if !askIDPattern.MatchString(id) {
			return fmt.Errorf("bad ask id %q", id)
		}
	case targetWindow, targetWorkspace:
		m := windowOrWorkspaceID.FindStringSubmatch(id)
		if m == nil {
			return fmt.Errorf("bad %s id %q", c.Target, id)
		}
		want := "w"
		if c.Target == targetWorkspace {
			want = "s"
		}
		if m[1] != want {
			return fmt.Errorf("%q is not a %s id", id, c.Target)
		}
	}
	return nil
}

// windowOrWorkspaceID is the shape of a window or workspace id: w1, w12, s1.
var windowOrWorkspaceID = regexp.MustCompile(`^([ws])[0-9]+$`)

// askIDPattern is the shape the store gives an ask: a1, a12. The counter only
// moves forward, so an id names one ask for the life of the session.
var askIDPattern = regexp.MustCompile(`^a[0-9]+$`)

// oneOf reports whether v is in list.
func oneOf(v string, list []string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// joinOr renders a list for an error message: "right, left, up or down".
func joinOr(list []string) string {
	if len(list) < 2 {
		return strings.Join(list, "")
	}
	return strings.Join(list[:len(list)-1], ", ") + " or " + list[len(list)-1]
}
