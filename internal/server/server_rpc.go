package server

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"dm/internal/box"
	"dm/internal/layout"
	"dm/internal/wire"
)

func (s *server) handleReq(cl *client, m wire.Msg) {
	switch m.Action {
	case "ping":
		s.reply(cl, m, map[string]any{"session": s.st.Name}, nil)

	case "term-colours":
		// A client reports the pair its own terminal answered. Normalise both
		// before storing, so a bad value changes nothing. An empty half is
		// left as it was: the client may have got only one reply.
		fg, bg := m.FG, m.BG
		if fg != "" {
			v, err := normaliseTermColour(fg)
			if err != nil {
				s.reply(cl, m, nil, err)
				return
			}
			fg = v
		}
		if bg != "" {
			v, err := normaliseTermColour(bg)
			if err != nil {
				s.reply(cl, m, nil, err)
				return
			}
			bg = v
		}
		if fg == "" && bg == "" {
			s.reply(cl, m, nil, nil)
			return
		}
		s.mu.Lock()
		tc := s.termColours
		if tc == nil {
			tc = &termColours{}
			s.termColours = tc
		}
		if fg != "" {
			tc.fg = fg
		}
		if bg != "" {
			tc.bg = bg
		}
		fg, bg = tc.fg, tc.bg
		panes := make([]*paneConn, 0, len(s.panes))
		for _, pc := range s.panes {
			panes = append(panes, pc)
		}
		s.mu.Unlock()
		for _, pc := range panes {
			if err := pc.send(wire.Msg{Action: "term-colours", FG: fg, BG: bg}); err != nil {
				log.Printf("pane %s: term-colours: %v", pc.id, err)
			}
		}
		s.reply(cl, m, nil, nil)

	case "list", "ls":
		s.mu.Lock()
		snap := s.snapshotLocked()
		win := s.st.ActiveWindow()
		tree := win.Root
		s.mu.Unlock()
		s.reply(cl, m, map[string]any{"snapshot": snap, "tree": tree}, nil)

	case "read":
		lines, st, err := s.paneLines(m.Target)
		if err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		s.reply(cl, m, map[string]any{"lines": lines, "status": st}, nil)

	case "history":
		lines, err := s.paneHistory(m.Target, m.Count)
		if err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		s.reply(cl, m, map[string]any{"lines": lines}, nil)

	case "replay":
		// The pane's history ring, on demand. Attach no longer pushes it.
		rp := readPaneReplay(s.st.Name, m.Target)
		if !rp.ok {
			s.reply(cl, m, nil, fmt.Errorf("no pane %q", m.Target))
			return
		}
		s.mu.Lock()
		if pc := s.panes[m.Target]; pc != nil {
			pc.alt, pc.mouse = rp.alt, rp.mouse
		}
		s.mu.Unlock()
		cl.deliver(wire.Msg{Type: "replay", Target: m.Target, Data: rp.data, St: &rp.st})
		s.reply(cl, m, map[string]any{"pane": m.Target}, nil)

	case "status":
		s.mu.Lock()
		pc := s.panes[m.Target]
		s.mu.Unlock()
		if m.Target == "" {
			s.reply(cl, m, s.statuses(), nil)
			return
		}
		if pc == nil {
			s.reply(cl, m, nil, fmt.Errorf("no pane %q", m.Target))
			return
		}
		s.reply(cl, m, pc.st, nil)

	case "write-to", "exec":
		if m.Action == "exec" {
			m.Enter = true
		}
		if err := s.writeTo(m.Target, m.Msg, m.Enter); err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		s.reply(cl, m, map[string]any{"pane": m.Target}, nil)

	case "keys":
		s.mu.Lock()
		pc := s.panes[m.Target]
		s.mu.Unlock()
		if pc == nil {
			s.reply(cl, m, nil, fmt.Errorf("no pane %q", m.Target))
			return
		}
		_ = pc.send(wire.Msg{Action: "write", Keys: m.Keys})
		s.reply(cl, m, nil, nil)

	case "split":
		var id string
		err := s.mutation(func() error {
			var e error
			id, e = s.split(m.Target, m.Dir, m.Cwd, m.Ratio, m.NoFocus)
			return e
		})
		s.afterMutation()
		s.reply(cl, m, map[string]any{"pane": id}, err)

	case "zoom-pane":
		// The target is the focus of the active window. One pane already fills
		// the area, so the action is a no-op there.
		s.mu.Lock()
		win := s.st.ActiveWindow()
		if len(layout.AllPanes(win.Root)) < 2 {
			s.mu.Unlock()
			s.reply(cl, m, map[string]any{"zoom": ""}, nil)
			return
		}
		if s.zoom == win.Focus {
			s.zoom = ""
		} else {
			s.zoom = win.Focus
		}
		zoom := s.zoom
		s.mu.Unlock()
		// resync, not afterMutation: afterMutation would clear the zoom it is
		// being asked to apply.
		s.resync()
		s.reply(cl, m, map[string]any{"zoom": zoom}, nil)

	case "reload":
		// The client asks the server to re-read the file it owns. On a failed
		// read nothing changes and the error rides back; on success the layout
		// is resynced and the reply carries the padding now in force.
		if err := s.reloadConfig(); err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		s.resync()
		s.mu.Lock()
		pad := s.cfg.Padding()
		s.mu.Unlock()
		s.reply(cl, m, map[string]any{"pad": pad}, nil)

	case "set-rail":
		rail := s.setRail(m.Index)
		s.syncSizes()
		s.pushSnapshot()
		s.reply(cl, m, map[string]any{"rail": rail}, nil)

	case "resize-split":
		err := s.mutation(func() error { return s.resizeSplit(m.Target, m.Other, m.Ratio) })
		if err == nil {
			s.clearZoom()
			s.syncSizes()
			s.pushSnapshot()
		}
		s.reply(cl, m, nil, err)

	case "focus":
		err := s.mutation(func() error { return s.setFocus(m.Target) })
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "focus-dir":
		err := s.mutation(func() error { return s.focusDir(m.Dir) })
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "next-pane", "prev-pane":
		step := 1
		if m.Action == "prev-pane" {
			step = -1
		}
		err := s.mutation(func() error { return s.cyclePane(step) })
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "close-pane":
		err := s.mutation(func() error { return s.closePane(m.Target) })
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "close-window":
		err := s.mutation(func() error { return s.closeWindow(m.Target) })
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "new-window":
		var wid, pid string
		err := s.mutation(func() error {
			var e error
			wid, pid, e = s.newWindow(m.Target, m.Name, m.Cwd, m.NoFocus)
			return e
		})
		s.afterMutation()
		s.reply(cl, m, map[string]any{"window": wid, "pane": pid}, err)

	case "next-window", "prev-window":
		step := 1
		if m.Action == "prev-window" {
			step = -1
		}
		err := s.mutation(func() error { return s.cycleWindow(step) })
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "select-window":
		err := s.mutation(func() error { return s.selectWindow(m.Index) })
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "new-workspace":
		var wsid, wid, pid string
		err := s.mutation(func() error {
			var e error
			wsid, wid, pid, e = s.newWorkspace(m.Name, m.Window, m.Cwd, m.Project, m.NoFocus)
			return e
		})
		s.afterMutation()
		s.reply(cl, m, map[string]any{"workspace": wsid, "window": wid, "pane": pid}, err)

	case "next-workspace", "prev-workspace":
		step := 1
		if m.Action == "prev-workspace" {
			step = -1
		}
		err := s.mutation(func() error { return s.cycleWorkspace(step) })
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "list-workspaces":
		s.reply(cl, m, s.workspaceList(), nil)

	case "list-panes":
		s.reply(cl, m, map[string]any{"panes": s.paneList()}, nil)

	case "pane":
		ws, w, ok := s.findPane(m.Target)
		if !ok {
			s.reply(cl, m, nil, fmt.Errorf("no pane %q", m.Target))
			return
		}
		ps := s.st.Panes[m.Target]
		res := map[string]any{"id": m.Target, "workspace": ws.ID, "window": w.ID,
			"workspaceName": ws.Name, "windowName": w.Name}
		if ps != nil {
			res["name"] = ps.Name
			res["cwd"] = ps.Cwd
		}
		s.reply(cl, m, res, nil)

	case "toggle-fold":
		unfolded, err := s.toggleFold(m.Target)
		if err == nil {
			s.pushSnapshot()
		}
		s.reply(cl, m, map[string]any{"window": m.Target, "unfolded": unfolded}, err)

	case "set-pane-name":
		err := s.mutation(func() error {
			ps := s.st.Panes[m.Target]
			if ps == nil {
				return fmt.Errorf("no pane %q", m.Target)
			}
			ps.Name = m.Name
			return nil
		})
		s.afterMutation()
		s.reply(cl, m, map[string]any{"pane": m.Target, "name": m.Name}, err)

	case "focus-window":
		err := s.mutation(func() error { return s.focusWindow(m.Target) })
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "focus-workspace":
		err := s.mutation(func() error { return s.focusWorkspace(m.Target) })
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "close-workspace":
		err := s.mutation(func() error { return s.closeWorkspace(m.Target) })
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "rename-window":
		err := s.mutation(func() error { return s.renameWindow(m.Target, m.Name) })
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "msg-post":
		req, err := box.ParseReq(m.Action, m.Res)
		if err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		e, err := s.boxPost(req.(*box.PostReq))
		if err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		s.reply(cl, m, box.PostReply{Entry: e}, nil)

	case "msg-list":
		req, err := box.ParseReq(m.Action, m.Res)
		if err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		entries, trimmed, err := s.boxList(req.(*box.ListReq))
		if err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		s.reply(cl, m, box.ListReply{Entries: entries, Trimmed: trimmed}, nil)

	case "msg-clear":
		req, err := box.ParseReq(m.Action, m.Res)
		if err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		cleared, err := s.boxClear(req.(*box.ClearReq))
		s.reply(cl, m, box.ClearReply{Cleared: cleared}, err)

	case "msg-wait":
		req, err := box.ParseReq(m.Action, m.Res)
		if err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		e, trimmed, err := s.boxWait(cl, req.(*box.WaitReq))
		s.reply(cl, m, box.WaitReply{Entry: e, Trimmed: trimmed}, err)

	case "msg-sub":
		req, err := box.ParseReq(m.Action, m.Res)
		if err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		// A stream has no completion reply. A bad request answered above; a
		// stream that ends because its connection went answers nothing.
		if err := s.boxSub(cl, req.(*box.SubReq)); err != nil && !errors.Is(err, errBoxGone) {
			s.reply(cl, m, nil, err)
		}

	case "msg-watchers":
		if _, err := box.ParseReq(m.Action, m.Res); err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		s.reply(cl, m, box.WatchersReply{Watchers: s.boxWatchers()}, nil)

	case "msg-schema":
		if _, err := box.ParseReq(m.Action, m.Res); err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		s.reply(cl, m, box.SchemaReply{Schema: box.Schema()}, nil)

	case "set-project":
		project := strings.TrimSpace(m.Name)
		err := s.mutation(func() error { return s.setProject(m.Target, project) })
		s.afterMutation()
		s.reply(cl, m, map[string]any{"workspace": m.Target, "project": project}, err)

	case "rename-workspace":
		name := strings.TrimSpace(m.Name)
		err := s.mutation(func() error { return s.renameWorkspace(m.Target, name) })
		s.afterMutation()
		s.reply(cl, m, map[string]any{"workspace": m.Target, "name": name}, err)

	default:
		s.reply(cl, m, nil, fmt.Errorf("unknown action %q", m.Action))
	}
}

