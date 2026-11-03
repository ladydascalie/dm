package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Msg is the single envelope for every dm socket, in both directions.
type Msg struct {
	Type    string  `json:"type"`             // req resp attach keys resize snapshot bytes replay status exit
	Action  string  `json:"action,omitempty"` // req action
	Target  string  `json:"target,omitempty"` // pane id
	Dir     string  `json:"dir,omitempty"`    // left right up down
	Other   string  `json:"other,omitempty"`  // resize-split: pane on the other side
	Msg     string  `json:"msg,omitempty"`
	Enter   bool    `json:"enter,omitempty"`
	Name    string  `json:"name,omitempty"`
	Window  string  `json:"window,omitempty"`
	Index   int     `json:"index,omitempty"`
	ID      int     `json:"id,omitempty"`
	Cwd     string  `json:"cwd,omitempty"`
	Ratio   float64 `json:"ratio,omitempty"`
	NoFocus bool    `json:"nofocus,omitempty"`

	Cols int `json:"cols,omitempty"`
	Rows int `json:"rows,omitempty"`

	Keys []byte `json:"keys,omitempty"`
	Data []byte `json:"data,omitempty"`

	OK    bool      `json:"ok,omitempty"`
	Error string    `json:"error,omitempty"`
	Snap  *Snapshot `json:"snap,omitempty"`
	// Lines is the pane read reply. It stays in the envelope because pane
	// daemons outlive servers, and moving it would break every old pane.
	Lines []string `json:"lines,omitempty"`
	// Count is the history request: how many rows above the screen.
	Count int             `json:"count,omitempty"`
	St    *Status         `json:"st,omitempty"`
	Res   json.RawMessage `json:"res,omitempty"`
}

type Status struct {
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
}

type Rect struct {
	X, Y, W, H int
}

type PaneInfo struct {
	ID      string `json:"id"`
	Status  string `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
	Title   string `json:"title,omitempty"`
	Name    string `json:"name,omitempty"`
	Rect    Rect   `json:"rect"`
	Dead    bool   `json:"dead,omitempty"`
	Restart bool   `json:"restart,omitempty"`
	// Alt is true when the pane's program is on the alternate screen, so the
	// terminal has no scrollback for it and the wheel belongs to the program.
	Alt bool `json:"alt,omitempty"`
	// Mouse is true when the program asked for mouse reporting.
	Mouse bool `json:"mouse,omitempty"`
}

// AllPane is one pane anywhere in the session, for a sidebar that lists the
// whole fleet. Panes stays the active window; All is everything, grouped by
// workspace then window in tree order.
type AllPane struct {
	ID              string `json:"id"`
	Name            string `json:"name,omitempty"`
	Status          string `json:"status,omitempty"`
	Message         string `json:"message,omitempty"`
	Agent           bool   `json:"agent"`
	Workspace       string `json:"workspace"`
	WorkspaceName   string `json:"workspaceName"`
	Window          string `json:"window"`
	WindowName      string `json:"windowName"`
	Focus           bool   `json:"focus,omitempty"`
	WorkspaceActive bool   `json:"workspaceActive,omitempty"`
	WindowActive    bool   `json:"windowActive,omitempty"`
}

type Snapshot struct {
	Session      string     `json:"session"`
	Workspace    string     `json:"workspace"`
	Windows      []string   `json:"windows"`
	ActiveWindow int        `json:"activeWindow"`
	Panes        []PaneInfo `json:"panes"`
	// All lists every pane in the session; Panes is the active window only.
	All []AllPane `json:"all,omitempty"`
	// Rail is the effective sidebar width the server used. Zero hides it.
	Rail  int    `json:"rail"`
	Focus string `json:"focus"`
	Cols  int    `json:"cols"`
	Rows  int    `json:"rows"`
	Root  *Node  `json:"root"`
}

const maxFrame = 16 << 20

func writeMsg(w io.Writer, m Msg) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], uint32(len(b)))
	if _, err := w.Write(h[:]); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

func readMsg(r io.Reader) (Msg, error) {
	var m Msg
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return m, err
	}
	n := binary.BigEndian.Uint32(h[:])
	if n == 0 || n > maxFrame {
		return m, fmt.Errorf("bad frame length %d", n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, errors.New("bad json: " + err.Error())
	}
	return m, nil
}
