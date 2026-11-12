package client

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"dm/internal/cells"
	"dm/internal/layout"
	"dm/internal/panel"
	"dm/internal/vt"
	"dm/internal/wire"
)

// floatView is one floating command pane: the terminal the child's bytes paint
// into, the panel the client draws around it, and the command and label the
// server named. The label is what the panel title paints, the command's first
// word when the server named none. The client keeps one at a time.
type floatView struct {
	id      string
	command string
	label   string
	prefix  string
	vt      *vt.Term
	float   *panel.FloatingPane
	exited  bool
	closed  bool
	// opened turns true when the float-open reply lands. Keys typed before
	// that are dropped: the child does not exist yet, and forwarding them
	// would either miss it or answer with a phantom error.
	opened bool
}

// floatLabelDefault is the panel title when the reply names no label: the
// command's first word, the way the server defaults it.
func floatLabelDefault(command string) string {
	if f := strings.Fields(command); len(f) > 0 {
		return f[0]
	}
	return ""
}

// floatRect is where the floating command sits: centred, eight cells clear of
// the screen's sides, at most 110 columns wide and rows-4 tall, so the box and
// its border always fit. It is the interior; the panel draws the border around
// it.
func floatRect(snap *wire.Snapshot) layout.Rect {
	w := snap.Cols - 8
	if w > 110 {
		w = 110
	}
	if w < 10 {
		w = snap.Cols - 2
	}
	h := snap.Rows - 4
	if h < 3 {
		h = snap.Rows - 2
	}
	x := (snap.Cols - w) / 2
	y := (snap.Rows - (h + 2)) / 2
	if x+w > snap.Cols-1 {
		x = snap.Cols - 1 - w
	}
	if y+h > snap.Rows-1 {
		y = snap.Rows - 1 - h
	}
	if x < 1 {
		x = 1
	}
	if y < 1 {
		y = 1
	}
	return layout.Rect{X: x, Y: y, W: w, H: h}
}

// openFloatCommand closes the dashboard and asks the server to run the diff
// command on the selected pane's folder. It sends no command of its own, so the
// server runs the config's diff-command as today.
func (t *tui) openFloatCommand() {
	if t.sw == nil || t.conn == nil || t.snap == nil {
		return
	}
	pane := t.sw.Target()
	if pane == "" {
		return
	}
	t.openFloatFor(pane, "", "")
}

// openFloatFor asks the server to run command on pane's folder and shows it in
// a floating pane. command may be empty, meaning the server's diff-command;
// label is what the panel title paints, defaulting to the command's first
// word. The client picks the id and builds the terminal at the size it will
// draw first, so the child's first bytes cannot arrive before there is a
// terminal to hold them.
func (t *tui) openFloatFor(pane, command, label string) {
	if t.conn == nil || t.snap == nil || pane == "" {
		return
	}
	closeSwitcher(t)
	r := floatRect(t.snap)
	id := "float-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	t.float = &floatView{
		id:     id,
		label:  label,
		prefix: t.km().PrefixName(),
		vt:     vt.NewTerm(r.W, r.H),
		float:  &panel.FloatingPane{ID: id, Rect: r, Active: true},
	}
	t.floatID++
	var res json.RawMessage
	if command != "" {
		res, _ = json.Marshal(map[string]string{"command": command, "label": label})
	}
	_ = t.conn.Send(wire.Msg{
		Type: "req", Action: "float-open", ID: t.floatID, Target: pane,
		Float: id, Cols: r.W, Rows: r.H, Res: res,
	})
	// The mode goes on before the reply, so a key typed during the round trip
	// waits instead of reaching the pane behind the coming float.
	t.mode = "float"
	t.armed = false
	t.dirty = true
}

// takeFloatOpen shows the float the reply opened. A refusal toasts the reason
// and drops the terminal, leaving the panes as they were.
func (t *tui) takeFloatOpen(m wire.Msg) {
	f := t.float
	if f == nil || f.closed {
		return
	}
	if !m.OK {
		t.float = nil
		t.mode = ""
		t.inbuf = nil
		t.pending = false
		t.notify(m.Error)
		t.dirty = true
		return
	}
	var res struct {
		Command string `json:"command"`
		Label   string `json:"label"`
	}
	_ = json.Unmarshal(m.Res, &res)
	f.command = res.Command
	if res.Label != "" {
		f.label = res.Label
	} else if res.Command != "" {
		f.label = floatLabelDefault(res.Command)
	}
	f.opened = true
	f.setBorders()
	f.float.VT = f.vt
	f.float.Paint = floatVTPaint(f.vt, nil)
	t.AddFloatingPane(f.float)
	t.mode = "float"
	t.armed = false
	t.dirty = true
}

