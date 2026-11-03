package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// dashRows flattens a session into palette entries: the target id first, then
// a label the eye can read. The first space-separated word is the id, because
// television's output template takes field zero.
func dashRows(session string) ([]string, error) {
	m, err := rpc(session, Msg{Action: "list"})
	if err != nil {
		return nil, err
	}
	if !m.OK {
		return nil, fmt.Errorf("%s", m.Error)
	}
	var res struct {
		Snapshot *Snapshot `json:"snapshot"`
	}
	if err := json.Unmarshal(m.Res, &res); err != nil {
		return nil, err
	}
	snap := res.Snapshot
	if snap == nil {
		return nil, fmt.Errorf("no snapshot")
	}

	var wss []wsInfo
	if wm, err := rpc(session, Msg{Action: "list-workspaces"}); err == nil && wm.OK {
		_ = json.Unmarshal(wm.Res, &wss)
	}

	byWS := map[string][]AllPane{}
	byWin := map[string][]AllPane{}
	for _, p := range snap.All {
		byWS[p.Workspace] = append(byWS[p.Workspace], p)
		byWin[p.Window] = append(byWin[p.Window], p)
	}

	var rows []string
	for _, ws := range wss {
		mark := " "
		if ws.Active {
			mark = ">"
		}
		rows = append(rows, ws.ID+" "+mark+" "+ws.Name)
		for _, w := range ws.Windows {
			wmark := " "
			if w.Active {
				wmark = ">"
			}
			rows = append(rows, w.ID+"   "+wmark+" "+w.Name)
			for _, p := range byWS[ws.ID] {
				if p.Window != w.ID {
					continue
				}
				rows = append(rows, p.ID+"     "+paneLabel(p))
			}
		}
	}
	return rows, nil
}

// paneLabel names a pane for the palette: its name when it has one, then its
// live state, branch and worktree, the way the rail labels it. The id already
// leads the row, so the label never repeats it.
func paneLabel(p AllPane) string {
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
	return name
}

// dash prints the entries, one per line.
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

// peek prints the screen the palette row would land on. A window or workspace
// row borrows its first pane, so every row has a preview.
func peek(session, target string, lines int) error {
	pane := target
	if !strings.Contains(target, "p") {
		m, err := rpc(session, Msg{Action: "list"})
		if err != nil {
			return err
		}
		if !m.OK {
			return fmt.Errorf("%s", m.Error)
		}
		var res struct {
			Snapshot *Snapshot `json:"snapshot"`
		}
		if err := json.Unmarshal(m.Res, &res); err != nil {
			return err
		}
		pane = ""
		for _, p := range res.Snapshot.All {
			if p.Window == target || p.Workspace == target {
				pane = p.ID
				break
			}
		}
		if pane == "" {
			return fmt.Errorf("no pane in %s", target)
		}
	}
	m, err := rpc(session, Msg{Action: "read", Target: pane})
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

// allDigits reports whether s is a non-empty run of decimal digits, the shape
// of an id without its letter.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
