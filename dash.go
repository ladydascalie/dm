package main

import (
	"dm/internal/transport"
	"dm/internal/wire"
	"encoding/json"
	"fmt"
	"strings"
)

// dashRows flattens a session into the rows a palette jump uses, one per pane.
// The target id leads, then the pane's label and where it lives. A workspace or
// a window row would only repeat the row of the pane it holds, so dash lists
// panes alone.
func dashRows(session string) ([]string, error) {
	m, err := transport.RPC(session, wire.Msg{Action: "list"})
	if err != nil {
		return nil, err
	}
	if !m.OK {
		return nil, fmt.Errorf("%s", m.Error)
	}
	var res struct {
		Snapshot *wire.Snapshot `json:"snapshot"`
	}
	if err := json.Unmarshal(m.Res, &res); err != nil {
		return nil, err
	}
	snap := res.Snapshot
	if snap == nil {
		return nil, fmt.Errorf("no snapshot")
	}

	var rows []string
	for _, p := range snap.All {
		mark := " "
		if p.Focus {
			mark = ">"
		}
		rows = append(rows, p.ID+" "+mark+" "+paneLabel(p))
	}
	return rows, nil
}

// paneLabel names a pane in a palette row: its name when it has one, then its
// live state, branch and worktree. The id already leads the row, so the label
// never repeats it, and the row ends with the workspace and window it lives
// in.
func paneLabel(p wire.AllPane) string {
	name := p.Name
	if name == "" {
		name = p.App
	}
	if name == "" {
		if p.Agent {
			name = p.ID
		} else {
			name = "(shell)"
		}
	}
	if p.Status != "" && p.Status != "unknown" {
		name += "  " + p.Status
	}
	if p.Message != "" {
		name += "  " + p.Message
	}
	if p.Branch != "" {
		name += "  " + p.Branch
	}
	if p.Worktree != "" {
		name += "  " + p.Worktree
	}
	if p.WorkspaceName != "" {
		name += "  " + p.WorkspaceName + " > " + p.WindowName
	}
	return name
}

// dash prints the rows a palette jump uses, one per line.
func dash(session string) error {
	rows, err := dashRows(session)
	if err != nil {
		return err
	}
	for _, r := range rows {
		fmt.Println(r)
	}
	return nil
}

// peek prints the screen a palette row shows.
func peek(session, target string, lines int) error {
	m, err := transport.RPC(session, wire.Msg{Action: "read", Target: target})
	if err != nil {
		return err
	}
	if !m.OK {
		return fmt.Errorf("%s", m.Error)
	}
	var res struct {
		Lines []string `json:"lines"`
	}
	_ = json.Unmarshal(m.Res, &res)
	out := res.Lines
	if lines > 0 && len(out) > lines {
		out = out[len(out)-lines:]
	}
	fmt.Println(strings.Join(out, "\n"))
	return nil
}
