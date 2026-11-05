package session

import (
	"encoding/json"
	"log"
	"os"
	"strconv"

	"dm/internal/box"
	"dm/internal/layout"
	"dm/internal/paths"
)

type Window struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Root     *layout.Node `json:"root"`
	Focus    string       `json:"focus"`
	NextPane int          `json:"nextPane,omitempty"`
	// Unfolded shows the window's pane rows in the rail. It is inverted so the
	// zero value, which older state files carry, means the default: folded.
	Unfolded bool `json:"unfolded,omitempty"`
}

type Workspace struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Windows []*Window `json:"windows"`
	Active  int       `json:"active"`
}

type PaneState struct {
	Shell     string `json:"shell"`
	Cwd       string `json:"cwd"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
	Restarted bool   `json:"restarted,omitempty"`
	// Name is the agent name recorded inside the mux. Agents survives a
	// restarted client because this is persisted, not held in memory.
	Name string `json:"name,omitempty"`
	// Reported records that the pane spoke the status protocol at least once.
	// It keeps the sidebar calling the pane an agent after state=clear.
	Reported bool `json:"reported,omitempty"`
	// App is the last app= a status report named. It survives state=clear so
	// the rail can keep labelling a pi pane pi.
	App string `json:"app,omitempty"`
}

type State struct {
	Name       string                `json:"name"`
	Workspaces []*Workspace          `json:"workspaces"`
	Active     int                   `json:"active"`
	Cols       int                   `json:"cols"`
	Rows       int                   `json:"rows"`
	Panes      map[string]*PaneState `json:"panes"`

	// Counters for the addressable id scheme. A pane id is its window id plus
	// p and a per-window sequence: w1p1 is window 1, pane 1.
	NextWorkspace int `json:"nextWorkspace,omitempty"`
	NextWindow    int `json:"nextWindow,omitempty"`
	IDVersion     int `json:"idVersion,omitempty"`

	// Rail is the requested sidebar width. RailSet tells an explicit 0 (hidden)
	// from a state written before the rail existed.
	Rail    int  `json:"rail,omitempty"`
	RailSet bool `json:"railSet,omitempty"`

	// Box is the session's message box: an append-only log any pane can post
	// into. BoxSeq is the last seq handed out. It never resets, so a clear
	// leaves a gap instead of reusing a number. A state file written before the
	// box existed loads with both zero, an empty box.
	Box    []box.Entry `json:"box,omitempty"`
	BoxSeq int         `json:"boxSeq,omitempty"`
}

// numericName reports whether a window label is a plain number, the old
// positional default.
func numericName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func NewState(name string) *State {
	cwd, _ := os.Getwd()
	defWin := paths.FolderName(cwd)
	if defWin == "" {
		defWin = "1"
	}
	st := &State{
		Name:          name,
		Cols:          80,
		Rows:          24,
		Panes:         map[string]*PaneState{},
		NextWorkspace: 1,
		NextWindow:    1,
		IDVersion:     2,
		Rail:          layout.DefaultRail,
		RailSet:       true,
		Workspaces: []*Workspace{{
			ID:   "s1",
			Name: "main",
			Windows: []*Window{{
				ID:   "w1",
				Name: defWin,
			}},
		}},
	}
	return st
}

func (st *State) Save() error {
	if err := os.MkdirAll(paths.SessionDir(st.Name), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := paths.StatePath(st.Name) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, paths.StatePath(st.Name))
}

func LoadState(name string) (*State, error) {
	b, err := os.ReadFile(paths.StatePath(name))
	if err != nil {
		return nil, err
	}
	st := &State{}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, err
	}
	if st.Panes == nil {
		st.Panes = map[string]*PaneState{}
	}
	st.Name = name
	if n := st.dropBadBoxEntries(); n > 0 {
		log.Printf("box: dropped %d invalid entries on load", n)
	}
	st.TrimBox()
	for _, e := range st.Box {
		if e.Seq > st.BoxSeq {
			st.BoxSeq = e.Seq
		}
	}
	changed := false
	if !st.RailSet {
		st.Rail = layout.DefaultRail
		st.RailSet = true
		changed = true
	}
	if st.IDVersion < 2 {
		st.MigrateIDs()
		st.IDVersion = 2
		changed = true
	}
	// Windows used to be named by position. Rename the unchanged numeric ones
	// after the folder they start in, which is what a new window gets now.
	for _, ws := range st.Workspaces {
		for _, w := range ws.Windows {
			if !numericName(w.Name) {
				continue
			}
			ps := st.Panes[w.Focus]
			if ps == nil {
				continue
			}
			if name := paths.FolderName(ps.Cwd); name != "" && name != w.Name {
				w.Name = name
				changed = true
			}
		}
	}
	if changed {
		_ = st.Save()
	}
	return st, nil
}

// dropBadBoxEntries removes entries that fail the box contract and reports how
// many went, so LoadState can name them in one log line. A hand-edited or
// truncated state file costs its bad entries, not the whole box.
func (st *State) dropBadBoxEntries() int {
	if len(st.Box) == 0 {
		return 0
	}
	kept := st.Box[:0]
	for _, e := range st.Box {
		if e.Validate() == nil {
			kept = append(kept, e)
		}
	}
	bad := len(st.Box) - len(kept)
	if bad > 0 {
		for i := len(kept); i < len(st.Box); i++ {
			st.Box[i] = box.Entry{}
		}
	}
	st.Box = kept
	return bad
}

// TrimBox drops the oldest entries until the log is inside both caps. The byte
// measure is the compact JSON of the entries, so the cap bounds the log itself;
// the state file's indentation is the file's, not the box's.
func (st *State) TrimBox() {
	kept := st.Box
	if len(kept) > box.MaxEntries {
		kept = kept[len(kept)-box.MaxEntries:]
	}
	for len(kept) > 0 {
		b, err := json.Marshal(kept)
		if err != nil || len(b) <= box.MaxBytes {
			break
		}
		kept = kept[1:]
	}
	if len(kept) != len(st.Box) {
		kept = append([]box.Entry(nil), kept...)
	}
	if len(kept) == 0 {
		kept = nil
	}
	st.Box = kept
}

func (st *State) ActiveWorkspace() *Workspace {
	if st.Active < 0 || st.Active >= len(st.Workspaces) {
		st.Active = 0
	}
	return st.Workspaces[st.Active]
}

func (st *State) ActiveWindow() *Window {
	ws := st.ActiveWorkspace()
	if ws.Active < 0 || ws.Active >= len(ws.Windows) {
		ws.Active = 0
	}
	return ws.Windows[ws.Active]
}

// Identifiers are addressable. A window is w<N>; a pane in it is w<N>p<M>,
// with M a per-window sequence that only grows. A workspace is s<N>. The id is
// therefore readable and constructible (w1p1), stable while the object lives,
// and never reused after a close because the counters only move forward.
func (st *State) NewWorkspaceID() string {
	st.NextWorkspace++
	return "s" + strconv.Itoa(st.NextWorkspace)
}

func (st *State) NewWindowID() string {
	st.NextWindow++
	return "w" + strconv.Itoa(st.NextWindow)
}

func (w *Window) NewPaneID() string {
	w.NextPane++
	return w.ID + "p" + strconv.Itoa(w.NextPane)
}

// MigrateIDs rewrites a state written before the addressable scheme. Panes are
// numbered in tree order per window; every focus and PaneState key follows.
func (st *State) MigrateIDs() {
	remap := map[string]string{}
	nws, nw := 0, 0
	for _, ws := range st.Workspaces {
		nws++
		ws.ID = "s" + strconv.Itoa(nws)
		for _, w := range ws.Windows {
			nw++
			w.ID = "w" + strconv.Itoa(nw)
			n := 0
			var walk func(*layout.Node)
			walk = func(nd *layout.Node) {
				if nd == nil {
					return
				}
				if nd.Pane != "" {
					n++
					nid := w.ID + "p" + strconv.Itoa(n)
					remap[nd.Pane] = nid
					nd.Pane = nid
					return
				}
				for _, k := range nd.Kids {
					walk(k)
				}
			}
			walk(w.Root)
			w.NextPane = n
			if nid, ok := remap[w.Focus]; ok {
				w.Focus = nid
			}
		}
	}
	panes := make(map[string]*PaneState, len(st.Panes))
	for old, ps := range st.Panes {
		if nid, ok := remap[old]; ok {
			panes[nid] = ps
		} else {
			panes[old] = ps
		}
	}
	st.Panes = panes
	st.NextWorkspace, st.NextWindow = nws, nw
}
