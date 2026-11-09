// Package wire is the dm socket wire contract: the message envelope, the
// snapshot it carries and the length-prefixed frame both sides speak.
package wire

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"dm/internal/layout"
	"dm/internal/vt"
)

// Msg is the single envelope for every dm socket, in both directions.
type Msg struct {
	Type   string `json:"type"`             // req resp attach keys resize snapshot bytes replay status exit
	Action string `json:"action,omitempty"` // req action
	Target string `json:"target,omitempty"` // pane id
	Dir    string `json:"dir,omitempty"`    // left right up down
	Other  string `json:"other,omitempty"`  // resize-split: pane on the other side
	Msg    string `json:"msg,omitempty"`
	// Project is the explicit project name on new-workspace. Empty means the
	// new workspace inherits the project of the one it was created from.
	Project string  `json:"project,omitempty"`
	Enter   bool    `json:"enter,omitempty"`
	Name    string  `json:"name,omitempty"`
	Window  string  `json:"window,omitempty"`
	Index   int     `json:"index,omitempty"`
	ID      int     `json:"id,omitempty"`
	Cwd     string  `json:"cwd,omitempty"`
	FG      string  `json:"fg,omitempty"` // terminal foreground the client detected
	BG      string  `json:"bg,omitempty"` // terminal background the client detected
	Ratio   float64 `json:"ratio,omitempty"`
	NoFocus bool    `json:"nofocus,omitempty"`

	Cols int `json:"cols,omitempty"`
	Rows int `json:"rows,omitempty"`

	Keys []byte `json:"keys,omitempty"`
	Data []byte `json:"data,omitempty"`
	// VTSnap is a pane's visible terminal state. It travels beside Data:
	// Data stays the replay action's raw ring, while a screen reply carries
	// the snapshot. The server passes it through untouched.
	VTSnap *vt.Snapshot `json:"vtsnap,omitempty"`

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
	// App is the reporting program from app= in the wire format. It is display
	// only: the rail uses it as a label when the pane has no recorded name.
	App string `json:"app,omitempty"`
}

type PaneInfo struct {
	ID      string      `json:"id"`
	Status  string      `json:"status,omitempty"`
	Message string      `json:"message,omitempty"`
	Title   string      `json:"title,omitempty"`
	Name    string      `json:"name,omitempty"`
	Rect    layout.Rect `json:"rect"`
	Dead    bool        `json:"dead,omitempty"`
	Restart bool        `json:"restart,omitempty"`
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
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Status  string `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
	Agent   bool   `json:"agent"`
	App     string `json:"app,omitempty"`
	Branch  string `json:"branch,omitempty"`
	// Cwd is the pane's working directory. Root is the repository's toplevel
	// for that directory, empty outside git.
	Cwd  string `json:"cwd,omitempty"`
	Root string `json:"root,omitempty"`
	// Worktree is the linked worktree's folder name; empty for the main
	// checkout or a directory outside git.
	Worktree string `json:"worktree,omitempty"`
	// InRepo is true when the pane's directory is inside a git repository, so
	// the rail can tell a clean tree from a directory git never saw.
	InRepo bool `json:"inRepo,omitempty"`
	// Dirty is true when a tracked file has an uncommitted change. Added and
	// Deleted count the lines the working tree gains and loses against HEAD.
	Dirty   bool `json:"dirty,omitempty"`
	Added   int  `json:"added,omitempty"`
	Deleted int  `json:"deleted,omitempty"`
	// WindowUnfolded is the rail fold state of the pane's window.
	WindowUnfolded bool `json:"windowUnfolded,omitempty"`
	// WorkspaceUnfolded is the rail fold state of the pane's workspace.
	WorkspaceUnfolded bool   `json:"workspaceUnfolded,omitempty"`
	Workspace         string `json:"workspace"`
	WorkspaceName     string `json:"workspaceName"`
	// Project is the pane's workspace project name. Empty means no project.
	Project    string `json:"project,omitempty"`
	Window     string `json:"window"`
	WindowName string `json:"windowName"`
	// WindowNamed is true when the user set the window's name with
	// rename-window.
	WindowNamed     bool `json:"windowNamed,omitempty"`
	Focus           bool `json:"focus,omitempty"`
	WorkspaceActive bool `json:"workspaceActive,omitempty"`
	WindowActive    bool `json:"windowActive,omitempty"`
}

// WindowInfo is one window in the active workspace: its id, its name and
// whether the user named it.
type WindowInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Named bool   `json:"named,omitempty"`
}

type Snapshot struct {
	Session   string       `json:"session"`
	Workspace string       `json:"workspace"`
	Windows   []WindowInfo `json:"windows"`
	// WindowLabel is the client's window label template. The server leaves it
	// empty; the client sets it from config before drawing.
	WindowLabel  string     `json:"-"`
	ActiveWindow int        `json:"activeWindow"`
	Panes        []PaneInfo `json:"panes"`
	// All lists every pane in the session; Panes is the active window only.
	All []AllPane `json:"all,omitempty"`
	// Rail is the effective sidebar width the server used. RailWant is the
	// requested width before the screen clamp; zero hides the rail.
	Rail     int    `json:"rail"`
	RailWant int    `json:"railWant"`
	Focus    string `json:"focus"`
	// Zoom is the zoomed pane id, empty when no pane is zoomed. Panes still
	// carry their layout rectangles: the rail and the switcher read them.
	Zoom string `json:"zoom,omitempty"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
	// Pad is the pane padding the server laid the layout out with: the blank
	// cells between a pane's border and its content. The server owns the
	// value and the client draws and hit-tests with it.
	Pad  int          `json:"pad"`
	Root *layout.Node `json:"root"`
}

// MaxFrame is the largest frame the socket accepts, four bytes of length plus
// the payload.
const MaxFrame = 16 << 20

// WriteMsg writes m to w as a big-endian length prefix followed by its JSON.
func WriteMsg(w io.Writer, m Msg) error {
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

// ReadMsg reads one framed message from r.
func ReadMsg(r io.Reader) (Msg, error) {
	var m Msg
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return m, err
	}
	n := binary.BigEndian.Uint32(h[:])
	if n == 0 || n > MaxFrame {
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
