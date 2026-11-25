package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"dm/internal/ask"
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
		tree := cloneTree(s.st.ActiveWindow().Root)
		s.mu.Unlock()
		s.reply(cl, m, map[string]any{"snapshot": snap, "tree": tree}, nil)

	case "layout":
		// The live shape as a layout document, for `dm layout save`. Pane ids
		// stay live: layoutdoc.BuildInto renumbers them when the file comes
		// back in.
		s.mu.Lock()
		doc := s.captureLayoutLocked()
		s.mu.Unlock()
		s.reply(cl, m, map[string]any{"layout": doc}, nil)

	case "read":
		lines, st, err := s.paneLines(m.Target)
		if err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		s.reply(cl, m, map[string]any{"lines": lines, "status": st}, nil)

	case "screen":
		// One pane's visible screen, on request. A client that previews a pane
		// outside the active window has no bytes stream for it, so the screen
		// is the only way to paint the pane live. The reply is the pane's own
		// frame, forwarded as the replay case forwards its ring.
		s.mu.Lock()
		pc := s.panes[m.Target]
		s.mu.Unlock()
		if pc == nil {
			s.reply(cl, m, nil, fmt.Errorf("no pane %q", m.Target))
			return
		}
		got, err := paneRequest(s.st, m.Target, wire.Msg{Type: "req", Action: "screen"})
		if err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		if got.VTSnap == nil {
			s.reply(cl, m, nil, fmt.Errorf("pane %q sent no screen", m.Target))
			return
		}
		s.sendPicture(cl, wire.Msg{Type: "screen", Target: m.Target, VTSnap: got.VTSnap, Res: got.Res},
			m, map[string]any{"pane": m.Target})

	case "history":
		lines, err := s.paneHistory(m.Target, m.Count)
		if err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		s.reply(cl, m, map[string]any{"lines": lines}, nil)

	case "float-open":
		// The request opens a floating command on a pane's folder. The client
		// picks the id and the size; the server supplies the folder. A request
		// may carry its own command and label, and those run instead of the
		// config's diff-command. The dashboard diff key sends neither and gets
		// the diff command as always.
		if m.Float == "" {
			s.reply(cl, m, nil, fmt.Errorf("float-open needs a float id"))
			return
		}
		var req struct {
			Command *string `json:"command"`
			Label   *string `json:"label"`
		}
		if len(m.Res) > 0 {
			if err := json.Unmarshal(m.Res, &req); err != nil {
				s.reply(cl, m, nil, fmt.Errorf("float-open: %w", err))
				return
			}
		}
		cwd := s.liveCwd(m.Target)
		if cwd == "" {
			s.reply(cl, m, nil, fmt.Errorf("pane %s has no working directory", m.Target))
			return
		}
		command := ""
		if req.Command != nil {
			command = *req.Command
			if command == "" {
				s.reply(cl, m, nil, fmt.Errorf("float command must not be empty"))
				return
			}
			if strings.ContainsRune(command, '\x00') {
				s.reply(cl, m, nil, fmt.Errorf("float command contains a NUL byte"))
				return
			}
		} else {
			if req.Label != nil {
				s.reply(cl, m, nil, fmt.Errorf("float label needs a command"))
				return
			}
			s.mu.Lock()
			command = s.cfg.DiffCommand()
			s.mu.Unlock()
			if command == "" {
				s.reply(cl, m, nil, fmt.Errorf("diff-command is empty"))
				return
			}
		}
		label := ""
		if req.Label != nil {
			label = *req.Label
		}
		label = floatLabel(command, label)
		err := s.openFloat(cl, floatSpec{
			id: m.Float, target: m.Target, cwd: cwd, command: command, label: label,
			cols: m.Cols, rows: m.Rows,
		})
		if err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		cl.deliver(wire.Msg{
			Type: "resp", Action: m.Action, ID: m.ID, Float: m.Float, OK: true,
			Res: jsonRaw(map[string]any{"command": command, "label": label}),
		})

	case "float-close":
		// Closing a float the server does not know, or one another client
		// owns, answers OK: neither is this client's to open or close.
		s.closeFloatFor(cl, m.Float)
		cl.deliver(wire.Msg{Type: "resp", Action: m.Action, ID: m.ID, Float: m.Float, OK: true})

	case "yank":
		lines, start, rows, anchor, err := s.paneYank(m.Target)
		if err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		s.reply(cl, m, map[string]any{
			"lines": lines, "start": start, "rows": rows, "anchor": anchor,
		}, nil)

	case "replay":
		// The pane's history ring, on demand. Attach no longer pushes it.
		rp := readPaneReplay(s.st, m.Target)
		if !rp.ok {
			s.reply(cl, m, nil, fmt.Errorf("no pane %q", m.Target))
			return
		}
		s.mu.Lock()
		if pc := s.panes[m.Target]; pc != nil {
			pc.alt, pc.mouse = rp.alt, rp.mouse
		}
		s.mu.Unlock()
		// The ring is one picture: it may exceed the output bound, and a ring
		// the wire cannot carry answers an error. A dropped ring with an OK
		// reply is what left `dm history` silent.
		s.sendPicture(cl, wire.Msg{Type: "replay", Target: m.Target, Data: rp.data, St: &rp.st},
			m, map[string]any{"pane": m.Target})

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
		s.reply(cl, m, pc.status(), nil)

	case "write-to", "exec":
		if m.Action == "exec" {
			m.Enter = true
		}
		if err := s.writeTo(m.Target, m.Msg, writeOpts{enter: m.Enter, paste: m.Paste}); err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		s.reply(cl, m, map[string]any{"pane": m.Target}, nil)

	case "keys":
		if err := s.sendKeys(m.Target, m.Keys); err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		s.reply(cl, m, nil, nil)

	case "synchronize-panes":
		// The active window's sync flag toggles away, or sets true or false
		// when the request names on. The snapshot that follows carries it, so
		// clients can paint the mark.
		var sync bool
		err := s.mutation(func() error {
			var e error
			sync, e = s.synchronizePanes(m.On)
			return e
		})
		if err == nil {
			s.pushSnapshot()
		}
		s.reply(cl, m, map[string]any{"sync": sync}, err)

	case "toggle-secrets":
		// The operator's redaction switch. The attached gate keeps it off
		// the dm exec surface, so no script flips it by accident; it is a
		// convenience gate, not a boundary. The toggle is for human editing:
		// an agent attacking the dm socket can send the same attach frame
		// and flip it, as it can already type keys and read panes, and the
		// real boundary is the sandboxing work. The switch rides the same
		// pane-daemon request path the screen and replay actions use, and
		// the reply carries the state the pane landed on, so the client can
		// show an unprotected pane as unprotected.
		if !cl.attached {
			s.reply(cl, m, nil, fmt.Errorf("toggle-secrets runs from the interactive client only"))
			return
		}
		// The default target and its liveness are one read of the state:
		// ActiveWindow and the pane map are both written under s.mu.
		s.mu.Lock()
		id := m.Target
		if id == "" {
			id = s.st.ActiveWindow().Focus
		}
		_, live := s.panes[id]
		s.mu.Unlock()
		if !live {
			s.reply(cl, m, nil, fmt.Errorf("no pane %q", id))
			return
		}
		got, err := paneRequest(s.st, id, wire.Msg{Type: "req", Action: "toggle-secrets"})
		if err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		var res struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.Unmarshal(got.Res, &res); err != nil {
			s.reply(cl, m, nil, fmt.Errorf("pane %s toggle reply: %w", id, err))
			return
		}
		s.reply(cl, m, map[string]any{"pane": id, "enabled": res.Enabled}, nil)

	case "split":
		var id string
		err := s.mutation(func() error {
			var e error
			id, e = s.split(m.Target, m.Dir, m.Cwd, m.Ratio, m.NoFocus, m.Argv, nil)
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

	case "swap-panes":
		// The two named panes swap in place, or a request carrying a dir and
		// no other pane swaps the focused pane with the neighbour focus-dir
		// would reach in that direction. Either way the rectangles keep their
		// shapes and the focused pane keeps its id and its focus.
		err := s.mutation(func() error {
			if m.Dir != "" && m.Other != "" {
				return fmt.Errorf("swap-panes takes dir or other, not both")
			}
			if m.Dir == "" && m.Other == "" {
				return fmt.Errorf("swap-panes needs a dir or an other pane")
			}
			if m.Dir != "" {
				return s.swapByDir(m.Dir)
			}
			return s.swapPanes(m.Target, m.Other)
		})
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "move-pane":
		// The pane named by target moves to the edge of the pane named by
		// other in dir and takes the focus.
		err := s.mutation(func() error { return s.movePane(m.Target, m.Other, m.Dir) })
		s.afterMutation()
		s.reply(cl, m, map[string]any{"pane": m.Target}, err)

	case "resize-split":
		err := s.mutation(func() error { return s.resizeSplit(m.Target, m.Other, m.Ratio) })
		if err == nil {
			s.clearZoom()
			s.syncSizes()
			s.pushSnapshot()
		}
		s.reply(cl, m, nil, err)

	case "select-layout":
		var name string
		err := s.mutation(func() error {
			var e error
			name, e = s.selectLayout(m.Layout)
			return e
		})
		s.afterMutation()
		s.reply(cl, m, map[string]any{"layout": name}, err)

	case "focus":
		// A shelf member has no window focus to take: naming one moves the
		// shelf's active member instead, which is what the shelf float's n
		// and p keys and a rail shelf row send.
		if s.focusShelf(m.Target) {
			s.pushSnapshot()
			s.reply(cl, m, nil, nil)
			return
		}
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

	case "shelve-pane":
		// The pane leaves its window's tree and joins the shelf: it keeps its
		// id, its state and its PTY, and gives up its rect and its focus.
		var id string
		err := s.mutation(func() error {
			var e error
			id, e = s.shelvePane(m.Target)
			return e
		})
		s.afterMutation()
		s.reply(cl, m, map[string]any{"pane": id}, err)

	case "pop-pane":
		// The shelf member returns beside the focused pane, on the right, and
		// takes the focus. The tree change is the split path's own.
		var id string
		err := s.mutation(func() error {
			var e error
			id, e = s.popPane(m.Target)
			return e
		})
		s.afterMutation()
		s.reply(cl, m, map[string]any{"pane": id}, err)

	case "close-window":
		err := s.mutation(func() error { return s.closeWindow(m.Target) })
		s.afterMutation()
		s.reply(cl, m, nil, err)

	case "new-window":
		var wid, pid string
		err := s.mutation(func() error {
			var e error
			wid, pid, e = s.newWindow(m.Target, m.Name, m.Cwd, m.NoFocus, m.Argv, nil)
			return e
		})
		s.afterMutation()
		s.reply(cl, m, map[string]any{"window": wid, "pane": pid}, err)

	case "run-plugin":
		var req struct {
			Name   string `json:"name"`
			Where  string `json:"where"`
			Source string `json:"source"`
		}
		if len(m.Res) > 0 {
			if err := json.Unmarshal(m.Res, &req); err != nil {
				s.reply(cl, m, nil, fmt.Errorf("run-plugin: %w", err))
				return
			}
		}
		p, ok := s.plugin(req.Name)
		if !ok {
			s.reply(cl, m, nil, fmt.Errorf("no plugin %q", req.Name))
			return
		}
		where := p.Where
		if req.Where != "" {
			where = req.Where
		}
		var id string
		err := s.mutation(func() error {
			var e error
			id, e = s.runPlugin(p, m.Target, where, req.Source)
			return e
		})
		s.afterMutation()
		s.reply(cl, m, map[string]any{"pane": id}, err)

	case "plugin-list":
		out := make([]map[string]any, 0)
		for _, p := range s.plugins() {
			out = append(out, map[string]any{
				"name": p.Name, "exec": p.Exec, "cwd": p.Cwd,
				"where": p.Where, "env": p.Env,
			})
		}
		s.reply(cl, m, map[string]any{"plugins": out}, nil)

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
			wsid, wid, pid, e = s.newWorkspace(m.Name, m.Window, m.Cwd, m.Project, m.NoFocus, m.Argv)
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
		place, ok := s.findPane(m.Target)
		if !ok {
			s.reply(cl, m, nil, fmt.Errorf("no pane %q", m.Target))
			return
		}
		res := map[string]any{
			"id": m.Target, "workspace": place.WorkspaceID, "window": place.WindowID,
			"workspaceName": place.WorkspaceName, "windowName": place.WindowName,
		}
		if place.Saved {
			res["name"] = place.Name
			res["cwd"] = place.Cwd
		}
		s.reply(cl, m, res, nil)

	case "toggle-fold":
		unfolded, err := s.toggleFold(m.Target)
		if err == nil {
			s.pushSnapshot()
		}
		s.reply(cl, m, map[string]any{"target": m.Target, "unfolded": unfolded}, err)

	case "set-pane-name":
		err := s.mutation(func() error {
			if err := checkName(m.Name); err != nil {
				return err
			}
			// The pane map is written by adopt and close under s.mu, so this
			// mutation takes it for its own read and write.
			s.mu.Lock()
			defer s.mu.Unlock()
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

	case "move-window":
		// The named window swaps with its neighbour in its workspace. The
		// slice order is the rail, tab bar and cycle order, so the snapshot
		// alone re-renders them.
		err := s.mutation(func() error { return s.moveWindow(m.Target, m.Dir) })
		if err == nil {
			s.pushSnapshot()
		}
		s.reply(cl, m, nil, err)

	case "move-workspace":
		// The named workspace swaps with its neighbour in its project
		// group. The slice order is the rail and cycle order, so the
		// snapshot alone re-renders them.
		err := s.mutation(func() error { return s.moveWorkspace(m.Target, m.Dir) })
		if err == nil {
			s.pushSnapshot()
		}
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

	case "secret-register":
		s.secretRegister(cl, m)

	case "secret-list":
		s.secretList(cl, m)

	case "secret-revoke":
		s.secretRevoke(cl, m)

	case "secret-use":
		s.secretUse(cl, m)

	case "secret-hydrate":
		s.secretHydrate(cl, m)

	case "secret-type":
		s.secretType(cl, m)

	case "secret-cover":
		s.secretCover(cl, m)

	case "secret-sync":
		s.secretSync(cl, m)

	// The ask store: the questions an agent puts to the reader on a pane. The
	// bodies come from internal/ask, so the CLI and the daemon decode one
	// contract, and every refusal carries a machine-readable code.
	case "ask-open":
		req, err := ask.ParseOpen(m.Res)
		if err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		id, err := s.askOpen(m.Target, req)
		s.reply(cl, m, ask.OpenReply{ID: id}, err)

	case "ask-await":
		req, err := ask.ParseAwait(m.Res)
		if err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		rep, err := s.askAwait(cl, m.Target, req.Wait())
		if errors.Is(err, errAskGone) {
			// The connection went, so there is no one to reply to.
			return
		}
		s.reply(cl, m, rep, err)

	case "ask-answer":
		req, err := ask.ParseAnswer(m.Res)
		if err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		answer, err := s.askAnswer(m.Target, req)
		s.reply(cl, m, answer, err)

	case "ask-close":
		closed, err := s.askClose(m.Target)
		s.reply(cl, m, ask.ClosedReply{Closed: closed}, err)

	case "ask-list":
		s.reply(cl, m, ask.ListReply{Asks: s.askList()}, nil)

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
		if c < '0' || c > 'f' || (c > '9' && c < 'A') || (c > 'F' && c < 'a') {
			return false
		}
	}
	return true
}