// normaliseTermColour reads the two forms a terminal answers OSC 10 and OSC 11
// with and returns the xterm form rgb:RRRR/GGGG/BBBB. Anything else is an
// error naming the value.
func normaliseTermColour(s string) (string, error) {
	if strings.HasPrefix(s, "#") {
		h := strings.ToLower(s[1:])
		if len(h) != 6 || !isHex(h) {
			return "", fmt.Errorf("colour %q is not #rrggbb", s)
		}
		return "rgb:" + h[0:2] + h[0:2] + "/" + h[2:4] + h[2:4] + "/" + h[4:6] + h[4:6], nil
	}
	if strings.HasPrefix(s, "rgb:") {
		parts := strings.Split(s[len("rgb:"):], "/")
		if len(parts) != 3 {
			return "", fmt.Errorf("colour %q is not rgb:RRRR/GGGG/BBBB", s)
		}
		for _, p := range parts {
			if len(p) != 4 || !isHex(p) {
				return "", fmt.Errorf("colour %q is not rgb:RRRR/GGGG/BBBB", s)
			}
		}
		return "rgb:" + strings.ToLower(strings.Join(parts, "/")), nil
	}
	return "", fmt.Errorf("colour %q is not a terminal colour", s)
}

// isHex reports whether s is one or more hex digits.
func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
