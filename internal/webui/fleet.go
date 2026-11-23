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
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	App           string `json:"app,omitempty"`
	Status        string `json:"status,omitempty"`
	Message       string `json:"message,omitempty"`
	Agent         bool   `json:"agent,omitempty"`
	Workspace     string `json:"workspace"`
	WorkspaceName string `json:"workspaceName"`
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
	// Cols and Rows are the active window's grid, the frame every Rect above
	// is measured in.
	Cols  int    `json:"cols,omitempty"`
	Rows  int    `json:"rows,omitempty"`
	Error string `json:"error,omitempty"`
}

// fleet collects one dashboard payload: the session's panes, then its box.
// Each half answers on its own, so a box the daemon refuses still leaves the
// fleet visible.
func (h *Handler) fleet(w http.ResponseWriter, r *http.Request) {
	out := fleetReply{Session: h.cfg.Session, ReadOnly: h.cfg.ReadOnly, Panes: []fleetPane{}, Box: []fleetBox{}}
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
	out.Cols, out.Rows = res.Snapshot.Cols, res.Snapshot.Rows
	for _, p := range res.Snapshot.All {
		out.Panes = append(out.Panes, fleetPane{
			ID: p.ID, Name: p.Name, App: p.App, Status: p.Status, Message: p.Message,
			Agent: p.Agent, Workspace: p.Workspace, WorkspaceName: p.WorkspaceName,
			Window: p.Window, WindowName: p.WindowName, Cwd: p.Cwd, Branch: p.Branch,
			Dirty: p.Dirty, Added: p.Added, Deleted: p.Deleted, Focus: p.Focus,
			Active: active[p.ID], Rect: rects[p.ID],
		})
	}
	return nil
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
