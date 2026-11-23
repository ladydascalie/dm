package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"dm/internal/box"
	"dm/internal/transport"
	"dm/internal/wire"
)

// boxLimit is how many box rows the page shows. A phone scrolls a screen or
// two, and the newest rows are the ones worth waking up to.
const boxLimit = 40

// fleetPane is one pane, flattened for a row: the id to stream, the status to
// colour, and the folder facts the rail would show.
type fleetPane struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	App     string `json:"app,omitempty"`
	Status  string `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
	Agent   bool   `json:"agent,omitempty"`
	// HasSession is true when the pane reported an OSC 7502 session file. It
	// answers a different question than Agent: a pane can report a session
	// with no 7501 status, and an agent status with no session. The page picks
	// the transcript or the screen from this flag and never from Agent.
	HasSession    bool   `json:"hasSession,omitempty"`
	Workspace     string `json:"workspace"`
	WorkspaceName string `json:"workspaceName"`
	// Project is the workspace project the rail groups by, and ProjectAccent
	// its colour slot. Both come from the snapshot's AllPane.
	Project       string `json:"project,omitempty"`
	ProjectAccent int    `json:"projectAccent,omitempty"`
	Window        string `json:"window"`
	WindowName    string `json:"windowName"`
	Cwd           string `json:"cwd,omitempty"`
	Branch        string `json:"branch,omitempty"`
	Dirty         bool   `json:"dirty,omitempty"`
	Added         int    `json:"added,omitempty"`
	Deleted       int    `json:"deleted,omitempty"`
	Focus         bool   `json:"focus,omitempty"`
	// Active is true for a pane of the active window, which is the one an
	// attached client is drawing.
	Active bool `json:"active,omitempty"`
	// Rect is the pane's box in the active window's layout, so a renderer can
	// keep the arrangement the desk shows. It is zero for a pane of another
	// window or workspace.
	Rect fleetRect `json:"rect,omitempty"`
	// Ask is true when the session lists a question set on this pane, so a row
	// waiting on the reader cannot pass for a pane at work.
	Ask bool `json:"ask,omitempty"`
}

// fleetAsk is one question set the session lists, flattened for the page: the
// id the answer names, the pane that asked, and the questions with their
// options. It carries no answer field, because the snapshot publishes none, so
// a reply to one ask cannot reach another row.
type fleetAsk struct {
	ID        string             `json:"id"`
	Pane      string             `json:"pane"`
	At        time.Time          `json:"at"`
	Questions []fleetAskQuestion `json:"questions"`
}

// fleetAskQuestion is one question of a listed ask: its text, its header,
// whether it takes several picks, and the options on offer. An empty option
// list is a freeform question.
type fleetAskQuestion struct {
	Question    string           `json:"question"`
	Header      string           `json:"header"`
	MultiSelect bool             `json:"multiSelect,omitempty"`
	Options     []fleetAskOption `json:"options"`
}

// fleetAskOption is one choice, with the description and the preview the form
// shows once the reader asks for them.
type fleetAskOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Preview     string `json:"preview,omitempty"`
}

// fleetRect is one pane's box, in the session's cell coordinates.
type fleetRect struct {
	X int `json:"x,omitempty"`
	Y int `json:"y,omitempty"`
	W int `json:"w,omitempty"`
	H int `json:"h,omitempty"`
}

// fleetBox is one box entry the page lists.
type fleetBox struct {
	Seq   int       `json:"seq"`
	At    time.Time `json:"at"`
	From  string    `json:"from"`
	Key   string    `json:"key"`
	State string    `json:"state"`
	Msg   string    `json:"msg"`
}

// fleetReply is the whole dashboard payload. Error rides in the body, so a
// session that will not answer shows as a banner instead of an empty fleet.
type fleetReply struct {
	Session  string      `json:"session"`
	ReadOnly bool        `json:"readOnly"`
	Panes    []fleetPane `json:"panes"`
	Box      []fleetBox  `json:"box"`
	// Ask lists the question sets the snapshot carries, oldest first, so the
	// page draws an open one the moment it loads.
	Ask []fleetAsk `json:"ask,omitempty"`
	// Cols and Rows are the active window's grid, the frame every Rect above
	// is measured in.
	Cols int `json:"cols,omitempty"`
	Rows int `json:"rows,omitempty"`
	// MaxPictures is how many panes the picture stream carries, so the page
	// asks for exactly the set it paints. One place decides the limit.
	MaxPictures int    `json:"maxPictures"`
	Error       string `json:"error,omitempty"`
}

// fleet collects one dashboard payload: the session's panes, then its box.
// Each half answers on its own, so a box the daemon refuses still leaves the
// fleet visible.
func (h *Handler) fleet(w http.ResponseWriter, r *http.Request) {
	out := fleetReply{Session: h.cfg.Session, ReadOnly: h.cfg.ReadOnly, Panes: []fleetPane{}, Box: []fleetBox{}, Ask: []fleetAsk{}, MaxPictures: maxThumbs}
	if err := h.collectFleet(&out); err != nil {
		out.Error = err.Error()
	} else if err := h.collectBox(&out); err != nil && out.Error == "" {
		out.Error = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

// collectFleet fills the pane rows from the list action's snapshot.
func (h *Handler) collectFleet(out *fleetReply) error {
	m, err := h.rpc(wire.Msg{Action: "list"})
	if err != nil {
		return err
	}
	if !m.OK {
		return fmt.Errorf("%s", m.Error)
	}
	var res struct {
		Snapshot wire.Snapshot `json:"snapshot"`
	}
	if err := json.Unmarshal(m.Res, &res); err != nil {
		return fmt.Errorf("list reply: %w", err)
	}
	// Panes is the active window and All is the whole session. A row is active
	// when both name the same pane, so a phone knows which one is on screen at
	// the desk.
	active := make(map[string]bool, len(res.Snapshot.Panes))
	rects := make(map[string]fleetRect, len(res.Snapshot.Panes))
	for _, p := range res.Snapshot.Panes {
		active[p.ID] = true
		rects[p.ID] = fleetRect{X: p.Rect.X, Y: p.Rect.Y, W: p.Rect.W, H: p.Rect.H}
	}
	// The asks are collected before the rows, because a row carries whether
	// one of them belongs to its pane.
	asking := make(map[string]bool, len(res.Snapshot.Ask))
	for _, a := range res.Snapshot.Ask {
		asking[a.Pane] = true
		out.Ask = append(out.Ask, fleetAsk{
			ID: a.ID, Pane: a.Pane, At: a.At, Questions: fleetQuestions(a.Questions),
		})
	}
	out.Cols, out.Rows = res.Snapshot.Cols, res.Snapshot.Rows
	for _, p := range res.Snapshot.All {
		out.Panes = append(out.Panes, fleetPane{
			ID: p.ID, Name: p.Name, App: p.App, Status: p.Status, Message: p.Message,
			Agent: p.Agent, HasSession: p.SessionFile != "",
			Workspace: p.Workspace, WorkspaceName: p.WorkspaceName,
			Project: p.Project, ProjectAccent: p.ProjectAccent,
			Window: p.Window, WindowName: p.WindowName, Cwd: p.Cwd, Branch: p.Branch,
			Dirty: p.Dirty, Added: p.Added, Deleted: p.Deleted, Focus: p.Focus,
			Active: active[p.ID], Rect: rects[p.ID], Ask: asking[p.ID],
		})
	}
	return nil
}

// fleetQuestions flattens one ask's questions into the page's shape.
func fleetQuestions(qs []wire.AskQuestion) []fleetAskQuestion {
	if len(qs) == 0 {
		return []fleetAskQuestion{}
	}
	out := make([]fleetAskQuestion, len(qs))
	for i, q := range qs {
		out[i] = fleetAskQuestion{
			Question: q.Question, Header: q.Header, MultiSelect: q.MultiSelect,
			Options: fleetOptions(q.Options),
		}
	}
	return out
}

// fleetOptions flattens one question's options into the page's shape.
func fleetOptions(opts []wire.AskOption) []fleetAskOption {
	if len(opts) == 0 {
		return []fleetAskOption{}
	}
	out := make([]fleetAskOption, len(opts))
	for i, o := range opts {
		out[i] = fleetAskOption{Label: o.Label, Description: o.Description, Preview: o.Preview}
	}
	return out
}

// collectBox fills the message rows, newest first.
func (h *Handler) collectBox(out *fleetReply) error {
	body, err := json.Marshal(box.ListReq{Limit: boxLimit})
	if err != nil {
		return err
	}
	m, err := h.rpc(wire.Msg{Action: "msg-list", Res: body})
	if err != nil {
		return err
	}
	if !m.OK {
		return fmt.Errorf("%s", m.Error)
	}
	var res box.ListReply
	if err := json.Unmarshal(m.Res, &res); err != nil {
		return fmt.Errorf("box reply: %w", err)
	}
	for _, e := range res.Entries {
		out.Box = append(out.Box, fleetBox{
			Seq: e.Seq, At: e.At, From: e.From, Key: e.Key, State: string(e.State), Msg: e.Msg,
		})
	}
	return nil
}

// rpc sends one request to the session server.
func (h *Handler) rpc(m wire.Msg) (wire.Msg, error) {
	return transport.RPCAt(h.cfg.Root, h.cfg.Session, m)
}

// writeJSON writes v as the response body.
func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "dm web: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
