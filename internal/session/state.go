package session

import (
	"encoding/json"
	"log"
	"os"
	"sort"
	"strconv"

	"dm/internal/box"
	"dm/internal/layout"
	"dm/internal/paths"
)

type Window struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Named records that the user set the name with rename-window. A window
	// created without a label starts unnamed.
	Named    bool         `json:"named,omitempty"`
	Root     *layout.Node `json:"root"`
	Focus    string       `json:"focus"`
	NextPane int          `json:"nextPane,omitempty"`
	// Unfolded shows the window's pane rows in the rail. It is inverted so the
	// zero value, which older state files carry, means the default: folded.
	Unfolded bool `json:"unfolded,omitempty"`
}

type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Project is the name of the project the workspace belongs to. Workspaces
	// that share a name form one contiguous group. Empty means no project.
	Project string    `json:"project,omitempty"`
	Windows []*Window `json:"windows"`
	Active  int       `json:"active"`
	// Unfolded shows the workspace's pane rows in the rail. A workspace starts
	// unfolded. Older state files carry no key; UnmarshalJSON reads that as
	// unfolded. An explicit false persists as folded.
	Unfolded bool `json:"unfolded"`
}

// UnmarshalJSON defaults a workspace to unfolded when an older state file has
// no workspace fold key. An explicit false in the file stays folded.
func (ws *Workspace) UnmarshalJSON(b []byte) error {
	type workspaceJSON Workspace
	aux := struct {
		*workspaceJSON
		Unfolded *bool `json:"unfolded"`
	}{workspaceJSON: (*workspaceJSON)(ws)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	if aux.Unfolded == nil {
		ws.Unfolded = true
	} else {
		ws.Unfolded = *aux.Unfolded
	}
	return nil
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
	// Argv is the program the pane runs instead of a shell, program first.
	// Empty means $SHELL -l, a pane as it has always been.
	Argv []string `json:"argv,omitempty"`
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

	// ProjectFold is the set of project names whose rail group is folded.
	// A name absent from the map is unfolded.
	ProjectFold map[string]bool `json:"projectFold,omitempty"`

	// root is the state tree this session lives under. It is fixed at
	// construction, so a State never consults the environment afterwards.
	root string

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

// NewStateAt builds a fresh state for name under the state tree rooted at
// root. Save and every path the session derives later come from root.
func NewStateAt(root, name string) *State {
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
		root:          root,
		Workspaces: []*Workspace{{
			ID:       "s1",
			Name:     "main",
			Unfolded: true,
			Windows: []*Window{{
				ID:   "w1",
				Name: defWin,
			}},
		}},
	}
	return st
}

// NewState builds a fresh state under the default state tree.
func NewState(name string) *State { return NewStateAt(paths.StateDir(), name) }

// tree is the state tree the state's paths come from. A state built through
// NewStateAt carries its root and never consults the environment. A state
// assembled by hand, as a zero-value literal, has no root and keeps the old
// behaviour: its paths follow the environment at call time.
func (st *State) tree() string {
	if st.root == "" {
		return paths.StateDir()
	}
	return st.root
}

// Dir is the state tree root the state was built with.
func (st *State) Dir() string { return st.tree() }

// SessionDir is the directory that holds this session's state and sockets.
func (st *State) SessionDir() string { return paths.SessionDirAt(st.tree(), st.Name) }

// ServerSock is the unix socket the session's server listens on.
func (st *State) ServerSock() string { return paths.ServerSockAt(st.tree(), st.Name) }

// PaneSock is the unix socket one pane's mux listens on.
func (st *State) PaneSock(id string) string { return paths.PaneSockAt(st.tree(), st.Name, id) }

// StatePath is the session's state file.
func (st *State) StatePath() string { return paths.StatePathAt(st.tree(), st.Name) }

func (st *State) Save() error {
	if err := os.MkdirAll(st.SessionDir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	// A unique temp name per save. Overlapping saves then write and rename
	// separate paths, so the last rename wins instead of one failing with
	// ENOENT on a temp file another save already moved. CreateTemp makes the
	// file 0600, so the state stays private while it is written.
	f, err := os.CreateTemp(st.SessionDir(), "state-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, st.StatePath()); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// LoadStateAt reads the state file for name from the state tree rooted at
// root. The returned state carries root, so its own Save and path methods
// stay inside that tree.
func LoadStateAt(root, name string) (*State, error) {
	st := &State{root: root, Name: name}
	b, err := os.ReadFile(st.StatePath())
	if err != nil {
		return nil, err
	}
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
	// A hand-edited or interleaved state file loads grouped, because the rail
	// and the cycle keys both read the slice order.
	st.GroupWorkspaces()
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

// LoadState reads the state file for name from the default state tree.
func LoadState(name string) (*State, error) { return LoadStateAt(paths.StateDir(), name) }

// GroupWorkspaces stable-sorts st.Workspaces so workspaces that share a project
// are contiguous. The first appearance of a project decides the group's
// position, and the order inside a group is preserved. A workspace with no
// project is its own group of one, keyed by its id, so untagged workspaces
// never merge with each other. Active follows the workspace it named before the
// sort. An out-of-range Active stays out of range.
func (st *State) GroupWorkspaces() {
	// Active is an index into the slice, so it has to survive the reorder.
	var activeID string
	activeOK := st.Active >= 0 && st.Active < len(st.Workspaces)
	if activeOK {
		activeID = st.Workspaces[st.Active].ID
	}
	key := func(ws *Workspace) string {
		if ws.Project == "" {
			return "\x00" + ws.ID
		}
		return "p" + ws.Project
	}
	rank := make(map[string]int, len(st.Workspaces))
	for _, ws := range st.Workspaces {
		k := key(ws)
		if _, ok := rank[k]; !ok {
			rank[k] = len(rank)
		}
	}
	sort.SliceStable(st.Workspaces, func(i, j int) bool {
		return rank[key(st.Workspaces[i])] < rank[key(st.Workspaces[j])]
	})
	if !activeOK {
		return
	}
	for i, ws := range st.Workspaces {
		if ws.ID == activeID {
			st.Active = i
			return
		}
	}
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