// setBorders writes the border title and hint from the label and whether the
// child is still running.
func (f *floatView) setBorders() {
	title := " " + f.label + " "
	if f.exited {
		title = " " + f.label + " · exited "
	}
	f.float.Title = cells.Clip(title, f.float.Rect.W-2)
	if f.exited {
		f.float.Hint = " exited · any key closes "
		return
	}
	f.float.Hint = " " + f.prefix + " kills the float · keys go to the command "
}

// applyFloatBytes paints the child's output into the float's own terminal. The
// terminal is not in t.vts, so the snapshot's prune loop never reaches it.
func (t *tui) applyFloatBytes(m wire.Msg) bool {
	f := t.float
	if f == nil || f.closed || m.Target != f.id {
		return false
	}
	f.vt.Write(m.Data)
	// The last grapheme of a read stays pending until the stream goes quiet,
	// the way a pane's does, so a character split across two reads lands whole.
	t.noteBytes(f.id, time.Now())
	t.dirty = true
	return true
}

// floatErrorSwallowed reports whether an error reply names the float the client
// holds or the one it just closed. A key or a resize that raced the child's
// exit is not news: the exit frame already said the command is gone, and the
// reply must not surface a float id as a pane toast.
func (t *tui) floatErrorSwallowed(err string) bool {
	if err == "" {
		return false
	}
	if t.float != nil && strings.Contains(err, t.float.id) {
		return true
	}
	return t.floatGone != "" && strings.Contains(err, t.floatGone)
}

// floatExited keeps the last screen and says the command is gone. The next key
// closes the float and goes nowhere.
func (t *tui) floatExited() {
	f := t.float
	if f == nil || f.exited {
		return
	}
	f.exited = true
	f.setBorders()
	t.dirty = true
}

// closeFloat drops the float and gives the keys back to the panes. A child that
// already exited is not closed again: the server dropped it with the exit.
func (t *tui) closeFloat() {
	f := t.float
	if f == nil {
		return
	}
	f.closed = true
	t.floatGone = f.id
	t.RemoveFloatingPane(f.float.ID)
	if t.conn != nil && !f.exited {
		_ = t.conn.Send(wire.Msg{Type: "req", Action: "float-close", Float: f.id})
	}
	t.float = nil
	t.mode = ""
	// The close key stays in the buffer while the pending timer waits. Drop
	// the whole buffer here, so a later keypress cannot forward a stray escape
	// to a pane.
	t.inbuf = nil
	t.pending = false
	t.dirty = true
}

// syncFloat fits the float to the screen. A changed rect resizes the terminal
// and tells the server, so the command reflows to the box the client draws.
func (t *tui) syncFloat(snap *wire.Snapshot) {
	f := t.float
	if f == nil {
		return
	}
	r := floatRect(snap)
	if f.float.Rect == r {
		return
	}
	f.float.Rect = r
	resizeTerm(f.vt, r.W, r.H)
	f.setBorders()
	if t.conn != nil && !f.exited {
		_ = t.conn.Send(wire.Msg{Type: "resize", Target: f.id, Cols: r.W, Rows: r.H})
	}
	t.dirty = true
}

// floatKey sends one read's worth of bytes to the command. The prefix is the
// client's and it is not a chord head here: one press kills the float and the
// command with it, so the command never sees that byte. Every other read goes
// to the command whole. It never asks for more bytes, so it always returns
// true.
func (t *tui) floatKey() bool {
	f := t.float
	if f == nil {
		return true
	}
	if f.exited {
		// The command is gone. The last screen stays and any key closes it.
		t.inbuf = nil
		t.pending = false
		t.closeFloat()
		return true
	}
	if !f.opened {
		// The open reply has not landed. Drop the read: nothing exists yet to
		// take it, and holding it would only delay a key the operator typed
		// against a screen that is not there.
		t.inbuf = nil
		t.pending = false
		return true
	}
	prefix := t.km().PrefixBytes()
	if len(prefix) > 0 && bytes.HasPrefix(t.inbuf, prefix) {
		// The prefix kills the float and the command under it in one press, so
		// there is no need to quit the program first. The command never sees
		// the prefix byte: the dashboard, the message box and the float agree
		// that the prefix is the chrome's, not the program's. A program that
		// needs that byte needs a different prefix.
		t.inbuf = t.inbuf[:0]
		t.pending = false
		t.closeFloat()
		return true
	}
	keys := append([]byte(nil), t.inbuf...)
	t.inbuf = t.inbuf[:0]
	t.pending = false
	if t.conn != nil {
		_ = t.conn.Send(wire.Msg{Type: "keys", Target: f.id, Keys: keys})
	}
	return true
}
